package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/telegram"
)

func TestToolOutputOverflowTriggersTruncationAndRetry(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	huge := strings.Repeat("A", 9000)
	agent := &sequenceAgent{
		replies: []string{
			fmt.Sprintf(`TOOL_CALL {"name":"echo","text":"%s"}`, huge),
			"final answer after truncation",
		},
	}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 9201},
			Text: "summarize huge output",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 2 {
		t.Fatalf("expected 2 agent calls, got %d", len(agent.calls))
	}
	if !strings.Contains(agent.calls[1].text, "[truncated tool output") {
		t.Fatalf("expected second prompt to include truncation marker, got %q", agent.calls[1].text)
	}
}

func TestContextRecoveryStopsAfterMaxAttempts(t *testing.T) {
	tmp := t.TempDir()
	bot := &fakeBot{}
	huge := strings.Repeat("B", 9000)
	agent := &sequenceAgent{
		replies: []string{
			fmt.Sprintf(`TOOL_CALL {"name":"echo","text":"%s"}`, huge),
			fmt.Sprintf(`TOOL_CALL {"name":"echo","text":"%s"}`, huge),
			fmt.Sprintf(`TOOL_CALL {"name":"echo","text":"%s"}`, huge),
			"fallback after context recovery limit",
		},
	}
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:      tmp,
			HistoryTurns: 8,
		},
	}, bot, agent)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 9202},
			Text: "keep using huge tool output",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	if len(agent.calls) != 4 {
		t.Fatalf("expected 4 agent calls (3 tool loops + forced final), got %d", len(agent.calls))
	}
	if !strings.Contains(agent.calls[3].text, "Context overflow persisted after recovery attempts") {
		t.Fatalf("expected forced prompt to include overflow reason, got %q", agent.calls[3].text)
	}
}
