package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/telegram"
)

type sequenceAgent struct {
	replies []string
	errs    []error
	calls   []agentCall
}

func (s *sequenceAgent) GenerateReply(_ context.Context, userText string, modelOverride string) (string, error) {
	s.calls = append(s.calls, agentCall{text: userText, model: modelOverride})

	index := len(s.calls) - 1
	if index < len(s.errs) && s.errs[index] != nil {
		return "", s.errs[index]
	}
	if index < len(s.replies) {
		return s.replies[index], nil
	}
	return "fallback", nil
}

func TestHandleUpdateInjectsHistoryIntoPrompt(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{"hello", "you said hello"},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 101},
			Text: "hello",
		},
	}); err != nil {
		t.Fatalf("first HandleUpdate() error = %v", err)
	}

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 101},
			Text: "what did i say",
		},
	}); err != nil {
		t.Fatalf("second HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 2 {
		t.Fatalf("expected 2 agent calls, got %d", len(agent.calls))
	}
	secondPrompt := agent.calls[1].text
	if !strings.Contains(secondPrompt, "Recent conversation") {
		t.Fatalf("expected prompt to include history marker, got: %q", secondPrompt)
	}
	if !strings.Contains(secondPrompt, "User: hello") {
		t.Fatalf("expected prompt to include previous user message, got: %q", secondPrompt)
	}
}

func TestHandleUpdateToolCallExecutesThenReturnsFinalAnswer(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"echo","text":"hello tool"}`,
			"final answer from tool result",
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 202},
			Text: "use a tool",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 2 {
		t.Fatalf("expected 2 agent calls, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "final answer from tool result" {
		t.Fatalf("unexpected bot response: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateRetriesAgentFailure(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		errs:    []error{errors.New("temporary error"), nil},
		replies: []string{"", "recovered reply"},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:         tmp,
			HistoryTurns:    8,
			AgentRetryCount: 2,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 303},
			Text: "retry please",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 2 {
		t.Fatalf("expected 2 agent calls due to retry, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "recovered reply" {
		t.Fatalf("unexpected reply: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateSupportsMultiStepToolExecution(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"echo","text":"first-tool-result"}`,
			`TOOL_CALL {"name":"echo","text":"second-tool-result"}`,
			"final answer after two tools",
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 404},
			Text: "do multi-step tool work",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 3 {
		t.Fatalf("expected 3 agent calls for multi-step tool execution, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "final answer after two tools" {
		t.Fatalf("unexpected final response: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateStopsAfterToolCallLimit(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"echo","text":"s1"}`,
			`TOOL_CALL {"name":"echo","text":"s2"}`,
			`TOOL_CALL {"name":"echo","text":"s3"}`,
			`TOOL_CALL {"name":"echo","text":"s4"}`,
			`TOOL_CALL {"name":"echo","text":"still-tool"}`,
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 505},
			Text: "keep calling tools",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 5 {
		t.Fatalf("expected 5 agent calls (4 loop + 1 forced final), got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "tool-call limit") {
		t.Fatalf("expected tool-call limit message, got %q", bot.sent[0].text)
	}
}

func TestHandleUpdateRecoversFromMalformedToolCall(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"web_search","query":"NVDA"`,
			"NVDA latest price is unavailable in this mock, but recovery worked.",
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)
	svc.tools = &fakeToolExecutor{
		run: func(callName string, query string) (string, error) {
			t.Fatalf("unexpected tool call during malformed tool-call recovery: %s %s", callName, query)
			return "", nil
		},
	}

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 606},
			Text: "research nvidia",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 2 {
		t.Fatalf("expected 2 agent calls (recover after malformed tool call), got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "recovery worked") {
		t.Fatalf("unexpected bot response: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateParsesFencedToolCall(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			"```text\nTOOL_CALL {\"name\":\"echo\",\"text\":\"hello-from-fence\"}\n```",
			"fenced tool call succeeded",
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 818},
			Text: "use a fenced tool call",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 2 {
		t.Fatalf("expected 2 agent calls, got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if bot.sent[0].text != "fenced tool call succeeded" {
		t.Fatalf("unexpected bot response: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateBreaksOnRepeatedSameToolError(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"stock_price","query":"???"}`,
			`TOOL_CALL {"name":"stock_price","query":"???"}`,
			"Please provide a valid ticker like NVDA or AAPL.",
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 819},
			Text: "stock ???",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 3 {
		t.Fatalf("expected 3 agent calls, got %d", len(agent.calls))
	}
	if !strings.Contains(agent.calls[2].text, "Repeated tool execution errors detected") {
		t.Fatalf("expected forced prompt to mention repeated tool errors, got: %q", agent.calls[2].text)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if !strings.Contains(bot.sent[0].text, "valid ticker") {
		t.Fatalf("unexpected bot response: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateBreaksOnRepeatedMalformedToolCall(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			`TOOL_CALL {"name":"web_search","query":"NVDA"`,
			`TOOL_CALL {"name":"web_search","query":"NVDA"`,
			"I can proceed if you provide one valid tool request or ask directly.",
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 820},
			Text: "broken tool format",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 3 {
		t.Fatalf("expected 3 agent calls, got %d", len(agent.calls))
	}
	if !strings.Contains(agent.calls[2].text, "Repeated invalid tool-call formatting detected") {
		t.Fatalf("expected forced prompt to mention repeated parse failures, got: %q", agent.calls[2].text)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if strings.Contains(strings.ToLower(bot.sent[0].text), "parse tool call") {
		t.Fatalf("unexpected raw parse error in final response: %q", bot.sent[0].text)
	}
}

func TestHandleUpdateMasksInternalErrorDetails(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		errs: []error{errors.New("upstream timeout"), errors.New("upstream timeout")},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:         tmp,
			HistoryTurns:    8,
			AgentRetryCount: 1,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 707},
			Text: "hard question",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if strings.Contains(strings.ToLower(bot.sent[0].text), "upstream timeout") ||
		strings.Contains(strings.ToLower(bot.sent[0].text), "agent request failed") {
		t.Fatalf("expected masked user-facing error, got %q", bot.sent[0].text)
	}
}

func TestHandleUpdateReturnsFriendlyRateLimitWhenFallbackAlsoFails(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		errs: []error{
			errors.New("agent returned status 429: too many requests"),
			errors.New("agent returned status 429: too many requests"),
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:         tmp,
			HistoryTurns:    8,
			AgentRetryCount: 1,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 909},
			Text: "hard question that still fails",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	text := strings.ToLower(bot.sent[0].text)
	if !strings.Contains(text, "rate") {
		t.Fatalf("expected friendly rate-limit message, got %q", bot.sent[0].text)
	}
	if strings.Contains(text, "429") || strings.Contains(text, "too many requests") {
		t.Fatalf("expected raw error not to leak, got %q", bot.sent[0].text)
	}
}

func TestHandleUpdateRepairsNonActionableReply(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	agent := &sequenceAgent{
		replies: []string{
			"I understand your request. I cannot access real-time data without tools.",
			"NVDA latest quote: use /price NVDA for direct result.",
		},
	}
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}
	svc := NewService(cfg, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 808},
			Text: "能不能自主解决问题",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 2 {
		t.Fatalf("expected 2 agent calls (repair pass), got %d", len(agent.calls))
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected 1 bot response, got %d", len(bot.sent))
	}
	if strings.Contains(strings.ToLower(bot.sent[0].text), "cannot access") {
		t.Fatalf("expected repaired actionable answer, got %q", bot.sent[0].text)
	}
}
