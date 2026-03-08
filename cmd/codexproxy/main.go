package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"openclaw-lite-go/internal/codexproxy"
)

func main() {
	fs := flag.NewFlagSet("codexproxy", flag.ExitOnError)
	listen := fs.String("listen", codexproxy.DefaultListenAddr(), "listen address")
	workdir := fs.String("workdir", ".", "directory where codex runs")
	stateDir := fs.String("state-dir", "", "directory for per-chat proxy transcript state")
	codexBin := fs.String("codex-bin", "codex", "codex executable path")
	model := fs.String("model", "", "optional codex model override")
	token := fs.String("token", "", "optional bearer token required by /chat")
	timeoutSec := fs.Int("timeout-sec", 600, "per-request codex timeout in seconds")
	dangerFullAccess := fs.Bool("danger-full-access", false, "run codex with --dangerously-bypass-approvals-and-sandbox")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "parse flags: %v\n", err)
		os.Exit(1)
	}

	absWorkdir, err := filepath.Abs(*workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve workdir: %v\n", err)
		os.Exit(1)
	}
	resolvedStateDir := *stateDir
	if resolvedStateDir == "" {
		resolvedStateDir = filepath.Join(absWorkdir, ".codexproxy")
	}

	server := &http.Server{
		Addr: *listen,
		Handler: codexproxy.NewServer(codexproxy.Config{
			WorkDir:          absWorkdir,
			StateDir:         resolvedStateDir,
			AuthToken:        *token,
			CodexBin:         *codexBin,
			Model:            *model,
			Timeout:          time.Duration(*timeoutSec) * time.Second,
			DangerFullAccess: *dangerFullAccess,
		}).Handler(),
	}

	fmt.Printf("codexproxy listening on %s\n", *listen)
	fmt.Printf("codexproxy workdir: %s\n", absWorkdir)
	fmt.Printf("codexproxy state dir: %s\n", resolvedStateDir)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "codexproxy server error: %v\n", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	shutdownCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
