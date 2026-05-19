# Kiro-Go Layered Claude Code Readiness UAT

Date: 2026-05-19
Commit under test: `2ca906d` plus `bb0c3a6`, `422117f`, `b528e89`, `0b70f23`

## Verdict

PASS for Kiro-Go layered readiness delivery and downstream `/www/sub2api/` compatibility smoke on a supported Claude model.

Current 10-concurrency/100-request upstream stress UAT is BLOCKED by real Kiro upstream account limits. This is not marked PASS because the latest run produced upstream temporary-limit failures, and the UAT requirement explicitly requires real 100% correct content.

The four previous `PARTIAL` items are intentionally not promoted to unconditional official parity:

- `assistantPrefill`: Claude Code compatibility is `EMULATED_PASS`; official native upstream prefill remains `PARTIAL`.
- `countTokens`: Claude Code compatibility is `PASS` with estimated token counting; official exact upstream token count remains `PARTIAL`.
- `fineGrainedToolStreaming`: Claude Code compatibility is `PASS`; true upstream partial JSON parity remains `PARTIAL`.
- `maxTokensZero`: Claude Code compatibility is `PASS`; official cache warmup is `BLOCKED_BY_UPSTREAM`.

## Docker Health

- Command: `docker compose ps`
- Kiro-Go container: `kiro-go-kiro-go-1`, status `Up`, port `0.0.0.0:8080->8080/tcp`
- Health: `GET http://127.0.0.1:8080/health`
- Response: `{"status":"ok","uptime":498,"version":"1.0.8"}`

## Direct Kiro-Go API

Artifact: `docs/superpowers/uat/kiro-layered-readiness-20260519/direct-api.json`

- `/v1/messages/count_tokens`: `200`, `input_tokens=25`
- `/v1/messages` with `max_tokens=0`: `200`, empty content, `stop_reason=max_tokens`, `output_tokens=0`
- `/v1/messages` non-stream: `200`, exact marker returned
- `/v1/messages` stream: `200`, exact marker returned, `message_start` and `message_stop` present
- `/admin/api/claude-code/readiness`: layered objects present for all target capabilities with `status/detail`, `claudeCodeCompatibility`, `officialAnthropicParity`, and `evidence`

## sub2api Downstream

Correct downstream Claude key path used for tests: `/tmp/sub2api_claude_real_key` (secret not recorded here).

Artifacts:

- Failed wrong/default key proof: `sub2api-layered-1779190355.json`
- Failed unsupported model proof: `sub2api-layered-real-1779190526.json`
- Passing supported model proof: `sub2api-layered-real-opus-1779190568.json`

Result with supported model `claude-opus-4-7`:

- `/v1/models`: `200`, `modelCount=13`
- `/v1/messages/count_tokens`: `200`, `input_tokens=28`
- `/v1/messages` non-stream: `200`, exact marker returned
- `/v1/messages` stream: `200`, exact marker returned, `message_start` and `message_stop` present
- sub2api log selected `api_key_id=2`, `group_id=1`, `account_id=24`, `platform=anthropic`

Root cause for the earlier sub2api 503:

- First run used the wrong key and routed to `api_key_id=1`, `group=openai`, so it never reached Kiro-Go as Claude traffic.
- Correct key with `claude-sonnet-4.5` still failed because sub2api account `24` is active and schedulable, but its `model_mapping` does not include `claude-sonnet-4.5`.
- sub2api log evidence: `total=1 eligible=0 ... model_unsupported=1 sample_model_unsupported=[24]`.
- Database evidence: account `24` supports `claude-opus-4-7` and other mapped models, but not `claude-sonnet-4.5`.

Historical 10-concurrency/100-request downstream evidence remains valid for `claude-opus-4-7`:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/SUBSYNC1832-summary.json`: sync `100/100` correct, `0` failed
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/SUBSTR1833-summary.json`: stream `100/100` correct, `0` failed

Latest attempted 10-concurrency/100-request evidence:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/LAYEREDSYNC194217-summary.json`: sync `0/100` correct, `100` failed, all HTTP `502`
- sub2api selected account `24`, then Kiro-Go returned upstream `409`/temporary-limit style errors.
- Kiro-Go container logs show repeated upstream `429` from Kiro IDE for Opus 4.7, including temporary suspicious-activity limits and `INSUFFICIENT_MODEL_CAPACITY`.
- sub2api log root message: `No available accounts for claude-opus-4.7: upstream temporary limits are cooling down (TEMPORARY_LIMITED)`.
- Conclusion: current 100x10 cannot be honestly passed until the upstream Kiro account pool leaves temporary-limit/cooldown or additional healthy accounts are available.

## Browser UAT

Artifacts:

- `admin-readiness-full.png`
- `admin-readiness-panel.png`
- `admin-readiness-browser.json`
- `readiness-api-browser-precheck.json`

Playwright checks:

- Logged in to `http://127.0.0.1:8080/admin`
- Opened API tab
- Verified readiness DOM contains `Claude Code`, `Official API`, `Evidence`, `countTokens`, `assistantPrefill`, `fineGrainedToolStreaming`, and `maxTokensZero`
- Viewport `1440x1200`, `bodyWidth=1440`, `horizontalOverflow=false`

Screenshot analysis:

- The readiness panel renders the layered capability rows without overlap or clipping.
- Status badges are visible and readable.
- The UI correctly distinguishes Claude Code compatibility from official upstream parity.
- `maxTokensZero` correctly shows official parity as `BLOCKED_BY_UPSTREAM`, not false `PASS`.

## Final Notes

Kiro-Go now exposes evidence-based readiness instead of hiding uncertainty behind a single status. This is the correct behavior for matching Claude Code operational needs while staying honest about upstream Kiro limits that cannot be proven equivalent to the official Anthropic API.

The remaining operational blocker is upstream account capacity/temporary limiting, not the layered readiness UI/API implementation.
