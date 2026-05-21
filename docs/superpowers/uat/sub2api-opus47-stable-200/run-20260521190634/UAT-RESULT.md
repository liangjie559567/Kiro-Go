# sub2api -> Kiro-Go Opus 4.7 100/100 UAT Result

Date: 2026-05-21 19:06 Asia/Shanghai

Verdict: **BLOCKED_BY_UPSTREAM_CAPACITY, not PASS**

## Scope

- Path: `sub2api /v1/messages -> account_id=24 kiro_claude_01 -> Kiro-Go /v1/messages -> Kiro upstream`
- Model: `claude-opus-4-7`, mapped by Kiro-Go to `claude-opus-4.7`
- Requested target: `100/100` non-stream plus `100/100` stream
- Effective concurrency: `1`, capped by Kiro-Go readiness `safeConcurrency=1`

## What Passed

- Docker services were healthy after rebuilding sub2api with the Kiro readiness fix.
- Smoke run passed: `1/1` non-stream and `1/1` stream both returned valid assistant content.
- sub2api selected the real Kiro-Go account: `account_id=24`, `account_name=kiro_claude_01`.
- DB usage evidence for the smoke run recorded `2` rows for `claude-opus-4-7`, both on account `24`.

## What Failed

The full run was stopped after 12 non-stream attempts and 0 stream attempts:

- `9/12` non-stream requests passed.
- `3/12` non-stream requests returned HTTP `200` with gateway capacity fallback text.
- Failed samples included `opus47_budget_exhausted` and `admission_pressure`.
- Because fallback text is not real model content, this cannot be marked PASS.

Kiro-Go readiness after the block:

- `status=degraded`
- `safeConcurrency=1`
- `reasonCodes=["admission_pressure"]`
- `lastPressureReason=rate_limited_or_model_capacity`
- `recommendedAction=limit_to_safe_concurrency`
- `recommendedQueueWaitSeconds=120`

## Evidence

- Runner results: `non-stream.jsonl`, `summary-blocked.json`
- Kiro readiness API: `evidence/readiness-after-block.json`
- sub2api redacted logs: `evidence/sub2api-redacted.log`
- Kiro-Go capacity logs: `evidence/kiro-go-capacity.log`
- DB usage aggregate: `evidence/db-usage-api-key-11.txt`
- UAT key cleanup proof: `evidence/db-key-cleanup.txt`
- Playwright screenshot: `evidence/kiro-opus47-readiness-after-block-202605211908.png`

## Screenshot Analysis

The screenshot shows Kiro-Go admin is running with `24` accounts. Counters after the run show request and failure totals increased, matching the observed capacity fallback behavior. The screenshot supports service health and admin visibility, but it does not override API/log/body evidence; because response bodies contained gateway capacity fallback text, the UAT remains blocked rather than PASS.

## Fixes Applied

- sub2api readiness matching now covers Anthropic APIKey accounts whose `base_url` points to Kiro-Go, not only OpenAI APIKey accounts.
- Runtime config now enables `gateway.scheduling.kiro_go_readiness` against `http://kiro-go:8080/admin/api/fleet/readiness`.
- The 100/100 runner now stops early with `BLOCKED_BY_UPSTREAM_CAPACITY` when it sees Kiro-Go capacity fallback text, `admission_pressure`, `opus47_budget_exhausted`, or HTTP 429/502/503.

## Follow-Up Gate

Do not rerun full `100/100 + 100/100` until readiness remains stable and no fallback text appears in a smaller probe. A valid PASS requires all 200 responses to contain real assistant text, thinking, or tool_use content.
