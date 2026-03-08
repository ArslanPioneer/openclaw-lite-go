package tools

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"openclaw-lite-go/internal/skills"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultDockerSocketPath  = "/var/run/docker.sock"
	defaultStockQuoteURL     = "https://query1.finance.yahoo.com/v7/finance/quote?symbols=%s"
	defaultStockQuoteURLStoq = "https://stooq.com/q/l/?s=%s&i=1"
	defaultWebSearchHTMLURL  = "https://duckduckgo.com/html/"
)

var (
	resultAnchorPattern  = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	resultSnippetPattern = regexp.MustCompile(`(?is)<(?:a|div)[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</(?:a|div)>`)
	htmlTagPattern       = regexp.MustCompile(`(?is)<[^>]+>`)
)

type Call struct {
	Name        string `json:"name"`
	Query       string `json:"query,omitempty"`
	URL         string `json:"url,omitempty"`
	Text        string `json:"text,omitempty"`
	Skill       string `json:"skill,omitempty"`
	Script      string `json:"script,omitempty"`
	Input       string `json:"input,omitempty"`
	MaxBytes    int    `json:"max_bytes,omitempty"`
	RecencyDays int    `json:"recency_days,omitempty"`
	MaxResults  int    `json:"max_results,omitempty"`
	All         bool   `json:"all,omitempty"`
}

type Executor struct {
	httpClient            *http.Client
	skills                *skills.Manager
	dockerSocketPath      string
	stockQuoteURLTemplate string
	stooqQuoteURLTemplate string
	webSearchHTMLURL      string
}

type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

func NewExecutor(timeout time.Duration, skillManager *skills.Manager) *Executor {
	return &Executor{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		skills:                skillManager,
		dockerSocketPath:      defaultDockerSocketPath,
		stockQuoteURLTemplate: defaultStockQuoteURL,
		stooqQuoteURLTemplate: defaultStockQuoteURLStoq,
		webSearchHTMLURL:      defaultWebSearchHTMLURL,
	}
}

func (e *Executor) Execute(ctx context.Context, call Call) (string, error) {
	switch strings.ToLower(strings.TrimSpace(call.Name)) {
	case "echo":
		return strings.TrimSpace(call.Text), nil
	case "web_search":
		return e.webSearch(ctx, call)
	case "http_get":
		return e.httpGet(ctx, strings.TrimSpace(call.URL))
	case "skill_install":
		return e.skillInstall(strings.TrimSpace(call.Skill))
	case "skill_list":
		return e.skillList()
	case "skill_read":
		return e.skillRead(strings.TrimSpace(call.Skill), call.MaxBytes)
	case "skill_run":
		return e.skillRun(ctx, strings.TrimSpace(call.Skill), strings.TrimSpace(call.Script), call.Input)
	case "docker_ps":
		return e.dockerPS(ctx, call.All)
	case "stock_price":
		ticker := strings.TrimSpace(call.Query)
		if ticker == "" {
			ticker = strings.TrimSpace(call.Text)
		}
		return e.stockPrice(ctx, ticker)
	default:
		return "", fmt.Errorf("unsupported tool: %s", call.Name)
	}
}

func (e *Executor) skillInstall(skill string) (string, error) {
	if e.skills == nil {
		return "", fmt.Errorf("skills manager is not configured")
	}
	path, err := e.skills.Install(skill)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("installed skill %q at %s", skill, path), nil
}

func (e *Executor) skillList() (string, error) {
	if e.skills == nil {
		return "", fmt.Errorf("skills manager is not configured")
	}
	names, err := e.skills.ListInstalled()
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "No installed skills.", nil
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

func (e *Executor) skillRead(skill string, maxBytes int) (string, error) {
	if e.skills == nil {
		return "", fmt.Errorf("skills manager is not configured")
	}
	if maxBytes <= 0 {
		maxBytes = 8000
	}
	return e.skills.ReadSkill(skill, maxBytes)
}

func (e *Executor) skillRun(ctx context.Context, skill string, script string, input string) (string, error) {
	if e.skills == nil {
		return "", fmt.Errorf("skills manager is not configured")
	}
	return e.skills.RunScript(ctx, skill, script, input)
}

func (e *Executor) dockerPS(ctx context.Context, all bool) (string, error) {
	socket := strings.TrimSpace(e.dockerSocketPath)
	if socket == "" {
		socket = defaultDockerSocketPath
	}
	if _, err := os.Stat(socket); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("docker socket not found: %s", socket)
		}
		return "", fmt.Errorf("check docker socket: %w", err)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   e.httpClient.Timeout,
		Transport: transport,
	}

	query := "0"
	if all {
		query = "1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/v1.41/containers/json?all="+query, nil)
	if err != nil {
		return "", fmt.Errorf("build docker_ps request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("docker_ps request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("docker_ps status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var containers []struct {
		Names  []string `json:"Names"`
		Image  string   `json:"Image"`
		State  string   `json:"State"`
		Status string   `json:"Status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&containers); err != nil {
		return "", fmt.Errorf("decode docker_ps response: %w", err)
	}

	if len(containers) == 0 {
		return "No containers found.", nil
	}

	lines := make([]string, 0, len(containers)+1)
	lines = append(lines, "Name | State | Status | Image")
	for i, c := range containers {
		if i >= 30 {
			lines = append(lines, "...[truncated]")
			break
		}
		name := "-"
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		lines = append(lines, fmt.Sprintf("%s | %s | %s | %s", name, c.State, c.Status, c.Image))
	}
	return strings.Join(lines, "\n"), nil
}

func (e *Executor) stockPrice(ctx context.Context, rawTicker string) (string, error) {
	ticker, err := sanitizeTicker(rawTicker)
	if err != nil {
		return "", err
	}

	if text, err := e.stockPriceFromYahoo(ctx, ticker); err == nil {
		return text, nil
	} else {
		if fallback, fallbackErr := e.stockPriceFromStooq(ctx, ticker); fallbackErr == nil {
			return fallback, nil
		}
		return "", fmt.Errorf("stock quote failed for %s", ticker)
	}
}

func (e *Executor) stockPriceFromYahoo(ctx context.Context, ticker string) (string, error) {
	target := fmt.Sprintf(e.stockQuoteURLTemplate, url.QueryEscape(ticker))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var payload struct {
		QuoteResponse struct {
			Result []struct {
				Symbol                 string  `json:"symbol"`
				RegularMarketPrice     float64 `json:"regularMarketPrice"`
				RegularMarketChangePct float64 `json:"regularMarketChangePercent"`
				RegularMarketTime      int64   `json:"regularMarketTime"`
				Currency               string  `json:"currency"`
				MarketState            string  `json:"marketState"`
			} `json:"result"`
		} `json:"quoteResponse"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.QuoteResponse.Result) == 0 {
		return "", fmt.Errorf("empty quote result")
	}
	q := payload.QuoteResponse.Result[0]
	symbol := strings.TrimSpace(q.Symbol)
	if symbol == "" {
		symbol = ticker
	}
	currency := strings.TrimSpace(q.Currency)
	if currency == "" {
		currency = "USD"
	}
	marketState := strings.TrimSpace(q.MarketState)
	if marketState == "" {
		marketState = "UNKNOWN"
	}
	timeText := "unknown"
	if q.RegularMarketTime > 0 {
		timeText = time.Unix(q.RegularMarketTime, 0).UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s: %.2f %s (change %.2f%%, market %s, time %s)", symbol, q.RegularMarketPrice, currency, q.RegularMarketChangePct, marketState, timeText), nil
}

func (e *Executor) stockPriceFromStooq(ctx context.Context, ticker string) (string, error) {
	symbol := strings.ToLower(strings.TrimSpace(ticker))
	if !strings.Contains(symbol, ".") {
		symbol += ".us"
	}
	target := fmt.Sprintf(e.stooqQuoteURLTemplate, url.QueryEscape(symbol))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	reader := csv.NewReader(bytes.NewReader(data))
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 2 || len(rows[1]) < 7 {
		return "", fmt.Errorf("invalid stooq payload")
	}
	row := rows[1]
	closePrice := strings.TrimSpace(row[6])
	if closePrice == "" || strings.EqualFold(closePrice, "N/D") {
		return "", fmt.Errorf("stooq no close price")
	}
	date := strings.TrimSpace(row[1])
	clock := strings.TrimSpace(row[2])
	return fmt.Sprintf("%s: %s USD (source stooq, date %s %s)", strings.ToUpper(ticker), closePrice, date, clock), nil
}

func sanitizeTicker(raw string) (string, error) {
	ticker := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(raw, "$")))
	if ticker == "" {
		return "", fmt.Errorf("stock ticker is required")
	}
	if len(ticker) > 15 {
		return "", fmt.Errorf("stock ticker is too long")
	}
	for _, ch := range ticker {
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '-' {
			continue
		}
		return "", fmt.Errorf("invalid stock ticker: %s", raw)
	}
	return ticker, nil
}

func (e *Executor) webSearch(ctx context.Context, call Call) (string, error) {
	results, err := e.WebSearchResults(ctx, call)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No citeable search sources found.", nil
	}

	query := strings.TrimSpace(call.Query)
	lines := make([]string, 0, 2+len(results)*3)
	lines = append(lines, "Query: "+query)
	if call.RecencyDays > 0 {
		lines = append(lines, fmt.Sprintf("Freshness: last %d day(s)", call.RecencyDays))
	}
	lines = append(lines, "Sources:")
	for i, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, result.Title))
		lines = append(lines, "   URL: "+result.URL)
		if result.Snippet != "" {
			lines = append(lines, "   Snippet: "+clipString(result.Snippet, 280))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (e *Executor) WebSearchResults(ctx context.Context, call Call) ([]SearchResult, error) {
	query := strings.TrimSpace(call.Query)
	if query == "" {
		return nil, fmt.Errorf("web_search query is required")
	}

	maxResults := call.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	values := url.Values{}
	values.Set("q", query)
	if df := recencyBucket(call.RecencyDays); df != "" {
		values.Set("df", df)
	}

	searchURL := strings.TrimSpace(e.webSearchHTMLURL)
	if searchURL == "" {
		searchURL = defaultWebSearchHTMLURL
	}
	if strings.Contains(searchURL, "?") {
		searchURL = searchURL + "&" + values.Encode()
	} else {
		searchURL = strings.TrimRight(searchURL, "?") + "?" + values.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build web_search request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; OpenClawLite/1.0)")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web_search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("web_search status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read web_search response: %w", err)
	}

	return extractSearchResults(string(data), maxResults), nil
}

func extractSearchResults(payload string, maxResults int) []SearchResult {
	matches := resultAnchorPattern.FindAllStringSubmatchIndex(payload, -1)
	if len(matches) == 0 {
		return nil
	}

	results := make([]SearchResult, 0, maxResults)
	for i, match := range matches {
		if len(match) < 6 {
			continue
		}

		href := payload[match[2]:match[3]]
		title := sanitizeHTMLText(payload[match[4]:match[5]])
		if title == "" {
			continue
		}

		urlText := normalizeSearchResultURL(href)
		if urlText == "" {
			continue
		}

		segmentStart := match[1]
		segmentEnd := len(payload)
		if i+1 < len(matches) {
			segmentEnd = matches[i+1][0]
		}
		snippet := ""
		if segmentStart >= 0 && segmentStart < segmentEnd && segmentEnd <= len(payload) {
			segment := payload[segmentStart:segmentEnd]
			if snippetMatch := resultSnippetPattern.FindStringSubmatch(segment); len(snippetMatch) > 1 {
				snippet = sanitizeHTMLText(snippetMatch[1])
			}
		}

		results = append(results, SearchResult{
			Title:   title,
			URL:     urlText,
			Snippet: snippet,
		})
		if len(results) >= maxResults {
			break
		}
	}
	return results
}

func normalizeSearchResultURL(raw string) string {
	target := strings.TrimSpace(html.UnescapeString(raw))
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "//") {
		target = "https:" + target
	}
	if strings.HasPrefix(target, "/") {
		target = "https://duckduckgo.com" + target
	}

	parsed, err := url.Parse(target)
	if err != nil {
		return target
	}
	if strings.Contains(strings.ToLower(parsed.Host), "duckduckgo.com") {
		redirectURL := strings.TrimSpace(parsed.Query().Get("uddg"))
		if redirectURL != "" {
			decoded, decodeErr := url.QueryUnescape(redirectURL)
			if decodeErr == nil && strings.TrimSpace(decoded) != "" {
				return decoded
			}
			return redirectURL
		}
	}
	return target
}

func sanitizeHTMLText(raw string) string {
	withoutTags := htmlTagPattern.ReplaceAllString(raw, " ")
	unescaped := html.UnescapeString(withoutTags)
	return strings.Join(strings.Fields(unescaped), " ")
}

func recencyBucket(recencyDays int) string {
	if recencyDays <= 0 {
		return ""
	}
	if recencyDays <= 1 {
		return "d"
	}
	if recencyDays <= 7 {
		return "w"
	}
	if recencyDays <= 31 {
		return "m"
	}
	return "y"
}

func clipString(raw string, max int) string {
	if len(raw) <= max {
		return raw
	}
	return strings.TrimSpace(raw[:max]) + "..."
}

func (e *Executor) httpGet(ctx context.Context, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("http_get url is required")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("only http/https urls are allowed")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", fmt.Errorf("build http_get request: %w", err)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http_get request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("http_get status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4000))
	if err != nil {
		return "", fmt.Errorf("read http_get response: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}
