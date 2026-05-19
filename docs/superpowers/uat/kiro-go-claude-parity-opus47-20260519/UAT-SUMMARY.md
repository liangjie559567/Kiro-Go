# Kiro-Go Claude Code Parity and Opus 4.7 UAT

Date: 2026-05-20

## Verdict

PASS.

Direct Kiro-Go Opus 4.7, max_tokens=0 cache prewarm, live sub2api black-box generation, readiness APIs, request-log evidence, database checks, Docker health, and browser screenshots all passed. Recent request logs also show temporary-limit 409 entries, but those are classified as `TEMPORARY_LIMITED` pool/cooldown conditions and did not prevent the successful direct or sub2api marker calls.

## Commands

- `go test ./... -count=1`: PASS
- `docker compose up -d --build kiro-go`: PASS
- Kiro-Go health: PASS (`{"status":"ok","version":"1.0.8"}`)
- sub2api health: PASS (`{"status":"ok"}`)

## API Evidence

- `kiro-claude-readiness.json`: readiness includes `opus47AdaptiveAdmission` PASS and recent admission pressure evidence.
- `kiro-opus47-readiness.json`: 21 account rows, 21 schedulable, `admissionPressure` object present.
- `kiro-admission-pressure.json`: current pressure list empty after successful recovery.
- `kiro-accounts.json`: account/risk-group source for browser dashboard evidence.
- `direct-opus47.json`: direct Kiro-Go returned `KIro-Go opus47 parity ok` with model `claude-opus-4.7`.
- `direct-opus47-prewarm.json`: `stop_reason:"max_tokens"`, `cache_creation_input_tokens:43`.
- `sub2api-opus47.json`: live sub2api returned `sub2api to Kiro-Go opus47 ok`.
- `kiro-request-logs.json`: 12 Opus 4.7 logs, 1 cache prewarm log, 11 Claude Code header logs.

## Browser Evidence

- `playwright/admin-dashboard.png`: nonblank dashboard with account list, cooldown badges, and risk-group badges.
- `playwright/admin-readiness.png`: nonblank API/readiness page with Claude Code readiness and admission pressure evidence.
- `playwright/browser-result.json`: `hasRiskGroup`, `hasClaudeCode`, `hasAdmission`, and `hasCooldown` are all true.

## Database Evidence

`sub2api-db-counts.txt`:

```text
users=3
groups=4
accounts=98
api_keys=4
```

The local sub2api schema uses `status` and `deleted_at` for API key availability; the UAT query used `deleted_at is null` and active/enabled status instead of the plan's `disabled_at` field.

## Account Health

Temporary limits and risk-group cooldowns were visible in the admin UI and recent request logs. They were classified as `TEMPORARY_LIMITED` and surfaced as cooldown/account availability evidence, not as false permanent account-health damage. The Opus readiness snapshot still reported 21 schedulable accounts at capture time.

## Concurrency Health

At readiness capture, `admissionPressure.active` was false for `claude-opus-4-7` and `/admin/api/admission-pressure` returned an empty pressure list. Recent request logs showed effective concurrent limit evidence (`10`) on temporary-limit entries and no queue-timeout pressure in the captured summary. Cache prewarm and successful direct/sub2api requests completed while the service stayed healthy.

## sub2api Black-Box Result

PASS. Live sub2api `/v1/messages` called Kiro-Go and returned the exact marker `sub2api to Kiro-Go opus47 ok` with `stop_reason:"end_turn"`.

## Screenshot Analysis

Screenshots are nonblank PNGs: `admin-dashboard.png` is 1440x4446 and `admin-readiness.png` is 1440x2565. Extracted page text contains Claude Code readiness, `opus47AdaptiveAdmission`, max_tokens=0 cache prewarm evidence, model readiness account rows, and `Admission pressure: inactive`.
