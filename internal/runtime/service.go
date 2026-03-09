package runtime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"openclaw-lite-go/internal/codexproxy"
	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/memory"
	"openclaw-lite-go/internal/skills"
	"openclaw-lite-go/internal/telegram"
	"openclaw-lite-go/internal/tools"
)

const (
	defaultAgentLoopMaxSteps     = 4
	defaultMaxParseFailures      = 2
	defaultMaxToolOutputChars    = 2400
	defaultMaxOverflowRecoveries = 2
	defaultCodexModel            = "gpt-5-codex"
)

type executionMode string

const (
	executionModeLegacy executionMode = "legacy"
	executionModeCodex  executionMode = "codex"
)

var tickerPattern = regexp.MustCompile(`(?i)\$?[A-Z]{1,5}(?:\.[A-Z]{1,3})?`)

type TelegramClient interface {
	GetUpdates(ctx context.Context, offset int64, timeoutSecond int) ([]telegram.Update, error)
	SendMessage(ctx context.Context, chatID int64, text string) error
}

type AgentClient interface {
	GenerateReply(ctx context.Context, userText string, modelOverride string) (string, error)
}

type ToolExecutor interface {
	Execute(ctx context.Context, call tools.Call) (string, error)
}

type CodexProxy interface {
	Chat(ctx context.Context, chatID int64, message string) (string, error)
}

type Service struct {
	cfg      config.Config
	bot      TelegramClient
	agent    AgentClient
	offset   int64
	health   *HealthState
	store    *memory.Store
	sessions *SessionStore
	goals    *GoalStore
	confirms *ConfirmStore
	runner   *GoalRunner
	tools    ToolExecutor
	skills   *skills.Manager
	codex    CodexProxy

	mu          sync.RWMutex
	activeModel map[int64]string
	chatMode    map[int64]executionMode

	dedupMu sync.Mutex
	seen    map[int64]struct{}
}

func NewService(cfg config.Config, bot TelegramClient, agent AgentClient) *Service {
	cfg.ApplyDefaults()
	skillManager := skills.NewManager(cfg.Runtime.SkillsSourceDir, cfg.Runtime.SkillsInstallDir)
	memStore := memory.NewStore(cfg.Runtime.DataDir, cfg.Runtime.HistoryTurns)
	var codexProxy CodexProxy
	if strings.TrimSpace(cfg.Runtime.CodexProxyURL) != "" {
		codexProxy = NewHTTPCodexProxy(
			cfg.Runtime.CodexProxyURL,
			cfg.Runtime.CodexProxyToken,
			time.Duration(cfg.Runtime.CodexProxyTimeout)*time.Second,
		)
	}
	svc := &Service{
		cfg:         cfg,
		bot:         bot,
		agent:       agent,
		store:       memStore,
		sessions:    NewSessionStore(memStore.DataDir()),
		goals:       NewGoalStore(memStore.DataDir()),
		confirms:    NewConfirmStore(memStore.DataDir()),
		tools:       tools.NewExecutor(12*time.Second, skillManager),
		skills:      skillManager,
		codex:       codexProxy,
		activeModel: make(map[int64]string),
		chatMode:    make(map[int64]executionMode),
		seen:        make(map[int64]struct{}),
	}
	svc.runner = NewGoalRunner(&serviceGoalStepExecutor{service: svc}, svc.goals, svc.sessions, bot, nil)
	return svc
}

func (s *Service) SetToolExecutor(exec ToolExecutor) {
	if exec == nil {
		return
	}
	s.tools = exec
}

func (s *Service) SetCodexProxy(proxy CodexProxy) {
	s.codex = proxy
}

func (s *Service) AttachHealthState(health *HealthState) {
	s.health = health
	if s.runner != nil {
		s.runner.SetHealthState(health)
	}
}

func (s *Service) SetGoalRunner(runner *GoalRunner) {
	s.runner = runner
	if s.runner != nil && s.health != nil {
		s.runner.SetHealthState(s.health)
	}
}

func (s *Service) Run(ctx context.Context) error {
	updates := make(chan telegram.Update, s.cfg.Runtime.QueueSize)
	var wg sync.WaitGroup

	if s.runner != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runner.Run(ctx)
		}()
	}

	for i := 0; i < s.cfg.Runtime.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for update := range updates {
				_ = s.ProcessUpdate(ctx, update)
			}
		}()
	}

	defer func() {
		close(updates)
		wg.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		batch, err := s.bot.GetUpdates(ctx, s.offset, s.cfg.Runtime.PollTimeoutSecond)
		if err != nil {
			if s.health != nil {
				s.health.RecordPollError(err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(1 * time.Second):
			}
			continue
		}
		if s.health != nil {
			s.health.RecordPollSuccess()
		}

		for _, update := range batch {
			if update.UpdateID >= s.offset {
				s.offset = update.UpdateID + 1
			}
			select {
			case updates <- update:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (s *Service) ProcessUpdate(ctx context.Context, update telegram.Update) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered while handling update %d: %v", update.UpdateID, r)
			if s.health != nil {
				s.health.RecordPollError(err)
			}
		}
	}()
	err = s.HandleUpdate(ctx, update)
	if err != nil && !errors.Is(err, context.Canceled) && s.health != nil {
		s.health.RecordPollError(err)
	}
	return err
}

func (s *Service) HandleUpdate(ctx context.Context, update telegram.Update) error {
	if update.UpdateID > 0 && s.seenBefore(update.UpdateID) {
		return nil
	}
	msg := update.Message
	if msg == nil {
		return nil
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil
	}

	chatID := msg.Chat.ID
	if strings.HasPrefix(text, "/start") {
		return s.bot.SendMessage(ctx, chatID, "ClawLite started. Send a message to chat. Use /agent <model> to switch model. Use /codex to switch to Codex model. Use /agentmode legacy|codex to switch execution mode. Use /codexcli on|off as a compatibility alias. Use /confirm to approve pending host-critical codex actions. Use /skills to list installable skills. Use /price <ticker> for direct stock quote. Use /version to see build version.")
	}
	if strings.HasPrefix(text, "/version") {
		return s.bot.SendMessage(ctx, chatID, "ClawLite version: "+BuildVersionString())
	}

	if strings.HasPrefix(text, "/agentmode") {
		return s.handleAgentModeCommand(ctx, chatID, text)
	}
	if strings.HasPrefix(text, "/agent") {
		return s.handleAgentCommand(ctx, chatID, text)
	}
	if strings.HasPrefix(text, "/codexcli") {
		return s.handleCodexCLICommand(ctx, chatID, text)
	}
	if strings.HasPrefix(text, "/codex") {
		return s.handleCodexCommand(ctx, chatID, text)
	}
	if strings.HasPrefix(text, "/price") {
		return s.handlePriceCommand(ctx, chatID, text)
	}
	if strings.HasPrefix(text, "/goalstop") {
		return s.handleGoalStopCommand(ctx, chatID)
	}
	if strings.HasPrefix(text, "/confirm") {
		return s.handleConfirmCommand(ctx, chatID)
	}
	if strings.HasPrefix(text, "/goals") {
		return s.handleGoalsCommand(ctx, chatID)
	}
	if strings.HasPrefix(text, "/goal") {
		return s.handleGoalCommand(ctx, chatID)
	}
	if strings.HasPrefix(text, "/skills") {
		return s.handleSkillsCommand(ctx, chatID, text)
	}

	goal, err := s.createGoalFromMessage(chatID, text)
	if err != nil {
		return err
	}
	mode := s.getExecutionMode(chatID)
	if mode == executionModeCodex && s.codex == nil {
		s.updateGoal(chatID, goal.ID, GoalResult{
			Err: fmt.Errorf("codex proxy is not configured; switch to /agentmode legacy to use the fallback agent"),
		})
		return s.bot.SendMessage(ctx, chatID, "Codex execution mode is enabled, but the Codex proxy is not configured. Use /agentmode legacy to run the fallback agent explicitly.")
	}
	if mode == executionModeCodex {
		if s.runner == nil {
			s.updateGoal(chatID, goal.ID, GoalResult{
				Err: fmt.Errorf("goal runner is not configured"),
			})
			return s.bot.SendMessage(ctx, chatID, "Codex goal runner is unavailable. Please retry.")
		}
		risk := codexproxy.ClassifyRisk(text)
		if risk == codexproxy.RiskLevelHostCritical {
			if s.confirms == nil {
				s.updateGoal(chatID, goal.ID, GoalResult{
					Err: fmt.Errorf("confirmation store is not configured"),
				})
				return s.bot.SendMessage(ctx, chatID, "Host-critical confirmation flow is unavailable. Please retry.")
			}
			pending := PendingConfirmation{
				GoalID:     goal.ID,
				RawRequest: text,
				RiskLevel:  string(risk),
				CreatedAt:  time.Now().UTC(),
			}
			if err := s.confirms.Save(chatID, pending); err != nil {
				s.updateGoal(chatID, goal.ID, GoalResult{Err: err})
				return s.bot.SendMessage(ctx, chatID, "Failed to save pending confirmation. Please retry.")
			}
			s.updateGoal(chatID, goal.ID, GoalResult{
				WaitingInput: true,
				Summary:      "Host-critical action pending explicit /confirm.",
			})
			s.recordSessionActivity(chatID, func(state *SessionState) {
				state.PendingConfirmation = true
				state.LastActivity = time.Now().UTC()
			})
			return s.bot.SendMessage(ctx, chatID, "Host-critical request detected. Reply /confirm to continue.")
		}
		if s.confirms != nil {
			if err := s.confirms.Clear(chatID); err != nil && s.health != nil {
				s.health.RecordPollError(fmt.Errorf("clear pending confirmation failed: %w", err))
			}
		}
		s.recordSessionActivity(chatID, func(state *SessionState) {
			state.PendingConfirmation = false
			state.LastActivity = time.Now().UTC()
		})
		s.updateGoal(chatID, goal.ID, GoalResult{Summary: "queued for background execution"})
		if err := s.runner.Enqueue(chatID, goal.ID); err != nil {
			s.updateGoal(chatID, goal.ID, GoalResult{Err: err})
			return s.bot.SendMessage(ctx, chatID, "Failed to queue codex goal. Please retry.")
		}
		return s.bot.SendMessage(ctx, chatID, "Goal queued. Running in background.")
	}
	if ticker, ok := extractTickerFromStockQuery(text); ok {
		stockReply, stockErr := s.lookupStockQuote(ctx, ticker)
		if stockErr == nil {
			s.updateGoal(chatID, goal.ID, GoalResult{Done: true, Summary: stockReply})
			if err := s.store.AppendExchange(chatID, text, stockReply); err != nil && s.health != nil {
				s.health.RecordPollError(fmt.Errorf("memory append failed: %w", err))
			}
			return s.bot.SendMessage(ctx, chatID, stockReply)
		}
	}

	s.updateGoal(chatID, goal.ID, GoalResult{Running: true, Summary: "started"})
	model := s.getActiveModel(chatID)
	prompt := s.buildPromptFromMemory(chatID, text)
	reply, err := s.runAgentLoop(ctx, prompt, model)
	if err != nil {
		s.updateGoal(chatID, goal.ID, GoalResult{Err: err})
		if s.health != nil {
			s.health.RecordPollError(fmt.Errorf("agent loop failed: %w", err))
		}
		reply = s.recoverReplyWithoutExposingInternalError(ctx, prompt, model, err)
	}
	reply = s.repairNonActionableReply(ctx, prompt, model, text, reply)
	s.updateGoal(chatID, goal.ID, GoalResult{Done: true, Summary: reply})

	if err := s.store.AppendExchange(chatID, text, reply); err != nil && s.health != nil {
		s.health.RecordPollError(fmt.Errorf("memory append failed: %w", err))
	}
	s.recordSessionActivity(chatID, func(state *SessionState) {
		state.LastActivity = time.Now().UTC()
	})
	return s.bot.SendMessage(ctx, chatID, reply)
}

type serviceGoalStepExecutor struct {
	service *Service
}

func (e *serviceGoalStepExecutor) ExecuteGoalStep(ctx context.Context, goal Goal) (GoalStep, error) {
	if e == nil || e.service == nil {
		return GoalStep{}, fmt.Errorf("goal step executor is not configured")
	}
	s := e.service
	if s.codex == nil {
		return GoalStep{}, fmt.Errorf("codex proxy is not configured")
	}

	reply, err := s.codex.Chat(ctx, goal.ChatID, formatGoalProxyMessage(goal, goal.Objective))
	if err != nil {
		return GoalStep{}, err
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "Codex proxy returned empty response."
	}

	if err := s.store.AppendExchange(goal.ChatID, goal.Objective, reply); err != nil && s.health != nil {
		s.health.RecordPollError(fmt.Errorf("memory append failed: %w", err))
	}
	s.recordSessionActivity(goal.ChatID, func(state *SessionState) {
		state.ExecutionMode = string(executionModeCodex)
		state.LastCodexResultSummary = summarizeSessionText(reply, 240)
		state.LastActivity = time.Now().UTC()
	})

	return parseCodexGoalStep(reply), nil
}

func parseCodexGoalStep(reply string) GoalStep {
	text := strings.TrimSpace(reply)
	if text == "" {
		return GoalStep{Status: GoalStatusDone, Message: "Codex proxy returned empty response."}
	}

	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, "running:"):
		return GoalStep{Status: GoalStatusRunning, Message: strings.TrimSpace(text[len("running:"):])}
	case strings.HasPrefix(lower, "blocked:"):
		return GoalStep{Status: GoalStatusBlocked, Message: strings.TrimSpace(text[len("blocked:"):])}
	case strings.HasPrefix(lower, "wait_input:"):
		return GoalStep{Status: goalStatusWaitingInput, Message: strings.TrimSpace(text[len("wait_input:"):])}
	case strings.HasPrefix(lower, "done:"):
		return GoalStep{Status: GoalStatusDone, Message: strings.TrimSpace(text[len("done:"):])}
	default:
		return GoalStep{Status: GoalStatusDone, Message: text}
	}
}

func formatGoalProxyMessage(goal Goal, message string) string {
	text := strings.TrimSpace(message)
	if text == "" {
		text = strings.TrimSpace(goal.Objective)
	}
	if strings.TrimSpace(goal.ID) == "" {
		return text
	}
	return fmt.Sprintf("[goal:%s] %s", strings.TrimSpace(goal.ID), text)
}

func (s *Service) handlePriceCommand(ctx context.Context, chatID int64, text string) error {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) < 2 {
		return s.bot.SendMessage(ctx, chatID, "Usage: /price <ticker>")
	}
	ticker := strings.TrimSpace(parts[1])
	if ticker == "" {
		return s.bot.SendMessage(ctx, chatID, "Usage: /price <ticker>")
	}

	stockReply, err := s.lookupStockQuote(ctx, ticker)
	if err != nil {
		if s.health != nil {
			s.health.RecordPollError(fmt.Errorf("quote lookup failed for %s: %w", ticker, err))
		}
		return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("quote lookup failed for %s. Please try again later.", strings.ToUpper(ticker)))
	}
	return s.bot.SendMessage(ctx, chatID, stockReply)
}

func (s *Service) lookupStockQuote(ctx context.Context, ticker string) (string, error) {
	normalizedTicker := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(ticker, "$")))
	if normalizedTicker == "" {
		return "", fmt.Errorf("stock ticker is required")
	}

	stockReply, stockErr := s.tools.Execute(ctx, tools.Call{Name: "stock_price", Query: normalizedTicker})
	if stockErr == nil && strings.TrimSpace(stockReply) != "" {
		return stockReply, nil
	}

	searchQuery := fmt.Sprintf("%s stock price latest", normalizedTicker)
	searchReply, searchErr := s.tools.Execute(ctx, tools.Call{Name: "web_search", Query: searchQuery})
	if searchErr == nil && strings.TrimSpace(searchReply) != "" {
		return searchReply, nil
	}

	if stockErr != nil && searchErr != nil {
		return "", fmt.Errorf("stock_price failed: %w; web_search failed: %v", stockErr, searchErr)
	}
	if stockErr != nil {
		return "", stockErr
	}
	if searchErr != nil {
		return "", searchErr
	}
	return "", fmt.Errorf("empty quote response")
}

func (s *Service) runAgentLoop(ctx context.Context, basePrompt string, model string) (string, error) {
	workingPrompt := basePrompt
	orchestrator := NewOrchestrator(defaultAgentLoopMaxSteps)
	forceReason := "Tool-call limit reached."
	var pendingMutationFailure *tools.PendingMutationFailure
	recoveryState := ContextRecoveryState{}

	for orchestrator.BeginStep() {
		step := orchestrator.State().Step
		reply, err := s.callAgentWithRetry(ctx, workingPrompt, model)
		if err != nil {
			return "", err
		}

		toolCall, requested, parseErr := tools.ParseToolCall(reply)
		if requested && parseErr != nil {
			parseFailures := orchestrator.RecordParseFailure()
			workingPrompt = workingPrompt +
				fmt.Sprintf("\n\nStep %d result: invalid tool-call format (%v).", step, parseErr) +
				"\nReturn either:" +
				"\n1) exactly one valid TOOL_CALL JSON line; or" +
				"\n2) final user answer without TOOL_CALL."
			if parseFailures >= defaultMaxParseFailures {
				forceReason = "Repeated invalid tool-call formatting detected."
				break
			}
			continue
		}
		if !requested {
			if pendingMutationFailure != nil && shouldBlockSuccessAfterMutationFailure(reply) {
				return unresolvedMutationMessage(*pendingMutationFailure), nil
			}
			return reply, nil
		}

		toolOutput, toolErr := s.tools.Execute(ctx, toolCall)
		repeatedToolError := orchestrator.RecordToolResult(toolCall, toolErr)
		if tools.IsMutatingCall(toolCall) {
			if toolErr != nil {
				failure := tools.NewPendingMutationFailure(toolCall, toolErr)
				pendingMutationFailure = &failure
			} else if pendingMutationFailure != nil && pendingMutationFailure.Matches(toolCall) {
				pendingMutationFailure = nil
			}
		}
		if toolErr != nil {
			toolOutput = "tool execution error: " + toolErr.Error()
		}
		overflowExceeded := false
		if len(toolOutput) > defaultMaxToolOutputChars {
			overflowExceeded = recoveryState.RecordOverflow(defaultMaxOverflowRecoveries)
			toolOutput = TruncateToolOutputForContext(toolOutput, defaultMaxToolOutputChars)
		}

		workingPrompt = workingPrompt +
			fmt.Sprintf("\n\nStep %d tool call: %s", step, toolCall.Name) +
			"\nTool result:\n" + toolOutput +
			"\n\nReflect on whether more information is required." +
			"\nIf another tool is required, return exactly one TOOL_CALL line." +
			"\nIf enough information is available, answer the user directly."
		if pendingMutationFailure != nil {
			workingPrompt = workingPrompt +
				fmt.Sprintf("\nUnresolved mutating action failure: tool=%s error=%s.", pendingMutationFailure.Tool, pendingMutationFailure.Error) +
				"\nDo not claim this action succeeded unless the exact same action succeeds."
		}
		if repeatedToolError {
			forceReason = "Repeated tool execution errors detected for the same action."
			break
		}
		if overflowExceeded {
			forceReason = "Context overflow persisted after recovery attempts."
			break
		}
	}

	forcedPrompt := workingPrompt +
		"\n\n" + forceReason +
		"\nDo not call tools anymore." +
		"\nAnswer the user directly using gathered context."
	finalReply, err := s.callAgentWithRetry(ctx, forcedPrompt, model)
	if err != nil {
		return "", err
	}
	if _, requested, _ := tools.ParseToolCall(finalReply); requested {
		return "I reached the tool-call limit for this request. Please narrow the scope and retry.", nil
	}
	if pendingMutationFailure != nil && shouldBlockSuccessAfterMutationFailure(finalReply) {
		return unresolvedMutationMessage(*pendingMutationFailure), nil
	}
	return finalReply, nil
}

func shouldBlockSuccessAfterMutationFailure(reply string) bool {
	text := strings.ToLower(strings.TrimSpace(reply))
	if text == "" {
		return true
	}

	failureHints := []string{
		"fail", "failed", "error", "unable", "could not", "cannot",
		"not completed", "retry", "rollback", "reverted",
		"失败", "无法", "未完成", "重试",
	}
	for _, hint := range failureHints {
		if strings.Contains(text, hint) {
			return false
		}
	}
	return true
}

func unresolvedMutationMessage(pending tools.PendingMutationFailure) string {
	toolName := strings.TrimSpace(pending.Tool)
	if toolName == "" {
		toolName = "mutating tool"
	}
	return fmt.Sprintf("The mutating action %s failed and is still unresolved (%s). Retry the same action and confirm success before claiming completion.", toolName, strings.TrimSpace(pending.Error))
}

func (s *Service) recoverReplyWithoutExposingInternalError(ctx context.Context, prompt string, model string, runErr error) string {
	fallbackPrompt := prompt +
		"\n\nInternal execution failed in previous attempt." +
		"\nDo not call tools." +
		"\nProvide the best direct answer you can with uncertainty noted briefly."
	fallbackReply, err := s.callAgentWithRetry(ctx, fallbackPrompt, model)
	if err == nil && strings.TrimSpace(fallbackReply) != "" {
		return strings.TrimSpace(fallbackReply)
	}

	classificationErr := runErr
	if err != nil {
		classificationErr = err
	}
	return FormatUserFacingExecutionError(ClassifyExecutionError(classificationErr))
}

func (s *Service) repairNonActionableReply(ctx context.Context, prompt string, model string, userText string, reply string) string {
	if !isNonActionableReply(userText, reply) {
		return reply
	}

	repairPrompt := prompt +
		"\n\nYour previous response was non-actionable:\n" + reply +
		"\n\nRewrite as a direct actionable answer." +
		"\nRules:" +
		"\n- Do not mention limitations/capabilities/apologies." +
		"\n- If tool data is unavailable, give the best next concrete step in one sentence." +
		"\n- Keep it concise."
	repaired, err := s.callAgentWithRetry(ctx, repairPrompt, model)
	if err != nil || strings.TrimSpace(repaired) == "" {
		return reply
	}
	return strings.TrimSpace(repaired)
}

func (s *Service) handleAgentCommand(ctx context.Context, chatID int64, text string) error {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		current := s.getActiveModel(chatID)
		return s.bot.SendMessage(ctx, chatID, "Usage: /agent <model>\nCurrent model: "+current)
	}
	model := strings.TrimSpace(parts[1])
	s.mu.Lock()
	s.activeModel[chatID] = model
	s.mu.Unlock()
	return s.bot.SendMessage(ctx, chatID, "Model switched to: "+model)
}

func (s *Service) handleCodexCommand(ctx context.Context, chatID int64, text string) error {
	parts := strings.Fields(text)
	if len(parts) > 1 && strings.EqualFold(strings.TrimSpace(parts[1]), "off") {
		model := s.cfg.Agent.Model
		s.mu.Lock()
		s.activeModel[chatID] = model
		s.mu.Unlock()
		return s.bot.SendMessage(ctx, chatID, "Codex mode disabled. Model switched to: "+model)
	}

	model := defaultCodexModel
	if len(parts) > 1 {
		override := strings.TrimSpace(parts[1])
		if override != "" {
			model = override
		}
	}

	s.mu.Lock()
	s.activeModel[chatID] = model
	s.mu.Unlock()
	return s.bot.SendMessage(ctx, chatID, "Codex mode enabled. Model switched to: "+model)
}

func (s *Service) handleCodexCLICommand(ctx context.Context, chatID int64, text string) error {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) < 2 {
		if s.isCodexPassThru(chatID) {
			return s.bot.SendMessage(ctx, chatID, "Codex execution mode is ON. Use /agentmode legacy or /codexcli off to disable.")
		}
		return s.bot.SendMessage(ctx, chatID, "Codex execution mode is OFF. Use /agentmode codex or /codexcli on to enable.")
	}

	switch strings.ToLower(strings.TrimSpace(parts[1])) {
	case "on":
		return s.setChatExecutionMode(ctx, chatID, executionModeCodex, "Codex execution mode enabled for this chat.")
	case "off":
		return s.setChatExecutionMode(ctx, chatID, executionModeLegacy, "Legacy agent mode enabled for this chat.")
	default:
		return s.bot.SendMessage(ctx, chatID, "Usage: /codexcli [on|off]")
	}
}

func (s *Service) handleAgentModeCommand(ctx context.Context, chatID int64, text string) error {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) < 2 {
		return s.bot.SendMessage(ctx, chatID, "Usage: /agentmode <legacy|codex>\nCurrent mode: "+string(s.getExecutionMode(chatID)))
	}

	switch strings.ToLower(strings.TrimSpace(parts[1])) {
	case string(executionModeLegacy):
		return s.setChatExecutionMode(ctx, chatID, executionModeLegacy, "Legacy agent mode enabled for this chat.")
	case string(executionModeCodex):
		return s.setChatExecutionMode(ctx, chatID, executionModeCodex, "Codex execution mode enabled for this chat.")
	default:
		return s.bot.SendMessage(ctx, chatID, "Usage: /agentmode <legacy|codex>")
	}
}

func (s *Service) handleCodexProxyChat(ctx context.Context, chatID int64, text string) error {
	if s.codex == nil {
		s.setCodexPassThru(chatID, false)
		return s.bot.SendMessage(ctx, chatID, "Codex proxy is not configured. Proxy mode has been turned off.")
	}

	reply, err := s.codex.Chat(ctx, chatID, text)
	if err != nil {
		s.updateActiveGoal(chatID, GoalResult{Err: err})
		if s.health != nil {
			s.health.RecordPollError(fmt.Errorf("codex proxy failed: %w", err))
		}
		return s.bot.SendMessage(ctx, chatID, "Codex proxy request failed. Please retry.")
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "Codex proxy returned empty response."
	}
	s.updateActiveGoal(chatID, GoalResult{Done: true, Summary: reply})

	if err := s.store.AppendExchange(chatID, text, reply); err != nil && s.health != nil {
		s.health.RecordPollError(fmt.Errorf("memory append failed: %w", err))
	}
	s.recordSessionActivity(chatID, func(state *SessionState) {
		state.ExecutionMode = string(executionModeCodex)
		state.LastCodexResultSummary = summarizeSessionText(reply, 240)
		state.LastActivity = time.Now().UTC()
	})
	return s.bot.SendMessage(ctx, chatID, reply)
}

func (s *Service) handleSkillsCommand(ctx context.Context, chatID int64, text string) error {
	if s.skills == nil {
		return s.bot.SendMessage(ctx, chatID, "skills manager is not configured")
	}
	parts := strings.Fields(strings.TrimSpace(text))
	mode := "available"
	if len(parts) > 1 {
		mode = strings.ToLower(strings.TrimSpace(parts[1]))
	}

	switch mode {
	case "installed":
		names, err := s.skills.ListInstalled()
		if err != nil {
			return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("list skills failed: %v", err))
		}
		if len(names) == 0 {
			return s.bot.SendMessage(ctx, chatID, "No skills found.")
		}
		return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("Skills (%s):\n%s", mode, strings.Join(names, "\n")))
	case "available":
		names, err := s.skills.ListAvailable()
		if err != nil {
			return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("list skills failed: %v", err))
		}
		if len(names) == 0 {
			return s.bot.SendMessage(ctx, chatID, "No skills found.")
		}
		return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("Skills (%s):\n%s", mode, strings.Join(names, "\n")))
	case "sync":
		names, err := s.skills.ListAvailable()
		if err != nil {
			return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("list skills failed: %v", err))
		}
		if len(names) == 0 {
			return s.bot.SendMessage(ctx, chatID, "No skills found.")
		}

		installedCount := 0
		failed := make([]string, 0)
		for _, name := range names {
			if _, err := s.skills.Install(name); err != nil {
				failed = append(failed, fmt.Sprintf("%s (%v)", name, err))
				continue
			}
			installedCount++
		}

		if len(failed) > 0 {
			return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("Installed %d skills, failed %d:\n%s", installedCount, len(failed), strings.Join(failed, "\n")))
		}
		return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("Installed %d skills.", installedCount))
	case "install":
		if len(parts) < 3 {
			return s.bot.SendMessage(ctx, chatID, "Usage: /skills install <skill_name>")
		}
		name := strings.TrimSpace(parts[2])
		if name == "" {
			return s.bot.SendMessage(ctx, chatID, "Usage: /skills install <skill_name>")
		}
		path, err := s.skills.Install(name)
		if err != nil {
			return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("install skill failed: %v", err))
		}
		return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("Installed skill %s at %s", name, path))
	default:
		return s.bot.SendMessage(ctx, chatID, "Usage: /skills [available|installed|sync|install <skill_name>]")
	}
}

func (s *Service) getActiveModel(chatID int64) string {
	s.mu.RLock()
	model, ok := s.activeModel[chatID]
	s.mu.RUnlock()
	if ok && strings.TrimSpace(model) != "" {
		return model
	}
	return s.cfg.Agent.Model
}

func (s *Service) isCodexPassThru(chatID int64) bool {
	return s.getExecutionMode(chatID) == executionModeCodex
}

func (s *Service) setCodexPassThru(chatID int64, enabled bool) {
	mode := executionModeLegacy
	if enabled {
		mode = executionModeCodex
	}
	s.setExecutionMode(chatID, mode)
}

func (s *Service) getExecutionMode(chatID int64) executionMode {
	s.mu.RLock()
	mode, ok := s.chatMode[chatID]
	s.mu.RUnlock()
	if ok {
		return mode
	}
	if s.sessions != nil {
		state, err := s.sessions.Load(chatID)
		if err != nil {
			if s.health != nil {
				s.health.RecordPollError(fmt.Errorf("session load failed: %w", err))
			}
		} else if persistedMode, ok := parseExecutionMode(state.ExecutionMode); ok {
			s.setExecutionMode(chatID, persistedMode)
			return persistedMode
		}
	}
	if s.cfg.Runtime.CodexFirstDefault {
		return executionModeCodex
	}
	return executionModeLegacy
}

func (s *Service) setExecutionMode(chatID int64, mode executionMode) {
	s.mu.Lock()
	s.chatMode[chatID] = mode
	s.mu.Unlock()
	s.recordSessionActivity(chatID, func(state *SessionState) {
		state.ExecutionMode = string(mode)
		state.LastActivity = time.Now().UTC()
	})
}

func (s *Service) setChatExecutionMode(ctx context.Context, chatID int64, mode executionMode, successMessage string) error {
	if mode == executionModeCodex && s.codex == nil {
		return s.bot.SendMessage(ctx, chatID, "Codex proxy is not configured. Set runtime.codex_proxy_url first.")
	}
	s.setExecutionMode(chatID, mode)
	return s.bot.SendMessage(ctx, chatID, successMessage)
}

func (s *Service) recordSessionActivity(chatID int64, apply func(*SessionState)) {
	if s.sessions == nil {
		return
	}
	if err := s.sessions.Update(chatID, apply); err != nil && s.health != nil {
		s.health.RecordPollError(fmt.Errorf("session update failed: %w", err))
	}
}

func parseExecutionMode(raw string) (executionMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(executionModeLegacy):
		return executionModeLegacy, true
	case string(executionModeCodex):
		return executionModeCodex, true
	default:
		return "", false
	}
}

func summarizeSessionText(text string, max int) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= max {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:max]) + "..."
}

func (s *Service) createGoalFromMessage(chatID int64, text string) (Goal, error) {
	goal := NewGoal(chatID, text)
	if err := s.goals.Save(goal); err != nil {
		return Goal{}, err
	}
	s.recordSessionActivity(chatID, func(state *SessionState) {
		state.ActiveGoalID = goal.ID
		state.LastActivity = time.Now().UTC()
	})
	return goal, nil
}

func (s *Service) updateGoal(chatID int64, goalID string, result GoalResult) {
	if s.goals == nil || strings.TrimSpace(goalID) == "" {
		return
	}
	goal, err := s.goals.Load(chatID, goalID)
	if err != nil {
		if s.health != nil {
			s.health.RecordPollError(fmt.Errorf("goal load failed: %w", err))
		}
		return
	}
	goal = ApplyGoalResult(goal, result)
	if err := s.goals.Save(goal); err != nil && s.health != nil {
		s.health.RecordPollError(fmt.Errorf("goal save failed: %w", err))
	}
}

func (s *Service) updateActiveGoal(chatID int64, result GoalResult) {
	if s.sessions == nil {
		return
	}
	state, err := s.sessions.Load(chatID)
	if err != nil {
		if s.health != nil {
			s.health.RecordPollError(fmt.Errorf("session load failed: %w", err))
		}
		return
	}
	s.updateGoal(chatID, state.ActiveGoalID, result)
}

func (s *Service) handleGoalCommand(ctx context.Context, chatID int64) error {
	goal, err := s.loadActiveGoal(chatID)
	if err != nil {
		return s.bot.SendMessage(ctx, chatID, "No active goal for this chat.")
	}
	return s.bot.SendMessage(ctx, chatID, formatGoal(goal))
}

func (s *Service) handleGoalsCommand(ctx context.Context, chatID int64) error {
	goals, err := s.goals.List(chatID)
	if err != nil {
		return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("list goals failed: %v", err))
	}
	if len(goals) == 0 {
		return s.bot.SendMessage(ctx, chatID, "No goals found for this chat.")
	}
	lines := make([]string, 0, min(5, len(goals))+1)
	lines = append(lines, "Recent goals:")
	for i, goal := range goals {
		if i >= 5 {
			break
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s (%s)", goal.Status, goal.Objective, goal.ID))
	}
	return s.bot.SendMessage(ctx, chatID, strings.Join(lines, "\n"))
}

func (s *Service) handleGoalStopCommand(ctx context.Context, chatID int64) error {
	goal, err := s.loadActiveGoal(chatID)
	if err != nil {
		return s.bot.SendMessage(ctx, chatID, "No active goal to stop.")
	}
	goal = ApplyGoalResult(goal, GoalResult{Err: fmt.Errorf("stopped by user")})
	if err := s.goals.Save(goal); err != nil {
		return s.bot.SendMessage(ctx, chatID, fmt.Sprintf("stop goal failed: %v", err))
	}
	s.recordSessionActivity(chatID, func(state *SessionState) {
		state.ActiveGoalID = ""
		state.PendingConfirmation = false
		state.LastActivity = time.Now().UTC()
	})
	if s.confirms != nil {
		if err := s.confirms.Clear(chatID); err != nil && s.health != nil {
			s.health.RecordPollError(fmt.Errorf("clear pending confirmation failed: %w", err))
		}
	}
	return s.bot.SendMessage(ctx, chatID, "Active goal stopped.")
}

func (s *Service) handleConfirmCommand(ctx context.Context, chatID int64) error {
	if s.confirms == nil {
		return s.bot.SendMessage(ctx, chatID, "Confirmation store is unavailable.")
	}
	pending, err := s.confirms.Load(chatID)
	if errors.Is(err, ErrPendingConfirmationNotFound) {
		return s.bot.SendMessage(ctx, chatID, "No pending host-critical request to confirm.")
	}
	if err != nil {
		return s.bot.SendMessage(ctx, chatID, "Failed to load pending confirmation. Please retry.")
	}
	if err := s.confirms.Clear(chatID); err != nil {
		return s.bot.SendMessage(ctx, chatID, "Failed to clear pending confirmation. Please retry.")
	}
	s.recordSessionActivity(chatID, func(state *SessionState) {
		state.PendingConfirmation = false
		state.LastActivity = time.Now().UTC()
	})
	if s.runner == nil {
		s.updateGoal(chatID, pending.GoalID, GoalResult{
			Err: fmt.Errorf("goal runner is not configured"),
		})
		return s.bot.SendMessage(ctx, chatID, "Codex goal runner is unavailable. Please retry.")
	}
	if err := s.bot.SendMessage(ctx, chatID, "Confirmed. Goal queued for background execution."); err != nil {
		return err
	}
	s.updateGoal(chatID, pending.GoalID, GoalResult{Summary: "queued for background execution"})
	if err := s.runner.Enqueue(chatID, pending.GoalID); err != nil {
		s.updateGoal(chatID, pending.GoalID, GoalResult{Err: err})
		return s.bot.SendMessage(ctx, chatID, "Failed to queue confirmed goal. Please retry.")
	}
	return nil
}

func (s *Service) loadActiveGoal(chatID int64) (Goal, error) {
	if s.sessions == nil || s.goals == nil {
		return Goal{}, fmt.Errorf("goal storage unavailable")
	}
	state, err := s.sessions.Load(chatID)
	if err != nil {
		return Goal{}, err
	}
	if strings.TrimSpace(state.ActiveGoalID) == "" {
		return Goal{}, fmt.Errorf("no active goal")
	}
	return s.goals.Load(chatID, state.ActiveGoalID)
}

func formatGoal(goal Goal) string {
	lines := []string{
		fmt.Sprintf("Goal: %s", goal.Objective),
		fmt.Sprintf("ID: %s", goal.ID),
		fmt.Sprintf("Status: %s", goal.Status),
	}
	if strings.TrimSpace(goal.LatestSummary) != "" {
		lines = append(lines, "Latest: "+goal.LatestSummary)
	}
	if strings.TrimSpace(goal.LastError) != "" {
		lines = append(lines, "Last error: "+goal.LastError)
	}
	return strings.Join(lines, "\n")
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Service) seenBefore(updateID int64) bool {
	s.dedupMu.Lock()
	defer s.dedupMu.Unlock()
	if _, exists := s.seen[updateID]; exists {
		return true
	}
	s.seen[updateID] = struct{}{}
	if len(s.seen) > 5000 {
		// Keep dedupe state bounded for long-running process.
		s.seen = map[int64]struct{}{updateID: {}}
	}
	return false
}

func (s *Service) buildPromptFromMemory(chatID int64, userText string) string {
	state, err := s.store.Load(chatID)
	if err != nil {
		return userText
	}
	parts := make([]string, 0, 4)
	if strings.TrimSpace(state.Summary) != "" {
		parts = append(parts, "Conversation summary:\n"+state.Summary)
	}
	if len(state.Messages) > 0 {
		lines := make([]string, 0, len(state.Messages)+1)
		lines = append(lines, "Recent conversation:")
		for _, msg := range state.Messages {
			role := strings.ToLower(strings.TrimSpace(msg.Role))
			if role == "assistant" {
				lines = append(lines, "Assistant: "+msg.Content)
			} else {
				lines = append(lines, "User: "+msg.Content)
			}
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	parts = append(parts, "Current user message:\n"+userText)
	return strings.Join(parts, "\n\n")
}

func (s *Service) callAgentWithRetry(ctx context.Context, prompt string, model string) (string, error) {
	attempts := s.cfg.Runtime.AgentRetryCount
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		reply, err := s.agent.GenerateReply(ctx, prompt, model)
		if err == nil {
			return reply, nil
		}
		lastErr = err
		if i < attempts-1 {
			timer := time.NewTimer(time.Duration(i+1) * 250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
	}
	return "", lastErr
}

func extractTickerFromStockQuery(text string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return "", false
	}
	hasKeyword := strings.Contains(lower, "股价") ||
		strings.Contains(lower, "行情") ||
		strings.Contains(lower, "price") ||
		strings.Contains(lower, "quote") ||
		strings.Contains(lower, "ticker") ||
		strings.Contains(lower, "stock")
	if !hasKeyword {
		return "", false
	}

	candidates := tickerPattern.FindAllString(text, -1)
	if len(candidates) == 0 {
		return "", false
	}
	stopwords := map[string]struct{}{
		"PRICE": {}, "STOCK": {}, "QUOTE": {}, "TICKER": {},
		"WHAT": {}, "WHATS": {}, "IS": {}, "THE": {}, "OF": {}, "FOR": {},
		"LATEST": {}, "CURRENT": {}, "TODAY": {}, "NOW": {}, "CAN": {},
		"YOU": {}, "TELL": {}, "ME": {}, "PLEASE": {},
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		c := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(candidates[i]), "$"))
		if _, blocked := stopwords[c]; blocked {
			continue
		}
		return c, true
	}
	return "", false
}

func isNonActionableReply(userText string, reply string) bool {
	user := strings.ToLower(strings.TrimSpace(userText))
	text := strings.ToLower(strings.TrimSpace(reply))
	if text == "" {
		return true
	}

	if strings.Contains(user, "自主") || strings.Contains(user, "自己解决") ||
		strings.Contains(user, "解决问题") || strings.Contains(user, "independent") ||
		strings.Contains(user, "solve") {
		if strings.Contains(text, "i cannot") || strings.Contains(text, "limitations") ||
			strings.Contains(text, "without tools") || strings.Contains(text, "cannot access") ||
			strings.Contains(text, "我不能") || strings.Contains(text, "无法") || strings.Contains(text, "限制") {
			return true
		}
	}

	if strings.Contains(text, "i cannot") || strings.Contains(text, "limitations") ||
		strings.Contains(text, "cannot access") || strings.Contains(text, "without additional access") ||
		strings.Contains(text, "我不能") || strings.Contains(text, "无法") {
		if strings.Contains(text, "you may consider") || strings.Contains(text, "you can") ||
			strings.Contains(text, "你可以") || strings.Contains(text, "请自行") {
			return true
		}
	}
	return false
}
