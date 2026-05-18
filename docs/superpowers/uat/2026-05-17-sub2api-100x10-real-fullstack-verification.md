# 2026-05-17 sub2api 100x10 Real Fullstack Verification

Run ID: `sub2api-100x10-real-20260517104910`

Scope:
- Real sub2api downstream requests to Kiro-Go.
- 100 sync requests with concurrency 10.
- 100 stream requests with concurrency 10.
- Per-request marker correctness and latency.
- sub2api Postgres usage logging.
- Real Chrome / Playwright browser verification of the admin Usage page.

## Verdict

PASS for correctness, streaming event integrity, database logging, and browser-visible Usage records.

Performance caveat: requests completed without client-visible errors, but Kiro-Go logged upstream 429 retries during the run, producing 20s+ tail latency.

## API Load Results

Artifact directory:

`docs/superpowers/uat/sub2api-100x10-real-20260517104910/`

| Mode | Total | Concurrency | HTTP 200 | Correct marker | Exact marker | Injection leaks | Failed | p50 | p90 | p95 | p99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| sync | 100 | 10 | 100 | 100 | 100 | 0 | 0 | 5435 ms | 25860 ms | 26451 ms | 27051 ms | 27435 ms |
| stream | 100 | 10 | 100 | 100 | 100 | 0 | 0 | 3321 ms | 22957 ms | 23349 ms | 23574 ms | 23598 ms |

Stream checks required each request to include:

- `message_start`
- `content_block_start`
- `content_block_stop`
- `message_delta`
- `message_stop`
- zero SSE parse errors
- no SSE error event

Artifacts:

- `docs/superpowers/uat/sub2api-100x10-real-20260517104910/summary.json`
- `docs/superpowers/uat/sub2api-100x10-real-20260517104910/sync-100x10-results.json`
- `docs/superpowers/uat/sub2api-100x10-real-20260517104910/stream-100x10-results.json`

## Database Verification

Before-run max `usage_logs.id`: `46748`.

After-run rows for `account_id=24`:

| Rows | Sync | Stream | `/v1/messages` inbound+upstream | Min ID | Max ID |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 200 | 100 | 100 | 200 | 46758 | 46974 |

Database latency percentiles matched the client-side results:

| Stream | Rows | Min | p50 | p90 | p95 | p99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| false | 100 | 1826 ms | 5428 ms | 25852 ms | 26440 ms | 27043 ms | 27428 ms |
| true | 100 | 1700 ms | 3311 ms | 22947 ms | 23339 ms | 23562 ms | 23589 ms |

The backend filter endpoint was also checked directly:

`GET /api/v1/admin/usage?account_id=24...`

It returned HTTP 200, total `1648`, first 20 rows all `account_id=24`.

## Browser Verification

Strict browser evidence:

`docs/superpowers/uat/sub2api-100x10-real-20260517104910/playwright-strict2/browser-summary.json`

Screenshot:

`docs/superpowers/uat/sub2api-100x10-real-20260517104910/playwright-strict2/sub2api-usage-account24-strict.png`

Browser checks:

| Check | Result |
| --- | --- |
| Actual network request included `account_id=24` | PASS |
| Visible rows show `kiro_claude_01` | PASS |
| Visible rows show `claude-sonnet-4.5` | PASS |
| Visible rows show `Inbound:/v1/messages` and `Upstream:/v1/messages` | PASS |
| Visible rows show stream/sync type data | PASS |
| Visible rows do not include OpenAI rows after strict account selection | PASS |
| Visible rows include 10:49-10:52 records from this run | PASS |
| Page errors | 0 |

The strict screenshot shows the account selector set to `kiro_claude_01`, `/v1/messages` endpoint distribution, and rows from this run with 20s+ stream durations such as `23.28s`, `23.59s`, `23.51s`, and `22.66s`.

## Bug / Risk Findings

1. Upstream Kiro endpoints returned rate limits during 10-concurrent load.

   Kiro-Go logs showed 64 upstream `429` retry events in the run window across Kiro IDE, CodeWhisperer, and AmazonQ. This did not surface as client-visible failures because retry/fallback succeeded, but it caused the 20s+ tail latency.

2. Browser onboarding overlay can block automated validation.

   The strict browser run initially failed because `driver-overlay` intercepted clicks. The final strict run pre-marked the onboarding tour as seen in the test browser session and removed overlay artifacts. Product functionality was not changed.

3. Filling the Account text input is not enough to filter Usage records.

   The Usage Account filter only applies after selecting the dropdown option, which sets `account_id=24`. A previous screenshot that only filled the text field mixed OpenAI rows. The strict run clicked the `kiro_claude_01 #24` option and verified actual network requests contained `account_id=24`.

4. Non-blocking failed browser requests were observed.

   Failed requests in the final strict run were aborted `/api/v1/usage/dashboard/*` user dashboard requests during navigation. The admin usage API requests with `account_id=24` completed and rendered correctly.

