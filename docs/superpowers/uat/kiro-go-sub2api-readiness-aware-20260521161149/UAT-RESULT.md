# Kiro-Go sub2api Readiness-Aware UAT Result

Run directory: `docs/superpowers/uat/kiro-go-sub2api-readiness-aware-20260521161149`

Checked at: 2026-05-21 16:20 Asia/Shanghai

## Verdict

**FAIL / NOT PASS**

This run cannot be marked PASS. The sub2api path precheck passed for both non-stream and streaming requests, but the readiness-aware non-stream batch failed before reaching the required `100/100` success target. The stream batch was not run after the non-stream gate failed.

## Scope

- Route: client -> sub2api `http://127.0.0.1:18080/v1/messages` -> Kiro-Go `http://kiro-go:8080/v1/messages`
- Model under test: `claude-opus-4-7` / response model `claude-opus-4.7`
- Required target: 100 successful non-stream requests and 100 successful streaming requests
- Browser validation: Playwright-MCP real browser against `https://kiro.cgtall.com/admin`
- Supporting validation: API health, Kiro-Go readiness API, sub2api PostgreSQL evidence, container logs, screenshot analysis

## Results

| Check | Result | Evidence |
| --- | --- | --- |
| Kiro-Go health API | PASS | `api/kiro-health.json`: `status=ok`, version `1.0.8` |
| sub2api health API | PASS | `api/sub2api-health.json`: `status=ok` |
| sub2api non-stream precheck | PASS | `api/precheck.json`: HTTP 200, response model `claude-opus-4.7`, marker present |
| sub2api stream precheck | PASS | `api/precheck.json`: HTTP 200, `text/event-stream`, `message_start` and `message_stop`, marker present |
| Runner readiness lookup | LIMITED | `api/readiness-before-run.json`: runner received 401 from Kiro-Go admin readiness API because no admin password was passed into the runner environment; it conservatively reduced effective concurrency to 1 |
| Real browser readiness lookup | FAIL evidence confirmed | `api/kiro-real-admin-browser-api-202605211620.json`: readiness API returned `status=degraded`, `circuitState=half_open`, `reasonCodes=["admission_pressure"]`, `lastPressureReason=rate_limited_or_model_capacity`, `safeConcurrency=2` at 16:20 |
| 100/100 non-stream pressure run | FAIL | `api/non-stream-partial-summary.json`: 7 completed records, 6 passed, 1 failed with `AbortError` after 180002 ms |
| 100/100 stream pressure run | NOT RUN | Non-stream gate failed first; stream pressure batch was intentionally skipped |
| sub2api DB correlation | PASS for failure diagnosis | `sub2api/db-evidence-after-abort.json`: since 16:12, DB recorded 7 non-stream `/v1/messages` usage rows, 1 stream precheck usage row, and 2 upstream 502 error rows for `/v1/messages` |
| Kiro-Go logs | PASS for failure diagnosis | `logs/kiro-go-tail-after-readiness-aware-abort.log`: repeated Opus 4.7 `INSUFFICIENT_MODEL_CAPACITY` 429 responses and quiet-mode skipped refreshes |
| sub2api logs | PASS for failure diagnosis | `logs/sub2api-tail-after-readiness-aware-abort.log`: upstream failure/context-canceled evidence and `/v1/messages` 502 rows during the failed window |
| Playwright screenshot analysis | PASS for evidence validity, not UAT pass | `screenshots/kiro-real-admin-current-readiness-202605211620.png`: screenshot is the live Kiro-Go admin page, not login/error page; visible counters show 25 accounts, 16173 requests, 6736 successes, 9437 failures |

## Failure Details

The readiness-aware runner used a small test batch because the previous 100x100 run had already shown capacity pressure. The precheck succeeded:

- Non-stream precheck: HTTP 200, marker `KIRO_GO_SUB2API_UAT_NON-STREAM_999` present.
- Stream precheck: HTTP 200, SSE valid, `message_start` and `message_stop` present, marker `KIRO_GO_SUB2API_UAT_STREAM_999` present.

The non-stream batch then failed:

- Completed records: 7
- Passed: 6
- Failed: 1
- Failure class: `AbortError`
- Failed request duration: 180002 ms
- Stream batch: not run because the non-stream gate had already failed

The acceptance target is strict: `100/100` non-stream plus `100/100` stream success through the sub2api path. A `6/7` partial non-stream result is therefore an immediate **FAIL / NOT PASS**.

## API, Browser, Database, and Log Correlation

The evidence is consistent across layers:

- Browser page/API evidence shows Kiro-Go is reachable and healthy at the process level, but Opus 4.7 fleet readiness is degraded and half-open under admission pressure.
- Runner evidence shows the sub2api route can succeed for individual precheck requests, but sustained non-stream execution still hit a 180s abort.
- Database evidence records successful usage rows for the same model and route, plus 2 upstream 502 error rows in the same window.
- Kiro-Go logs show upstream Opus 4.7 429 capacity responses with `INSUFFICIENT_MODEL_CAPACITY`.
- sub2api logs show `/v1/messages` upstream failure/context-canceled behavior and 502 completion rows.

## Screenshot Analysis

Playwright-MCP screenshot `screenshots/kiro-real-admin-current-readiness-202605211620.png` was visually inspected. It correctly shows the Kiro-Go admin dashboard with the expected top-level counters and account list. The screenshot is suitable evidence that the real admin UI was reachable and that the visible operational counters were:

- Accounts: 25
- Requests: 16173
- Successes: 6736
- Failures: 9437
- Tokens: 222.8M
- Credits: 1960.1

This screenshot validates the UI evidence, but it does not satisfy the business acceptance criterion because the API pressure run failed.

## Operational Finding

The logs and DB evidence indicate that Opus 4.7 requests remain constrained by upstream model capacity/admission pressure. Prior log review also showed sub2api sticky scheduling repeatedly selecting the same Kiro account for Opus requests in the failed window, which may reduce the practical benefit of the account pool under pressure. That is a likely next fix target, but it was not fixed or revalidated in this run.

## Final Decision

**Do not pass this UAT.**

The correct result is **FAIL / NOT PASS** until a real run completes `100/100` non-stream and `100/100` stream through the sub2api path, and screenshots/API/database/log evidence all agree.
