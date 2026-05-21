# Roadmap: Kiro-Go Opus 4.7 Sustainable Health

## Overview

Phase 1-3 are the completed previous baseline for Claude Code compatibility, high-availability account routing, and Kiro ecosystem operations. Milestone v1.1 continues numbering at Phase 4 and turns the existing `sub2api -> Kiro-Go -> Kiro Opus 4.7` path into a measurable sustainable health contract: Kiro-Go exposes truthful readiness, sub2api consumes it before dispatch, upstream failures drive correct retry/cooldown behavior, and final PASS depends on aligned latest-code UAT evidence.

## Milestones

- Completed baseline: Phases 1-3, completed 2026-05-20 with remaining human/live evidence notes.
- Current milestone: v1.1 Opus 4.7 Sustainable Health, Phases 4-7 planned.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Previous baseline work
- Integer phases (4, 5, 6, 7): v1.1 planned milestone work
- Decimal phases (4.1, 4.2): Urgent insertions, if needed

- [x] **Phase 1: A - Claude Code Official Compatibility** - Previous baseline for official-like Claude Code API behavior.
- [x] **Phase 2: B - High-Availability Account Pool and sub2api Routing** - Previous baseline for account-aware routing and high-concurrency regression evidence.
- [x] **Phase 3: C - Kiro Ecosystem Operations** - Previous baseline for admin operations, scheduler controls, and Kiro ecosystem visibility.
- [x] **Phase 4: Opus 4.7 Readiness Contract and Scheduler Truth** - Make Kiro-Go the truthful source for Opus 4.7 readiness, schedulability, retry budgets, and real content success.
- [x] **Phase 5: sub2api Readiness Provider Integration** - Make sub2api consume Kiro-Go readiness before dispatching Opus 4.7 traffic.
- [x] **Phase 6: Upstream Error Semantics and Safe Kiro CLI Diagnostics** - Classify upstream failures structurally and expose safe, explicit Kiro CLI diagnostics without reading secrets.
- [x] **Phase 7: Observability, Latest-Code UAT, and Acceptance Evidence** - Prove the full contract with aligned logs, APIs, headers, database state, admin evidence, and latest-code stream/non-stream UAT.

## Phase Details

<details>
<summary>Previous Baseline: Phases 1-3</summary>

### Phase 1: A - Claude Code Official Compatibility
**Goal**: Claude Code can use Kiro-Go API with official-like behavior for models, SSE, tool loops, thinking, prompt cache metadata, and large context.
**Depends on**: Existing validated routing baseline
**Requirements**: CC-01, CC-02, CC-03, CC-04, CC-05, CC-06, CC-07
**Success Criteria** (what must be TRUE):
  1. Claude Code-style non-stream and stream calls return correct Anthropic-compatible events and content.
  2. Tool-use/tool-result loops no longer produce empty tool-use behavior when the model should call tools.
  3. Model alias and readiness APIs explain requested, mapped, listed, and schedulable model state.
  4. UAT includes screenshots, API, and log evidence and marks PASS only when evidence agrees.
**Plans**: 3 plans

Plans:
- [x] 01-01: Build the Claude Code parity matrix and model resolver/readiness contract.
- [x] 01-02: Harden Anthropic SSE, tool-loop, thinking, prompt-cache, and large-context translation.
- [x] 01-03: Add real Claude Code UAT harness with screenshot/API/log evidence and PASS gating.

### Phase 2: B - High-Availability Account Pool and sub2api Routing
**Goal**: Kiro-Go schedules accounts by real per-account availability and integrates with sub2api so high-concurrency Claude Code calls route correctly instead of amplifying 429s.
**Depends on**: Phase 1
**Requirements**: HA-01, HA-02, HA-03, HA-04, HA-05, HA-06, HA-07
**Success Criteria** (what must be TRUE):
  1. A temporary-limited account is skipped while other viable accounts still serve requests.
  2. Kiro model capacity, account temporary limit, quota, auth, and network failures are separately logged and surfaced.
  3. Background auto-refresh and health-check traffic cannot repeatedly hit accounts that are cooling down.
  4. sub2api Opus 4.7 stream and non-stream concurrency tests return correct content with latency recorded.
**Plans**: 3 plans

Plans:
- [x] 02-01: Refactor failure taxonomy, per-account cooldown, and attempt-trace observability.
- [x] 02-02: Implement account-aware admission, scheduler policies, and background probe throttling.
- [x] 02-03: Verify sub2api retry semantics with API, database, Playwright screenshot, and concurrency evidence.

### Phase 3: C - Kiro Ecosystem Operations
**Goal**: Operators managing many Kiro accounts can import, diagnose, route, and observe Kiro/CLI/WebSearch behavior without fragile manual debugging.
**Depends on**: Phase 2
**Requirements**: KE-01, KE-02, KE-03, KE-04, KE-05
**Success Criteria** (what must be TRUE):
  1. Admin account onboarding reports actionable auth, profile, model, quota, and proxy diagnostics.
  2. Scheduler policy controls are visible and their routing decisions are inspectable.
  3. WebSearch/MCP calls show query, result, status, and injection evidence in logs or admin views.
  4. Fleet admin workflows support batch refresh, health check, enable/disable, filtering, and export with screenshot evidence.
**Plans**: 3 plans

Plans:
- [x] 03-01: Add CLI credential import/validation and account onboarding diagnostics.
- [x] 03-02: Add scheduler policy controls and admin fleet operations.
- [x] 03-03: Improve WebSearch/MCP observability and ecosystem documentation.

</details>

### Phase 4: Opus 4.7 Readiness Contract and Scheduler Truth
**Goal**: sub2api and admins can trust Kiro-Go readiness, scheduler preview, routing priority, and retry budgets as one consistent Opus 4.7 contract.
**Depends on**: Phase 3
**Requirements**: RDY-01, RDY-02, RDY-03, RDY-04, RDY-05, RDY-06
**Success Criteria** (what must be TRUE):
  1. sub2api and admins can query versioned Opus 4.7 readiness with `healthy`, `degraded`, or `blocked`, safe concurrency, counts, retry timing, actions, and reason codes.
  2. Readiness, scheduler preview, and account routing explain the same account/model eligibility state for cooldown, breaker, token/session, and model-list visibility.
  3. Recent real `account + claude-opus-4.7` content success affects readiness and routing priority, while fallback or transport-only success never counts as healthy model success.
  4. Opus 4.7 requests stop after bounded attempt, wait, and first-token retry budgets and return retryable pressure metadata when exhausted.
  5. Streaming retries happen only before downstream SSE content begins; started streams terminate through protocol-safe error behavior.
**Plans**: 5 plans

Plans:
- [x] 04-01-PLAN.md — Add pool account/model real content-success evidence and routing priority.
- [x] 04-02-PLAN.md — Record real content success from handlers while excluding fallback and transport-only success.
- [x] 04-03-PLAN.md — Lock the versioned fleet readiness contract and scheduler eligibility parity.
- [x] 04-04-PLAN.md — Enforce bounded Opus 4.7 retry budgets and streaming no-replay behavior.
- [x] 04-05-PLAN.md — Render safe readiness contract evidence in the existing admin fleet card.
**UI hint**: yes

### Phase 5: sub2api Readiness Provider Integration
**Goal**: sub2api uses Kiro-Go readiness before dispatch so Opus 4.7 traffic is skipped, capped, or scheduled according to the effective upstream model and current fleet state.
**Depends on**: Phase 4
**Requirements**: S2A-01, S2A-02, S2A-03, S2A-04, S2A-05, S2A-06
**Success Criteria** (what must be TRUE):
  1. Operators can configure a Kiro-Go readiness provider with endpoint, timeout, TTLs, fail mode, and model match rules.
  2. sub2api checks readiness using the effective upstream model before consuming account concurrency across sticky, load-balanced, and fallback-wait scheduling paths.
  3. `blocked` Kiro-Go candidates are skipped or temporarily unscheduled without permanent account errors, while `degraded` candidates are prioritized or capped within safe concurrency.
  4. sub2api logs readiness status, cache behavior, TTL, retry-after, safe concurrency, requested/effective model, and Kiro-Go identifiers with secrets redacted.
**Plans**: 3 plans
**UI hint**: yes

Plans:
- [x] 05-01-PLAN.md — Configure and query Kiro-Go readiness in sub2api.
- [x] 05-02-PLAN.md — Gate sub2api scheduling before concurrency consumption.
- [x] 05-03-PLAN.md — Add readiness observability and regression coverage.

### Phase 6: Upstream Error Semantics and Safe Kiro CLI Diagnostics
**Goal**: Kiro-Go classifies recoverable and terminal upstream failures correctly and provides safe, explicit Kiro CLI diagnostics without exposing runtime secrets or performing unsafe account actions by default.
**Depends on**: Phase 5
**Requirements**: ERR-01, ERR-02, ERR-03, ERR-04, ERR-05, CLI-01, CLI-02, CLI-03, CLI-04, CLI-05
**Success Criteria** (what must be TRUE):
  1. Kiro-Go parses structured upstream error evidence before string fallback and classifies temporary account limit, model pressure, rate limit, quota, auth, suspension, timeout, and 5xx separately.
  2. Account-level failures cool down only the affected account, while model-level pressure changes model breaker and retry behavior without poisoning viable accounts.
  3. Foreground Opus 4.7 pressure puts background probes and health checks into bounded, jittered, cooldown-aware quiet mode.
  4. Admins can view redacted CLI path, availability, version, router state, home presence, model-list status, and read-only diagnostic output.
  5. Credit-consuming probes, WebSearch live probes, login/logout, updates, writes, imports, token refresh, or router changes require explicit admin triggering and audit logging.
**Plans**: 3 plans
**UI hint**: yes

Plans:
- [x] 06-01-PLAN.md — Structured upstream error parser and reason mapping.
- [x] 06-02-PLAN.md — Wire structured reasons into retry, cooldown, breaker, and quiet mode.
- [x] 06-03-PLAN.md — Safe Kiro CLI diagnostics API, UI, and audit evidence.

### Phase 7: Observability, Latest-Code UAT, and Acceptance Evidence
**Goal**: The v1.1 contract is observable end to end and passes only when latest-code stream and non-stream sub2api Opus 4.7 traffic proves real content success with aligned evidence.
**Depends on**: Phase 6
**Requirements**: OBS-01, OBS-02, OBS-03, OBS-04, UAT-01, UAT-02, UAT-03, UAT-04
**Success Criteria** (what must be TRUE):
  1. Kiro-Go logs and admin/API evidence show readiness at admission, safe concurrency, retry-after, selected account, effective model, attempt trace, pressure reason, fallback state, and content-success evidence.
  2. sub2api logs distinguish readiness-blocked scheduling decisions from upstream 429/529 responses, account limits, overloads, and temporary unschedulable rules.
  3. Safe diagnostic headers or admin API fields expose readiness, retryability, circuit state, safe concurrency, and content success, with explicit control over propagated headers.
  4. Latest-code non-stream and stream Opus 4.7 UAT each prove 100/100 real content successes only when readiness reports viable capacity and no started stream is transparently replayed.
  5. Blocked-capacity UAT proves explicit `blocked` or retryable exhausted-pool behavior with `Retry-After`, and final PASS includes aligned Kiro-Go logs/API, fleet readiness, sub2api logs, database state, headers, and admin screenshots.
**Plans**: 2 plans
**UI hint**: yes

Plans:
- [x] 07-01-PLAN.md — Lock Kiro-Go acceptance evidence API, request-log evidence, and safe headers.
- [x] 07-02-PLAN.md — Define repeatable latest-code UAT evidence schema, validator, and blocked-capacity fixture.

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7.

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|----------------|--------|-----------|
| 1. A - Claude Code Official Compatibility | Previous baseline | 3/3 | Human validation needed | 2026-05-20 |
| 2. B - High-Availability Account Pool and sub2api Routing | Previous baseline | 3/3 | Human validation needed | 2026-05-20 |
| 3. C - Kiro Ecosystem Operations | Previous baseline | 3/3 | Human validation needed | 2026-05-20 |
| 4. Opus 4.7 Readiness Contract and Scheduler Truth | v1.1 | 5/5 | Complete | 2026-05-21 |
| 5. sub2api Readiness Provider Integration | v1.1 | 3/3 | Complete | 2026-05-21 |
| 6. Upstream Error Semantics and Safe Kiro CLI Diagnostics | v1.1 | 3/3 | Complete | 2026-05-21 |
| 7. Observability, Latest-Code UAT, and Acceptance Evidence | v1.1 | 2/2 | Complete | 2026-05-21 |
