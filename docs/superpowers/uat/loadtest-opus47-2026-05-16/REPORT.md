# 2026-05-16 Opus 4.7 sub2api -> Kiro-Go Load Test Verification

Scope: real downstream calls through sub2api to Kiro-Go `/v1/messages`, model `claude-opus-4.7`, 10 concurrency, 100 non-streaming requests and 100 streaming requests. Every request used a unique marker and validated returned content exactly.

## Production Fix Before Test

Initial precheck failed with `503 No available accounts` after authentication succeeded. Root cause: sub2api account `24` (`kiro_claude_01`) had `model_mapping` for `claude-opus-4-7` but not for requested `claude-opus-4.7`, so the scheduler filtered the only Kiro-Go account as model-unsupported.

Fix applied in production DB: added `credentials.model_mapping["claude-opus-4.7"] = "claude-opus-4.7"` for account `24`, inserted a scheduler outbox event, and restarted `sub2api`. Post-fix prechecks passed for both sync and stream.

## Target

- Endpoint: `http://127.0.0.1:18080/v1/messages`
- API key: sub2api API key id `2` (`claude`, redacted)
- Routed account: `account_id=24`, `kiro_claude_01`
- Channel/group: `channel_id=2`, group `claude`
- Model: `claude-opus-4.7`
- Kiro-Go upstream base URL: `http://kiro-go:8080`

## Non-Streaming Result

- Total: 100
- Concurrency: 10
- HTTP 200: 100/100
- Exact content correct: 100/100
- Failed/error requests: 0
- Wall time: 28.666s
- Latency ms: min 1021, avg 2463, p50 1239, p90 6130, p95 6620, p99 11595, max 12242
- Detail files: `nonstream-results.jsonl`, `nonstream-summary.json`

## Streaming Result

- Total: 100
- Concurrency: 10
- HTTP 200: 100/100
- Exact SSE-assembled content correct: 100/100
- SSE events positive: 100/100
- Failed/error requests: 0
- Wall time: 22.161s
- End-to-end latency ms: min 1038, avg 2029, p50 1193, p90 5302, p95 6270, p99 7618, max 8256
- First-byte ms: min 1036, avg 2028, p50 1192, p90 5301, p95 6269, p99 7616, max 8255
- SSE data events per request: min 6, avg 6, max 6
- Detail files: `stream-results.jsonl`, `stream-summary.json`

## Database Evidence

`usage_logs` for `api_key_id=2`, `account_id=24`, `model='claude-opus-4.7'`, after `2026-05-16 20:55:00+08`:

- Recent count: 202 rows, including 2 post-fix precheck requests.
- Non-stream rows: 101
- Stream rows: 101
- DB duration ms: min 1013, avg 2227, p50 1195, p90 6028, p95 6554, p99 8223, max 12233
- Token totals: input 1,198,070, output 2,624
- Latest rows show `api_key_id=2`, `account_id=24`, `channel_id=2`, `group_id=1`, `/v1/messages -> /v1/messages`, stream true/false, and `first_token_ms` populated for streaming rows.

## Browser Evidence

Screenshots captured with Playwright-MCP and archived in this directory:

- `sub2api-opus47-usage-2026-05-16.png`: Usage page shows `claude-opus-4.7` with 202 requests, `claude-opus-4-7` pricing bucket, `claude` group, and `/v1/messages`.
- `sub2api-opus47-accounts-2026-05-16.png`: Accounts page shows `kiro_claude_01`, Anthropic Key, Active, group `claude`, recent usage.
- `kiro-go-opus47-request-logs-2026-05-16.png`: Kiro-Go admin request logs show repeated `claude-opus-4.7` requests, HTTP 200, latency, first-token timing, and token counts.
- Structured browser checks: `browser-evidence.json`.

Note: direct Playwright navigation to sub2api port `18080` is blocked in this environment. Browser validation used Playwright routing from a reachable local origin to the real deployed `127.0.0.1:18080` backend; CSP response headers were removed only in the browser proxy so the deployed frontend could execute.

## Log Evidence

sub2api logs during the run show:

- `sticky.account_selected` with `selected_account_id=24`, `account_name=kiro_claude_01`.
- `[Anthropic 自动透传]` API key passthrough branch hit for account 24.
- `status_code=200`, path `/v1/messages`, model `claude-opus-4.7`.

Kiro-Go logs showed one upstream Opus 4.7 `INSUFFICIENT_MODEL_CAPACITY` warning during the window, but retry/fallback recovered and every downstream request still completed HTTP 200 with exact marker correctness.

## Verdict

PASS. After fixing the sub2api model mapping for `claude-opus-4.7`, both non-streaming and streaming load tests completed with 100% HTTP success and 100% exact content correctness. Database rows, service logs, and Playwright-rendered admin pages match the routed account, endpoint, model, stream mode, latency, and usage records.
