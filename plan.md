# OpenClaw Core Gap Closure Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close the highest-impact core capability gaps between `openclaw-lite-go` and mature `openclaw` so the bot can complete complex tasks with fewer dead-ends and no raw internal errors.

**Architecture:** Keep Lite's single-process Go runtime, but add three mature runtime layers: (1) robust orchestration state machine, (2) tool/error policy engine, (3) reliability verification gates. Borrow OpenClaw's proven behaviors (bounded retries, failover classification, tool warning policy, mutation-aware guarantees) without importing full gateway/channel complexity.

**Tech Stack:** Go 1.22+, stdlib (`context`, `encoding/json`, `errors`, `regexp`, `strings`, `time`), existing modules in `internal/runtime`, `internal/tools`, `internal/agent`, `internal/memory`.

---

## Core Gap Check (Current Lite vs Mature OpenClaw)

1. **Run Orchestration Depth**
- Lite now: fixed `defaultAgentLoopMaxSteps=4` with single model path (`internal/runtime/service.go`).
- OpenClaw: adaptive retry ceiling + branch recovery + profile/model rotation (`src/agents/pi-embedded-runner/run.ts`).

2. **Tool Call Robustness**
- Lite now: strict `TOOL_CALL ` prefix + direct JSON decode, malformed output often becomes user-visible failure (`internal/tools/executor.go`).
- OpenClaw: stronger sanitization and tolerant processing around tool events/payload assembly (`run/payloads.ts`, `handlers.tools.ts`).

3. **Error Taxonomy and User Messaging**
- Lite now: partial masking, still vulnerable to raw/internal phrasing and fallback乱码 risk in legacy error text (`internal/runtime/service.go`, `internal/agent/client.go`).
- OpenClaw: centralized classification (`rate_limit/timeout/auth/billing/format`) + safe user formatting (`helpers/errors.ts`).

4. **Action Safety (Mutating Tool Calls)**
- Lite now: no persistent "mutation failed but not resolved" model.
- OpenClaw: keeps mutating failures unresolved until same action succeeds (`handlers.tools.ts`).

5. **Context Recovery**
- Lite now: simple summary compaction in memory store.
- OpenClaw: overflow recovery chain (compaction -> truncate oversized tool results -> retry guardrails) in run loop.

6. **Reliability Evaluation Gates**
- Lite now: unit tests/benchmarks exist, but no scenario eval gate tied to real user failure modes.
- OpenClaw: richer run diagnostics and policy-driven resilience checks.

---

## Implementation Strategy Options

1. **Option A (Recommended): Reliability-first core gap closure**
- Prioritize orchestration/tool/error policy and evals first.
- Pros: directly solves "答非所问 + 报错暴露 + 不会自恢复".
- Cons: not feature-expansion oriented.

2. **Option B: Feature-first parity**
- Expand tool/channel features first, reliability later.
- Pros: visible capabilities increase quickly.
- Cons: failure rate and wrong-answer risk stay high.

3. **Option C: Full architecture mimic**
- Port large OpenClaw subsystems into Lite.
- Pros: closest behavior.
- Cons: high complexity, likely violates Lite performance/maintainability goals.

This plan uses **Option A**.

---

### Task 1: Introduce Explicit Agent Orchestration State Machine

**Files:**
- Create: `internal/runtime/orchestrator.go`
- Create: `internal/runtime/orchestrator_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_intelligence_test.go`

**Step 1: Write the failing test**

```go
func TestOrchestratorRecoversFromMalformedToolCallAndContinues(t *testing.T) {
    // malformed TOOL_CALL -> corrected TOOL_CALL -> final answer
}

func TestOrchestratorStopsLoopWithDirectActionableAnswer(t *testing.T) {
    // repeated tool loops should end with direct answer, not endless TOOL_CALL
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run Orchestrator -v`
Expected: FAIL because orchestrator abstraction does not exist.

**Step 3: Write minimal implementation**

```go
type OrchestratorState struct {
    Step int
    MaxSteps int
    ParseFailures int
    ConsecutiveToolErrors int
    LastToolFingerprint string
}
```

- Move loop control out of `runAgentLoop` into `orchestrator.go`.
- Add deterministic state transitions: `Plan -> Tool -> Observe -> Reflect -> Finalize`.
- Add hard global iteration guard (OpenClaw style retry-limit guardrail).

**Step 4: Run tests to verify it passes**

Run: `go test ./internal/runtime -run "Orchestrator|HandleUpdate" -v`
Expected: PASS with old behavior preserved and new loop stability.

**Step 5: Commit**

```bash
git add internal/runtime/orchestrator.go internal/runtime/orchestrator_test.go internal/runtime/service.go internal/runtime/service_intelligence_test.go
git commit -m "feat: add explicit orchestration state machine with loop guardrails"
```

---

### Task 2: Harden Tool Call Parsing + Repair Path

**Files:**
- Create: `internal/tools/parser.go`
- Create: `internal/tools/parser_test.go`
- Modify: `internal/tools/executor.go`
- Modify: `internal/runtime/service.go`

**Step 1: Write the failing test**

```go
func TestParseToolCallAcceptsFencedAndTrailingText(t *testing.T) {
    // TOOL_CALL JSON with markdown fence and trailing assistant text
}

func TestParseToolCallReturnsRecoverableErrorOnBrokenJson(t *testing.T) {
    // invalid JSON should produce typed recoverable parse error
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tools -run ParseToolCall -v`
Expected: FAIL on fenced/mixed outputs.

**Step 3: Write minimal implementation**

```go
func ParseToolCall(raw string) (Call, bool, error) {
    // extract TOOL_CALL line
    // isolate first balanced JSON object
    // decode one object only, ignore trailing narrative
}
```

- Keep strict schema for tool name but tolerant extraction.
- In runtime, convert parse errors into repair prompt context, never raw user-facing errors.

**Step 4: Run tests to verify it passes**

Run: `go test ./internal/tools ./internal/runtime -run "ParseToolCall|MalformedToolCall" -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tools/parser.go internal/tools/parser_test.go internal/tools/executor.go internal/runtime/service.go
git commit -m "feat: harden tool-call parsing and repair flow"
```

---

### Task 3: Add Central Error Policy (Classify + Safe User Copy)

**Files:**
- Create: `internal/runtime/error_policy.go`
- Create: `internal/runtime/error_policy_test.go`
- Modify: `internal/agent/client.go`
- Modify: `internal/runtime/service.go`

**Step 1: Write the failing test**

```go
func TestClassifyErrorKind(t *testing.T) {
    // 429 -> rate_limit, timeout -> timeout, 401 -> auth, 402 -> billing
}

func TestFormatUserSafeErrorMessage(t *testing.T) {
    // output should never include raw provider payload fragments
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run "ClassifyErrorKind|UserSafe" -v`
Expected: FAIL because shared error policy is missing.

**Step 3: Write minimal implementation**

```go
type ErrorKind string
const (
    ErrorRateLimit ErrorKind = "rate_limit"
    ErrorTimeout ErrorKind = "timeout"
    ErrorAuth ErrorKind = "auth"
    ErrorBilling ErrorKind = "billing"
    ErrorFormat ErrorKind = "format"
    ErrorUnknown ErrorKind = "unknown"
)
```

- Create classifier inspired by OpenClaw `classifyFailoverReason` logic.
- Replace ad-hoc fallback text with policy-based safe copy.
- Ensure fallback text is pure UTF-8 and concise.

**Step 4: Run tests to verify it passes**

Run: `go test ./internal/runtime ./internal/agent -v`
Expected: PASS and no internal error leakage in responses.

**Step 5: Commit**

```bash
git add internal/runtime/error_policy.go internal/runtime/error_policy_test.go internal/agent/client.go internal/runtime/service.go
git commit -m "feat: centralize error classification and user-safe messaging"
```

---

### Task 4: Add Mutating Tool Safety Policy

**Files:**
- Create: `internal/tools/policy.go`
- Create: `internal/tools/policy_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/tools/executor.go`
- Modify: `internal/runtime/service_intelligence_test.go`

**Step 1: Write the failing test**

```go
func TestMutationFailureMustNotBeReportedAsSuccess(t *testing.T) {
    // skill_install failure followed by optimistic assistant text should be blocked/rewritten
}

func TestMutationFailureClearsOnlyOnMatchingSuccess(t *testing.T) {
    // unresolved mutation error persists until same action succeeds
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run "Mutation" -v`
Expected: FAIL because mutation state is not modeled.

**Step 3: Write minimal implementation**

```go
type PendingMutationFailure struct {
    Tool string
    Fingerprint string
    Error string
}
```

- Tag mutating tools (`skill_install`, future write/deploy tools).
- Persist unresolved mutation failure in per-request state.
- If unresolved, inject warning into reflection prompt and prevent false-success final response.

**Step 4: Run tests to verify it passes**

Run: `go test ./internal/tools ./internal/runtime -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tools/policy.go internal/tools/policy_test.go internal/tools/executor.go internal/runtime/service.go internal/runtime/service_intelligence_test.go
git commit -m "feat: enforce mutating-tool failure safety policy"
```

---

### Task 5: Upgrade Context Recovery for Oversized Tool Outputs

**Files:**
- Create: `internal/runtime/context_recovery.go`
- Create: `internal/runtime/context_recovery_test.go`
- Modify: `internal/memory/store.go`
- Modify: `internal/runtime/service.go`

**Step 1: Write the failing test**

```go
func TestToolOutputOverflowTriggersTruncationAndRetry(t *testing.T) {
    // oversized tool output should be summarized/truncated, then loop retries
}

func TestContextRecoveryStopsAfterMaxAttempts(t *testing.T) {
    // must return stable fallback after cap, not spin forever
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run "ContextRecovery|Overflow" -v`
Expected: FAIL because overflow recovery chain does not exist.

**Step 3: Write minimal implementation**

```go
func TruncateToolOutputForContext(raw string, maxChars int) string {
    // keep head+tail and include truncation marker
}
```

- Add lightweight overflow recovery chain in runtime loop:
  - detect oversized step context
  - truncate tool output snapshots
  - retry once/twice with guard
- Keep memory compaction simple; do not add full OpenClaw compactor.

**Step 4: Run tests to verify it passes**

Run: `go test ./internal/memory ./internal/runtime -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/runtime/context_recovery.go internal/runtime/context_recovery_test.go internal/memory/store.go internal/runtime/service.go
git commit -m "feat: add lightweight context overflow recovery for tool outputs"
```

---

### Task 6: Add Reliability Eval Gate and Release Criteria

**Files:**
- Create: `scripts/evals/cases.json`
- Create: `scripts/evals/run.go`
- Modify: `README.md`
- Modify: `internal/runtime/service_benchmark_test.go`

**Step 1: Write failing eval cases**

```json
[
  {
    "name": "nvda_price_should_not_leak_parse_error",
    "input": "nvda股价",
    "expect_not_contains": ["parse tool call", "invalid character", "agent request failed"]
  },
  {
    "name": "docker_status_should_return_data_not_command_hint",
    "input": "当前vps docker容器情况",
    "expect_contains": ["Name | State | Status | Image"]
  }
]
```

**Step 2: Run eval to verify baseline fails**

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/cases.json`
Expected: FAIL on at least one current known weak scenario.

**Step 3: Write minimal implementation**

```go
// scripts/evals/run.go
// load cases, run mocked runtime flow, assert contain/not-contain, print pass ratio
```

- Add benchmark for multi-step tool-call flow.
- Document release gates in README:
  - all tests pass
  - eval pass rate >= 90%
  - benchmark regression <= 15%.

**Step 4: Run full verification**

Run: `go test ./... -v`
Expected: PASS.

Run: `go test ./internal/runtime -bench BenchmarkHandleUpdate -benchmem -run ^$`
Expected: benchmark output generated without failure.

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/cases.json`
Expected: PASS with summary.

**Step 5: Commit**

```bash
git add scripts/evals/cases.json scripts/evals/run.go README.md internal/runtime/service_benchmark_test.go
git commit -m "chore: add reliability eval gate and release criteria"
```

---

## Delivery Milestones

1. **M1 (2-3 days):** Task 1-2 complete, parser/orchestration failures no longer user-visible.
2. **M2 (2 days):** Task 3-4 complete, error and mutation safety behavior aligned with mature pattern.
3. **M3 (1-2 days):** Task 5-6 complete, context resilience + eval gates enforce quality.

## Out of Scope (Keep Lite Lightweight)

- Full OpenClaw gateway/control-plane architecture.
- Full auth-profile/account rotation subsystem.
- Multi-channel parity beyond Telegram.

