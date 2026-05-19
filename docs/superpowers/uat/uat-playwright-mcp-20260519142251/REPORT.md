# Playwright MCP Full-Stack UAT

Date: 2026-05-19
Verdict: FAIL

## Scope

Validate Kiro-Go after the Claude streaming compatibility fix with real browser screenshots, API calls, and sub2api PostgreSQL evidence.

## Evidence Summary

### Frontend Screenshots

PASS: Real Chromium/Playwright loaded both admin UIs without page errors, console errors, or failed localhost requests.

- `kiro-admin-dashboard.png`: Kiro-Go dashboard visible.
- `kiro-admin-api-readiness.png`: Claude Code/API readiness panel visible.
- `kiro-admin-request-logs.png`: Kiro-Go admin reachable after the failed API call.
- `sub2api-dashboard.png`: sub2api dashboard visible.
- `sub2api-accounts.png`: sub2api accounts page visible.
- `sub2api-groups.png`: sub2api groups page visible.
- `sub2api-usage.png`: sub2api usage page visible.

`playwright-summary.json` assertions all passed, and `console`, `pageErrors`, and `requestFailures` are empty.

### Kiro-Go Direct API

FAIL: Direct Kiro-Go `/v1/messages` did not reach the successful SSE path.

- Request marker: `uat-playwright-mcp-20260519142251-direct`
- Evidence: `direct-kiro-stream.headers`, `direct-kiro-stream.sse`, `direct-kiro-stream.summary.json`
- HTTP status: `429 Too Many Requests`
- Error type: `rate_limit_error`
- `Retry-After: 5`
- No SSE events were emitted.
- The requested marker was not present in the response.

Kiro-Go request logs confirm the same request id with `statusCode=429`, `outcome=error`.

### sub2api Downstream API

FAIL: sub2api `/v1/messages` did not route to a usable account.

- Request marker: `uat-playwright-mcp-20260519142251-sub2api`
- Evidence: `sub2api-stream.headers`, `sub2api-stream.sse`, `sub2api-stream.summary.json`, `sub2api-log-after-stream.txt`
- HTTP status: `503 Service Unavailable`
- Error body: `Service temporarily unavailable`
- sub2api log cause: `openai_messages.account_select_failed`, `error=no available accounts`
- No SSE events were emitted.
- The requested marker was not present in the response.

### Database Evidence

FAIL for end-to-end persistence: the marker request did not create a `usage_logs` row.

- `db-usage-marker-after.txt` is empty for the UAT marker request ids.
- `db-usage-after-api.txt` shows unrelated concurrent production traffic, so total usage count is not a valid pass signal.
- `db-accounts-shape.txt` shows the anthropic account row `kiro_claude_01` exists as active/schedulable in PostgreSQL, but sub2api runtime selection still returned `no available accounts`.
- `db-channel-groups.txt` shows `claude` and `openai` groups/channels exist.

## Screenshot Analysis

The screenshot assertions are correct for UI reachability only. They do not prove the Claude Code downstream API path is healthy.

Because both API probes failed and no marker usage row was written, the overall UAT cannot pass.

## Conclusion

Do not mark this UAT as passed.

The code-level regression tests pass (`go test ./...`), and the admin pages render correctly, but real full-stack API validation is blocked by runtime/account availability:

- Kiro-Go direct path: upstream Kiro account temporary limit / suspicious activity 429.
- sub2api path: downstream account selection failure before reaching Kiro-Go.

