# Web Search Source Citation and Recency Control Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Upgrade `web_search` so outputs include citeable sources and configurable freshness controls, aligned with OpenClaw-style tool reliability expectations.

**Architecture:** Extend tool-call schema with optional search controls (`recency_days`, `max_results`), replace instant-answer-only parsing with web-result extraction, and format output into a stable source list for downstream reasoning. Keep implementation in stdlib and preserve existing tool executor contract.

**Tech Stack:** Go 1.22+, stdlib (`net/http`, `net/url`, `regexp`, `html`, `strings`, `time`), existing `internal/tools` package and tests.

---

### Task 1: Add Failing Tests for Source Citation and Freshness Controls

**Files:**
- Modify: `internal/tools/executor_test.go`

**Step 1: Write the failing tests**

Add:
- `TestExecutorWebSearchReturnsCitedSources`
- `TestExecutorWebSearchAppliesRecencyAndMaxResults`

Both tests should:
- Mock web-search upstream via `httptest.Server`.
- Provide deterministic HTML result entries.
- Assert output includes numbered source list with title + URL.
- Assert freshness parameter (`df`) is sent when `recency_days` is provided.
- Assert `max_results` limits returned entries.

**Step 2: Run tests to verify failure**

Run: `go test ./internal/tools -run WebSearch -v`
Expected: FAIL because current implementation uses DuckDuckGo instant-answer API and has no citation/freshness support.

### Task 2: Implement Web Search Citation + Recency

**Files:**
- Modify: `internal/tools/executor.go`

**Step 1: Add minimal implementation**

Changes:
- Extend `Call` with optional `RecencyDays` and `MaxResults`.
- Add configurable web-search endpoint fields on `Executor` for testability.
- Implement HTML result parsing for title/link/snippet extraction.
- Normalize redirect links to source URLs.
- Map `recency_days` to coarse DuckDuckGo freshness buckets (`d/w/m/y`).
- Format result payload as stable source list.

**Step 2: Run tests**

Run: `go test ./internal/tools -run WebSearch -v`
Expected: PASS.

### Task 3: Update Prompt/Docs for New Tool Parameters

**Files:**
- Modify: `internal/config/config.go`
- Modify: `README.md`

**Step 1: Update prompt and docs**

Document supported fields:
- `query` (required)
- `recency_days` (optional, freshness control)
- `max_results` (optional, source count cap)

**Step 2: Verify full suite**

Run: `go test ./...`
Expected: PASS.
