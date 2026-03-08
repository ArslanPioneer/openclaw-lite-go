package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SessionState struct {
	ExecutionMode          string    `json:"execution_mode,omitempty"`
	ActiveGoalID           string    `json:"active_goal_id,omitempty"`
	LastCodexResultSummary string    `json:"last_codex_result_summary,omitempty"`
	LastActivity           time.Time `json:"last_activity,omitempty"`
}

type SessionStore struct {
	baseDir string
	mu      sync.Mutex
}

func NewSessionStore(dataDir string) *SessionStore {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return &SessionStore{
		baseDir: filepath.Join(dataDir, "sessions"),
	}
}

func (s *SessionStore) Load(chatID int64) (SessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked(chatID)
}

func (s *SessionStore) Save(chatID int64, state SessionState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked(chatID, state)
}

func (s *SessionStore) Update(chatID int64, apply func(*SessionState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadUnlocked(chatID)
	if err != nil {
		return err
	}
	apply(&state)
	return s.saveUnlocked(chatID, state)
}

func (s *SessionStore) loadUnlocked(chatID int64) (SessionState, error) {
	data, err := os.ReadFile(s.filePath(chatID))
	if err != nil {
		if os.IsNotExist(err) {
			return SessionState{}, nil
		}
		return SessionState{}, fmt.Errorf("read session state: %w", err)
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return SessionState{}, fmt.Errorf("parse session state: %w", err)
	}
	return state, nil
}

func (s *SessionStore) saveUnlocked(chatID int64, state SessionState) error {
	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(s.filePath(chatID), data, 0o600); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}
	return nil
}

func (s *SessionStore) filePath(chatID int64) string {
	return filepath.Join(s.baseDir, strconv.FormatInt(chatID, 10)+".json")
}
