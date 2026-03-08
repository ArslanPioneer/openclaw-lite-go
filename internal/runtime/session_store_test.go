package runtime

import (
	"context"
	"testing"
	"time"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/telegram"
)

func TestSessionStorePersistsCodexModeAcrossRestart(t *testing.T) {
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:           t.TempDir(),
			CodexProxyURL:     "http://127.0.0.1:8099/chat",
			CodexFirstDefault: false,
		},
	}

	firstBot := &fakeBot{}
	firstSvc := NewService(cfg, firstBot, &fakeAgent{reply: "legacy"})
	firstSvc.SetCodexProxy(&fakeCodexProxy{reply: "codex"})

	if err := firstSvc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "/agentmode codex",
		},
	}); err != nil {
		t.Fatalf("first HandleUpdate(/agentmode codex) error = %v", err)
	}

	restartedProxy := &fakeCodexProxy{reply: "codex after restart"}
	restartedAgent := &fakeAgent{reply: "legacy after restart"}
	restartedBot := &fakeBot{}
	restartedSvc := NewService(cfg, restartedBot, restartedAgent)
	restartedSvc.SetCodexProxy(restartedProxy)

	if err := restartedSvc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 42},
			Text: "continue in codex",
		},
	}); err != nil {
		t.Fatalf("restarted HandleUpdate(chat) error = %v", err)
	}

	if len(restartedProxy.calls) != 1 {
		t.Fatalf("expected codex proxy to be restored after restart, got %d calls", len(restartedProxy.calls))
	}
	if len(restartedAgent.calls) != 0 {
		t.Fatalf("expected legacy agent to stay idle after restart, got %d calls", len(restartedAgent.calls))
	}
}

func TestSessionStorePersistsGoalAndTaskMetadata(t *testing.T) {
	baseDir := t.TempDir()
	store := NewSessionStore(baseDir)
	wantTime := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)

	if err := store.Save(7, SessionState{
		ExecutionMode:          string(executionModeCodex),
		ActiveGoalID:           "goal-123",
		LastCodexResultSummary: "checked deployment and found nginx healthy",
		LastActivity:           wantTime,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded := NewSessionStore(baseDir)
	got, err := reloaded.Load(7)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.ExecutionMode != string(executionModeCodex) {
		t.Fatalf("ExecutionMode = %q, want %q", got.ExecutionMode, executionModeCodex)
	}
	if got.ActiveGoalID != "goal-123" {
		t.Fatalf("ActiveGoalID = %q, want goal-123", got.ActiveGoalID)
	}
	if got.LastCodexResultSummary != "checked deployment and found nginx healthy" {
		t.Fatalf("LastCodexResultSummary = %q", got.LastCodexResultSummary)
	}
	if !got.LastActivity.Equal(wantTime) {
		t.Fatalf("LastActivity = %s, want %s", got.LastActivity, wantTime)
	}
}
