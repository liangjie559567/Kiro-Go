# UAT Result: sub2api -> Kiro-Go Opus 4.7 Account LB

Date: 2026-05-20 15:03 Asia/Shanghai

## Verdict

PASS

## Scope

- Fix Kiro-Go temporary-limit behavior so one Kiro upstream account 429 does not cool the whole shared profile/risk group.
- Keep Claude Code model aliases usable, including `claude-haiku-4-5-20251001`.
- Verify `/www/sub2api` calling Kiro-Go with `claude-opus-4-7` under 10 concurrency x 10 rounds for both non-streaming and streaming.
- Verify frontend pages, API evidence, database evidence, and screenshot text before marking pass.

## Code Verification

- `go test ./...`: PASS
- Container rebuilt and restarted with `docker compose up -d --build kiro-go`: PASS

## Real Load Test

Endpoint: `http://127.0.0.1:18080/v1/messages`

Model: `claude-opus-4-7`

| Mode | Requests | Pass | HTTP 200 | Max latency | P95 latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| non-stream | 100 | 100 | 100 | 46651 ms | 40392 ms |
| stream | 100 | 100 | 100 | 56595 ms | 44386 ms |

Highest observed latency: 56595 ms.

Evidence:

- `api/sub2api-opus47-nonstream-10x10-summary.json`
- `api/sub2api-opus47-stream-10x10-summary.json`
- `api/sub2api-opus47-nonstream-10x10-results.json`
- `api/sub2api-opus47-stream-10x10-results.json`

## API Evidence

Kiro-Go Opus 4.7 readiness after load:

- `mappedModel`: `claude-opus-4.7`
- `listedByGateway`: true
- `routingReason`: `schedulable accounts available`
- `locallySchedulable`: 30
- `riskGroupCoolingDown`: 0

Kiro-Go recent request logs:

- recent Opus 4.7 successes inspected: 200
- recent Opus 4.7 errors: 0
- distinct Kiro upstream accounts used: 18

Evidence:

- `api/kiro-model-readiness-opus47-after.json`
- `api/kiro-request-logs-after-10x10.json`
- `api/playwright-fullstack-summary.json`

## Database Evidence

sub2api `usage_logs` for API key 2 and requested model `claude-opus-4-7`:

- non-stream rows: 100
- stream rows: 100
- sub2api account: `kiro_claude_01` / account id 24
- account id 24 remains `schedulable=true`
- `temp_unschedulable_until` is null

Note: sub2api sees only its downstream Kiro-Go account id 24. Kiro upstream account distribution is visible in Kiro-Go request logs, not sub2api `usage_logs`.

Evidence:

- `db/sub2api-usage-opus47-after-10x10.txt`
- `db/sub2api-usage-account-distribution.txt`
- `db/sub2api-kiro-account-24-after.txt`
- `db/playwright-sub2api-db.json`

## Playwright / Screenshot Evidence

Playwright checked:

- Kiro-Go admin dashboard loads.
- Kiro-Go accounts page shows account rows.
- Kiro-Go API readiness page shows Opus readiness.
- Kiro-Go request logs page shows Opus logs.
- sub2api accounts page shows `kiro_claude_01`.
- sub2api usage page shows Opus usage.
- API calls for sub2api admin accounts and usage succeed.
- Screenshot text checks pass.
- No page errors.

Screenshots:

- `screenshots/kiro-admin-dashboard.png`
- `screenshots/kiro-admin-accounts.png`
- `screenshots/kiro-admin-opus-readiness.png`
- `screenshots/kiro-admin-request-logs.png`
- `screenshots/sub2api-admin-accounts.png`
- `screenshots/sub2api-admin-usage.png`

Final Playwright checks:

- `screenshotTextLooksCorrect`: true
- `screenshotsExist`: true
- `pass`: true

