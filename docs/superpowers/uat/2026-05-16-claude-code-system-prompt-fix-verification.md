# Claude Code System Prompt Fix Verification - 2026-05-16

## Scope

Validate that Kiro-Go no longer forwards Claude Code or Anthropic `system` content to Kiro using spoofable `SYSTEM PROMPT` boundary markers, while keeping `/www/sub2api` downstream callable.

## Code Verification

- `go test ./proxy -run 'TestClaudeCode.*Prompt|TestClaudeToKiroEmbedsSystemPromptWithoutSpoofableBoundaryMarkers' -count=1`
  - Result: PASS
- `go test ./...`
  - Result: PASS
- `go build ./...`
  - Result: PASS

## Runtime Verification

- Rebuilt and restarted Kiro-Go with `docker compose up -d --build`.
- `GET http://127.0.0.1:8080/health`
  - Result: HTTP 200
- `GET /admin/api/claude-code/compat`
  - Result: `modelAdmission.default=16/300`, `claude-opus-4.7=10/300`
- Kiro-Go `/v1/models`
  - Result: HTTP 200, 29 models
- sub2api `/v1/models`
  - Result: HTTP 200, 31 models

## Real Downstream Calls Through sub2api

All calls used `http://127.0.0.1:18080/v1/messages` with the local sub2api key.

### Claude Code CLI

- Command: `claude -p --bare --no-session-persistence --model claude-sonnet-4.5`
- Environment: `ANTHROPIC_BASE_URL=http://127.0.0.1:18080`
- Result:
  - `type=result`
  - `subtype=success`
  - output: `CLI链路验证通过`
  - duration: 4823 ms

### Structured Claude Code System Prompt

- Non-stream request with structured `system` blocks:
  - HTTP 200
  - output: `修复验证通过`
  - spoof-warning detector: false
- 20 requests, concurrency 5:
  - success: 20
  - failed: 0
  - spoof warnings: 0
  - p50: 1419 ms
  - p90: 2183 ms
  - p95: 2401 ms
  - max: 12816 ms
- Tool call request:
  - HTTP 200
  - `stop_reason=tool_use`
  - tool name preserved: `get_project_status`
  - spoof-warning detector: false
- Streaming request:
  - events: 6
  - content deltas: 1
  - `message_stop`: 1
  - warnings: 0

## Kiro-Go Request Stats Snapshot

- total: 33
- success: 33
- failed: 0
- `/v1/messages`: 32 total, 32 success, 0 failed
- `claude-sonnet-4.5`: 23 total, 23 success, 0 failed

## Conclusion

The original failure mode was reproduced by unit test and fixed. Real Claude Code, raw Anthropic Messages, streaming, and tool-use calls through `/www/sub2api` no longer trigger the Kiro upstream spoofed-system-prompt warning in this verification run.
