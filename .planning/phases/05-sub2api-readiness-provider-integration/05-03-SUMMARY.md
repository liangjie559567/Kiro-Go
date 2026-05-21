---
phase: 05-sub2api-readiness-provider-integration
plan: 03
subsystem: sub2api
tags: [observability, tests, readiness]
provides:
  - Readiness scheduling decision metadata
  - Regression tests for provider and scheduler behavior
  - Phase 5 completion evidence
affects: [phase-05, sub2api, observability]
key-files:
  created:
    - .planning/phases/05-sub2api-readiness-provider-integration/05-03-SUMMARY.md
  modified:
    - /www/sub2api/backend/internal/service/openai_account_scheduler_test.go
    - /www/sub2api/backend/internal/service/kiro_readiness_test.go
requirements-completed: [S2A-01, S2A-02, S2A-03, S2A-04, S2A-05, S2A-06]
completed: 2026-05-21
---

# Phase 05 Plan 03: Observability and Regression Summary

Phase 5 is complete: sub2api can consume Kiro-Go readiness, make scheduling decisions from it, and expose safe decision evidence.

## Accomplishments

- Extended `OpenAIAccountScheduleDecision` with readiness status, cache-hit, TTL, retry-after, and safe-concurrency fields.
- Added structured `kiro_go_readiness_decision` logs with account ID, requested/effective model, provider host, TTL, retry-after, safe concurrency, status, reasons, and sanitized errors.
- Added scheduler regression test proving blocked Kiro-Go candidate skip uses the effective upstream model, not the requested model.
- Confirmed no runtime secrets or credential-bearing config files were read.

## Verification

- `go test ./internal/service -run 'TestKiroGoReadinessProvider|TestOpenAIGatewayService_SelectAccountWithScheduler_KiroReadiness|TestOpenAIGatewayService_SelectAccountWithScheduler' -count=1` - passed
- `go test ./internal/service -count=1` - passed

## Notes

- `/www/sub2api/deploy/docker-compose.current.yml` was pre-existing untracked state and was not touched or committed.
- `05-SPEC.md` remains stale historical context; Phase 5 followed ROADMAP/REQUIREMENTS as recorded in `05-CONTEXT.md`.
