package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendMessageUsesHTMLParseModeAndMarkdownRendering(t *testing.T) {
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

	msg := "## Summary\n- *ready*\n- `cmd`\nSee [docs](https://example.com)\n\n```sh\necho ok\n```"
	if err := client.SendMessage(context.Background(), 42, msg); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}

	if payload["parse_mode"] != "HTML" {
		t.Fatalf("parse_mode = %v, want HTML", payload["parse_mode"])
	}

	text, ok := payload["text"].(string)
	if !ok {
		t.Fatalf("text type = %T, want string", payload["text"])
	}

	for _, want := range []string{
		"<b>Summary</b>",
		"&#8226; <i>ready</i>",
		"&#8226; <code>cmd</code>",
		`<a href="https://example.com">docs</a>`,
		"<pre><code>echo ok\n</code></pre>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text = %q, want substring %q", text, want)
		}
	}
}

func TestSendMessageEscapesRawHTMLBeforeSending(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":3}}`))
	}))
	defer server.Close()

	client := NewClient("token", 2*time.Second)
	client.baseURL = server.URL

	if err := client.SendMessage(context.Background(), 9, "<b>nope</b>"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}

	if payload["text"] != "&lt;b&gt;nope&lt;/b&gt;" {
		t.Fatalf("text = %q, want escaped HTML", payload["text"])
	}
}

func TestSendMessageFallsBackToPlainTextWhenHTMLRejected(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: can't parse entities: unsupported start tag"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":2}}`))
	}))
	defer server.Close()

	client := NewClient("token", 2*time.Second)
	client.baseURL = server.URL

	if err := client.SendMessage(context.Background(), 7, "*deploy*"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}

	if len(payloads) != 2 {
		t.Fatalf("send attempts = %d, want 2", len(payloads))
	}
	if payloads[0]["parse_mode"] != "HTML" {
		t.Fatalf("first parse_mode = %v, want HTML", payloads[0]["parse_mode"])
	}
	if payloads[0]["text"] != "<i>deploy</i>" {
		t.Fatalf("first text = %q, want rendered HTML", payloads[0]["text"])
	}
	if _, ok := payloads[1]["parse_mode"]; ok {
		t.Fatalf("second payload should not include parse_mode: %+v", payloads[1])
	}
	if payloads[1]["text"] != "*deploy*" {
		t.Fatalf("second text = %q, want plain text", payloads[1]["text"])
	}
}
