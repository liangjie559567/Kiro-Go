# 2026-05-16 sub2api -> Kiro-Go Load Test Verification

Scope: real sub2api downstream calls to Kiro-Go through `/v1/messages`, 10 concurrency, 100 non-streaming requests and 100 streaming requests. Each request used a unique expected marker and validated the returned content exactly.

## Target

- Endpoint: `http://127.0.0.1:18080/v1/messages`
- API key: sub2api API key id 2 (`claude`, redacted)
- Routed account: `account_id=24`, `kiro_claude_01`
- Channel/group: `channel_id=2`, group `claude`
- Model: `claude-sonnet-4.5`
- Kiro-Go upstream base URL: `http://kiro-go:8080`

## Non-Streaming Result

- Total: 100
- Concurrency: 10
- HTTP 200: 100/100
- Exact content correct: 100/100
- Failed/error requests: 0
- Wall time: 20.402s
- Latency ms: min 1617, avg 1996, p50 1846, p90 2014, p95 2256, p99 6269, max 9409
- Detail files: `nonstream-results.jsonl`, `nonstream-summary.json`

## Streaming Result

- Total: 100
- Concurrency: 10
- HTTP 200: 100/100
- Exact SSE-assembled content correct: 100/100
- SSE events positive: 100/100
- Failed/error requests: 0
- Wall time: 25.325s
- End-to-end latency ms: min 1588, avg 1998, p50 1788, p90 2149, p95 2691, p99 8065, max 9132
- First-byte ms: min 1587, avg 1997, p50 1786, p90 2149, p95 2690, p99 8065, max 9131
- SSE data events per request: min 6, avg 6, max 6
- Detail files: `stream-results.jsonl`, `stream-summary.json`

## Database Evidence

Recent usage rows for `api_key_id=2`, `account_id=24`, `model=claude-sonnet-4.5` after the test:

- Recent count: 202 rows in the 10-minute window, including the two precheck requests.
- Non-stream rows: 101
- Stream rows: 101
- DB duration ms: min 1580, avg 1997, p50 1799, p90 2063, p95 2684, max 9373
- Token totals in the 202-row window: input 833348, output 2019
- Latest rows show `/v1/messages -> /v1/messages`, account 24, channel 2, group 1, status recorded through usage logs.

## Browser Evidence

Screenshots captured with Playwright-MCP and archived in this directory:

- `sub2api-loadtest-usage-2026-05-16.png`: Usage page shows `claude-sonnet-4.5`, `claude` group, `/v1/messages`, and recent `kiro_claude_01` stream rows.
- `sub2api-loadtest-account-kiro-2026-05-16.png`: Accounts page filtered to `kiro_claude_01`, Active, schedulable, Anthropic Key, group `claude`, recent use 1 minute ago.
- `kiro-go-loadtest-request-logs-2026-05-16.png`: Kiro-Go admin request logs show repeated `claude-sonnet-4.5` requests, HTTP 200, latency, first-token timing, token counts, and no error.

Note: Direct Playwright navigation to sub2api port 18080 remains browser-policy blocked in this environment. Browser validation used Playwright request routing from a reachable local origin to the real `127.0.0.1:18080` backend, so the rendered UI and API responses are from the deployed sub2api service.

## Verdict

PASS. Both non-streaming and streaming load tests completed with 100% HTTP success and 100% exact content correctness. Database and browser evidence match the routed account, endpoint, model, stream mode, latency, and usage records.
