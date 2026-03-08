package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPCodexProxyChatParsesJSONReply(t *testing.T) {
	var capturedAuth string
	var capturedBody codexProxyRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reply":"done from codex proxy"}`))
	}))
	defer server.Close()

	proxy := NewHTTPCodexProxy(server.URL, "token-123", 2*time.Second)
	reply, err := proxy.Chat(context.Background(), 42, "fix this test")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != "done from codex proxy" {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if capturedAuth != "Bearer token-123" {
		t.Fatalf("unexpected auth header: %q", capturedAuth)
	}
	if capturedBody.ChatID != 42 || capturedBody.Message != "fix this test" {
		t.Fatalf("unexpected request payload: %+v", capturedBody)
	}
}

func TestHTTPCodexProxyChatFallsBackToRawText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("raw terminal output"))
	}))
	defer server.Close()

	proxy := NewHTTPCodexProxy(server.URL, "", 2*time.Second)
	reply, err := proxy.Chat(context.Background(), 7, "hello")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "raw terminal output") {
		t.Fatalf("unexpected reply: %q", reply)
	}
}
