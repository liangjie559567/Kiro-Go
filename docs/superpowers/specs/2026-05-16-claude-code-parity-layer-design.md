# Claude Code Parity Layer Design

Date: 2026-05-16

## Goal

Make Kiro-Go behave like a high-fidelity Anthropic-compatible upstream for Claude Code, while still using Kiro accounts as the real upstream capacity. The target experience is:

- healthy downstream concurrency under Claude Code and sub2api workloads;
- low first-token latency when there is available Kiro capacity;
- Anthropic-compatible errors, headers, and streaming event order;
- complete Claude Code tool support, including MCP Tool Search flows that rely on `tool_reference`;
- production evidence that failures come from upstream capacity or payload limits, not gateway protocol gaps.

This design extends the existing gateway reliability work. It does not replace the current account pool, request logs, prompt filtering, web search support, or OpenAI/Responses routes.

## Source Requirements

Official behavior to match:

- Claude Code can use `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`, and `ANTHROPIC_SMALL_FAST_MODEL` for custom API endpoints.
- Claude Code disables MCP Tool Search for non-Anthropic `ANTHROPIC_BASE_URL` by default unless the user opts into `ENABLE_TOOL_SEARCH=true`; the proxy must support `tool_reference` for this to be useful.
- Anthropic Messages streaming uses ordered SSE events: `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, and `message_stop`, with optional `ping` and `error` events.
- Tool argument streaming uses `input_json_delta` chunks for tool input.
- Anthropic errors use structured error types such as `rate_limit_error`, `overloaded_error`, `authentication_error`, and `invalid_request_error`, and responses expose request IDs.
- Prompt caching, count tokens, images, tool results, and beta headers must be accepted without breaking Claude Code clients.

Useful implementation references:

- `Jwadow/kiro-gateway`: circuit breaker, sticky account, payload guards, truncation recovery, broad converter and streaming tests.
- `hj01857655/kiro-account-manager`: Responses session restore, `previous_response_id`, tool/tool_choice inheritance, one-click Claude Code configuration.
- `musistudio/claude-code-router`: SSE parser/serializer patterns, provider fallback, tool streaming compatibility workarounds.
- `claude-code-proxy` implementations: Claude/OpenAI conversion tests, streaming usage capture, adaptive retry and tool correspondence tests.

## Current Baseline

Kiro-Go already has:

- Anthropic `/v1/messages` and `/v1/messages/count_tokens`;
- OpenAI `/v1/chat/completions` and `/v1/responses`;
- account pool scheduling by health, round-robin, or least connections;
- atomic request reservation via `BeginNextForModelExcept`;
- token refresh, health checks, endpoint fallback, prompt filtering, and request logs;
- Claude image blocks, `tool_use`, `tool_result`, prompt cache approximation, and Kiro-backed native web search;
- stream emission for text, thinking, and tool-use blocks.

Known gaps:

- no `tool_reference` request model or Tool Search handling;
- `anthropic-beta` is allowed by CORS but not treated as a capability contract;
- stream tool input is emitted as one full JSON delta instead of a controlled fine-grained stream;
- no periodic `ping` heartbeat for long upstream silence;
- no per-model circuit breaker with half-open recovery and session stickiness;
- payload trimming exists but does not yet provide full serialized-size preflight, current-turn repair, and next-turn recovery notices;
- compatibility tests do not yet replay real Claude Code request shapes for MCP Tool Search, beta headers, and long tool-result histories.

## Scope

In scope:

- Claude Code compatibility layer for `/v1/messages`, `/v1/messages/count_tokens`, `/v1/models`, and relevant headers.
- Request parsing that accepts known new Anthropic fields and preserves unknown top-level fields for logging and future routing.
- `tool_reference` support sufficient for Claude Code MCP Tool Search compatibility.
- SSE conformance writer with heartbeats, deterministic event order, safe error events after stream start, and official request IDs.
- Account scheduler upgrades: per-model circuit breaker, sticky session routing, rate-limit-aware cooldowns, and half-open probes.
- Payload guard and truncation recovery before Kiro payload submission.
- A Claude Code compatibility harness with fake Kiro upstream and real Claude Code-shaped golden requests.

Out of scope for this phase:

- replacing Kiro upstream capacity with official Anthropic capacity;
- building a new desktop account manager;
- full parity for Anthropic features that Kiro cannot physically execute, unless they can be accepted and degraded without breaking clients;
- broad UI redesign beyond small config/log fields needed to validate the feature.

## Architecture

### 1. Anthropic Compatibility Ingress

Add a small request-envelope layer before `ClaudeRequest` conversion:

- capture headers: `anthropic-version`, `anthropic-beta`, `request-id`, `x-request-id`, Claude Code session headers, and user agent;
- decode known fields into `ClaudeRequest`;
- store unknown top-level request fields in `Extra map[string]json.RawMessage`;
- expose beta capability helpers, for example `HasBeta("fine-grained-tool-streaming")`;
- accept `tool_reference` without rejecting the request.

The converter should remain strict where Kiro needs structure, but the ingress should be tolerant like the official API. Unknown fields are logged and ignored unless a compatibility module claims them.

### 2. Tool Reference And MCP Tool Search

Add `ClaudeToolReference` support:

- request model accepts `tool_reference` blocks in the top-level request and in relevant content positions if Claude Code sends them that way;
- the compatibility layer expands references into concrete Kiro tools when enough metadata is present;
- if the reference is deferred or unresolved, the gateway should return a clear `invalid_request_error` only when execution would be impossible;
- names are sanitized for Kiro, but the outward Claude tool name and `tool_use.id` mapping remain stable.

Expected user setup:

- Claude Code points `ANTHROPIC_BASE_URL` to Kiro-Go;
- users who need MCP Tool Search set `ENABLE_TOOL_SEARCH=true`;
- Kiro-Go documents that Tool Search requires this parity layer.

The first implementation does not need to discover MCP servers itself. Claude Code remains the MCP host; Kiro-Go only needs to accept and preserve the request shape Claude Code sends.

### 3. SSE Conformance Writer

Replace ad hoc stream writes for Claude with a narrow `ClaudeSSEWriter` helper:

- starts the message exactly once;
- allocates monotonically increasing content block indexes;
- guarantees every started block is stopped;
- emits `ping` while Kiro upstream is silent for a configurable interval;
- emits `error` events only after the HTTP response has become a stream;
- records first-token latency on the first text, thinking, or tool event;
- optionally chunks tool input JSON into bounded `input_json_delta` fragments.

This isolates event-order correctness from Kiro event parsing and makes golden SSE tests straightforward.

### 4. Scheduler Circuit Breaker

Extend account runtime health into a per-account, per-model breaker:

- closed: account is healthy and schedulable;
- open: account is skipped until `retry_after` or calculated backoff expires;
- half-open: allow one low-risk probe; success closes, failure reopens with a longer delay.

Failure classification:

- 401/403/token errors: auth path, token refresh, then auth cooldown;
- 402/quota/billing: quota cooldown or account disabled depending on current policy;
- 429: honor upstream `Retry-After` when present;
- 5xx, HTTP/2 reset, timeout: exponential backoff with jitter;
- payload validation errors: do not penalize the account.

Session stickiness:

- derive a sticky key from Claude Code session ID, request ID chain, or stable conversation ID;
- prefer the previous healthy account for that key to improve cache locality and reduce cross-account Kiro conversation drift;
- break stickiness immediately when the account is open, over quota, missing the requested model, or overloaded.

### 5. Payload Guard And Truncation Recovery

Add a preflight guard before `CallKiroAPI`:

- serialize the final Kiro payload and compare against configurable soft/hard byte limits;
- trim history by semantic pairs: assistant tool_use plus matching user tool_result;
- never leave orphan tool results or orphan assistant tool uses;
- if current-turn tool results are too large, summarize or reject with an Anthropic-compatible `invalid_request_error` before touching account health;
- record a truncation marker in request metadata.

Recovery:

- on the next turn for the same conversation, inject a short system-side recovery note only when previous truncation could affect continuity;
- make the note deterministic and testable;
- log truncation decisions for diagnosis.

### 6. Headers, Models, And Errors

Headers:

- return both `request-id` and `x-request-id`;
- expose rate-limit headers when Kiro provides enough data;
- propagate or synthesize `Retry-After` on rate-limit and overload responses.

Models:

- add or harden `/v1/models` for Anthropic-compatible model discovery;
- include model aliases and thinking variants already supported by Kiro-Go;
- keep model mappings deterministic under tests.

Errors:

- centralize Anthropic error conversion for non-stream and pre-stream failures;
- return 429 `rate_limit_error`, 529 or 503 `overloaded_error`, 504 `timeout_error`, 401 `authentication_error`, 402 `billing_error`, 400 `invalid_request_error`;
- after stream start, emit SSE `error` and stop writing normal events.

### 7. Observability

Extend request logs and admin diagnostics with:

- `requestID` and `anthropicRequestID`;
- `claudeCodeSessionID` and optional agent ID;
- beta flags seen;
- tool count, tool-reference count, tool-use count;
- selected account, previous sticky account, breaker state, and breaker reason;
- queue wait, first-token latency, total latency, attempts, and retry-after;
- payload original bytes, final bytes, trimmed message count, and truncation recovery status.

This is required for production validation. The user should be able to answer why a Claude Code turn was slow without reading gateway logs.

## Data Flow

1. HTTP request enters `/v1/messages`.
2. Compatibility ingress captures headers, beta flags, unknown fields, request ID, and Claude Code metadata.
3. Shape validation runs with Claude Code-tolerant rules.
4. Payload guard computes request size and applies safe history trimming if needed.
5. Scheduler selects an account using model support, breaker state, active connections, latency, health, and sticky key.
6. Kiro payload is submitted.
7. `ClaudeSSEWriter` or non-stream response builder converts Kiro events to Anthropic format.
8. Success/failure updates breaker state, request logs, prompt cache profile, and account stats.

## Test Strategy

Unit tests:

- request envelope parses `anthropic-beta`, unknown fields, and `tool_reference`;
- tool reference conversion preserves outward names and IDs;
- SSE writer golden tests cover text, thinking, tool use, error-after-start, heartbeat, and empty final content;
- breaker state transitions cover closed, open, half-open, retry-after, auth, quota, 429, 5xx, and payload errors;
- payload guard trims pairwise and never emits orphan tool messages.

Integration tests with fake Kiro:

- Claude Code basic text turn, stream and non-stream;
- Claude Code MCP-heavy request with many tools;
- Tool Search opt-in request shape with `tool_reference`;
- large tool_result history that triggers trimming and next-turn recovery;
- upstream 429 with Retry-After;
- HTTP/2 reset before first token, retry succeeds;
- HTTP/2 reset after stream start, SSE error is emitted;
- concurrent 100x10 load with multiple accounts and synthetic latency.

Manual validation:

- run Claude Code with `ANTHROPIC_BASE_URL=http://localhost:8080`;
- run with and without `ENABLE_TOOL_SEARCH=true`;
- verify filesystem/shell/MCP tools still appear and execute in Claude Code;
- verify first-token latency, retries, and breaker state in admin logs;
- compare error shape against Anthropic SDK expectations.

## Rollout

Phase 1: Compatibility harness and envelope.

- Add golden request fixtures and fake Kiro stream fixtures first.
- Implement request IDs, beta capture, tolerant unknown-field parsing, `/v1/models`, and stronger error headers.

Phase 2: SSE writer and tool parity.

- Move Claude stream emission behind `ClaudeSSEWriter`.
- Add heartbeat and chunked `input_json_delta`.
- Add `tool_reference` acceptance and outward-name preservation.

Phase 3: Scheduler breaker.

- Add per-model breaker state and half-open probes.
- Add session stickiness with immediate escape on unhealthy accounts.
- Add breaker observability.

Phase 4: Payload guard and truncation recovery.

- Add serialized-size preflight.
- Add pairwise trimming and recovery notice.
- Add large tool-result tests.

Phase 5: Production UAT.

- Run fake-upstream tests, `go test ./...`, and real Claude Code smoke tests.
- Run 100x10 stream and non-stream UAT through sub2api.
- Capture admin evidence and latency distribution.

## Acceptance Criteria

- `go test ./...` passes.
- Golden SSE tests prove official event ordering for text, thinking, tool-use, heartbeat, and stream error cases.
- Claude Code can run against Kiro-Go with `ANTHROPIC_BASE_URL` for normal tasks.
- With `ENABLE_TOOL_SEARCH=true`, Kiro-Go accepts Tool Search request shapes that include `tool_reference`.
- Tool calls preserve Claude Code-visible names and IDs even when Kiro requires sanitized names.
- Concurrent load does not pile all new work onto one unhealthy or slow account.
- 429, overload, timeout, auth, quota, and invalid payload failures are distinguishable in both API responses and admin logs.
- Large histories are trimmed deterministically without orphan tool messages.

## Risks And Limits

- Kiro upstream may not expose all Anthropic beta behavior. The gateway can accept and degrade some fields, but it cannot make Kiro execute unsupported server-side features.
- Tool Search support depends on the exact request shape emitted by the installed Claude Code version. The compatibility harness must keep fixtures from real Claude Code sessions.
- Heartbeats reduce client-side silence but do not reduce upstream latency.
- Sticky accounts improve locality but can reduce balancing if not escaped aggressively on overload.
- Payload trimming can preserve protocol validity, but it can still lose conversational detail. Recovery notices make this visible rather than invisible.

## Implementation Defaults

- Heartbeat interval: 15 seconds for Claude streams.
- Tool JSON delta chunk size: 4 KiB to avoid huge SSE frames while keeping overhead low.
- Breaker backoff: 30 seconds for transient 5xx, `Retry-After` for 429, 10 minutes for auth failures after refresh fails, and 1 hour for quota/suspension.
- Stickiness TTL: 30 minutes idle or until account health changes.
- `/v1/models` exposes both Kiro-supported model IDs and configured aliases. The public response stays Anthropic-shaped and does not add non-Anthropic fields.
