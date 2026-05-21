# Phase 5 Research: sub2api Readiness Provider Integration

**Date:** 2026-05-21
**Scope:** `/www/sub2api` scheduler integration with Kiro-Go Phase 4 readiness.

## Findings

Phase 4 made Kiro-Go expose a versioned readiness contract at `/admin/api/fleet/readiness?model=claude-opus-4-7`. Phase 5 should consume that contract inside sub2api before dispatch.

The sub2api scheduler already has the primitives needed:

- `Account.IsSchedulable()` excludes disabled, expired, overloaded, rate-limited, and temp-unschedulable accounts.
- Generic scheduling in `gateway_service.go` filters candidates before acquiring concurrency slots.
- OpenAI advanced scheduling in `openai_account_scheduler.go` has explicit previous-response, session, and load-balance layers.
- Fallback wait plans are created before acquiring a slot, so readiness can gate those candidates too.
- Requested/upstream model fields already exist for usage logs and channel mapping.

## Recommended Architecture

Add a small `KiroReadinessProvider` service in `backend/internal/service`:

- Config-backed and disabled by default.
- Uses `net/http` with timeout.
- Queries `GET {endpoint}?model={effectiveModel}` or appends the path to a configured base URL.
- Parses only safe readiness fields: status, safeConcurrency, retryAfterSeconds, reasons, cache TTL hints if present.
- Caches by endpoint/account/effective model with status-specific TTLs.
- Returns a scheduler decision object that includes status, cache hit, TTL, retry-after, safe concurrency, provider ID, and reason.

Integrate at scheduler candidate gates:

- OpenAI previous-response sticky: evaluate the selected sticky account before returning it.
- OpenAI session sticky: evaluate before returning or creating wait plans.
- OpenAI load-balance: evaluate top candidates before sorting/acquiring; blocked candidates are skipped, degraded candidates get a lower score/capped concurrency.
- Generic `selectAccountWithLoadAwareness`: evaluate route/sticky, load-balance, and fallback candidates before slot acquisition.

## Risk Notes

- The stale `05-SPEC.md` says sub2api is out of scope. Do not follow it for implementation.
- Do not persist provider endpoint secrets or raw auth headers in usage logs.
- Avoid adding broad database migrations unless needed; scheduler logs and in-memory decisions are enough for Phase 5 acceptance.
- Existing tests often instantiate services with partial structs. New code must be nil-safe and disabled by default.

## Verification Targets

- `go test ./backend/internal/service -run 'Test.*Kiro.*Readiness|TestOpenAI.*Readiness|Test.*Readiness.*Scheduler' -count=1`
- `go test ./backend/internal/service -run 'TestOpenAIGatewayService_SelectAccountWithScheduler|Test.*AccountSelection' -count=1`
- Broader service package test if feasible after focused tests pass.
