# Claude Code High-Concurrency Correctness Design

Date: 2026-05-20
Scope: `/www/Kiro-Go` plus coordinated `/www/sub2api` behavior

## Goal

Make Claude Code calls through `sub2api -> Kiro-Go -> Kiro` correct, observable, and recoverable under high concurrency.

This design does not promise to bypass Kiro official upstream limits. It promises that when Kiro returns capacity or temporary-limit 429s, Kiro-Go and sub2api classify them correctly, stop amplifying them, expose consistent diagnostics, and return valid Anthropic-compatible responses to Claude Code. If a non-Kiro fallback account is configured, sub2api may switch to it. If no fallback exists, it must fail fast with a correct retryable error.

## Evidence

Observed failing chain:

`Claude Code -> sub2api /v1/messages -> api_key_id=2 claude -> group_id=1 -> account_id=24 kiro_claude_01 -> Kiro-Go :8080 -> Kiro official`

Kiro-Go logs show real upstream failures:

- `INSUFFICIENT_MODEL_CAPACITY` for Opus 4.7.
- `Due to suspicious activity... temporary limits` from Kiro official.
- Auto refresh also receives `429 Too many requests`.

sub2api logs and DB show:

- Claude group has one account: `kiro_claude_01`, concurrency 12.
- Claude Code sticky session binds to account 24.
- sub2api performs same-account retries on Kiro-Go 429s.
- `ops_error_logs` records repeated `status_code=429`, `upstream_status_code=429`, `account_id=24`, `model=claude-opus-4-7`.

Important distinction:

- Kiro-Go model listing/readiness can say a model is schedulable.
- A real generation request can still fail due to model capacity, risk-group temporary limits, or background refresh pressure.

## External References Considered

Anthropic API compatibility requirements:

- Messages API request/response shape.
- Messages streaming SSE events: `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`.
- `rate_limit_error` and `Retry-After` behavior.
- `messages/count_tokens` compatibility or explicit unsupported fallback.
- Tool use and tool result blocks.
- Claude Code gateway mode headers, base URL, auth token, model selection, and sticky session metadata.

`jwadow/kiro-gateway` comparison:

- Multi-account failover with circuit breaker and sticky behavior.
- Anthropic adapter separated from shared Kiro conversion core.
- Anthropic SSE formatter.
- Tool use/tool result and tool-result image handling.
- Payload guard and truncation recovery.
- MCP/web search support.

Kiro-Go already implements several comparable capabilities, so this phase focuses on high-concurrency correctness rather than copying all features.

## Non-Goals

This phase will not implement every `kiro-gateway` feature.

Excluded from this pass:

- Full MCP web search emulation.
- Complete truncation recovery system.
- Replacing Kiro-Go's Go architecture with the Python gateway architecture.
- Silent model downgrade from `claude-opus-4-7` to Sonnet or OpenAI by default.
- Claiming 100 percent successful responses when Kiro official is actually limiting the upstream pool.

Optional fallback can be added as a configuration-controlled behavior, default off unless explicitly enabled.

## Design

### 1. Kiro-Go Layered Readiness

Kiro-Go readiness should separate these states:

- `model_listed`: requested model exists in Kiro-Go model cache or model mapping.
- `account_schedulable`: local account is enabled, token-valid, not over quota, and not locally cooled down.
- `risk_group_ready`: profile/user risk group is not in temporary-limit cooldown.
- `generation_ready`: recent real generation probe succeeded or no recent failure blocks the model.
- `admission_ready`: model admission gate has capacity or queue room.

The existing admin readiness response should expose summary counts and per-account reasons. The UI and UAT must not treat `model_listed=true` as proof that real generation will succeed.

### 2. Kiro-Go Temporary-Limit Dampening

When Kiro official returns suspicious-activity temporary limits:

- Classify as `temporary_limited`, not generic 429.
- Apply account cooldown and risk-group cooldown when multiple accounts in the same risk group are hit.
- Avoid continuing to test all accounts in the same request once the signal is clearly risk-group temporary limit.
- Expose `Retry-After` based on the computed cooldown floor.

Background traffic must respect cooldown:

- Auto refresh and health check must skip accounts/risk groups that are cooling down.
- If a background call itself receives 429, it must not immediately retry aggressively.
- Admin testing must show `temporary_limited` without hitting upstream when cooldown is active.

### 3. sub2api Kiro-Go 429 Classification

sub2api should special-case Kiro-Go upstream errors that contain:

- `TEMPORARY_LIMITED`
- `No available accounts for ... upstream temporary limits are cooling down`
- Kiro temporary-limit message body from Kiro official

For those errors:

- Do not run same-account retry three times.
- Temporarily mark the sub2api account as unschedulable for the parsed retry window.
- Return Anthropic-compatible JSON error:
  - HTTP 429
  - `type: error`
  - `error.type: rate_limit_error`
  - useful message including temporary-limit/cooling semantics
  - `Retry-After`
- Log `ops_error_logs` with account, group, requested model, upstream status, and upstream detail.

This prevents sub2api from amplifying Kiro-Go's risk-group cooldown.

### 4. sub2api Fallback Behavior

If the Claude API key's group has only `kiro_claude_01`, sub2api cannot provide successful failover. It can only fail correctly.

Supported fallback modes:

- No fallback: default. Return correct 429 and retry hints.
- Fallback account in same group: switch to another Anthropic-compatible account if configured.
- Fallback group: use existing group fallback if explicitly configured.
- Model downgrade: optional config only, default off. If enabled, map Opus temporary-limit failures to a configured model such as `claude-sonnet-4.5`.

The implementation must not silently change the requested model unless a specific fallback policy is enabled.

### 5. Claude Code Protocol Correctness

The implementation must preserve these client-facing behaviors:

- Non-stream `/v1/messages` returns valid Anthropic message JSON.
- Stream `/v1/messages` starts with `message_start` when generation begins.
- Pre-generation upstream failures return JSON errors, not broken SSE.
- Mid-stream failures emit a valid Anthropic error event if the stream has already started.
- Tool use blocks preserve IDs, names, and JSON input deltas.
- Tool result blocks and large request bodies are accepted or fail with clear, valid errors.
- `anthropic-version`, `anthropic-beta`, Claude Code session metadata, and client request IDs are preserved for routing and diagnostics.

### 6. Observability

Kiro-Go should expose enough information to answer:

- Is the model listed?
- How many accounts are locally schedulable?
- Which accounts/risk groups are cooling down?
- Is model admission under pressure?
- What was the last upstream failure reason?

sub2api should expose enough information to answer:

- Which API key/group/account handled the request?
- Was sticky session honored?
- Did it retry same account, switch account, or stop immediately?
- Was the upstream 429 from Kiro-Go temporary-limit/capacity/rate-limit?
- Did DB usage/error records match HTTP outcomes?

## Implementation Plan Outline

Detailed implementation planning will follow after this design is reviewed.

Expected work areas:

- Kiro-Go:
  - `pool/account.go`
  - `proxy/handler.go`
  - `proxy/request_log.go`
  - readiness/admin UI data model
  - tests for risk-group cooldown, admin readiness, and error mapping

- sub2api:
  - `backend/internal/handler/failover_loop.go`
  - `backend/internal/service/gateway_service.go`
  - rate-limit/temp-unschedule logic
  - ops error detail logging
  - tests for Kiro-Go `TEMPORARY_LIMITED` no same-account retry

## UAT Plan

Create a new directory:

`docs/superpowers/uat/claude-code-high-concurrency-correctness-<timestamp>/`

Artifacts:

- `UAT-RESULT.md`
- API JSON summaries
- DB query outputs
- Playwright screenshots
- Playwright analysis JSON
- raw probe outputs for stream and non-stream requests

API checks:

- Kiro-Go direct `/v1/messages`, stream false.
- Kiro-Go direct `/v1/messages`, stream true.
- sub2api Claude key `/v1/messages`, stream false.
- sub2api Claude key `/v1/messages`, stream true.
- large Claude Code-like body around 300 KB.
- tool_use/tool_result path.
- count_tokens behavior.
- controlled concurrency test with bounded workers.
- forced or observed 429 classification path.

DB checks:

- `accounts` and `account_groups` prove group 1 account composition.
- `usage_logs` contain only successful requests.
- `ops_error_logs` contain failed 429s with correct account/model/group/upstream status.
- account temp-unschedulable fields reflect classified cooldown if triggered.

Playwright checks:

- Kiro-Go admin readiness page.
- Kiro-Go request logs page.
- sub2api admin accounts/groups page.
- sub2api error/usage log pages.

Pass criteria:

- No empty HTTP responses.
- No stream response starts as SSE after a pre-generation upstream error.
- No same-account retry loop for Kiro-Go `TEMPORARY_LIMITED`.
- 429s have valid Anthropic error envelope and `Retry-After` when cooldown is known.
- API, DB, logs, and screenshots agree on account, model, status, and reason.
- UAT marks PASS only after screenshot analysis confirms the visible UI state matches API and DB evidence.

