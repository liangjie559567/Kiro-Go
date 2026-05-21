# Kiro-Go High Availability Matrix

**Phase:** 02 - High-Availability Account Pool and sub2api Routing  
**Generated:** 2026-05-20  
**Scope:** Kiro-Go account-pool routing, retryability, background traffic, request logs, and black-box `/www/sub2api` evidence.

This matrix separates current-code automated evidence from live UAT evidence. Historical UAT artifacts from 2026-05-20 are valid regression references, but latest-code Phase 2 PASS still requires rerunning live 10x10 after the current code changes.

Machine-readable source: [`docs/kiro-ha-compatibility-matrix.json`](./kiro-ha-compatibility-matrix.json)

| Requirement | Feature | Surface | Automated Status | Live UAT Status | Evidence |
|---|---|---|---|---|---|
| HA-01 | Temporary-limit isolation | account cooldown and readiness | PASS (`account_local_cooldown`) | HISTORICAL_PASS (`pre_current_change_uat`) | `TestHandleClaudeTemporaryLimitFallsThroughToNextAccount`; risk-group UAT |
| HA-02 | Failure taxonomy | `pool.FailureReason` and handler mappings | PASS (`classified_failures`) | NOT_REQUIRED (`unit_contract`) | `TestClassifyFailureReason`; upstream error status tests |
| HA-03 | Retry and attempt behavior | request retry loops and request logs | PASS (`excluded_account_retry`) | HISTORICAL_PASS (`pre_current_change_uat`) | `TestHandleClaudeTemporaryLimitFallsThroughToNextAccount`; `attemptTrace` request log |
| HA-04 | Admission and scheduler observability | `opus_gate`, readiness, request logs | PASS (`model_pressure_gate_and_pool_health`) | HISTORICAL_PASS (`pre_current_change_uat`) | admission tests; model readiness tests; sub2api Opus UAT |
| HA-05 | Background throttling | auto-refresh and health-check workers | PASS (`overlap_guard_and_cooldown_skip`) | NOT_REQUIRED (`worker_unit_contract`) | auto-refresh/health-check selection tests; skipped-count status |
| HA-06 | sub2api retryability semantics | exhausted-pool responses and headers | PASS (`retryable_exhausted_pool_semantics`) | HISTORICAL_PASS (`pre_current_change_uat`) | no-available-account tests; sub2api logs and DB evidence |
| HA-07 | Real 10x10 sub2api Opus 4.7 UAT | `/www/sub2api` black-box | HUMAN_NEEDED (`latest_code_live_uat`) | HISTORICAL_PASS (`pre_current_change_uat`) | `docs/superpowers/uat/sub2api-kiro-opus47-account-lb-20260520144855/UAT-RESULT.md` |
| HA-08 | sub2api Opus 4.7 stable downstream status | `StableDownstream` generation contract | HUMAN_NEEDED (`latest_code_live_uat`) | REQUIRED | `docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js` |

## PASS Rules

- A row may use automated `PASS` when current-code tests exercise the behavior.
- A row must not use live UAT `PASS` unless the referenced run was produced after the current implementation changes.
- Historical PASS evidence is allowed as regression context, not as latest-code final PASS.
- Missing credentials, unavailable services, or real upstream exhaustion must be marked `BLOCKED_BY_ENV`, `BLOCKED_BY_UPSTREAM`, `HUMAN_NEEDED`, or `FAIL` with evidence.
