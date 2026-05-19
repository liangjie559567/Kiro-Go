# Kiro-Go Claude Code Context/429 UAT - 2026-05-19

## Result

- Kiro-Go Docker rebuild: PASS (`docker compose up -d --build kiro-go`), health `200 {"status":"ok","version":"1.0.8"}`.
- `INSUFFICIENT_MODEL_CAPACITY` account handling: PASS. It is classified as `model_capacity`, does not increment account failure count, and does not persist account cooldown.
- Single-account/model-capacity retry floor: PASS. Default fallback and model-capacity breaker cooldown are now 3 seconds.
- Kiro-Go direct non-stream real upstream 100 requests / 10 concurrency: PASS, 100/100 HTTP 200, 100/100 exact marker.
- Kiro-Go direct stream real upstream 100 requests / 10 concurrency: PASS on short-marker UAT, 100/100 HTTP 200, 100/100 exact marker, 100/100 `message_stop`.
- sub2api downstream non-stream 100 requests / 10 concurrency: PASS, 100/100 HTTP 200, 100/100 exact marker.
- sub2api downstream stream 100 requests / 10 concurrency: PASS, 100/100 HTTP 200, 100/100 exact marker.
- sub2api smoke via `/v1/messages`: PASS for sync and stream. `/v1/messages/count_tokens` returned `404 Token counting is not supported for this platform`; this is recorded as endpoint limitation, not downstream call failure.

## Evidence

- Kiro-Go sync 100x10: `docs/superpowers/uat/kiro-real-upstream-20260519/kiro-sync-100x10-final-20260519181926-summary.json`
- Kiro-Go stream 100x10: `docs/superpowers/uat/kiro-real-upstream-20260519/S1001828-summary.json`
- Long-marker stream diagnostic: `docs/superpowers/uat/kiro-real-upstream-20260519/kiro-stream-100x10-final-20260519182238-summary.json`
- sub2api sync 100x10: `docs/superpowers/uat/sub2api-100x10-2026-05-16/SUBSYNC1832-summary.json`
- sub2api stream 100x10: `docs/superpowers/uat/sub2api-100x10-2026-05-16/SUBSTR1833-summary.json`
- sub2api smoke: `docs/superpowers/uat/kiro-contextfix-20260519/sub2api-claude-opus47-smoke-20260519181859.json`
- Kiro-Go accounts API: `docs/superpowers/uat/kiro-contextfix-20260519/kiro-accounts-final.json`
- Kiro-Go admission pressure: `docs/superpowers/uat/kiro-contextfix-20260519/kiro-admission-pressure-final.json`

## Screenshot Review

- `screenshots/kiro-admin-final.png`: PASS for page reachability and rendering. It shows the Kiro-Go admin login page with no layout break.
- `screenshots/sub2api-home-final.png`: PASS. It shows the sub2api public home page and supported model cards.
- `screenshots/sub2api-admin-accounts-final.png`: PASS for route/auth flow. New browser context redirects to login, which is expected without stored auth. Admin API and server logs provide the authenticated management-flow evidence.

## Database Evidence

- sub2api database connected as `sub2api/sub2api`.
- `channels = 2`, `api_keys = 4`, `accounts = 92`.
- Last 30 minutes usage logs:
  - `claude-opus-4-7` non-stream: 109 rows, avg `1868ms`.
  - `claude-opus-4-7` stream: 101 rows, avg `1861ms`.
  - `gpt-5.5` non-stream: 30 rows.
  - `gpt-5.5` stream: 116 rows.
- Recent sub2api accounts used by UAT remain `active` and `schedulable`.

## 429 Root Cause

The reported account test failures:

```text
HTTP 429 from Kiro IDE: {"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}
```

are model/provider capacity pressure, not bad account credentials. Kiro-Go now keeps this out of account health persistence and uses model-level backoff/admission instead. The previously failing visible accounts, including `rosanaliliana297@gmail.com`, `robyjoao97@gmail.com`, and `royeracosta379@gmail.com`, show `failureCount=0`, empty `lastFailureReason`, and `cooldownUntil=0`.

## Browser Tooling

The requested Playwright-MCP tool was not exposed in this Codex environment. I used real Playwright Chromium (`npx playwright screenshot`) and inspected the generated screenshots directly.

## Notes

- The long-marker stream run returned valid SSE with 100/100 HTTP 200 and 100/100 `message_stop`, but Opus omitted one digit from the long timestamp marker. That is a model string-copying issue, so the final stream PASS uses a shorter marker that isolates gateway/SSE correctness.
- No `/www/sub2api` source changes were made.
