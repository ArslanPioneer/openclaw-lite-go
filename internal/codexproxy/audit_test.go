package codexproxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAuditLogCapturesPromptAndReplyMetadata(t *testing.T) {
	workdir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	server := NewServer(Config{
		WorkDir:  workdir,
		StateDir: stateDir,
		Executor: &fakeExecutor{reply: []byte(`{"reply":"host looks healthy"}`)},
	})

	req := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(`{"chat_id":42,"message":"[goal:goal-123] inspect the host status"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	log := NewAuditLog(stateDir)
	records, err := log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}

	record := records[0]
	if record.ChatID != 42 {
		t.Fatalf("ChatID = %d, want 42", record.ChatID)
	}
	if record.GoalID != "goal-123" {
		t.Fatalf("GoalID = %q, want goal-123", record.GoalID)
	}
	if record.RawUserMessage != "[goal:goal-123] inspect the host status" {
		t.Fatalf("RawUserMessage = %q", record.RawUserMessage)
	}
	if record.FinalReply != "host looks healthy" {
		t.Fatalf("FinalReply = %q", record.FinalReply)
	}
	if record.PromptHash == "" {
		t.Fatal("expected PromptHash to be recorded")
	}
	if record.ExecutionMode != "full-auto" {
		t.Fatalf("ExecutionMode = %q, want full-auto", record.ExecutionMode)
	}
}
