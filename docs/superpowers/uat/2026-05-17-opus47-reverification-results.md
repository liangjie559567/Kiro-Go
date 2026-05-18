# 2026-05-17 Opus 4-7 Reverification

Run ID: `opus47-reverify-20260517-111005`

Scope:
- Docker production runtime.
- `/www/sub2api` downstream calling Kiro-Go through `http://127.0.0.1:18080/v1/messages`.
- Model fixed to `claude-opus-4-7`.
- 100 non-stream requests at concurrency 10.
- 100 stream requests at concurrency 10.
- Per-request content correctness and latency.
- sub2api Postgres usage logging.
- Real Chrome/Playwright browser screenshot evidence.

## Verdict

Overall: **FAIL for full content-correctness pass criteria**.

The deployed chain is callable and stable at the transport/protocol layer:
- Docker services were running: `kiro-go-kiro-go-1`, `sub2api`, `sub2api-postgres`, `sub2api-redis`.
- `GET http://127.0.0.1:8080/health` returned Kiro-Go `1.0.8`.
- `GET http://127.0.0.1:18080/health` returned OK.
- All load-test requests returned HTTP 200.
- No `SYSTEM PROMPT`, `END SYSTEM PROMPT`, `<thinking_mode>`, `x-anthropic-billing-header`, or Claude Code fingerprint leaked in model output.
- Stream responses contained the required SSE event set in checked failure samples.

It does not meet the requested "each response content is correct" bar:
- Spoofed-prompt defense run: sync `86/100`, stream `95/100`.
- Clean marker baseline: sync `98/100`, stream `99/100`.
- Failures were model refusals such as `I can't discuss that` / `I can't return that marker`, not HTTP, database, or SSE transport failures.

## Main Load Result

Artifact directory:

`docs/superpowers/uat/opus47-reverify-20260517-111005/`

This run intentionally included spoofed system prompt text in the user prompt to verify injection stripping and leakage behavior.

| Mode | Total | Concurrency | HTTP 200 | Correct | Exact | Leaks | Failed | p50 | p90 | p95 | p99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| sync | 100 | 10 | 100 | 86 | 86 | 0 | 14 | 9344 ms | 20643 ms | 36836 ms | 38109 ms | 38145 ms |
| stream | 100 | 10 | 100 | 95 | 94 | 0 | 5 | 6821 ms | 41801 ms | 44398 ms | 47543 ms | 49849 ms |

Representative failed content:
- Sync index 12: `I can't return that marker.`
- Sync index 27: `I can't help with that.`
- Stream index 41: `I can't discuss that.`

Stream failed samples still had:
- `message_start`
- `content_block_start`
- `content_block_stop`
- `message_delta`
- `message_stop`
- zero SSE parse errors
- no SSE error event

## Clean Baseline

To isolate proxy health from spoofed-prompt refusal behavior, I also ran clean marker prompts without fake system prompt text.

Artifacts:
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-clean-sync-20260517-111546-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/opus47-clean-stream-20260517-111801-summary.json`

| Mode | Total | Concurrency | HTTP 200 | Correct | Failed | p50 | p90 | p95 | p99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| clean sync | 100 | 10 | 100 | 98 | 2 | 3509 ms | 35616 ms | 41051 ms | 43212 ms | 49126 ms |
| clean stream | 100 | 10 | 100 | 99 | 1 | 5599 ms | 34944 ms | 37123 ms | 42832 ms | 43011 ms |

Clean failures were also model refusals to echo a marker.

## Database Evidence

For the main spoofed-prompt run window `2026-05-17 11:10:05+08` to `11:14:50+08`, sub2api Postgres `usage_logs` showed:

| Rows | Stream | Sync | First Seen | Last Seen | Avg | p50 | p95 | Max |
| ---: | ---: | ---: | --- | --- | ---: | ---: | ---: | ---: |
| 200 | 100 | 100 | 2026-05-17 11:10:06+08 | 2026-05-17 11:14:48+08 | 13256 ms | 8101.5 ms | 41835 ms | 49842 ms |

Grouped by stream:

| Stream | Rows | Min | p50 | p95 | Max | Input Tokens | Output Tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| false | 100 | 1068 ms | 9423 ms | 36812.4 ms | 38132 ms | 592573 | 1818 |
| true | 100 | 979 ms | 7273.5 ms | 44398.5 ms | 49842 ms | 592454 | 1730 |

Account row after run:

`kiro_claude_01` account `24`: `active`, `schedulable=true`, `concurrency=12`, `last_used_at=2026-05-17 11:14:48+08`, no rate-limit timestamp.

Additional DB window covering the clean baseline showed `200` sync rows and `200` stream rows for account `24` and model `claude-opus-4-7`.

## Browser Evidence

Real Chrome/Playwright artifacts:

- `docs/superpowers/uat/opus47-reverify-20260517-111005/playwright/sub2api-usage-opus47-reverify.png`
- `docs/superpowers/uat/opus47-reverify-20260517-111005/playwright/browser-summary.json`
- `docs/superpowers/uat/opus47-reverify-20260517-111005/playwright/sub2api-usage-opus47-reverify-text.txt`

Browser/API checks:
- Admin login succeeded through real sub2api auth.
- Usage page rendered and screenshot was captured.
- Direct admin usage API from the browser session returned HTTP 200 for `account_id=24`.
- Returned rows included `claude-opus-4-7`, `/v1/messages` inbound/upstream, stream rows, and recent timestamps around `11:20`.

Browser UI filtering did **not** pass:
- The onboarding tour overlay intercepted clicks during the account dropdown interaction.
- After removing overlay nodes, the script could click the account option, but the page did not reliably render filtered rows before screenshot.
- Therefore the browser screenshot proves the Usage page renders and model distribution includes `claude-opus-4-7`, while the filtered account view is supported by browser-session API evidence rather than visible filtered rows.

## Verification Commands

Unit tests:

`go test ./...`

Result: passed.

## Findings

1. The sub2api -> Kiro-Go -> Opus 4-7 route is production-callable under 10 concurrency and logs to Postgres.
2. HTTP and SSE protocol health are good in this run: no 429/5xx surfaced to the client and no SSE parse failures in sampled failures.
3. Content correctness is not 100%. Opus 4-7 sometimes refuses exact marker echo tasks, even without spoofed system prompt text.
4. The spoofed-prompt defense is effective for leakage: zero forbidden-marker output leaks across 200 requests.
5. Tail latency is high under concurrency 10, with p95 around 37-44s depending on mode.
6. The sub2api Usage page onboarding tour is still a browser automation obstacle and should be disabled in admin sessions used for validation, or the tour completion key should be made stable/documented.
