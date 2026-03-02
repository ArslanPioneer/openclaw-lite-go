# OpenClaw Lite Reliability Upgrade Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `openclaw-lite-go` solve user tasks through multi-step plan/execute/reflect loops with robust self-recovery, instead of leaking parser/internal errors or giving non-actionable replies.

**Architecture:** Keep the current single-process Go runtime, but split intelligence flow into three explicit layers: `tool-call parsing + repair`, `loop state machine + recovery policy`, and `error classification + user-safe output policy`. Align each layer with proven OpenClaw patterns (retry guardrails, tool warning policy, mutation-aware error retention, and sanitized user payloads) while preserving low-latency Telegram handling.

**Tech Stack:** Go 1.22+, stdlib (`encoding/json`, `net/http`, `context`, `time`, `strings`), existing modules in `internal/runtime`, `internal/tools`, `internal/agent`.

---

## OpenClaw Reference Map (What to Copy, Not Rebuild)

- `openclaw/src/agents/pi-embedded-runner/run.ts`
  - Bounded retry loop and branch recovery (`resolveMaxRunRetryIterations`, retry-limit user fallback).
  - Context-overflow recovery chain: compaction -> truncate oversized tool results -> graceful give-up.
  - Profile/failover rotation on timeout/rate-limit style failures.
- `openclaw/src/agents/pi-embedded-subscribe.handlers.tools.ts`
  - Tool lifecycle tracking, sanitized tool outputs, and mutating-action error retention until same action succeeds.
- `openclaw/src/agents/pi-embedded-runner/run/payloads.ts`
  - Tool error warning policy, duplicate warning suppression, and no raw provider payload leakage.
- `openclaw/src/agents/pi-embedded-helpers/errors.ts`
  - Error taxonomy (`rate_limit`, `timeout`, `billing`, `auth`, `format`) with user-safe formatting.

Use these as design references only. Do not port OpenClaw complexity wholesale.

**Execution discipline:** @superpowers/test-driven-development, @superpowers/systematic-debugging, @superpowers/verification-before-completion

---

### Task 1: Harden Tool-Call Parsing and Recovery Input

**Files:**
- Create: `internal/tools/parser.go`
- Create: `internal/tools/parser_test.go`
- Modify: `internal/tools/executor.go`
- Modify: `internal/tools/executor_test.go`

**Step 1: Write the failing tests**

```go
func TestParseToolCallExtractsFirstJSONObjectFromMixedText(t *testing.T) {
    raw := "TOOL_CALL {\"name\":\"stock_price\",\"query\":\"NVDA\"}\nNow I will summarize."
    call, requested, err := ParseToolCall(raw)
    if err != nil || !requested || call.Name != "stock_price" || call.Query != "NVDA" {
        t.Fatalf("unexpected parse result: call=%+v requested=%v err=%v", call, requested, err)
    }
}

func TestParseToolCallHandlesMarkdownFence(t *testing.T) {
    raw := "```\nTOOL_CALL {\"name\":\"web_search\",\"query\":\"NVDA price\"}\n```"
    _, requested, err := ParseToolCall(raw)
    if err != nil || !requested {
        t.Fatalf("expected fenced tool call to parse, err=%v", err)
    }
}

func TestParseToolCallReturnsRecoverableErrorOnBrokenJSON(t *testing.T) {
    raw := "TOOL_CALL {\"name\":\"web_search\",\"query\":\"NVDA\""
    _, requested, err := ParseToolCall(raw)
    if !requested || err == nil {
        t.Fatalf("expected recoverable parse error")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/tools -run ParseToolCall -v`
Expected: FAIL on mixed text / fenced format / malformed JSON handling.

**Step 3: Write minimal implementation**

```go
// internal/tools/parser.go
func extractToolJSONPayload(raw string) (string, error) {
    // 1) locate "TOOL_CALL"
    // 2) trim markdown fences / whitespace
    // 3) scan first balanced JSON object { ... }
    // 4) return payload for json.Decoder
}
```

- Move parsing logic out of `executor.go` into `parser.go`.
- `ParseToolCall` should parse one call only and ignore trailing assistant text.
- Keep strict errors, but return parse errors as recoverable input for loop repair prompts.

**Step 4: Run tests to verify pass**

Run: `go test ./internal/tools -v`
Expected: PASS with all parser and executor tests green.

**Step 5: Commit**

```bash
git add internal/tools/parser.go internal/tools/parser_test.go internal/tools/executor.go internal/tools/executor_test.go
git commit -m "feat: harden tool-call parser for mixed assistant output"
```

---

### Task 2: Introduce Explicit Plan-Execute-Reflect Loop State Machine

**Files:**
- Create: `internal/runtime/loop_state.go`
- Create: `internal/runtime/loop_state_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_intelligence_test.go`

**Step 1: Write the failing tests**

```go
func TestRunAgentLoopRepromptsAfterMalformedToolCallThenRecovers(t *testing.T) {
    // agent replies: malformed tool call -> valid tool call -> final answer
    // assert service returns final answer without exposing parser internals
}

func TestRunAgentLoopStopsOnRepeatedSameToolErrorWithActionableFallback(t *testing.T) {
    // repeated tool execution error for same tool/query
    // assert final reply is actionable and does not repeat raw "tool execution error"
}

func TestRunAgentLoopTracksStepObservationsForReflectionPrompt(t *testing.T) {
    // ensure prompt for step N includes concise structured observations from step N-1
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/runtime -run RunAgentLoop -v`
Expected: FAIL because state machine and reflection prompt contracts do not yet exist.

**Step 3: Write minimal implementation**

```go
// internal/runtime/loop_state.go
type LoopState struct {
    Step int
    MaxSteps int
    ParseFailures int
    LastToolName string
    LastToolQuery string
    LastToolError string
    ConsecutiveToolErrors int
    Observations []string
}
```

- Refactor `runAgentLoop` in `service.go` to use `LoopState`.
- Per step: `plan -> maybe tool -> observation -> reflect`.
- On malformed tool call: append structured feedback and retry same step.
- On repeated same tool failure threshold: force direct answer prompt (`Do not call tools anymore`) with concrete next action.
- Preserve hard cap on loop iterations (OpenClaw-style defensive guard).

**Step 4: Run tests to verify pass**

Run: `go test ./internal/runtime -run "RunAgentLoop|HandleUpdate" -v`
Expected: PASS for loop recovery tests and existing intelligence tests.

**Step 5: Commit**

```bash
git add internal/runtime/loop_state.go internal/runtime/loop_state_test.go internal/runtime/service.go internal/runtime/service_intelligence_test.go
git commit -m "feat: add explicit plan-execute-reflect loop with recovery guardrails"
```

---

### Task 3: Add Error Taxonomy and User-Safe Response Policy

**Files:**
- Create: `internal/runtime/error_policy.go`
- Create: `internal/runtime/error_policy_test.go`
- Modify: `internal/agent/client.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/runtime/service_intelligence_test.go`

**Step 1: Write the failing tests**

```go
func TestClassifyAgentError(t *testing.T) {
    cases := []struct{
        in string
        want string
    }{
        {"429 too many requests", "rate_limit"},
        {"context deadline exceeded", "timeout"},
        {"401 unauthorized", "auth"},
        {"402 payment required", "billing"},
    }
}

func TestUserFacingMessageDoesNotLeakRawInternalError(t *testing.T) {
    // input contains raw provider payload or stack-ish message
    // output must be normalized short safe message
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/runtime -run "ClassifyAgentError|UserFacingMessage" -v`
Expected: FAIL because no shared taxonomy/policy layer exists.

**Step 3: Write minimal implementation**

```go
// internal/runtime/error_policy.go
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

- Implement classifier inspired by OpenClaw `classifyFailoverReason` patterns.
- Add `formatUserFacingError(kind ErrorKind) string` and enforce in `service.go` final replies.
- In `agent/client.go`, wrap errors with stable prefixes that classifier can consume.
- Ensure `recoverReplyWithoutExposingInternalError` never emits mojibake/garbled fallback text.

**Step 4: Run tests to verify pass**

Run: `go test ./internal/runtime ./internal/agent -v`
Expected: PASS with no leaked raw internal errors.

**Step 5: Commit**

```bash
git add internal/runtime/error_policy.go internal/runtime/error_policy_test.go internal/agent/client.go internal/runtime/service.go internal/runtime/service_intelligence_test.go
git commit -m "feat: add error taxonomy and user-safe response policy"
```

---

### Task 4: Add Tool Error Warning Policy and Mutation-Aware Guarantees

**Files:**
- Create: `internal/tools/policy.go`
- Create: `internal/tools/policy_test.go`
- Modify: `internal/runtime/service.go`
- Modify: `internal/tools/executor.go`
- Modify: `internal/runtime/service_intelligence_test.go`

**Step 1: Write the failing tests**

```go
func TestMutatingToolFailureCannotBeSilentlyConfirmed(t *testing.T) {
    // simulate skill_install failure, then assistant tries to confirm success
    // assert runtime injects warning and blocks false-success finalization
}

func TestRecoverableToolErrorGetsRepairPromptWithoutUserNoise(t *testing.T) {
    // missing argument/invalid argument errors should trigger model self-repair prompt
    // no raw tool stack details in final user text
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/runtime -run "MutatingTool|RecoverableToolError" -v`
Expected: FAIL because tool warning policy layer does not exist.

**Step 3: Write minimal implementation**

```go
// internal/tools/policy.go
type ToolFailurePolicy struct {
    ShowWarning bool
    IncludeDetails bool
    Recoverable bool
    Mutating bool
}
```

- Add `isMutatingTool(name string)` for `skill_install`, future write tools, and deployment actions.
- Keep unresolved mutating failure state until a matching successful action occurs.
- Deduplicate repeated warnings (normalize text before append).
- Integrate policy output into reflection prompt and final reply assembly.

**Step 4: Run tests to verify pass**

Run: `go test ./internal/tools ./internal/runtime -v`
Expected: PASS including new policy tests and all prior tests.

**Step 5: Commit**

```bash
git add internal/tools/policy.go internal/tools/policy_test.go internal/tools/executor.go internal/runtime/service.go internal/runtime/service_intelligence_test.go
git commit -m "feat: enforce mutation-aware tool failure policy"
```

---

### Task 5: Build Reliability Eval + Performance Gate Before Deployment

**Files:**
- Create: `scripts/evals/reliability_cases.json`
- Create: `scripts/evals/run.go`
- Modify: `internal/runtime/service_benchmark_test.go`
- Modify: `README.md`

**Step 1: Write the failing tests/evals**

```json
[
  {
    "name": "nvda_price_malformed_tool_call",
    "input": "nvda price now",
    "expect_contains": ["NVDA"],
    "expect_not_contains": ["parse tool call", "invalid character", "I cannot"]
  },
  {
    "name": "docker_status_direct_answer",
    "input": "what containers are running on this host",
    "expect_contains": ["Name | State | Status | Image"]
  }
]
```

**Step 2: Run eval to verify baseline fails**

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/reliability_cases.json`
Expected: FAIL on at least malformed tool-call recovery and non-actionable-answer checks (baseline gap).

**Step 3: Implement eval runner and benchmark assertions**

```go
// scripts/evals/run.go
// load cases -> drive Service with stubbed agent/tool -> print pass/fail summary and exit non-zero on failure
```

- Add benchmark scenario for multi-step tool chain and parser-repair path in `service_benchmark_test.go`.
- Define release gate targets in README:
  - Reliability pass rate >= 90% on eval suite.
  - No raw internal error leakage in eval outputs.
  - Benchmark regression <= 15% vs current `BenchmarkHandleUpdateSerial`.

**Step 4: Run full verification**

Run: `go test ./... -v`
Expected: PASS.

Run: `go test ./internal/runtime -bench BenchmarkHandleUpdate -benchmem -run ^$`
Expected: Benchmarks run successfully and allocation trend recorded.

Run: `go run ./scripts/evals/run.go -cases ./scripts/evals/reliability_cases.json`
Expected: PASS with summary like `PASS 9/10` or better.

**Step 5: Commit**

```bash
git add scripts/evals/reliability_cases.json scripts/evals/run.go internal/runtime/service_benchmark_test.go README.md
git commit -m "chore: add reliability eval gate and perf baseline checks"
```

---

## Rollout Checklist

1. Deploy to staging VPS with existing Docker flow (`scripts/docker-deploy.sh`).
2. Run smoke prompts in Telegram:
   - `nvda price now`
   - `/price nvda`
   - `/skills`
   - `what containers are running on this host`
3. Confirm no raw parser/internal error text appears in Telegram output.
4. Promote to production only after eval + benchmark gates pass.

## Non-Goals

- Full OpenClaw profile/account rotation system.
- Full compaction pipeline parity with OpenClaw.
- Multi-agent orchestration.

This plan intentionally copies only high-impact reliability primitives needed for your current failure modes.
