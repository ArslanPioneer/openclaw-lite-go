package codexproxy

import (
	"context"
	"strings"
	"time"

	"openclaw-lite-go/internal/tools"
)

type searchExecutor interface {
	WebSearchResults(ctx context.Context, call tools.Call) ([]tools.SearchResult, error)
}

type Researcher interface {
	Research(ctx context.Context, query string, recencyDays int, maxResults int) ([]tools.SearchResult, error)
}

type WebResearcher struct {
	search searchExecutor
}

func NewResearcher(search searchExecutor) *WebResearcher {
	if search == nil {
		search = tools.NewExecutor(20*time.Second, nil)
	}
	return &WebResearcher{search: search}
}

func (r *WebResearcher) Research(ctx context.Context, query string, recencyDays int, maxResults int) ([]tools.SearchResult, error) {
	if r == nil || r.search == nil {
		return nil, nil
	}
	return r.search.WebSearchResults(ctx, tools.Call{
		Name:        "web_search",
		Query:       strings.TrimSpace(query),
		RecencyDays: recencyDays,
		MaxResults:  maxResults,
	})
}

func needsExplicitResearch(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" {
		return false
	}
	for _, hint := range []string{"latest", "current", "today", "now", "recent", "breaking"} {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}
