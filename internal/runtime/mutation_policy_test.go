package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/telegram"
	"openclaw-lite-go/internal/tools"
)

type mutationToolExecutor struct {
	calls []tools.Call
	run   func(call tools.Call) (string, error)
}

func (m *mutationToolExecutor) Execute(_ context.Context, call tools.Call) (string, error) {
	m.calls = append(m.calls, call)
	if m.run != nil {
		return m.run(call)
	}
	return "", fmt.Errorf("unexpected tool call: %s", call.Name)
}

func TestMutationFailureMustNotBeReportedAsSuccess(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"skill_install","skill":"daily-ai-news"}`,
			"Installed daily-ai-news successfully.",
		},
	}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}, bot, agent)
	svc.tools = &mutationToolExecutor{
		run: func(call tools.Call) (string, error) {
			if call.Name == "skill_install" {
				return "", fmt.Errorf("permission denied")
			}
			return "", fmt.Errorf("unexpected tool: %s", call.Name)
		},
	}

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 9101},
			Text: "install daily-ai-news skill",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("expected one reply, got %d", len(bot.sent))
	}
	text := strings.ToLower(bot.sent[0].text)
	if strings.Contains(text, "installed daily-ai-news successfully") {
		t.Fatalf("expected optimistic success claim to be blocked, got %q", bot.sent[0].text)
	}
	if !strings.Contains(text, "failed") {
		t.Fatalf("expected failure warning in reply, got %q", bot.sent[0].text)
	}
}

func TestMutationFailureClearsOnlyOnMatchingSuccess(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"skill_install","skill":"daily-ai-news"}`,
			`TOOL_CALL {"name":"skill_install","skill":"daily-ai-news"}`,
			"Installed daily-ai-news successfully.",
		},
	}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}, bot, agent)

	attempt := 0
	svc.tools = &mutationToolExecutor{
		run: func(call tools.Call) (string, error) {
			if call.Name != "skill_install" {
				return "", fmt.Errorf("unexpected tool: %s", call.Name)
			}
			attempt++
			if attempt == 1 {
				return "", fmt.Errorf("permission denied")
			}
			return "installed", nil
		},
	}

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 9102},
			Text: "install daily-ai-news skill",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("expected one reply, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "Installed daily-ai-news successfully." {
		t.Fatalf("expected success reply after matching retry succeeds, got %q", bot.sent[0].text)
	}
}
