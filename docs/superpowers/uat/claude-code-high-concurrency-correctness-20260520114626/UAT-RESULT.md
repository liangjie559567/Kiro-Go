# Claude Code High-Concurrency Correctness UAT

Date: 2026-05-20 11:46-11:56 Asia/Shanghai

Verdict: PASS

## Scope

Validate方案 A for `/www/sub2api -> /www/Kiro-Go -> Kiro official`:

- Kiro-Go 429 is treated as real upstream/runtime state, not as model-list unavailability.
- Kiro-Go model readiness separates "model listed" from "currently schedulable".
- sub2api Anthropic API-key passthrough does not amplify Kiro-Go `TEMPORARY_LIMITED` through same-account retry.
- Real Claude Code-style `/v1/messages` non-stream and stream requests work through the downstream path.
- Frontend, API, and database evidence agree before PASS.

## Code Verification

- `go test ./pool ./proxy -count=1`: PASS.
- `go test ./internal/service ./internal/handler -run 'KiroTemporaryLimited|FailoverError|AnthropicAPIKeyPassthrough' -count=1` in `/www/sub2api/backend`: PASS.
- Docker rebuild/restart:
  - `docker compose up -d --build` in `/www/Kiro-Go`: PASS.
  - `docker compose -f docker-compose.yml -f docker-compose.current.yml up -d --build sub2api` in `/www/sub2api/deploy`: PASS.

## API Evidence

- `api/kiro-model-readiness-opus47.json`:
  - `requestedModel=claude-opus-4-7`
  - `mappedModel=claude-opus-4.7`
  - `listedByGateway=true`
  - `routingReason=schedulable accounts available`
  - `summary.accountsEvaluated=21`
  - `summary.locallySchedulable=21`
  - `summary.riskGroupCoolingDown=0`
- `api/sub2api-claude-opus47-nonstream.summary.json`: HTTP 200, reply `ok`.
- `api/sub2api-claude-opus47-stream.summary.json`: HTTP 200, SSE includes `message_start`, `content_block_delta` with `ok`, and `message_stop`.
- `api/playwright-fullstack-summary.json`: all checks PASS.

## Database Evidence

- `db/sub2api-kiro-account-24-temp-unsched.json`:
  - `id=24`
  - `name=kiro_claude_01`
  - `temp_unschedulable_until=null`
  - `temp_unschedulable_reason=null`
- `db/playwright-sub2api-db.json`:
  - Claude group has exactly one account: `kiro_claude_01`.
  - Recent Opus/account 24 error logs length is `0` after successful probes.
  - Recent usage contains the successful `api_key_id=2/account_id=24` probes.

## Screenshot Review

- `screenshots/kiro-admin-accounts.png`: valid Kiro-Go account page with 21 accounts and no blocking overlay.
- `screenshots/kiro-admin-api-readiness.png`: valid Kiro-Go API tab; Claude Code readiness is visible; model readiness table shows schedulable rows.
- `screenshots/kiro-admin-settings-request-logs.png`: valid Kiro-Go settings/logs page; recent request logs show successful Opus calls and a historical 429 row.
- `screenshots/sub2api-admin-dashboard.png`: valid sub2api dashboard.
- `screenshots/sub2api-admin-accounts.png`: valid sub2api accounts page with no onboarding overlay; account rows visible.
- `screenshots/sub2api-admin-groups.png`: valid sub2api groups page showing `openai`, `google`, and `claude`.

## Screenshot/API/DB Alignment

PASS. The screenshots show rendered admin pages, the APIs show Opus 4.7 currently schedulable through 21 Kiro-Go accounts, and the database shows sub2api's Claude group still routes through `kiro_claude_01` without a stale temp-unschedulable marker after successful probes.

## Conclusion

方案 A passes UAT for the current runtime state. The earlier 429 class remains a real Kiro upstream/runtime condition, but the implemented path now exposes readiness correctly and prevents sub2api from treating Kiro-Go `TEMPORARY_LIMITED` as same-account retryable amplification.
