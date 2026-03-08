package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const goalStatusWaitingInput GoalStatus = "waiting_input"

type GoalStep struct {
	Status  GoalStatus
	Message string
}

type GoalStepExecutor interface {
	ExecuteGoalStep(ctx context.Context, goal Goal) (GoalStep, error)
}

type GoalRunner struct {
	exec     GoalStepExecutor
	goals    *GoalStore
	sessions *SessionStore
	bot      TelegramClient
	health   *HealthState

	queue chan queuedGoal
	once  sync.Once
}

type queuedGoal struct {
	chatID int64
	goalID string
}

func NewGoalRunner(exec GoalStepExecutor, goals *GoalStore, sessions *SessionStore, bot TelegramClient, health *HealthState) *GoalRunner {
	return &GoalRunner{
		exec:     exec,
		goals:    goals,
		sessions: sessions,
		bot:      bot,
		health:   health,
		queue:    make(chan queuedGoal, 32),
	}
}

func (r *GoalRunner) SetHealthState(health *HealthState) {
	r.health = health
}

func (r *GoalRunner) Run(ctx context.Context) {
	if r == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-r.queue:
			r.runGoal(ctx, item.chatID, item.goalID)
		}
	}
}

func (r *GoalRunner) Enqueue(chatID int64, goalID string) error {
	if r == nil {
		return fmt.Errorf("goal runner is not configured")
	}
	if strings.TrimSpace(goalID) == "" {
		return fmt.Errorf("goal id is required")
	}
	if r.health != nil {
		r.health.RecordGoalEnqueued()
	}
	r.queue <- queuedGoal{chatID: chatID, goalID: strings.TrimSpace(goalID)}
	return nil
}

func (r *GoalRunner) runGoal(ctx context.Context, chatID int64, goalID string) {
	if r.exec == nil || r.goals == nil {
		return
	}
	if r.health != nil {
		r.health.RecordGoalStarted()
		defer r.health.RecordGoalFinished()
	}

	goal, err := r.goals.Load(chatID, goalID)
	if err != nil {
		r.notify(chatID, "Goal execution failed: unable to load goal.")
		return
	}

	for steps := 0; steps < 8; steps++ {
		goal.Status = GoalStatusRunning
		goal.UpdatedAt = time.Now().UTC()
		_ = r.goals.Save(goal)

		step, err := r.exec.ExecuteGoalStep(ctx, goal)
		if err != nil {
			goal.Status = GoalStatusBlocked
			goal.LastError = strings.TrimSpace(err.Error())
			goal.UpdatedAt = time.Now().UTC()
			_ = r.goals.Save(goal)
			r.notify(chatID, "Goal blocked: "+goal.LastError)
			return
		}

		if strings.TrimSpace(step.Message) != "" {
			goal.LatestSummary = strings.TrimSpace(step.Message)
		}
		goal.LastError = ""
		switch step.Status {
		case GoalStatusDone:
			goal.Status = GoalStatusDone
			goal.UpdatedAt = time.Now().UTC()
			_ = r.goals.Save(goal)
			r.notify(chatID, goal.LatestSummary)
			return
		case GoalStatusBlocked:
			goal.Status = GoalStatusBlocked
			if goal.LatestSummary != "" {
				goal.LastError = goal.LatestSummary
			}
			goal.UpdatedAt = time.Now().UTC()
			_ = r.goals.Save(goal)
			r.notify(chatID, "Goal blocked: "+firstNonEmpty(goal.LastError, goal.LatestSummary))
			return
		case goalStatusWaitingInput:
			goal.Status = goalStatusWaitingInput
			goal.UpdatedAt = time.Now().UTC()
			_ = r.goals.Save(goal)
			r.notify(chatID, firstNonEmpty(goal.LatestSummary, "Goal is waiting for input."))
			return
		default:
			goal.Status = GoalStatusRunning
			goal.UpdatedAt = time.Now().UTC()
			_ = r.goals.Save(goal)
		}
	}

	goal.Status = GoalStatusBlocked
	goal.LastError = "goal runner exceeded step limit"
	goal.UpdatedAt = time.Now().UTC()
	_ = r.goals.Save(goal)
	r.notify(chatID, "Goal blocked: "+goal.LastError)
}

func (r *GoalRunner) notify(chatID int64, message string) {
	if r.bot == nil || strings.TrimSpace(message) == "" {
		return
	}
	_ = r.bot.SendMessage(context.Background(), chatID, strings.TrimSpace(message))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
