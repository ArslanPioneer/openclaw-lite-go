package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"openclaw-lite-go/internal/agent"
	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/runtime"
	"openclaw-lite-go/internal/telegram"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "setup":
		err = runSetup(os.Args[2:])
	case "run":
		err = runBot(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("clawlite - minimal Telegram AI bot")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  clawlite setup --provider <openai|minimax|glm|custom> --telegram-token <token> --agent-key <key> --agent-model <model> [--agent-url <url>] [--skills-source-dir <dir>] [--skills-install-dir <dir>]")
	fmt.Println("  clawlite run --config ./config.json")
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)

	configPath := fs.String("config", "config.json", "config file path")
	provider := fs.String("provider", "openai", "model provider: openai|minimax|glm|custom")
	telegramToken := fs.String("telegram-token", "", "Telegram bot token")
	agentURL := fs.String("agent-url", "", "agent base URL (optional; auto-filled by provider)")
	agentKey := fs.String("agent-key", "", "agent API key")
	agentModel := fs.String("agent-model", "gpt-4o-mini", "default model")
	systemPrompt := fs.String("system-prompt", "", "system prompt")
	workers := fs.Int("workers", 4, "worker count")
	queueSize := fs.Int("queue-size", 64, "queue size")
	pollTimeout := fs.Int("poll-timeout", 25, "telegram poll timeout (seconds)")
	requestTimeout := fs.Int("request-timeout", 60, "http request timeout (seconds)")
	dataDir := fs.String("data-dir", "data", "data directory for memory/state")
	historyTurns := fs.Int("history-turns", 8, "stored conversation turns per chat")
	agentRetryCount := fs.Int("agent-retry-count", 2, "agent retry attempts")
	skillsSourceDir := fs.String("skills-source-dir", "openclaw-skills", "directory containing source skills (each subdir has SKILL.md)")
	skillsInstallDir := fs.String("skills-install-dir", "data/skills", "directory where installed skills are copied")
	healthPort := fs.Int("health-port", 18080, "health check port")
	restartBackoffMs := fs.Int("restart-backoff-ms", 1000, "restart initial backoff (ms)")
	restartMaxMs := fs.Int("restart-max-ms", 30000, "restart max backoff (ms)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.Config{
		Telegram: config.TelegramConfig{
			BotToken: *telegramToken,
		},
		Agent: config.AgentConfig{
			Provider:     *provider,
			BaseURL:      *agentURL,
			APIKey:       *agentKey,
			Model:        *agentModel,
			SystemPrompt: *systemPrompt,
		},
		Runtime: config.RuntimeConfig{
			Workers:           *workers,
			QueueSize:         *queueSize,
			PollTimeoutSecond: *pollTimeout,
			RequestTimeoutSec: *requestTimeout,
			DataDir:           *dataDir,
			HistoryTurns:      *historyTurns,
			AgentRetryCount:   *agentRetryCount,
			SkillsSourceDir:   *skillsSourceDir,
			SkillsInstallDir:  *skillsInstallDir,
			HealthPort:        *healthPort,
			RestartBackoffMs:  *restartBackoffMs,
			RestartMaxMs:      *restartMaxMs,
		},
	}

	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	absolutePath, err := filepath.Abs(*configPath)
	if err != nil {
		return err
	}
	if err := config.Save(absolutePath, cfg); err != nil {
		return err
	}
	fmt.Printf("config saved: %s\n", absolutePath)
	return nil
}

func runBot(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	timeout := time.Duration(cfg.Runtime.RequestTimeoutSec) * time.Second
	tg := telegram.NewClient(cfg.Telegram.BotToken, timeout)
	ag := agent.NewClient(cfg.Agent, timeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	healthState := runtime.NewHealthState()
	healthServer := startHealthServer(cfg.Runtime.HealthPort, healthState)
	defer shutdownHTTPServer(healthServer)

	fmt.Println("clawlite is running...")
	fmt.Println("press Ctrl+C to stop")
	run := func(runCtx context.Context) error {
		service := runtime.NewService(cfg, tg, ag)
		service.AttachHealthState(healthState)
		return service.Run(runCtx)
	}
	opts := runtime.SupervisorOptions{
		InitialBackoff: time.Duration(cfg.Runtime.RestartBackoffMs) * time.Millisecond,
		MaxBackoff:     time.Duration(cfg.Runtime.RestartMaxMs) * time.Millisecond,
		OnRestart: func(attempt int, runErr error, backoff time.Duration) {
			healthState.RecordRestart(runErr)
			fmt.Fprintf(os.Stderr, "service restart #%d after error: %v (backoff=%s)\n", attempt, runErr, backoff)
		},
	}

	if err := runtime.RunWithSupervisor(ctx, run, opts); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func startHealthServer(port int, state *runtime.HealthState) *http.Server {
	if port == 0 {
		return nil
	}
	addr := resolveHealthListenAddr(port)
	mux := http.NewServeMux()
	mux.Handle("/healthz", runtime.HealthHandler(state))

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "health server error: %v\n", err)
		}
	}()
	return server
}

func resolveHealthListenAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}

func shutdownHTTPServer(server *http.Server) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
