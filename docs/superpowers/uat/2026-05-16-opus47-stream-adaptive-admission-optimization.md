# Opus 4.7 Stream Adaptive Admission Optimization

Date: 2026-05-16

## Research Inputs

Official documentation checked during this pass:

- Anthropic Messages API streaming: stream responses are SSE event sequences and clients expect protocol-shaped events, including terminal events.
- Anthropic prompt caching and tool-use docs: Claude-compatible gateways must preserve request features such as `cache_control`, tool definitions, tool-use blocks, and stream shape.
- Claude Code LLM gateway guidance: Claude Code expects a compatible Anthropic base URL, auth token, model discovery or stable model naming, and streaming behavior close to the official API.
- Kiro hooks/MCP/steering docs: Kiro can inject hook and context material into model context, so Kiro-originated context should be transported as neutral context instead of being synthesized into a fake `system prompt` header.

Open-source gateway patterns reviewed:

- `musistudio/claude-code-router`
- `1rgs/claude-code-proxy`
- `zed-industries/claude-code-acp`
- `justin-schroeder/Code-Proxy`
- LiteLLM proxy/provider patterns for Anthropic-compatible gateways

Common relevant patterns:

- Keep streaming responses protocol-correct even on upstream errors.
- Prefer model aliases/model discovery so clients do not depend on one provider's exact spelling.
- Apply backpressure before upstream capacity errors become downstream failures.
- Keep test prompts neutral; model-identifying markers can trigger refusal behavior unrelated to transport correctness.

## Optimization Implemented

Before this change, `modelAdmissionGate.recordPressure()` tracked upstream pressure from stream and non-stream requests, but `acquireOpus47Admission()` bypassed admission for all stream requests.

New behavior:

- Normal stream requests still bypass admission for lowest latency.
- If the same model has active admission pressure, stream requests enter the model admission gate.
- Under high pressure, the existing adaptive gate can temporarily reduce the model to one upstream slot.
- This limits Opus 4.7 stream fan-out when upstream is returning `429`, `5xx`, or long-latency responses.

Files changed:

- `proxy/opus_gate.go`
  - Added `modelAdmissionGateSet.hasPressure(model string) bool`.
- `proxy/handler.go`
  - Changed stream bypass to apply only when the model has no active pressure.
- `proxy/handler_test.go`
  - Added regression coverage for pressure-gated stream admission.

## Verification

Targeted failing test before implementation:

```bash
go test ./proxy -run TestAcquireAdmissionGatesStreamOnlyWhenModelHasPressure -count=1
```

Observed before fix:

```text
expected pressured stream to be gated and time out
```

Targeted tests after implementation:

```bash
go test ./proxy -run 'TestAcquireAdmissionGatesStreamOnlyWhenModelHasPressure|TestModelAdmissionGateAdaptivePressureTemporarilyReducesConcurrency|TestHandleClaudeStreamOpus47CapacityLimitNeverReturnsEmptyBodyUnderConcurrency|TestHandleClaudeStreamOpus47CapacityLimitReturnsExplicitError' -count=1
```

Result:

```text
ok  	kiro-go/proxy	0.052s
```

Full verification:

```bash
go test ./...
go build ./...
docker compose up -d --build
curl -fsS http://127.0.0.1:8080/health
```

Results:

- `go test ./...`: passed after rerun. The first full run hit an intermittent `TempDir RemoveAll cleanup: directory not empty` in `TestHandleClaudeNativeWebSearchAccepts20260209ToolType`; the same test passed alone and the full suite passed on rerun.
- `go build ./...`: passed.
- Docker rebuild/restart: passed.
- Kiro-Go health: `{"status":"ok","uptime":8,"version":"1.0.8"}`.

Compatibility endpoint after restart:

```json
{
  "capabilities": {
    "adaptiveModelAdmission": true,
    "anthropicMessages": true,
    "countTokens": true,
    "fineGrainedToolStreaming": true,
    "modelAdmission": true,
    "models": true,
    "openAIChat": true,
    "openAIResponses": true,
    "promptCacheControl": true,
    "promptCaching": true,
    "requestLogs": true,
    "streaming": true,
    "thinking": true,
    "toolReferences": true,
    "toolSearch": true,
    "toolUse": true,
    "vision": true,
    "webSearch": true,
    "webSearch20260209": true
  },
  "modelAdmission": {
    "default": {
      "maxConcurrent": 16,
      "maxWaiting": 300
    },
    "models": {
      "claude-opus-4.7": {
        "maxConcurrent": 10,
        "maxWaiting": 300
      }
    }
  }
}
```

sub2api model listing after restart:

```json
{
  "count": 31,
  "has_opus47": true
}
```

## sub2api Real Smoke

Command:

```bash
RUN_ID=neutral-smoke-20260516233905
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 1 1 claude-opus-4-7 ${RUN_ID}-sync
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 1 1 claude-opus-4-7 ${RUN_ID}-stream
```

Results:

| Mode | HTTP 200 | Correct | Warning | Failed | Latency |
| --- | ---: | ---: | ---: | ---: | ---: |
| Non-stream | 1 | 1 | 0 | 0 | 1138ms |
| Stream | 1 | 1 | 0 | 0 | 1166ms |

Artifacts:

- `docs/superpowers/uat/sub2api-100x10-2026-05-16/neutral-smoke-20260516233905-sync-summary.json`
- `docs/superpowers/uat/sub2api-100x10-2026-05-16/neutral-smoke-20260516233905-stream-summary.json`

## Test Prompt Risk Found

A post-restart smoke using run ID `opus47-post-admission-...` returned HTTP 200 but content validation failed because the model refused to return a marker that looked like a model/build identifier.

This was not a gateway failure. It was a test prompt design issue. Future load-test run IDs should avoid model/provider-identifying strings in markers. Use neutral IDs such as `neutral-smoke-*`, `load-a-*`, or random base32 IDs.

## Verdict

The optimization preserves `/www/sub2api` downstream usability:

- Kiro-Go rebuilt and restarted.
- sub2api still lists `claude-opus-4-7`.
- Real non-stream and stream Opus 4.7 requests through sub2api succeeded with exact content.
- Normal stream path remains low-latency.
- Stream path now participates in adaptive admission only after active model pressure is detected.
