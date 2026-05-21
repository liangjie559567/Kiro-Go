# Kiro-Go sub2api 100x100 Pressure UAT

Run directory: `docs/superpowers/uat/kiro-go-sub2api-full-100x100-postfix-20260521163818`

Checked at: 2026-05-21 16:49 Asia/Shanghai

## Verdict

**FAIL / NOT PASS**

The Docker service and admin UI were reachable, and the precheck passed for both non-stream and stream requests. The formal pressure gate failed before completion: the non-stream 100-request batch recorded only 13 result rows, with 3 successes and 10 aborts at the 180 second client timeout. Because the non-stream gate failed, the stream 100-request batch was not executed and must not be inferred as passing.

## Pass Gate

This UAT can only pass when all of the following are true:

- 100/100 non-stream requests through the sub2api path return HTTP 200, non-empty content, and the expected marker.
- 100/100 stream requests through the sub2api path return HTTP 200, complete SSE lifecycle, non-empty content, expected marker, and no replay after content started.
- Browser screenshot, API health/readiness, database evidence, and logs agree with the request result files.
- No screenshot-only or health-only result is promoted to PASS.

This run does not meet the gate.

## Execution Summary

| Step | Result | Evidence |
| --- | --- | --- |
| Kiro-Go Docker service | PASS for service health | `docker compose ps` showed `kiro-go-kiro-go-1` healthy after rebuild |
| Kiro-Go health before/after | PASS | `api/kiro-health-before.json`, `api/kiro-health-after.json`: `status=ok`, version `1.0.8` |
| sub2api health before/after | PASS | `api/sub2api-health-before.json`, `api/sub2api-health-after.json`: `status=ok` |
| Fleet readiness before run | PASS for starting condition | `api/readiness-before-run.json`: `status=degraded`, `circuitState=closed`, `safeConcurrency=10`, `reasonCodes=["healthy"]` |
| Non-stream precheck | PASS | `api/precheck.json`: HTTP 200, marker present, duration `124553ms` |
| Stream precheck | PASS | `api/precheck.json`: HTTP 200, `messageStart=true`, `messageStop=true`, marker present, duration `14297ms` |
| Formal non-stream 100/100 | FAIL | `api/non-stream-partial-summary.json`: total `13`, passed `3`, failed `10`, status counts `{ "0": 10, "200": 3 }` |
| Formal stream 100/100 | NOT RUN | Correctly blocked because the non-stream gate failed |
| Browser screenshot | PASS for UI reachability only | Screenshots show the real Kiro-Go admin page and post-run counters, but they do not override failed request results |
| DB evidence | FAIL-supporting evidence | `sub2api/db-evidence-after-abort.json`: 20 upstream `502` errors and 1 `429` rate-limit error in the run window |
| Logs | FAIL-supporting evidence | `logs/sub2api-tail-after-abort.log` includes upstream/gateway activity around the failed window; Kiro-Go log confirms service startup and health check completion |

## Request Results

The formal non-stream request file contains 13 rows:

- Passed: 3
- Failed: 10
- Failure type: `AbortError`
- Failure duration: about `180003ms` to `180008ms`
- HTTP status on failed rows: `0`, meaning the client-side request was aborted before a normal HTTP response was recorded

The passing rows show valid HTTP 200 responses with expected markers for indices `010`, `020`, and `021`. That proves partial functionality, not 100/100 stability.

## Browser Screenshot Analysis

Playwright-MCP captured before and after screenshots:

- `screenshots/kiro-go-full-100x100-postfix-before-202605211638.png`
- `screenshots/kiro-go-full-100x100-postfix-after-202605211646.png`

The after screenshot and browser snapshot show the real local admin page at `http://127.0.0.1:8080/admin`, page title `Kiro-Go`, status `运行中`, version `v1.0.8`, and visible counters:

- Accounts: 25
- Requests: 16182
- Successes: 6741
- Failures: 9441
- Tokens: 222.8M
- Credits: 1960.3

The screenshot is correct for UI reachability and confirms counters increased during testing. It is not sufficient for PASS because the request result file and DB evidence show the formal non-stream batch failed.

## API Evidence

Browser-side authenticated API capture is stored in `api/kiro-browser-api-after-sanitized.json`.

Observed after the aborted run:

- `/admin/api/status`: HTTP 200
- `/admin/api/stats`: `totalRequests=16183`, `successRequests=6742`, `failedRequests=9441`
- `/admin/api/fleet/readiness?model=claude-opus-4-7`: `status=degraded`, `circuitState=half_open`, `safeConcurrency=1`, `reasonCodes=["admission_pressure"]`, `lastPressureReason=queue_timeout`

This supports the FAIL verdict: the service remained alive, but fleet readiness degraded under pressure and safe concurrency dropped to 1 after the failed run.

## Database Evidence

`sub2api/db-evidence-after-abort.json` covers the window since `2026-05-21 16:38:00+08`.

Usage rows prove that traffic reached sub2api and Kiro-Go-compatible upstream endpoints:

- 4 non-stream rows for `/v1/messages -> /v1/messages`
- 1 stream precheck row for `/v1/messages -> /v1/messages`
- 2 rows for `/v1/messages -> /v1/responses`

Error rows support the failed non-stream run:

- 20 `upstream_error` rows with status `502`
- 1 `rate_limit_error` row with status `429`

The DB artifact is summary-level evidence, not a complete per-request ledger for all 100 planned non-stream and 100 planned stream requests.

## Known Gaps

- No stream batch result file exists because the formal stream 100/100 gate was not executed after the non-stream failure.
- DB evidence is aggregated by endpoint/model/status, not a complete row-by-row proof for each planned request.
- Screenshots prove the browser/UI state, but they cannot override the failed request result file.

## Final Decision

Do not mark this UAT as PASS. The correct result is:

- Service health: **PASS**
- Browser reachability: **PASS**
- Precheck: **PASS**
- Formal non-stream 100/100: **FAIL**
- Formal stream 100/100: **NOT RUN**
- Overall UAT: **FAIL / NOT PASS**

