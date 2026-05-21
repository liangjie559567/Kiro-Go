# UAT Result: Opus 4.7 sub2api fix and proxy research

Date: 2026-05-21 17:45-17:54 Asia/Shanghai

Verdict: PARTIAL PASS.

The fix passes the targeted regression: Opus 4.7 pressure no longer turns into sub2api `502 upstream_error/context canceled` in the verified window. The requested `100/100 non-stream + 100/100 stream` full real-content pressure target is still NOT PASS because upstream continued returning capacity and temporary-limit signals.

## Code Fix

- `pool/account.go`: suspicious temporary-limit single-account base cooldown increased from 3s to 60s.
- `proxy/content_continuity.go`: stable downstream continuity wait can now respect the per-request Opus budget.
- `proxy/handler.go`: stable downstream admission/open-circuit wait now returns bounded stable fallback instead of waiting until client cancellation.
- Tests updated in `pool` and `proxy`.

Verification:

- `go test ./pool ./proxy`: PASS
- Evidence: `logs/code-diff.patch`

## Docker Health

- Kiro-Go Docker service rebuilt and restarted.
- `api/kiro-health.json`: PASS
- `api/sub2api-health.json`: PASS
- `logs/docker-ps.txt`: Kiro-Go and sub2api healthy.

## Browser Evidence

Playwright-MCP verified the real admin UI:

- `screenshots/kiro-go-admin-after-opus47-fix-202605211746.png`
- `screenshots/kiro-go-settings-proxy-after-opus47-fix-202605211746.png`
- `screenshots/kiro-go-proxy-section-after-opus47-fix-202605211747.png`

Screenshot analysis:

- Kiro-Go admin page renders and reports running status.
- Settings page renders.
- Proxy section is present and currently shows direct connection, so this run did not use a proxy.

## API / sub2api Verification

Temporary UAT key:

- Created as api key id `9`; only hash recorded.
- Disabled and soft-deleted after verification.
- Evidence: `db/uat-key-created.txt`, `db/uat-key-cleanup.txt`

Single smoke through `sub2api -> Kiro-Go`:

- Non-stream: HTTP 200, stable fallback after Opus budget exhausted, no 502.
- Stream: HTTP 200, real marker present, `message_stop` present.
- Evidence: `api/smoke-summary.json`

Low-pressure 5+5 probe through `sub2api -> Kiro-Go`:

- Total: 10
- HTTP 200: 10
- HTTP 502: 0
- Real marker responses: 7
- Stable fallback responses: 3
- Stream `message_stop`: 5/5
- Evidence: `api/load-5x5-summary.json`

## DB Evidence

Post-fix window:

- `db/postfix-error-aggregate.txt`: no `ops_error_logs` rows for Opus 4.7 in the verification window.
- `db/postfix-usage-aggregate.txt`: 12 usage rows for `claude-opus-4.7`, all routed to sub2api account `24`, group `1`.

This confirms the verified requests reached the intended sub2api account path and did not create new sub2api error rows.

## Upstream Evidence

Kiro-Go logs still show upstream pressure during the verification window:

- `INSUFFICIENT_MODEL_CAPACITY`
- `temporary limits` / `suspicious activity`
- token/model-cache 429s

Evidence: `logs/kiro-go-since-fix.log`

This means the gateway fix is working by closing pressured turns cleanly for sub2api/Claude Code, not by making upstream Opus 4.7 capacity reliable.

## Proxy / IP Conclusion

Research notes: `research/open-source-research.md`

Conclusion:

- Dynamic proxy IP is not proven as the root cause or complete fix.
- It may help only with exit-IP reputation components of temporary limits.
- It will not solve `INSUFFICIENT_MODEL_CAPACITY`.
- Kiro-Go already supports static global and per-account outbound proxies; this UAT did not validate dynamic proxy rotation because no proxy pool endpoints were provided.

## Final Status

PASS for the regression fix: no new sub2api 502/context-canceled errors in the verified Opus 4.7 window.

FAIL/PARTIAL for the original full pressure requirement: do not mark `100/100 non-stream + 100/100 stream` as PASS. Current upstream evidence still shows Opus 4.7 capacity and temporary-limit pressure, and 3/10 low-pressure requests used stable fallback rather than real model content.
