# UAT Result: Best-Practice Opus 4.7 Validation

Verdict: PARTIAL IMPROVEMENT VERIFIED, full 100/100 still not PASS.

Date: 2026-05-21 Asia/Shanghai

## External Research Applied

- `kiro-gateway`: useful patterns are account failover, bounded retry, cooldown, token refresh, and first-token stream retry. It is not copied directly because some 5xx handling paths are not safe for this stricter UAT gate, and Anthropic streaming may emit `message_start` before first real upstream content, which is unsafe as a replay boundary.
- `kiro-account-manager`: public README documents Kiro account management, automatic token refresh, quota/status display, balance-based switching, OpenAI/Anthropic-compatible gateway, multi-account load balancing, prompt caching, request logs, and client setup. Its FAQ also treats expired bearer tokens as refreshable operational state, which supports keeping refresh-token-capable accounts schedulable long enough for Kiro-Go to refresh them on request.
- Claude/Anthropic gateway best practice applied here: avoid retry amplification, respect overload/capacity signals, ramp traffic gradually, and stop or wait while readiness reports no safe capacity.

## Optimizations Implemented

- UAT runner now supports gradual traffic controls:
  - `RAMP_UP`
  - `REQUEST_SPACING_MS`
  - `READINESS_CHECK_EVERY`
  - `CAPACITY_RECOVERY_WAIT_SECONDS`
  - `RESUME_AFTER_READINESS_BLOCK`
- Readiness checkpoints now write aggregate readiness fields only, not full account lists.
- UAT content validation now requires the per-request `ok <index>` marker in real assistant text or stream delta content. This prevents HTTP 200 responses with unrelated, stale, truncated, or fallback text from being counted as PASS.
- UAT stream validation now rejects empty SSE shells: `message_start` + `message_stop` without a real `content_block_delta` no longer counts as valid content.
- PASS criteria remain strict: HTTP 200 is insufficient; every response must contain real assistant content and no capacity fallback text.

## Validation Runs

### Conservative Smoke

- 5/5 non-stream PASS
- 5/5 stream PASS
- Real path: sub2api -> Kiro-Go -> upstream

### Best-Practice 100/100 Attempt

Directory: `docs/superpowers/uat/sub2api-opus47-stable-200/run-bestpractice-20260521193046`

- Non-stream reached 70/70 real PASS before readiness became blocked.
- Stream did not run in that directory.
- Runner stopped at readiness checkpoint with `BLOCKED_BY_UPSTREAM_CAPACITY`.
- This is not PASS, but it proves the paced mode improved from the prior run, which blocked after 5 real non-stream successes.

### Resume Strategy Smoke

Directory: `docs/superpowers/uat/sub2api-opus47-stable-200/resume-smoke-20260521193651`

- 20/20 non-stream PASS
- 20/20 stream PASS
- No fallback content, no forbidden status, no content failures.
- DB evidence for temporary key id 13 shows aggregate real usage rows for 95 non-stream and 25 stream calls across the two best-practice validations.

## Final Status

The best-practice optimization is valid and materially improves stability, but this session did not produce a full 100/100 non-stream + 100/100 stream PASS. The correct UAT status remains not PASS until one complete run reaches 100 real non-stream and 100 real stream responses without capacity fallback or readiness block.

After the stricter marker gate, previous non-stream samples still show the requested marker in their captured samples, but prior stream evidence samples are truncated around `message_start` and cannot prove marker correctness from the stored sample alone. Future stream PASS evidence must be generated with the updated runner so full SSE delta content is parsed for the marker before passing.

## Structural Validation Follow-up

The updated runner was verified against local mock responses:

- Valid stream with `content_block_delta` containing `ok <index>` returns PASS.
- Empty stream envelope with only `message_start` and `message_stop` returns FAIL with `sse body has no real content delta`.
- Stream with real content but wrong marker returns FAIL with `markerOk=false`.

This confirms the UAT gate now validates response structure and response identity, not only transport success.

## Evidence

- Best-practice run summary: `docs/superpowers/uat/sub2api-opus47-stable-200/run-bestpractice-20260521193046/summary.json`
- Best-practice non-stream evidence: `docs/superpowers/uat/sub2api-opus47-stable-200/run-bestpractice-20260521193046/non-stream.jsonl`
- Resume smoke summary: `docs/superpowers/uat/sub2api-opus47-stable-200/resume-smoke-20260521193651/summary.json`
- Resume smoke stream evidence: `docs/superpowers/uat/sub2api-opus47-stable-200/resume-smoke-20260521193651/stream.jsonl`
- Resume DB evidence: `docs/superpowers/uat/sub2api-opus47-stable-200/resume-smoke-20260521193651/evidence/db-usage-api-key-13.txt`
- Temporary key cleanup: `docs/superpowers/uat/sub2api-opus47-stable-200/resume-smoke-20260521193651/evidence/db-key-cleanup.txt`
- Redacted logs: `docs/superpowers/uat/sub2api-opus47-stable-200/resume-smoke-20260521193651/evidence/kiro-go-redacted.log`, `docs/superpowers/uat/sub2api-opus47-stable-200/resume-smoke-20260521193651/evidence/sub2api-redacted.log`
