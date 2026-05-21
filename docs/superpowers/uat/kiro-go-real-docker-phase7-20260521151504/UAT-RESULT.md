# Kiro-Go Real Docker Phase 7 UAT Result

Run ID: `kiro-go-real-docker-phase7-20260521151504`

Time: `2026-05-21 15:34 +08:00`

Verdict: **SMOKE PASS / FULL 100x100 GENERATION UAT FAIL**

## Scope

This run started the latest local code in the real Docker service, checked service health, used Playwright-MCP with the real Chromium browser against `http://127.0.0.1:8080/admin`, collected browser screenshots, collected same-origin admin API responses through the logged-in browser session, and inspected non-secret container/storage evidence.

Follow-up repair verification added a direct Kiro-Go Opus 4.7 non-stream smoke request and a direct Kiro-Go Opus 4.7 stream smoke request. Both returned real content. This fixes the earlier "current request log is empty" failure at smoke scope.

The run did not read `data/config.json`, `.env`, token stores, key files, credential stores, browser session files, or recovery snapshots. Raw settings and aggregate browser API artifacts were removed after detecting `apiKey`-like fields; only redacted/key-summary artifacts remain.

## Environment Evidence

| Check | Evidence | Result |
| --- | --- | --- |
| Docker compose service running | `api/docker-compose-ps.json` | PASS: `kiro-go-kiro-go-1` running, port `8080:8080`, Docker health `healthy` |
| App health endpoint | `api/health.json`, `headers/health.headers` | PASS: HTTP 200, `{"status":"ok","version":"1.0.8"}` |
| Runtime logs | `logs/kiro-go-tail-after-playwright.log` | PASS: service started, admin/API endpoints printed, model cache loaded 13 models, health check `success=25 failed=0` |
| Kiro CLI in container | `api/kiro-cli-version.txt`, `api/kiro-cli_diagnostics.json` | PASS: `/usr/local/bin/kiro-cli`, `kiro-cli 2.4.0`, diagnostics read-only |

## Browser / Screenshot Analysis

| Screenshot | Analysis | Result |
| --- | --- | --- |
| `screenshots/kiro-admin-accounts.png` | Admin Accounts page is loaded in real browser. Top counters show `25` accounts, `16144` requests, `6718` successes, `9426` failures, `222.7M` tokens, `1959.6` credits. Account rows are visible with masked emails and enabled/healthy/listed/schedulable controls. | PASS for admin accounts UI visibility and account inventory display |
| `screenshots/kiro-admin-api.png` | API tab is loaded in real browser. Claude Code contract cards render, Kiro CLI diagnostics render with availability `available` and version `kiro-cli 2.4.0`, Opus 4.7 fleet health renders as `degraded`, safe concurrency `10 / 10`, retry after `0s`, reason `healthy`, and rows show eligible accounts. Endpoint cards render for Claude/OpenAI/models/stats. | PASS for admin API observability UI |
| `screenshots/kiro-admin-api-after-smoke.png` | API tab after smoke generation shows updated counters: requests increased to `16153`, successes to `6721`, failures to `9432`, credits to `1959.7`; Opus 4.7 fleet health remains rendered with `degraded`, safe concurrency `10 / 10`, and real content counters visible. | PASS for post-smoke browser evidence |
| `browser/console-summary.json` | Browser console had no captured app errors or warnings; only Chromium verbose DOM password-field advisory messages. | PASS |

Screenshot correctness conclusion: screenshots are correct for admin UI, diagnostics, fleet readiness observability, and post-smoke counter movement. Generation success is proven by API/request-log evidence, not by screenshot alone.

## API Evidence Analysis

| API | Evidence | Result |
| --- | --- | --- |
| `/admin/api/status` | `api/status.json` | PASS: `accounts=25`, `available=25`, request counters populated |
| `/admin/api/fleet/readiness?model=claude-opus-4-7` | `api/fleet_readiness_model_claude-opus-4-7.json` | PASS for readiness visibility: `status=degraded`, `safeConcurrency=10`, `retryAfterSeconds=0`, `reasonCodes=["healthy"]`, `enabledAccounts=25`, `locallySchedulableAccounts=25`, circuit closed |
| `/admin/api/claude-code/model-readiness?model=claude-opus-4-7` | `api/claude-code_model-readiness_model_claude-opus-4-7.json` | PASS for scheduler/readiness summary: 25 accounts evaluated, 25 locally schedulable, 0 generation blocked |
| `/admin/api/kiro-cli/diagnostics` | `api/kiro-cli_diagnostics.json` | PASS: CLI available, version command succeeded, output redacted flag true, read-only true |
| `/admin/api/request-logs?limit=50` | `api/request-logs_limit_50.json` | FAIL for generation evidence: logs array is empty |
| `/admin/api/request-stats` | `api/request-stats.json` | FAIL for current-run generation evidence: `total=0`, `success=0`, `failed=0`, no endpoint/model stats |
| `/admin/api/acceptance/evidence?model=claude-opus-4-7` | `api/acceptance_evidence_model_claude-opus-4-7.json` | FAIL for full acceptance: embedded request log evidence reports `recentOpus47Logs=0`, `contentSuccessCount=0`, `verdict=no_recent_generation_evidence` |

## Smoke Repair Evidence

| Check | Evidence | Result |
| --- | --- | --- |
| Direct Kiro-Go non-stream Opus 4.7 | `api/smoke-generation-results.json` | PASS: HTTP 200, response model `claude-opus-4.7`, output preview `KIRO_GO_UAT_OK`, usage `input_tokens=5916`, `output_tokens=5` |
| Direct Kiro-Go stream Opus 4.7 | `api/smoke-generation-results.json` | PASS: HTTP 200, `text/event-stream`, 6 SSE event blocks, output preview `KIRO_GO_UAT_OK`, no error event |
| Post-smoke request stats | `api/request-stats_after_smoke.json` | PASS smoke: `total=3`, `success=3`, `failed=0`, model `claude-opus-4.7`, output tokens `40` |
| Post-smoke request logs | `api/request-logs_limit_20_after_smoke.json` | PASS smoke: recent Opus 4.7 logs exist, include stream and non-stream success, account selected, attempt traces present |
| Post-smoke acceptance evidence | `api/acceptance_evidence_model_claude-opus-4-7_after_smoke.json` | PASS smoke: `recentOpus47Logs=3`, `contentSuccessCount=3`, `stableFallbackCount=0`, latest required coverage fields all true, verdict `latest_real_content_success` |

## Storage / Database Evidence

The Kiro-Go compose service itself has no separate database container in this run.

The runtime persistent store is mounted under `/app/data`, but configuration and credential-bearing files are secret-protected for this UAT. This run intentionally did not read `/app/data/config.json` or token/key/credential/session/recovery paths. Non-secret storage evidence is limited to file/service inventory:

- `db/compose-services.txt`
- `db/container-files.txt`
- `db/data-nonsecret-files.txt`
- `db/data-nonsecret-inventory.txt`

Result: **PASS LIMITED** for Kiro-Go non-secret storage inventory and secret-boundary compliance.

Additional sub2api/database evidence was collected after discovering local `sub2api`, `sub2api-postgres`, and `sub2api-redis` containers:

| Check | Evidence | Result |
| --- | --- | --- |
| sub2api health | `sub2api/health-summary.json` | PASS: `http://127.0.0.1:18080/health` returned `{"status":"ok"}` |
| sub2api PostgreSQL read-only access | `sub2api/usage-logs-recent.json`, `sub2api/ops-errors-recent.json`, `sub2api/account-scheduling-summary.json` | PASS: read-only SQL succeeded via `sub2api-postgres`; no secret tables or credential columns were selected |
| sub2api usage logs | `sub2api/usage-logs-recent.json` | PASS limited: recent `claude-opus-4-7` usage rows exist with request/account/channel/model/token/duration fields |
| sub2api ops errors | `sub2api/ops-errors-recent.json` | PASS limited: query returned an empty array for recent Opus/message errors |
| sub2api account scheduling | `sub2api/account-scheduling-summary.json` | PASS limited: active account scheduling summary and recent non-sensitive scheduling fields were exported |

Important boundary: the smoke requests in this repair run were sent directly to Kiro-Go `/v1/messages`, not through sub2api. Therefore the sub2api DB evidence proves sub2api and its database are healthy and have recent Opus usage/scheduling state, but it does not prove the two smoke request IDs traversed sub2api.

## Acceptance Verdict

Partial PASS:

- Latest local Docker service started successfully.
- Docker health and `/health` are healthy.
- Real Playwright-MCP browser loaded admin Accounts and API pages.
- Screenshots are visually correct for account inventory, CLI diagnostics, Claude Code observability, and Opus 4.7 fleet readiness.
- Browser same-origin admin APIs returned valid non-401 responses after using the page login session.
- Kiro CLI diagnostics show `kiro-cli 2.4.0`.
- Secret evidence boundary was enforced and raw settings evidence was replaced with redacted summaries.

Smoke generation PASS:

- Direct non-stream Opus 4.7 request returned HTTP 200 and real content `KIRO_GO_UAT_OK`.
- Direct stream Opus 4.7 request returned HTTP 200, SSE events, and real content `KIRO_GO_UAT_OK`.
- Post-smoke request stats show 3/3 successful Opus 4.7 requests.
- Post-smoke acceptance evidence shows `recentOpus47Logs=3`, `contentSuccessCount=3`, `stableFallbackCount=0`, and all latest required coverage fields true.

Remaining full-generation UAT gap:

- This is not a 100/100 non-stream plus 100/100 stream run.
- sub2api database evidence is not correlated to the direct Kiro-Go smoke request IDs because the smoke path did not go through sub2api.
- The original Phase 7 full acceptance rule still requires high-volume 100/100 evidence and correlated sub2api usage/scheduling evidence for that path.

Final verdict: **mark Docker health, admin UI, diagnostics, readiness observability, direct Kiro-Go Opus 4.7 stream/non-stream smoke, and limited sub2api DB health/evidence as PASS. Do not mark full Phase 7 / Opus 4.7 100x100 generation acceptance as PASS.**
