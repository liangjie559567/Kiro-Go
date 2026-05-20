---
phase: 02-b-high-availability-account-pool-and-sub2api-routing
verified: 2026-05-20T08:45:00Z
status: human_needed
score: 8/9 must-haves verified
---

# Phase 2: B - High-Availability Account Pool and sub2api Routing Verification Report

**Phase Goal:** Kiro-Go schedules accounts by real per-account availability and integrates with `/www/sub2api` so high-concurrency Claude Code calls route correctly instead of amplifying one account's 429.  
**Verified:** 2026-05-20T08:45:00Z  
**Status:** human_needed

## Goal Achievement

| # | Truth | Status | Evidence |
|---|---|---|---|
| 1 | HA matrix covers HA-01 through HA-07 and does not overclaim latest-code live PASS | VERIFIED | `docs/kiro-ha-compatibility-matrix.json`; `TestKiroHAMatrixIsCompleteAndHonest` |
| 2 | Temporary-limit cooldown remains account-local | VERIFIED | `TestHandleClaudeTemporaryLimitFallsThroughToNextAccount`; pool temporary-limit tests |
| 3 | Failure taxonomy maps routing-relevant Kiro failures | VERIFIED | `pool/account_test.go:TestClassifyFailureReason` |
| 4 | Recoverable account-local failures fall through to other accounts | VERIFIED | `TestHandleClaudeTemporaryLimitFallsThroughToNextAccount` |
| 5 | Request logs expose per-attempt selection/failure/success trace | VERIFIED | `attemptTrace[]`; request-log and handler tests |
| 6 | Model admission and pressure are tested and observable | VERIFIED | `proxy/opus_gate_test.go`; readiness/admission tests |
| 7 | Auto-refresh and health checks skip cooling pressure accounts and expose skipped counts | VERIFIED | auto-refresh and health-check selection/status tests |
| 8 | Exhausted-pool responses expose retryability only when no viable account remains | VERIFIED | no-available-account handler tests |
| 9 | Latest-code live `/www/sub2api` 10x10 stream/non-stream evidence exists | NEEDS HUMAN | Historical PASS exists, but current code must be rerun |

**Score:** 8/9 truths verified

## Requirements Coverage

| Requirement | Status | Blocking Issue |
|-------------|--------|----------------|
| HA-01 | SATISFIED | - |
| HA-02 | SATISFIED | - |
| HA-03 | SATISFIED | - |
| HA-04 | SATISFIED BY CURRENT ARCHITECTURE | Admission is model-level; account-aware availability is pool/readiness owned |
| HA-05 | SATISFIED BY AUTOMATED TEST CONTRACT | Jitter remains deferred; cooldown awareness and skipped counts implemented |
| HA-06 | SATISFIED | - |
| HA-07 | NEEDS HUMAN | Rerun live 10x10 stream/non-stream UAT against latest code |

## Historical UAT Evidence

- `docs/superpowers/uat/sub2api-kiro-opus47-account-lb-20260520144855/UAT-RESULT.md`: PASS, non-stream 100/100, stream 100/100, max latency 56595 ms, sub2api account remained schedulable.
- `docs/superpowers/uat/sub2api-kiro-10x10-20260520135054/UAT-RESULT.md`: PASS, non-stream 100/100, stream 100/100, Kiro-Go saw real upstream 429 pressure while sub2api saw no downstream 429/503.

These runs are regression evidence. They were produced before the latest request-log/background-worker changes and must be rerun before marking Phase 2 final PASS.

## Human Verification Required

Run the fixed Opus 4.7 `/www/sub2api` workload against the latest Kiro-Go build:

- 10 concurrent x 10 non-stream `/v1/messages`
- 10 concurrent x 10 stream `/v1/messages`
- Capture max/average latency, Kiro-Go readiness before/after, Kiro-Go request logs with `attemptTrace`, sub2api DB/usage state, sub2api logs, and Playwright screenshots.

## Verification Metadata

**Automated checks:** focused HA tests passed; full repository test recorded separately in the autonomous run.  
**Human checks required:** latest-code live sub2api 10x10.  
**Secret handling:** no runtime secret files were read; `/www/sub2api` source was not modified.
