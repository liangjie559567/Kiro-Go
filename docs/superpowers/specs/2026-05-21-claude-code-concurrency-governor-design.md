# Claude Code Concurrency Governor Design

Date: 2026-05-21
Status: Draft for user review
Scope: Kiro-Go Opus 4.7 downstream behavior for Claude Code through sub2api

## Decision

Implement Option C: a Claude Code Concurrency Governor.

The goal is not to make fake `200` fallbacks look healthy. The goal is to keep Claude Code usable under real Opus 4.7 pressure by preserving protocol correctness, prioritizing foreground turns, isolating subagent load, and routing only to accounts that are actually likely to produce real upstream content.

## Problem

The current Kiro-Go stack has improved transport behavior, but Claude Code still has a real failure mode:

- A foreground `继续` turn can wait behind subagent traffic.
- Subagents can consume model and account concurrency without producing useful work.
- Opus 4.7 temporary limits and model-capacity errors can spread across accounts when retries are not coordinated.
- Returning a syntactically valid StableDownstream fallback is not enough because Claude Code treats it as a completed assistant turn.
- A healthy Docker process and HTTP `200` do not prove real content correctness.

For this phase, "healthy" means:

- Claude Code receives only protocol-valid Anthropic/OpenAI responses.
- Stream responses obey Anthropic SSE order and use `ping` only as keepalive.
- Foreground user turns are not starved by same-session subagents.
- Healthy upstream capacity is used for real content instead of fallback text.
- When upstream Opus 4.7 is globally blocked, Kiro-Go reports a controlled degraded/blocked state instead of pretending content succeeded.

## External Grounding

Anthropic streaming semantics require a stream to build a message through `message_start`, content block events, `message_delta`, and `message_stop`; `ping` events are valid keepalives but not assistant content. This defines the transparent retry boundary: Kiro-Go may wait or switch upstream before `message_start`, but after visible message content starts it must preserve that one stream.

`jwadow/kiro-gateway` shows practical gateway patterns worth borrowing: multi-account support, intelligent failover, automatic retry on 403/429/5xx, and temporary removal of repeatedly failing accounts before periodic recovery checks. Kiro-Go should borrow the control pattern, not the exact failure buckets, because Opus 4.7 model capacity and account temporary limits need separate treatment.

`zeoak9297/KiroSwitchManager` reinforces operator requirements: automatic account switching, token refresh, visible account usage/quota, Claude/OpenAI compatible proxying, stream heartbeat, model locking, and stable machine identity per account.

Envoy's gateway model supports the same shape: circuit breakers prevent overloaded upstreams from receiving unlimited active or pending requests, and outlier detection ejects unhealthy upstream hosts temporarily instead of retrying them blindly.

References:

- https://platform.claude.com/docs/en/build-with-claude/streaming
- https://github.com/jwadow/kiro-gateway
- https://github.com/zeoak9297/KiroSwitchManager
- https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/circuit_breaking
- https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/outlier

## Architecture

### 1. Request Classifier

Add a small classifier that assigns every generation request to one of three lanes:

- `interactive`: foreground Claude Code user turn. Highest priority.
- `subagent`: Claude Code background agent task. Medium priority.
- `background`: health probes, cache warmups, model checks, and other non-user work. Lowest priority.

Classification inputs:

- `User-Agent`
- `X-Sub2API-Request`
- Claude Code session, agent, and parent-agent headers when present
- Anthropic `metadata.user_id` or equivalent local metadata when present
- endpoint and stream flag
- request body traits such as tool-heavy subagent style payloads

The classifier must be conservative. If a request appears to be a direct user turn and cannot be proven to be a subagent/background request, treat it as `interactive`.

### 2. Three-Level Concurrency Gates

The Governor adds explicit budgets on top of existing model admission and account reservation:

- model gate: total Opus 4.7 active requests and pending queue
- account gate: active requests/streams per Kiro account
- session gate: per Claude Code session budgets for foreground and subagent traffic

Initial defaults:

- `interactive` gets at least one reserved slot per active Claude Code session when any safe model capacity exists.
- `subagent` has a per-session cap, default `2`.
- `background` has no reserved Opus 4.7 capacity during pressure.
- model queue order is weighted: `interactive` before `subagent` before `background`.
- a request may wait only inside its request budget; no queue has an independent unbounded timer.

This prevents a fan-out of subagents from consuming all capacity while the user is waiting in the main conversation.

### 3. Priority Queue

Replace direct "first waiter wins" behavior for Opus 4.7 with a small priority scheduler:

- `interactive` can move ahead of queued `subagent` and `background` requests.
- Already-started upstream streams are never killed or replayed for priority.
- queued requests are cancellable through `r.Context()`.
- queue entries store lane, session id, agent id, enqueue time, request deadline, and model.
- queue wakeups happen on account release, model pressure recovery, and request cancellation.

Fairness rule:

- `interactive` has priority, but a continuous stream of interactive requests must still allow bounded subagent progress when there is spare capacity.

### 4. Account Health State Machine

Add a first-class account/model health state used by account selection:

- `healthy`: eligible for all lanes.
- `warm`: recently recovered; eligible with reduced concurrency.
- `degraded`: lower priority because of recent latency/errors.
- `model_capacity_limited`: Opus 4.7 capacity signal; affects model circuit more than the account.
- `temporary_limited`: account-specific temporary Kiro limit; account is cooled down.
- `auth_refreshing`: token refresh in progress; do not start new generation.
- `quota_exhausted`: disabled for generation until quota changes.
- `quarantined`: repeated hard failures or operator disabled.

Routing score should consider:

- state
- active stream/request count
- first-token EWMA for the requested model
- recent success rate
- recent 429/5xx/EOF
- sticky session success for the same Claude Code session
- `Retry-After` or local cooldown expiry

The key separation is that `model_capacity_limited` should not poison every account. It should reduce model safe concurrency and circuit state. `temporary_limited`, auth failure, and quota exhaustion remain account-specific.

### 5. First-Token EWMA Scheduler

Track first-token latency per account/model:

- `firstTokenEwmaMs`
- `firstTokenP95Ms`
- `lastFirstTokenAt`
- `firstTokenTimeouts`
- `recentStreamStarts`

Interactive requests prefer accounts with low first-token latency and recent success. Subagent requests can use a broader pool so they do not monopolize the fastest accounts.

For streaming:

- before `message_start`, Kiro-Go may wait, retry, or switch accounts while emitting only legal `ping` keepalive events downstream when a stream has already been opened.
- once `message_start` is emitted, no transparent account switch is allowed.
- after `tool_use`, preserve tool id continuity and do not fabricate a terminal answer.

### 6. Pressure-Aware Background Quiet Mode

Background operations must stop competing with foreground Opus 4.7 calls during pressure:

- skip generation-like probes while the Opus 4.7 circuit is degraded/open.
- add jitter to account health checks.
- cap concurrent token refreshes and health checks.
- allow token refresh needed for account survival, but do not combine it with model-generating probes.
- log quiet-mode skips so operators can see why background checks slowed down.

This reduces self-amplified temporary limits.

## Config

Add config under a new `claudeCodeGovernor` block:

```json
{
  "claudeCodeGovernor": {
    "enabled": true,
    "models": ["claude-opus-4.7"],
    "interactiveReservedPerSession": 1,
    "subagentMaxConcurrentPerSession": 2,
    "backgroundMaxConcurrent": 1,
    "queueMaxDepth": 300,
    "interactiveMaxWaitSeconds": 120,
    "subagentMaxWaitSeconds": 90,
    "backgroundMaxWaitSeconds": 15,
    "firstTokenEwmaHalfLife": 20,
    "temporaryLimitMinCooldownSeconds": 120,
    "quietModePressureScore": 2
  }
}
```

Defaults must be safe if the block is absent:

- Governor disabled until explicitly enabled in config. A separate rollout decision can enable it automatically for StableDownstream Opus 4.7 traffic after UAT passes.
- Existing non-Opus and non-Claude Code behavior remains unchanged.
- Legacy `modelAdmission` still applies as an outer safety cap.

## Request Logs

Add fields to `RequestLogEntry`:

- `priorityLane`
- `queuePosition`
- `sessionConcurrencyWaitMs`
- `accountConcurrencyWaitMs`
- `modelConcurrencyWaitMs`
- `selectedAccountHealthState`
- `selectedAccountFirstTokenEwmaMs`
- `firstTokenLatencyMs`
- `governorDecision`
- `governorWaitReason`
- `backgroundQuietModeSkipped`
- `contentSuccess`
- `contentFailureReason`

The log must distinguish:

- downstream transport success
- real upstream content success
- stable fallback completion
- queue timeout
- upstream pressure
- client cancellation

## Admin And Readiness

Extend the existing readiness/admin surface with a Claude Code Governor view:

- model
- circuit state
- safe concurrency
- active interactive/subagent/background counts
- queued interactive/subagent/background counts
- schedulable accounts by health state
- first-token P50/P95 by account class
- recent content success rate
- recent stable fallback count
- recent client cancellation count
- quiet-mode state

Readiness status:

- `healthy`: content success rate is acceptable and safe concurrency is available.
- `degraded`: capacity exists but queueing, latency, or fallback rate is elevated.
- `blocked`: no safe concurrency or all eligible accounts are cooling/auth/quota blocked.

## Data Flow

1. sub2api sends a Claude/OpenAI compatible request to Kiro-Go.
2. Kiro-Go authenticates and normalizes the model.
3. Request classifier assigns a lane.
4. Session, model, and account gates evaluate whether the request can run.
5. If capacity exists, account router chooses the healthiest eligible account.
6. If capacity does not exist, the request waits in the priority queue within its budget.
7. For streams, Kiro-Go keeps the downstream connection alive with legal `ping` while no message has started.
8. Upstream first token or tool event marks content progress.
9. Request log records lane, waits, selected state, first-token latency, attempts, and content outcome.
10. On pressure, account/model state is updated separately according to failure reason.

## Error And Fallback Contract

Do not use fake assistant text as a healthy answer.

Rules:

- If real upstream content is produced, return it normally.
- If no real content is produced but downstream must remain protocol-compatible, log `contentSuccess=false`.
- StableDownstream fallbacks are transport-only completions, not model success.
- For Claude streams, fallback SSE must be syntactically complete and must not contain internal marker text that Claude Code treats as useful output.
- For retryable upstream pressure, prefer queue/wait and account switch before terminal fallback.
- For client cancellation, release every gate and account reservation immediately.

## Implementation Slices

### Slice 1: Classification And Logging

- Add request classifier tests.
- Add log fields.
- Prove main turn, subagent, and background examples classify correctly.
- No scheduling behavior changes yet.

### Slice 2: Session Governor

- Add per-session budgets.
- Reserve one interactive slot per active session.
- Cap subagent concurrency per session.
- Add starvation tests where many subagents cannot block a main turn.

### Slice 3: Priority Model Queue

- Add weighted queue for Opus 4.7 generation.
- Respect request context cancellation.
- Track queue position and wait time.
- Keep existing modelAdmission as an outer cap.

### Slice 4: Account Health State

- Add account/model state separation.
- Add first-token EWMA.
- Update router to score by state, latency, and lane.
- Add tests for temporary limit vs model capacity behavior.

### Slice 5: Quiet Mode

- Add pressure-aware background suppression.
- Add jitter and concurrency cap for probes.
- Log quiet-mode skips.

### Slice 6: UAT Harness

- Add Docker/sub2api Claude Code concurrency UAT.
- Simulate 1 foreground turn plus 5, 10, and 20 subagent-like streams.
- Assert protocol correctness, content success, no fake fallback success, and bounded first-token latency when healthy capacity exists.

## Acceptance Criteria

Automated tests:

- classifier identifies interactive/subagent/background requests.
- subagent load cannot consume the interactive reserved slot.
- queued requests release on context cancellation.
- `message_start` boundary prevents transparent replay after downstream content begins.
- model capacity updates model state without poisoning every account.
- account temporary limit cools only that account.
- first-token EWMA changes account selection for interactive traffic.
- quiet mode skips background probes under pressure.
- non-Opus and non-Claude Code behavior is unchanged.

Live Docker UAT:

- Kiro-Go and sub2api containers stay healthy.
- Claude Code-like stream responses are valid Anthropic SSE.
- No downstream fake fallback is counted as content success.
- Foreground request gets first token before subagent backlog when at least one healthy account exists.
- Subagent concurrency is bounded and observable.
- Request logs prove lane, waits, selected account state, first-token latency, and content outcome.
- If upstream Opus 4.7 is globally unavailable, result is `BLOCKED_BY_UPSTREAM`, not PASS.

## Risks

- Lower subagent concurrency may make background Claude Code work slower, but it preserves the foreground user turn.
- A too-aggressive interactive reservation can underuse capacity when no user turn is active; idle reservations must be released quickly.
- First-token scoring can overfit to one fast account; include active-stream penalty and jitter.
- Cross-instance Kiro-Go deployment still needs shared state for perfect fairness. This design is local-instance first.

## Recommended First Plan

Start with Slices 1 and 2. They are low risk, directly address the user-visible "main conversation hangs while subagents fail" problem, and create observability before deeper routing changes.

Then implement Slices 3 and 4 together because priority queue behavior and account selection need to agree on the same health state.

Quiet mode and UAT should follow immediately after the scheduler changes so regressions are caught against the real Docker/sub2api path.
