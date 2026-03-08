package codexproxy

import (
	"context"
	"strings"
	"testing"

	"openclaw-lite-go/internal/tools"
)

type fakeSearchExecutor struct {
	calls []tools.Call
	run   func(call tools.Call) ([]tools.SearchResult, error)
}

func (f *fakeSearchExecutor) WebSearchResults(_ context.Context, call tools.Call) ([]tools.SearchResult, error) {
	f.calls = append(f.calls, call)
	if f.run != nil {
		return f.run(call)
	}
	return nil, nil
}

func TestResearchToolReturnsSourcesAndSnippets(t *testing.T) {
	search := &fakeSearchExecutor{
		run: func(call tools.Call) ([]tools.SearchResult, error) {
			return []tools.SearchResult{
				{
					Title:   "OpenClaw Release Notes",
					URL:     "https://example.com/release",
					Snippet: "Release summary with reliability updates.",
				},
			}, nil
		},
	}

	researcher := NewResearcher(search)
	results, err := researcher.Research(context.Background(), "openclaw release notes", 7, 3)
	if err != nil {
		t.Fatalf("Research() error = %v", err)
	}

	if len(search.calls) != 1 {
		t.Fatalf("search calls = %d, want 1", len(search.calls))
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Title != "OpenClaw Release Notes" {
		t.Fatalf("Title = %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/release" {
		t.Fatalf("URL = %q", results[0].URL)
	}
	if !strings.Contains(results[0].Snippet, "reliability") {
		t.Fatalf("Snippet = %q", results[0].Snippet)
	}
}

func TestCodexPromptMentionsResearchPathWhenUserNeedsCurrentInfo(t *testing.T) {
	prompt := buildPrompt(nil, "what is the latest OpenAI news today?", []tools.SearchResult{
		{
			Title:   "Latest OpenAI News",
			URL:     "https://example.com/openai-news",
			Snippet: "Current update summary.",
		},
	})

	for _, fragment := range []string{
		"explicit research",
		"include sources",
		"Research context:",
		"https://example.com/openai-news",
	} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(fragment)) {
			t.Fatalf("prompt missing %q:\n%s", fragment, prompt)
		}
	}
}
