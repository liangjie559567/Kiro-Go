# Claude Code Parity Layer UAT

Date: 2026-05-16

## Automated Verification

- `go test ./...`: PASS
- `go build ./...`: PASS
- Targeted Claude compatibility tests: PASS
- Targeted account breaker tests: PASS

## Verified Areas

- Anthropic envelope parsing captures beta headers, request IDs, unknown fields, and `tool_reference`.
- Claude SSE writer emits valid ordered events for text, tool use, ping, error, and final stop events.
- Claude tool references with schemas convert into Kiro tools while preserving outward tool names.
- Payload guard trims oversized history without orphan tool messages and rejects oversized current tool results before account selection.
- Per-model breaker and sticky account routing avoid breaker-open accounts and preserve active-connection accounting.
- Public `/v1/models` uses Anthropic-shaped model objects without local-only capability fields.
- Claude upstream 429 responses can include `Retry-After` when reset timing is available.

## Commands

```text
go test ./...
go build ./...
go test ./proxy -run 'TestParseAnthropicEnvelope|TestClaudeSSEWriter|TestClaudeToKiroExpandsToolReferences|TestGuardKiroPayload|TestPayloadGuard|TestAnthropicModelsResponse|TestClaudeCodeToolReferenceFixtureParses' -v
go test ./pool -run 'TestModelBreaker|TestBeginNextForModel' -v
```

## Manual Claude Code Smoke Plan

1. Run Kiro-Go locally on `http://localhost:8080`.
2. Run Claude Code with `ANTHROPIC_BASE_URL=http://localhost:8080`.
3. Send a normal prompt and confirm stream text appears.
4. Run again with `ENABLE_TOOL_SEARCH=true`.
5. Confirm request logs show beta flags, request IDs, selected account, first-token latency, and tool-reference count.

## Production Load Plan

- Non-stream: 100 requests at concurrency 10 through sub2api.
- Stream: 100 requests at concurrency 10 through sub2api.
- Success criteria: no protocol-invalid responses, no orphan tool messages, errors are classified as rate limit, overload, timeout, auth, billing, or invalid request.

## Results

- Automated verification completed before deployment.
- Manual Claude Code smoke and production load results should be appended after deployment.
