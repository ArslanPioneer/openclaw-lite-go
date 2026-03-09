package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/runtime"
	"openclaw-lite-go/internal/telegram"
	"openclaw-lite-go/internal/tools"
)

type evalCase struct {
	Name              string   `json:"name"`
	Mode              string   `json:"mode,omitempty"`
	PreInputs         []string `json:"pre_inputs,omitempty"`
	Input             string   `json:"input"`
	WaitForCompletion bool     `json:"wait_for_completion,omitempty"`
	ExpectContains    []string `json:"expect_contains"`
	ExpectNotContains []string `json:"expect_not_contains"`
}

type evalBot struct {
	mu      sync.Mutex
	updates []telegram.Update
	next    int
	sent    []string
}

func (b *evalBot) GetUpdates(ctx context.Context, offset int64, _ int) ([]telegram.Update, error) {
	b.mu.Lock()
	if b.next < len(b.updates) {
		update := b.updates[b.next]
		b.next++
		b.mu.Unlock()
		if update.UpdateID >= offset {
			return []telegram.Update{update}, nil
		}
		return nil, nil
	}
	b.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return nil, nil
	}
}

func (b *evalBot) SendMessage(_ context.Context, _ int64, text string) error {
	b.mu.Lock()
	b.sent = append(b.sent, text)
	b.mu.Unlock()
	return nil
}

func (b *evalBot) SentSnapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.sent))
	copy(out, b.sent)
	return out
}

type evalAgent struct{}

func (a *evalAgent) GenerateReply(_ context.Context, prompt string, _ string) (string, error) {
	if strings.Contains(prompt, "Tool result:\nName | State | Status | Image") {
		return "Name | State | Status | Image\nclawlite | running | Up 3 minutes | openclaw-lite-go-clawlite", nil
	}
	if strings.Contains(prompt, "Tool result:\nNVDA latest:") {
		return "NVDA latest: 177.19 USD (from stock_price)", nil
	}

	lower := strings.ToLower(prompt)
	if strings.Contains(lower, "docker") {
		return `TOOL_CALL {"name":"docker_ps"}`, nil
	}
	if strings.Contains(lower, "nvda") {
		return `TOOL_CALL {"name":"stock_price","query":"NVDA"}`, nil
	}
	return "No eval action required.", nil
}

type evalToolExecutor struct{}

type evalCodexProxy struct{}

func (e *evalToolExecutor) Execute(_ context.Context, call tools.Call) (string, error) {
	switch strings.ToLower(strings.TrimSpace(call.Name)) {
	case "docker_ps":
		return "Name | State | Status | Image\nclawlite | running | Up 3 minutes | openclaw-lite-go-clawlite", nil
	case "stock_price":
		return "NVDA latest: 177.19 USD (from stock_price)", nil
	case "web_search":
		return "Query: NVDA stock price latest\nSources:\n1. NVDA Quote\n   URL: https://example.com/nvda\n   Snippet: 177.19 USD", nil
	default:
		return "", fmt.Errorf("unsupported eval tool: %s", call.Name)
	}
}

func (p *evalCodexProxy) Chat(_ context.Context, _ int64, message string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(lower, "service status") || strings.Contains(lower, "service health"):
		return "ClawLite service is healthy. Codex auth present. Proxy reply correctness OK.", nil
	case strings.Contains(lower, "disk usage"):
		return "Disk usage: 42% used on / and 18% used on /var.", nil
	case strings.Contains(lower, "latest") || strings.Contains(lower, "today") || strings.Contains(lower, "current"):
		return "Latest AI news summary.\nSources:\n1. https://example.com/ai-news", nil
	case strings.Contains(lower, "repo") || strings.Contains(lower, "multi-step"):
		return "Codex-first runtime review complete. Codex goal completion ratio: 100%.", nil
	default:
		return "Codex-first eval handled the request.", nil
	}
}

func main() {
	casesPath := flag.String("cases", "./scripts/evals/cases.json", "path to eval cases json")
	minPassRatio := flag.Float64("min-pass-ratio", 0.9, "minimum pass ratio required")
	flag.Parse()

	cases, err := loadCases(*casesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load cases failed: %v\n", err)
		os.Exit(1)
	}
	if len(cases) == 0 {
		fmt.Fprintln(os.Stderr, "no eval cases found")
		os.Exit(1)
	}

	passed := 0
	for idx, c := range cases {
		output, runErr := runCase(idx, c)
		if runErr != nil {
			fmt.Printf("[FAIL] %s: run error: %v\n", c.Name, runErr)
			continue
		}

		if checkErr := validateCaseOutput(output, c); checkErr != nil {
			fmt.Printf("[FAIL] %s: %v\n", c.Name, checkErr)
			fmt.Printf("  output: %q\n", output)
			continue
		}
		passed++
		fmt.Printf("[PASS] %s\n", c.Name)
	}

	total := len(cases)
	ratio := float64(passed) / float64(total)
	fmt.Printf("Summary: %d/%d passed (%.1f%%)\n", passed, total, ratio*100)

	if ratio < *minPassRatio {
		fmt.Printf("Eval gate failed: pass ratio %.1f%% is below %.1f%%\n", ratio*100, (*minPassRatio)*100)
		os.Exit(1)
	}
	if passed != total {
		os.Exit(1)
	}
}

func loadCases(path string) ([]evalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}

func runCase(idx int, c evalCase) (string, error) {
	tmpDir, err := os.MkdirTemp("", "openclaw-lite-eval-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	chatID := int64(1000 + idx)
	updates := make([]telegram.Update, 0, len(c.PreInputs)+1)
	nextUpdateID := int64(idx*100 + 1)
	for _, pre := range c.PreInputs {
		updates = append(updates, telegram.Update{
			UpdateID: nextUpdateID,
			Message: &telegram.Message{
				Chat: telegram.Chat{ID: chatID},
				Text: pre,
			},
		})
		nextUpdateID++
	}
	updates = append(updates, telegram.Update{
		UpdateID: nextUpdateID,
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: chatID},
			Text: c.Input,
		},
	})

	bot := &evalBot{updates: updates}
	agent := &evalAgent{}
	cfg := config.Config{
		Agent: config.AgentConfig{
			Model: "gpt-4o-mini",
		},
		Runtime: config.RuntimeConfig{
			DataDir:           tmpDir,
			HistoryTurns:      8,
			Workers:           1,
			PollTimeoutSecond: 1,
		},
	}
	if strings.EqualFold(strings.TrimSpace(c.Mode), "codex_first") {
		cfg.Runtime.CodexFirstDefault = true
		cfg.Runtime.CodexProxyURL = "http://127.0.0.1:8099/chat"
	}
	svc := runtime.NewService(cfg, bot, agent)
	svc.SetToolExecutor(&evalToolExecutor{})
	if strings.EqualFold(strings.TrimSpace(c.Mode), "codex_first") {
		svc.SetCodexProxy(&evalCodexProxy{})
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- svc.Run(runCtx)
	}()

	var (
		output  string
		waitErr error
	)
	if c.WaitForCompletion {
		output, waitErr = waitForCompletionOutput(bot)
	} else {
		output, waitErr = waitForAnyOutput(bot)
	}
	if waitErr != nil {
		return "", waitErr
	}
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
	}
	return output, nil
}

func waitForAnyOutput(bot *evalBot) (string, error) {
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		sent := bot.SentSnapshot()
		if len(sent) > 0 {
			return sent[0], nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "", fmt.Errorf("no bot output")
}

func waitForCompletionOutput(bot *evalBot) (string, error) {
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		sent := bot.SentSnapshot()
		if len(sent) > 0 {
			last := sent[len(sent)-1]
			if !isTransientEvalMessage(last) {
				return last, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	sent := bot.SentSnapshot()
	if len(sent) == 0 {
		return "", fmt.Errorf("no bot output")
	}
	return sent[len(sent)-1], nil
}

func isTransientEvalMessage(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(lower, "goal queued") {
		return true
	}
	if strings.Contains(lower, "reply /confirm to continue") {
		return true
	}
	if strings.Contains(lower, "confirmed. goal queued") {
		return true
	}
	return false
}

func validateCaseOutput(output string, c evalCase) error {
	lowerOutput := strings.ToLower(output)
	for _, needle := range c.ExpectContains {
		if !strings.Contains(lowerOutput, strings.ToLower(needle)) {
			return fmt.Errorf("missing expected fragment %q", needle)
		}
	}
	for _, needle := range c.ExpectNotContains {
		if strings.Contains(lowerOutput, strings.ToLower(needle)) {
			return fmt.Errorf("found forbidden fragment %q", needle)
		}
	}
	return nil
}
