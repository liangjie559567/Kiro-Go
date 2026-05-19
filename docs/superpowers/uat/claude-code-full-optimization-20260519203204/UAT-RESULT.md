# Claude Code Full Optimization UAT - 2026-05-19 20:45 CST

## Verdict

PARTIAL PASS. Kiro-Go code, Docker service, local Claude compatibility endpoints, browser admin UI, sub2api reachability, and database integration were verified. Real upstream generation through Kiro is BLOCKED by Kiro account-level suspicious temporary limits, so 10-concurrency x 100 real generation cannot be honestly marked PASS now.

## Changes Verified

- Classified suspicious temporary limits as `temporary_limited` and model capacity as `model_capacity` instead of generic account rate limit.
- `model_capacity` no longer marks accounts failed; it only participates in model/admission backoff.
- Suspicious temporary limits stop the current request and do not enter the Opus 4.7 90s retry loop.
- Pool-level `TEMPORARY_LIMITED` is returned only when all evaluated accounts for a model are blocked, not when one account is cooling down.
- Non-stream Claude/OpenAI paths now record suppressed invalid tool use like stream paths.

## Evidence

| Area | Result | Evidence |
|---|---:|---|
| Go tests | PASS | `go test ./... -count=1` passed |
| Docker deploy | PASS | `final/kiro-health-final.json`, `final/docker-ps-final.jsonl` |
| Kiro-Go health | PASS | `{"status":"ok","version":"1.0.8"}` in `final/kiro-health-final.json` |
| sub2api health | PASS | `{"status":"ok"}` in `final/sub2api-health-final.json` |
| Claude count_tokens compatibility | PASS for Claude Code compatibility, PARTIAL for official exact parity | `final/kiro-count-tokens-final.json` returned `input_tokens` |
| `max_tokens:0` compatibility | PASS for local response shape, PARTIAL for official cache-warmup parity | `final/kiro-max-tokens-zero-final.json` returned empty content and `stop_reason=max_tokens` |
| Claude Code readiness page | PASS/PARTIAL mixed | `final/kiro-claude-readiness-final.json`, `final/kiro-admin-claude-readiness.png` |
| Opus 4.7 model readiness | PASS for local schedulability | `final/kiro-model-readiness-opus47-final.json` showed 21 enabled/schedulable accounts at capture time |
| sub2api DB wiring | PASS | `final/sub2api-db-claude-evidence.txt` shows anthropic accounts using `http://kiro-go:8080` and active API key/group |
| sub2api browser pages | PASS | `final/sub2api-admin-dashboard-final.png`, `final/sub2api-admin-accounts-final.png`, `final/sub2api-admin-usage-final.png`, `final/sub2api-admin-groups-final.png` |
| Kiro-Go browser pages | PASS | `final/kiro-admin-dashboard.png`, `final/kiro-admin-claude-readiness.png` |
| sub2api -> Kiro-Go generation | REACHED, BLOCKED_BY_UPSTREAM | `final/sub2api-nonstream.headers`, `final/sub2api-db-after-marker.txt`, `final/kiro-request-logs-after-sub2api-marker.json` |
| Account risk amplification | PASS for gateway-side retry guard | Kiro-Go marker request logs show `attempts: 1`; previous 42-attempt scan is stopped |
| 10 x 100 real stream/non-stream | NOT RUN / BLOCKED | Upstream is returning suspicious temporary limits; running this now would burn more accounts and cannot reach 100% success |

## Root Cause From Latest Marker Test

The marker request through sub2api returned `502` at the sub2api edge, but DB evidence shows it reached the Kiro-Go account and Kiro-Go returned upstream temporary-limit evidence as a non-retryable `409`:

- sub2api request id: recorded in `final/sub2api-nonstream.headers`.
- sub2api error row: `final/sub2api-db-after-marker.txt`, `api_key_id=2`, `account_id=24`, `model=claude-opus-4-7`, `upstream_status_code=409`.
- upstream message: Kiro returned `Due to suspicious activity, we are imposing temporary limits...`.
- Kiro-Go request logs: `final/kiro-request-logs-after-sub2api-marker.json` shows `attempts: 1` for the failed upstream call.

This means the current failure is not a local payload/tool schema bug and not a sub2api reachability bug. It is upstream Kiro account-level temporary limiting. sub2api currently maps Kiro-Go's protective 409 to client-facing 502; that is a downstream error mapping issue, not Kiro-Go failing to receive traffic.

## Screenshot Analysis

- Kiro-Go dashboard screenshot shows service running, v1.0.8, 21 accounts, request counters, and account list.
- Kiro-Go Claude Code screenshot shows the expected readiness matrix and correctly labels official parity-sensitive features as `PARTIAL`.
- sub2api dashboard/accounts/usage/groups screenshots show authenticated admin pages, not login screens.
- No Playwright page errors or failed browser requests were recorded in `final/sub2api-playwright-summary-final.json`.

## Why PARTIAL Remains Correct

Official Anthropic parity cannot be marked full PASS for features that Kiro upstream does not natively prove:

- `count_tokens`: Kiro-Go estimates/compatibly returns token counts; exact official Anthropic count parity is not proven.
- `max_tokens:0`: local zero-output response shape works; upstream cache warmup proof is not available.
- Fine-grained tool streaming: Kiro-Go can emit Anthropic-shaped `input_json_delta` from complete Kiro tool input, but true upstream partial JSON parity depends on Kiro stream shape.
- Assistant prefill: text prefill is emulated; tool-use prefill is rejected.

## Operator Notes

Do not run 10x100 real upstream tests while Kiro is returning suspicious temporary limits. Use cooldown/admission evidence first, then rerun a small smoke, then scale gradually. A 100% pass claim is only valid after upstream returns normal content for both stream and non-stream paths.
