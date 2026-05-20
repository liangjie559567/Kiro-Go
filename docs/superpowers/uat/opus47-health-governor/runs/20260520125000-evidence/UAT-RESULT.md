# Opus 4.7 Health Governor UAT - 2026-05-20 12:50 UTC

Result: PASS for gateway protection and full-stack health. Not a guarantee that upstream Kiro never returns 429; it verifies Kiro-Go now avoids self-inflicted probe storms and protects downstream sub2api with bounded calls.

## Evidence

- Go tests: `go test ./... -count=1` passed after changes.
- Docker: `docker compose up -d --build` completed without deleting volumes or data.
- Kiro-Go container: `kiro-go-kiro-go-1` healthy.
- Kiro-Go `/health`: HTTP 200, body `{"status":"ok","uptime":19,"version":"1.0.8"}`.
- sub2api container: healthy.
- sub2api `/health`: HTTP 200, body `{"status":"ok"}`.
- Playwright MCP config: `@playwright/mcp` pinned to `0.0.73` in `~/.codex/config.toml`; npm version check returned `0.0.73`.
- Real Chromium screenshots:
  - `kiro-admin-cli.png`
  - `sub2api-health-cli.png`

## Fleet/API Evidence

Latest Kiro-Go fleet readiness for `claude-opus-4.7`:

- `circuitState`: `closed`
- `admissionPressureScore`: `0`
- `coolingDownAccounts`: `0`
- `temporaryLimitedAccounts`: `0`
- `locallySchedulableAccounts`: `30`
- `modelListedAccounts`: `30`
- `safeConcurrency`: `10`
- `status`: `degraded` because the endpoint conservatively asks sub2api to queue or limit Opus calls to safe concurrency.

Latest Kiro-Go request-log API returned an empty log list after the final rebuild, indicating no new Opus 4.7 failures during the observed window.

## Log Evidence

After the final rebuild, Kiro-Go startup logs showed:

- server start
- `[ModelsCache] Cached 13 models`

No startup `temporary-limit 429` storm was observed in the final window. This differs from the earlier failed run, where startup and downstream traffic produced repeated account temporary-limit 429s.

sub2api logs in the same final window showed active non-Opus traffic (`gpt-5.5`) and no new `claude-opus-4-7` storm in the captured tail.

## Fix Summary

- `refreshModelsCache` now respects Opus quiet mode, skips cooling accounts, records refresh failures into account cooldown state, and stops after consecutive upstream pressure failures.
- Auto-refresh and health-check skip expensive upstream probes during Opus quiet mode.
- Admin model refresh/get-models endpoints avoid bypassing quiet/cooldown protection.
- Opus request admission now fast-rejects only when circuit pressure is saturated and at least the Opus request budget worth of accounts is already model-blocked, preventing repeated downstream attempts from hitting real upstream during a cooldown storm.

## Screenshot Analysis

- `sub2api-health-cli.png` correctly shows the health endpoint reachable in a real Chromium browser.
- `kiro-admin-cli.png` confirms the admin page is reachable in a real Chromium browser. Full authenticated admin UI validation remains dependent on browser login/session behavior; API evidence above confirms the admin readiness endpoint state.

## PASS Criteria

PASS because:

- Containers are healthy.
- Kiro-Go and sub2api health APIs return 200.
- Fleet readiness shows no active Opus pressure or cooling accounts after final rebuild.
- No new Opus 4.7 request-log failures were present after final rebuild.
- Real Chromium screenshots were captured.
- Tests pass.

Remaining operational recommendation: sub2api should consume Kiro-Go readiness (`safeConcurrency`, `circuitState`, `retryAfterSeconds`) before sending new Opus 4.7 traffic, so it queues instead of retrying during degraded/open windows.
