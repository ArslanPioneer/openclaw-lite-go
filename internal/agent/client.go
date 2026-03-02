package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"openclaw-lite-go/internal/config"
)

type Client struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	httpClient   *http.Client
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

func NewClient(cfg config.AgentConfig, timeout time.Duration) *Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:       strings.TrimSpace(cfg.APIKey),
		model:        strings.TrimSpace(cfg.Model),
		systemPrompt: strings.TrimSpace(cfg.SystemPrompt),
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

func (c *Client) GenerateReply(ctx context.Context, userText string, modelOverride string) (string, error) {
	model := strings.TrimSpace(modelOverride)
	if model == "" {
		model = c.model
	}
	messages := make([]message, 0, 2)
	if c.systemPrompt != "" {
		messages = append(messages, message{Role: "system", Content: c.systemPrompt})
	}
	messages = append(messages, message{Role: "user", Content: userText})

	reqBody := chatCompletionRequest{
		Model:    model,
		Messages: messages,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/chat/completions",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("agent request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("agent returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var parsed chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty choices in agent response")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("empty message content in agent response")
	}
	return content, nil
}
