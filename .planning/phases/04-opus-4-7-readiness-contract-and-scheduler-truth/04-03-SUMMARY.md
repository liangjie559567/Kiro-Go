---
phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
plan: 03
subsystem: proxy
tags: [go, readiness, scheduler, opus-4-7, admin-api]
requires:
  - phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
    provides: Account/model real content-success evidence from Plans 04-01 and 04-02
provides:
  - Versioned Opus 4.7 fleet readiness contract
  - Shared scheduler/readiness account eligibility explanations
  - Exact safeConcurrency formula and readiness reason codes
affects: [phase-04, phase-05, readiness, scheduler, admin-api]
tech-stack:
  added: []
  patterns: [shared-readiness-row-builder, versioned-admin-contract, safe-concurrency-formula]
key-files:
  created:
    - .planning/phases/04-opus-4-7-readiness-contract-and-scheduler-truth/04-03-SUMMARY.md
  modified:
    - pool/account.go
    - proxy/ecosystem_ops.go
    - proxy/ecosystem_ops_test.go
key-decisions:
  - "Fleet readiness remains on the existing route and now returns contractVersion opus-4.7-readiness.1."
  - "Scheduler preview and fleet readiness share a single account row builder for eligibility, reasonCodes, and content-success evidence."
  - "A small pool read API exposes per-account model breaker state without exposing credentials."
patterns-established:
  - "readinessAccountRows is the shared source for admin scheduler/readiness account explanations."
  - "Blocked readiness always returns safeConcurrency 0; non-blocked readiness uses min(local, admission effective concurrency)."
requirements-completed: [RDY-01, RDY-02, RDY-03, RDY-04]
duration: 8 min
completed: 2026-05-21
---

# Phase 04 Plan 03: Readiness Scheduler Contract Summary

**Fleet readiness is now a versioned Opus 4.7 contract aligned with scheduler preview eligibility rows**

## Performance

- **Duration:** 8 min
- **Started:** 2026-05-21T05:37:29Z
- **Completed:** 2026-05-21T05:45:53Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Added shared readiness account rows with stable `reasonCodes` for disabled, cooling down, model breaker open, token expired, usage limit, model not listed, and eligible states.
- Extended fleet readiness with `contractVersion`, `admissionEffectiveConcurrency`, top-level `reasonCodes`, `recommendedAction`, model-breaker counts, and account content-success timestamps.
- Locked safe concurrency behavior: blocked responses return `0`; healthy/degraded responses return `min(locallySchedulableAccounts, admissionEffectiveConcurrency)`.

## Task Commits

1. **Task 1: Share Opus 4.7 account eligibility explanations** - `7913210`
2. **Task 2: Lock the versioned fleet readiness response** - `7913210`

**Plan metadata:** this summary commit

## Files Created/Modified

- `pool/account.go` - Added `ModelAccountBlockState` read API for safe per-account model breaker explanation.
- `proxy/ecosystem_ops.go` - Added shared row builder and versioned fleet readiness contract fields.
- `proxy/ecosystem_ops_test.go` - Added parity, contract, safe-concurrency, and separate content/fallback evidence tests.

## Decisions Made

- Added the smallest pool API needed for row-level `model_breaker_open` parity instead of duplicating breaker internals in proxy code.
- Preserved existing response fields while adding contract fields for downstream compatibility.
- Kept reason codes as arrays so accounts with multiple blockers remain machine-readable.

## Deviations from Plan

### Auto-fixed Issues

**1. Missing row-level breaker visibility**
- **Found during:** Task 1
- **Issue:** Existing pool API exposed aggregate model block state but not per-account breaker state, so scheduler/readiness rows could not honestly emit `model_breaker_open`.
- **Fix:** Added `ModelAccountBlockState` as a read-only pool method returning breaker status, reason, retry time, and blocked boolean.
- **Files modified:** `pool/account.go`
- **Verification:** `go test ./proxy -run 'TestSchedulerPreview.*FleetReadiness|TestFleetReadiness.*Eligibility|TestSchedulerPreview' -count=1`
- **Committed in:** `7913210`

**Total deviations:** 1 auto-fixed
**Impact on plan:** Narrow API addition required to satisfy RDY-04 parity without unsafe proxy-side introspection.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Verification

- `go test ./proxy -run 'TestSchedulerPreview.*FleetReadiness|TestFleetReadiness.*Eligibility|TestSchedulerPreview' -count=1` - passed
- `go test ./proxy -run 'TestFleetReadiness' -count=1` - passed
- `go test ./proxy -run 'TestFleetReadiness|TestSchedulerPreview' -count=1` - passed
- `go test ./proxy ./pool -count=1` - passed
- `git diff --check -- pool/account.go proxy/ecosystem_ops.go proxy/ecosystem_ops_test.go` - passed

## Next Phase Readiness

Ready for Plan 04-04. The readiness API now exposes the contract and reason-code truth that retry budget and stream no-replay behavior can reference in logs and pressure metadata.

---
*Phase: 04-opus-4-7-readiness-contract-and-scheduler-truth*
*Completed: 2026-05-21*
