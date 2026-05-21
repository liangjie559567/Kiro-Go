---
phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
plan: 02
subsystem: proxy
tags: [go, request-log, opus-4-7, readiness, content-success]
requires:
  - phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
    provides: In-memory account/model real content-success evidence from Plan 04-01
provides:
  - Shared real-content success predicate for request logs and account routing evidence
  - Handler account/model content-success recording for Claude, OpenAI Chat, and OpenAI Responses paths
affects: [phase-04, readiness, scheduler, routing, request-log]
tech-stack:
  added: []
  patterns: [shared-real-content-predicate, handler-pool-evidence-recording]
key-files:
  created:
    - .planning/phases/04-opus-4-7-readiness-contract-and-scheduler-truth/04-02-SUMMARY.md
  modified:
    - proxy/request_log.go
    - proxy/request_log_test.go
    - proxy/handler.go
    - proxy/handler_test.go
key-decisions:
  - "One package-local predicate classifies real content from output tokens, structured output count, or non-empty text/reasoning."
  - "Stable downstream fallback and empty completion do not update account/model pool success evidence."
patterns-established:
  - "Handlers call recordModelContentSuccessIfPresent immediately beside request-log content-success updates."
  - "Request-log and pool evidence share realContentSuccessTokenCount so transport-only success cannot diverge."
requirements-completed: [RDY-02, RDY-03]
duration: 31 min
completed: 2026-05-21
---

# Phase 04 Plan 02: Handler Content-Success Evidence Summary

**Request logs and routing evidence now share one real-content definition across Claude, OpenAI Chat, and OpenAI Responses handlers**

## Performance

- **Duration:** 31 min
- **Started:** 2026-05-21T05:06:00Z
- **Completed:** 2026-05-21T05:37:29Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Moved real-content classification into `proxy/request_log.go` as `realContentSuccessTokenCount`.
- Added `recordModelContentSuccessIfPresent` and wired it into Claude stream/non-stream, OpenAI Chat stream/non-stream, and OpenAI Responses stream/non-stream success paths.
- Added tests for text, reasoning, structured output, empty completion, stable fallback, and representative handler account/model evidence recording.

## Task Commits

1. **Task 1: Centralize real content-success classification** - `e8a0b31`
2. **Task 2: Record pool success evidence from protocol handlers** - `e8a0b31`

**Plan metadata:** this summary commit

## Files Created/Modified

- `proxy/request_log.go` - Added shared real-content predicate used by request-log updates and handler pool evidence recording.
- `proxy/request_log_test.go` - Added predicate coverage for output tokens, structured output, text, reasoning text, and empty completion.
- `proxy/handler.go` - Added account/model evidence recording helper and invoked it next to existing content-success request-log updates.
- `proxy/handler_test.go` - Added handler tests proving real content records pool evidence while stable fallback and empty completion do not.

## Decisions Made

- Kept the predicate package-local and token-count returning so existing request-log `UpstreamContentTokens` semantics are preserved.
- Recorded pool evidence only from the selected account and effective model at existing successful completion points.
- Left stable fallback writers untouched except for tests proving they do not record pool evidence.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- Initial new tests raced with asynchronous account stats writes during temp-directory cleanup. Added existing `waitForAccountRequestCount` synchronization to the new handler tests.
- Full `go test ./proxy ./pool -count=1` failed once in pre-existing `TestEnsureValidTokenCoalescesConcurrentRefreshesPerAccount`, which reported two auth refreshes instead of one. The focused 04-02 gates and `pool` package passed; this failure is outside the changed content-success paths.

## User Setup Required

None - no external service configuration required.

## Verification

- `go test ./proxy -run 'TestRequestLog|Test.*Stable.*Fallback|Test.*ContentSuccess' -count=1` - passed
- `go test ./proxy -run 'Test.*ContentSuccess|Test.*Stable.*Fallback|TestHandleClaude|TestOpenAI' -count=1` - passed
- `go test ./proxy ./pool -count=1` - `pool` passed; `proxy` failed in pre-existing `TestEnsureValidTokenCoalescesConcurrentRefreshesPerAccount`
- `git diff --check -- proxy/request_log.go proxy/request_log_test.go proxy/handler.go proxy/handler_test.go` - passed

## Next Phase Readiness

Ready for Plan 04-03. Pool evidence now has handler-produced timestamps from real content only, so readiness and scheduler contract work can consume the same truth without treating fallback or empty completions as success.

---
*Phase: 04-opus-4-7-readiness-contract-and-scheduler-truth*
*Completed: 2026-05-21*
