# Codex Proxy Server Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a VPS-side Codex proxy server so Telegram chats can route through `/codexcli` to a locally authenticated Codex CLI session.

**Architecture:** Introduce a small HTTP server command `cmd/codexproxy` backed by an internal package that stores a per-chat transcript on disk and shells out to `codex exec --json` in the configured repo workdir for every Telegram turn. Keep the runtime client contract unchanged: POST JSON in, JSON reply out.

**Tech Stack:** Go 1.22 standard library, `os/exec`, `net/http`, existing runtime config/docs.

---

### Task 1: Add proxy package tests

**Files:**
- Create: `internal/codexproxy/server_test.go`

**Step 1: Write the failing tests**

- Test first-turn execution builds `codex exec --skip-git-repo-check --full-auto --json`.
- Test follow-up turn reuses stored transcript state and includes prior conversation in the next prompt.
- Test HTTP handler returns JSON `{"reply":"..."}` and propagates executor failures as `502`.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/codexproxy -v`
Expected: FAIL because package does not exist yet.

**Step 3: Write minimal implementation**

- Add request/response structs, workspace resolver, runner abstraction, and HTTP handler.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/codexproxy -v`
Expected: PASS

### Task 2: Add command entrypoint

**Files:**
- Create: `cmd/codexproxy/main.go`

**Step 1: Write the failing test**

- Keep this minimal and validate with package build through `go test ./...`.

**Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL if main package wiring is broken.

**Step 3: Write minimal implementation**

- Add flags for `--listen`, `--workspace-root`, `--codex-bin`, `--token`.
- Start HTTP server on `/chat`.

**Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

### Task 3: Document VPS deployment with device auth

**Files:**
- Modify: `README.md`
- Modify: `config.example.json`

**Step 1: Write the failing doc expectation**

- Add explicit instructions for `codex login --device-auth`, building `codexproxy`, and wiring `runtime.codex_proxy_url`.

**Step 2: Run verification**

Run: `go test ./...`
Expected: PASS

**Step 3: Update docs**

- Add copy-pasteable Ubuntu console commands and systemd service examples.

**Step 4: Final verification**

Run: `go test ./...`
Expected: PASS
