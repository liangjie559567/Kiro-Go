# UAT Result: Kiro-Go Docker + Playwright-MCP

Date: 2026-05-21 10:22-10:27 Asia/Shanghai

Verdict: PARTIAL PASS / DEGRADED

## Scope

- Service: `/www/Kiro-Go`
- Deployment: Docker Compose service `kiro-go`
- Base URL: `http://127.0.0.1:8080`
- Browser verification: Playwright-MCP real browser session
- Persistent data store: mounted `./data/config.json`
- Code under test: current working tree, including uncommitted `proxy/kiro.go` first-upstream-event timeout fix

## Deployment Evidence

PASS: latest local code was rebuilt and the container was recreated.

Evidence:

- `api/docker-compose-ps-final.txt`
- `api/docker-health.json`
- `api/docker-inspect-summary.txt`
- `api/health-final.json`

Final health:

```json
{"status":"ok","uptime":300,"version":"1.0.8"}
```

Docker status at final check: `Up 5 minutes (healthy)`.

## API Evidence

PASS: basic service APIs are reachable.

- `GET /health`: HTTP 200, `status=ok`
- `GET /admin/api/status`: HTTP 200, `accountCount=27`
- `GET /v1/models`: HTTP 200, model list returned
- `POST /v1/messages/count_tokens`: HTTP 200, estimated token count returned

Evidence files:

- `api/health.json`
- `api/status.json`
- `api/models-response.txt`
- `api/count-tokens-response.txt`
- `api/readiness.json`
- `api/compat.json`

## Real Upstream Smoke

PASS: live generation works for the smoke model.

Non-streaming `/v1/messages`:

- Model: `claude-haiku-4.5`
- HTTP: 200
- Duration: 1.558142s
- Expected marker present: `KIRO_GO_UAT_SMOKE_20260521`
- Evidence: `api/messages-nonstream-smoke.txt`

Streaming `/v1/messages`:

- Model: `claude-haiku-4.5`
- HTTP: 200
- Duration: 1.244244s
- Expected marker present: `KIRO_GO_UAT_STREAM_20260521`
- SSE contained `message_start`, `content_block_delta`, and `message_stop`
- Evidence: `api/messages-stream-smoke.sse`

Request-log evidence after smoke:

- `claude-haiku-4.5` non-stream: HTTP 200, `contentSuccess=true`, `durationMs=1555`
- `claude-haiku-4.5` stream: HTTP 200, `contentSuccess=true`, `durationMs=1243`
- Evidence: `api/request-logs-after-smoke.json`

## Persistent Data / Database Evidence

PASS for persistence wiring: Docker uses `./data:/app/data`, and the running service loaded the mounted config.

Config summary:

- Accounts configured: 27
- Enabled accounts: 27
- API key required: true
- Stable downstream enabled for `claude-opus-4.7`
- Content continuity enabled for `claude-opus-4.7`
- Model admission configured for Opus 4.7 and Sonnet 4.5
- Load balancing strategy: `health`

Evidence:

- `api/config-summary.json`
- `api/database-config-evidence.json`

Note: this app uses JSON persistence rather than a separate SQL database for its own state. No external SQL database was required for this Kiro-Go-only UAT.

## Playwright-MCP Frontend Evidence

PASS: real browser login and major admin pages render correctly.

Verified flows:

- Opened `/admin`, saw login page.
- Logged in with configured admin password.
- Accounts page rendered 27 accounts and top-level stats.
- API page rendered Claude Code readiness, model readiness, Opus 4.7 fleet health, WebSearch/MCP section, and API endpoint cards.
- Settings page rendered API key settings, refresh/health settings, endpoint/load-balance settings, and recent request logs.

Screenshots:

- `screenshots/kiro-admin-accounts.png`
- `screenshots/kiro-admin-api.png`
- `screenshots/kiro-admin-settings-logs.png`

Screenshot analysis:

- `kiro-admin-accounts.png`: PASS. Page is not blank, shows `27` accounts and account cards consistent with API status.
- `kiro-admin-api.png`: PASS for rendering and correctness. It shows Claude Code capability cards and Opus fleet status. It also correctly exposes `Status: degraded`, `Circuit: half_open`, `Safe concurrency: 1`, `Cooling: 3`, `Temporary limited: 3`.
- `kiro-admin-settings-logs.png`: PASS for rendering and workflow visibility. It shows API key validation enabled and recent logs including smoke and Opus entries.

## Opus 4.7 Fleet Evidence

DEGRADED, not PASS for high-capacity Opus 4.7.

Fleet summary:

```json
{
  "status": "degraded",
  "safeConcurrency": 1,
  "circuitState": "half_open",
  "enabledAccounts": 27,
  "locallySchedulableAccounts": 20,
  "coolingDownAccounts": 3,
  "temporaryLimitedAccounts": 3,
  "lastPressureReason": "rate_limited_or_model_capacity"
}
```

Evidence:

- `api/fleet.json`
- `api/fleet-summary.json`
- `api/opus_readiness.json`
- `api/admission.json`
- `logs/kiro-go-since-10m.log`

Log evidence includes real upstream pressure:

- Multiple account temporary-limit 429s.
- Multiple Opus 4.7 `INSUFFICIENT_MODEL_CAPACITY` 429s.
- No `upstream first event timeout` log observed during this UAT window.

Interpretation:

- The service is healthy and can serve real generation.
- Opus 4.7 is currently under upstream pressure and should be treated as degraded.
- The UI and API correctly surface this degraded state, so the screenshot result is correct.
- This UAT cannot honestly mark Opus 4.7 high-concurrency readiness as PASS.

## Final Verdict

- Docker deployment health: PASS
- Admin frontend rendering/login/API pages: PASS
- Basic API endpoints: PASS
- Real upstream haiku stream and non-stream smoke: PASS
- Request log evidence for smoke: PASS
- Opus 4.7 fleet capacity: DEGRADED
- Overall: PARTIAL PASS / DEGRADED

Do not mark this as full PASS until Opus 4.7 fleet returns healthy or a separate UAT scope explicitly excludes Opus 4.7 capacity.
