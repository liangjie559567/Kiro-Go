# 2026-05-16 Docker Production Integration Verification

Scope: Kiro-Go Docker production deployment, sub2api downstream integration, PostgreSQL/Redis connectivity, API compatibility, and Playwright-MCP browser validation.

## Environment

- Kiro-Go container: `kiro-go-kiro-go-1`, image `kiro-go-kiro-go`, port `8080:8080`.
- sub2api containers: `sub2api`, `sub2api-postgres`, `sub2api-redis`, port `18080:8080`.
- sub2api is attached to both `deploy_sub2api-network` and `kiro-go_default`; DNS resolves `kiro-go` inside the sub2api container.

## API Evidence

- Kiro-Go `/health`: HTTP 200, `{"status":"ok","version":"1.0.8"}`.
- Kiro-Go `/v1/models`: HTTP 200, Anthropic-shaped model list, 29 models, no `supports_image` field.
- Kiro-Go direct `/v1/messages` with production key: HTTP 200, returned `KIRO_DIRECT_OK_20260516`, usage present.
- Kiro-Go direct `/v1/messages/count_tokens`: HTTP 200, `input_tokens=9`.
- sub2api `/health`: HTTP 200, `{"status":"ok"}`.
- sub2api -> Kiro-Go `/v1/messages`: HTTP 200, returned `SUB2API_KIRO_OK_20260516`, total client time about 1.785s.
- sub2api -> Kiro-Go `/v1/messages/count_tokens`: HTTP 200, `input_tokens=9`, total client time about 6ms. This path does not write usage_logs, consistent with count-token probe behavior.
- sub2api 4-way concurrent `/v1/messages`: 4/4 HTTP 200, each returned `OK`, duration about 1.41s to 1.51s.

## Database Evidence

- `accounts.id=24`: `kiro_claude_01`, platform `anthropic`, type `apikey`, status `active`, schedulable `true`, concurrency `12`, base_url `http://kiro-go:8080`, `anthropic_passthrough=true`.
- `api_keys.id=2`: `claude`, group `1`.
- `channels.id=2`: `claude`, status `active`.
- `groups.id=1`: `claude`, platform `anthropic`; `account_groups` links account 24 to group 1; `channel_groups` links group 1 to channel 2.
- Latest usage rows for account 24/API key 2:
  - `30383`: `/v1/messages`, `claude-sonnet-4.5`, input `4131`, output `10`, duration `1771ms`.
  - `30388`-`30391`: four concurrent `/v1/messages`, input `4105`, output `1`, duration `1397ms` to `1495ms`.

## Browser Evidence

Screenshots are archived in `docs/superpowers/uat/playwright-2026-05-16/`.

- `kiro-go-admin-dashboard-2026-05-16.png`: admin logged in; running status, v1.0.8, account/request/token stats visible.
- `kiro-go-admin-api-tab-2026-05-16.png`: Claude/OpenAI/model/stats endpoints visible.
- `kiro-go-admin-settings-logs-2026-05-16.png`: API key auth enabled, auto-refresh/health-check enabled, health-first scheduler, recent request logs show 200 responses and token timings.
- `sub2api-admin-dashboard-2026-05-16.png`: admin dashboard rendered, API key/account/request/model distribution visible.
- `sub2api-admin-accounts-kiro-clean-2026-05-16.png`: account filter shows `kiro_claude_01`, Anthropic Key, Active, schedulable, group `claude`, capacity `0/12`.
- `sub2api-admin-channels-2026-05-16.png`: channels page shows `claude` and `openai` active entries.
- `sub2api-admin-usage-2026-05-16.png`: usage page shows `claude-sonnet-4.5`, `claude` group, and `/v1/messages` endpoint distribution.

Note: Playwright direct navigation to `127.0.0.1:18080` returned `ERR_BLOCKED_BY_CLIENT`, while curl to the same URL returned HTTP 200. Browser validation used Playwright route interception from a reachable local origin to the real `127.0.0.1:18080` backend, preserving real sub2api JS/CSS/API behavior. Console warnings were limited to external payment SDKs blocked by browser policy (`Stripe`, `Airwallex`); gateway/admin pages and API requests were HTTP 200.

## Verdict

PASS for Docker deployment, Kiro-Go API compatibility smoke tests, sub2api database connectivity/configuration, sub2api -> Kiro-Go real downstream calls, 4-way concurrency smoke, and Playwright-MCP admin workflow validation.

Residual note: this is a production smoke/integration verification, not a long-duration load test. Count-token calls return correctly but are not recorded in `usage_logs`.
