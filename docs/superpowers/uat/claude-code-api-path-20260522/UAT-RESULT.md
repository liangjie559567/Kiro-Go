# Claude Code API Path UAT - 2026-05-22

## Scope

Validate the real path:

Claude Code CLI -> sub2api `:18080` -> Kiro-Go `:8080` -> Kiro upstream.

Focus: the Claude Code API bug where Opus 4.7 upstream capacity/admission pressure could surface as `529/503 overloaded_error`, `cooling_down`, or endless retry behavior during a conversation.

## Fix Under Test

Kiro-Go now returns a normal non-streaming Claude `message` assistant turn with HTTP 200 for stable downstream fallbacks, instead of returning a retryable Anthropic error envelope. This is intended to stop sub2api and Claude Code from treating Kiro-Go's bounded fallback as another upstream failure.

Streaming fallback still uses SSE semantics because a stream may already have started.

## Evidence

- Unit/regression: `go test ./proxy` passed.
- Full Go suite: `go test ./...` passed.
- Docker: `docker compose up -d --build kiro-go`, container healthy.
- API health: `GET /health` returned `{"status":"ok","version":"1.0.8"}`.
- Playwright-MCP: admin page loaded and showed service `运行中`; screenshot: `browser/kiro-go-admin-readiness-20260521-1755.png`.
- Real Claude Code CLI smoke, 2 agents: `api/summary-2-agents.json`, `db/db-evidence-2-agents.json`.
- Real Claude Code CLI pressure, 4 agents: `api/summary-4-agents.json`, `api/claude-results-4-agents.json`, `db/db-evidence-4-agents.json`.
- Logs: `logs/kiro-go-175330-175440.log`, `logs/sub2api-claude-path-175330-175440.log`.

## Results

2-agent smoke: PASS

- `claudeApiOk=2/2`
- `markerOk=2/2`
- `overloaded=0`
- sub2api DB `usageRows=2`
- sub2api DB `errorRows=0`
- readiness started healthy and ended degraded with safe concurrency still available.

4-agent pressure: API bug PASS, capacity state PARTIAL

- `claudeApiOk=4/4`
- `markerOk=4/4`
- `overloaded=0`
- sub2api DB `errorRows=0`
- sub2api access logs show `/v1/messages` status 200 for Claude Code requests.
- Kiro-Go logs still show real Kiro upstream temporary-limit 429 events.
- Readiness ended `blocked` / `admission_circuit_open` with `safeConcurrency=0`, so full readiness is not PASS under that live upstream pressure.

## Conclusion

PASS for the Claude Code API bug: after the fix, real Claude Code calls through sub2api did not receive capacity/cooling/overloaded API errors, and sub2api did not record new `ops_error_logs` rows for those Claude Code requests.

PARTIAL for capacity readiness under aggressive concurrent load: the upstream Kiro service still temporary-limits real accounts, causing Kiro-Go readiness to degrade or briefly block. That is real upstream/account pressure, not the fixed client-facing API error path.
