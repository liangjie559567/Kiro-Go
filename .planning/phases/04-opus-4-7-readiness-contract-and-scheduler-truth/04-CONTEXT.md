# Phase 4: Opus 4.7 Readiness Contract and Scheduler Truth - Context

**Gathered:** 2026-05-21
**Status:** Ready for planning
**Mode:** Auto-generated from locked SPEC and targeted code scout (`$gsd-autonomous --auto`)

<domain>
## Phase Boundary

Phase 4 turns the existing `/admin/api/fleet/readiness?model=claude-opus-4-7` and `/admin/api/scheduler/preview?model=claude-opus-4-7` surfaces into a versioned, falsifiable Opus 4.7 readiness contract. The contract must report `healthy`, `degraded`, or `blocked`, exact safe concurrency, retry timing, recommended action, reason codes, and aligned account/model eligibility explanations. It must also make real `account + claude-opus-4.7` content success queryable and use that evidence to improve routing priority for otherwise eligible accounts.

This phase is Kiro-Go only. sub2api readiness-provider dispatch belongs to Phase 5. Upstream error parser expansion and safe Kiro CLI diagnostics belong to Phase 6. Final latest-code stream/non-stream sub2api UAT evidence belongs to Phase 7.

</domain>

<decisions>
## Implementation Decisions

### Locked Contract Shape
- Extend the existing `/admin/api/fleet/readiness?model=claude-opus-4-7` endpoint; do not add a new dedicated Opus readiness route.
- Add an explicit readiness contract version field while preserving existing useful fields for admin and UAT consumers.
- Readiness status values are limited to `healthy`, `degraded`, and `blocked`.
- Include numeric capacity/count fields, `retryAfterSeconds`, `recommendedAction`, and machine-readable reason codes.
- `safeConcurrency` must equal `min(locallySchedulableAccounts, admissionEffectiveConcurrency)` for non-blocked states and must be `0` whenever status is `blocked`.

### Scheduler Truth Alignment
- Scheduler preview and fleet readiness must explain the same per-account eligibility state for Opus 4.7.
- Eligibility explanations must cover disabled accounts, account cooldown, model breaker state, token/session expiry, usage limits, and cached model-list visibility.
- Readiness and preview should expose matching account IDs, eligibility booleans, and reason codes for the same model query.
- Runtime model breaker and cooldown state should be part of the read-only explanation, not hidden behind aggregate counts.

### Real Content Success Evidence
- Real Opus 4.7 content success means actual non-empty content or validated tool-use output from an upstream account.
- Stable downstream fallback, empty completions, and transport-only HTTP 200 responses must never update account-level real content-success evidence.
- Readiness should expose both account-level recent real content-success evidence and request-log aggregate evidence.
- Request log `Outcome` and HTTP status are not sufficient evidence; use `ContentSuccess`, `StableDownstreamFallback`, `ContentFailureReason`, attempt trace, and selected account/model fields.

### Routing Priority
- Among otherwise eligible Opus 4.7 accounts, fresher real Opus 4.7 content-success evidence should improve routing priority.
- Success evidence cannot override disabled state, cooldown, open breaker, token/session invalidity, usage limit, or model-list invisibility.
- Routing changes should be centralized in the pool/account selection path rather than duplicated in admin endpoint code.

### Retry and Streaming Contract
- Claude Messages, OpenAI Chat Completions, and OpenAI Responses Opus 4.7 paths must stay bounded by account attempts, total wait/request budget, and pre-first-token retry opportunities.
- Budget exhaustion must return retryable pressure metadata instead of stable fake success or unbounded account scanning.
- Streaming retries are allowed only before downstream SSE content has begun.
- After downstream SSE content begins, Kiro-Go must record the failure and end with protocol-safe stream error behavior without transparent replay.

### Security and Scope
- Do not read or quote `data/config.json`, recovery snapshots, token stores, browser sessions, keychains, CLI auth databases, or raw credential material.
- Keep changes sympathetic to the current Go standard-library HTTP service and JSON-backed single-process architecture.
- Preserve Claude Code and OpenAI compatibility for existing request/stream behavior.

### the agent's Discretion
Implementation details such as helper names, exact internal struct layout, and whether to introduce small package-local helpers are at the agent's discretion. Prefer minimal, package-local helpers in `proxy/` or `pool/` when they reduce duplication between readiness, scheduler preview, and routing tests.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `proxy/handler.go` routes `GET /admin/api/scheduler/preview` and `GET /admin/api/fleet/readiness` through `handleAdminAPI`.
- `proxy/ecosystem_ops.go` owns `apiGetSchedulerPreview`, `schedulerPreviewRows`, `preferredSchedulerPreviewRows`, `apiGetFleetReadiness`, `contentContinuityReadinessStats`, and readiness note helpers.
- `proxy/request_log.go` already has content and fallback fields: `ContentSuccess`, `ContentFailureReason`, `UpstreamContentTokens`, `StableDownstreamFallback`, `StableFallbackReason`, and `AttemptTrace`.
- `proxy/handler.go` already calls `updateRequestLogContentSuccessIfPresent` from Claude and OpenAI stream/non-stream paths.
- `pool/account.go` owns account selection through `BeginNextForModelExcept`, `BeginNextForModelSessionExcept`, `getNextForModelExceptLocked`, and `isBetterCandidateLocked`.
- `pool/breaker.go` owns per-account/model breaker state and half-open probing behavior.

### Established Patterns
- Admin APIs return JSON maps/structs from `proxy` handlers and are tested with `httptest`.
- Runtime selection state lives in `pool`; persisted account state and account fields live in `config`.
- Request logs are updated through small helper functions using request context.
- Tests use Go standard `testing`, `httptest`, fake transports/function replacements, and package-local helpers. Avoid `t.Parallel` around global config/pool state.

### Integration Points
- Extend `proxy/ecosystem_ops.go` for the fleet readiness and scheduler preview contract.
- Add account-level real content-success evidence storage/exposure in the smallest owner that can be safely updated from request completion paths and read by readiness/routing.
- Adjust `pool/account.go` only where routing priority must use recent real model success evidence for eligible candidates.
- Add focused coverage in `proxy/ecosystem_ops_test.go`, `proxy/handler_test.go`, `proxy/request_log_test.go`, and `pool/account_test.go` as needed.
- Existing retry budget and stream-safety code is concentrated in `proxy/handler.go` around the Claude/OpenAI retry loops and `streamStarted` guards; tests already cover several attempt-budget and streaming paths.

</code_context>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase Contract
- `.planning/phases/04-opus-4-7-readiness-contract-and-scheduler-truth/04-SPEC.md` - locked Phase 4 requirements and acceptance criteria.
- `.planning/REQUIREMENTS.md` - RDY-01 through RDY-06 and milestone constraints.
- `.planning/PROJECT.md` - core value, security constraints, and stable fallback truthfulness rule.

### Codebase Context
- `.planning/codebase/STRUCTURE.md` - source layout and where to add code/tests.
- `.planning/codebase/ARCHITECTURE.md` - runtime architecture and data flow.
- `.planning/codebase/CONVENTIONS.md` - Go style and package conventions.
- `.planning/codebase/TESTING.md` - test commands and patterns.

### Implementation Hotspots
- `proxy/ecosystem_ops.go` - scheduler preview, fleet readiness, readiness content stats.
- `proxy/handler.go` - route dispatch, retry loops, streaming retry guards, content-success call sites.
- `proxy/request_log.go` - request log schema and content/fallback update helpers.
- `pool/account.go` - model-aware account selection and candidate priority.
- `pool/breaker.go` - account/model circuit breaker state.
- `config/config.go` - persisted account fields and config model.

</canonical_refs>

<specifics>
## Specific Ideas

- Prefer one shared eligibility explanation helper for readiness and scheduler preview if it keeps account reason-code parity obvious.
- Include tests that compare scheduler preview rows and fleet readiness rows for the same account fixtures.
- Add tests for healthy, degraded, open circuit, no schedulable account, and admission pressure safe-concurrency cases.
- Add tests proving stable fallback HTTP 200 and empty completions do not advance real content-success evidence.
- Add routing tests with two otherwise eligible Opus 4.7 accounts where the fresher real content-success account wins, and then becomes ineligible when a blocker is applied.
- Add streaming tests for failure before first downstream content versus failure after downstream SSE content starts.

</specifics>

<deferred>
## Deferred Ideas

- sub2api readiness provider configuration, cache TTLs, dispatch behavior, and logs are deferred to Phase 5.
- Broader upstream error parser taxonomy and safe Kiro CLI diagnostics are deferred to Phase 6.
- Final latest-code sub2api stream/non-stream UAT and screenshot/API/log/database evidence are deferred to Phase 7.
- Multi-replica distributed readiness state is out of scope for this milestone.

</deferred>
