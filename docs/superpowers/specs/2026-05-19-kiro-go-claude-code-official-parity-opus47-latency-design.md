# Kiro-Go Claude Code Official Parity and Opus 4.7 Latency Design

Date: 2026-05-19

## Goal

Make Kiro-Go behave as close as practical to the official Anthropic Claude Code API experience while preserving the current Kiro account pool architecture and improving downstream Opus 4.7 concurrency latency.

The implementation scope is Kiro-Go only. `/www/sub2api` must not be modified. It is a black-box downstream caller used for real integration verification after Kiro-Go changes are deployed.

## Non-Goals

- Do not modify sub2api source, database schema, deployment, or runtime configuration.
- Do not bypass Kiro upstream risk controls.
- Do not claim true official Anthropic behavior where Kiro upstream cannot support it. Unsupported features must be accepted, preserved for diagnostics, locally approximated, or explicitly reported as partial.
- Do not mark UAT PASS from unit tests alone. Browser, API, database, and screenshot evidence are required.

## Current Evidence

Recent real UAT showed:

- `claude-sonnet-4.5`, no sticky headers, 10 concurrency, 100 total requests: `100/100` success.
- `claude-opus-4.7`, no sticky headers, 10 concurrency, 100 total requests: `99/100` success and one `503 claude-opus-4.7 concurrency queue timeout`.
- Opus 4.7 container logs showed Kiro upstream `429 INSUFFICIENT_MODEL_CAPACITY`, not `suspicious temporary limit`.
- All 21 configured Kiro accounts share one `profileArn` risk group, so suspicious temporary limit must still cool the whole group.
- After the Opus 4.7 run, admin readiness still showed `21/21 schedulable` and `0 coolingDown`.

This means Opus 4.7 pressure is primarily model-capacity/admission pressure, not account health failure.

## Design Principles

- Kiro-Go is a gateway, not a gatekeeper. Unknown-but-plausible model names should be normalized or passed through to Kiro where safe.
- Claude Code fields and headers should be preserved or logged as structured metadata even when Kiro cannot use them directly.
- Account-level failures must stay separate from model/provider capacity pressure.
- Opus 4.7 latency should be reduced by adaptive admission and cache-aware behavior, not by blindly raising concurrency.
- UAT must prove the downstream chain still works through sub2api without changing sub2api.

## Module 1: Claude Code and Anthropic Protocol Parity

Kiro-Go should expand the Anthropic request envelope to recognize and preserve official Claude Code / Messages API fields:

- `container`
- `context_management`
- `mcp_servers`
- `metadata`
- `service_tier`
- `stop_sequences`
- `tool_choice`
- `tool_reference`
- `tools[*].cache_control`
- `system[*].cache_control`
- `anthropic-beta`
- Claude Code session headers:
  - `X-Claude-Code-Session-Id`
  - `X-Claude-Code-Agent-Id`
  - `X-Claude-Code-Parent-Agent-Id`
  - `X-Claude-Code-Project-Dir`
  - `X-Claude-Code-Version`

Behavior:

- Preserve accepted fields in the decoded envelope.
- Use fields that Kiro-Go already supports when constructing Kiro payloads.
- For fields Kiro upstream cannot consume, keep them out of unsafe Kiro payload paths but record safe presence/shape metadata in request logs.
- Continue to support `tools`, `tool_use`, `tool_result`, `tool_reference`, server-side web search, vision, streaming, and non-streaming responses.

Request logs must record booleans or counts such as:

- `hasContainer`
- `hasContextManagement`
- `mcpServerCount`
- `hasServiceTier`
- `toolChoiceMode`
- `anthropicBetaPresent`
- `claudeCodeSessionPresent`
- `claudeCodeAgentPresent`
- `claudeCodeParentAgentPresent`

Sensitive values must not be logged.

## Module 2: Model Name Resolution and Opus 4.7 Semantics

Kiro-Go should normalize official and Kiro-style model names:

- `claude-opus-4-7`
- `claude-opus-4.7`
- versioned/date suffixed variants when safe
- thinking suffix variants such as `claude-opus-4-7-thinking`

For routing, Kiro-Go should use the Kiro-compatible internal model ID. For response and logs, it should preserve both:

- `requestedModel`
- `mappedModel`

Opus 4.7 behavior:

- Detect Opus 4.7 before request translation.
- If the request uses `thinking.type=enabled` with `budget_tokens`, convert or reject according to an explicit compatibility mode.
- Default compatibility mode should be conservative:
  - prefer `thinking.type=adaptive` for Opus 4.7;
  - remove unsupported manual thinking budget before Kiro routing when the request came from Claude Code compatibility paths;
  - log the normalization as `opus47ThinkingNormalized`.
- Remove or ignore non-default `temperature`, `top_p`, and `top_k` for Opus 4.7 if they would produce an upstream 400.
- If a user explicitly wants strict official validation, the design should allow a future mode that returns a local 400 instead of silently normalizing.

Admin readiness and `/v1/models` should expose both official and Kiro-style names where useful:

- `claude-opus-4-7`
- `claude-opus-4.7`

The list must avoid duplicate logical entries when clients consume it.

## Module 3: Adaptive Opus 4.7 Admission and Latency Control

The current fixed Opus 4.7 admission settings can allow too many requests into a constrained upstream, producing long queue waits and 90s local timeouts. Replace fixed pressure behavior with an adaptive model admission controller.

Signals:

- Kiro upstream `429 INSUFFICIENT_MODEL_CAPACITY`
- upstream 5xx
- local queue timeout
- p90/p95 request latency above threshold
- first-token latency above threshold for streaming
- repeated retry budget exhaustion

State per model:

- `pressureScore`
- `effectiveMaxConcurrent`
- `queueDepth`
- `activeRequests`
- `recentCapacityErrors`
- `recentQueueTimeouts`
- `p50LatencyMs`
- `p95LatencyMs`
- `lastPressureAt`
- `cooldownUntil`

Control loop:

- Start Opus 4.7 from a safe configured baseline.
- On capacity pressure, multiplicatively reduce effective concurrency, down to a floor of 1 or 2.
- During stable windows, increase concurrency slowly by 1 step.
- Do not count `model_capacity` against account health.
- Do not trigger risk-group cooldown for capacity pressure.
- Continue to trigger whole-risk-group cooldown for suspicious temporary limit.

Request logs must split latency:

- `admissionWaitMs`
- `queueWaitMs`
- `upstreamFirstByteMs` or `firstTokenMs` where available
- `upstreamTotalMs`
- `retryCount`
- `capacityRetryCount`
- `effectiveConcurrentLimit`
- `pressureScore`

Admin UI/readiness must show model pressure for Opus 4.7 and any other configured model admission rule.

## Module 4: Prompt Cache and Prewarm Compatibility

Kiro-Go should improve Claude Code long-context behavior by making cache usage visible and by supporting safe prewarm flows.

Behavior:

- Preserve `cache_control` metadata from `system`, `messages`, and `tools` in the local Claude request envelope.
- Compute a stable cache fingerprint from Claude Code system/tool prefixes and major context blocks.
- Support `max_tokens=0` as a local prewarm path that does not perform a normal text generation.
- Record whether a request is:
  - `normal_generation`
  - `local_zero_output`
  - `cache_prewarm`
- Track estimated cache read/write usage in request logs.

Kiro upstream may not support official Anthropic prompt caching semantics directly. If exact upstream cache behavior cannot be proven, readiness must say `PARTIAL`, not `PASS`.

UAT must compare at least:

- normal Opus 4.7 request latency without prewarm;
- prewarm request result;
- repeated request latency after prewarm.

The result can only pass if evidence shows either a measurable improvement or a truthful `PARTIAL` with no protocol breakage.

## sub2api Black-Box Verification Contract

Kiro-Go changes must be verified through the live sub2api service without modifying sub2api.

Required checks:

- sub2api container remains healthy.
- sub2api database has expected base objects such as users, groups, accounts, and API keys.
- A real downstream `/v1/messages` call through sub2api reaches Kiro-Go and returns a valid Claude-compatible response.
- Opus 4.7 downstream calls do not get mislabeled as account failures when Kiro-Go reports capacity pressure.
- Claude Code headers, if sent through sub2api, are visible in Kiro-Go request logs as present metadata.

If sub2api strips a header or rewrites a field, Kiro-Go must report the observed state truthfully in UAT. Kiro-Go should not depend on changing sub2api to pass.

## Browser and UAT Requirements

After implementation, the deploy-and-verify flow must:

1. Run `go test ./... -count=1`.
2. Rebuild and start Kiro-Go in Docker.
3. Confirm Kiro-Go health endpoint.
4. Confirm sub2api health endpoint.
5. Use real browser Playwright verification against Kiro-Go admin UI.
6. Capture screenshots for:
   - accounts/risk groups;
   - Claude Code readiness;
   - model readiness for Opus 4.7;
   - admission pressure;
   - recent request logs.
7. Analyze screenshots and API JSON. A screenshot file existing is not enough; visible content must match expected state.
8. Capture database/API evidence for sub2api black-box integration.
9. Write a UAT summary under `docs/superpowers/uat/<run-id>/UAT-SUMMARY.md`.

UAT can only mark PASS when:

- browser screenshots render correctly;
- request logs show expected fields;
- Kiro-Go and sub2api health checks pass;
- real downstream generation through sub2api succeeds;
- account health does not show false temporary limits;
- Opus 4.7 pressure is either healthy or truthfully reported as capacity pressure with adaptive behavior.

## Testing Strategy

Unit tests:

- model normalization for `claude-opus-4-7` and `claude-opus-4.7`;
- Opus 4.7 thinking normalization;
- sampling parameter stripping or validation;
- request envelope preservation for official fields;
- request log metadata redaction;
- adaptive admission pressure transitions;
- `model_capacity` does not mark account unhealthy;
- temporary suspicious limit still cools the risk group.

Integration tests:

- `/v1/models` returns compatible model entries.
- `/v1/messages/count_tokens` still works.
- `max_tokens=0` path returns valid Claude-compatible zero-output response.
- non-streaming and streaming Claude requests still work.
- admin readiness reflects admission pressure and cache state.

Real UAT:

- direct Kiro-Go Sonnet smoke.
- direct Kiro-Go Opus 4.7 bounded load.
- sub2api to Kiro-Go real generation.
- Playwright browser screenshots and DOM checks.

## Acceptance Criteria

- Kiro-Go exposes official and Kiro-style Opus 4.7 model names safely.
- Claude Code request metadata is accepted and observable without leaking sensitive values.
- Opus 4.7 incompatible parameters are normalized or rejected consistently.
- Opus 4.7 capacity pressure reduces effective concurrency instead of causing repeated 90s queue timeouts.
- Account health remains clean for model capacity errors.
- Suspicious temporary limits still trigger risk-group cooldown.
- sub2api continues to call Kiro-Go successfully as a downstream black box.
- UAT evidence includes screenshots, API JSON, database checks, request logs, and explicit screenshot analysis.

