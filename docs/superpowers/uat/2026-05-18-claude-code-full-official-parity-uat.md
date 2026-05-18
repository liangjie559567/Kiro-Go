# Claude Code Full Official Parity UAT

Date: 2026-05-18

Artifact directory: `docs/superpowers/uat/uat-full-official-parity-20260518235053`

## Commands

- `go test ./...`: PASS.
- `docker compose up -d --build kiro-go`: PASS; `kiro-go-kiro-go-1` was rebuilt and recreated.
- `curl http://127.0.0.1:8080/health`: PASS; returned `{"status":"ok"}`.
- `docker compose -f docker-compose.yml -f docker-compose.current.yml up -d --build sub2api`: PASS; app container rebuilt/restarted, Postgres and Redis remained healthy.
- `curl http://127.0.0.1:18080/health`: PASS; returned `{"status":"ok"}`.

## API Evidence

- Direct Kiro-Go `/v1/messages`: PASS. Authenticated smoke returned assistant text `UAT_FULL_PARITY_DIRECT`.
- sub2api non-stream `/v1/messages`: PASS. HTTP 200 with assistant text `UAT_FULL_PARITY_SUB2API_NONSTREAM`.
- sub2api stream `/v1/messages`: PASS. HTTP 200 SSE included `message_start`, text deltas for `UAT_FULL_PARITY_SUB2API_STREAM`, and `message_stop`.
- Kiro request logs: captured in `kiro-request-logs.json`.
- Kiro Claude readiness: PASS. `kiro-claude-readiness.json` shows `recentClaudeCode=true`, `recentParentAgents=true`, and `recentFineGrainedToolStreaming=true`.
- Kiro model matrix: PASS. `kiro-model-readiness.json` maps `claude-sonnet-4.5`, is listed by gateway, and reports `vision`, `toolUse`, `thinking`, and `webSearch`.

## Database Evidence

Pre-call counts:

- `users=3`
- `groups=4`
- `accounts=82`
- `api_keys=4`

Post-call counts:

- `users=3`
- `groups=4`
- `accounts=82`
- `api_keys=4`

Verdict: PASS. Core data counts stayed stable after sub2api rebuild and real calls.

## Browser Evidence

- `kiro-admin-login-or-dashboard.png`: nonblank 1440x1000 PNG showing the Kiro admin login/dashboard surface.
- `kiro-claude-readiness-json.png`: nonblank 1440x1000 PNG. Browser page was unauthenticated, so the screenshot corresponds to the protected JSON endpoint behavior; authenticated JSON artifact contains expected readiness fields.
- `kiro-model-readiness-json.png`: nonblank 1440x1000 PNG. Browser page was unauthenticated, so the screenshot corresponds to the protected JSON endpoint behavior; authenticated JSON artifact contains expected model matrix.
- `sub2api-health-json.png`: nonblank 1440x1000 PNG and health JSON returned `status=ok`.
- `sub2api-root.png`: nonblank 1440x1106 PNG showing sub2api web UI/login surface.

Playwright verdict: PARTIAL. The script exited 0 and wrote screenshots, but `playwright-summary.json` records expected 401 console errors from unauthenticated Kiro admin JSON page visits. Authenticated curl artifacts verified those endpoints separately.

## Verdict

PASS with one browser-note caveat. Backend Go verification, Docker rebuilds, direct Kiro-Go smoke, sub2api non-stream/stream smokes, database stability, and authenticated readiness/model-matrix evidence all passed. Playwright screenshots were generated successfully, with protected Kiro admin JSON endpoints validated by authenticated curl artifacts instead of unauthenticated browser navigation.
