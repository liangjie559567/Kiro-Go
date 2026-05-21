---
phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
plan: 05
subsystem: ui
tags: [admin-ui, readiness, opus-4-7, fleet-health]
requires:
  - phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
    provides: Versioned readiness contract and account eligibility rows from Plan 04-03
provides:
  - Admin fleet card rendering for the Opus 4.7 readiness contract
  - Compact contract, status, reason-code, content-success, and fallback evidence display
  - Masked account-level readiness evidence rows
affects: [phase-04, phase-05, admin-ui, readiness]
tech-stack:
  added: []
  patterns: [single-file-admin-ui, compact-operational-card, escaped-readiness-rendering]
key-files:
  created:
    - .planning/phases/04-opus-4-7-readiness-contract-and-scheduler-truth/04-05-SUMMARY.md
  modified:
    - web/index.html
key-decisions:
  - "Extended the existing Opus 4.7 fleet health card instead of adding a new page or UI framework."
  - "Rendered only safe readiness API fields and continued using escapeHtml for dynamic text."
patterns-established:
  - "Admin readiness evidence should stay in the existing compact fleet card and wrap long reason codes."
  - "Unavailable readiness renders the exact Fleet readiness unavailable state."
requirements-completed: [RDY-01, RDY-02, RDY-03, RDY-04]
duration: 5 min
completed: 2026-05-21
---

# Phase 04 Plan 05: Admin Fleet Evidence Summary

**The existing admin fleet card now renders the versioned Opus 4.7 readiness contract and safe account evidence**

## Performance

- **Duration:** 5 min
- **Started:** 2026-05-21T06:00:56Z
- **Completed:** 2026-05-21T06:05:26Z
- **Tasks:** 2
- **Files modified:** 1

## Accomplishments

- Added exact UI-spec labels: `Contract`, `Status`, `Safe concurrency`, `Retry after`, `Reasons`, `Real content success`, and `Stable fallbacks`.
- Displayed recommended action, local schedulable count, effective admission concurrency, fleet counts, circuit state, and pressure evidence in the existing card.
- Added compact account rows for safe fields: account ID, masked email, eligibility, reason codes, model cache state, runtime health, and content-success timestamp.
- Preserved the existing route `/admin/api/fleet/readiness?model=claude-opus-4-7` and refresh button.

## Task Commits

1. **Task 1: Render readiness contract fields in the existing fleet card** - `1d295c8`
2. **Task 2: Guard UI rendering against secret exposure and missing fields** - `1d295c8`

**Plan metadata:** this summary commit

## Files Created/Modified

- `web/index.html` - Extended `loadFleetReadiness()` rendering for contract fields, reason-code wrapping, unavailable state, and safe account rows.

## Decisions Made

- Used inline styles already common in the single-file admin UI to avoid introducing a build step or design system.
- Kept dynamic values escaped and avoided secret-bearing fields such as tokens, raw headers, config paths, and credentials.

## Deviations from Plan

None - plan executed in the allowed UI file.

## Issues Encountered

- `web/index.html` had unrelated pre-existing unstaged changes. The 04-05 commit staged only the `loadFleetReadiness()` hunk and left the unrelated request-log UI changes untouched.

## User Setup Required

None - no external service configuration required.

## Verification

- `rg -n 'Contract|Safe concurrency|Retry after|Reasons|Real content success|Stable fallbacks|Opus 4\.7 fleet health' web/index.html` - passed
- `awk '/async function loadFleetReadiness\(\)/,/async function loadWebSearchDiagnostics\(\)/' web/index.html | rg -n 'data/config|rawHeaders|KIRO_CLI_HOME|accessToken|refreshToken|clientSecret|apiKey|credential'; test $? -ne 0` - passed
- `go test ./proxy -run TestFleetReadiness -count=1` - passed
- `git diff --cached --check -- web/index.html` - passed

## Next Phase Readiness

Phase 4 is complete. Phase 5 can now consume Kiro-Go's readiness contract from both API and admin evidence perspectives.

---
*Phase: 04-opus-4-7-readiness-contract-and-scheduler-truth*
*Completed: 2026-05-21*
