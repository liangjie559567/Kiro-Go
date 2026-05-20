# Phase 2: B - High-Availability Account Pool and sub2api Routing - Specification

**Created:** 2026-05-20
**Ambiguity score:** 0.15 (gate: <= 0.20)
**Requirements:** 7 locked

## Goal

Kiro-Go routes Claude Code traffic by account-local availability so one Kiro account's 429 does not poison the account pool, while `/www/sub2api` remains a black-box downstream validator that passes fixed Opus 4.7 10x10 stream and non-stream UAT through Kiro-Go.

## Background

Kiro-Go already has a multi-account pool, account-local cooldown fields, failure classifications, model-level breaker state, runtime health scoring, model admission gates, request logs, model readiness APIs, auto-refresh jobs, health-check jobs, and prior full-stack UAT evidence. Relevant code exists in `pool/account.go`, `pool/breaker.go`, `proxy/handler.go`, `proxy/opus_gate.go`, `proxy/account_refresh.go`, `proxy/account_health.go`, and `proxy/request_log.go`.

Existing UAT from 2026-05-20 shows `/www/sub2api` can pass 100/100 non-stream and 100/100 stream Opus 4.7 calls through Kiro-Go, while Kiro upstream still emits real 429 pressure. The Phase 2 requirement is to make that reliability contract explicit and durable: classify upstream failures precisely, isolate account-local temporary limits, keep other viable accounts schedulable, throttle background traffic, and prove downstream black-box behavior with aligned API, database, log, readiness, and screenshot evidence.

External reference inputs are used for behavior principles, not copy-paste implementation requirements:
- `jwadow/kiro-gateway`: multi-account failover, fatal-vs-recoverable error classification, sticky account behavior, circuit breaker, exponential backoff, probabilistic recovery, model cache refresh on use, and retry/failover around 403, 429, quota, account, and network failures.
- `zeoak9297/KiroSwitchManager`: operator expectations around multi-account management, automatic account switching, quota/ban detection, token refresh, proxy compatibility, message truncation protection, streaming keepalive, model locking, and CLI/IDE mode awareness.

## Requirements

1. **Failure taxonomy**: Kiro-Go classifies upstream failures into explicit, routing-relevant categories.
   - Current: `pool.FailureReason` already includes `temporary_limited`, `model_capacity`, `rate_limited`, `quota_exhausted`, `auth_expired`, `transient_network`, `upstream_5xx`, and `unknown`; tests cover several known Kiro messages, but the phase contract does not yet lock required routing semantics for each category.
   - Target: Kiro-Go distinguishes `model_capacity`, account `temporary_limited`, generic `rate_limited`, `quota`, `auth`, `network`, and `unknown` in request handling, readiness, request logs, and exhausted-pool responses.
   - Acceptance: Tests prove representative Kiro bodies and statuses map to the intended category, including suspicious-activity temporary limits as `temporary_limited`, `INSUFFICIENT_MODEL_CAPACITY` and high-traffic model pressure as `model_capacity`, monthly/quota failures as `quota`, token failures as `auth`, transport failures as `network`, and unrecognized responses as `unknown`.

2. **Temporary-limit isolation**: Account temporary limits do not create global or risk-group pool lockout without direct evidence.
   - Current: Account-local cooldowns and tests already prevent temporary limits from cooling shared profile/user-prefix risk groups, but this behavior must be locked as a phase requirement because it is the core value of Phase 2.
   - Target: A `temporary_limited` result cools only the account that returned it; accounts with the same profile ARN, user prefix, model, or downstream sub2api gateway identity remain schedulable when individually viable.
   - Acceptance: Unit and integration tests show one and multiple temporary-limited accounts are skipped while other accounts in the same apparent risk group continue to be selected; readiness reports evaluated, locally schedulable, generation-blocked, and risk-group cooling counts without claiming the whole group is unavailable.

3. **Retry and attempt behavior**: Request handling keeps trying other viable accounts after recoverable per-account failures while avoiding same-account retry amplification.
   - Current: Kiro-Go has excluded-account routing, model breaker state, sticky escape behavior, and tests for falling through to the next account after temporary limits and rate limits.
   - Target: For recoverable account-local failures, the current request excludes the failed account and attempts another viable account; the same account is not retried in a tight loop, and single-account exhaustion returns a retryable Anthropic-compatible error with `Retry-After` when applicable.
   - Acceptance: Tests prove account attempts advance across distinct accounts, same-account amplification does not occur inside one request, sticky routing escapes open breakers, and exhausted temporary-limited pools return a downstream-retryable error only when no viable Kiro-Go account remains.

4. **Model-aware admission and scheduler state**: High-pressure models are controlled by account-aware admission and observable scheduler policy state.
   - Current: `proxy/opus_gate.go` provides model admission gates and pressure snapshots, while `pool/account.go` supports health, round-robin, and least-connections strategies.
   - Target: Opus 4.7 and other configured high-pressure models use configurable admission limits, waiting limits, pressure reduction, model-level breaker cooldown, and scheduler policies without hiding account-specific availability.
   - Acceptance: Tests or UAT show configured model admission limits are applied, pressure reduces effective concurrency temporarily, model-capacity failures do not mark an account globally temporary-limited, and readiness/request logs expose admission pressure, effective limit, routing strategy, account health, and model block reason.

5. **Background traffic throttling**: Auto-refresh and health-check jobs do not amplify user traffic limits.
   - Current: Auto-refresh and health-check jobs run as batches with overlap guards, but the phase contract does not yet require bounded concurrency, jitter, or cooldown-aware skipping.
   - Target: Background refresh and health-check operations use bounded work, avoid overlap, respect account cooldown/failure state where applicable, and report skipped/running/next-run status so they cannot repeatedly hammer cooling accounts during user traffic pressure.
   - Acceptance: Tests verify overlapping jobs are skipped, disabled or cooling accounts are not repeatedly probed in a way that extends account limits, job status exposes running/last/next/skipped/result counts, and auth/suspended failures remain the only automatic disable triggers unless a stricter operator setting is explicitly configured.

6. **sub2api black-box semantics**: `/www/sub2api` is not modified by this phase; it is used as the downstream truth test for Kiro-Go's retryability and exhausted-pool semantics.
   - Current: Prior UAT verified `/www/sub2api` can pass through Kiro-Go under load, and recent fixes avoided turning Kiro-Go `TEMPORARY_LIMITED` into downstream gateway-account unschedulability.
   - Target: Kiro-Go emits responses, headers, logs, and account-pool behavior that allow `/www/sub2api` to keep serving when Kiro-Go still has viable accounts, and to surface accurate exhausted-pool failures only when Kiro-Go has no viable accounts.
   - Acceptance: Final evidence shows `/www/sub2api` source was not edited for Phase 2, downstream 429/503/temp-unschedulable state does not appear while Kiro-Go readiness has viable accounts, and any downstream failure is reconciled to Kiro-Go exhausted-pool or external upstream exhaustion with request IDs and logs.

7. **Full-stack HA UAT evidence**: Phase 2 cannot pass on unit tests alone.
   - Current: Existing UAT artifacts already include a passing `/www/sub2api` Opus 4.7 10x10 run with API, database, log, readiness, and screenshot evidence.
   - Target: Final Phase 2 UAT runs against the latest Kiro-Go code and treats `/www/sub2api` as a black-box downstream client, using fixed Opus 4.7 10 concurrent x 10 non-stream and 10 concurrent x 10 stream workloads.
   - Acceptance: The final UAT report records 100/100 correct non-stream responses, 100/100 correct stream responses reconstructed from SSE, max and average latency for each mode, sub2api usage/database reconciliation, Kiro-Go readiness before/after, Kiro-Go and sub2api log filters, Playwright screenshots, and an explicit PASS/FAIL verdict where API, database, logs, readiness, and screenshots agree.

## Boundaries

**In scope:**
- Kiro-Go account-pool failure taxonomy and cooldown semantics.
- Per-account temporary-limit isolation and retry/failover across other viable Kiro accounts.
- Model-aware breaker and admission behavior for Opus 4.7 and other configured high-pressure models.
- Scheduler policy observability needed to explain account selection, exclusion, cooldown, pressure, and exhausted-pool states.
- Auto-refresh and health-check throttling sufficient to avoid background amplification of user traffic limits.
- Black-box `/www/sub2api` verification through API calls, database/usage reconciliation, logs, and screenshots.
- Tests and UAT evidence for HA-01 through HA-07.

**Out of scope:**
- Editing `/www/sub2api` source code - this phase validates Kiro-Go behavior through sub2api as an external black-box downstream.
- Treating one temporary-limited account as global risk-group lockout - the project decision is per-account isolation unless direct evidence proves shared upstream state.
- Hiding real upstream exhaustion from downstream clients - Kiro-Go must return accurate exhausted-pool or retryable errors when no viable account exists.
- Claude Code official protocol parity beyond what Phase 2 UAT needs - Phase 1 owns the broader Anthropic compatibility contract.
- Kiro CLI credential import, account onboarding diagnostics, scheduler policy UI/fleet operations, and WebSearch/MCP operator improvements - Phase 3 owns ecosystem operations.
- Multi-replica distributed account state, admin session hardening, atomic config persistence, and other v2 operational hardening items.

## Constraints

- Keep `/www/sub2api` as a black-box validation target; do not require source edits there for Phase 2 completion.
- Use `jwadow/kiro-gateway` and `KiroSwitchManager` as behavior references for mature Kiro gateway/account operations, especially failover, fatal-vs-recoverable classification, circuit breaking, sticky escape, backoff, automatic switching, quota/ban detection, token refresh, proxy compatibility, truncation protection, and streaming resilience.
- Do not read, expose, or commit runtime secrets from `data/config.json`, recovery snapshots, API keys, refresh tokens, admin passwords, or downstream database credentials.
- Keep implementation sympathetic to the current Go standard-library HTTP service, `pool` and `proxy` package boundaries, JSON config store, and co-located Go tests.
- Real UAT may encounter live Kiro upstream 429s or model pressure; reports must separate correct gateway behavior from external upstream exhaustion.
- PASS requires aligned API, database, log, readiness, and screenshot evidence; contradictory evidence blocks PASS.

## Acceptance Criteria

- [ ] Failure taxonomy tests cover `model_capacity`, `temporary_limited`, `rate_limited`, `quota`, `auth`, `network`, and `unknown` with representative Kiro upstream bodies/statuses.
- [ ] Temporary-limited accounts are cooled account-locally while other viable accounts, including same apparent risk-group accounts, remain schedulable.
- [ ] Recoverable per-account failures exclude the failed account for the current request and try other viable accounts without same-account retry amplification.
- [ ] Exhausted-pool responses are Anthropic-compatible, include retryability/`Retry-After` when applicable, and are returned only when no viable account remains.
- [ ] Model admission and pressure behavior for Opus 4.7 is configurable and visible in readiness/request logs.
- [ ] Background auto-refresh and health-check jobs avoid overlap and do not repeatedly hammer cooling accounts.
- [ ] `go test ./... -count=1` passes after Phase 2 implementation.
- [ ] `/www/sub2api` source is unchanged for Phase 2 except for external runtime/deployment setup outside this repository if needed for UAT.
- [ ] Final `/www/sub2api` non-stream UAT returns 100/100 correct Opus 4.7 responses for 10 concurrent x 10 requests and records max/average latency.
- [ ] Final `/www/sub2api` stream UAT returns 100/100 correct Opus 4.7 responses for 10 concurrent x 10 requests, reconstructed from SSE, and records max/average latency.
- [ ] Final UAT includes sub2api database/usage reconciliation, Kiro-Go readiness before/after, Kiro-Go and sub2api log filters, Playwright screenshots, and a PASS verdict only when all evidence agrees.

## Ambiguity Report

| Dimension           | Score | Min   | Status | Notes |
|---------------------|-------|-------|--------|-------|
| Goal Clarity        | 0.90  | 0.75  | OK     | Goal locks per-account HA and black-box sub2api UAT. |
| Boundary Clarity    | 0.82  | 0.70  | OK     | User explicitly chose not to edit `/www/sub2api`; Phase 1 and Phase 3 boundaries are separated. |
| Constraint Clarity  | 0.78  | 0.65  | OK     | Open-source references, no secret exposure, no false PASS, and evidence requirements are explicit. |
| Acceptance Criteria | 0.86  | 0.70  | OK     | Fixed 10x10 stream/non-stream black-box UAT plus tests and evidence gates. |
| **Ambiguity**       | 0.15  | <=0.20| OK     | Gate passed. |

Status: OK = met minimum, WARN = below minimum (planner treats as assumption)

## Interview Log

| Round | Perspective | Question summary | Decision locked |
|-------|-------------|------------------|-----------------|
| 1 | Researcher | What is the Phase 2 delivery scope relative to `/www/sub2api`? | `/www/sub2api` is a black-box downstream acceptance target; do not modify its source for Phase 2. |
| 1 | Researcher | What should guide 429/failure classification? | Use mature open-source behavior from `jwadow/kiro-gateway` and `KiroSwitchManager`: recoverable account failures, failover, circuit breaking, backoff, quota/ban detection, and automatic switching. |
| 1 | Researcher | Is the 10x10 sub2api UAT gate fixed? | Yes. PASS requires Opus 4.7 10 concurrent x 10 non-stream and 10 concurrent x 10 stream calls with correct content and aligned API/database/log/readiness/screenshot evidence. |

---

*Phase: 02-b-high-availability-account-pool-and-sub2api-routing*
*Spec created: 2026-05-20*
*Next step: $gsd-discuss-phase 2 - implementation decisions (how to build what's specified above)*
