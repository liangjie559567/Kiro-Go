# Opus 4.7 Health Governor Design

Date: 2026-05-20
Status: Draft for user review
Scope: Kiro-Go downstream reliability for sub2api calls to `claude-opus-4.7`

## Context

The latest live monitor showed that Kiro-Go and sub2api stayed process-healthy while Opus 4.7 traffic still produced client-visible retryable failures. The important failure shape was not a dead service. It was a pressure-control failure:

- Kiro-Go `/health` and sub2api `/api/status` stayed HTTP 200 during the monitor.
- Request logs recorded 67 successful calls and 2 client-visible errors.
- One Opus 4.7 `/v1/messages` request took about 73 seconds, tried 17 account attempts, and ended in HTTP 429.
- The attempt trace mixed `model_capacity` and `temporary_limited`, meaning upstream model pressure and account-specific limits were being amplified by the retry loop.
- Final readiness still had locally schedulable accounts, but many accounts were cooling down from temporary limits.

External references support the same shape:

- `jwadow/kiro-gateway` uses lazy account initialization, sticky selection, circuit-breaker-style failure tracking, and fatal/recoverable classification. It is useful as a failover pattern, but it treats some upstream/server failures too simply for Opus 4.7 capacity pressure.
- `zeoak9297/KiroSwitchManager` has no source code, but its product behavior reinforces the operator needs: account quota visibility, automatic account switching, token refresh, model locking, and fleet UI.
- Anthropic API semantics distinguish retryable rate limits and temporary overloads. The gateway must respect `Retry-After`-style backoff and must not self-amplify overload by blindly trying many upstream identities.

## Goal

Make sub2api calls through Kiro-Go to Opus 4.7 sustainable under real upstream pressure by introducing an Opus 4.7 Health Governor.

"100% healthy" in this design means Kiro-Go provides a bounded, observable, retryable, and non-amplifying control plane. It does not mean upstream Opus 4.7 can never return capacity or rate-limit errors. The system passes only when real Docker, browser, API, log, and database/usage evidence show that failures are handled inside the designed contract.

## Non-Goals

- Do not fabricate success when upstream Opus 4.7 is genuinely unavailable.
- Do not edit or discard existing account data.
- Do not rely on reading or printing secrets from `data/config.json`, recovery snapshots, tokens, passwords, or account emails.
- Do not make sub2api blindly retry more aggressively to hide Kiro-Go pressure.
- Do not mark UAT PASS from screenshots alone. Screenshots must match API, log, and database/usage evidence.

## Recommended Approach

Implement a model-specific Health Governor for `claude-opus-4.7` with five cooperating controls:

1. Model-level circuit breaker.
2. Request-level retry and wait budgets.
3. Account health scoring with separate account and model pressure reasons.
4. Background quiet mode for refresh and health-check tasks.
5. Explicit downstream readiness contract for sub2api.

This is stronger than tuning the existing admission gate alone because it changes the failure behavior from "try the whole pool and hope" to "make a small bounded attempt, open the model circuit when pressure is global, and tell sub2api exactly when to wait."

## Architecture

### Opus Model Circuit

Add a model-level circuit state for `claude-opus-4.7`:

- `closed`: normal routing.
- `degraded`: allow limited calls with reduced safe concurrency.
- `open`: do not send new generation traffic upstream for this model until `retryAfter`.
- `half_open`: allow a small probe count after `retryAfter` to verify recovery.

The circuit opens on bursts of model-level pressure, especially repeated `model_capacity`, upstream overload, or retryable 429/529-like errors across multiple accounts in a short window.

The circuit must not open solely because one account is `temporary_limited`; that remains account-specific unless multiple independent accounts produce the same model-capacity signal.

### Request Budget

Each Opus 4.7 request receives an immutable budget:

- Maximum attempted accounts: default 3 to 5.
- Maximum total upstream wait: default 15 to 30 seconds for interactive sub2api/Claude Code calls.
- Maximum gate wait: bounded by the same deadline, never independent from the request deadline.
- Retry-After respect: if the best retry-after exceeds the remaining request budget, stop early and return a retryable response.

The 73-second, 17-attempt pattern becomes invalid by design. A request should either succeed within budget or return a clear retryable 429 with `Retry-After` and Kiro-Go pressure headers.

### Account Health Scoring

Keep failure reasons separate:

- `temporary_limited`: account-specific cooldown, longer as failures repeat.
- `model_capacity`: model-level pressure, short model cooldown or circuit pressure. It should not poison many individual accounts.
- `rate_limited`: account or upstream rate limit depending on body and headers.
- `quota_exhausted`: account-specific until operator refreshes quota/account pool.
- `auth_expired`: account-specific and not retryable until refreshed.
- `upstream_5xx` or overload: model/global pressure unless evidence shows account-specific failure.

Routing should prefer accounts with:

- model listed,
- healthy auth,
- no active cooldown,
- low active connections,
- recent success for the same model,
- no recent temporary-limit failures.

### Background Quiet Mode

Auto-refresh and health-check tasks should become pressure-aware:

- When Opus 4.7 circuit is degraded/open, skip generation-like probes for cooling accounts.
- Add jitter to scheduled refresh/health runs.
- Cap concurrent background account checks.
- Avoid scanning all accounts while foreground Opus requests are timing out or producing temporary limits.
- Preserve token refresh work needed for account survival, but separate it from model-generating probes.

This prevents background operations from increasing upstream pressure while user traffic is already struggling.

### Downstream sub2api Contract

Expose a read-only readiness contract that sub2api can consume before routing Opus 4.7 traffic:

Endpoint:

```text
GET /admin/api/fleet/readiness?model=claude-opus-4-7
```

Response fields:

- `model`
- `status`: `healthy`, `degraded`, `blocked`
- `circuitState`: `closed`, `degraded`, `open`, `half_open`
- `retryAfterSeconds`
- `safeConcurrency`
- `currentInFlight`
- `enabledAccounts`
- `modelListedAccounts`
- `locallySchedulableAccounts`
- `coolingDownAccounts`
- `temporaryLimitedAccounts`
- `quotaBlockedAccounts`
- `authBlockedAccounts`
- `admissionPressureScore`
- `lastPressureReason`
- `lastPressureAt`
- `notes`

sub2api expected behavior:

- Route normally only when `status=healthy`.
- Queue or slow-route only when `status=degraded` and `safeConcurrency > currentInFlight`.
- Do not send new Opus 4.7 calls when `status=blocked`; honor `retryAfterSeconds`.
- Treat Kiro-Go retryable 429 as a controlled backoff signal, not as a reason to immediately fan out retries.

## Error Contract

For client-visible Opus 4.7 pressure responses:

- Use HTTP 429 for retryable model/account pressure when the service is otherwise healthy.
- Use `Retry-After` whenever there is a known retry time.
- Include Kiro-Go headers such as:
  - `X-Kiro-Go-Error-Reason`
  - `X-Kiro-Go-Circuit-State`
  - `X-Kiro-Go-Retryable`
  - `X-Kiro-Go-Safe-Concurrency`
- Reserve HTTP 503 for local service unavailability, queue admission timeout caused by Kiro-Go itself, or no viable backend state where retry timing is unknown.

The response body must be compatible with Claude/OpenAI-style clients already supported by Kiro-Go.

## Observability

Request logs must support root-cause analysis without secrets:

- Full `attemptTrace[]` for Opus 4.7:
  - attempt number,
  - account id,
  - selected/failure/success event,
  - reason,
  - status code,
  - retry-after,
  - duration,
  - circuit state at selection.
- Admission fields:
  - effective concurrency,
  - wait duration,
  - pressure score,
  - model circuit state.
- Background-task fields:
  - skipped due to quiet mode,
  - accounts checked,
  - accounts skipped due to cooldown,
  - jittered next run.

Admin UI should show a compact Opus 4.7 health panel: circuit state, safe concurrency, schedulable accounts, retry-after, recent pressure, and last failed attempt trace summary.

## Data Safety

The implementation must preserve all existing account and usage data:

- No schema migration may drop account fields.
- Any new persistent state must be additive.
- Config writes must remain atomic.
- UAT and logs must redact account emails, tokens, refresh tokens, passwords, cookies, and raw config contents.
- Recovery and backup directories remain untouched unless a future implementation plan explicitly defines a safe backup step.

## UAT Acceptance Criteria

A PASS requires real evidence from the latest code running in Docker with sub2api:

- Kiro-Go health HTTP 200 throughout the run.
- sub2api health HTTP 200 throughout the run.
- Playwright browser screenshots for Kiro-Go admin health/readiness/log pages and sub2api account/usage pages.
- API evidence from health, model readiness, fleet readiness, request logs, and sub2api usage/admin APIs.
- Database/usage evidence from sub2api showing requests were accounted for and not lost.
- Screenshot analysis confirms the visible UI matches the API/database evidence.

Reliability gates:

- 30-minute monitor has no client-visible HTTP 503 for Opus 4.7 under controlled load.
- Client-visible retryable 429 is either zero or within the explicit degraded-mode threshold.
- No request attempts more accounts than the configured request budget.
- No request exceeds the configured Opus 4.7 request budget.
- No burst of model-capacity errors produces mass account temporary-limit pollution.
- Background refresh/health logs show quiet-mode skips during Opus pressure.
- sub2api honors `blocked/degraded` readiness instead of blindly sending new Opus calls.

If upstream Opus 4.7 is unavailable for the whole window, the correct result is `BLOCKED_BY_UPSTREAM`, not PASS.

## Implementation Units

1. Model circuit state and pressure scoring.
2. Opus request budget enforcement.
3. Account/model failure reason separation and routing score adjustments.
4. Background quiet mode and jitter.
5. Fleet readiness API and Admin UI health panel.
6. sub2api-facing contract verification scripts.
7. Docker + Playwright + API + database UAT evidence pack.

## Alternatives Considered

### Conservative Tuning

Tune existing admission values, cooldowns, and retry budget only. This is low-risk but likely insufficient because sub2api still lacks an explicit health contract and background tasks can still add pressure.

### Dedicated Opus Service Lane

Create a dedicated Opus 4.7 virtual queue with token-budget scheduling and deeper sub2api feedback. This is powerful but larger than needed for the immediate reliability target.

### Recommended Governor

The Health Governor keeps the implementation inside Kiro-Go's current architecture while adding the missing control-plane concepts: model circuit, bounded request attempts, quiet mode, and downstream readiness. It is the best fit for immediate sustainable health.

## Open Decisions For Implementation Planning

- Exact default request budget: 3, 4, or 5 accounts.
- Exact interactive timeout: 15, 20, or 30 seconds.
- Whether sub2api will poll fleet readiness before every Opus request or cache it briefly.
- Whether the first implementation only exposes readiness for sub2api or also adds a visible Admin UI panel in the same phase.

The recommended defaults for the first plan are 4 account attempts, 25 seconds total budget, and a 3-second sub2api readiness cache.
