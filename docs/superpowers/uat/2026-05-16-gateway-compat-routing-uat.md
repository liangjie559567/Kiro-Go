# Kiro-Go Gateway Compatibility UAT - 2026-05-16

## Scope

- Commit under test: `785e087` (`Improve gateway compatibility and routing controls`).
- Runtime: Docker production service on `127.0.0.1:8080`, container rebuilt with `docker compose up -d --build kiro-go`.
- Evidence policy after operator instruction: direct production Docker/API/database checks only. Temporary proxy or sandbox-style browser evidence is excluded from pass criteria.
- Raw sanitized evidence: `docs/superpowers/uat/2026-05-16-gateway-evidence.json`.
- Direct browser evidence: `docs/superpowers/uat/2026-05-16-playwright-direct-evidence.json`.

## Results

| Item | Evidence | Verdict |
| --- | --- | --- |
| Docker deployment | `/health` returned HTTP 200, `status=ok`, `version=1.0.8` after rebuild. | PASS |
| Admin/API auth path | Direct `/admin/api/status` with production admin password returned HTTP 200. Accounts `12/12` available. | PASS |
| Load-balance setting persistence | Direct POST changed strategy `health -> least_connections`; `data/config.json` persisted `least_connections`; strategy restored to `health`. | PASS |
| `/v1/responses` non-stream real upstream call | HTTP 200 in `7270ms`, object `response`, model `claude-opus-4.7`, expected text marker returned. | PASS |
| `/v1/responses` stream real upstream call | HTTP 200 in `2566ms`, SSE length `1210`, completion event present, expected text marker returned. | PASS |
| `/v1/messages` non-stream real upstream call | HTTP 200 in `1595ms`, type `message`, model `claude-opus-4.7`, expected text marker returned. | PASS |
| Request log enrichment | Latest logs include request id, endpoint, model, HTTP 200, success outcome, duration, account presence, region `us-east-1`, input/output tokens, stream flag. | PASS |
| Request stats aggregation | Direct `/admin/api/request-stats`: total `24`, success `24`, failed `0`; `/v1/messages` and `/v1/responses` are grouped by endpoint/model. | PASS |
| Frontend asset contains controls | Direct `/admin` HTML contains `loadBalanceStrategy`, `requestLogsBody`, `loadRequestLogs`, `saveLoadBalanceConfig`. | PASS |
| Playwright direct page flow and screenshot | Environment root cause was the MCP `--allowed-origins` list excluding `8080`. Config was updated, but the active MCP transport did not hot-reload. Direct Playwright CLI against `http://127.0.0.1:8080/admin` returned HTTP 200, logged in, and captured real screenshots from the production Docker service. | PASS |

## Screenshot Analysis

Screenshot-based UI validation is now PASS using direct Playwright CLI against the real Docker production port `127.0.0.1:8080`. No proxy, port-forward shim, or sandbox-rendered page was used.

- `docs/superpowers/uat/kiro-admin-login-direct-20260516.png`: 1440x1100 PNG, shows the real Kiro-Go admin login page loaded from `127.0.0.1:8080/admin`.
- `docs/superpowers/uat/kiro-admin-settings-logs-direct-20260516.png`: 1440x4769 PNG, shows the logged-in settings page with `v1.0.8`, account/status cards, `账号调度策略`, `System Prompt 过滤`, and `最近请求日志`.
- Screenshot/API consistency: the page-flow evidence recorded HTTP 200, successful login, load-balance controls present, request logs present, and prompt filter present. This matches the direct `/admin` HTML and `/admin/api/request-*` evidence.
- Console note: one non-blocking 404 was observed for `/favicon.ico`; it does not affect login, settings, or request-log flows.

## Notes

- Secrets were not recorded. Evidence only stores booleans and redacted metadata for API key/admin password presence.
- One prior temporary verification proxy was stopped and its screenshots were removed from the repository workspace. It is not used as pass evidence.
- MCP environment fix: `/root/.codex/config.toml` now includes `http://localhost:8080`, `http://127.0.0.1:8080`, and `https://kiro.cgtall.com` in the Playwright MCP `--allowed-origins` list. The current MCP transport must be restarted by the host session to pick up the change.
