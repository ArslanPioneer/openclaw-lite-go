package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/telegram"
	"openclaw-lite-go/internal/tools"
)

type fakeBot struct {
	sent []sentMsg
}

type sentMsg struct {
	chatID int64
	text   string
}

func (f *fakeBot) GetUpdates(_ context.Context, _ int64, _ int) ([]telegram.Update, error) {
	return nil, nil
}

func (f *fakeBot) SendMessage(_ context.Context, chatID int64, text string) error {
	f.sent = append(f.sent, sentMsg{chatID: chatID, text: text})
	return nil
}

type fakeAgent struct {
	calls []agentCall
	reply string
}

type fakeCodexProxy struct {
	calls []codexProxyCall
	reply string
	err   error
}

type codexProxyCall struct {
	chatID  int64
	message string
}

type agentCall struct {
	text  string
	model string
}

type fakeToolExecutor struct {
	calls []string
	run   func(callName string, query string) (string, error)
}

func (f *fakeToolExecutor) Execute(_ context.Context, call tools.Call) (string, error) {
	f.calls = append(f.calls, call.Name+":"+call.Query)
	if f.run != nil {
		return f.run(call.Name, call.Query)
	}
	return "", fmt.Errorf("unexpected tool call")
}

func (f *fakeAgent) GenerateReply(_ context.Context, userText string, modelOverride string) (string, error) {
	f.calls = append(f.calls, agentCall{text: userText, model: modelOverride})
	if f.reply == "" {
		return "default-reply", nil
	}
	return f.reply, nil
}

func (f *fakeCodexProxy) Chat(_ context.Context, chatID int64, message string) (string, error) {
	f.calls = append(f.calls, codexProxyCall{
		chatID:  chatID,
		message: message,
	})
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

func TestHandleUpdateRoutesTextThroughAgentAndSendsReply(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "hello from agent"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "hi",
		},
	}

	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	if len(agent.calls) != 1 {
		t.Fatalf("expected 1 agent call, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "hello from agent" {
		t.Fatalf("unexpected sent text: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateAgentCommandSwitchesModelPerChat(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "ok"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	setAgent := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/agent gpt-4.1-mini",
		},
	}
	if err := svc.HandleUpdate(context.Background(), setAgent); err != nil {
		t.Fatalf("HandleUpdate(/agent) error = %v", err)
	}

	chat := telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "what model?",
		},
	}
	if err := svc.HandleUpdate(context.Background(), chat); err != nil {
		t.Fatalf("HandleUpdate(chat) error = %v", err)
	}

	if len(agent.calls) != 1 {
		t.Fatalf("expected 1 agent call, got %d", len(agent.calls))
	}
	if agent.calls[0].model != "gpt-4.1-mini" {
		t.Fatalf("expected model override gpt-4.1-mini, got %q", agent.calls[0].model)
	}
}

func TestHandleUpdateCodexCommandSwitchesModelPerChat(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "ok"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	setCodex := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/codex",
		},
	}
	if err := svc.HandleUpdate(context.Background(), setCodex); err != nil {
		t.Fatalf("HandleUpdate(/codex) error = %v", err)
	}

	chat := telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "hello codex",
		},
	}
	if err := svc.HandleUpdate(context.Background(), chat); err != nil {
		t.Fatalf("HandleUpdate(chat) error = %v", err)
	}

	if len(agent.calls) != 1 {
		t.Fatalf("expected 1 agent call, got %d", len(agent.calls))
	}
	if agent.calls[0].model != "gpt-5-codex" {
		t.Fatalf("expected model override gpt-5-codex, got %q", agent.calls[0].model)
	}
}

func TestHandleUpdateCodexCLIProxyModeRoutesMessagesToProxy(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	proxy := &fakeCodexProxy{reply: "codex proxy reply"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			CodexProxyURL: "http://127.0.0.1:8099/chat",
		},
	}, bot, agent)
	svc.SetCodexProxy(proxy)
	if svc.runner == nil {
		t.Fatal("expected default goal runner to be attached")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.runner.Run(ctx)

	enable := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 7},
			Text: "/codexcli on",
		},
	}
	if err := svc.HandleUpdate(context.Background(), enable); err != nil {
		t.Fatalf("HandleUpdate(/codexcli on) error = %v", err)
	}

	chat := telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 7},
			Text: "please refactor this function",
		},
	}
	if err := svc.HandleUpdate(context.Background(), chat); err != nil {
		t.Fatalf("HandleUpdate(chat) error = %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(proxy.calls) >= 1 && len(bot.sent) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(proxy.calls) != 1 {
		t.Fatalf("expected 1 codex proxy call, got %d", len(proxy.calls))
	}
	if !strings.Contains(proxy.calls[0].message, "please refactor this function") {
		t.Fatalf("unexpected proxy message: %q", proxy.calls[0].message)
	}
	if !strings.HasPrefix(proxy.calls[0].message, "[goal:") {
		t.Fatalf("expected goal marker in proxy message, got %q", proxy.calls[0].message)
	}
	if len(agent.calls) != 0 {
		t.Fatalf("expected agent to be bypassed, got %d calls", len(agent.calls))
	}
	if len(bot.sent) < 3 || !strings.Contains(strings.ToLower(bot.sent[1].text), "queued") {
		t.Fatalf("expected queued ack before completion, got %+v", bot.sent)
	}
	if got := bot.sent[len(bot.sent)-1].text; got != "codex proxy reply" {
		t.Fatalf("unexpected bot reply: %q", got)
	}
}

func TestHandleUpdateCodexCLIOnRequiresConfiguredProxy(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "normal-agent-reply"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	enable := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 8},
			Text: "/codexcli on",
		},
	}
	if err := svc.HandleUpdate(context.Background(), enable); err != nil {
		t.Fatalf("HandleUpdate(/codexcli on) error = %v", err)
	}

	if len(bot.sent) != 1 || !strings.Contains(strings.ToLower(bot.sent[0].text), "not configured") {
		t.Fatalf("expected not configured message, got %+v", bot.sent)
	}

	chat := telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 8},
			Text: "hello",
		},
	}
	if err := svc.HandleUpdate(context.Background(), chat); err != nil {
		t.Fatalf("HandleUpdate(chat) error = %v", err)
	}
	if len(agent.calls) != 1 {
		t.Fatalf("expected normal agent call, got %d", len(agent.calls))
	}
}

func TestHandleUpdateCodexFirstRoutesNormalChatToCodexProxyByDefault(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	proxy := &fakeCodexProxy{reply: "codex default reply"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			CodexProxyURL:      "http://127.0.0.1:8099/chat",
			CodexFirstDefault:  true,
		},
	}, bot, agent)
	svc.SetCodexProxy(proxy)
	if svc.runner == nil {
		t.Fatal("expected default goal runner to be attached")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.runner.Run(ctx)

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 9},
			Text: "check the deployment health",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(proxy.calls) >= 1 && len(bot.sent) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(proxy.calls) != 1 {
		t.Fatalf("expected 1 codex proxy call, got %d", len(proxy.calls))
	}
	if len(agent.calls) != 0 {
		t.Fatalf("expected agent to be bypassed, got %d calls", len(agent.calls))
	}
	if !strings.Contains(strings.ToLower(bot.sent[0].text), "queued") {
		t.Fatalf("expected queued acknowledgement, got %q", bot.sent[0].text)
	}
	if got := bot.sent[len(bot.sent)-1].text; got != "codex default reply" {
		t.Fatalf("unexpected bot reply: %q", got)
	}
}

func TestHandleUpdateCanFallbackToLegacyAgentMode(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "legacy agent reply"}
	proxy := &fakeCodexProxy{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			CodexProxyURL:     "http://127.0.0.1:8099/chat",
			CodexFirstDefault: true,
		},
	}, bot, agent)
	svc.SetCodexProxy(proxy)

	switchMode := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 10},
			Text: "/agentmode legacy",
		},
	}
	if err := svc.HandleUpdate(context.Background(), switchMode); err != nil {
		t.Fatalf("HandleUpdate(/agentmode legacy) error = %v", err)
	}

	update := telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 10},
			Text: "say hello",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate(chat) error = %v", err)
	}

	if len(agent.calls) != 1 {
		t.Fatalf("expected 1 agent call, got %d", len(agent.calls))
	}
	if len(proxy.calls) != 0 {
		t.Fatalf("expected codex proxy to be bypassed, got %d calls", len(proxy.calls))
	}
	if got := bot.sent[len(bot.sent)-1].text; got != "legacy agent reply" {
		t.Fatalf("unexpected bot reply: %q", got)
	}
}

func TestLegacyAgentPathOnlyRunsWhenExplicitlyRequested(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			CodexFirstDefault: true,
		},
	}, bot, agent)

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 11},
			Text: "inspect the deployment",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 0 {
		t.Fatalf("expected legacy agent to stay idle, got %d calls", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(bot.sent))
	}
	if !strings.Contains(strings.ToLower(bot.sent[0].text), "/agentmode legacy") {
		t.Fatalf("expected explicit fallback instruction, got %q", bot.sent[0].text)
	}
}

func TestHandleUpdateCodexFirstEnqueuesGoalRunnerInsteadOfRunningSynchronously(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	proxy := &fakeCodexProxy{reply: "proxy completed"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			CodexProxyURL:     "http://127.0.0.1:8099/chat",
			CodexFirstDefault: true,
		},
	}, bot, agent)
	svc.SetCodexProxy(proxy)

	if svc.runner == nil {
		t.Fatal("expected default goal runner to be attached")
	}

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 66},
			Text: "inspect the deployment health",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 0 {
		t.Fatalf("expected legacy agent to stay idle, got %d calls", len(agent.calls))
	}
	if len(proxy.calls) != 0 {
		t.Fatalf("expected codex proxy not to run synchronously, got %d calls", len(proxy.calls))
	}
	if got := len(svc.runner.queue); got != 1 {
		t.Fatalf("expected queued goal count = 1, got %d", got)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 acknowledgement message, got %d", len(bot.sent))
	}
	if !strings.Contains(strings.ToLower(bot.sent[0].text), "queued") {
		t.Fatalf("expected queued acknowledgement, got %q", bot.sent[0].text)
	}
}

func TestHandleUpdateGoalRunnerCompletionUpdatesTelegramAndGoalState(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	proxy := &fakeCodexProxy{reply: "codex goal completed"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			CodexProxyURL:     "http://127.0.0.1:8099/chat",
			CodexFirstDefault: true,
			DataDir:           t.TempDir(),
		},
	}, bot, agent)
	svc.SetCodexProxy(proxy)

	if svc.runner == nil {
		t.Fatal("expected default goal runner to be attached")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.runner.Run(ctx)

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 67},
			Text: "perform codex-first health check",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(proxy.calls) >= 1 && len(bot.sent) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(proxy.calls) != 1 {
		t.Fatalf("expected 1 proxy call from runner, got %d", len(proxy.calls))
	}
	if len(bot.sent) < 2 {
		t.Fatalf("expected queued + completion messages, got %d", len(bot.sent))
	}
	if !strings.Contains(strings.ToLower(bot.sent[0].text), "queued") {
		t.Fatalf("expected first message to be queued ack, got %q", bot.sent[0].text)
	}
	if bot.sent[len(bot.sent)-1].text != "codex goal completed" {
		t.Fatalf("expected completion message, got %q", bot.sent[len(bot.sent)-1].text)
	}

	state, err := svc.sessions.Load(67)
	if err != nil {
		t.Fatalf("sessions.Load() error = %v", err)
	}
	if strings.TrimSpace(state.ActiveGoalID) == "" {
		t.Fatal("expected active goal id to be persisted")
	}
	goal, err := svc.goals.Load(67, state.ActiveGoalID)
	if err != nil {
		t.Fatalf("goals.Load() error = %v", err)
	}
	if goal.Status != GoalStatusDone {
		t.Fatalf("goal status = %q, want %q", goal.Status, GoalStatusDone)
	}
	if !strings.Contains(goal.LatestSummary, "codex goal completed") {
		t.Fatalf("goal summary = %q", goal.LatestSummary)
	}
}

func TestHandleUpdateCodexProxyRequestCarriesGoalMarker(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	proxy := &fakeCodexProxy{reply: "done"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			CodexProxyURL:     "http://127.0.0.1:8099/chat",
			CodexFirstDefault: true,
			DataDir:           t.TempDir(),
		},
	}, bot, agent)
	svc.SetCodexProxy(proxy)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.runner.Run(ctx)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 88},
			Text: "inspect host status",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(proxy.calls) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(proxy.calls) != 1 {
		t.Fatalf("expected 1 proxy call, got %d", len(proxy.calls))
	}

	session, err := svc.sessions.Load(88)
	if err != nil {
		t.Fatalf("sessions.Load() error = %v", err)
	}
	if strings.TrimSpace(session.ActiveGoalID) == "" {
		t.Fatal("expected active goal id")
	}

	wantPrefix := "[goal:" + session.ActiveGoalID + "] "
	if !strings.HasPrefix(proxy.calls[0].message, wantPrefix) {
		t.Fatalf("expected proxy message prefix %q, got %q", wantPrefix, proxy.calls[0].message)
	}
	if !strings.Contains(proxy.calls[0].message, "inspect host status") {
		t.Fatalf("expected original objective in proxy message, got %q", proxy.calls[0].message)
	}
}

type panicAgent struct{}

func (p *panicAgent) GenerateReply(_ context.Context, _ string, _ string) (string, error) {
	panic("boom")
}

func TestProcessUpdateRecoversPanic(t *testing.T) {
	bot := &fakeBot{}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, &panicAgent{})

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "trigger panic",
		},
	}

	err := svc.ProcessUpdate(context.Background(), update)
	if err == nil {
		t.Fatal("expected panic to be recovered as error")
	}
}

func TestHandleUpdateSkillsCommandListsAvailableSkills(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "skills-source")
	install := filepath.Join(tmp, "skills-install")
	newsDir := filepath.Join(source, "daily-ai-news")
	if err := os.MkdirAll(newsDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newsDir, "SKILL.md"), []byte("daily-ai-news"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			SkillsSourceDir:  source,
			SkillsInstallDir: install,
		},
	}, bot, agent)

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/skills",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate(/skills) error = %v", err)
	}
	if len(agent.calls) != 0 {
		t.Fatalf("expected 0 agent calls for /skills, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "daily-ai-news") {
		t.Fatalf("unexpected /skills output: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateSkillsCommandSyncInstallsSkills(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "skills-source")
	install := filepath.Join(tmp, "skills-install")

	for _, name := range []string{"daily-ai-news", "news-aggregator-skill"} {
		dir := filepath.Join(source, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s SKILL.md: %v", name, err)
		}
	}

	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			SkillsSourceDir:  source,
			SkillsInstallDir: install,
		},
	}, bot, agent)

	syncUpdate := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/skills sync",
		},
	}
	if err := svc.HandleUpdate(context.Background(), syncUpdate); err != nil {
		t.Fatalf("HandleUpdate(/skills sync) error = %v", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message after sync, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "Installed 2 skills") {
		t.Fatalf("unexpected /skills sync output: %q", bot.sent[0].text)
	}

	installedUpdate := telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/skills installed",
		},
	}
	if err := svc.HandleUpdate(context.Background(), installedUpdate); err != nil {
		t.Fatalf("HandleUpdate(/skills installed) error = %v", err)
	}
	if len(bot.sent) != 2 {
		t.Fatalf("expected 2 sent messages after installed query, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[1].text, "daily-ai-news") || !strings.Contains(bot.sent[1].text, "news-aggregator-skill") {
		t.Fatalf("unexpected /skills installed output: %q", bot.sent[1].text)
	}
}

func TestHandleUpdatePriceCommandUsage(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/price",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate(/price) error = %v", err)
	}
	if len(agent.calls) != 0 {
		t.Fatalf("expected 0 agent calls for /price usage, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "Usage: /price <ticker>") {
		t.Fatalf("unexpected /price usage output: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateVersionCommand(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	originalVersion := AppVersion
	originalCommit := AppCommit
	t.Cleanup(func() {
		AppVersion = originalVersion
		AppCommit = originalCommit
	})
	AppVersion = "v9.9.9"
	AppCommit = "deadbee"

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/version",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate(/version) error = %v", err)
	}
	if len(agent.calls) != 0 {
		t.Fatalf("expected 0 agent calls for /version, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "ClawLite version: v9.9.9 (deadbee)" {
		t.Fatalf("unexpected /version output: %q", bot.sent[0].text)
	}
}

func TestExtractTickerFromStockQuery(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "nvda股价", want: "NVDA", ok: true},
		{in: "what is NVDA stock price now", want: "NVDA", ok: true},
		{in: "Tell me $TSLA price", want: "TSLA", ok: true},
		{in: "just chatting about nvidia", want: "", ok: false},
	}

	for _, tc := range tests {
		got, ok := extractTickerFromStockQuery(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("extractTickerFromStockQuery(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestHandleUpdateStockQueryFallsBackToWebSearch(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	toolsExec := &fakeToolExecutor{
		run: func(callName string, query string) (string, error) {
			switch callName {
			case "stock_price":
				return "", fmt.Errorf("stock source unavailable")
			case "web_search":
				if !strings.Contains(strings.ToLower(query), "nvda") {
					t.Fatalf("unexpected web_search query: %q", query)
				}
				return "NVDA latest: 177.19 USD (from web_search)", nil
			default:
				return "", fmt.Errorf("unsupported tool call: %s", callName)
			}
		},
	}
	svc.tools = toolsExec

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "nvda股价",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 0 {
		t.Fatalf("expected agent not to be called, got %d calls", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "NVDA latest") {
		t.Fatalf("unexpected sent message: %q", bot.sent[0].text)
	}
	if len(toolsExec.calls) != 2 || toolsExec.calls[0] != "stock_price:NVDA" || !strings.HasPrefix(toolsExec.calls[1], "web_search:") {
		t.Fatalf("unexpected tool call sequence: %#v", toolsExec.calls)
	}
}

func TestHandleUpdatePriceCommandFallsBackToWebSearch(t *testing.T) {
	bot := &fakeBot{}
	agent := &fakeAgent{reply: "should-not-be-called"}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, bot, agent)

	toolsExec := &fakeToolExecutor{
		run: func(callName string, query string) (string, error) {
			switch callName {
			case "stock_price":
				return "", fmt.Errorf("rate limited")
			case "web_search":
				return "NVDA web quote fallback", nil
			default:
				return "", fmt.Errorf("unsupported tool call: %s", callName)
			}
		},
	}
	svc.tools = toolsExec

	update := telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/price NVDA",
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 0 {
		t.Fatalf("expected agent not to be called, got %d calls", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "NVDA web quote fallback" {
		t.Fatalf("unexpected sent message: %q", bot.sent[0].text)
	}
}
