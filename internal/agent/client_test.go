package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"openclaw-lite-go/internal/config"
)

func TestGenerateReplyBuildsOpenAICompatibleRequest(t *testing.T) {
	var capturedAuth string
	var capturedPath string
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	defer server.Close()

	client := NewClient(config.AgentConfig{
		BaseURL:      server.URL,
		APIKey:       "sk-test",
		Model:        "gpt-4o-mini",
		SystemPrompt: "system prompt",
	}, 5*time.Second)

	reply, err := client.GenerateReply(context.Background(), "ping", "")
	if err != nil {
		t.Fatalf("GenerateReply() error = %v", err)
	}
	if reply != "pong" {
		t.Fatalf("unexpected reply: got %q want %q", reply, "pong")
	}
	if capturedPath != "/chat/completions" {
		t.Fatalf("unexpected path: got %q", capturedPath)
	}
	if capturedAuth != "Bearer sk-test" {
		t.Fatalf("unexpected auth header: got %q", capturedAuth)
	}

	model, _ := capturedBody["model"].(string)
	if model != "gpt-4o-mini" {
		t.Fatalf("unexpected model: got %q", model)
	}
}

func TestGenerateReplyReturnsErrorOnNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(config.AgentConfig{
		BaseURL: server.URL,
		APIKey:  "sk-test",
		Model:   "gpt-4o-mini",
	}, 5*time.Second)

	_, err := client.GenerateReply(context.Background(), "ping", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
