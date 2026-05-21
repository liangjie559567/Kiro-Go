# Kiro-Go Claude Code Bounded Retry Verification

Run directory: `docs/superpowers/uat/kiro-go-claude-code-bounded-retry-20260521170347`

Checked at: 2026-05-21 17:04 Asia/Shanghai

## Verdict

**FIX VERIFIED / NOT A 100x100 PRESSURE PASS**

The Claude/Anthropic StableDownstream path no longer waits indefinitely until the Claude Code client disconnects. After one configured content-continuity wait window, Kiro-Go now returns a complete stable fallback response for Claude-format Opus 4.7 sub2api/Claude Code requests when upstream capacity still has not recovered.

This verifies the bounded-retry fix. It does not convert the earlier full 100/100 non-stream plus 100/100 stream pressure UAT into PASS.

## Root Cause

The StableDownstream code was designed to hide retryable upstream `429/502/503` transport failures from sub2api/Claude Code and wait for real upstream content. The problem was that Claude-format paths could repeatedly re-enter content-continuity waits after timeout:

- `waitForStableClaudeCapacityWithHeartbeat` looped internally until success or client cancellation.
- Several Claude StableDownstream branches used `continue` after a timed-out wait.
- Admission-pressure paths could return without writing a complete response.
- The fallback text included wording equivalent to retrying the same turn.

Together, these behaviors matched the reported Claude Code symptom: calls could keep waiting/retrying instead of converging to a terminal response.

## Code Change

- `proxy/handler.go`
  - `waitForStableClaudeCapacityWithHeartbeat` now performs one bounded content-continuity wait instead of an internal infinite loop.
  - Claude StableDownstream attempt-budget, capacity timeout, no-accounts, stream-upstream-pressure, and admission-pressure branches now end with a complete stable fallback when capacity does not recover.
  - Stable fallback wording no longer says `Please retry this turn`.
  - Existing OpenAI/OpenAI Responses model-capacity breaker fix remains in place.

- `proxy/handler_test.go`
  - Updated StableDownstream tests to assert bounded fallback completion rather than waiting for client cancellation.
  - Added/kept regression coverage for OpenAI Responses model-capacity breaker recording.

## Verification

| Check | Result | Evidence |
| --- | --- | --- |
| Focused bounded-retry tests | PASS | `go test ./proxy -run 'Stable.*Bounded|Stable.*DoesNotLoop|Stable.*Fallback|Stable.*OpenCircuit|TestStableClaudeStreamCapacityWaitSendsPingBeforeMessageStart|TestHandleOpenAIResponsesModelCapacityRecordsModelBreaker' -count=1` |
| Broader tests | PASS | `go test ./pool ./proxy` |
| Token refresh isolation check | PASS | `go test ./proxy -run 'TestEnsureValidTokenCoalescesConcurrentRefreshesPerAccount|TestEnsureValidTokenRefreshesDifferentAccountsInParallel' -count=5` |
| Docker rebuild | PASS | `docker compose up -d --build kiro-go` |
| Kiro-Go health | PASS | `api/kiro-health.json`: `status=ok`, version `1.0.8` |
| sub2api health | PASS | `api/sub2api-health.json`: `status=ok` |
| Docker service health | PASS | `logs/docker-compose-ps.txt`: container `healthy` |
| Playwright-MCP admin UI | PASS | `screenshots/kiro-go-claude-code-bounded-retry-admin-202605211704.png` |
| Browser API evidence | PASS | `api/kiro-go-claude-code-bounded-retry-browser-api-202605211704.json` |
| Open-source research | COMPLETED | `research/open-source-research-summary.md` |

## Browser Screenshot Analysis

The Playwright-MCP screenshot shows the real Kiro-Go admin page at `http://127.0.0.1:8080/admin`, not a login page or browser error. Visible state after rebuild:

- Status: `运行中`
- Version: `v1.0.8`
- Accounts: 25
- Requests: 16184
- Successes: 6743
- Failures: 9441
- Tokens: 222.8M
- Credits: 1960.3

This validates the rebuilt service and admin UI reachability. It does not prove full pressure-pass.

## API Evidence

Browser-authenticated, sanitized API capture shows:

- `/admin/api/status`: reachable
- `/admin/api/stats`: request counters available
- `/admin/api/fleet/readiness?model=claude-opus-4-7`: `status=degraded`, `circuitState=closed`, `safeConcurrency=10`, `reasonCodes=["healthy"]`
- Claude Code readiness/model-readiness endpoints reachable

## Final Decision

The bounded-retry defect is fixed and verified by focused regression tests plus Docker/browser/API health checks.

The earlier 100/100 full pressure result remains **FAIL / NOT PASS** until a new real pressure run completes all required non-stream and stream requests with matching API/database/screenshot evidence.

