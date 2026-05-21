# Phase 7 Research

**Date:** 2026-05-21
**Scope:** Kiro-Go observability, UAT evidence assets, and sub2api readiness evidence

## Findings

Kiro-Go already had most low-level evidence: request logs included selected account, routing decision, attempt trace, fallback state, content success, admission wait, concurrency limit, pressure score, and Opus governor retry/circuit fields. The missing pieces were explicit requested/effective model separation, admission readiness snapshot fields, pressure reason, and content-success evidence source.

Fleet readiness already exposes the right API contract: `status`, `safeConcurrency`, `retryAfterSeconds`, `circuitState`, `lastPressureReason`, account rows, content success rate, stable fallback counters, and empty-completion counters. The implementation needed a reusable evidence builder so request admission and final acceptance evidence could use the same calculation.

Historical UAT assets under `docs/superpowers/uat/` contain prior 100/100 stream and non-stream Opus 4.7 success runs plus 2026-05-21 degraded/blocked evidence, but they are not a single machine-checkable Phase 7 contract. Some historical scripts read local runtime config or sub2api secret files, so they are references only, not the final safe harness.

sub2api already emits the relevant evidence: `kiro_go_readiness_decision` logs include account, status, cache, TTL, retry-after, safe concurrency, requested/effective model, provider, contract, reasons, and error. Its database/account state includes schedulable, rate limit, overload, temporary unschedulable timestamps and reason. Usage and ops logs distinguish upstream 429/529 from scheduling decisions.

## Research Outcome

Phase 7 should implement a Kiro-Go acceptance evidence API and an offline evidence schema/validator. Live UAT should be explicitly gated by readiness; when upstream is blocked, the correct final artifact is a blocked-capacity verdict, not a failed or fake generation PASS.
