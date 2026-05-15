# Kiro-Go Gateway Compatibility UAT - 2026-05-16

## Scope

- Commit under test: `785e087` (`Improve gateway compatibility and routing controls`).
- Runtime: Docker production service on `127.0.0.1:8080`, container rebuilt with `docker compose up -d --build kiro-go`.
- Evidence policy after operator instruction: direct production Docker/API/database checks only. Temporary proxy or sandbox-style browser evidence is excluded from pass criteria.
- Raw sanitized evidence: `docs/superpowers/uat/2026-05-16-gateway-evidence.json`.

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
| Playwright direct page flow and screenshot | Direct Playwright navigation to `http://127.0.0.1:8080/admin` and `https://kiro.cgtall.com/admin` was blocked by browser environment with `net::ERR_BLOCKED_BY_CLIENT`. No proxy/sandbox screenshot is counted. | BLOCKED |

## Screenshot Analysis

No PASS is assigned for screenshot-based UI validation. Direct Playwright access to the real production origin was blocked by the MCP browser environment. Per operator instruction, proxy/sandbox screenshots are excluded from the UAT pass criteria.

The frontend was still checked through direct production HTML/API evidence: the deployed `/admin` HTML contains the settings and request-log controls, and the backing admin APIs returned the expected live data.

## Notes

- Secrets were not recorded. Evidence only stores booleans and redacted metadata for API key/admin password presence.
- One prior temporary verification proxy was stopped and its screenshots were removed from the repository workspace. It is not used as pass evidence.
