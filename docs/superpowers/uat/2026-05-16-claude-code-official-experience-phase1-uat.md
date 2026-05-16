# Claude Code Official Experience Phase 1 UAT

Date: 2026-05-16

## Scope

- Kiro-Go verification-first phase for Claude Code official API experience.
- Hard compatibility gate: `/www/sub2api -> Kiro-Go -> Kiro` must remain genuinely callable through the local sub2api route.
- Focus areas: token estimator coverage, Claude SSE compatibility, Claude Code envelope/log metadata, and real downstream sub2api smoke coverage.
- This UAT does not change routing, service wiring, or local sub2api/Kiro configuration.

## Focused Go Verification

Run:

```bash
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludesToolReferencesAndToolCacheControl|TestEstimateClaudeRequestInputTokensIncludesThinkingBudget|TestClaudeSSEWriter|TestClaudeCode2143WireFixtureParsesAndPreservesCompatibilityFields|TestClaudeCodeToolReferenceFixtureParses|TestRequestLogCapturesClaudeCodeCompatibilityMetadata' -count=1 -v
```

Expected:

- Token estimator tests pass.
- Claude SSE writer golden tests pass.
- Claude Code fixture parsing tests pass.
- Request log compatibility metadata tests pass.

## Full Go Verification

Run:

```bash
go test ./...
```

Expected:

- All packages pass.
- No regression breaks existing Kiro-Go proxy, admin, config, or translator behavior.

## sub2api Real Downstream Smoke

Preconditions:

- Kiro-Go is reachable through the existing local service path.
- sub2api is reachable at `http://127.0.0.1:18080` unless `SUB2API_BASE` is set.
- `/tmp/sub2api_claude_key` contains the local sub2api Claude API key unless `SUB2API_KEY_FILE` is set.
- The selected model is available through sub2api and Kiro. Default: `claude-sonnet-4.5`.
- The real route `/www/sub2api -> Kiro-Go -> Kiro` must be used; do not substitute a mock or alternate endpoint for this gate.

Run:

```bash
node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Optional overrides:

```bash
SUB2API_BASE=http://127.0.0.1:18080 \
SUB2API_MODEL=claude-sonnet-4.5 \
SUB2API_KEY_FILE=/tmp/sub2api_claude_key \
SUB2API_SMOKE_OUT=/www/Kiro-Go/docs/superpowers/uat/sub2api-smoke \
node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Expected:

- `/v1/models` returns HTTP 200 through sub2api.
- `/v1/messages/count_tokens` returns HTTP 200 with positive `input_tokens`.
- Non-stream `/v1/messages` returns the exact generated marker.
- Stream `/v1/messages` returns the exact generated marker and includes `message_stop`.
- The script writes a JSON artifact under `SUB2API_SMOKE_OUT`.
- The script exits nonzero if models or count_tokens fail, if sync or stream output differs from the marker, or if stream output lacks `message_stop`.

## Optional sub2api 100x10 Regression

Run this for scheduler, admission, error-mapping, or stream behavior changes:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-sonnet-4.5 phase1-sync-$(date +%Y%m%d%H%M%S)
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 100 10 claude-sonnet-4.5 phase1-stream-$(date +%Y%m%d%H%M%S)
```

Expected:

- No protocol errors.
- Content correctness is preserved.
- Stream responses include `message_stop`.
- Any 429/529/concurrency failure is attributable to explicit sub2api or Kiro-Go admission limits, not malformed Anthropic-compatible responses.

## Results

Record command outputs and artifact paths here after execution.

- Focused Go verification:
- Full Go verification:
- sub2api smoke artifact:
- Optional 100x10 artifacts:
- Compatibility gate result:
