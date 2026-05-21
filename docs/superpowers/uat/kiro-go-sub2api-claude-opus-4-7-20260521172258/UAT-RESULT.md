# UAT Result: Claude Code -> sub2api -> Kiro-Go, claude-opus-4.7

Date: 2026-05-21 17:22-17:31 Asia/Shanghai
Verdict: FAIL for requested 100/100 non-stream + 100/100 stream pressure test.

## Scope

- Downstream entry: sub2api `http://127.0.0.1:18080/v1/messages`
- Upstream target: Kiro-Go account `kiro_claude_01` / account id `24`
- Model: `claude-opus-4.7`
- Request shape: Claude Code style Anthropic Messages request with `anthropic-version`, `anthropic-beta: claude-code-20250219`, `x-claude-code-session-id`, `x-claude-code-agent-id`, and `claude-cli` user agent.

## Precheck

- Kiro-Go health: PASS, `api/kiro-health.json`
- sub2api health: PASS, `api/sub2api-health.json`
- Kiro-Go model list includes `claude-opus-4.7` and `claude-opus-4-7` aliases.
- sub2api account 24 includes mappings for `claude-opus-4.7` and `claude-opus-4-7`.

## Single Request Verification

Single non-stream: PASS

- Evidence: `api/single-nonstream.json`
- HTTP `200`
- response model `claude-opus-4.7`
- stop reason `end_turn`
- text marker `OPUS47_OK`

Single stream: PASS

- Evidence: `api/single-stream.json`
- HTTP `200`
- content type `text/event-stream`
- event sequence included `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`
- `has_message_stop: true`
- text marker `OPUS47_STREAM_OK`

## Pressure Test Result

The requested `100/100 non-stream + 100/100 stream` validation did not pass.

During the non-stream 100-run attempt, failures started immediately. The first 10 concurrent requests failed client-side after timeout/abort, and the run continued to accumulate failures. Because 100/100 was already impossible, the pressure run was terminated to avoid further upstream capacity impact.

No `load-summary.json` was produced because the run was intentionally stopped after the failure condition was established.

## Failure Evidence

Database error aggregate: `db/error-aggregate.txt`

- errors for UAT key: `50`
- HTTP `502`: `50`
- upstream request failed: `50`

Recent DB error rows: `db/error-logs.txt`

- selected account: `24`
- group: `1`
- platform: `anthropic`
- model: `claude-opus-4.7`
- error phase: `upstream`
- error type: `upstream_error`
- status: `502`
- upstream error excerpt: `Post "http://kiro-go:8080/v1/messages?beta=true": context canceled`

Kiro-Go logs: `logs/kiro-go-after-opus47.log`

- repeated upstream `429` entries during the test window
- includes `Too many requests, please wait before trying again`
- includes account temporary-limit warnings from Kiro upstream

sub2api logs: `logs/sub2api-after-opus47.log`

- requests selected account 24 correctly
- failing requests completed as HTTP `502` after about 50s
- failures were upstream context cancellation, not route selection failure

Usage aggregate: `db/usage-aggregate.txt`

- successful usage rows for UAT key: `3`
- account id `24`: `3`
- non-stream rows: `2`
- stream rows: `1`

This confirms route selection can work, but pressure does not sustain 100/100 for this model.

## Browser Evidence

- `screenshots/kiro-go-admin-opus47-fail-202605211731.png`
  - Kiro-Go admin was reachable and running after the failed pressure attempt.

## Secret Handling

The temporary UAT key was disabled/soft-deleted after the run; see `db/uat-key-cleanup.txt`.
A secret scan against this UAT directory found no full user-supplied key, admin password, or temporary UAT key.

## Final Verdict

FAIL.

`claude-opus-4.7` single non-stream and single stream requests can pass through `sub2api -> Kiro-Go`, but the requested pressure target did not pass. The pressure run failed during the non-stream phase with upstream capacity/rate-limit symptoms: sub2api recorded HTTP `502 upstream_error/context canceled`, while Kiro-Go logged repeated upstream `429` and temporary-limit warnings.
