# Kiro-Go Claude Code Admission Pressure UAT

Date: 2026-05-21 18:38 CST

## Verdict

- Code fix: PASS
- Docker health: PASS
- Playwright page render: PASS
- Real Opus 4.7 generation/load test: BLOCKED_BY_UPSTREAM_CAPACITY

## Scope

This UAT covers the Claude Code stable downstream bug where Kiro-Go emitted an assistant fallback message containing:

`Opus 4.7 upstream capacity is temporarily unavailable ... Retry reason: admission_pressure`

The fix prevents the Claude stream admission-pressure path from starting an assistant message, caps stable Opus 4.7 capacity waits, and keeps bounded fallback behavior for non-stream/transport fallback paths.

## Evidence

- Unit tests:
  - `go test ./proxy -run 'StableAdmissionPressure|StableClaudeStreamCapacity|StableClaudeNoAccounts|StableContentContinuity'`: PASS
  - `go test ./proxy -run TestEnsureValidTokenCoalescesConcurrentRefreshesPerAccount -count=10`: PASS
  - `go test ./pool ./proxy`: PASS
- Docker:
  - `logs/docker-compose-ps.txt`: container `kiro-go-kiro-go-1` is `healthy`
  - `api/health.json`: `{"status":"ok","version":"1.0.8"}`
- Playwright-MCP:
  - `screenshots/dashboard.png`: admin dashboard rendered, status shows running, stats and account list visible
  - `screenshots/api.png`: API tab rendered, Claude Code section visible
  - `api/playwright-api-summary.json`: admin APIs returned HTTP 200 for status, compat, readiness, model-readiness, fleet-readiness, request logs
  - `playwright/console.log`: one 404 came from an intentionally probed non-existent endpoint `/admin/api/claude-code/status`; the correct endpoints were verified afterward

## Screenshot Analysis

- Dashboard screenshot is nonblank and correctly framed.
- Header shows `Kiro-Go`, version `v1.0.8`, and `运行中`.
- Stats are visible: 24 accounts, request/success/failure/token/credit counters.
- Account list is visible and account email text is masked in the UI.
- API screenshot shows the `Claude Code` section, confirming the API tab is accessible after rebuild.

Screenshot result: PASS for page rendering and navigation.

## Readiness Analysis

Fleet readiness for `claude-opus-4-7` is currently:

- `status`: `blocked`
- `safeConcurrency`: `0`
- `reasonCodes`: `admission_circuit_open`, `cooling_down`, `model_breaker_open`

Because readiness is blocked with safe concurrency 0, this UAT did not run 100/100 stream and non-stream generation pressure. Running that load in this state would only create more upstream pressure and would not prove a correct fix.

## External Research Summary

- `jwadow/kiro-gateway` uses bounded retry patterns for 429/5xx/timeouts and separates first-token timeout retry from HTTP retry.
- `jwadow/kiro-gateway` supports proxy configuration, but that only proves proxy support as a deployment option, not that this Kiro-Go incident is caused by IP.
- `zeoak9297/KiroSwitchManager` documents account switching, token refresh, proxy UI, and stream heartbeat behavior, but the public repo does not provide source code, so its internal retry/failover logic cannot be verified.

## Redaction

The first Playwright API capture included environment key fields from `/admin/api/claude-code/compat`. The evidence file was redacted immediately. Current UAT evidence was scanned for API keys, password strings, access tokens, and refresh tokens.

## Final Status

The reported Claude Code `admission_pressure` assistant fallback text is fixed at the code-path level and covered by tests. Full real generation PASS is blocked by current upstream/model readiness, not by Docker or UI health.
