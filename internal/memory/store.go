package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatState struct {
	Summary  string    `json:"summary,omitempty"`
	Messages []Message `json:"messages,omitempty"`
}

type Store struct {
	baseDir  string
	maxTurns int
	mu       sync.Mutex
}

func NewStore(dataDir string, maxTurns int) *Store {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	if maxTurns <= 0 {
		maxTurns = 8
	}
	return &Store{
		baseDir:  filepath.Join(dataDir, "memory"),
		maxTurns: maxTurns,
	}
}

func (s *Store) DataDir() string {
	return filepath.Dir(s.baseDir)
}

func (s *Store) Load(chatID int64) (ChatState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked(chatID)
}

func (s *Store) AppendExchange(chatID int64, userText string, assistantText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadUnlocked(chatID)
	if err != nil {
		return err
	}

	state.Messages = append(state.Messages,
		Message{Role: "user", Content: strings.TrimSpace(userText)},
		Message{Role: "assistant", Content: strings.TrimSpace(assistantText)},
	)

	state = s.compact(state)
	return s.saveUnlocked(chatID, state)
}

func (s *Store) loadUnlocked(chatID int64) (ChatState, error) {
	path := s.filePath(chatID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ChatState{}, nil
		}
		return ChatState{}, fmt.Errorf("read memory state: %w", err)
	}

	var state ChatState
	if err := json.Unmarshal(data, &state); err != nil {
		return ChatState{}, fmt.Errorf("parse memory state: %w", err)
	}
	return state, nil
}

func (s *Store) saveUnlocked(chatID int64, state ChatState) error {
	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memory state: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(s.filePath(chatID), data, 0o600); err != nil {
		return fmt.Errorf("write memory state: %w", err)
	}
	return nil
}

func (s *Store) compact(state ChatState) ChatState {
	maxMessages := s.maxTurns * 2
	if len(state.Messages) <= maxMessages {
		return state
	}

	cut := len(state.Messages) - maxMessages
	old := state.Messages[:cut]
	state.Messages = state.Messages[cut:]

	delta := summarizeMessages(old)
	if delta != "" {
		if state.Summary == "" {
			state.Summary = delta
		} else {
			state.Summary = strings.TrimSpace(state.Summary + " " + delta)
		}
	}
	if len(state.Summary) > 4000 {
		state.Summary = state.Summary[len(state.Summary)-4000:]
	}
	return state
}

func summarizeMessages(messages []Message) string {
	segments := make([]string, 0, 8)
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		switch msg.Role {
		case "user":
			segments = append(segments, "User asked: "+clip(content, 120))
		case "assistant":
			segments = append(segments, "Assistant answered: "+clip(content, 120))
		}
		if len(segments) >= 8 {
			break
		}
	}
	return strings.Join(segments, " ")
}

func clip(raw string, max int) string {
	if len(raw) <= max {
		return raw
	}
	return strings.TrimSpace(raw[:max]) + "..."
}

func (s *Store) filePath(chatID int64) string {
	return filepath.Join(s.baseDir, strconv.FormatInt(chatID, 10)+".json")
}
