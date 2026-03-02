package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/runtime"
	"openclaw-lite-go/internal/telegram"
	"openclaw-lite-go/internal/tools"
)

type evalCase struct {
	Name              string   `json:"name"`
	Input             string   `json:"input"`
	ExpectContains    []string `json:"expect_contains"`
	ExpectNotContains []string `json:"expect_not_contains"`
}

type evalBot struct {
	sent []string
}

func (b *evalBot) GetUpdates(_ context.Context, _ int64, _ int) ([]telegram.Update, error) {
	return nil, nil
}

func (b *evalBot) SendMessage(_ context.Context, _ int64, text string) error {
	b.sent = append(b.sent, text)
	return nil
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

	bot := &evalBot{}
	agent := &evalAgent{}
	svc := runtime.NewService(config.Config{
		Agent: config.AgentConfig{
			Model: "gpt-4o-mini",
		},
		Runtime: config.RuntimeConfig{
			DataDir:      tmpDir,
			HistoryTurns: 8,
		},
	}, bot, agent)
	svc.SetToolExecutor(&evalToolExecutor{})

	update := telegram.Update{
		UpdateID: int64(idx + 1),
		Message: &telegram.Message{
			Chat: telegram.Chat{ID: int64(1000 + idx)},
			Text: c.Input,
		},
	}
	if err := svc.HandleUpdate(context.Background(), update); err != nil {
		return "", err
	}
	if len(bot.sent) == 0 {
		return "", fmt.Errorf("no bot output")
	}
	return bot.sent[len(bot.sent)-1], nil
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
