package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type fakeGoalExecutor struct {
	steps []GoalStep
	err   error
	calls int
}

func (f *fakeGoalExecutor) ExecuteGoalStep(_ context.Context, goal Goal) (GoalStep, error) {
	f.calls++
	if f.err != nil {
		return GoalStep{}, f.err
	}
	if len(f.steps) == 0 {
		return GoalStep{Status: GoalStatusDone, Message: goal.Objective}, nil
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	return step, nil
}

func TestGoalRunnerCanContinueUntilObjectiveReached(t *testing.T) {
	dataDir := t.TempDir()
	goals := NewGoalStore(dataDir)
	sessions := NewSessionStore(dataDir)
	goal := NewGoal(42, "finish the deploy")
	if err := goals.Save(goal); err != nil {
		t.Fatalf("goals.Save() error = %v", err)
	}
	if err := sessions.Save(42, SessionState{ActiveGoalID: goal.ID}); err != nil {
		t.Fatalf("sessions.Save() error = %v", err)
	}

	exec := &fakeGoalExecutor{
		steps: []GoalStep{
			{Status: GoalStatusRunning, Message: "step 1"},
			{Status: GoalStatusRunning, Message: "step 2"},
			{Status: GoalStatusDone, Message: "deploy completed"},
		},
	}
	bot := &fakeBot{}
	health := NewHealthState()
	runner := NewGoalRunner(exec, goals, sessions, bot, health)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)

	if err := runner.Enqueue(42, goal.ID); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	waitForGoalStatus(t, goals, 42, goal.ID, GoalStatusDone)

	reloaded, err := goals.Load(42, goal.ID)
	if err != nil {
		t.Fatalf("goals.Load() error = %v", err)
	}
	if reloaded.LatestSummary != "deploy completed" {
		t.Fatalf("LatestSummary = %q", reloaded.LatestSummary)
	}
	if exec.calls != 3 {
		t.Fatalf("executor calls = %d, want 3", exec.calls)
	}
}

func TestGoalRunnerSurfacesBlockedStateToTelegram(t *testing.T) {
	dataDir := t.TempDir()
	goals := NewGoalStore(dataDir)
	sessions := NewSessionStore(dataDir)
	goal := NewGoal(77, "repair the service")
	if err := goals.Save(goal); err != nil {
		t.Fatalf("goals.Save() error = %v", err)
	}
	if err := sessions.Save(77, SessionState{ActiveGoalID: goal.ID}); err != nil {
		t.Fatalf("sessions.Save() error = %v", err)
	}

	exec := &fakeGoalExecutor{err: fmt.Errorf("waiting for production credentials")}
	bot := &fakeBot{}
	runner := NewGoalRunner(exec, goals, sessions, bot, NewHealthState())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)

	if err := runner.Enqueue(77, goal.ID); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	waitForGoalStatus(t, goals, 77, goal.ID, GoalStatusBlocked)

	if len(bot.sent) == 0 {
		t.Fatal("expected blocked state to be surfaced to telegram")
	}
	if got := bot.sent[len(bot.sent)-1].text; got == "" || got == "repair the service" {
		t.Fatalf("unexpected telegram message: %q", got)
	}
}

func waitForGoalStatus(t *testing.T, store *GoalStore, chatID int64, goalID string, want GoalStatus) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		goal, err := store.Load(chatID, goalID)
		if err == nil && goal.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	goal, err := store.Load(chatID, goalID)
	if err != nil {
		t.Fatalf("goals.Load() error = %v", err)
	}
	t.Fatalf("goal status = %q, want %q", goal.Status, want)
}
