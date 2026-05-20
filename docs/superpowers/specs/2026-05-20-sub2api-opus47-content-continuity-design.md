# sub2api Opus 4.7 Content Continuity Design

Date: 2026-05-20

## Goal

Kiro-Go must keep the sub2api-facing Opus 4.7 generation path usable under upstream pressure without pretending that an empty response is a valid model answer.

The current StableDownstream contract prevents downstream HTTP `429`, `502`, and `503` leaks, but live monitoring showed a remaining failure mode: when Opus 4.7 admission is blocked or retry attempts are exhausted, Kiro-Go can return HTTP `200` with syntactically valid but empty Claude/OpenAI content. That protects sub2api transport health, but Claude Code still receives an unusable turn.

This design separates transport continuity from content correctness and makes both observable.

## Non-Goals

- Do not modify sub2api source code.
- Do not fake model answers when Kiro upstream did not produce content.
- Do not remove StableDownstream status-code protection.
- Do not increase aggressive health probes during Opus 4.7 pressure.
- Do not treat admin or maintenance endpoints as part of the stable downstream generation contract.

## Definitions

- Transport success: Kiro-Go returns a syntactically valid downstream response, preferably HTTP `200`, without gateway-level `429`, `502`, or `503`.
- Content success: the response includes real upstream model output, such as non-empty assistant text, thinking, or valid tool calls.
- Stable fallback: a controlled downstream response emitted after Kiro-Go cannot obtain real upstream content within its bounded policy.
- Pressure window: the period where Opus 4.7 admission reports degraded, half-open, or blocked state, or when accounts are cooling down due to rate limits, temporary limits, capacity errors, or upstream 5xx.

## Observed Failure

Live Docker monitoring on 2026-05-20 showed:

- Kiro-Go container healthy and sub2api healthy.
- Current Kiro-Go request logs returned only HTTP `200`.
- Opus 4.7 readiness degraded into blocked state with `safeConcurrency=0`.
- Kiro upstream returned repeated `INSUFFICIENT_MODEL_CAPACITY` and account temporary-limit `429`.
- Kiro-Go emitted StableDownstream fallbacks:
  - `stableFallbackReason=admission_pressure`
  - `stableFallbackReason=attempt_budget_exhausted`
- Corresponding sub2api usage rows had `output_tokens=0`.

The root issue is not downstream status leakage. The root issue is that transport success is being counted as request success even when content success is false.

## External Research Summary

### jwadow/kiro-gateway

Useful patterns:

- Multi-account failover with sticky successful account.
- Circuit breaker style failure counters.
- Exponential backoff for unhealthy accounts.
- Probabilistic retry for accounts that are cooling down.
- Lazy account initialization and state persistence.
- First-token retry handling before a stream has produced client-visible output.
- Truncation recovery notices when upstream behavior damages content.

Pattern not copied directly:

- Kiro-Go must classify Opus 4.7 `429` and `503` more precisely than a generic fatal or retryable bucket. Account temporary limits, model capacity, request validation failures, and token failures need different routing behavior.

### zeoak9297/KiroSwitchManager

The public repository does not include source code, so only product behavior is usable:

- Automatic account switching.
- One account to one machine identity binding.
- Token refresh and account status visibility.
- Model locking.
- Stream heartbeat/keepalive.
- Dynamic model list behavior.

Kiro-Go should borrow the operational principle, not implementation details: keep account identity stable, rotate away from unhealthy accounts, and avoid unnecessary probes that can worsen upstream limits.

### Gateway Reliability Practices

AWS, Envoy, and SRE-style gateway guidance supports these rules:

- Use bounded retry budgets instead of unbounded retries.
- Use exponential backoff with jitter.
- Apply circuit breakers and outlier detection per upstream target.
- Shed or queue load deliberately under pressure.
- Avoid retry storms.
- Do not classify a protocol-level successful response as business success when the response has no useful payload.

## Proposed Architecture

### 1. Two-Layer Success Semantics

Request logs and admin APIs must expose both:

- `transportSuccess`: downstream protocol completed successfully.
- `contentSuccess`: real upstream model content was produced.

Existing `outcome=success` should remain compatible for HTTP status behavior, but Opus 4.7 generation diagnostics must stop treating StableDownstream empty responses as healthy model completions.

Add request log fields:

- `contentSuccess bool`
- `contentFailureReason string`
- `upstreamContentTokens int`
- `stableFallbackFinal bool`
- `queuedForCapacity bool`
- `capacityQueueWaitMs int64`

Rules:

- Real text/tool/thinking output sets `contentSuccess=true`.
- Stable fallback with empty content sets `transportSuccess=true`, `contentSuccess=false`.
- Fallback reason remains visible through `stableFallbackReason`.
- Admin readiness must count recent `contentSuccess=false` events as pressure, even if HTTP status was `200`.

### 2. Opus 4.7 Capacity Queue

Before returning StableDownstream fallback for `admission_pressure`, Kiro-Go should queue sub2api-compatible Opus 4.7 generation requests for a bounded wait.

Queue policy:

- Queue is per normalized model.
- Queue admission is allowed only for generation requests.
- Queue drains according to live `safeConcurrency`.
- When `safeConcurrency=0`, requests wait until either capacity recovers or their continuity deadline expires.
- Queue wait uses jittered wakeups and broadcasts from pressure recovery.
- The queue must not run health probes; it only waits for real request capacity.

Suggested initial defaults:

- `contentContinuity.enabled=true`
- `contentContinuity.models=["claude-opus-4.7"]`
- `contentContinuity.maxQueueWaitSeconds=120`
- `contentContinuity.maxQueueDepth=300`
- `contentContinuity.minContentTokens=1`
- `contentContinuity.streamHeartbeatSeconds=10`

The queue is not a guarantee of upstream availability. It is a backpressure layer that prevents immediate empty `200` responses during short capacity dips.

### 3. StableDownstream Fallback Demotion

StableDownstream remains responsible for suppressing downstream `429`, `502`, and `503`, but its empty fallback must be demoted from success to transport-only completion.

Behavior:

- If real upstream content is available, return it normally.
- If no real content is available before continuity deadline:
  - HTTP status remains stable for sub2api compatibility.
  - Response stays syntactically valid.
  - `contentSuccess=false` is logged.
  - `stableFallbackFinal=true` is logged.
  - Admin readiness reports the model as blocked or degraded.

For Claude Code, empty content is still a degraded outcome. The main improvement is that Kiro-Go will wait longer and route better before falling back, and observability will no longer hide the failure.

### 4. Account Routing Improvements

Borrow the useful routing ideas from kiro-gateway and KiroSwitchManager:

- Prefer recently successful accounts for the same model and session.
- Break sticky binding immediately when an account returns temporary-limit, rate-limit, or model-capacity pressure.
- Keep account cooldowns reason-specific:
  - temporary-limit: longer cooldown with jitter
  - model capacity: short model-level pressure, not a hard account ban
  - token/auth failure: refresh or disable depending on refresh result
  - request validation failure: no account penalty
- Allow probabilistic half-open probes only when pressure window expires.
- Persist enough account pressure state to survive container restart without immediately hammering the same accounts.

### 5. Streaming Correctness

For stream requests:

- Before first downstream SSE event, Kiro-Go may transparently retry another account.
- After first content/tool/thinking event, Kiro-Go must not switch upstream.
- During capacity queue wait, emit no Claude SSE event unless a heartbeat-compatible event is proven safe for Claude Code.
- If a heartbeat is used, it must not create assistant content or close the message early.
- If the continuity deadline expires before content starts, return a valid terminal SSE fallback and log `contentSuccess=false`.

### 6. sub2api Cooperation Without Source Changes

Kiro-Go should expose enough readiness metadata for sub2api or operators to throttle externally:

- `GET /admin/api/fleet/readiness?model=claude-opus-4-7`
  - `safeConcurrency`
  - `contentSuccessRate`
  - `recentStableFallbacks`
  - `recentEmptyCompletions`
  - `recommendedQueueWaitSeconds`
  - `retryAfterSeconds`

Because this project will not edit sub2api source, any direct sub2api integration must be operational:

- lower sub2api account concurrency for the Kiro-Go upstream account when Kiro readiness is blocked
- prefer Kiro-Go queueing over sub2api retry storms
- use UAT to verify sub2api sees content, not just HTTP `200`

## Data Flow

1. sub2api sends Claude/OpenAI compatible generation request to Kiro-Go.
2. Kiro-Go normalizes model and request payload.
3. Kiro-Go detects sub2api-compatible StableDownstream request.
4. Admission checks current Opus 4.7 pressure.
5. If safe concurrency is available, request proceeds to account routing.
6. If safe concurrency is unavailable, request enters capacity queue.
7. Queue releases request when capacity recovers or continuity deadline expires.
8. Account router selects a healthy account and performs pre-first-token retry across accounts.
9. If upstream returns content, Kiro-Go streams or returns real response and logs `contentSuccess=true`.
10. If all bounded paths fail, Kiro-Go emits StableDownstream fallback and logs `contentSuccess=false`.

## Error Handling

Fatal request errors:

- malformed request
- unsupported payload that Kiro-Go cannot normalize
- client authentication failure

These should not enter continuity queue.

Recoverable upstream errors:

- `INSUFFICIENT_MODEL_CAPACITY`
- account temporary-limit `429`
- account rate limit
- upstream 5xx
- transient network errors

These may use account failover, cooldown, backoff, and capacity queue.

Token/auth errors:

- Attempt token refresh once through the existing refresh path.
- If refresh fails, mark account unavailable and route to another account.
- Do not count refresh failure as model capacity pressure.

## Acceptance Criteria

Automated tests must prove:

- StableDownstream fallback responses set `contentSuccess=false`.
- Real upstream text responses set `contentSuccess=true`.
- `admission_pressure` does not immediately fallback while continuity queue has time remaining.
- Open circuit requests wait for capacity recovery before emitting fallback.
- Attempt-budget exhaustion records the suppressed status and `contentSuccess=false`.
- Stream fallback is syntactically valid and does not leak internal marker text.
- Non-stable and non-Opus behavior remains unchanged.

Live UAT must prove:

- sub2api Opus 4.7 calls return HTTP `200`.
- No body contains gateway-level `HTTP 429`, `HTTP 502`, or `HTTP 503`.
- Successful UAT requires non-empty assistant text or valid tool use.
- `output_tokens=0` is not counted as content success.
- Kiro-Go request logs and sub2api usage rows can be correlated by time/request id.
- During a pressure window, readiness reports degraded or blocked instead of healthy.

## Rollout Plan

1. Add observability fields first so current empty fallback behavior is visible.
2. Add tests that fail on current `contentSuccess` behavior.
3. Add capacity queue behind config flag.
4. Enable queue for Opus 4.7 StableDownstream only.
5. Run focused Go tests.
6. Run Docker UAT against real Kiro-Go and sub2api.
7. Tune queue defaults based on observed pressure and latency.

## Operational Guidance

When readiness is blocked:

- Do not increase sub2api concurrency.
- Do not run broad model refreshes or account probes.
- Let temporary-limited accounts cool down.
- Prefer a small number of real user requests as half-open probes.
- Watch `contentSuccessRate`, not just HTTP status.

When readiness recovers:

- Gradually restore safe concurrency.
- Keep jitter on probes and retries.
- Continue tracking empty completions for at least one pressure window.

## Risks

- A longer queue can increase Claude Code latency during true upstream outages.
- If sub2api has a shorter client timeout than Kiro-Go continuity wait, sub2api may still terminate the request first.
- Empty StableDownstream fallback may still be necessary as a last resort for transport continuity, but it must never be treated as content success.
- More conservative account cooldowns can reduce throughput during transient pressure but should reduce repeated temporary-limit escalation.

## Open Implementation Decisions

- Exact continuity deadline default should be validated in Docker UAT. Start with 120 seconds, then tune.
- Stream heartbeat compatibility with Claude Code should be tested before enabling heartbeat events.
- Persisted pressure state format should be minimal and avoid writing secrets.
- The admin UI can be updated after API/log fields are stable.
