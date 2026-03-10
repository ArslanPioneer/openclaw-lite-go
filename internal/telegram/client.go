package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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

var (
	headingLinePattern       = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.+?)\s*$`)
	unorderedListLinePattern = regexp.MustCompile(`^\s*[-*+]\s+(.+?)\s*$`)
	orderedListLinePattern   = regexp.MustCompile(`^\s*(\d+)\.\s+(.+?)\s*$`)
	blockquoteLinePattern    = regexp.MustCompile(`^\s*>\s?(.*)$`)
)

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
	rendered := renderTelegramHTML(text)
	htmlPayload := map[string]any{
		"chat_id":    chatID,
		"text":       rendered,
		"parse_mode": "HTML",
	}
	var ignored map[string]any
	err := c.call(ctx, "sendMessage", htmlPayload, &ignored)
	if err == nil {
		return nil
	}
	if !isTelegramParseError(err) {
		return err
	}

	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	return c.call(ctx, "sendMessage", payload, &ignored)
}

func isTelegramParseError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lower, "parse entities")
}

func renderTelegramHTML(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	rendered := make([]string, 0, len(lines))

	inCodeBlock := false
	codeLines := make([]string, 0)
	quoteLines := make([]string, 0)

	flushCodeBlock := func() {
		if !inCodeBlock {
			return
		}
		codeText := strings.Join(codeLines, "\n")
		if len(codeLines) > 0 {
			codeText += "\n"
		}
		rendered = append(rendered, "<pre><code>"+html.EscapeString(codeText)+"</code></pre>")
		codeLines = codeLines[:0]
		inCodeBlock = false
	}

	flushQuoteBlock := func() {
		if len(quoteLines) == 0 {
			return
		}
		parts := make([]string, 0, len(quoteLines))
		for _, line := range quoteLines {
			parts = append(parts, renderInlineMarkdown(line))
		}
		rendered = append(rendered, "<blockquote>"+strings.Join(parts, "\n")+"</blockquote>")
		quoteLines = quoteLines[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flushQuoteBlock()
			if inCodeBlock {
				flushCodeBlock()
			} else {
				inCodeBlock = true
				codeLines = codeLines[:0]
			}
			continue
		}
		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}
		if match := blockquoteLinePattern.FindStringSubmatch(line); match != nil {
			quoteLines = append(quoteLines, match[1])
			continue
		}
		flushQuoteBlock()

		switch {
		case trimmed == "":
			rendered = append(rendered, "")
		case headingLinePattern.MatchString(line):
			match := headingLinePattern.FindStringSubmatch(line)
			rendered = append(rendered, "<b>"+renderInlineMarkdown(match[1])+"</b>")
		case unorderedListLinePattern.MatchString(line):
			match := unorderedListLinePattern.FindStringSubmatch(line)
			rendered = append(rendered, "&#8226; "+renderInlineMarkdown(match[1]))
		case orderedListLinePattern.MatchString(line):
			match := orderedListLinePattern.FindStringSubmatch(line)
			rendered = append(rendered, match[1]+". "+renderInlineMarkdown(match[2]))
		default:
			rendered = append(rendered, renderInlineMarkdown(line))
		}
	}

	flushQuoteBlock()
	flushCodeBlock()

	return strings.Join(rendered, "\n")
}

func renderInlineMarkdown(text string) string {
	if text == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(text) + 16)
	for i := 0; i < len(text); {
		switch {
		case text[i] == '`':
			if segment, width, ok := wrapInlineSpan(text[i+1:], "`", "code", false); ok {
				b.WriteString("<code>" + html.EscapeString(segment) + "</code>")
				i += width + 1
				continue
			}
		case strings.HasPrefix(text[i:], "["):
			if rendered, width, ok := renderInlineLink(text[i:]); ok {
				b.WriteString(rendered)
				i += width
				continue
			}
		case strings.HasPrefix(text[i:], "**"):
			if segment, width, ok := wrapInlineSpan(text[i+2:], "**", "b", true); ok {
				b.WriteString("<b>" + renderInlineMarkdown(segment) + "</b>")
				i += width + 2
				continue
			}
		case strings.HasPrefix(text[i:], "__"):
			if segment, width, ok := wrapInlineSpan(text[i+2:], "__", "b", true); ok {
				b.WriteString("<b>" + renderInlineMarkdown(segment) + "</b>")
				i += width + 2
				continue
			}
		case strings.HasPrefix(text[i:], "~~"):
			if segment, width, ok := wrapInlineSpan(text[i+2:], "~~", "s", true); ok {
				b.WriteString("<s>" + renderInlineMarkdown(segment) + "</s>")
				i += width + 2
				continue
			}
		case strings.HasPrefix(text[i:], "||"):
			if segment, width, ok := wrapInlineSpan(text[i+2:], "||", "tg-spoiler", true); ok {
				b.WriteString("<tg-spoiler>" + renderInlineMarkdown(segment) + "</tg-spoiler>")
				i += width + 2
				continue
			}
		case text[i] == '*':
			if segment, width, ok := wrapInlineSpan(text[i+1:], "*", "i", true); ok {
				b.WriteString("<i>" + renderInlineMarkdown(segment) + "</i>")
				i += width + 1
				continue
			}
		case text[i] == '_':
			if segment, width, ok := wrapInlineSpan(text[i+1:], "_", "i", true); ok {
				b.WriteString("<i>" + renderInlineMarkdown(segment) + "</i>")
				i += width + 1
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			size = 1
		}
		b.WriteString(html.EscapeString(text[i : i+size]))
		i += size
	}
	return b.String()
}

func wrapInlineSpan(text string, marker string, _ string, allowNested bool) (string, int, bool) {
	end := strings.Index(text, marker)
	if end <= 0 {
		return "", 0, false
	}
	segment := text[:end]
	if strings.TrimSpace(segment) == "" {
		return "", 0, false
	}
	if !allowNested && strings.Contains(segment, "\n") {
		return "", 0, false
	}
	return segment, end + len(marker), true
}

func renderInlineLink(text string) (string, int, bool) {
	closeLabel := strings.Index(text, "](")
	if closeLabel <= 1 {
		return "", 0, false
	}
	label := text[1:closeLabel]
	rest := text[closeLabel+2:]
	closeURL := strings.Index(rest, ")")
	if closeURL <= 0 {
		return "", 0, false
	}
	href := strings.TrimSpace(rest[:closeURL])
	if !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
		return "", 0, false
	}
	rendered := `<a href="` + html.EscapeString(href) + `">` + renderInlineMarkdown(label) + `</a>`
	return rendered, closeLabel + 2 + closeURL + 1, true
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
