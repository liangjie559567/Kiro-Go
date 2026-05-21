---
phase: 05-sub2api-readiness-provider-integration
plan: 01
subsystem: sub2api
tags: [readiness, scheduler, opus-4-7, sub2api]
requires:
  - phase: 04-opus-4-7-readiness-contract-and-scheduler-truth
    provides: Kiro-Go fleet readiness contract
provides:
  - Configurable Kiro-Go readiness provider in sub2api
  - Cached readiness checks keyed by account/provider/model
  - Fail-open/fail-closed and status-specific TTL behavior
affects: [phase-05, sub2api, scheduler]
key-files:
  created:
    - /www/sub2api/backend/internal/service/kiro_readiness.go
    - /www/sub2api/backend/internal/service/kiro_readiness_test.go
  modified:
    - /www/sub2api/backend/internal/config/config.go
    - /www/sub2api/deploy/config.example.yaml
requirements-completed: [S2A-01, S2A-06]
completed: 2026-05-21
---

# Phase 05 Plan 01: Readiness Provider Summary

sub2api now has a disabled-by-default Kiro-Go readiness provider configuration and a cached client for the Phase 4 fleet readiness contract.

## Accomplishments

- Added `gateway.scheduling.kiro_go_readiness` config with endpoint, timeout, healthy/degraded/blocked/error TTLs, fail mode, model matchers, base URL matchers, and optional temp-unschedule behavior.
- Added safe readiness parsing for status, retry-after, safe concurrency, reason codes, and contract/provider identifiers.
- Added structured scheduler logging for readiness decisions without credentials.
- Added tests for blocked skip, degraded safe-concurrency cap, cache hits, and fail-open/fail-closed.

## Verification

- `go test ./internal/service -run 'TestKiroGoReadinessProvider|TestOpenAIGatewayService_SelectAccountWithScheduler_KiroReadiness|TestOpenAIGatewayService_SelectAccountWithScheduler' -count=1` - passed
- `go test ./internal/service -count=1` - passed
