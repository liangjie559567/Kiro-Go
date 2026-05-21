---
phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
plan: 04
subsystem: proxy
tags: [go, opus-4-7, retry-budget, streaming, request-logs]
requires:
  - phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
    provides: Real content-success evidence and versioned readiness contract from Plans 04-01 through 04-03
provides:
  - Bounded Opus 4.7 attempt-budget exhaustion metadata
  - Streaming pre-content retry and post-content no-replay coverage
  - Request-log attempt trace evidence for OpenAI protocol attempts
affects: [phase-04, phase-05, opus-4-7, streaming, sub2api]
tech-stack:
  added: []
  patterns: [bounded-retry-budget, stream-start-no-replay, request-log-attempt-trace]
key-files:
  created:
    - .planning/phases/04-opus-4-7-readiness-contract-and-scheduler-truth/04-04-SUMMARY.md
  modified:
    - proxy/handler.go
    - proxy/handler_test.go
key-decisions:
  - "Use opus47_budget_exhausted as the stable public reason for Opus 4.7 attempt-budget exhaustion."
  - "OpenAI Chat and Responses attempts now append request-log failure/success trace entries, matching Claude trace evidence."
  - "Started streams terminate with protocol-safe SSE error events instead of returning to account retry."
patterns-established:
  - "Retryable Opus 4.7 exhaustion responses carry X-Kiro-Go-Retryable, Retry-After, and opus47_budget_exhausted."
  - "Stream retry tests distinguish pre-downstream-content retry from post-downstream-content no-replay."
requirements-completed: [RDY-05, RDY-06]
duration: 18 min
completed: 2026-05-21
---

# Phase 04 Plan 04: Opus Retry And Stream Safety Summary

**Opus 4.7 request exhaustion and streaming replay boundaries are now locked by handler-level protocol tests**

## Performance

- **Duration:** 18 min
- **Started:** 2026-05-21T05:46:00Z
- **Completed:** 2026-05-21T06:00:56Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Standardized Opus 4.7 bounded-attempt exhaustion reason metadata on `opus47_budget_exhausted`.
- Added OpenAI Chat and OpenAI Responses non-stream budget exhaustion tests that assert bounded attempts, retryable pressure headers, `Retry-After`, and request-log failure traces.
- Added streaming tests proving Claude and OpenAI Chat retry before downstream SSE content starts, strengthened Responses stream account-switch evidence, and locked post-content no-replay behavior across Claude, OpenAI Chat, and OpenAI Responses.
- Added request-log attempt traces for OpenAI Chat and Responses success/failure attempts so post-start stream failures are auditable.

## Task Commits

1. **Task 1: Assert bounded Opus 4.7 retry exhaustion metadata** - `d155b0c`
2. **Task 2: Lock streaming no-replay after downstream SSE start** - `d155b0c`

**Plan metadata:** this summary commit

## Files Created/Modified

- `proxy/handler.go` - Added shared exhaustion reason constant and OpenAI request-log attempt trace entries.
- `proxy/handler_test.go` - Added bounded OpenAI budget tests and stream retry/no-replay protocol tests.

## Decisions Made

- Reused the existing SSE error behavior after downstream stream start instead of adding a new protocol surface.
- Kept retries before the first downstream content event, because no client-visible stream has started and account retry is still transparent.
- Logged OpenAI protocol attempts symmetrically with Claude so retry exhaustion and post-start stream failures can be inspected from request logs.

## Deviations from Plan

None - plan executed within the allowed handler files.

## Issues Encountered

- Short post-content test payloads did not flush Claude/OpenAI Chat downstream content before the injected upstream read error. The tests were adjusted to use longer content so the handler enters the started-stream branch.
- Successful pre-content stream retry tests needed to wait for async account request-count persistence before temp config cleanup.

## User Setup Required

None - no external service configuration required.

## Verification

- `go test ./proxy -run 'Test.*AttemptBudget|Test.*Opus.*Pressure|TestHandleClaude.*Opus47|TestOpenAI.*Opus' -count=1` - passed
- `go test ./proxy -run 'Test.*Stream.*Retry|Test.*Stream.*Started|Test.*SSE' -count=1` - passed
- `go test ./proxy -run 'Test.*AttemptBudget|Test.*Opus.*Pressure|Test.*Stream.*Retry|Test.*Stream.*Started|Test.*SSE' -count=1` - passed
- `go test ./proxy -count=1` - passed
- `git diff --check -- proxy/handler.go proxy/handler_test.go` - passed

## Next Phase Readiness

Ready for Plan 04-05. The backend now exposes the bounded retry and stream-safety truth that the admin fleet card can surface as operator evidence.

---
*Phase: 04-opus-4-7-readiness-contract-and-scheduler-truth*
*Completed: 2026-05-21*
