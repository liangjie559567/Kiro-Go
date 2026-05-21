# Phase 5: sub2api Readiness Provider Integration - Context

**Gathered:** 2026-05-21
**Status:** Ready for planning
**Mode:** Autonomous, --auto

<domain>
## Phase Boundary

sub2api must consume the Kiro-Go Opus 4.7 readiness contract before dispatch. The goal is not another Kiro-Go readiness surface; Phase 4 already locked that contract. Phase 5 changes the downstream scheduler so OpenAI-compatible Kiro-Go accounts are skipped, capped, or deprioritized using readiness for the effective upstream model.

</domain>

<decisions>
## Implementation Decisions

### ROADMAP and REQUIREMENTS override stale 05-SPEC

The existing `05-SPEC.md` describes a Kiro-Go-only black-box contract and explicitly excludes sub2api. That conflicts with `.planning/ROADMAP.md` Phase 5 and `.planning/REQUIREMENTS.md` S2A-01 through S2A-06. For this phase, treat `ROADMAP.md` and `REQUIREMENTS.md` as the newer authority. The stale spec is retained as historical context only.

### Provider shape

Add a sub2api gateway scheduling configuration for a Kiro-Go readiness provider: endpoint, timeout, status-specific TTLs, fail-open/fail-closed behavior, and model match rules. The provider must query Kiro-Go's `GET /admin/api/fleet/readiness?model=...` contract without sending account secrets.

### Scheduling scope

Readiness gating must run before account concurrency is consumed. It must cover previous-response sticky, session sticky, load-balanced selection, and fallback-wait paths. A `blocked` result skips the candidate without creating a permanent account error. A `degraded` result remains eligible only within Kiro-Go's `safeConcurrency` and should lose priority against healthy candidates.

### Effective model

Readiness must use the upstream model actually sent to Kiro-Go after channel/account/model mapping, not only the client-requested model.

### Observability

The integration should log readiness status, cache hit/miss, TTL, retry-after, safeConcurrency, requested/effective model, and account/channel identifiers. Logs must avoid credentials, endpoint query secrets, and raw auth material.

</decisions>

<code_context>
## Existing Code Insights

sub2api is a separate repository at `/www/sub2api`. The existing untracked `/www/sub2api/deploy/docker-compose.current.yml` is unrelated and must not be committed.

Key integration points:

- `backend/internal/config/config.go`: `GatewaySchedulingConfig` already holds scheduler options and default/validation logic.
- `backend/internal/service/gateway_service.go`: generic account selection, sticky session, load-balance, fallback-wait, and temp-unschedulable paths.
- `backend/internal/service/openai_account_scheduler.go`: OpenAI advanced scheduler with previous-response sticky, session sticky, and load-balance layers.
- `backend/internal/handler/gateway_handler_responses.go`, `gateway_handler_chat_completions.go`, `openai_gateway_handler.go`: effective model mapping before dispatch.
- `backend/internal/service/account.go`: account base URLs and model mapping helpers.
- `backend/ent/schema/account.go`: `temp_unschedulable_until` and `temp_unschedulable_reason` already exist.
- `backend/internal/repository/account_repo.go`: `SetTempUnschedulable` and cache/outbox sync already exist for account-level temporary scheduling state.
- `backend/internal/repository/usage_log_repo.go` and ops logging: existing requested/upstream model fields can be paired with structured scheduler logs without adding secret-bearing DB columns.

</code_context>

<specifics>
## Specific Ideas

- Identify Kiro-Go accounts by OpenAI-compatible account base URL and/or explicit account extra metadata.
- Implement an in-process readiness cache keyed by provider endpoint/account/effective model.
- Respect healthy/degraded/blocked TTLs and fail-open/fail-closed mode.
- For `blocked`, exclude the account during selection and optionally mark a short temp-unschedulable state only when configured and bounded by retry-after/TTL.
- For `degraded`, cap usable concurrency by `safeConcurrency` and rank behind healthy candidates.
- Add tests for blocked skip, degraded cap/ranking, fail mode, cache TTL, previous-response sticky, session sticky, load-balance, and fallback-wait behavior.

</specifics>

<deferred>
## Deferred Ideas

Persistent readiness history, admin UI for provider status, distributed cross-replica coordination, and final 100/100 latest-code UAT are deferred to later phases unless needed for the Phase 5 tests.

</deferred>
