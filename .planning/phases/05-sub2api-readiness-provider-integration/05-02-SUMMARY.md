---
phase: 05-sub2api-readiness-provider-integration
plan: 02
subsystem: sub2api
tags: [scheduler, sticky, load-balance, fallback-wait]
provides:
  - Readiness gating before OpenAI and generic account slot acquisition
  - Blocked candidate skip and optional temporary unscheduling
  - Degraded candidate ranking penalty and safe-concurrency cap
affects: [phase-05, sub2api, scheduler]
key-files:
  modified:
    - /www/sub2api/backend/internal/service/gateway_service.go
    - /www/sub2api/backend/internal/service/openai_gateway_service.go
    - /www/sub2api/backend/internal/service/openai_account_scheduler.go
    - /www/sub2api/backend/internal/service/openai_ws_forwarder.go
requirements-completed: [S2A-02, S2A-03, S2A-04, S2A-05]
completed: 2026-05-21
---

# Phase 05 Plan 02: Scheduler Gating Summary

sub2api now checks Kiro-Go readiness before dispatching Kiro-Go Opus 4.7 candidates.

## Accomplishments

- Gated OpenAI previous-response sticky, session sticky, load-balance, and fallback-wait paths before slot acquisition.
- Gated generic GatewayService routed, sticky, load-balance, legacy fallback, and fallback-wait paths.
- Used effective upstream model resolution for readiness checks.
- Skipped `blocked` candidates without permanent account errors; optional temp-unschedule uses bounded readiness TTL/retry-after.
- Penalized `degraded` candidates and enforced Kiro-Go `safeConcurrency` from current concurrency load.

## Verification

- `go test ./internal/service -run 'TestKiroGoReadinessProvider|TestOpenAIGatewayService_SelectAccountWithScheduler_KiroReadiness|TestOpenAIGatewayService_SelectAccountWithScheduler' -count=1` - passed
- `go test ./internal/service -count=1` - passed
