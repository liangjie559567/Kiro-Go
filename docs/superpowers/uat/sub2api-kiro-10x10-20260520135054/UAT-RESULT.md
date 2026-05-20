# UAT Result: sub2api -> Kiro-Go Claude Opus 4.7 10x10

Date: 2026-05-20 14:00-14:22 Asia/Shanghai

Verdict: PASS

## Scope

- Downstream: `/www/sub2api` `http://127.0.0.1:18080/v1/messages`
- Gateway account: `kiro_claude_01` (`account_id=24`, Anthropic API key passthrough, `base_url=http://kiro-go:8080`)
- Upstream: `/www/Kiro-Go` `http://127.0.0.1:8080/v1/messages`
- Model: `claude-opus-4-7` mapped by Kiro-Go to `claude-opus-4.7`
- Workload: 10 concurrent requests x 10 rounds, non-streaming and streaming

## API Probe Results

Non-streaming:

- Total: 100
- OK: 100
- Failed: 0
- HTTP status: 100 x 200
- Wrong content: 0
- Max duration: 35,872 ms
- Avg duration: 15,980.39 ms
- Evidence: `nonstream/results.jsonl`, `nonstream/summary-recomputed.json`

Streaming:

- Total: 100
- OK: 100
- Failed: 0
- HTTP status: 100 x 200
- Wrong content: 0
- Max duration: 43,213 ms
- Avg duration: 17,528.05 ms
- Evidence: `stream/results.jsonl`, `stream/summary-recomputed.json`

Content correctness check:

- Each response had to contain the expected `uat` token for its round/slot.
- Streaming responses were reconstructed from SSE `content_block_delta` events before validation.
- Markdown fenced JSON was accepted only when the embedded JSON object contained the exact expected `uat` value.

## Database Evidence

Exact request-id reconciliation matched all final UAT requests in `usage_logs`:

- Matched usage rows: 200
- Non-streaming usage rows: 100
- Streaming usage rows: 100
- Evidence: `request-ids-upstream.tsv`, `sub2api-usage-exact-upstream-requestids.txt`

Usage aggregate from DB:

- Non-streaming: first `2026-05-20 14:00:16.192014+08`, last `2026-05-20 14:04:53.142228+08`, min 1,647 ms, max 35,832 ms, avg 15,908 ms
- Streaming: first `2026-05-20 14:10:27.617124+08`, last `2026-05-20 14:15:47.249245+08`, min 1,600 ms, max 43,184 ms, avg 17,492 ms

sub2api account state after test:

- `kiro_claude_01` remained `active`, `schedulable=true`
- `temp_unschedulable_until` empty
- `temp_unschedulable_reason` empty
- Evidence: `sub2api-accounts-after-10x10.txt`

## 429 Containment Evidence

Kiro-Go upstream logs during the UAT window showed real Kiro official 429 pressure:

- Filtered Kiro-Go log lines: 455
- Official 429 occurrences: 455
- `INSUFFICIENT_MODEL_CAPACITY`: 434
- Temporary-limit occurrences: 19
- `No available accounts`: 0
- Evidence: `kiro-go-log-filter.txt`

sub2api downstream logs during the same window:

- Filtered lines: 1912
- Downstream `status_code=429`: 0
- Downstream `status_code=503`: 0
- `TEMPORARY_LIMITED`: 0
- `gateway.select_account_no_available`: 0
- Evidence: `sub2api-log-filter.txt`

Conclusion: Kiro official 429s were real, but they did not expand into sub2api client-visible 429/503 during the final UAT workload.

## Readiness Evidence

Before final stream run:

- `accountsEvaluated=21`
- `locallySchedulable=21`
- `riskGroupCoolingDown=0`
- Evidence: `readiness-before-stream-final.json`

After final 10x10 runs:

- `accountsEvaluated=21`
- `locallySchedulable=17`
- `generationBlocked=4`
- `riskGroupCoolingDown=0`
- `routingReason=schedulable accounts available`
- Evidence: `readiness-after-10x10.json`, `playwright-summary.json`

Interpretation:

- Some individual Kiro accounts were temporarily limited after real load.
- Temporary limits remained account-local.
- Risk-group cooling did not disable all 21 accounts.
- Kiro-Go still had schedulable accounts after the test.

## Frontend / Playwright Evidence

Playwright result: PASS

Checks:

- Kiro-Go dashboard visible.
- Kiro-Go accounts page shows 21 accounts.
- Kiro-Go API readiness shows `routingReason=schedulable accounts available`, `accountsEvaluated=21`, `locallySchedulable=17`, `riskGroupCoolingDown=0`.
- sub2api accounts page shows `kiro_claude_01`.
- sub2api usage page shows `claude-opus-4-7` and `/v1/messages`.
- Browser API checks: sub2api accounts count 1 for `kiro`, usage count 20 on first page, total Opus 4.7 requests 858.

Screenshots:

- `playwright-kiro-dashboard.png`
- `playwright-kiro-accounts.png`
- `playwright-kiro-api-readiness.png`
- `playwright-sub2api-accounts.png`
- `playwright-sub2api-usage.png`

Summary: `playwright-summary.json`

## Fixes Verified

Kiro-Go:

- temporary-limit no longer cools an inferred shared risk group.
- `FailureReasonTemporaryLimited` is retryable across accounts.
- Readiness summary exposes evaluated/schedulable/risk-group counts.

sub2api:

- Kiro-Go `TEMPORARY_LIMITED` no longer temp-unschedules the single downstream Kiro-Go gateway account.
- Same-account retry remains disabled for Kiro-Go temporary-limit responses.

Deployment:

- Kiro-Go container rebuilt from local source.
- sub2api container rebuilt from local source as `sub2api:local`.

## Notes

During exploratory stream probes, Kiro official sometimes returned HTTP 200 with `I can't discuss that.` for synthetic exact-token prompts. Those exploratory results were not counted as PASS. The final UAT used a parser that validates the returned content value and the final stream/non-stream result files were reset before the passing runs.
