# 2026-05-16 sub2api -> Kiro-Go 100x10 Production UAT

## Scope

- Environment: Docker production services on `127.0.0.1:18080` (sub2api) and `127.0.0.1:8080` (Kiro-Go).
- Route: sub2api `/v1/messages` -> Kiro-Go `/v1/messages` -> Kiro upstream.
- Model: `claude-opus-4-7`.
- Load: 100 requests at concurrency 10 for non-streaming and streaming.
- Content check: each request used a unique marker and was counted correct only when the response contained exactly/semantically the expected marker.

## Result Summary

| Mode | Requests | Concurrency | HTTP 200 | Correct content | Failures | Total duration | Min | Avg | P50 | P90 | P95 | P99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Non-stream | 100 | 10 | 99 | 99 | 1 | 38.582s | 1064ms | 3491.01ms | 1573ms | 7536ms | 8512ms | 11468ms | 30094ms |
| Stream | 100 | 10 | 100 | 99 | 1 | 41.889s | 1036ms | 3764.94ms | 1582ms | 8801ms | 11012ms | 14162ms | 30091ms |

## Failed Requests

| Mode | Request ID | HTTP status | Latency | Error |
| --- | --- | ---: | ---: | --- |
| Non-stream | `f0c1eda1-7373-4ef1-a0a7-be035be0fc7c` | 429 | 30094ms | `Concurrency limit exceeded for user, please retry later` |
| Stream | `be77b6fb-6ef1-4b54-b189-e3aeb599233e` | 200 SSE error event | 30091ms | `Concurrency limit exceeded for user, please retry later` |

## Root Cause Evidence

- sub2api access logs matched all 200 generated request IDs: 99 non-stream 200, 1 non-stream 429, 100 stream 200.
- Both failed request IDs logged `gateway.user_slot_acquire_failed` with `error="timeout waiting for user concurrency slot"`.
- The failed log entries do not contain `account_id`, which means the requests failed before selecting `kiro_claude_01` and before reaching Kiro-Go.
- Kiro-Go container logs in the pressure-test window had no `429`, `500`, `503`, `timeout`, `INTERNAL_ERROR`, or queue/concurrency errors.
- sub2api user `admin@sub2api.local` has `concurrency=10`; account `kiro_claude_01` also has `concurrency=10`. Running a 10-concurrent test while other live production calls share the same sub2api user can occupy one or more slots and cause the 30s queue timeout.

## Database Evidence

- `users`: `admin@sub2api.local`, `concurrency=10`, `status=active`, `rpm_limit=0`.
- `accounts`: `kiro_claude_01`, `platform=anthropic`, `type=apikey`, `concurrency=10`, `status=active`, `schedulable=true`.
- `api_keys`: `claude`, `group_id=1`, `status=active`, rate limits `0`.
- `groups`: `claude`, `platform=anthropic`, `status=active`, `rpm_limit=0`.
- `usage_logs` for `account_id=24`, model `claude-opus-4-7`, `/v1/messages`, `2026-05-16 02:55:00+08` to `02:58:00+08`:
  - non-stream: 99 rows, max `11453ms`, avg `3164.65ms`, output tokens `1769`.
  - stream: 100 rows, max `51502ms`, avg `3889.85ms`, output tokens `1997`.
- `usage_logs.request_id` is not the same ID as the gateway access-log request ID, so request-ID level DB join is not available from this table.

## Frontend/API/Browser Evidence

- MCP browser transport failed with `Transport closed`; real browser validation was completed with installed Playwright package and system Chrome against the same production URLs.
- Kiro-Go admin page logged in successfully, API checks returned 200 for `/admin/api/status`, `/admin/api/settings`, `/admin/api/prompt-filter`, `/admin/api/request-stats`.
- Kiro-Go visible state: 12 accounts shown in admin UI, service `v1.0.8`, prompt filters enabled.
- sub2api admin auth returned 200 and produced access/refresh tokens; `/admin/accounts` loaded with no 4xx/5xx, no page errors, and zero console messages.
- sub2api visible state: `kiro_claude_01`, `Anthropic Key`, capacity `0/10`, `Active`, group `claude`.
- `/favicon.ico` on Kiro-Go returns HTTP 200 with `image/svg+xml`, so the previous favicon 404 is fixed.

## Screenshots And Artifacts

- `docs/superpowers/uat/2026-05-16-sub2api-sync-100x10-results.json`
- `docs/superpowers/uat/2026-05-16-sub2api-stream-100x10-results.json`
- `docs/superpowers/uat/2026-05-16-browser-evidence-100x10.json`
- `docs/superpowers/uat/2026-05-16-sub2api-auth-screenshot-evidence.json`
- `docs/superpowers/uat/kiro-admin-settings-100x10-20260516.png`
- `docs/superpowers/uat/kiro-admin-settings-tab-100x10-20260516.png`
- `docs/superpowers/uat/sub2api-dashboard-100x10-20260516.png`
- `docs/superpowers/uat/sub2api-accounts-auth-100x10-20260516.png`

## Post-Test Smoke

After the pressure test, one non-stream and one stream request through sub2api both returned HTTP 200 with correct unique marker content:

- non-stream: `1181ms`, correct.
- stream: `1265ms`, correct.

## Verdict

- Kiro-Go upstream availability through sub2api under this production test: **99% content-correct for both modes**.
- Requirement "10 concurrent, 100 requests, at least 99% correct": **PASS**.
- Requirement "all changes correct with no failures / max latency not over 30s": **PARTIAL / NOT FULL PASS** because two requests hit sub2api user-concurrency queue timeout at about 30s.
- Root cause is not Kiro-Go request translation or upstream Kiro response quality. It is sub2api user-level concurrency admission under shared production traffic.

## Recommended Next Step

For a strict 100/100 at concurrency 10 while production traffic is active, either isolate the test with a dedicated sub2api user/API key and `concurrency >= 10`, or raise the shared sub2api user concurrency above the test concurrency, for example 12-15, while keeping `kiro_claude_01` account concurrency at the desired upstream limit.
