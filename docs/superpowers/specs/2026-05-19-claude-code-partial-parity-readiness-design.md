# Claude Code Partial Parity Readiness Design

Date: 2026-05-19

## Goal

Make Kiro-Go's Claude Code readiness output accurate, actionable, and closer to official Anthropic API behavior without overstating capabilities that Kiro upstream cannot prove.

The immediate target is the current readiness `PARTIAL` set:

- `assistantPrefill`
- `countTokens`
- `fineGrainedToolStreaming`
- `maxTokensZero`

The design must preserve downstream `/www/sub2api/` compatibility and must not regress the existing `/v1/messages`, `/v1/messages/count_tokens`, streaming, tool use, tool reference, request log, and Docker deployment paths.

## Research Summary

Official Anthropic and Claude Code documentation establishes the gateway baseline:

- Claude Code gateways need `/v1/messages`, `/v1/messages/count_tokens`, and Anthropic version/beta header handling.
- Assistant prefill is not uniformly supported by current Claude models. Official docs currently state that Claude Opus 4.7, Opus 4.6, and Sonnet 4.6 do not support prefill.
- Fine-grained tool streaming is an event-level feature using `input_json_delta.partial_json` and may emit invalid or incomplete JSON.
- `max_tokens: 0` is more than a local empty response when used for prompt cache warmup. Official parity requires upstream cache creation/read usage evidence.
- Official count tokens behavior is exact for the target Anthropic model and includes messages, system, tools, images, documents, and beta blocks.

Open-source gateway research points to the same implementation principles:

- `jwadow/kiro-gateway` implements `/v1/messages/count_tokens` as an estimate and says so in code comments.
- LiteLLM uses provider capability maps and either preserves, removes, or rejects unsupported parameters explicitly.
- LiteLLM preserves provider-specific cache-control fields only when the target provider supports them.
- LiteLLM's Anthropic stream adapter takes care not to drop `input_json_delta` chunks when converting from other provider streams.
- ccflare favors provider-native passthrough and separates compatibility routes from native routes.
- Portkey, ccflare, Helicone, and inference-gateway emphasize request history, retry visibility, provider capability awareness, and configurable fallback behavior.

## Problem

Kiro-Go currently exposes a single readiness status per capability. That forces two different meanings into one field:

- Can Claude Code use this behavior without breaking?
- Is the behavior identical to official Anthropic upstream semantics?

Those are not always the same. For example, Kiro-Go can emit Anthropic-shaped `input_json_delta` events for Claude Code, but it cannot prove that Kiro upstream generated the tool input as live partial JSON. Kiro-Go can return a valid empty response for `max_tokens=0`, but it cannot prove prompt cache warmup unless upstream returns cache usage.

Marking these as full `PASS` would make operator debugging worse. Leaving them only as `PARTIAL` hides that some paths are already good enough for Claude Code.

## Design

### 1. Split Readiness Into Two Layers

Each nuanced capability should expose two statuses:

- `claudeCodeCompatibility`: whether Claude Code can rely on Kiro-Go's behavior.
- `officialAnthropicParity`: whether Kiro-Go can prove official Anthropic API semantic equivalence.

Allowed status values:

- `PASS`: verified behavior is available.
- `EMULATED_PASS`: client-facing compatibility is provided by Kiro-Go, not native upstream behavior.
- `PARTIAL`: behavior exists but is incomplete or approximate.
- `UNSUPPORTED_BY_MODEL`: official model behavior rejects the feature.
- `BLOCKED_BY_UPSTREAM`: Kiro upstream does not expose the evidence or primitive needed for full parity.
- `FAIL`: known broken behavior.

The existing top-level `status` field remains for backward compatibility. It should be derived conservatively:

- `PASS` only if both layers are `PASS`, or when the capability is not an official semantic concern.
- `PARTIAL` when Claude Code compatibility passes by emulation but official parity is partial or blocked.
- `FAIL` only for known broken behavior.

### 2. Evidence-Driven Capability Details

Readiness should include recent request-log evidence for each capability where possible:

- `lastSeenAt`
- `lastRequestId`
- `mode`
- `model`
- `proof`

Examples:

- `countTokens.mode = estimated | calibrated | upstream_exact`
- `maxTokensZero.mode = local_zero_output | upstream_cache_warmup`
- `fineGrainedToolStreaming.mode = kiro_go_chunked_complete_input | upstream_partial_json`
- `assistantPrefill.mode = emulated_text_prefill | native_prefill | unsupported_by_model`

The UI should show the compatibility layer prominently and the official-parity layer as the reason why a row may remain `PARTIAL`.

### 3. Assistant Prefill Handling

Model-aware behavior:

- For models officially known not to support prefill, mark `officialAnthropicParity=UNSUPPORTED_BY_MODEL`.
- For final assistant text prefill, keep current Kiro-Go emulation: convert to a user continuation instruction.
- Mark this as `claudeCodeCompatibility=EMULATED_PASS`, not native `PASS`.
- For final assistant `tool_use` prefill, continue rejecting with a clear 400 unless a future upstream primitive can represent assistant-started tool state.

This prevents false claims for models where official Anthropic itself rejects prefill.

### 4. Count Tokens Handling

Keep the endpoint compatible with Claude Code:

- `POST /v1/messages/count_tokens` continues returning `{"input_tokens": int}`.
- The request log stores `countTokensMode=estimated` by default.
- Readiness marks `claudeCodeCompatibility=PASS` because Claude Code can use the endpoint for compaction heuristics.
- Readiness marks `officialAnthropicParity=PARTIAL` unless strict exact counting is configured and verified.

Future optional modes:

- `estimated`: current local estimator.
- `calibrated`: estimator corrected with recent upstream usage from completed requests.
- `upstream_exact`: only if Kiro or a configured Anthropic-compatible backend provides exact count-token results.

### 5. Fine-Grained Tool Streaming Handling

Keep the current Anthropic SSE shape:

- `content_block_start` with `tool_use` and empty input.
- one or more `content_block_delta` events with `input_json_delta.partial_json`.
- `content_block_stop`.

Readiness should distinguish:

- `claudeCodeCompatibility=PASS` when recent streams emitted valid `input_json_delta` events.
- `officialAnthropicParity=PARTIAL` when Kiro-Go chunked a complete Kiro tool input after the fact.
- `officialAnthropicParity=PASS` only if request logs prove Kiro upstream delivered real partial tool input deltas.

### 6. max_tokens=0 Handling

Keep local zero-output compatibility:

- Return a valid Anthropic message response.
- Empty `content`.
- `stop_reason=max_tokens`.
- `output_tokens=0`.

Readiness should distinguish:

- `claudeCodeCompatibility=PASS` for response shape.
- `officialAnthropicParity=PARTIAL` or `BLOCKED_BY_UPSTREAM` until upstream cache usage proves warmup.
- If future upstream calls return `cache_creation_input_tokens` or `cache_read_input_tokens` for a zero-output warmup path, mark `officialAnthropicParity=PASS` for that evidence window.

### 7. Strict Official Mode

Add a configuration concept, not necessarily enabled by default:

- `KIRO_GO_CLAUDE_PARITY_MODE=compat` default.
- `KIRO_GO_CLAUDE_PARITY_MODE=strict_official` optional.

In `compat` mode, Kiro-Go preserves Claude Code usability through emulation where safe.

In `strict_official` mode:

- estimated count tokens may return an explicit unsupported/partial diagnostic unless an exact backend is configured.
- assistant prefill emulation is disabled for models where native support is unavailable.
- `max_tokens=0` does not claim cache warmup without upstream cache proof.
- fine-grained tool streaming does not claim native partial JSON unless upstream partial deltas are observed.

Strict mode is for diagnostics and conformance testing. It should not be the default for `/www/sub2api/`.

## API Shape

Readiness response should remain backward compatible and add structured details:

```json
{
  "capabilities": {
    "countTokens": {
      "status": "PARTIAL",
      "detail": "Claude Code compatible estimated token counting; official exact count is not proven",
      "claudeCodeCompatibility": {
        "status": "PASS",
        "mode": "estimated",
        "proof": "count_tokens endpoint returned input_tokens"
      },
      "officialAnthropicParity": {
        "status": "PARTIAL",
        "mode": "estimated",
        "proof": "no upstream exact count_tokens evidence"
      }
    }
  }
}
```

Existing UI consumers that read only `capabilities.<name>.status` continue to work.

## Implementation Scope

In scope:

- Update `/admin/api/claude-code/readiness` capability payload.
- Update the admin UI readiness rendering to show layered status and evidence.
- Add request-log fields for capability modes where missing.
- Add unit tests for readiness status derivation.
- Add focused integration/UAT checks for direct Kiro-Go and sub2api.

Out of scope:

- Replacing the token estimator with a true Anthropic tokenizer.
- Claiming official prompt cache warmup without upstream cache usage evidence.
- Changing sub2api routing or storage.
- Reworking Kiro account routing and 429 behavior beyond current model-capacity handling.

## Verification

Required automated checks:

- Go unit tests for readiness JSON shape and backward-compatible `status`.
- Go unit tests for each mode derivation:
  - text assistant prefill emulation.
  - tool-use prefill unsupported.
  - count_tokens estimated.
  - local max_tokens=0 response.
  - fine-grained event format.
- Existing proxy tests remain passing.

Required live checks before marking UAT pass:

- Direct Kiro-Go non-stream request succeeds.
- Direct Kiro-Go stream request succeeds.
- Direct Kiro-Go `/v1/messages/count_tokens` succeeds.
- sub2api to Kiro-Go non-stream request succeeds.
- sub2api to Kiro-Go stream request succeeds.
- readiness endpoint shows layered statuses and evidence.
- browser screenshot confirms the UI displays compatibility and official-parity details without hiding the reason for `PARTIAL`.

## Success Criteria

- Claude Code operators can see which capabilities are usable today and which are only emulated.
- No `PARTIAL` capability is mislabeled as official `PASS` without proof.
- sub2api remains able to call Kiro-Go.
- Readiness output becomes more useful for debugging Claude Code retry, compaction, streaming, and cache-warmup issues.
- The system follows open-source gateway best practices: explicit capability maps, transparent fallback, request evidence, and configurable strictness.
