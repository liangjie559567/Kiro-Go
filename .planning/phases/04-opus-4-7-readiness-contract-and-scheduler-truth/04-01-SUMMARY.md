---
phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
plan: 01
subsystem: pool
tags: [go, account-routing, opus-4-7, readiness, content-success]
requires: []
provides:
  - In-memory account/model real content-success evidence
  - Routing priority bias for fresher eligible Opus 4.7 success
affects: [phase-04, readiness, scheduler, routing]
tech-stack:
  added: []
  patterns: [mutex-protected-runtime-state, pool-candidate-comparison]
key-files:
  created:
    - .planning/phases/04-opus-4-7-readiness-contract-and-scheduler-truth/04-01-SUMMARY.md
  modified:
    - pool/account.go
    - pool/account_test.go
key-decisions:
  - "Account/model real content-success evidence is in-memory runtime pool state, not persisted in config."
  - "Success evidence participates only after existing model visibility, cooldown, breaker, token, and usage eligibility filters pass."
patterns-established:
  - "RecordModelContentSuccess stores the newest timestamp per account plus normalized model key."
  - "Model-aware pool comparison can use success evidence without changing non-model GetNext behavior."
requirements-completed: [RDY-03]
duration: 25 min
completed: 2026-05-21
---

# Phase 04 Plan 01: Pool Content-Success Routing Evidence Summary

**In-memory Opus 4.7 account success evidence now biases eligible account routing without bypassing blockers**

## Performance

- **Duration:** 25 min
- **Started:** 2026-05-21T05:05:00Z
- **Completed:** 2026-05-21T05:30:13Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Added `RecordModelContentSuccess` and `ModelContentSuccess` to `pool.AccountPool`.
- Added model-aware routing comparison that prefers fresher content-success evidence after normal eligibility filters.
- Added tests for latest timestamp behavior, account/model isolation, fresher eligible selection, and disabled/cooldown/breaker/token/usage/model-list blockers.

## Task Commits

1. **Task 1: Add in-memory account/model content-success evidence** - `889870a`
2. **Task 2: Bias eligible Opus 4.7 account selection by fresher success** - `889870a`

**Plan metadata:** this summary commit

## Files Created/Modified

- `pool/account.go` - Added runtime model-success map, record/query APIs, model-aware comparison, and explicit enabled-account filtering when mixed enabled state is present.
- `pool/account_test.go` - Added content-success evidence and routing eligibility tests.

## Decisions Made

- Kept success evidence in memory to avoid expanding secret-bearing runtime config.
- Reused existing breaker model normalization semantics for model-success keys.
- Preserved round-robin behavior by keeping success-evidence comparison inactive for `StrategyRoundRobin`.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- The first GREEN run showed the model routing loop returned the first idle healthy account before comparing later fresher-success candidates. Fixed by deferring the early idle return when any success evidence exists for that model.
- Existing tests often use zero-value `config.Account{}` as available. To support the disabled blocker test without breaking those fixtures, `accountBaseUsableLocked` treats `Enabled:false` as disabled only when at least one account in the pool has `Enabled:true`.

## User Setup Required

None - no external service configuration required.

## Verification

- `go test ./pool -run 'Test.*ContentSuccess' -count=1` - passed
- `go test ./pool -run 'TestBeginNextForModel|Test.*ContentSuccess' -count=1` - passed
- `go test ./pool -count=1` - passed
- `git diff --check -- pool/account.go pool/account_test.go` - passed

## Next Phase Readiness

Ready for Plan 04-02. Handler/request-log code can now call `h.pool.RecordModelContentSuccess(accountID, model, timestamp)` when real content evidence is established.

---
*Phase: 04-opus-4-7-readiness-contract-and-scheduler-truth*
*Completed: 2026-05-21*
