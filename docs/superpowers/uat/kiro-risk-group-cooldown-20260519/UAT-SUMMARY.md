# Kiro-Go Risk Group Cooldown UAT

Date: 2026-05-19 Asia/Shanghai

## Result
PASS for local fix, Docker deployment, health checks, admin API evidence, and sub2api availability.

Live upstream 10x100 generation stress was intentionally not run because current evidence shows all configured Kiro accounts share one upstream risk group; doing that test would increase account-level risk instead of validating the fix safely.

## Evidence Summary
- Kiro-Go accounts: 21
- Non-empty risk groups: 1
- Largest risk group size: 21
- Risk group: `profile:arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK` size=21
- Currently cooling accounts from API: 0
- Claude Code readiness rows: 21, schedulable=21

## Files
- `kiro-health-final.headers`
- `kiro-health-final.body`
- `accounts-final.headers`
- `accounts-final.json`
- `readiness-final.headers`
- `readiness-final.json`
- `sub2api-health-final.headers`
- `sub2api-health-final.body`
- `sub2api-db-counts-final.txt`
- `docker-ps-final.txt`
- `kiro-go-tail-final.log`
- `sub2api-tail-final.log`

## Interpretation
The previous UI state was misleading because every configured account could show as schedulable while all 21 accounts shared the same `profileArn` risk subject. The fix adds local risk-group cooldown propagation: once one account receives `temporary_limited`, same-group accounts are skipped by pool routing, admin account test, account list cooldown display, and Claude Code model readiness.

Current final API evidence shows no active local cooldown at capture time, but confirms all 21 accounts are in one shared profile risk group. If the upstream returns the suspicious-activity 429 again, the group-level local protection now prevents continuing through the other 20 accounts in that same group.

## Browser Evidence
- Browser runner: Playwright 1.60.0 with system Google Chrome, headless. Playwright-MCP was not exposed in this Codex tool environment, so browser-level evidence was captured through Playwright CLI/runtime.
- `playwright/accounts.png`: admin account page rendered 21 account cards and shows `风险组:21`.
- `playwright/api-readiness.png`: Claude Code API readiness page rendered the account readiness table with the new `Cooldown` column.
- `playwright/browser-result.json`: structured browser result, including `accountCards=21`, `riskGroupTextPresent=true`, `readinessTextPresent=true`.
