# Phase 7: Observability, Latest-Code UAT, and Acceptance Evidence - Specification

**Created:** 2026-05-21
**Ambiguity score:** 0.09 (gate: <= 0.20)
**Requirements:** 8 locked

## Goal

Kiro-Go and sub2api expose enough aligned observability and repeatable latest-code UAT evidence that Opus 4.7 is marked PASS only for real content success under viable readiness, and marked correctly blocked when no viable capacity exists.

## Background

Kiro-Go already has request logs in `proxy/request_log.go`, admin request-log and fleet-readiness APIs, Opus 4.7 readiness fields such as `status`, `safeConcurrency`, `retryAfterSeconds`, `circuitState`, `lastPressureReason`, recent content-success counters, stable-fallback counters, and admin UI cards in `web/index.html`. Historical UAT artifacts under `docs/superpowers/uat/` prove prior 100/100 stream and non-stream sub2api to Kiro-Go Opus 4.7 success, plus screenshots, request logs, sub2api usage/database evidence, and a 2026-05-21 blocked-capacity monitor window.

The remaining gap is that Phase 7 must turn these surfaces into a final acceptance contract. The phase must add or tighten any missing Kiro-Go observability fields, require sub2api evidence that readiness decisions are distinguishable from upstream errors and temporary unschedulable state, and produce repeatable latest-code UAT evidence. External research reinforces the same contract: Kiro Opus 4.7 availability is account/model/region dependent, proxy ecosystems rely on multi-account readiness and health checks, OpenTelemetry-style GenAI/HTTP evidence separates model, provider, error, retry, and status dimensions, and SRE practice requires latency, traffic, errors, and saturation to agree before declaring service health.

## Requirements

1. **Admission observability**: Every Opus 4.7 generation request records readiness and routing evidence at admission.
   - Current: Request logs already record model, selected account, circuit state, retry-after, attempt trace, fallback flags, content-success flags, routing decisions, and latency fields, but Phase 7 requires a locked evidence set for final PASS.
   - Target: Kiro-Go request logs for Opus 4.7 expose readiness state at admission, safe concurrency, retry-after, selected account, effective model, attempt trace, pressure reason, stable fallback flag, content-success evidence, latency, status, and error class.
   - Acceptance: A verifier can fetch `/admin/api/request-logs?limit=100` after UAT and find each Opus 4.7 row contains the locked evidence fields or an explicit empty/zero value when not applicable.

2. **Safe diagnostic response evidence**: Kiro-Go exposes safe API fields or diagnostic headers for readiness, retryability, circuit state, safe concurrency, retry-after, and content success.
   - Current: Fleet readiness exposes many API fields and fallback/error paths set selected headers, but the final safe evidence contract and propagation boundaries are not locked.
   - Target: Kiro-Go admin APIs expose the full safe evidence set, and any downstream-propagated headers are explicitly safe, redacted, and configurable by sub2api or deployment policy.
   - Acceptance: Tests or UAT captures show readiness/circuit/safe concurrency/retry-after/content-success evidence in Kiro-Go API output or safe response headers; no credential, token, raw account secret, CLI token-store path, or private upstream header is exposed.

3. **sub2api scheduling evidence**: sub2api evidence distinguishes readiness-blocked scheduling from upstream 429/529, account limits, overloads, and temporary unschedulable rules.
   - Current: Historical UAT captures sub2api usage logs, account state, and ops-error snippets, but Phase 7 needs a required evidence schema for final PASS.
   - Target: UAT evidence includes sub2api usage or ops logs, account/channel identifiers, requested model, effective upstream model, readiness status/cache state when available, dispatch or skip decision, upstream status when dispatched, and temporary unschedulable state.
   - Acceptance: The UAT evidence bundle proves at least one healthy/degraded dispatch path and one blocked-capacity skip or retryable-exhaustion path can be distinguished without treating model-level pressure as a permanent account error.

4. **Admin evidence surface**: Admin UI or admin API evidence shows Opus 4.7 pool state and latest real content success without exposing secrets.
   - Current: The admin API tab renders Claude Code readiness, model readiness, fleet readiness, and request logs; historical screenshots cover dashboard, accounts, readiness, and request logs.
   - Target: Final UAT captures admin API/UI evidence for Opus 4.7 `status`, `safeConcurrency`, schedulable accounts, retry-after, circuit state, degraded/blocked reason, CLI model-list status when Phase 6 provides it, latest real content-success timestamp or count, and recent fallback/empty-completion counters.
   - Acceptance: Playwright screenshots and API JSON agree on the Opus 4.7 status and capacity fields; screenshot text checks pass and browser console errors are zero.

5. **Latest-code non-stream UAT gate**: Latest-code sub2api non-stream Opus 4.7 UAT proves 100/100 real content successes only when readiness reports viable capacity.
   - Current: Historical 2026-05-20 evidence shows 100/100 non-stream success, but v1.1 final PASS requires a rerun against latest code and the new observability contract.
   - Target: A repeatable UAT script sends 100 non-stream requests through `/www/sub2api -> Kiro-Go -> Kiro Opus 4.7` only when Kiro-Go readiness is `healthy` or `degraded` with `safeConcurrency > 0`; every success must contain real Opus 4.7 content evidence and not stable fallback.
   - Acceptance: Non-stream PASS requires 100/100 HTTP-level completions, 100/100 real content-success validations, zero stable fallback successes, readiness viable before or during the run, and aligned Kiro-Go/sub2api evidence.

6. **Latest-code stream UAT gate**: Latest-code sub2api stream Opus 4.7 UAT proves 100/100 real content successes only when readiness reports viable capacity and started streams are not replayed.
   - Current: Historical 2026-05-20 evidence shows 100/100 stream success, and Phase 4 locks retry safety, but final PASS must verify latest code with evidence.
   - Target: A repeatable UAT script sends 100 streaming requests through `/www/sub2api -> Kiro-Go -> Kiro Opus 4.7` only when readiness is viable; every stream must include valid SSE content and stop events, real content-success evidence, no stable fallback success, and no transparent replay after downstream content begins.
   - Acceptance: Stream PASS requires 100/100 valid streams, 100/100 real content-success validations, zero replay-after-content violations, zero stable fallback successes, and aligned Kiro-Go/sub2api evidence.

7. **Blocked-capacity UAT gate**: Blocked-capacity UAT proves explicit blocked or retryable exhausted-pool behavior without stimulating real upstream accounts unnecessarily.
   - Current: A 2026-05-21 monitor window captured real `blocked` readiness with `safeConcurrency=0`, but final acceptance needs a repeatable and safe gate.
   - Target: UAT uses a controlled fixture, test configuration, or naturally observed blocked window to prove no viable Opus 4.7 account results in `blocked` readiness or retryable exhausted-pool behavior with `Retry-After`; fake success, stable fallback success, and permanent sub2api account poisoning are forbidden.
   - Acceptance: Blocked-capacity PASS requires Kiro-Go readiness `blocked` or `safeConcurrency=0`, response/API evidence with retryability and retry-after, sub2api skip/exhaustion evidence, no real-content PASS claim, and no permanent account disablement caused by model-capacity pressure.

8. **Evidence bundle and PASS manifest**: Final UAT writes a complete, machine-checkable evidence bundle before PASS.
   - Current: Historical UAT directories contain JSON, logs, screenshots, scripts, and `UAT-RESULT.md`, but the required final bundle shape is not locked.
   - Target: Each Phase 7 UAT run writes a timestamped directory under `docs/superpowers/uat/` containing raw request results, Kiro-Go request logs/API snapshots, fleet readiness snapshots, sub2api usage or ops logs, database or scheduling-state evidence, response headers, admin screenshots, browser console summary, Playwright trace or equivalent debug artifact, and a `UAT-RESULT.md` verdict.
   - Acceptance: `UAT-RESULT.md` marks PASS only when all required evidence exists and agrees; if viable readiness is absent, the verdict is blocked-capacity PASS or generation PASS blocked by upstream capacity, not full Opus 4.7 generation PASS.

## Boundaries

**In scope:**
- Kiro-Go request-log, admin API, diagnostic-header, and admin UI evidence needed to verify Opus 4.7 readiness, routing, retries, fallback separation, and content success.
- sub2api evidence collection for readiness-blocked scheduling, dispatch decisions, upstream errors, usage logs, ops logs, and temporary unschedulable/database state.
- Repeatable latest-code 100/100 non-stream UAT through the real sub2api to Kiro-Go to Kiro Opus 4.7 path when readiness is viable.
- Repeatable latest-code 100/100 stream UAT through the real sub2api to Kiro-Go to Kiro Opus 4.7 path when readiness is viable.
- Safe blocked-capacity UAT using controlled fixtures, test configuration, or naturally observed blocked windows.
- Timestamped evidence directories under `docs/superpowers/uat/` with raw JSON, logs, headers, database/scheduling state, screenshots, browser console summary, trace/debug artifacts, and `UAT-RESULT.md`.
- Unit, integration, or UAT checks that prevent stable fallback, empty completion, or transport-only HTTP 200 from being counted as Opus 4.7 content success.

**Out of scope:**
- Building the core Phase 4 readiness contract, routing priority, retry-budget, or stream-retry mechanics - those are Phase 4 responsibilities.
- Building the Phase 5 sub2api readiness provider itself - Phase 7 verifies and captures evidence from it after it exists.
- Building Phase 6 upstream error parser expansion or CLI diagnostics - Phase 7 displays and verifies the safe outputs when available.
- Forcing real Kiro upstream into blocked state by aggressive probes or high-concurrency stimulation - blocked UAT must be safe and controlled.
- Guaranteeing Opus 4.7 upstream capacity when Kiro has no viable account available - Phase 7 proves correct success or correct blocked behavior.
- Reading, copying, storing, or exposing API keys, account tokens, CLI token stores, browser sessions, keychains, or private runtime secrets.
- Copying implementation code from AGPL/GPL Kiro proxy projects; external research informs behavior and evidence contracts only.

## Constraints

- Full generation PASS requires real content-success evidence, not only HTTP 200, SSE transport completion, or stable downstream fallback.
- 100/100 stream and non-stream generation UAT may run only when readiness is `healthy` or `degraded` and `safeConcurrency > 0`.
- If readiness is `blocked` or `safeConcurrency=0`, the correct verdict is blocked-capacity PASS or generation PASS blocked by upstream capacity, not full generation PASS.
- Blocked-capacity UAT must prefer controlled fixtures or existing blocked evidence over generating extra expensive upstream traffic.
- Safe propagated headers and admin evidence must redact secrets and must not expose unsafe upstream headers by default.
- Evidence must align across Kiro-Go API/logs, fleet readiness, sub2api logs/usage, scheduling/database state, response headers, and screenshots before PASS.
- Playwright screenshot checks must validate non-empty, relevant pages and zero browser console errors for admin evidence.
- Final UAT artifacts must live under `docs/superpowers/uat/` and must be reproducible enough for a verifier to rerun or audit.

## Acceptance Criteria

- [ ] Kiro-Go Opus 4.7 request logs expose readiness-at-admission, safe concurrency, retry-after, selected account, effective model, attempt trace, pressure reason, fallback state, content-success evidence, latency, status, and error class.
- [ ] Kiro-Go admin APIs or safe diagnostic headers expose readiness, retryability, circuit state, safe concurrency, retry-after, and content-success evidence without leaking secrets.
- [ ] sub2api evidence distinguishes readiness-blocked scheduling from upstream 429/529, account limits, overloads, and temporary unschedulable rules.
- [ ] Admin API/UI evidence shows Opus 4.7 pool state, degraded/blocked reasons, CLI model-list status when available, latest real content-success evidence, and fallback/empty-completion counters.
- [ ] Latest-code non-stream UAT through sub2api returns 100/100 real Opus 4.7 content successes when readiness is viable and records zero stable fallback successes.
- [ ] Latest-code stream UAT through sub2api returns 100/100 real Opus 4.7 content successes when readiness is viable and records zero replay-after-content violations.
- [ ] Blocked-capacity UAT proves `blocked` or retryable exhausted-pool behavior with `Retry-After`, no fake success, and no permanent account poisoning.
- [ ] Final evidence bundle includes raw request results, Kiro-Go logs/API, fleet readiness, sub2api logs/usage, scheduling/database state, response headers, admin screenshots, console summary, trace/debug artifact, and `UAT-RESULT.md`.
- [ ] `UAT-RESULT.md` marks full generation PASS only when all evidence agrees; otherwise it records the precise blocked/degraded verdict.

## Ambiguity Report

| Dimension           | Score | Min   | Status | Notes |
|---------------------|-------|-------|--------|-------|
| Goal Clarity        | 0.92  | 0.75  | met    | Phase goal is final observable acceptance, not core readiness implementation. |
| Boundary Clarity    | 0.86  | 0.70  | met    | Separates Phase 7 evidence/UAT from Phase 4, 5, and 6 implementation responsibilities. |
| Constraint Clarity  | 0.80  | 0.65  | met    | Viable-readiness gate, fallback exclusion, safe blocked UAT, and secret redaction are explicit. |
| Acceptance Criteria | 0.86  | 0.70  | met    | 9 pass/fail criteria cover logs, headers/APIs, sub2api evidence, stream/non-stream, blocked capacity, and bundle shape. |
| **Ambiguity**       | 0.09  | <=0.20| met    | Gate passed after research-backed round 1 confirmation. |

Status: met = meets minimum, below = planner treats as assumption.

## Interview Log

| Round | Perspective | Question summary | Decision locked |
|-------|-------------|------------------|-----------------|
| 1 | Researcher | Should final Phase 7 deliver only reports or also code/scripts/evidence gates? | Final deliverable is code changes where needed, repeatable UAT scripts, machine-checkable evidence, and `UAT-RESULT.md`. |
| 1 | Researcher | Must 100/100 stream and non-stream UAT use the real sub2api to Kiro-Go to Kiro Opus 4.7 path? | Yes, but only when readiness is `healthy` or `degraded` with `safeConcurrency > 0`. |
| 1 | Researcher | How should blocked-capacity UAT be verified safely? | Prefer controlled fixture/test configuration or naturally observed blocked windows; do not stimulate real upstream accounts just to create blocked state. |
| 1 | Researcher | Which external best practices shape the evidence contract? | Use OpenTelemetry-style GenAI/HTTP dimensions, SRE latency/traffic/error/saturation alignment, and Playwright screenshot/trace evidence as audit support. |
| 1 | Researcher | What must never count as final Opus 4.7 success? | Stable fallback, empty completion, transport-only HTTP 200, and unaligned screenshot/API/log/database evidence never count as full generation PASS. |

---

*Phase: 07-observability-latest-code-uat-and-acceptance-evidence*
*Spec created: 2026-05-21*
*Next step: $gsd-discuss-phase 7 - implementation decisions for how to build and run the locked evidence contract above*
