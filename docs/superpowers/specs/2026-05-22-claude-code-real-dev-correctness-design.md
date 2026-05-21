# Claude Code Real Development Correctness Design

Date: 2026-05-22
Status: Draft for user review
Scope: Kiro-Go Opus 4.7 behavior when Claude Code calls Kiro-Go through sub2api for real development workflows

## Decision

Optimize for real Claude Code development correctness first.

Kiro-Go must not close a Claude Code development turn with assistant fallback text when Opus 4.7 capacity is unavailable. A real development workflow is successful only when Claude Code receives genuine upstream model content and completes the requested file/test work. Stable downstream transport behavior can still exist for simple compatibility calls, but it must not create a false development success.

## Problem

The previous transport fix changed some retryable upstream failures into stable HTTP responses so Claude Code would not enter an endless retry loop. That avoids one failure mode, but it exposes a more serious semantic bug for real development workflows:

- Claude Code treats a valid assistant message as a completed turn.
- Kiro-Go can return fallback assistant text such as "Opus 4.7 upstream capacity is temporarily unavailable".
- The CLI then stops without editing files or running the intended workflow.
- HTTP `200` and a syntactically valid Anthropic response therefore do not prove that the development task succeeded.

This is unacceptable for GSD/subagent development flows. The gateway should prefer waiting, queueing, or explicit retryable failure over fake assistant completion.

## External Grounding

`jwadow/kiro-gateway` is useful because it treats gateway reliability as account orchestration, not text fallback. The patterns worth borrowing are account-level failover, recoverable versus fatal error classification, sticky successful account selection, circuit breaker backoff, and retry before visible stream content. It does not treat a capacity placeholder as successful model content.

`zeoak9297/KiroSwitchManager` is mostly product/operator documentation, but it reinforces the operational needs: multi-account switching, token refresh, stream heartbeat, model lock, visible quota state, and avoiding message truncation or corrupted task completion.

Anthropic streaming semantics are also important. `ping` events are legal keepalives, but they are not assistant content. Once a stream emits `message_start` and content blocks, Kiro-Go cannot transparently replay the turn through another account.

References:

- https://github.com/jwadow/kiro-gateway
- https://github.com/zeoak9297/KiroSwitchManager
- https://platform.claude.com/docs/en/build-with-claude/streaming

## Architecture

### 1. Request Classification

Kiro-Go should classify generation requests before choosing the stable downstream behavior:

- `claude_code_dev`: Anthropic `/v1/messages` request from Claude Code with session/beta/tool/development-flow signals.
- `claude_code_simple`: Claude Code or sub2api smoke call without development/tool-loop signals.
- `openai_compatible`: OpenAI-compatible downstream request.
- `background`: probes, readiness checks, and other non-user generation-like calls.

The classifier should be conservative. If a Claude Code request includes tool schemas, tool results, subagent headers, session headers, or Claude Code beta markers, treat it as `claude_code_dev`.

### 2. Development Completion Semantics

For `claude_code_dev`, completion must mean real upstream model content:

- non-streaming success requires genuine assistant content/tool_use from upstream;
- streaming success requires legal Anthropic message events from upstream, not local fallback text;
- fallback text must never be sent as assistant content;
- request logs must record fallback/timeouts as `contentSuccess=false`;
- a turn that timed out waiting for capacity must be observable as a retryable capacity failure.

This preserves Claude Code's task semantics: either it gets real model behavior, or it knows the turn did not complete.

### 3. Capacity Governance

The existing `claudeCodeGovernor` should keep session-level fairness, but real development workflows also need global model capacity control:

- use Opus 4.7 readiness/admission snapshot to derive a dynamic safe capacity;
- reserve at least one interactive slot when any safe capacity exists;
- admit subagents only within the remaining safe capacity;
- when readiness is `degraded`, `half_open`, or dominated by `cooling_down`, queue subagents instead of racing every account;
- queue entries must be cancellable through the request context;
- queue wait cannot exceed the request budget.

This prevents concurrent Claude Code agents from pushing all accounts into cooldown and producing only fallback turns.

### 4. Stable Downstream Split

Stable downstream behavior should split by request class:

- `claude_code_dev`: wait for real content; if capacity does not recover, return retryable error or close a stream without assistant fallback content.
- `claude_code_simple`: keep compatibility fallback only if it is not likely to be interpreted as completed development work.
- `openai_compatible`: keep existing stable fallback response shape, but record it as transport fallback, not content success.
- `background`: fail fast or skip under pressure.

This keeps sub2api compatibility without letting a gateway placeholder masquerade as a completed coding turn.

### 5. Account Failover

Borrow the useful part of `kiro-gateway`:

- classify upstream errors as recoverable or fatal;
- retry recoverable errors before first visible assistant content;
- skip accounts in `temporary_limited`, cooldown, auth failure, or quota exhaustion states;
- prefer a sticky successful account for the same Claude Code session;
- do not mark fallback content as account/model success.

`model_capacity_limited` should reduce model safe concurrency. It should not automatically poison every account as if each account had a fatal failure.

## Error Handling

Recoverable capacity errors:

- `admission_pressure`
- `cooling_down`
- `temporary_limited`
- `attempt_budget_exhausted`
- first-token timeout before `message_start`

Behavior: queue, heartbeat, switch account if still before visible assistant content, or return retryable failure after the budget expires.

Fatal account errors:

- invalid token
- auth failure
- disabled account
- exhausted quota when over-usage is not allowed

Behavior: remove that account from routing, expose it in readiness, and continue with other eligible accounts.

Streaming rules:

- before `message_start`, Kiro-Go may send Anthropic `ping` keepalives while waiting;
- after `message_start`, Kiro-Go must not replay the turn through another account;
- local fallback text must never be emitted as a content block for `claude_code_dev`.

## Tests

Unit coverage should include:

- Claude Code development requests under `admission_pressure` do not receive assistant fallback text.
- Non-development sub2api/simple requests keep existing stable fallback compatibility.
- Streaming Claude Code queue wait emits only `ping` before upstream content.
- `safeConcurrency=1` admits only one development request and queues/rejects excess subagents.
- fallback and timeout logs always set `contentSuccess=false`.
- dotted and dashed Opus 4.7 model names remain equivalent.

Integration coverage should include fake upstream scenarios:

- first account returns temporary limit, second account succeeds, and Claude Code receives real content;
- all accounts are cooling down, and Kiro-Go returns retryable failure without assistant fallback;
- concurrent subagents do not exceed dynamic safe capacity.

## Real UAT

Use the live Docker environment:

- Kiro-Go: `http://127.0.0.1:8080`
- sub2api: `http://127.0.0.1:18080`
- sub2api Postgres container for usage/request evidence

Run real Claude Code against a fresh test project:

- one development task must edit a file and pass its selected test;
- two concurrent development tasks must either queue and complete with real content or fail explicitly as retryable capacity pressure;
- requests over safe concurrency must not return HTTP `200` assistant fallback as task completion.

Use Playwright-MCP and API/database evidence:

- capture admin readiness;
- capture request logs and usage views;
- compare screenshots, API evidence, and database rows;
- mark PASS only when all evidence agrees.

## Acceptance Criteria

The fix is accepted only if all of the following are true:

- Claude Code development output does not contain capacity fallback text as a completed assistant answer.
- Claude Code actually modifies the requested files.
- The generated project tests pass.
- Kiro-Go request logs do not mark fallback or timeout as `contentSuccess=true`.
- If readiness is `degraded`, reason codes match real account/admission state.
- High-concurrency tests either complete with real content or fail explicitly; no fake success is allowed.

## Out Of Scope

- Rewriting the entire account pool.
- Changing non-Opus model behavior.
- Guaranteeing unlimited subagent concurrency during upstream capacity shortage.
- Treating all HTTP `200` responses as user-visible success.
