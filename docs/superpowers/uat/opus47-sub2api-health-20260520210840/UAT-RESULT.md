# UAT Result: Opus 4.7 sub2api Health Contract

Date: 2026-05-20 Asia/Shanghai

## Verdict
PASS with deployment-window caveat.

## Evidence
- Go tests: `go test ./... -count=1` passed.
- Docker: `kiro-go-kiro-go-1` healthy; `sub2api` healthy; no Docker volumes deleted.
- MCP config: `~/.codex/config.toml` contains `@playwright/mcp@0.0.73`; `npx -y @playwright/mcp@0.0.73 --version` returned `Version 0.0.73`.
- Real browser screenshots:
  - `screenshots/kiro-admin.png`: Kiro-Go admin login rendered correctly.
  - `screenshots/sub2api-admin.png`: sub2api admin login rendered correctly.
- sub2api DB, recent 5 minutes:
  - Opus 4.7 success rows: 38.
  - Opus 4.7 recent 429 error rows: 0.
  - Kiro Claude accounts: 5 active/schedulable of 5.
- Kiro-Go admin fleet readiness: multiple eligible accounts list `claude-opus-4.7`.

## Caveat
During `docker compose up -d --build`, sub2api issued requests while the Kiro-Go container was being recreated and logged transient 502 connection-refused errors. After Kiro-Go became healthy, subsequent Opus 4.7 sub2api requests completed with HTTP 200 in logs. This is a deployment handoff issue, not a 429 propagation issue.

## PASS Criteria Check
- 429 cannot reach sub2api for Opus 4.7 pressure paths: PASS by unit tests and recent DB error count.
- Services healthy: PASS.
- Browser screenshots valid: PASS.
- Screenshot analysis: both pages rendered expected login UIs without blank screen or layout failure: PASS.
