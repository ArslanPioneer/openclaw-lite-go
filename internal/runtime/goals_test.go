package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/telegram"
)

func TestCreateGoalFromTelegramMessage(t *testing.T) {
	cfg := config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
		Runtime: config.RuntimeConfig{
			DataDir:           t.TempDir(),
			CodexProxyURL:     "http://127.0.0.1:8099/chat",
			CodexFirstDefault: true,
		},
	}

	svc := NewService(cfg, &fakeBot{}, &fakeAgent{reply: "should-not-be-called"})
	svc.SetCodexProxy(&fakeCodexProxy{reply: "deployment looks healthy"})
	if svc.runner == nil {
		t.Fatal("expected default goal runner to be attached")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.runner.Run(ctx)

	if err := svc.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: 55},
			Text: "check the deployment health",
		},
	}); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	session, err := svc.sessions.Load(55)
	if err != nil {
		t.Fatalf("sessions.Load() error = %v", err)
	}
	if strings.TrimSpace(session.ActiveGoalID) == "" {
		t.Fatal("expected active goal id to be persisted")
	}

	store := NewGoalStore(cfg.Runtime.DataDir)
	goal, err := store.Load(55, session.ActiveGoalID)
	if err != nil {
		t.Fatalf("goals.Load() error = %v", err)
	}

	if goal.ChatID != 55 {
		t.Fatalf("goal.ChatID = %d, want 55", goal.ChatID)
	}
	if goal.Objective != "check the deployment health" {
		t.Fatalf("goal.Objective = %q", goal.Objective)
	}
	var loadErr error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		goal, loadErr = store.Load(55, session.ActiveGoalID)
		if loadErr == nil && goal.Status == GoalStatusDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if goal.Status != GoalStatusDone {
		t.Fatalf("goal.Status = %q, want %q", goal.Status, GoalStatusDone)
	}
	if !strings.Contains(goal.LatestSummary, "deployment looks healthy") {
		t.Fatalf("goal.LatestSummary = %q", goal.LatestSummary)
	}
}

func TestGoalStatusTransitionsQueuedRunningBlockedDone(t *testing.T) {
	goal := NewGoal(42, "deploy the service")
	if goal.Status != GoalStatusQueued {
		t.Fatalf("initial status = %q, want %q", goal.Status, GoalStatusQueued)
	}

	goal = ApplyGoalResult(goal, GoalResult{Running: true, Summary: "starting deployment"})
	if goal.Status != GoalStatusRunning {
		t.Fatalf("running status = %q, want %q", goal.Status, GoalStatusRunning)
	}

	goal = ApplyGoalResult(goal, GoalResult{Err: errors.New("waiting for production credentials")})
	if goal.Status != GoalStatusBlocked {
		t.Fatalf("blocked status = %q, want %q", goal.Status, GoalStatusBlocked)
	}
	if !strings.Contains(goal.LastError, "credentials") {
		t.Fatalf("goal.LastError = %q", goal.LastError)
	}

	goal = ApplyGoalResult(goal, GoalResult{Done: true, Summary: "deployment completed"})
	if goal.Status != GoalStatusDone {
		t.Fatalf("done status = %q, want %q", goal.Status, GoalStatusDone)
	}
	if goal.LastError != "" {
		t.Fatalf("goal.LastError = %q, want empty", goal.LastError)
	}
}
