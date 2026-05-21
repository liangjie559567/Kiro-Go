# Phase 4: Opus 4.7 Readiness Contract and Scheduler Truth - Specification

**Created:** 2026-05-21
**Ambiguity score:** 0.08 (gate: <= 0.20)
**Requirements:** 7 locked

## Goal

Kiro-Go changes the existing Opus 4.7 fleet readiness and scheduler surfaces from best-effort diagnostics into a versioned, falsifiable contract that reports true `healthy`, `degraded`, or `blocked` capacity and drives routing priority from real content-success evidence.

## Background

Kiro-Go already has account-aware routing, model admission pressure, per-account cooldowns, model breaker state, request logs, stable downstream fallback, scheduler preview, and `/admin/api/fleet/readiness?model=claude-opus-4-7`. The current implementation exposes useful Opus 4.7 signals such as `status`, `safeConcurrency`, schedulable counts, admission pressure, request-log content success rate, and stable fallback counts.

The remaining gap is that this surface is not yet locked as the contract sub2api and admins can trust. The existing readiness endpoint is not explicitly versioned, `safeConcurrency` must be made exact, scheduler preview and fleet readiness must explain the same per-account eligibility state, recent real `account + claude-opus-4.7` content success must become account-level queryable evidence, and that evidence must affect Opus 4.7 routing priority. Fallback HTTP 200 responses must remain visible as fallback transport behavior, but must never count as healthy Opus 4.7 content success.

## Requirements

1. **Versioned readiness contract**: The existing `/admin/api/fleet/readiness?model=claude-opus-4-7` endpoint returns a versioned Opus 4.7 readiness contract.
   - Current: `/admin/api/fleet/readiness` exists and returns status, counts, admission pressure, notes, accounts, and recent content stats, but the response is not explicitly locked as a versioned contract.
   - Target: The existing endpoint, not a new dedicated readiness route, returns a stable version field plus `healthy`, `degraded`, or `blocked`, `safeConcurrency`, schedulable account counts, cooldown counts, `retryAfterSeconds`, `recommendedAction`, and machine-readable reason codes.
   - Acceptance: A GET request to `/admin/api/fleet/readiness?model=claude-opus-4-7` returns HTTP 200 with a contract version, one of the three allowed statuses, numeric capacity/count fields, a recommended action, and reason codes; no new dedicated Opus readiness endpoint is required.

2. **Exact safe concurrency**: `safeConcurrency` is computed as `min(locallySchedulableAccounts, admissionEffectiveConcurrency)` and is `0` whenever readiness is `blocked`.
   - Current: Fleet readiness derives `safeConcurrency` from admission pressure and local schedulability, but the exact contract is not locked and blocked-state zeroing is not explicitly required.
   - Target: The readiness response uses the exact formula for Opus 4.7 and exposes enough fields to verify both inputs.
   - Acceptance: Tests cover healthy, degraded, open-circuit, no-schedulable-account, and admission-pressure cases; in every case `safeConcurrency` equals the strict formula, and all `blocked` responses return `safeConcurrency: 0`.

3. **Scheduler truth alignment**: Scheduler preview and fleet readiness explain the same account/model eligibility state for Opus 4.7.
   - Current: Scheduler preview and fleet readiness share `schedulerPreviewRows`, but fleet readiness also applies aggregate model breaker state and the row-level explanation does not fully expose every scheduler blocker.
   - Target: `/admin/api/scheduler/preview?model=claude-opus-4-7` and `/admin/api/fleet/readiness?model=claude-opus-4-7` agree on per-account eligibility, including account cooldown, model breaker state, token/session state, usage limit state, and Opus 4.7 model-list visibility.
   - Acceptance: A test builds accounts covering cooldown, breaker-open, token-expiring, usage-limited, model-not-listed, disabled, and eligible states; scheduler preview and fleet readiness return matching account IDs, eligibility booleans, and reason codes for those states.

4. **Real content-success evidence**: Kiro-Go maintains queryable account-level recent real content success evidence for `account + claude-opus-4.7` and exposes request-log aggregate evidence in readiness.
   - Current: Request logs can record `ContentSuccess`, `StableDownstreamFallback`, and content failure reasons, and fleet readiness derives aggregate recent content stats from request logs.
   - Target: Successful real Opus 4.7 completions update account-level model evidence such as latest real content-success timestamp and model; readiness exposes both account-level evidence and request-log aggregate evidence. Stable fallback and transport-only HTTP 200 responses do not update account-level content-success evidence.
   - Acceptance: After a real Opus 4.7 response with non-empty content or validated tool use, the selected account row exposes a recent content-success timestamp for `claude-opus-4.7`; after a stable fallback HTTP 200 or empty completion, that timestamp is not advanced and aggregate fallback counters increase instead.

5. **Routing priority uses real success**: Opus 4.7 account selection is biased toward accounts with recent real Opus 4.7 content success without routing to accounts that are otherwise ineligible.
   - Current: Routing priority uses scheduler strategy and runtime health, but does not explicitly prefer recent `account + model` real content success.
   - Target: Among eligible Opus 4.7 accounts, recent real content-success evidence improves routing priority; disabled, cooling-down, breaker-blocked, token-expiring, usage-limited, or model-not-listed accounts remain ineligible even if they have historical success evidence.
   - Acceptance: A routing test with two otherwise eligible accounts selects the account with fresher real Opus 4.7 content success; a second test proves the same account is skipped when any eligibility blocker is active.

6. **Bounded Opus 4.7 retry budgets**: Opus 4.7 requests have bounded account attempts, total wait time, and first-token retry behavior, and budget exhaustion returns retryable pressure metadata.
   - Current: Opus 4.7 request paths use attempt and request budgets and log attempts, but the readiness-phase contract does not explicitly define the exhausted-pool behavior.
   - Target: Claude Messages, OpenAI Chat Completions, and OpenAI Responses Opus 4.7 paths stop after bounded account tries, total wait time, and pre-first-token retry opportunities; exhaustion returns retryable pressure metadata such as retry-after, circuit/readiness reason, and budget-exhausted reason instead of scanning accounts indefinitely.
   - Acceptance: Tests force all Opus 4.7 accounts through retryable pressure and verify attempts never exceed the configured attempt budget, elapsed wait does not exceed the request budget, the response is retryable pressure rather than fake success, and request logs include attempt trace plus exhaustion reason.

7. **Streaming retry safety**: Streaming retries are allowed only before any downstream SSE content has been emitted.
   - Current: The Claude streaming path tracks `streamStarted` and avoids retrying after content starts, but the behavior is not locked as a phase requirement across Opus 4.7 streaming surfaces.
   - Target: Claude stream, OpenAI Chat stream, and OpenAI Responses stream paths may retry only before downstream content starts; after downstream SSE content begins, Kiro-Go records the failure and terminates with protocol-safe stream error behavior rather than transparent replay.
   - Acceptance: Tests inject an upstream failure before first content and verify retry can occur; tests inject an upstream failure after downstream SSE content begins and verify no second account attempt occurs, a stream-safe error is emitted, and request logs record the failure.

## Boundaries

**In scope:**
- Extend the existing `/admin/api/fleet/readiness?model=claude-opus-4-7` response as the versioned readiness contract.
- Keep `/admin/api/scheduler/preview?model=claude-opus-4-7` aligned with fleet readiness account eligibility explanations.
- Add or expose account-level recent real content-success evidence for `account + claude-opus-4.7`.
- Use recent real content-success evidence in Opus 4.7 routing priority for otherwise eligible accounts.
- Ensure stable fallback and transport-only HTTP 200 responses do not count as healthy Opus 4.7 content success.
- Enforce and expose bounded Opus 4.7 attempts, total wait, first-token retry behavior, and retryable pressure metadata.
- Add focused unit and integration tests for readiness contract fields, safe concurrency, eligibility alignment, routing priority, fallback separation, retry budgets, and streaming retry safety.

**Out of scope:**
- Adding a new dedicated Opus readiness endpoint - Phase 4 extends the existing fleet readiness route.
- sub2api readiness provider configuration or dispatch behavior - that is Phase 5.
- Structural upstream error parser expansion and safe Kiro CLI diagnostics - those are Phase 6.
- Final latest-code 100/100 stream and non-stream sub2api UAT evidence - that is Phase 7.
- Reading CLI token stores, browser sessions, keychains, or runtime secrets - diagnostics and secret handling are outside this phase.
- Multi-replica distributed readiness state - current milestone targets the existing single-process gateway.
- Treating stable fallback content as real Opus 4.7 success - this is explicitly forbidden by the milestone requirements.

## Constraints

- The readiness contract must remain on the existing `/admin/api/fleet/readiness?model=claude-opus-4-7` route.
- `safeConcurrency` must equal `min(locallySchedulableAccounts, admissionEffectiveConcurrency)` and must be `0` when status is `blocked`.
- Readiness statuses are limited to `healthy`, `degraded`, and `blocked`.
- Real content success requires actual Opus 4.7 content or validated tool-use output from an upstream account; stable fallback and transport-only HTTP 200 do not qualify.
- Account-level success evidence affects only otherwise eligible accounts; it cannot override cooldowns, breaker state, token/session invalidity, usage limits, disabled state, or model-list invisibility.
- Streaming retries may not replay after downstream SSE content has begun.
- Planning and diagnostics must not expose account secrets, API keys, token stores, or raw credential material.

## Acceptance Criteria

- [ ] `/admin/api/fleet/readiness?model=claude-opus-4-7` returns a versioned contract with status, safe concurrency, schedulable/cooldown counts, retry-after, recommended action, and reason codes.
- [ ] `safeConcurrency` equals `min(locallySchedulableAccounts, admissionEffectiveConcurrency)` in healthy and degraded cases and equals `0` for every blocked case.
- [ ] Scheduler preview and fleet readiness return matching per-account eligibility and reason codes for cooldown, breaker, token/session, usage-limit, disabled, and model-list states.
- [ ] Real Opus 4.7 content success updates queryable account-level `account + model` evidence and readiness aggregate evidence.
- [ ] Stable fallback HTTP 200 responses and transport-only successes do not advance real content-success evidence and do not count as healthy model success.
- [ ] Opus 4.7 routing priority prefers eligible accounts with fresher real content success while still skipping ineligible accounts.
- [ ] Opus 4.7 account attempts, total wait, and first-token retry behavior are bounded and budget exhaustion returns retryable pressure metadata.
- [ ] Streaming retries happen before downstream SSE content only; failures after SSE content begins emit protocol-safe stream errors without transparent replay.

## Ambiguity Report

| Dimension           | Score | Min   | Status | Notes |
|---------------------|-------|-------|--------|-------|
| Goal Clarity        | 0.93  | 0.75  | met    | Phase maps directly to RDY-01 through RDY-06 and locks routing impact for RDY-03. |
| Boundary Clarity    | 0.82  | 0.70  | met    | Existing fleet readiness route is the contract; Phase 5, 6, and 7 work is excluded. |
| Constraint Clarity  | 0.82  | 0.65  | met    | Safe concurrency formula, fallback exclusion, and streaming retry rule are explicit. |
| Acceptance Criteria | 0.80  | 0.70  | met    | Requirements have pass/fail API, routing, and retry checks. |
| **Ambiguity**       | 0.08  | <=0.20| met    | Gate passed after round 2. |

Status: met = meets minimum, below = planner treats as assumption.

## Interview Log

| Round | Perspective | Question summary | Decision locked |
|-------|-------------|------------------|-----------------|
| 1 | Researcher | Should the versioned Opus 4.7 readiness contract be a new API or extend an existing API? | Extend existing `/admin/api/fleet/readiness?model=claude-opus-4-7`; do not require a new dedicated readiness route. |
| 1 | Researcher | What is the required `safeConcurrency` formula? | `safeConcurrency` must equal `min(locallySchedulableAccounts, admissionEffectiveConcurrency)` and must be `0` when readiness is `blocked`. |
| 2 | Researcher + Simplifier | Where should real `account + claude-opus-4.7` content success evidence come from? | Use both account-level recent success evidence and request-log aggregate evidence. |
| 2 | Researcher + Simplifier | Must routing priority change in Phase 4 or only expose evidence? | Phase 4 must make routing priority use real content-success evidence for otherwise eligible Opus 4.7 accounts. |

---

*Phase: 04-opus-4-7-readiness-contract-and-scheduler-truth*
*Spec created: 2026-05-21*
*Next step: $gsd-discuss-phase 4 - implementation decisions for how to build the locked requirements above*
