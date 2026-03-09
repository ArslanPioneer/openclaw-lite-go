# Codex-First Residual Closure Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close the remaining codex-first gaps so the runtime actually uses durable background goal execution, propagates goal identity into codexproxy audit records, and enforces explicit confirmation for host-critical full-access actions.

**Architecture:** Keep the existing codex-first runtime and proxy split. Finish the control-plane wiring instead of introducing new subsystems: route durable objectives through `GoalRunner`, carry `goal_id` from runtime into proxy requests, and add a lightweight per-chat confirmation state machine in runtime that cooperates with codexproxy policy decisions.

**Tech Stack:** Go 1.22, existing `internal/runtime`, `internal/codexproxy`, `internal/memory`, `cmd/codexproxy`, Telegram polling runtime, file-backed stores, JSON HTTP proxy.

---

## Context

The previous rebuild landed most of the codex-first scaffolding, but four important gaps remain:

1. `GoalRunner` exists, but the default `HandleUpdate` path still executes most work synchronously instead of queueing durable background goals.
2. `goal_id` exists in runtime session/goal state and in codexproxy audit records, but runtime does not yet pass a stable goal marker into proxy requests.
3. codexproxy policy can classify host-critical requests and expose `RequireConfirm`, but there is no Telegram `/confirm` flow to complete the human approval loop.
4. Telegram replies are still plain text, so long multi-line status outputs are harder to scan than Markdown-formatted messages.

This plan closes only those gaps. Do not expand scope into multi-operator RBAC, browser automation, or distributed runners.

---

## Architecture Decision

### Option A: Leave the synchronous path in place and treat `GoalRunner` as optional

- Lowest implementation effort
- Leaves the main codex-first path inconsistent with the product goal
- Keeps durable goals as a side feature instead of the execution backbone
- Not recommended

### Option B: Promote `GoalRunner` to the default codex-first execution path and add approval state in runtime (Recommended)

- Keeps the current Go runtime as the control plane
- Uses the existing file-backed goal/session stores
- Preserves legacy agent fallback without mixing it into codex-first execution

### Option C: Move all state orchestration into codexproxy

- Would centralize execution logic
- Requires a larger redesign of runtime/proxy responsibilities
- Too large for a residual closure pass
- Not recommended

This plan uses **Option B**.

---

### Task 1: Route Codex-First Messages Through the Background Goal Runner

**Files:**
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `internal/runtime/goal_runner.go`
- Modify: `internal/runtime/goal_runner_test.go`
- Modify: `internal/runtime/health.go`

**Step 1: Write the failing test**

```go
func TestHandleUpdateCodexFirstEnqueuesGoalRunnerInsteadOfRunningSynchronously(t *testing.T) {
    // normal codex-first chat should queue a durable goal and return an acknowledgement
}

func TestHandleUpdateGoalRunnerCompletionUpdatesTelegramAndGoalState(t *testing.T) {
    // queued codex-first work should later complete through GoalRunner
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run "EnqueuesGoalRunner|GoalRunnerCompletion" -v`
Expected: FAIL because `HandleUpdate` still executes the codex-first request inline.

**Step 3: Write minimal implementation**

- Add a codex-facing `GoalStepExecutor` implementation inside runtime that:
  - loads the active goal
  - calls codexproxy for the next step
  - maps reply outcomes into `running`, `waiting_input`, `blocked`, `done`
- In `NewService`, create and attach a default `GoalRunner`
- For codex-first chats:
  - create the goal
  - enqueue it
  - send a short queued acknowledgement instead of executing synchronously
- Keep stock quote direct handling and explicit legacy mode on the old synchronous path
- Ensure health counters reflect queued and active goals correctly

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run "EnqueuesGoalRunner|GoalRunnerCompletion|GoalRunner" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/service.go internal/runtime/service_test.go internal/runtime/goal_runner.go internal/runtime/goal_runner_test.go internal/runtime/health.go
git commit -m "feat: route codex-first chats through goal runner"
```

---

### Task 2: Propagate Goal IDs End-to-End Into Codex Proxy Audit Records

**Files:**
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/goals.go`
- Modify: `internal/runtime/goals_test.go`
- Modify: `internal/codexproxy/server.go`
- Modify: `internal/codexproxy/audit_test.go`

**Step 1: Write the failing test**

```go
func TestHandleUpdateCodexProxyRequestCarriesGoalMarker(t *testing.T) {
    // runtime should prefix proxy-bound work with a stable goal id marker
}

func TestAuditLogCapturesGoalIDFromRuntimePropagatedMessage(t *testing.T) {
    // proxy audit record should contain the goal id generated by runtime
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime ./internal/codexproxy -run "GoalMarker|GoalIDFromRuntime" -v`
Expected: FAIL because runtime does not yet pass goal ids into proxy-bound messages.

**Step 3: Write minimal implementation**

- Add a helper in runtime to format codexproxy messages as:
  - `[goal:<goal-id>] <objective-or-step-message>`
- Ensure only proxy-bound codex-first traffic gets the marker
- Keep user-visible stored conversation text clean; do not expose the raw marker back to Telegram replies
- Reuse existing proxy-side `extractGoalID`
- Update goal tests to verify the runtime-generated goal id survives into proxy calls and audit rows

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime ./internal/codexproxy -run "GoalMarker|GoalIDFromRuntime|Audit" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/service.go internal/runtime/goals.go internal/runtime/goals_test.go internal/codexproxy/server.go internal/codexproxy/audit_test.go
git commit -m "feat: propagate goal ids into codex proxy audit records"
```

---

### Task 3: Add Explicit `/confirm` Flow for Host-Critical Codex Requests

**Files:**
- Modify: `internal/runtime/service.go`
- Create: `internal/runtime/confirm_store.go`
- Create: `internal/runtime/confirm_store_test.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `internal/runtime/session_store.go`
- Modify: `internal/codexproxy/policy.go`
- Modify: `internal/codexproxy/server.go`
- Modify: `README.md`

**Step 1: Write the failing test**

```go
func TestHandleUpdateHostCriticalCodexRequestRequiresExplicitConfirm(t *testing.T) {
    // host-critical codex-first request should pause and ask for /confirm
}

func TestHandleUpdateConfirmReplaysPendingCodexGoal(t *testing.T) {
    // /confirm should release the exact pending goal request and continue execution
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime ./internal/codexproxy -run "Confirm|HostCritical" -v`
Expected: FAIL because runtime has no confirmation state machine and proxy policy is not wired into Telegram approval flow.

**Step 3: Write minimal implementation**

- Add a tiny file-backed confirmation store keyed by chat id
- Persist:
  - pending goal id
  - pending raw codex request
  - risk level
  - created timestamp
- In codex-first flow:
  - classify the outgoing request before execution
  - if host-critical and confirmation is required, store pending action and send a `/confirm` prompt
  - do not enqueue the goal step until confirmed
- Add `/confirm` command in runtime:
  - load pending confirmation
  - clear it atomically
  - enqueue the saved goal/action
- Keep legacy mode unchanged
- Update README with `/confirm` semantics and host-critical examples

**Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime ./internal/codexproxy -run "Confirm|HostCritical" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/service.go internal/runtime/confirm_store.go internal/runtime/confirm_store_test.go internal/runtime/service_test.go internal/runtime/session_store.go internal/codexproxy/policy.go internal/codexproxy/server.go README.md
git commit -m "feat: require explicit confirm for host-critical codex actions"
```

---

### Task 4: Extend Acceptance Gates for Durable Goals and Confirmation Workflow

**Files:**
- Modify: `scripts/evals/run.go`
- Modify: `scripts/evals/codex_first_cases.json`
- Modify: `README.md`

**Step 1: Write the failing test**

- Add eval cases for:
  - codex-first message returns queued acknowledgement
  - background goal completion updates final output
  - host-critical request pauses for `/confirm`
  - `/confirm` resumes the saved request

**Step 2: Run verification to confirm gaps**

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/codex_first_cases.json`
Expected: FAIL before implementation because the eval runner does not yet exercise queued execution or confirmation flow.

**Step 3: Write minimal implementation**

- Extend eval runner fake codex/proxy behavior to support:
  - queued goal acknowledgements
  - delayed completion simulation
  - pending confirmation
  - confirm replay
- Add acceptance thresholds that require:
  - codex-first queueing behavior
  - explicit confirmation gate for host-critical actions
  - goal completion signal after confirmation when applicable

**Step 4: Run verification to confirm pass**

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/codex_first_cases.json`
Expected: PASS.

**Step 5: Commit**

```bash
git add scripts/evals/run.go scripts/evals/codex_first_cases.json README.md
git commit -m "test: extend codex-first gates for goal runner and confirm flow"
```

---

### Task 5: Switch Telegram Output to Markdown Mode With Safe Fallback

**Files:**
- Modify: `internal/telegram/client.go`
- Create: `internal/telegram/client_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_test.go`
- Modify: `README.md`

**Step 1: Write the failing test**

```go
func TestSendMessageUsesMarkdownV2ParseMode(t *testing.T) {
    // telegram payload should include parse_mode=MarkdownV2 for runtime replies
}

func TestSendMessageFallsBackToPlainTextWhenMarkdownRejected(t *testing.T) {
    // if Telegram returns parse entity error, client should retry once without markdown
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/telegram -run "Markdown|ParseMode" -v`
Expected: FAIL because the current telegram client only sends plain text payloads.

**Step 3: Write minimal implementation**

- Add Markdown-aware send path in telegram client:
  - default `parse_mode` to `MarkdownV2`
  - escape MarkdownV2 special characters before sending
  - keep message body readable for lists/code blocks already generated by runtime
- Add one retry fallback:
  - if Telegram rejects markdown entities, resend as plain text without `parse_mode`
- Keep runtime call sites unchanged (`SendMessage` signature remains stable) to avoid broad refactor
- Add runtime-level formatting helper only where necessary for common structured outputs (`/goals`, status summaries)

**Step 4: Run test to verify it passes**

Run: `go test ./internal/telegram ./internal/runtime -run "Markdown|ParseMode|SendMessage" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/telegram/client.go internal/telegram/client_test.go internal/runtime/service.go internal/runtime/service_test.go README.md
git commit -m "feat: enable markdown telegram replies with safe fallback"
```

---

## Migration Result

After this plan:

- codex-first chats execute through the durable goal runner by default instead of the synchronous inline path
- `goal_id` flows from runtime goals into codexproxy audit records end-to-end
- host-critical full-access requests pause for explicit `/confirm`
- acceptance tests cover queued execution and confirmation workflow, not just simple codex-first passthrough
- Telegram outputs use Markdown formatting with fallback to plain text on parse errors

## Out of Scope

- Multi-user approval chains
- Role-based access control
- Rich Telegram inline buttons for approvals
- Multiple concurrent active goals per chat
- Replacing the current proxy HTTP contract
