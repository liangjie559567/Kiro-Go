# Phase 5: Kiro-Go Readiness Contract for Black-Box Downstream Verification - Specification

**Created:** 2026-05-21
**Ambiguity score:** 0.09 (gate: ≤ 0.20)
**Requirements:** 4 locked

## Goal

Kiro-Go exposes a stable, black-box-verifiable readiness contract for Opus 4.7 that keeps scheduler preview, fleet readiness, and model-readiness aligned on the same account eligibility state.

## Background

Kiro-Go already has scheduler preview, fleet readiness, account diagnostics, model-breaker pressure, and content-success evidence in the admin surface. The remaining gap is not a new scheduler, but a tighter contract: the existing readiness views must stay read-only, internally consistent, and suitable for downstream black-box verification without depending on sub2api internals.

## Requirements

1. **Fleet readiness contract**: `GET /admin/api/fleet/readiness?model=claude-opus-4-7` returns a versioned Opus 4.7 readiness summary with `healthy`, `degraded`, or `blocked`, plus `safeConcurrency`, schedulable account counts, cooldown counts, `retryAfterSeconds`, `recommendedAction`-style notes, and reason codes.
   - Current: The endpoint already returns fleet counts, circuit state, retry-after, safe concurrency, notes, and account rows, but it is documented more as an admin view than a locked contract.
   - Target: The response is treated as the canonical Opus 4.7 fleet readiness contract for black-box verification.
   - Acceptance: A request for `claude-opus-4-7` returns the documented fields, and the status is consistent with the underlying scheduler and model-admission state.

2. **Scheduler preview alignment**: `GET /admin/api/scheduler/preview?model=claude-opus-4-7` and fleet readiness report the same account eligibility state for cooldown, token expiry, quota blocking, model-list visibility, and runtime health.
   - Current: The preview endpoint is already read-only and sorts eligible accounts using local readiness logic, while fleet readiness aggregates the same preview rows.
   - Target: Both endpoints remain read-only and stay logically equivalent on eligibility, with fleet readiness serving as the aggregate view.
   - Acceptance: For a controlled fixture set, preview and fleet readiness disagree on neither eligible account count nor per-account eligibility reasons.

3. **Model-readiness consistency**: `GET /admin/api/claude-code/model-readiness` keeps Opus 4.7 readiness evidence consistent with fleet readiness and scheduler preview, including model-list visibility, breaker state, and content-success evidence.
   - Current: The model-readiness surface already exists and reports layered readiness evidence.
   - Target: The Opus 4.7 readiness story across all three admin surfaces uses one shared underlying interpretation of eligibility and pressure.
   - Acceptance: The same Opus 4.7 fixture state yields compatible readiness conclusions across all three surfaces.

4. **Black-box only scope**: This phase defines Kiro-Go behavior only; sub2api is outside the implementation scope and is treated only as a black-box consumer in later verification.
   - Current: The roadmap names a downstream integration phase, but this phase is now scoped to Kiro-Go-only contract work.
   - Target: No sub2api code, configuration, or logging changes are required or specified here.
   - Acceptance: The phase can be verified entirely from Kiro-Go admin/API responses and logs.

## Boundaries

**In scope:**
- Kiro-Go admin/API readiness contract for Opus 4.7
- `GET /admin/api/fleet/readiness`
- `GET /admin/api/scheduler/preview`
- `GET /admin/api/claude-code/model-readiness`
- Read-only, black-box-verifiable readiness evidence
- Consistency between fleet readiness, scheduler preview, and model-readiness surfaces
- Opus 4.7-specific contract language and evidence

**Out of scope:**
- sub2api implementation changes - this phase treats sub2api as a black-box verifier, not a code target
- new upstream routing algorithms - the existing scheduler remains the mechanism
- non-Opus models - this phase hardens the Opus 4.7 contract only
- UI redesign - the admin views already exist and do not need a new interaction model
- durable external storage or distributed coordination - the current single-process gateway architecture stays in place

## Constraints

- The phase must remain compatible with the existing single-process Go gateway and JSON-backed config model.
- The contract must stay read-only on the preview/readiness surfaces.
- `claude-opus-4.7` is the only hard-scoped model for this phase.
- The readiness contract must distinguish real model success from stable fallback or transport-level HTTP 200.

## Acceptance Criteria

- [ ] `GET /admin/api/fleet/readiness?model=claude-opus-4-7` returns `healthy`, `degraded`, or `blocked` plus safe concurrency, retry-after, and account eligibility counts.
- [ ] `GET /admin/api/scheduler/preview?model=claude-opus-4-7` is read-only and does not mutate accounts, cooldowns, or counters.
- [ ] `GET /admin/api/fleet/readiness` and `GET /admin/api/scheduler/preview` agree on eligible accounts and reasons for a fixed test fixture.
- [ ] `GET /admin/api/claude-code/model-readiness` reports Opus 4.7 evidence that is consistent with the fleet readiness contract.
- [ ] The phase can be validated without any sub2api code changes.

## Ambiguity Report

| Dimension          | Score | Min  | Status | Notes |
|--------------------|-------|------|--------|-------|
| Goal Clarity       | 0.92  | 0.75 | ✓      | Kiro-Go readiness contract is now explicit and Opus 4.7-scoped |
| Boundary Clarity   | 0.90  | 0.70 | ✓      | sub2api is excluded as an implementation target |
| Constraint Clarity | 0.83  | 0.65 | ✓      | Read-only admin surfaces, single-process gateway, Opus 4.7 only |
| Acceptance Criteria| 0.88  | 0.70 | ✓      | Five pass/fail checks tied to existing surfaces |
| **Ambiguity**      | 0.09  | ≤0.20| ✓      | |

Status: ✓ = met minimum, ⚠ = below minimum (planner treats as assumption)

## Interview Log

| Round | Perspective     | Question summary | Decision locked |
|-------|-----------------|------------------|-----------------|
| 1     | Researcher      | What exists in Kiro-Go today? | Fleet readiness, scheduler preview, model-readiness, model admission, and content-success evidence already exist |
| 2     | Simplifier      | What is the minimum viable phase? | Lock the Kiro-Go readiness contract only; no sub2api implementation work |
| 3     | Boundary Keeper | What is explicitly out of scope? | No sub2api code changes, no new scheduler algorithm, no UI redesign |
| 4     | Failure Analyst | What would invalidate the contract? | Mismatch between readiness surfaces, hidden fallback-as-success, or non-read-only behavior |
| 5     | Seed Closer     | What model is hard-scoped? | `claude-opus-4.7` only |
| 6     | Seed Closer     | What is the verification model? | Black-box verification against Kiro-Go admin/API responses only |

---

*Phase: 05-sub2api-readiness-provider-integration*
*Spec created: 2026-05-21*
*Next step: $gsd-discuss-phase 5 - implementation decisions (how to build what's specified above)*
