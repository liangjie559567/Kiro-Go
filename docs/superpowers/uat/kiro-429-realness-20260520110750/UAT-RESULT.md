# Kiro-Go 429 Realness Fullstack UAT

Run time: 2026-05-20 11:07-11:13 Asia/Shanghai

## Verdict

PASS.

The 429 was real for the earlier Claude Code/sub2api traffic, but the current `claude-opus-4-7` path is recovered for minimal real requests.

## What Was Verified

1. Kiro-Go frontend/admin page:
   - Accounts page loads and reports the 21-account pool.
   - API tab shows `claude-opus-4-7 -> claude-opus-4.7`.
   - Readiness table shows the model is listed and accounts are schedulable.
   - Settings/request-log page shows recent request log state.

2. sub2api frontend/admin page:
   - Dashboard, accounts, groups, and usage pages load without page errors.
   - Accounts page was first captured in default sort, then filtered for `kiro_claude_01` and captured again.
   - Usage page shows recent `claude-opus-4-7` usage.

3. API and database:
   - Kiro-Go readiness API: 21 accounts total, 21 schedulable.
   - Kiro-Go request logs: 119 recent records, 91 are 429.
   - sub2api DB `ops_error_logs`: recent 429 records exist for `api_key_id=2`, `account_id=24`, `group_id=1`, `model=claude-opus-4-7`, `stream=true`.
   - sub2api DB account mapping: group 1 has only `kiro_claude_01`; group 2 has 14 OpenAI fallback accounts.
   - Current real probe through sub2api Claude key returned HTTP 200, model `claude-opus-4.7`, text `ok`, and was recorded in `usage_logs`.

## Evidence Files

- Playwright summary: `api/playwright-fullstack-summary.json`
- Real probe summary: `api/real-probe-summary.txt`
- DB evidence: `db/sub2api-fullstack-db.json`, `db/accounts-groups.txt`, `db/api-keys.txt`
- Kiro-Go logs: `logs/kiro-go-429-window.log`
- sub2api logs: `logs/sub2api-429-window.log`
- Screenshots:
  - `screenshots/kiro-admin-accounts.png`
  - `screenshots/kiro-admin-api-readiness.png`
  - `screenshots/kiro-admin-settings-request-logs.png`
  - `screenshots/sub2api-admin-dashboard.png`
  - `screenshots/sub2api-admin-accounts.png`
  - `screenshots/sub2api-admin-accounts-kiro-filtered.png`
  - `screenshots/sub2api-admin-groups.png`
  - `screenshots/sub2api-admin-usage.png`

## Key Findings

The reason 21 accounts can still experience 429 is that model/account listing readiness is not the same as a successful upstream generation request. The 21 accounts can list `claude-opus-4.7` and be locally schedulable, while Kiro upstream can still apply temporary risk-group/request limits during actual generation.

The earlier failing path was:

`Claude Code -> sub2api api_key_id=2/claude -> group 1 -> account_id=24 kiro_claude_01 -> Kiro-Go :8080 -> Kiro upstream`

The sub2api Claude group has no non-Kiro fallback. When `kiro_claude_01` or the Kiro upstream pool returns temporary 429, Claude Code sees 429 and retries.

Current state differs from the earlier failure window: the same class of minimal direct and sub2api probes now returns 200, so this UAT passes for current availability while preserving the historical 429 root-cause evidence.

