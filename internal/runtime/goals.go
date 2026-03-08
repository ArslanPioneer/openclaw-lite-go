package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GoalStatus string

const (
	GoalStatusQueued  GoalStatus = "queued"
	GoalStatusRunning GoalStatus = "running"
	GoalStatusBlocked GoalStatus = "blocked"
	GoalStatusDone    GoalStatus = "done"
)

type Goal struct {
	ID            string     `json:"id"`
	ChatID        int64      `json:"chat_id"`
	Objective     string     `json:"objective"`
	Status        GoalStatus `json:"status"`
	LatestSummary string     `json:"latest_summary,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type GoalResult struct {
	Running bool
	Done    bool
	Summary string
	Err     error
}

type GoalStore struct {
	baseDir string
	mu      sync.Mutex
}

func NewGoal(chatID int64, objective string) Goal {
	now := time.Now().UTC()
	return Goal{
		ID:        fmt.Sprintf("%d-%d", chatID, now.UnixNano()),
		ChatID:    chatID,
		Objective: strings.TrimSpace(objective),
		Status:    GoalStatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func ApplyGoalResult(goal Goal, result GoalResult) Goal {
	goal.UpdatedAt = time.Now().UTC()
	if strings.TrimSpace(result.Summary) != "" {
		goal.LatestSummary = strings.TrimSpace(result.Summary)
	}
	if result.Running {
		goal.Status = GoalStatusRunning
	}
	if result.Err != nil {
		goal.Status = GoalStatusBlocked
		goal.LastError = strings.TrimSpace(result.Err.Error())
		return goal
	}
	if result.Done {
		goal.Status = GoalStatusDone
		goal.LastError = ""
	}
	return goal
}

func NewGoalStore(dataDir string) *GoalStore {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return &GoalStore{
		baseDir: filepath.Join(dataDir, "goals"),
	}
}

func (s *GoalStore) Save(goal Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.loadChatGoalsUnlocked(goal.ChatID)
	if err != nil {
		return err
	}

	replaced := false
	for i := range goals {
		if goals[i].ID == goal.ID {
			goals[i] = goal
			replaced = true
			break
		}
	}
	if !replaced {
		goals = append(goals, goal)
	}

	sort.Slice(goals, func(i, j int) bool {
		return goals[i].UpdatedAt.After(goals[j].UpdatedAt)
	})
	return s.saveChatGoalsUnlocked(goal.ChatID, goals)
}

func (s *GoalStore) Load(chatID int64, goalID string) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	goals, err := s.loadChatGoalsUnlocked(chatID)
	if err != nil {
		return Goal{}, err
	}
	for _, goal := range goals {
		if goal.ID == goalID {
			return goal, nil
		}
	}
	return Goal{}, fmt.Errorf("goal not found: %s", goalID)
}

func (s *GoalStore) List(chatID int64) ([]Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadChatGoalsUnlocked(chatID)
}

func (s *GoalStore) loadChatGoalsUnlocked(chatID int64) ([]Goal, error) {
	path := s.filePath(chatID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read goals: %w", err)
	}

	var goals []Goal
	if err := json.Unmarshal(data, &goals); err != nil {
		return nil, fmt.Errorf("parse goals: %w", err)
	}
	return goals, nil
}

func (s *GoalStore) saveChatGoalsUnlocked(chatID int64, goals []Goal) error {
	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("create goals directory: %w", err)
	}

	data, err := json.MarshalIndent(goals, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal goals: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(s.filePath(chatID), data, 0o600); err != nil {
		return fmt.Errorf("write goals: %w", err)
	}
	return nil
}

func (s *GoalStore) filePath(chatID int64) string {
	return filepath.Join(s.baseDir, strconv.FormatInt(chatID, 10)+".json")
}
