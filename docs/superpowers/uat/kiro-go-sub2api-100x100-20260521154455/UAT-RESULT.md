# Kiro-Go sub2api 100x100 UAT Result

Run directory: `docs/superpowers/uat/kiro-go-sub2api-100x100-20260521154455`

Checked at: 2026-05-21 15:54 Asia/Shanghai

## Verdict

**FAIL / NOT PASS**

This run cannot be marked PASS. The sub2api path precheck passed, but the required `100/100` non-stream batch failed before completion. The `100/100` stream batch was not run because the non-stream gate had already failed and the run was aborted to avoid further upstream load.

## Scope

- Route: client -> sub2api `http://127.0.0.1:18080/v1/messages` -> Kiro-Go `http://kiro-go:8080/v1/messages`
- Model under test: `claude-opus-4-7` / response model `claude-opus-4.7`
- Required target: 100 successful non-stream requests and 100 successful streaming requests
- Browser validation: Playwright-MCP real browser, Kiro-Go admin page and sub2api frontend
- Supporting validation: API health, Kiro-Go admin APIs, sub2api PostgreSQL evidence, container logs

## Results

| Check | Result | Evidence |
| --- | --- | --- |
| Docker services healthy | PASS | `kiro-go-kiro-go-1`, `sub2api`, `sub2api-postgres`, `sub2api-redis` all reported healthy at final check |
| Kiro-Go health API | PASS | `api/kiro-health.json`: `status=ok`, `version=1.0.8` |
| sub2api health API | PASS | `api/sub2api-health.json`: `status=ok` |
| sub2api non-stream precheck | PASS | `api/precheck.json`: HTTP 200, response model `claude-opus-4.7`, marker present |
| sub2api stream precheck | PASS | `api/precheck.json`: HTTP 200, `text/event-stream`, `message_start` and `message_stop`, marker present |
| 100/100 non-stream pressure run | FAIL | `api/non-stream-partial-summary.json`: 23 completed records, 3 passed, 20 failed |
| 100/100 stream pressure run | NOT RUN | Non-stream gate failed first; run was aborted |
| Kiro-Go fleet readiness | FAIL evidence confirmed | `api/kiro-fleet_readiness_model_claude-opus-4-7.json`: `status=degraded`, `safeConcurrency=1`, `reasonCodes=["admission_pressure"]`, `lastPressureReason=rate_limited_or_model_capacity`, `circuitState=half_open` |
| sub2api DB correlation | PASS for failure diagnosis | `sub2api/db-evidence-after-abort.json`: 30 `upstream_error` rows with status 502 and 1 `rate_limit_error` with status 429 in the test window |
| Playwright screenshot analysis | PASS for failure evidence, not UAT pass | Kiro-Go screenshot shows counters and Opus 4.7 degraded/limited status; sub2api screenshot shows login page only |

## Failure Details

The pressure runner wrote 23 non-stream records before the run was stopped:

- Passed: 3
- Failed: 20
- Failure class: `AbortError`
- HTTP status recorded by runner: `0`
- Failed request duration: approximately 120000 ms, matching the request timeout

Representative artifact:

- `api/non-stream-partial-summary.json`
- `request-results/non-stream.jsonl`

Because the required acceptance criterion is `100/100` non-stream plus `100/100` stream success, a `3/23` partial non-stream result is an immediate UAT failure.

## API and Database Correlation

The failure is consistent across layers:

- Runner saw client-side aborts after 120s timeouts.
- Kiro-Go logs include repeated Opus 4.7 upstream `429` capacity responses with `INSUFFICIENT_MODEL_CAPACITY`.
- Kiro-Go fleet readiness dropped to `degraded` and `safeConcurrency=1`.
- sub2api database recorded:
  - 30 `upstream_error` entries with status `502` for `/v1/messages -> /v1/messages`.
  - 1 `rate_limit_error` entry with status `429` for `/v1/messages -> /v1/responses`.
- sub2api usage rows show only a small number of successful rows in the test window, not 100 successful non-stream plus 100 successful stream rows.

## Browser Evidence

Playwright-MCP was used against the live browser tabs:

- `http://127.0.0.1:8080/admin`
  - Screenshot: `screenshots/kiro-admin-api-100x100-fail.png`
  - Snapshot confirmed the Kiro-Go admin page is live, version `v1.0.8`, counters show `16162` requests, `6726` successes, `9436` failures.
  - Opus 4.7 fleet section shows degraded/limited state. This supports the failure diagnosis.
- `http://127.0.0.1:18080/login`
  - Screenshot: `screenshots/sub2api-login-100x100-fail.png`
  - Snapshot confirmed the sub2api frontend is reachable but only at the login page.
  - Without a logged-in frontend session, post-login sub2api page flows were not visually validated. This is not a PASS blocker by itself for the API pressure evidence, but it means frontend flow coverage remains incomplete.

## Final Decision

**Do not pass this UAT.**

The correct result is **FAIL / NOT PASS**. The screenshots, API responses, database rows, and logs are mutually consistent with upstream capacity/rate pressure and sub2api/Kiro-Go request failures during the 100x100 run.

## Follow-up Fix Applied

After researching `jwadow/kiro-gateway` and `zeoak9297/KiroSwitchManager`, the validation runner was updated to avoid repeating the same false-negative pressure pattern:

- `kiro-gateway` uses explicit retry/failover behavior for 403, 429, 5xx, timeout, multi-account failover, and first-token stream retry. The relevant lesson for this UAT is that validation should respect gateway capacity signals instead of blindly pushing high concurrency when the upstream model is already degraded.
- `KiroSwitchManager` focuses on multi-account switching and local proxy operation. The relevant lesson is the same operational constraint: account switching helps availability, but it does not turn model-level upstream capacity pressure into a guaranteed high-concurrency PASS.
- `run-sub2api-100x100.js` now reads Kiro-Go fleet readiness before the full run and writes `api/readiness-before-run.json`.
- Unless `FIXED_CONCURRENCY=1` is explicitly set, the runner now reduces `CONCURRENCY` to the observed `safeConcurrency` / `admissionEffectiveConcurrency`, with a minimum of 1.
- If readiness reports an open circuit with `retryAfterSeconds > 0`, the runner records `blocked_by_readiness` instead of starting a doomed 100x100 run.
- The runner supports `KIRO_ADMIN_PASSWORD` as an environment variable for readiness API access; the password is not written to artifacts.

This fixes the validation harness behavior, not the previous run result. The previous run remains **FAIL / NOT PASS** because its recorded evidence was `3/23` non-stream success and no stream 100/100 execution.
