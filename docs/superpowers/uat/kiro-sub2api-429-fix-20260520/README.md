# Kiro-Go Temporary Limit 429 / sub2api UAT - 2026-05-20

## Verdict

PASS for the targeted Claude Code/sub2api failure mode.

Kiro-Go no longer maps Kiro upstream temporary-limit 429s into Anthropic `409 invalid_request_error`. In the Docker service, sub2api now receives Anthropic-compatible `429 rate_limit_error`, retries/failovers it as a retryable upstream limit, and returns 429 to clients when all attempts are exhausted. The observed Claude Code-style `502 Upstream request failed` path is removed for this root cause.

## Root Cause

| Stage | Before | After |
| --- | --- | --- |
| Kiro upstream suspicious temporary limit | HTTP 429 | HTTP 429 |
| Kiro-Go outbound Anthropic error | `409 invalid_request_error` | `429 rate_limit_error` |
| sub2api classification | non-retryable upstream error | retryable rate-limit/failover |
| Claude Code-visible result | `502 Upstream request failed` | `429 rate_limit_error` or successful retry |

The concrete bug was in Kiro-Go's pool temporary-limit mapping. Pool cooldown errors and real suspicious temporary-limit errors were surfaced as invalid request errors instead of Anthropic rate-limit errors.

## Official / Reference Basis

- Anthropic error responses use a top-level `type: "error"` with nested `error.type`; rate limits are represented as `rate_limit_error` and HTTP 429.
- Anthropic requests may use `/v1/messages`, `/v1/messages/count_tokens`, model list compatibility, `cache_control`, tools, and streaming semantics.
- `jwadow/kiro-gateway` confirms the same gateway direction: Anthropic-compatible `/v1/messages`, OpenAI-compatible endpoints, model normalization/pass-through, multi-account failover, retry handling for 429/5xx, streaming, tool calling, vision, web search, and full message history.

Sources used during review:
- Anthropic API errors: https://docs.anthropic.com/en/api/errors
- Anthropic prompt caching: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
- `jwadow/kiro-gateway`: https://github.com/jwadow/kiro-gateway

## Code Evidence

Changed behavior is in:

- `proxy/handler.go`: `poolTemporaryLimitStatus = http.StatusTooManyRequests`
- `proxy/handler.go`: `claudeUpstreamErrorStatusAndType` returns `429/rate_limit_error` for `poolTemporaryLimitError` and `pool.FailureReasonTemporaryLimited`
- `proxy/handler.go`: `openAIUpstreamErrorStatusAndType` returns equivalent 429 semantics for OpenAI-compatible paths
- `proxy/handler_test.go`: regression tests cover Claude/OpenAI mapping, suspicious temporary limit persistence, and pool cooldown Anthropic response shape

Focused and full verification were run before this UAT:

```bash
go test ./proxy -run 'TestClaudeUpstreamErrorsMapToAnthropicErrorTypes|TestOpenAIUpstreamErrorsMapToGatewayTypes|TestSuspiciousTemporaryLimitStopsRequestAndPersistsCooldown|TestSendNoAvailableAccountsMapsTemporaryLimitedPoolToClaudeRetryableRateLimit' -count=1
go test ./... -count=1
docker compose up -d --build kiro-go
```

## Docker Evidence

Saved files:

- `api/kiro-health.json`
- `api/docker-ps.txt`

Observed:

- Kiro-Go health: `{"status":"ok","uptime":314,"version":"1.0.8"}`
- `kiro-go-kiro-go-1` up on port 8080
- `sub2api`, `sub2api-postgres`, and `sub2api-redis` all healthy/up

## Direct Kiro-Go API Evidence

Saved files:

- `api/kiro-models.body.json`
- `api/kiro-models.headers.txt`
- `api/kiro-count-tokens.body.json`
- `api/kiro-count-tokens.headers.txt`
- `api/kiro-request-logs.json`

Observed:

- `/v1/models` returned 200 and 31 models, including `auto`, `auto-thinking`, `claude-opus-4.7`, `claude-opus-4.7-thinking`, `claude-opus-4-7`, and `claude-opus-4-7-thinking`.
- `/v1/messages/count_tokens` returned 200 with `{"input_tokens":6}` and `X-Kiro-Go-Token-Count-Mode: estimated`.
- Admin request logs show recent Opus 4.7 temporary-limit responses as `statusCode: 429`.

Note: one earlier count_tokens probe used the wrong config key name and returned 401. It was rerun with the correct `apiKey` and is not used as pass evidence.

## Real sub2api Evidence

Saved files:

- `api/sub2api-message-uat-kiro-429-fix-20260520015846.meta.txt`
- `api/sub2api-message-uat-kiro-429-fix-20260520015846.headers.txt`
- `api/sub2api-message-uat-kiro-429-fix-20260520015846.body.json`
- `api/sub2api-logs-uat-kiro-429-fix-20260520015846.txt`

Request:

```bash
POST http://127.0.0.1:18080/v1/messages
model: claude-opus-4-7
stream: false
x-request-id: uat-kiro-429-fix-20260520015846
```

Observed response:

```json
{
  "error": {
    "message": "Upstream rate limit exceeded, please retry later",
    "type": "rate_limit_error"
  },
  "type": "error"
}
```

Observed status: `429`, not `502`.

sub2api logs for the same request show:

- Kiro-Go upstream body contained `type:"rate_limit_error"`
- `gateway.failover_same_account_retry`
- `gateway.failover_switch_account`
- final HTTP access `status_code: 429`

Earlier same-environment success evidence from the recovered request session:

- request id: `2ab8a579-15cf-40d0-9446-7f6f6fae090e`
- status: `200`
- body contained a valid Anthropic message with `model: "claude-opus-4.7"` and assistant text `sub2api kiro smoke`

## Database Evidence

Saved files:

- `db/usage-logs-recent.txt`
- `db/ops-error-logs-recent.txt`

sub2api account evidence:

- account `24`, name `kiro_claude_01`
- platform `anthropic`
- base URL `http://kiro-go:8080`
- `anthropic_passthrough=true`

Usage evidence includes successful Kiro-Go passthrough entries:

- `req_9f15ad30-6950-404d-b546-c6dfac6fe78a`
- account `24`
- `/v1/messages` -> `/v1/messages`
- model/requested_model `claude-opus-4-7`
- output tokens recorded

Error-log evidence shows the before/after fix:

- Before: rows `1238`, `1239`, `1240` recorded `status_code=502`, `upstream_status_code=409`, `error_type=upstream_error`, message `Upstream request failed`.
- After: row `1256` for `uat-kiro-429-fix-20260520015846` recorded `status_code=429`, `upstream_status_code=429`, `error_type=rate_limit_error`, `is_retryable=true`.

## Browser Evidence

Real Chromium via Playwright was used. No Playwright-MCP tool is exposed in this environment, so Playwright CLI/script was used as the available real-browser equivalent.

Saved screenshots:

- `screenshots/kiro-admin-login.png`
- `screenshots/kiro-admin-dashboard-after-eval-login.png`
- `screenshots/sub2api-home.png`

Saved text extraction/debug:

- `api/playwright-page-text.json`
- `api/kiro-login-debug.json`

Screenshot analysis:

- `kiro-admin-dashboard-after-eval-login.png` shows Kiro-Go dashboard loaded after real login: status `运行中`, version `v1.0.8`, account/request/success/failure/token/credit counters, account list, cooldown badges, and risk-group badges.
- `sub2api-home.png` shows the sub2api frontend loaded: CGTall-AI home page, API conversion positioning, login entry, and supported Claude/GPT/Gemini/Antigravity cards.

## Parity / Optimization Backlog from kiro-gateway Comparison

Kiro-Go now covers the immediate Claude Code/sub2api compatibility break. Further parity work should focus on:

1. Keep Anthropic/OpenAI behavior consistent across streaming and non-streaming paths.
2. Expand official Anthropic surface checks for tools, tool_result, image blocks, thinking, prompt caching, count_tokens, and beta headers.
3. Add first-token timeout retry evidence comparable to `kiro-gateway`'s streaming retry layer.
4. Keep model resolver optimistic but scoped: normalize only known compatibility aliases, preserve unknown pass-through aliases.
5. Add end-to-end Claude Code scripted UAT for streaming tool loops once upstream account capacity is available.

## Final Result

PASS for the requested root-cause fix and real integration verification:

- latest Kiro-Go Docker service is running
- sub2api remains healthy and can call Kiro-Go
- direct Kiro-Go APIs respond correctly
- real sub2api calls no longer convert temporary limits into 502
- database and logs prove the status/type transition from 502/409 to 429/429
- browser screenshots prove both Kiro-Go admin and sub2api frontend load correctly
