package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSendMessageUsesMarkdownV2ParseMode(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer server.Close()

	client := NewClient("token", 2*time.Second)
	client.baseURL = server.URL

	if err := client.SendMessage(context.Background(), 42, "deploy_now"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if payload["parse_mode"] != "MarkdownV2" {
		t.Fatalf("parse_mode = %v, want MarkdownV2", payload["parse_mode"])
	}
	if payload["text"] != "deploy\\_now" {
		t.Fatalf("text = %q, want %q", payload["text"], "deploy\\_now")
	}
}

func TestSendMessageFallsBackToPlainTextWhenMarkdownRejected(t *testing.T) {
	payloads := make([]map[string]any, 0, 2)
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		payloads = append(payloads, payload)

		w.Header().Set("Content-Type", "application/json")
		if attempt == 0 {
			attempt++
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: can't parse entities: Character '-' is reserved and must be escaped with the preceding '\\'"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":2}}`))
	}))
	defer server.Close()

	client := NewClient("token", 2*time.Second)
	client.baseURL = server.URL

	if err := client.SendMessage(context.Background(), 7, "deploy-now"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("send attempts = %d, want 2", len(payloads))
	}
	if payloads[0]["parse_mode"] != "MarkdownV2" {
		t.Fatalf("first parse_mode = %v, want MarkdownV2", payloads[0]["parse_mode"])
	}
	if payloads[0]["text"] != "deploy\\-now" {
		t.Fatalf("first text = %q, want escaped markdown", payloads[0]["text"])
	}
	if _, ok := payloads[1]["parse_mode"]; ok {
		t.Fatalf("second payload should not include parse_mode: %+v", payloads[1])
	}
	if payloads[1]["text"] != "deploy-now" {
		t.Fatalf("second text = %q, want plain text", payloads[1]["text"])
	}
}
