package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"openclaw-lite-go/internal/skills"
)

func TestExecutorSkillInstallListRead(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source")
	install := filepath.Join(tmp, "install")

	skillDir := filepath.Join(source, "weather")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: weather\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	manager := skills.NewManager(source, install)
	executor := NewExecutor(2*time.Second, manager)

	installOutput, err := executor.Execute(context.Background(), Call{
		Name:  "skill_install",
		Skill: "weather",
	})
	if err != nil {
		t.Fatalf("skill_install error = %v", err)
	}
	if !strings.Contains(installOutput, "weather") {
		t.Fatalf("unexpected skill_install output: %q", installOutput)
	}

	listOutput, err := executor.Execute(context.Background(), Call{Name: "skill_list"})
	if err != nil {
		t.Fatalf("skill_list error = %v", err)
	}
	if !strings.Contains(listOutput, "weather") {
		t.Fatalf("unexpected skill_list output: %q", listOutput)
	}

	readOutput, err := executor.Execute(context.Background(), Call{
		Name:  "skill_read",
		Skill: "weather",
	})
	if err != nil {
		t.Fatalf("skill_read error = %v", err)
	}
	if !strings.Contains(readOutput, "name: weather") {
		t.Fatalf("unexpected skill_read output: %q", readOutput)
	}
}

func TestExecutorSkillRunScript(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source")
	install := filepath.Join(tmp, "install")

	skillDir := filepath.Join(source, "echoer")
	scriptDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: echoer\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	script := "scripts/run.sh"
	scriptBody := "#!/usr/bin/env sh\necho \"$SKILL_INPUT\"\n"
	if runtime.GOOS == "windows" {
		script = "scripts/run.ps1"
		scriptBody = "Write-Output $env:SKILL_INPUT\n"
	}
	if err := os.WriteFile(filepath.Join(skillDir, filepath.FromSlash(script)), []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	manager := skills.NewManager(source, install)
	executor := NewExecutor(2*time.Second, manager)

	if _, err := executor.Execute(context.Background(), Call{
		Name:  "skill_install",
		Skill: "echoer",
	}); err != nil {
		t.Fatalf("skill_install error = %v", err)
	}

	output, err := executor.Execute(context.Background(), Call{
		Name:   "skill_run",
		Skill:  "echoer",
		Script: script,
		Input:  "hello-from-tool",
	})
	if err != nil {
		t.Fatalf("skill_run error = %v", err)
	}
	if strings.TrimSpace(output) != "hello-from-tool" {
		t.Fatalf("unexpected skill_run output: %q", output)
	}
}

func TestExecutorDockerPS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test")
	}

	tmp := t.TempDir()
	socketPath := filepath.Join(tmp, "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/containers/json") {
				http.Error(w, "bad path", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"Names":  []string{"/clawlite"},
					"Image":  "openclaw-lite-go-clawlite",
					"State":  "running",
					"Status": "Up 3 minutes",
				},
			})
		}),
	}
	defer server.Close()
	go func() {
		_ = server.Serve(listener)
	}()

	executor := NewExecutor(2*time.Second, nil)
	executor.dockerSocketPath = socketPath

	output, err := executor.Execute(context.Background(), Call{Name: "docker_ps"})
	if err != nil {
		t.Fatalf("docker_ps error = %v", err)
	}
	if !strings.Contains(output, "clawlite") || !strings.Contains(output, "running") {
		t.Fatalf("unexpected docker_ps output: %q", output)
	}
}

func TestParseToolCallAllowsTrailingAssistantText(t *testing.T) {
	raw := `TOOL_CALL {"name":"web_search","query":"NVDA price"}
Now I will summarize based on tool result.`

	call, requested, err := ParseToolCall(raw)
	if err != nil {
		t.Fatalf("ParseToolCall() unexpected error = %v", err)
	}
	if !requested {
		t.Fatal("expected tool call to be detected")
	}
	if call.Name != "web_search" || call.Query != "NVDA price" {
		t.Fatalf("unexpected parsed call: %#v", call)
	}
}

func TestExecutorStockPriceFromYahoo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v7/finance/quote") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quoteResponse": map[string]any{
				"result": []map[string]any{
					{
						"symbol":                     "NVDA",
						"regularMarketPrice":         130.12,
						"regularMarketChangePercent": 1.23,
						"regularMarketTime":          1735689600,
						"currency":                   "USD",
						"marketState":                "REGULAR",
					},
				},
			},
		})
	}))
	defer server.Close()

	executor := NewExecutor(2*time.Second, nil)
	executor.stockQuoteURLTemplate = server.URL + "/v7/finance/quote?symbols=%s"

	output, err := executor.Execute(context.Background(), Call{Name: "stock_price", Query: "nvda"})
	if err != nil {
		t.Fatalf("stock_price error = %v", err)
	}
	if !strings.Contains(output, "NVDA") || !strings.Contains(output, "130.12") {
		t.Fatalf("unexpected stock_price output: %q", output)
	}
}

func TestExecutorWebSearchReturnsCitedSources(t *testing.T) {
	encoded1 := url.QueryEscape("https://example.com/openclaw-release")
	encoded2 := url.QueryEscape("https://example.org/agent-loop")
	htmlBody := fmt.Sprintf(`<html><body>
<a class="result__a" href="https://duckduckgo.com/l/?uddg=%s">OpenClaw Release Notes</a>
<a class="result__snippet">Release summary with reliability updates.</a>
<a class="result__a" href="https://duckduckgo.com/l/?uddg=%s">Agent Loop Improvements</a>
<div class="result__snippet">Parser recovery and loop guardrails.</div>
</body></html>`, encoded1, encoded2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	executor := NewExecutor(2*time.Second, nil)
	executor.webSearchHTMLURL = server.URL + "/html/"

	output, err := executor.Execute(context.Background(), Call{
		Name:       "web_search",
		Query:      "openclaw reliability",
		MaxResults: 2,
	})
	if err != nil {
		t.Fatalf("web_search error = %v", err)
	}

	if !strings.Contains(output, "Sources:") {
		t.Fatalf("expected sources section, got %q", output)
	}
	if !strings.Contains(output, "OpenClaw Release Notes") {
		t.Fatalf("expected first title in output, got %q", output)
	}
	if !strings.Contains(output, "https://example.com/openclaw-release") {
		t.Fatalf("expected normalized first source URL, got %q", output)
	}
	if !strings.Contains(output, "https://example.org/agent-loop") {
		t.Fatalf("expected normalized second source URL, got %q", output)
	}
	if strings.Contains(output, "duckduckgo.com/l/?uddg=") {
		t.Fatalf("expected redirect links to be normalized, got %q", output)
	}
}

func TestExecutorWebSearchAppliesRecencyAndMaxResults(t *testing.T) {
	var capturedDF string
	var capturedQ string

	htmlBody := `<html><body>
<a class="result__a" href="https://example.com/1">Result One</a>
<div class="result__snippet">Snippet one.</div>
<a class="result__a" href="https://example.com/2">Result Two</a>
<div class="result__snippet">Snippet two.</div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedDF = r.URL.Query().Get("df")
		capturedQ = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	executor := NewExecutor(2*time.Second, nil)
	executor.webSearchHTMLURL = server.URL + "/html/"

	output, err := executor.Execute(context.Background(), Call{
		Name:        "web_search",
		Query:       "fresh ai news",
		RecencyDays: 7,
		MaxResults:  1,
	})
	if err != nil {
		t.Fatalf("web_search error = %v", err)
	}
	if capturedQ != "fresh ai news" {
		t.Fatalf("unexpected query sent upstream: %q", capturedQ)
	}
	if capturedDF != "w" {
		t.Fatalf("expected recency bucket df=w, got %q", capturedDF)
	}
	if !strings.Contains(output, "Result One") {
		t.Fatalf("expected first result in output, got %q", output)
	}
	if strings.Contains(output, "Result Two") {
		t.Fatalf("expected max_results=1 to limit output, got %q", output)
	}
}

func TestExecutorResearchReturnsStructuredSources(t *testing.T) {
	encoded := url.QueryEscape("https://example.com/openclaw-release")
	htmlBody := fmt.Sprintf(`<html><body>
<a class="result__a" href="https://duckduckgo.com/l/?uddg=%s">OpenClaw Release Notes</a>
<div class="result__snippet">Release summary with reliability updates.</div>
</body></html>`, encoded)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer server.Close()

	executor := NewExecutor(2*time.Second, nil)
	executor.webSearchHTMLURL = server.URL + "/html/"

	results, err := executor.WebSearchResults(context.Background(), Call{
		Name:       "web_search",
		Query:      "openclaw reliability",
		MaxResults: 1,
	})
	if err != nil {
		t.Fatalf("WebSearchResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Title != "OpenClaw Release Notes" {
		t.Fatalf("Title = %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/openclaw-release" {
		t.Fatalf("URL = %q", results[0].URL)
	}
	if !strings.Contains(results[0].Snippet, "reliability updates") {
		t.Fatalf("Snippet = %q", results[0].Snippet)
	}
}
