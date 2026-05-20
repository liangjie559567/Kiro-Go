# Kiro-Go Temporary-Limit Risk Group Removal UAT

Date: 2026-05-20 13:43-13:49 Asia/Shanghai
Scope: Kiro-Go -> sub2api -> Claude Code `/v1/messages` path for `claude-opus-4-7` after canceling local risk-group cooldown escalation.

## Result

PASS.

The screenshot, API, and database evidence agree:

- Kiro-Go model readiness reports `routingReason=schedulable accounts available`.
- `summary.accountsEvaluated=21`.
- `summary.locallySchedulable=21`.
- `summary.riskGroupCoolingDown=0`.
- `summary.generationBlocked=0`.
- Every readiness row has `cooldownSource=account`; no row has `cooldownSource=risk_group`.
- sub2api Claude key path returned HTTP 200 streaming response with `ok`.
- sub2api database recorded the real Claude group call as `api_key_id=2`, `group_id=1`, `account_id=24`, `model=claude-opus-4-7`, `stream=true`, `duration_ms=2729`.
- sub2api `ops_error_logs` recent temporary-limit count is 0.

## What Was Fixed

Kiro-Go no longer treats Kiro official suspicious temporary-limit 429 as a shared risk-group signal. A temporary-limit failure now cools only the account that received it. Other accounts with the same profile ARN or the same `d-...` user-id prefix remain schedulable and can be selected by the normal account load-balancing path.

The `riskGroupKey`/`riskGroupSize` fields remain visible as informational grouping on the accounts page, but they are no longer used to block routing or readiness.

## Evidence Files

- `kiro-model-readiness.json` - Kiro-Go readiness API evidence.
- `kiro-accounts.json` - Kiro-Go accounts API evidence.
- `sub2api-claude-stream.sse` - real sub2api Claude streaming response.
- `sub2api-claude-stream.headers` - HTTP headers for the real streaming response.
- `sub2api-usage-logs.txt` - database usage log evidence.
- `sub2api-temp-errors.txt` - database temporary-limit error count.
- `kiro-go-log-since-5m.txt` - Kiro-Go runtime log excerpt.
- `sub2api-log-since-5m.txt` - sub2api runtime log excerpt.
- `playwright-summary.json` - browser automation result.
- `kiro-dashboard.png` - browser screenshot after login.
- `kiro-accounts-page.png` - accounts page screenshot.
- `kiro-api-readiness-page.png` - API/readiness page screenshot.

## Screenshot Analysis

The screenshots are valid PNGs and non-empty:

- `kiro-dashboard.png`: 1440 x 4446.
- `kiro-accounts-page.png`: 1440 x 4446.
- `kiro-api-readiness-page.png`: 1440 x 2565.

Playwright verified the rendered pages and then fetched the readiness API inside the same logged-in browser session. The page-level checks passed:

```json
{
  "dashboardVisible": true,
  "accountsRows": 21,
  "accountsTextHasRiskGroup": true,
  "readiness": {
    "routingReason": "schedulable accounts available",
    "accountsEvaluated": 21,
    "locallySchedulable": 21,
    "riskGroupCoolingDown": 0,
    "generationBlocked": 0,
    "nonSchedulable": 0,
    "riskGroupRows": 0
  }
}
```

`accountsTextHasRiskGroup=true` is expected because the page still displays informational risk group size. It is not a failure because `riskGroupRows=0` and `riskGroupCoolingDown=0` prove the group is not used as a cooldown source.

## API Evidence

Kiro-Go readiness:

```json
{
  "summary": {
    "accountsEvaluated": 21,
    "generationBlocked": 0,
    "locallySchedulable": 21,
    "modelListed": true,
    "riskGroupCoolingDown": 0
  },
  "routingReason": "schedulable accounts available"
}
```

sub2api real streaming response:

```text
status=200 duration_ms=2751
content_block_delta: ok
message_stop
```

## Database Evidence

Latest relevant `usage_logs` row:

```text
id=68107
created_at=2026-05-20 13:43:46.374657+08
api_key_id=2
account_id=24
group_id=1
model=claude-opus-4-7
requested_model=claude-opus-4-7
stream=true
duration_ms=2729
inbound_endpoint=/v1/messages
upstream_endpoint=/v1/messages
```

Temporary-limit error count in recent window:

```text
recent_temp_errors=0
```

## 429 Realness Conclusion

Kiro official 429 is real. During the validation window, Kiro-Go logged an official Opus 4.7 capacity response:

```text
Endpoint Kiro IDE returned Opus 4.7 429: {"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}
```

But this is not evidence that all accounts should be locally blocked. User-provided live evidence showed one account temporary-limited while adjacent accounts succeeded. The corrected behavior is therefore account-local temporary-limit cooldown plus normal load-balanced scheduling across remaining accounts.

## Research Comparison

`jwadow/kiro-gateway` classifies HTTP 429 as a recoverable account error and fails over to another account in its account system. It does not implement same-profile or same-user-prefix group cooling for 429. This supports the corrected Kiro-Go behavior.

Official Kiro documentation and rollout notes indicate Opus 4.7 availability can depend on tier, region, and rollout state, and model availability does not eliminate runtime capacity limits. That supports keeping model-capacity handling separate from account temporary-limit handling.

## Verification Commands

```bash
go test ./pool -count=1
go test ./proxy -count=1
go test ./... -count=1
NODE_PATH=/root/.npm/_npx/705bc6b22212b352/node_modules node docs/superpowers/uat/kiro-riskgroup-removal-20260520134343/playwright-uat.js
```

All passed.
