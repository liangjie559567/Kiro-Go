# Claude Code Official Experience And Reliability Design

Date: 2026-05-16

## Goal

Make Kiro-Go feel like the official Anthropic Claude API when Claude Code is the downstream client, while still routing requests to Kiro accounts. Success is not just "requests work"; the target is:

- Claude Code tools work completely, including filesystem/shell/MCP-heavy sessions and Tool Search request shapes.
- High-concurrency Claude Code calls avoid unhealthy accounts, avoid queue collapse, and fail with actionable Anthropic-shaped errors when upstream capacity is truly unavailable.
- First-token and total latency stay low when healthy Kiro capacity exists.
- Streaming, usage, request IDs, retry headers, model discovery, count tokens, prompt caching, and beta headers are close enough that Claude Code and Anthropic SDK clients do not need special workarounds.
- The local `/www/sub2api` deployment remains a real, working downstream of Kiro-Go for both streaming and non-streaming calls.

This design builds on the existing parity and reliability work already present in Kiro-Go. Current code already includes `tool_reference`, `ClaudeSSEWriter`, request IDs, per-model breaker/stickiness, Retry-After propagation, request logs, prompt cache tracking, count_tokens, web_search via Kiro MCP, and model admission gates. The next phase should harden and verify those capabilities against official behavior and production load.

## Official Behavior To Match

Claude Code custom provider behavior:

- Claude Code can be pointed at a gateway with `ANTHROPIC_BASE_URL` and auth/model environment variables.
- Claude Code disables MCP Tool Search for a non-Anthropic base URL unless `ENABLE_TOOL_SEARCH=true`.
- If users enable Tool Search, the gateway must accept `tool_reference` request shapes and preserve outward tool names and IDs.

Anthropic Messages behavior:

- `/v1/messages` supports streaming and non-streaming responses with ordered SSE events.
- Streaming events include `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`, and optional `ping` or stream `error`.
- Tool input streaming is represented with `input_json_delta` chunks.
- Extended thinking streams as thinking content and may include signature-related fields in official responses.
- API responses expose request IDs.

Anthropic tool and beta behavior:

- User tools, `tool_choice`, tool-use/tool-result loops, images, server tools, prompt caching fields, and count-token calls must be accepted without breaking the request parser.
- Official prompt caching applies across tools, system, and messages. Tool order and tool definition stability matter because upstream cache invalidation is hierarchical.
- Count-token results should include meaningful input token estimates for tools, system, messages, images, documents, and thinking configuration.

Anthropic error and rate-limit behavior:

- Errors are structured by type, for example `invalid_request_error`, `authentication_error`, `permission_error`, `rate_limit_error`, `overloaded_error`, and `api_error`.
- Rate limits and overloads should preserve or synthesize `Retry-After` when the upstream provides timing.
- Clients should see distinguishable auth, quota, payload, timeout, rate-limit, and overload failures.

Kiro behavior to account for:

- Kiro official network guidance includes current `runtime.<region>.kiro.dev` and `management.<region>.kiro.dev` domains, while `q.<region>.amazonaws.com` is legacy.
- Some Anthropic features can only be emulated or accepted-and-degraded because Kiro upstream does not expose official Anthropic capacity directly.

## Current Baseline In Kiro-Go

Implemented strengths:

- Anthropic `/v1/messages`, `/v1/messages/count_tokens`, OpenAI `/v1/chat/completions`, OpenAI `/v1/responses`, `/v1/models`, `/v1/stats`, admin APIs, and request logs.
- Multi-account routing by health, round-robin, or least connections.
- Per-account weights, overage policy, token refresh, account health checks, model list caching, per-account outbound proxy, and model mappings.
- Per-model breaker/stickiness with `BeginNextForModelSessionExcept`.
- Claude Code request envelope fields, `tool_reference`, beta capture, request IDs, CORS headers, and request-log metadata.
- Claude SSE writer for event ordering, tool input chunking, ping, and stream errors.
- Kiro-backed native web_search emulation through MCP.
- Payload guard and orphan tool-message trimming.
- Request logs with account, model, endpoint, attempts, first-token latency, tool counts, cache tokens, payload bytes, and outcomes.

Local full-stack baseline:

- `/www/sub2api/deploy/docker-compose.current.yml` attaches sub2api to the external Docker network `kiro-go_default`.
- Existing production UAT evidence uses `http://127.0.0.1:18080` for sub2api and `http://127.0.0.1:8080` for Kiro-Go.
- The verified route is `sub2api /v1/messages -> Kiro-Go /v1/messages -> Kiro upstream`.
- Existing 100x10 UAT artifacts show successful real calls through this chain for `claude-opus-4-7`, including both streaming and non-streaming modes.

Remaining gaps:

- No single official-behavior validation suite proves that current implementation stays compatible across Claude Code versions.
- `/v1/messages/count_tokens` is still an estimator; it is not yet calibrated against real post-call usage and can undercount tool-heavy or image-heavy Claude Code turns.
- Streaming conformance needs golden tests for complete Claude Code flows, not only unit-level writer behavior.
- Scheduler policies exist, but admission limits, half-open probes, and latency scoring are not yet consistently driven by per-model/per-account measured first-token latency.
- Kiro endpoint selection needs a runtime-first strategy with safe fallback to legacy endpoints where needed.
- Prompt caching is tracked, but cache-control insertion/stability is not yet treated as a first-class performance contract for Claude Code.
- Error mapping should be audited end-to-end so upstream 429/5xx/auth/quota/payload errors produce official-compatible client behavior.

## Design Approach

Use a three-layer reliability model.

1. Protocol parity layer:
   Validate and normalize Claude Code and Anthropic request/response shapes before account selection. This layer owns headers, request IDs, beta flags, `tool_reference`, model naming, count_tokens, streaming event shape, and official error serialization.

2. Capacity and routing layer:
   Select accounts using measured health, active connections, per-model breaker state, session stickiness, model support, token expiry, quota state, and retry-after cooldowns. This layer must decide quickly whether to route, queue briefly, probe, retry on another account, or fail with a structured overload/rate-limit response.

3. Performance and observability layer:
   Keep prompt-cache-affecting data stable, estimate and log token/caching behavior, record first-token latency, record retries and queue wait, and expose enough admin evidence to diagnose slow Claude Code turns.

This is preferable to adding isolated fixes because Claude Code failures often cross boundaries: an oversized tool result can look like an account failure; a stream reset before first token is retryable but a reset after tool_use is not; unstable tool ordering can inflate latency through prompt-cache misses.

## Feature Design

### 1. Official Compatibility Harness

Create a dedicated test harness under existing Go tests and `proxy/testdata`.

Fixtures:

- Real Claude Code wire request for plain chat.
- Real Claude Code wire request with many MCP tools.
- Real Claude Code wire request with `tool_reference`.
- Tool-use/tool-result continuation turn.
- Thinking stream request.
- Native web_search request.
- Large history with oversized tool results.
- Upstream 429 with `Retry-After`.
- Upstream 5xx/HTTP2 reset before first emitted event.
- Upstream reset after stream start.
- Real sub2api downstream request shape for `/v1/messages`, stream and non-stream.

Assertions:

- Request envelope accepts known and unknown Anthropic fields without data loss in logs.
- Public tool names and `tool_use.id` values remain stable even when Kiro requires sanitized names.
- SSE events are valid JSON and ordered like Anthropic.
- Pre-stream retry never emits duplicate partial messages.
- Post-stream failure emits SSE `error` and stops normal event emission.
- Error JSON and headers match the intended Anthropic-compatible shape.
- sub2api receives a response shape it can parse, bill, log, and classify without gateway-specific regressions.

### 1A. sub2api Downstream Compatibility Gate

Treat `/www/sub2api` as a first-class downstream, not just an incidental client.

Hard compatibility requirements:

- Kiro-Go must keep `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, `/health`, and CORS/header behavior compatible with sub2api.
- Kiro-Go must not remove or rename response fields that sub2api uses for usage, model, stream, error, or request logging.
- Kiro-Go must preserve stream event semantics that sub2api's Anthropic gateway path expects.
- Gateway-side 429/529/5xx mapping must remain intelligible to sub2api's error classification and temp-unschedulable logic.
- Request IDs should be propagated so sub2api access logs, usage logs, and Kiro-Go request logs can be correlated more easily.
- Admission changes in Kiro-Go must not conflict with sub2api's own user/account concurrency slots. Kiro-Go should fail fast with retryable upstream-compatible errors when its own capacity is full rather than holding sub2api slots until the 30s user-slot timeout.

Regression checks:

- Before and after implementation, run a smoke request through sub2api for non-stream and stream.
- For high-risk scheduler/error changes, run the existing 100x10 content-latency script against sub2api.
- Confirm sub2api `usage_logs` records rows for both stream and non-stream calls with expected model, duration, input tokens, and output tokens.
- Confirm failures, if any, can be attributed to sub2api admission, Kiro-Go admission, or upstream Kiro capacity rather than ambiguous protocol errors.

### 2. Count Tokens Calibration

Keep local count_tokens fast, but make it less naive.

Estimator inputs:

- system blocks, message text, images, tool_use, tool_result, and tool_reference;
- tool schemas and descriptions;
- thinking budget and thinking mode prompt;
- cache_control blocks;
- request metadata that Claude Code commonly sends.

Calibration loop:

- After each real Kiro call, compare estimated input tokens with upstream usage or context-usage-derived estimate.
- Maintain per-model correction ratios in memory and expose them in admin diagnostics.
- Do not make count_tokens call upstream by default; that would add latency and consume capacity. Add an optional "strict count" mode only for diagnostics if a reliable upstream endpoint becomes available.

Acceptance:

- Tool-heavy count_tokens undercount should be reduced enough that Claude Code does not overrun Kiro payload limits unexpectedly.
- The estimator must never reject a request solely because the estimate is high; payload byte guard remains the hard safety gate.

### 3. Scheduler And Admission Upgrade

Extend current per-model breaker behavior into an explicit admission policy.

Per account, per model runtime state:

- active requests;
- queued requests;
- recent successes/failures;
- EWMA first-token latency;
- EWMA total latency;
- last failure reason;
- retry-after/cooldown deadline;
- half-open probe state.

Selection order:

1. Filter by enabled account, token freshness, quota/overage policy, model support, and cooldown.
2. Prefer sticky session account when healthy and not overloaded.
3. Prefer lower active connections.
4. Prefer better breaker score and lower first-token EWMA.
5. Respect account/model admission limits.
6. If all accounts are open but eligible for probe, allow one half-open probe.

Retry policy:

- Retry on another account only before stream output starts.
- Honor upstream `Retry-After` for 429 and capacity errors.
- Use jittered backoff for transient network/5xx failures.
- Do not penalize account health for invalid request, payload too large, malformed tool schema, or downstream disconnect.

Expected result:

- Under 100x10 load, no single slow account should continue receiving most new work.
- When all capacity is saturated, clients receive fast, structured overload/rate-limit responses instead of long ambiguous hangs.

### 4. Streaming Conformance And Heartbeats

Harden the existing `ClaudeSSEWriter` contract.

Requirements:

- `message_start` is always first.
- Every content block start has exactly one stop.
- Tool input JSON is chunked into bounded `input_json_delta` events.
- Thinking and text blocks do not interleave in a way Claude Code cannot parse.
- A `ping` heartbeat is emitted during long upstream silence.
- First-token latency is recorded on first text, thinking, or tool event.
- If upstream fails after stream start, emit an SSE `error` with a compatible type and stop.

Testing:

- Golden event snapshots for normal text, tool-only, thinking-only, mixed thinking/text/tool, web_search, and post-start error.
- Parse every SSE `data:` payload as JSON except `[DONE]`-style OpenAI responses.

### 5. Prompt Caching As A Latency Feature

Treat prompt caching as an explicit Claude Code performance layer.

Rules:

- Keep tool order deterministic across requests.
- Avoid unnecessary mutation of tool schemas after cache_control points.
- Preserve client cache_control fields where Kiro can use an equivalent cache point.
- If auto cache insertion is enabled, insert at stable boundaries and document the policy.
- Log cache_creation and cache_read token estimates separately.

Admin diagnostics:

- cache read tokens;
- cache creation tokens;
- requests with cache;
- cache hit rate by model and by client session;
- reason when a request cannot use cache effectively, such as tool-order change or trimmed history.

### 6. Kiro Endpoint Strategy

Add an endpoint strategy that prefers current Kiro runtime domains while retaining legacy fallback.

Policy:

- Streaming generation should try configured preferred endpoint first.
- Runtime/management endpoints should be first-class options where supported by current Kiro auth and request shape.
- Legacy `q.<region>.amazonaws.com` and `codewhisperer.<region>.amazonaws.com` remain fallback endpoints for APIs not yet available on runtime domains, especially model/profile/MCP compatibility.
- Endpoint health should be tracked independently from account health so one failing endpoint does not falsely mark an account unhealthy.

Acceptance:

- Endpoint failures are visible in logs as endpoint failures, not generic account failures.
- Fallback attempts are bounded and logged with duration.

### 7. Error And Header Contract

Centralize Anthropic-compatible error mapping.

Mapping:

- malformed JSON, invalid tool schema, oversized current tool result: `400 invalid_request_error`;
- missing/invalid client API key: `401 authentication_error`;
- upstream auth/token failure after refresh fails: `401 authentication_error` or `403 permission_error` depending on upstream;
- quota/billing: `402 billing_error` when clear, otherwise `403 permission_error`;
- upstream 429: `429 rate_limit_error` with `Retry-After`;
- Kiro capacity exhaustion or all accounts saturated: `529 overloaded_error` when used for Anthropic routes, or `503 api_error` only when 529 is unsuitable for client compatibility;
- timeout: `504 timeout_error`;
- unexpected upstream 5xx: `500 api_error` or `529 overloaded_error` when capacity-related.

Headers:

- always return `request-id` and `x-request-id`;
- preserve upstream `Retry-After` when present;
- synthesize retry timing only when the gateway knows its own cooldown;
- expose rate-limit headers only when the values are meaningful.

### 8. Production Observability

Request log fields should be enough to answer why Claude Code was slow or failed.

Required fields:

- request ID, client request ID, Claude Code session ID, user agent;
- endpoint, model, mapped model, stream flag;
- beta flags, tool count, tool_reference count, tool_use count;
- selected account, region, endpoint, sticky key status;
- admission queue wait, attempts, first-token latency, total latency;
- breaker state before and after request;
- failure reason, retry-after, cooldown until;
- payload original/final bytes, trim count, recovery note status;
- cache read/create tokens and estimated hit rate.

Admin page changes can be small and focused: add columns or expandable detail fields rather than redesigning the UI.

## Rollout Plan

Phase 1: Verification first.

- Add official-behavior fixtures and golden SSE tests.
- Add fake Kiro upstream scenarios for 429, 5xx, early reset, late reset, web_search, and tool-heavy turns.
- Make current behavior measurable before changing routing logic.
- Add sub2api downstream smoke verification to the regression checklist.

Phase 2: Count tokens and payload safety.

- Broaden estimator coverage.
- Add calibration against real usage.
- Add tests for tools, images, thinking, cache_control, tool_reference, and oversized tool results.

Phase 3: Scheduler/admission.

- Add per-account/per-model EWMA first-token metrics.
- Wire admission selection to measured latency and breaker state.
- Add half-open probes and endpoint-health separation.
- Run synthetic 100x10 load against fake upstream.
- Verify Kiro-Go admission does not cause sub2api's user/account concurrency slots to time out under normal 10-concurrent load.

Phase 4: Streaming and errors.

- Complete golden tests for official event sequences.
- Centralize error mapping and header propagation.
- Verify post-stream-start error behavior.

Phase 5: Kiro runtime endpoints and prompt-cache diagnostics.

- Add runtime-first endpoint policy behind config.
- Preserve legacy fallbacks.
- Add cache stability diagnostics and request-log evidence.

Phase 6: Real Claude Code UAT.

- Run Claude Code with `ANTHROPIC_BASE_URL=http://localhost:8080`.
- Run with and without `ENABLE_TOOL_SEARCH=true`.
- Execute filesystem/shell/MCP tasks, tool loops, web_search, thinking, and long-context sessions.
- Capture admin logs, latency distribution, and failure classification.
- Run 100x10 streaming and non-streaming production-style tests after fake-upstream tests pass.
- Re-run the sub2api full-stack route after Claude Code UAT: `sub2api -> Kiro-Go -> Kiro`, stream and non-stream.

## Acceptance Criteria

- `go test ./...` passes.
- Golden SSE tests cover text, thinking, tool-only, mixed tool/text, web_search, heartbeat, pre-stream retry, and post-stream error.
- Claude Code normal tasks run through Kiro-Go without protocol errors.
- Claude Code with `ENABLE_TOOL_SEARCH=true` sends Tool Search request shapes that Kiro-Go accepts and logs.
- Tool names and IDs visible to Claude Code remain stable across Kiro sanitization.
- Count_tokens estimates include tools, images, tool_reference, thinking, and cache_control.
- 100x10 fake-upstream load shows no repeated routing to accounts with open breakers or high first-token latency.
- 429, overload, timeout, auth, quota, payload, and downstream disconnect are distinguishable in API responses and request logs.
- Runtime endpoint failures are logged separately from account failures.
- Prompt-cache diagnostics show cache read/create tokens and cache hit indicators for Claude Code sessions.
- Local `/www/sub2api` remains able to make real stream and non-stream calls through Kiro-Go.
- sub2api 100x10 regression is at least as good as the existing baseline: no protocol errors, content correctness preserved, and any 429/529/concurrency failures attributable to explicit admission limits rather than malformed Kiro-Go responses.

## Risks And Limits

- Kiro upstream may not support every official Anthropic feature. The gateway can accept and degrade fields, but it cannot provide official Anthropic-only behavior that Kiro cannot execute.
- Claude Code request shapes can change. The fixture suite must be updated with real wire requests from new Claude Code versions.
- Aggressive retry improves reliability before stream start but can duplicate upstream work if the gateway misdetects whether output started. This boundary must be tested.
- Strict count_tokens parity is impossible without an official upstream tokenizer. Calibration reduces risk but does not make estimates exact.
- Prompt-cache insertion can improve latency but can also cause cache churn if boundaries are unstable. It should be observable and configurable.

## References

- Claude Code environment variables and Tool Search behavior: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`, `ENABLE_TOOL_SEARCH`.
- Anthropic Messages API and streaming event model.
- Anthropic prompt caching documentation for tools, system, messages, images, documents, and cache invalidation order.
- Anthropic count_tokens API behavior.
- Anthropic error and rate-limit response conventions.
- Kiro official firewall/network domains for runtime, management, and legacy endpoints.
- Open-source references reviewed: `hj01857655/kiro-account-manager`, `Jwadow/kiro-gateway`, `dext7r/KiroGate`, Claude-compatible proxy/router projects, and transparent prompt-cache projects.
