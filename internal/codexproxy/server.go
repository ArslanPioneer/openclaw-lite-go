package codexproxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"openclaw-lite-go/internal/tools"
)

const (
	defaultListenAddr = "127.0.0.1:8099"
	defaultTimeout    = 10 * time.Minute
	maxPromptChars    = 16000
	maxTurnsKept      = 12
)

type Config struct {
	WorkDir          string
	StateDir         string
	AuthToken        string
	CodexBin         string
	Model            string
	Timeout          time.Duration
	DangerFullAccess bool
	Executor         Executor
	Researcher       Researcher
	AuditLog         *AuditLog
	Policy           Policy
}

type Executor interface {
	Run(ctx context.Context, workdir string, args []string) ([]byte, error)
}

type Server struct {
	workdir          string
	stateDir         string
	token            string
	model            string
	dangerFullAccess bool
	exec             Executor
	research         Researcher
	audit            *AuditLog
	policy           Policy
}

type request struct {
	ChatID  int64  `json:"chat_id"`
	Message string `json:"message"`
}

type response struct {
	Reply string `json:"reply"`
}

type turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CLIExecutor struct {
	bin     string
	model   string
	timeout time.Duration
}

func NewServer(cfg Config) *Server {
	workdir := strings.TrimSpace(cfg.WorkDir)
	if workdir == "" {
		workdir = "."
	}
	stateDir := strings.TrimSpace(cfg.StateDir)
	if stateDir == "" {
		stateDir = filepath.Join(workdir, ".codexproxy")
	}
	executor := cfg.Executor
	if executor == nil {
		executor = NewCLIExecutor(cfg.CodexBin, cfg.Model, cfg.Timeout)
	}
	return &Server{
		workdir:          workdir,
		stateDir:         stateDir,
		token:            strings.TrimSpace(cfg.AuthToken),
		model:            strings.TrimSpace(cfg.Model),
		dangerFullAccess: cfg.DangerFullAccess,
		exec:             executor,
		research:         cfg.Researcher,
		audit:            cfg.AuditLog,
		policy: Policy{
			DangerFullAccess: cfg.DangerFullAccess || cfg.Policy.DangerFullAccess,
			RequireConfirm:   cfg.Policy.RequireConfirm,
		},
	}
}

func NewCLIExecutor(bin string, model string, timeout time.Duration) *CLIExecutor {
	if strings.TrimSpace(bin) == "" {
		bin = "codex"
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &CLIExecutor{
		bin:     strings.TrimSpace(bin),
		model:   strings.TrimSpace(model),
		timeout: timeout,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat", s.handleChat)
	return mux
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.token != "" && !validBearerToken(r.Header.Get("Authorization"), s.token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.ChatID == 0 || req.Message == "" {
		http.Error(w, "chat_id and message are required", http.StatusBadRequest)
		return
	}

	reply, err := s.chat(r.Context(), req.ChatID, req.Message)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{Reply: reply})
}

func (s *Server) chat(ctx context.Context, chatID int64, message string) (string, error) {
	turns, err := s.loadTurns(chatID)
	if err != nil {
		return "", err
	}

	goalID, promptMessage := extractGoalID(message)
	if s.research == nil {
		s.research = NewResearcher(nil)
	}
	if s.audit == nil {
		s.audit = NewAuditLog(s.stateDir)
	}
	decision := s.policy.Evaluate(promptMessage)
	if decision.RequiresConfirmation {
		return "", fmt.Errorf("host-critical request requires explicit confirmation")
	}
	if !decision.Allowed {
		return "", fmt.Errorf("request blocked by execution policy (%s)", decision.Risk)
	}

	researchResults := s.runResearch(ctx, promptMessage)
	prompt := buildPrompt(turns, promptMessage, researchResults)
	args := buildExecArgs(s.model, prompt, s.dangerFullAccess)
	output, err := s.exec.Run(ctx, s.workdir, args)
	if err != nil {
		s.appendAuditRecord(chatID, goalID, message, prompt, "", s.executionMode())
		return "", fmt.Errorf("codex exec failed: %w", err)
	}
	reply := parseReply(output)
	if strings.TrimSpace(reply) == "" {
		s.appendAuditRecord(chatID, goalID, message, prompt, "", s.executionMode())
		return "", fmt.Errorf("codex returned empty reply")
	}

	turns = append(turns, turn{Role: "user", Content: promptMessage}, turn{Role: "assistant", Content: reply})
	if err := s.saveTurns(chatID, turns); err != nil {
		return "", err
	}
	s.appendAuditRecord(chatID, goalID, message, prompt, reply, s.executionMode())
	return reply, nil
}

func buildExecArgs(model string, prompt string, dangerFullAccess bool) []string {
	args := []string{"exec", "--skip-git-repo-check"}
	if dangerFullAccess {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else {
		args = append(args, "--full-auto")
	}
	args = append(args, "--json")
	if strings.TrimSpace(model) != "" {
		args = append(args, "-m", strings.TrimSpace(model))
	}
	args = append(args, prompt)
	return args
}

func buildPrompt(turns []turn, message string, researchResults []tools.SearchResult) string {
	lines := []string{
		"You are continuing the same Telegram conversation.",
		"Use prior context where it matters and answer the newest user message directly.",
	}
	if needsExplicitResearch(message) {
		lines = append(lines,
			"For current/latest/time-sensitive facts, use the explicit research path first and include sources in the final answer.",
		)
	}
	if len(researchResults) > 0 {
		lines = append(lines, "", "Research context:")
		for i, result := range researchResults {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, strings.TrimSpace(result.Title)))
			lines = append(lines, "   URL: "+strings.TrimSpace(result.URL))
			if strings.TrimSpace(result.Snippet) != "" {
				lines = append(lines, "   Snippet: "+strings.TrimSpace(result.Snippet))
			}
		}
	}
	if len(turns) > 0 {
		trimmed := turns
		if len(trimmed) > maxTurnsKept {
			trimmed = trimmed[len(trimmed)-maxTurnsKept:]
		}
		lines = append(lines, "", "Conversation so far:")
		for _, item := range trimmed {
			role := "User"
			if strings.EqualFold(strings.TrimSpace(item.Role), "assistant") {
				role = "Assistant"
			}
			lines = append(lines, role+": "+strings.TrimSpace(item.Content))
		}
	}
	lines = append(lines, "", "New user message:", strings.TrimSpace(message))
	prompt := strings.Join(lines, "\n")
	if len(prompt) <= maxPromptChars {
		return prompt
	}
	return prompt[len(prompt)-maxPromptChars:]
}

func (s *Server) runResearch(ctx context.Context, message string) []tools.SearchResult {
	if s.research == nil || !needsExplicitResearch(message) {
		return nil
	}
	results, err := s.research.Research(ctx, message, 7, 5)
	if err != nil {
		return nil
	}
	return results
}

func (s *Server) executionMode() string {
	if s.dangerFullAccess {
		return "danger-full-access"
	}
	return "full-auto"
}

func (s *Server) appendAuditRecord(chatID int64, goalID string, rawMessage string, prompt string, reply string, mode string) {
	if s.audit == nil {
		return
	}
	record := AuditRecord{
		Timestamp:      time.Now().UTC(),
		ChatID:         chatID,
		GoalID:         strings.TrimSpace(goalID),
		RawUserMessage: strings.TrimSpace(rawMessage),
		PromptHash:     hashPrompt(prompt),
		FinalReply:     strings.TrimSpace(reply),
		ExecutionMode:  strings.TrimSpace(mode),
	}
	_ = s.audit.Append(record)
}

func hashPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func extractGoalID(message string) (string, string) {
	text := strings.TrimSpace(message)
	if !strings.HasPrefix(text, "[goal:") {
		return "", text
	}
	end := strings.Index(text, "]")
	if end <= len("[goal:") {
		return "", text
	}
	goalID := strings.TrimSpace(text[len("[goal:"):end])
	rest := strings.TrimSpace(text[end+1:])
	return goalID, rest
}

func parseReply(data []byte) string {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	last := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(line), &payload); err == nil {
			if candidate := extractReplyCandidate(payload); strings.TrimSpace(candidate) != "" {
				last = strings.TrimSpace(candidate)
			}
			continue
		}
		last = line
	}
	return strings.TrimSpace(last)
}

func extractReplyCandidate(value any) string {
	obj, ok := value.(map[string]any)
	if !ok {
		return ""
	}

	if candidate := firstNonEmptyString(obj, "reply", "output", "text", "message"); candidate != "" {
		return candidate
	}

	if strings.EqualFold(stringValue(obj["type"]), "item.completed") {
		if item, ok := obj["item"].(map[string]any); ok {
			if strings.EqualFold(stringValue(item["type"]), "agent_message") {
				if candidate := firstNonEmptyString(item, "text", "message", "output", "reply"); candidate != "" {
					return candidate
				}
			}
		}
	}

	return ""
}

func firstNonEmptyString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(obj[key]); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func validBearerToken(header string, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return true
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix)) == expected
}

func (s *Server) loadTurns(chatID int64) ([]turn, error) {
	path := s.turnsPath(chatID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read proxy chat state: %w", err)
	}
	var turns []turn
	if err := json.Unmarshal(data, &turns); err != nil {
		return nil, fmt.Errorf("decode proxy chat state: %w", err)
	}
	return turns, nil
}

func (s *Server) saveTurns(chatID int64, turns []turn) error {
	path := s.turnsPath(chatID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create proxy state dir: %w", err)
	}
	data, err := json.MarshalIndent(turns, "", "  ")
	if err != nil {
		return fmt.Errorf("encode proxy chat state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write proxy chat state: %w", err)
	}
	return nil
}

func (s *Server) turnsPath(chatID int64) string {
	return filepath.Join(s.stateDir, fmt.Sprintf("%d.json", chatID))
}

type lockedExecutor struct {
	inner Executor
	mu    sync.Mutex
}

func (l *lockedExecutor) Run(ctx context.Context, workdir string, args []string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Run(ctx, workdir, args)
}

func (e *CLIExecutor) Run(ctx context.Context, workdir string, args []string) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("nil cli executor")
	}
	runCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, e.bin, args...)
	cmd.Dir = workdir
	return cmd.CombinedOutput()
}

func DefaultListenAddr() string {
	return defaultListenAddr
}
