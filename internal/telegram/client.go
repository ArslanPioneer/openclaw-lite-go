package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

func NewClient(token string, timeout time.Duration) *Client {
	return &Client{
		token:   strings.TrimSpace(token),
		baseURL: "https://api.telegram.org",
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSecond int) ([]Update, error) {
	payload := map[string]any{
		"timeout": timeoutSecond,
	}
	if offset > 0 {
		payload["offset"] = offset
	}

	var updates []Update
	if err := c.call(ctx, "getUpdates", payload, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	markdownPayload := map[string]any{
		"chat_id":    chatID,
		"text":       escapeMarkdownV2(text),
		"parse_mode": "MarkdownV2",
	}
	var ignored map[string]any
	err := c.call(ctx, "sendMessage", markdownPayload, &ignored)
	if err == nil {
		return nil
	}
	if !isMarkdownEntityParseError(err) {
		return err
	}

	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	return c.call(ctx, "sendMessage", payload, &ignored)
}

func isMarkdownEntityParseError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lower, "parse entities")
}

func escapeMarkdownV2(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text) + 8)
	for _, r := range text {
		switch r {
		case '\\', '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	url := c.baseURL + "/bot" + c.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("telegram status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode telegram response: %w", err)
	}
	if !parsed.OK {
		return fmt.Errorf("telegram api error: %s", parsed.Description)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(parsed.Result, out); err != nil {
		return fmt.Errorf("decode telegram result: %w", err)
	}
	return nil
}

func ParseChatID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid chat id %q", raw)
	}
	return id, nil
}
