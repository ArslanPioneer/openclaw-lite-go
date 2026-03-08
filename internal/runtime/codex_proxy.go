package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPCodexProxy struct {
	url    string
	token  string
	client *http.Client
}

type codexProxyRequest struct {
	ChatID  int64  `json:"chat_id"`
	Message string `json:"message"`
}

type codexProxyResponse struct {
	Reply  string `json:"reply"`
	Output string `json:"output"`
	Text   string `json:"text"`
}

func NewHTTPCodexProxy(url string, token string, timeout time.Duration) *HTTPCodexProxy {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &HTTPCodexProxy{
		url:   strings.TrimSpace(url),
		token: strings.TrimSpace(token),
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (p *HTTPCodexProxy) Chat(ctx context.Context, chatID int64, message string) (string, error) {
	if p == nil || strings.TrimSpace(p.url) == "" {
		return "", fmt.Errorf("codex proxy url is not configured")
	}

	payload := codexProxyRequest{
		ChatID:  chatID,
		Message: strings.TrimSpace(message),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal codex proxy request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build codex proxy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("codex proxy request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read codex proxy response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("codex proxy status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	reply := parseCodexProxyReply(data)
	if strings.TrimSpace(reply) == "" {
		return "", nil
	}
	return reply, nil
}

func parseCodexProxyReply(data []byte) string {
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return ""
	}

	var parsed codexProxyResponse
	if err := json.Unmarshal(data, &parsed); err == nil {
		if strings.TrimSpace(parsed.Reply) != "" {
			return strings.TrimSpace(parsed.Reply)
		}
		if strings.TrimSpace(parsed.Output) != "" {
			return strings.TrimSpace(parsed.Output)
		}
		if strings.TrimSpace(parsed.Text) != "" {
			return strings.TrimSpace(parsed.Text)
		}
	}
	return raw
}
