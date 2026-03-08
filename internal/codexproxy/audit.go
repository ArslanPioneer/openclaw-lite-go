package codexproxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AuditRecord struct {
	Timestamp     time.Time `json:"timestamp"`
	ChatID        int64     `json:"chat_id"`
	GoalID        string    `json:"goal_id,omitempty"`
	RawUserMessage string   `json:"raw_user_message"`
	PromptHash    string    `json:"prompt_hash"`
	FinalReply    string    `json:"final_reply,omitempty"`
	ExecutionMode string    `json:"execution_mode"`
}

type AuditLog struct {
	path string
	mu   sync.Mutex
}

func NewAuditLog(stateDir string) *AuditLog {
	base := strings.TrimSpace(stateDir)
	if base == "" {
		base = "."
	}
	return &AuditLog{
		path: filepath.Join(base, "audit.jsonl"),
	}
}

func (l *AuditLog) Append(record AuditRecord) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("create audit directory: %w", err)
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal audit record: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit record: %w", err)
	}
	return nil
}

func (l *AuditLog) ReadAll() ([]AuditRecord, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	file, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	defer file.Close()

	records := make([]AuditRecord, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record AuditRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("parse audit log: %w", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan audit log: %w", err)
	}
	return records, nil
}
