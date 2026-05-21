# Kiro-Go -> sub2api Opus 4.7 UAT

Run time: 2026-05-21 23:35-23:42 CST

## Verdict

PASS_WITH_CURRENT_CAPACITY_RECOVERED.

The live Docker path is healthy and callable end to end:

`Claude Code / Anthropic client -> sub2api :18080 -> Kiro-Go :8080 -> Kiro upstream`

The original capacity-exhausted symptom could not be reproduced naturally during this run because Opus 4.7 capacity had recovered. Current real non-stream, stream, and Claude Code calls all returned `ok` and were recorded in sub2api Postgres `usage_logs`. Therefore this UAT passes the deployment, health, browser, API, DB, and Claude Code integration checks, while the overload-fallback behavior is covered by the fresh regression suite rather than by a live overloaded upstream event.

## External Research

- Anthropic/Claude API error documentation defines HTTP `529` as `overloaded_error`, meaning the API is temporarily overloaded.
- Claude Code settings documentation confirms `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`, and gateway-oriented configuration are supported for LLM gateway deployments.
- `jwadow/kiro-gateway` uses recoverable-error classification for 429 and network/5xx-like failures, tries alternate accounts, propagates first-token timeout errors, and does not treat upstream failures as successful assistant content.
- `hj01857655/kiro-account-manager` gateway/account code tracks account rate limits, quota, status, request logs, and returns non-success upstream status as errors. This aligns with Kiro-Go returning retryable error envelopes instead of successful assistant messages for capacity failure.

## Docker Health

- `kiro-go-kiro-go-1`: rebuilt from latest local code and healthy, started `2026-05-21T15:33:26Z`.
- `sub2api`: healthy on `127.0.0.1:18080`.
- `sub2api-postgres`: healthy.
- `sub2api-redis`: healthy.

Evidence:

- `logs/docker-ps.txt`
- `logs/kiro-container-state.json`
- `api/kiro-status.summary.json`
- `api/kiro-readiness.summary.json`

## API Results

Kiro-Go readiness:

- `claude-opus-4-7` mapped/listed by gateway.
- `routingReason`: `schedulable accounts available`.
- `accountCount`: 21.
- `schedulableCount`: 21.

sub2api Anthropic non-stream:

- HTTP `200`.
- model `claude-opus-4.7`.
- text `ok`.
- no error envelope.

sub2api Anthropic stream:

- HTTP `200`.
- events: `ping`, `message_start`, `content_block_delta`, `message_stop`.
- text `ok`.
- no `event: error`.
- no internal fallback marker.

Evidence:

- `api/sub2api-nonstream.summary.json`
- `api/sub2api-stream.summary.json`
- `api/kiro-claude-code-model-readiness-opus47.json`
- `api/kiro-fleet-readiness-opus47.json`
- `api/kiro-request-logs.json`

## Database Results

sub2api Postgres confirms the current UAT calls reached the intended Claude group and Kiro account:

- `usage_logs.id=91892`: `api_key_id=2`, `account_id=24`, `group_id=1`, model `claude-opus-4-7`, non-stream, output tokens `1`.
- `usage_logs.id=91893`: `api_key_id=2`, `account_id=24`, `group_id=1`, model `claude-opus-4-7`, stream, output tokens `1`.
- `usage_logs.id=91922`: Claude Code CLI request, `api_key_id=2`, `account_id=24`, `group_id=1`, model `claude-opus-4-7`, stream, user agent `claude-cli/2.1.143`, output tokens `1`.
- No `ops_error_logs` rows were created during this run because current live calls succeeded.

Evidence:

- `db/sub2api-opus47-usage-for-run.pretty.json`
- `db/sub2api-evidence.pretty.json`

## Claude Code Result

Local Claude Code 2.1.143 was run against sub2api:

- `ANTHROPIC_BASE_URL=http://127.0.0.1:18080`
- `ANTHROPIC_MODEL=claude-opus-4-7`
- `ANTHROPIC_AUTH_TOKEN` from sub2api api key id 2, not persisted in evidence.

Result:

- exit code `0`.
- parsed JSON result.
- `result`: `ok`.
- no `overloaded_error` in this run because current capacity was available.

Evidence:

- `claude/claude-code.summary.json`
- `claude/claude-code.raw`
- `claude/claude-code.stderr`

## Browser / Screenshot Results

Playwright-MCP real browser checks:

- Kiro-Go admin accounts page loaded, status running, version `v1.0.8`, 21 accounts visible.
- Kiro-Go API page loaded, Claude Code compatibility panel visible, Opus 4.7 fleet health shows healthy/routable state.
- sub2api dashboard loaded after login, metrics visible.
- sub2api accounts page loaded, accounts table visible.
- sub2api groups page loaded and shows `openai`, `google`, `claude`; `claude` group active.
- sub2api usage page loaded and shows model distribution including `claude-opus-4-7`.
- Browser console captured 0 errors and 0 warnings.

Evidence:

- `screenshots/kiro-go-sub2api-overload-20260521233518-kiro-accounts.png`
- `screenshots/kiro-go-sub2api-overload-20260521233518-kiro-api.png`
- `screenshots/kiro-go-sub2api-overload-20260521233518-sub2api-dashboard.png`
- `screenshots/kiro-go-sub2api-overload-20260521233518-sub2api-accounts.png`
- `screenshots/kiro-go-sub2api-overload-20260521233518-sub2api-groups.png`
- `screenshots/kiro-go-sub2api-overload-20260521233518-sub2api-usage.png`
- `logs/kiro-go-sub2api-overload-20260521233518-browser-console.log`

## Code Verification

Fresh verification commands passed:

- `go test ./proxy`
- `go test ./...`
- `git diff --check`

The fallback regression tests assert:

- Stable Claude non-stream fallback returns HTTP `529` with Anthropic `overloaded_error`.
- Stable Claude stream fallback emits SSE `event: error` / `overloaded_error`.
- Fallback no longer emits assistant content or `This turn has been closed by the gateway`.

