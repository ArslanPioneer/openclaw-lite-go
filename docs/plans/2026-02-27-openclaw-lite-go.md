# OpenClaw Lite (Go) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a minimal, faster Telegram-first assistant inspired by OpenClaw, with one-command token/agent configuration.

**Architecture:** Single-process Go service with three modules: `telegram` transport, `agent` HTTP adapter (OpenAI-compatible), and `runtime` worker pool. Configuration is persisted to `config.json` via a dedicated setup command. This removes plugin overhead and keeps a low-latency hot path.

**Tech Stack:** Go 1.22+, stdlib (`net/http`, `encoding/json`, `context`, `sync`), Telegram Bot API over HTTP long polling.

---

### Task 1: Bootstrap Project and Config Contracts

**Files:**
- Create: `go.mod`
- Create: `cmd/clawlite/main.go`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Step 1: Write failing tests**
- Validate missing required fields in config.
- Validate save/load roundtrip keeps all values.

**Step 2: Implement minimal config module**
- Define config structs for Telegram + Agent + Runtime.
- Add `Load`, `Save`, `Validate`, and default worker settings.

**Step 3: Implement CLI shell**
- `setup` command: accept token/agent flags and write config.
- `run` command: read and validate config before runtime startup.

### Task 2: Build Agent Adapter (OpenAI-compatible)

**Files:**
- Create: `internal/agent/client.go`
- Test: `internal/agent/client_test.go`

**Step 1: Write failing tests**
- Verify request payload shape and auth header.
- Verify non-200 response is surfaced as actionable error.

**Step 2: Implement minimal adapter**
- `GenerateReply(ctx, userText)` using chat completions endpoint.
- Shared HTTP client with keep-alive timeout tuning.

### Task 3: Build Telegram Transport and Runtime

**Files:**
- Create: `internal/telegram/client.go`
- Create: `internal/runtime/service.go`
- Test: `internal/runtime/service_test.go`

**Step 1: Write failing tests**
- Runtime routes text update -> agent -> sendMessage.
- `/agent <id>` command changes active agent in memory.

**Step 2: Implement Telegram client**
- `GetUpdates`, `SendMessage` using Telegram Bot API.
- Long-poll update loop with offset checkpointing.

**Step 3: Implement runtime worker pool**
- Configurable workers and bounded queue.
- Deduplicate by update id and graceful shutdown.

### Task 4: One-Click Setup UX and Documentation

**Files:**
- Create: `scripts/quickstart.ps1`
- Create: `README.md`

**Step 1: Setup script**
- One command writes `config.json` and supports immediate run mode.

**Step 2: README**
- Explain differences from OpenClaw.
- Document Telegram BotFather flow and agent endpoint setup.
- Add runbook + troubleshooting.
