package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrPendingConfirmationNotFound = errors.New("pending confirmation not found")

type PendingConfirmation struct {
	GoalID     string    `json:"goal_id"`
	RawRequest string    `json:"raw_request"`
	RiskLevel  string    `json:"risk_level"`
	CreatedAt  time.Time `json:"created_at"`
}

type ConfirmStore struct {
	baseDir string
	mu      sync.Mutex
}

func NewConfirmStore(dataDir string) *ConfirmStore {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return &ConfirmStore{
		baseDir: filepath.Join(dataDir, "confirmations"),
	}
}

func (s *ConfirmStore) Save(chatID int64, pending PendingConfirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("create confirmation directory: %w", err)
	}

	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending confirmation: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(s.filePath(chatID), data, 0o600); err != nil {
		return fmt.Errorf("write pending confirmation: %w", err)
	}
	return nil
}

func (s *ConfirmStore) Load(chatID int64) (PendingConfirmation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath(chatID))
	if err != nil {
		if os.IsNotExist(err) {
			return PendingConfirmation{}, ErrPendingConfirmationNotFound
		}
		return PendingConfirmation{}, fmt.Errorf("read pending confirmation: %w", err)
	}

	var pending PendingConfirmation
	if err := json.Unmarshal(data, &pending); err != nil {
		return PendingConfirmation{}, fmt.Errorf("parse pending confirmation: %w", err)
	}
	return pending, nil
}

func (s *ConfirmStore) Clear(chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.filePath(chatID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear pending confirmation: %w", err)
	}
	return nil
}

func (s *ConfirmStore) filePath(chatID int64) string {
	return filepath.Join(s.baseDir, strconv.FormatInt(chatID, 10)+".json")
}
