# Codex-First Rebuild Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rebuild `openclaw-lite-go` into a Codex-first Telegram operations agent that can use full VPS access, perform reliable web research, and execute multi-step goals instead of acting as a lightweight chat bot with an optional Codex side path.

**Architecture:** Keep the current Go process as the Telegram ingress, config loader, health endpoint, and persistence shell. Deprecate the existing `agent + small tools` path as the primary execution engine and promote `codexproxy` into the default runtime, with durable sessions, explicit goal/task state, execution/audit policy, and a dedicated research path for citeable web results. Treat Codex as the actor, not as an optional integration.

**Tech Stack:** Go 1.22, existing `internal/runtime`, `internal/memory`, `internal/codexproxy`, `internal/tools`, `cmd/clawlite`, `cmd/codexproxy`, systemd on Ubuntu VPS, Codex CLI with ChatGPT Business auth.

---

## Assumptions

1. Telegram remains the only user-facing channel for now.
2. The VPS is the trusted execution environment.
3. Codex will run with full host access because that is the product requirement.
4. Reliable web search still needs explicit plumbing; do not assume Codex CLI alone gives stable, citeable browsing behavior.
5. The current `agent.GenerateReply` path becomes fallback/legacy, not the main runtime.

---

## Architecture Decision

### Option A: Keep today’s hybrid (`agent` first, `/codexcli` optional)

- Lowest change risk
- Still leaves the real agent path fragmented
- User intent routing stays ambiguous
- Not recommended

### Option B: Codex-first gateway with legacy fallback (Recommended)

- All normal chats go to `codexproxy` by default
- `agent + tools` remains available as backup mode
- Lets the system evolve toward OpenClaw-like task execution without losing the current Telegram shell

### Option C: Throw away `lite-go`, rewrite around a new service

- Cleanest architecture on paper
- Highest delivery risk
- Would force replacing stable pieces you already have: Telegram polling, config, health, deployment
- Not recommended

This plan uses **Option B**.

---

### Task 1: Flip Runtime Ownership to Codex-First

**Files:**
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `internal/config/config.go`
- Modify: `config.example.json`
- Modify: `README.md`

**Step 1: Write the failing test**

```go
func TestHandleUpdateRoutesNormalChatToCodexProxyByDefault(t *testing.T) {
    // when codex-first is enabled, a normal message should bypass agent.GenerateReply
}

func TestHandleUpdateCanFallbackToLegacyAgentMode(t *testing.T) {
    // /agentmode legacy should restore the old path for one chat
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run "CodexFirst|LegacyAgentMode" -v`
Expected: FAIL because the current runtime still defaults to the old agent/tool path.

**Step 3: Write minimal implementation**

- Add config flag: `runtime.codex_first_default`
- Replace `/codexcli on|off` with:
  - default codex-first behavior
  - explicit `/agentmode legacy|codex`
- Keep `/codexcli` as backward-compatible alias during migration

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run "CodexFirst|LegacyAgentMode|CodexCLI" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/service.go internal/runtime/service_test.go internal/config/config.go config.example.json README.md
git commit -m "feat: make codex proxy the default execution runtime"
```

---

### Task 2: Persist Session Mode and Codex Session Metadata

**Files:**
- Create: `internal/runtime/session_store.go`
- Create: `internal/runtime/session_store_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/memory/store.go`

**Step 1: Write the failing test**

```go
func TestSessionStorePersistsCodexModeAcrossRestart(t *testing.T) {
    // write session mode, recreate service, verify mode is restored
}

func TestSessionStorePersistsGoalAndTaskMetadata(t *testing.T) {
    // goal id, status, last run, and current mode survive restart
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run SessionStore -v`
Expected: FAIL because session state is currently in-memory only.

**Step 3: Write minimal implementation**

- Add a file-backed `session_store`
- Persist:
  - execution mode
  - active goal id
  - last codex result summary
  - last activity timestamp

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run SessionStore -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/session_store.go internal/runtime/session_store_test.go internal/runtime/service.go internal/memory/store.go
git commit -m "feat: persist codex session state and execution mode"
```

---

### Task 3: Add Goal Objects Instead of Pure Chat Turns

**Files:**
- Create: `internal/runtime/goals.go`
- Create: `internal/runtime/goals_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `README.md`

**Step 1: Write the failing test**

```go
func TestCreateGoalFromTelegramMessage(t *testing.T) {
    // normal message should create/update a structured goal
}

func TestGoalStatusTransitionsQueuedRunningBlockedDone(t *testing.T) {
    // codex result updates goal state machine
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run Goal -v`
Expected: FAIL because no goal abstraction exists yet.

**Step 3: Write minimal implementation**

- Define `Goal`:
  - id
  - chat_id
  - objective
  - status
  - latest_summary
  - last_error
- Introduce commands:
  - `/goal`
  - `/goals`
  - `/goalstop`

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run Goal -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/goals.go internal/runtime/goals_test.go internal/runtime/service.go README.md
git commit -m "feat: introduce structured goals for codex-first execution"
```

---

### Task 4: Add Explicit Research Capability for Web Search

**Files:**
- Modify: `internal/codexproxy/server.go`
- Create: `internal/codexproxy/research.go`
- Create: `internal/codexproxy/research_test.go`
- Modify: `internal/tools/executor.go`
- Modify: `README.md`

**Step 1: Write the failing test**

```go
func TestResearchToolReturnsSourcesAndSnippets(t *testing.T) {
    // result should contain title, url, snippet
}

func TestCodexPromptMentionsResearchPathWhenUserNeedsCurrentInfo(t *testing.T) {
    // proxy should tell Codex to use explicit research path for latest facts
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/codexproxy -run Research -v`
Expected: FAIL because codexproxy does not yet expose a stable research path.

**Step 3: Write minimal implementation**

- Add a `research` helper callable by proxy-side shell flow
- Reuse or wrap current `web_search` extraction logic for citeable results
- Inject a short system preamble into Codex prompts:
  - for current/latest questions, perform explicit research
  - include sources in the final answer

**Step 4: Run test to verify it passes**

Run: `go test ./internal/codexproxy ./internal/tools -run Research -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/codexproxy/research.go internal/codexproxy/research_test.go internal/codexproxy/server.go internal/tools/executor.go README.md
git commit -m "feat: add explicit research path for codex-first runtime"
```

---

### Task 5: Add Full-Access Execution Policy and Audit Log

**Files:**
- Create: `internal/codexproxy/policy.go`
- Create: `internal/codexproxy/policy_test.go`
- Create: `internal/codexproxy/audit.go`
- Create: `internal/codexproxy/audit_test.go`
- Modify: `internal/codexproxy/server.go`
- Modify: `README.md`

**Step 1: Write the failing test**

```go
func TestAuditLogCapturesPromptAndReplyMetadata(t *testing.T) {
    // every codex execution should produce an audit record
}

func TestPolicyFlagsDangerousCommandsButAllowsConfiguredExecution(t *testing.T) {
    // system should mark rm -rf /, reboot, usermod, iptables flush as high risk
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/codexproxy -run "Audit|Policy" -v`
Expected: FAIL because no audit/policy layer exists.

**Step 3: Write minimal implementation**

- Log:
  - timestamp
  - chat id
  - goal id
  - raw user message
  - codex prompt hash
  - final reply
  - execution mode
- Add risk classifier:
  - informational
  - mutating
  - host-critical
- In full-access mode:
  - log all actions
  - optionally require explicit `/confirm` for host-critical requests

**Step 4: Run test to verify it passes**

Run: `go test ./internal/codexproxy -run "Audit|Policy" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/codexproxy/policy.go internal/codexproxy/policy_test.go internal/codexproxy/audit.go internal/codexproxy/audit_test.go internal/codexproxy/server.go README.md
git commit -m "feat: add audit and risk policy for full-access codex execution"
```

---

### Task 6: Add Long-Running Goal Loop and Background Execution

**Files:**
- Create: `internal/runtime/goal_runner.go`
- Create: `internal/runtime/goal_runner_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/health.go`

**Step 1: Write the failing test**

```go
func TestGoalRunnerCanContinueUntilObjectiveReached(t *testing.T) {
    // queued goal should re-enter codex until done/blocked
}

func TestGoalRunnerSurfacesBlockedStateToTelegram(t *testing.T) {
    // when codex cannot continue, user should see blocked reason
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run GoalRunner -v`
Expected: FAIL because execution is currently one Telegram message = one codex call.

**Step 3: Write minimal implementation**

- Add background runner queue
- Support states:
  - queued
  - running
  - waiting_input
  - blocked
  - done
- Let one user message define a durable objective, not just a single chat turn

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run GoalRunner -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/goal_runner.go internal/runtime/goal_runner_test.go internal/runtime/service.go internal/runtime/health.go
git commit -m "feat: add background goal runner for codex-first execution"
```

---

### Task 7: Demote Legacy Agent Path to Explicit Fallback

**Files:**
- Modify: `internal/runtime/service.go`
- Modify: `internal/agent/client.go`
- Modify: `README.md`

**Step 1: Write the failing test**

```go
func TestLegacyAgentPathOnlyRunsWhenExplicitlyRequested(t *testing.T) {
    // normal messages should not hit agent.GenerateReply in codex-first mode
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run LegacyAgentPath -v`
Expected: FAIL because legacy path is still the default.

**Step 3: Write minimal implementation**

- Keep `/agentmode legacy`
- Keep `/agent <model>`
- Remove legacy path from default docs and startup hints

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run LegacyAgentPath -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/service.go internal/agent/client.go README.md
git commit -m "refactor: demote legacy agent path to explicit fallback mode"
```

---

### Task 8: Add VPS Acceptance Tests and Deployment Gates

**Files:**
- Create: `scripts/evals/codex_first_cases.json`
- Modify: `scripts/evals/run.go`
- Modify: `README.md`

**Step 1: Write the failing test**

- Add eval cases:
  - inspect service status
  - inspect disk usage
  - use research for latest/current query
  - complete a multi-step repo task

**Step 2: Run verification to confirm gaps**

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/codex_first_cases.json`
Expected: FAIL before implementation.

**Step 3: Write minimal implementation**

- Extend eval runner to support codex-first proxy mode checks
- Define acceptance thresholds:
  - service health
  - codex auth present
  - proxy reply correctness
  - goal completion ratio

**Step 4: Run verification to confirm pass**

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/codex_first_cases.json`
Expected: PASS.

**Step 5: Commit**

```bash
git add scripts/evals/codex_first_cases.json scripts/evals/run.go README.md
git commit -m "test: add codex-first acceptance gates"
```

---

## Migration Result

After this plan:

- `openclaw-lite-go` is no longer a lightweight bot with optional Codex passthrough.
- It becomes a Codex-first Telegram operations agent.
- The Go runtime remains as the shell and control plane.
- Codex becomes the default actor with full VPS access.
- Web research becomes explicit and reliable instead of ad hoc.
- Legacy `agent/tools` stays only as fallback.

## Out of Scope

- Multi-channel support beyond Telegram
- Fine-grained RBAC for multiple human operators
- Full browser automation stack
- Multi-VPS fleet orchestration
- Replacing systemd deployment with Kubernetes
