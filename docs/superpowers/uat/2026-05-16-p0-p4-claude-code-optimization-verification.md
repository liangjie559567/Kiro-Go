# P0-P4 Claude Code Optimization Verification - 2026-05-16

## Scope

Implement and verify the first complete P0-P4 optimization slice for Claude Code downstream usage through `/www/sub2api` into local Kiro-Go.

## Implemented

- P0: Added a Claude Code 2.1.143 wire fixture covering structured `system`, `tool_reference`, `cache_control`, `eager_input_streaming`, `web_search_20260209`, beta headers, session id, and agent id.
- P1: Added conservative adaptive model admission pressure tracking. The gate never exceeds configured concurrency, and temporarily contracts under 429/5xx/long-latency pressure.
- P2: Preserved `eager_input_streaming` on Claude tools and kept Anthropic fine-grained tool streaming capability advertised. Existing SSE writer emits `input_json_delta.partial_json` chunks for tool input.
- P3: Preserved typed tool `cache_control` so prompt cache tracking sees tool-level TTL hints.
- P4: Explicitly validated `web_search_20260209` routes through Kiro MCP web search and returns Anthropic web search blocks.

## Code Verification

- `go test ./proxy -run 'TestClaudeCode2143WireFixture|TestClaudeRequestPreservesToolCacheControl|TestPromptCacheTrackerUsesTypedToolCacheControl|TestHandleClaudeNativeWebSearchAccepts20260209ToolType|TestModelAdmissionGateAdaptivePressure|TestAdminClaudeCodeCompatibility' -count=1`
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
  - Result includes:
    - `adaptiveModelAdmission=true`
    - `fineGrainedToolStreaming=true`
    - `promptCacheControl=true`
    - `toolSearch=true`
    - `webSearch20260209=true`
    - `modelAdmission.default=16/300`
    - `claude-opus-4.7=10/300`

## Real `/www/sub2api` Downstream Verification

All requests used `http://127.0.0.1:18080`.

- Claude Code CLI:
  - Command: `claude -p --bare --no-session-persistence --model claude-sonnet-4.5`
  - Result: success
  - Output: `P0P4 CLI验证通过`
  - Duration: 3251 ms
- Plain Messages request:
  - Result: HTTP 200
  - Output: `最终普通验证通过`
  - Spoof warning: false
- Tool request using fixture tool with `cache_control` and `eager_input_streaming`:
  - Result: HTTP 200
  - `stop_reason=tool_use`
  - Tool name: `write_file`
- Streaming request using the Claude Code fixture shape:
  - Result: HTTP 200
  - SSE events: 9
  - `message_stop=1`
  - warnings: 0

## Kiro-Go Request Stats Snapshot

- total: 5
- success: 5
- failed: 0
- `/v1/messages`: 5 total, 5 success, 0 failed
- `claude-sonnet-4.5`: 5 total, 5 success, 0 failed
- average duration: 1872 ms
- max first token: 1366 ms
- toolUseCount: 1

## Notes

This slice preserves the existing `/www/sub2api` route and key. It does not require sub2api reconfiguration. The adaptive admission behavior is intentionally conservative and only reduces pressure below the configured model limit after recent upstream errors or long latency.
