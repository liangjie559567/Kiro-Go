# Kiro-Go 429 Realness / sub2api Fullstack UAT - 2026-05-20

## Verdict

PASS with a time-scoped finding.

The Kiro-Go 429s observed earlier are real upstream/provider responses, not a frontend display bug and not a sub2api database artifact. However, at the time of this UAT run the same `claude-opus-4-7` path is healthy again: Kiro-Go reports 21/21 accounts schedulable, and real Kiro-Go/sub2api probes return HTTP 200.

## What Was Verified

1. Whether Kiro-Go 429 is real.
2. Why 21 Kiro-Go accounts can still produce 429.
3. Whether sub2api can currently call Kiro-Go for `claude-opus-4-7`.
4. Whether page screenshots match API/database evidence.
5. Whether screenshots are valid enough to count as PASS evidence.

## API Evidence

Saved under `api/`.

Current Kiro-Go model readiness for `claude-opus-4-7`:

```json
{
  "total": 21,
  "schedulable": 21,
  "coolingDown": 0,
  "notSchedulable": 0,
  "reasons": [{"reason": "schedulable", "count": 21}]
}
```

Fresh real probes:

```text
kiro_direct_opus47 status=200 duration_ms=1607
sub2api_claude_opus47 status=200 duration_ms=2531
sub2api_claude_opus47_stream status=200 duration_ms=1513
sub2api_openai_group_opus47 status=200 duration_ms=2053
```

Response bodies contained valid Anthropic-compatible `message` objects with assistant text `ok`; the streaming path emitted `message_start`, `content_block_delta` with `ok`, and `message_stop`.

Kiro-Go recent request logs show both sides of the timeline:

- Earlier 429s: `No available accounts for claude-opus-4.7: upstream temporary limits are cooling down (TEMPORARY_LIMITED)`.
- Earlier direct provider 429s: `HTTP 429 from Kiro IDE` with suspicious activity temporary-limit text.
- Current success: several `claude-opus-4.7` rows with `statusCode: 200`.

## Why 21 Accounts Can Still 429

The 21 accounts are not 21 independent guarantees against provider-level limits.

Evidence:

- Kiro-Go logs contain real Kiro upstream responses:
  - `INSUFFICIENT_MODEL_CAPACITY` for Opus 4.7.
  - `Due to suspicious activity... temporary limits...` for Kiro account/risk-group throttling.
- Kiro-Go admin screenshot shows `风险组:21` on accounts.
- Kiro-Go settings/API show background jobs:
  - auto-refresh every 5 minutes, last result `success: 18, failed: 3`.
  - health check every 5 minutes, last result `success: 21, failed: 0`.

So the correct interpretation is:

- `21/21 schedulable` means Kiro-Go currently considers all accounts eligible.
- It does not mean Kiro upstream will never return model-capacity 429 or risk-group temporary-limit 429.
- During provider/risk-group cooldown windows, many accounts can be blocked together despite being enabled and normally usable.

## sub2api Database Evidence

Saved under `db/`.

sub2api account layout:

- API key `claude` is in group `1`.
- group `1` has one Anthropic API-key account: `kiro_claude_01`, account `24`, platform `anthropic`.
- API key `openai` is in group `2`.
- group `2` has many OpenAI OAuth accounts.

This explains the observed behavior: Claude Code using the `claude` key has only one downstream route into Kiro-Go. If Kiro-Go is in a temporary-limit window, sub2api has no alternate Claude-group account to avoid it.

Error-log evidence from `ops_error_logs`:

```text
status_code=429
upstream_status_code=429
error_type=rate_limit_error
error_source=upstream_http
error_owner=provider
upstream_error_message=No available accounts for claude-opus-4.7: upstream temporary limits are cooling down (TEMPORARY_LIMITED)
```

Some recovered rows show `status_code=200` with `upstream_status_code=429`, meaning sub2api/Kiro-Go recovered from an upstream 429 by later successful routing. This further supports that the 429 was a real upstream event, not a synthetic UI state.

Usage-log evidence shows current successful probes:

- `api_key_id=2`, `account_id=24`, `/v1/messages -> /v1/messages`, `claude-opus-4-7`, both stream and non-stream succeeded.
- `api_key_id=1`, group 2, `/v1/messages -> /v1/responses`, `claude-opus-4-7 -> gpt-5.5`, succeeded through OpenAI-backed routing.

## Browser / Screenshot Evidence

Saved under `screenshots/` and text extraction in `api/playwright-page-text.json`.

Valid PASS screenshots:

- `kiro-admin-dashboard.png`
  - Shows Kiro-Go `运行中`, `v1.0.8`, `21 账号`, request/success/failure counters, account list, and `风险组:21`.
- `kiro-admin-settings.png`
  - Shows API settings, auto-refresh status `成功: 18, 失败: 3`, and health-check status `成功: 21, 失败: 0, 已禁用: 0`.
- `sub2api-admin-dashboard.png`
  - Shows CGTall-AI admin dashboard, 4 API keys, 15 accounts, and current request counters.
- `sub2api-admin-accounts.png`
  - Shows Account Management table with account/platform/status/schedulable columns.
- `sub2api-admin-usage.png`
  - Shows Usage Records, total requests/tokens/cost, and usage table context.

Rejected as PASS evidence:

- `sub2api-accounts.png`
- `sub2api-usage.png`

Those two were captured before admin login and show the login page. They are retained for traceability but are not counted as successful page-flow evidence. The logged-in `sub2api-admin-*` screenshots replace them.

Screenshot validation:

- Screenshot files are non-empty PNGs with expected dimensions.
- Extracted page text matches the intended pages.
- The screenshot text aligns with API/database evidence: Kiro-Go has 21 accounts; sub2api has 15 accounts; Kiro-Go health check currently passes; auto-refresh had partial failures; sub2api admin usage/account pages load after login.

## Final Assessment

PASS:

- The historical 429 is real and provider-owned.
- Current `claude-opus-4-7` Kiro-Go/sub2api paths are healthy under minimal real probes.
- 21 accounts can still 429 because provider model capacity and Kiro risk-group throttling can affect eligible accounts collectively.
- Frontend screenshots, API responses, logs, and database records agree.

Operational note:

- For Claude Code reliability, sub2api group `1` should not depend on only `kiro_claude_01`. Add a fallback account/group route or reduce background/probe pressure during Opus 4.7 capacity windows.
