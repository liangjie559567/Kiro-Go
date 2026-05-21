# UAT Result: Claude Code -> sub2api -> Kiro-Go E2E

Date: 2026-05-21 17:14-17:18 Asia/Shanghai
Verdict: PASS for the tested Claude Code /v1/messages route through sub2api to Kiro-Go.

## Scope

- Downstream entry: sub2api `http://127.0.0.1:18080/v1/messages`
- Upstream target: Kiro-Go Docker service `http://kiro-go:8080/v1/messages`
- Request shape: Claude Code style Anthropic Messages request with `anthropic-version`, `anthropic-beta: claude-code-20250219`, `x-claude-code-session-id`, `x-claude-code-agent-id`, and `claude-cli` user agent.
- Model requested by downstream: `claude-sonnet-4-6`
- Model sent upstream after mapping: `claude-sonnet-4.6`

## Fix Applied

The running sub2api account `kiro_claude_01` (account id 24) already pointed at `http://kiro-go:8080`, but its model allowlist lacked Claude Code's hyphenated `claude-sonnet-4-6` form. This caused routing failures before an upstream account was selected.

A runtime account model mapping was added:

- `claude-sonnet-4-6` -> `claude-sonnet-4.6`
- `claude-sonnet-4-6-thinking` -> `claude-sonnet-4.6-thinking`
- Additional dated/hyphen Claude aliases for common Claude Code forms.

sub2api was restarted after the mapping change and returned healthy.

## Browser Evidence

- Kiro-Go admin screenshot: `screenshots/kiro-go-admin-post-mapping-fix-2026052117.png`
  - Shows Kiro-Go admin loaded, running, version `v1.0.8`, with account/request/success/failure counters visible.
- sub2api browser screenshot: `screenshots/sub2api-login-health-2026052117.png`
  - Shows the live sub2api frontend is reachable at port `18080`; the browser session was not logged in, so verification used API/DB/log evidence rather than UI-only claims.

## API Evidence

Health checks:

- Kiro-Go: `api/kiro-health.json` -> `status: ok`, version `1.0.8`
- sub2api: `api/sub2api-health.json` -> `status: ok`

Single request proof:

- Non-stream: `api/nonstream-result.json`
  - HTTP `200`
  - response type `message`
  - response model `claude-sonnet-4.6`
  - stop reason `end_turn`
  - text matched expected marker `SUB2API_KIRO_GO_OK`
- Stream: `api/stream-result.json`
  - HTTP `200`
  - content type `text/event-stream`
  - event sequence included `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`
  - `has_message_stop: true`
  - text matched expected marker `SUB2API_KIRO_GO_STREAM_OK`

Load proof:

- Non-stream 100/100: `api/load-nonstream-results.json`
  - `pass: 100`, `fail: 0`, status `200: 100`
  - concurrency `10`
  - p50 `3216ms`, p95 `24543ms`, max `28804ms`
- Stream 100/100: `api/load-stream-results.json`
  - `pass: 100`, `fail: 0`, status `200: 100`
  - concurrency `10`
  - every stream required `message_stop`
  - p50 `2814ms`, p95 `12760ms`, max `19144ms`

## Database Evidence

`db/usage-aggregate.txt` shows all UAT calls were routed through the intended path:

- total usage rows for the UAT key: `202`
- account id `24`: `202`
- group id `1`: `202`
- requested model `claude-sonnet-4-6`: `202`
- upstream model `claude-sonnet-4.6`: `202`
- non-stream rows: `101`
- stream rows: `101`

`db/error-aggregate.txt` shows UAT errors: `0`.

`db/usage-logs.txt` contains the first single non-stream and stream request IDs and confirms `account_id=24`.

The temporary UAT key was disabled and soft-deleted after verification; see `db/uat-key-cleanup.txt`.

## Log Evidence

- sub2api runtime log: `logs/sub2api-after-load.log`
- Kiro-Go runtime log: `logs/kiro-go-after-load.log`
- Docker service state: `logs/docker-ps.txt`, `logs/docker-compose-ps.txt`

Logs were redacted before saving. Secret scan found no full admin password, original user-supplied key, or temporary UAT key in this UAT directory.

## External Research Alignment

The PASS criteria were aligned with public Claude Code/Anthropic and kiro-gateway behavior:

- Claude Code gateway must support Anthropic `POST /v1/messages` and forward Claude headers.
- Streaming must terminate with `message_stop`; missing terminal SSE event is a failure.
- Retryable gateway failures such as `503`, transient `429`, timeout, and stream disconnect can cause Claude Code retry amplification.
- The prior `503 no available accounts` state therefore remained FAIL until DB/API evidence showed account 24 selected and stream termination complete.

## Final Verdict

PASS.

The tested Claude Code style requests now traverse:

`Claude Code-shaped client -> sub2api /v1/messages -> account 24 kiro_claude_01 -> Kiro-Go /v1/messages -> successful Anthropic response`

This verdict is limited to the tested model alias `claude-sonnet-4-6`, Kiro-Go account id 24, and 10-concurrency 100 non-stream + 100 stream workload. It does not certify unrelated models, disabled accounts, external domains, or higher sustained load.
