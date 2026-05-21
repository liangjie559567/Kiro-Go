# Kiro-Go Readiness Cooling Down UAT

Date: 2026-05-22 Asia/Shanghai
Verdict: PASS

## Root Cause

`/admin/api/fleet/readiness?model=claude-opus-4-7` previously returned `status=degraded` while `reasonCodes=["cooling_down"]` even though fleet capacity was usable. Live evidence showed this was not an upstream outage:

- `circuitState=closed`
- `admissionPressureScore=0`
- `safeConcurrency=7` before the fix, later `10` after service restart/config reload
- `locallySchedulableAccounts=20`
- only one account was cooling with `lastFailureReason=temporary_limited`

The bug was fleet-level readiness semantics. A configured admission limit lower than the number of locally schedulable accounts was treated as degraded, and account-level cooling reasons were promoted to top-level reason codes even when enough healthy capacity remained.

## Research Notes

- `jwadow/kiro-gateway` uses account-level circuit breaker/backoff and skips failed accounts while continuing to route through available accounts.
- `hj01857655/kiro-account-manager` separates account availability from whole-system availability by filtering banned/invalid/quota-capped accounts.
- These patterns match the corrected Kiro-Go behavior: partial account cooling is a routing constraint, not a fleet degradation when safe concurrency remains available.

## Code Fix

- `proxy/ecosystem_ops.go`: configured admission concurrency no longer makes readiness degraded by itself.
- `proxy/ecosystem_ops.go`: top-level `reasonCodes` only includes account-level reasons when no accounts are schedulable.
- `proxy/ecosystem_ops.go`: account cooldown retry-after is only promoted to fleet `retryAfterSeconds` when no local capacity remains.
- `proxy/ecosystem_ops_test.go`: added regression coverage for configured safe limit and partial cooldown with healthy fleet capacity.

## Verification

| Check | Result | Evidence |
| --- | --- | --- |
| Go unit tests | PASS | `go test ./pool ./proxy`; `go test ./...` |
| Whitespace check | PASS | `git diff --check` |
| Docker rebuild | PASS | `docker compose up -d --build` |
| Container health | PASS | `health.json`: `status=ok`, version `1.0.8` |
| Real readiness API | PASS | `readiness-after-summary.json`: `status=healthy`, `reasonCodes=["healthy"]`, `safeConcurrency=10`, `locallySchedulableAccounts=20`, `coolingDownAccounts=1`, `retryAfterSeconds=0` |
| Playwright-MCP admin UI | PASS | `playwright-api-page.yml`: Opus 4.7 fleet health shows `Status healthy`, `Safe concurrency 10 / 10 · local 20`, `Retry after 0s`, `Reasons healthy`, and summary `Cooling: 1 · Temporary limited: 1` |
| Edge-case UAT harness | PASS | `/root/gsd-workspaces/edge-case-uat-harness/runs/20260521172959/evidence-manifest.json` verdict `PASS` |
| Harness DB evidence | PASS | latest run `usageCount=88`, `errorCount=0` |
| Harness browser evidence | PASS | latest run browser summary has Kiro-Go and sub2api admin login success, no console errors/page errors |

## Final Readiness State

```json
{
  "status": "healthy",
  "circuitState": "closed",
  "reasonCodes": ["healthy"],
  "safeConcurrency": 10,
  "admissionEffectiveConcurrency": 10,
  "locallySchedulableAccounts": 20,
  "coolingDownAccounts": 1,
  "temporaryLimitedAccounts": 1,
  "admissionPressureScore": 0,
  "lastPressureReason": "healthy",
  "retryAfterSeconds": 0,
  "recommendedAction": "send_with_safe_concurrency"
}
```

This is the expected outcome: the cooling account remains visible and must not be probed aggressively, but the fleet is healthy because safe concurrency remains available.
