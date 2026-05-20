# Roadmap: Kiro-Go Claude Code Compatibility and High Availability

## Overview

This milestone follows the requested A -> B -> C path. Phase 1 locks down Claude Code official compatibility, Phase 2 makes Kiro account scheduling and `/www/sub2api` high availability under real concurrent load, and Phase 3 improves Kiro ecosystem operations that make large account fleets easier to run and debug.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions, if needed

- [ ] **Phase 1: A - Claude Code Official Compatibility** - Make Kiro-Go behave like the official Anthropic API for Claude Code's real request shapes.
- [ ] **Phase 2: B - High-Availability Account Pool and sub2api Routing** - Ensure one account's 429 does not poison the pool or downstream Claude Code calls.
- [ ] **Phase 3: C - Kiro Ecosystem Operations** - Improve CLI/account onboarding, scheduler controls, WebSearch/MCP observability, and fleet admin workflows.

## Phase Details

### Phase 1: A - Claude Code Official Compatibility
**Goal**: Claude Code can use Kiro-Go API with official-like behavior for models, SSE, tool loops, thinking, prompt cache metadata, and large context.
**Depends on**: Existing validated routing baseline
**Requirements**: [CC-01, CC-02, CC-03, CC-04, CC-05, CC-06, CC-07]
**Success Criteria** (what must be TRUE):
  1. Claude Code-style non-stream and stream calls return correct Anthropic-compatible events and content.
  2. Tool-use/tool-result loops no longer produce "Done (0 tool uses / 0 tokens)" when the model should call tools.
  3. Model alias and readiness APIs explain requested, mapped, listed, and schedulable model state.
  4. UAT includes screenshots/API/log evidence and explicitly marks PASS only when evidence agrees.
**Plans**: 3 plans

Plans:
- [ ] 01-01: Build the Claude Code parity matrix and model resolver/readiness contract.
- [ ] 01-02: Harden Anthropic SSE, tool-loop, thinking, prompt-cache, and large-context translation.
- [ ] 01-03: Add real Claude Code UAT harness with screenshot/API/log evidence and PASS gating.

### Phase 2: B - High-Availability Account Pool and sub2api Routing
**Goal**: Kiro-Go schedules accounts by real per-account availability and integrates with sub2api so high-concurrency Claude Code calls route correctly instead of amplifying 429s.
**Depends on**: Phase 1
**Requirements**: [HA-01, HA-02, HA-03, HA-04, HA-05, HA-06, HA-07]
**Success Criteria** (what must be TRUE):
  1. A temporary-limited account is skipped while other viable accounts still serve requests.
  2. Kiro model capacity, account temporary limit, quota, auth, and network failures are separately logged and surfaced.
  3. Background auto-refresh and health-check traffic cannot repeatedly hit accounts that are cooling down.
  4. `/www/sub2api` Opus 4.7 10 concurrent x 10 stream and non-stream tests return correct content with max latency recorded.
**Plans**: 3 plans

Plans:
- [ ] 02-01: Refactor failure taxonomy, per-account cooldown, and attempt-trace observability.
- [ ] 02-02: Implement account-aware admission, scheduler policies, and background probe throttling.
- [ ] 02-03: Verify sub2api retry semantics with API, database, Playwright screenshot, and 10x10 concurrency evidence.

### Phase 3: C - Kiro Ecosystem Operations
**Goal**: Operators managing many Kiro accounts can import, diagnose, route, and observe Kiro/CLI/WebSearch behavior without fragile manual debugging.
**Depends on**: Phase 2
**Requirements**: [KE-01, KE-02, KE-03, KE-04, KE-05]
**Success Criteria** (what must be TRUE):
  1. Admin account onboarding reports actionable auth/profile/model/quota/proxy diagnostics.
  2. Scheduler policy controls are visible and their routing decisions are inspectable.
  3. WebSearch/MCP calls show query/result/status/injection evidence in logs or admin views.
  4. Fleet admin workflows support batch refresh, health check, enable/disable, filtering, and export with screenshot evidence.
**Plans**: 3 plans

Plans:
- [ ] 03-01: Add CLI credential import/validation and account onboarding diagnostics.
- [ ] 03-02: Add scheduler policy controls and admin fleet operations.
- [ ] 03-03: Improve WebSearch/MCP observability and ecosystem documentation.

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> 3

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. A - Claude Code Official Compatibility | 0/3 | Not started | - |
| 2. B - High-Availability Account Pool and sub2api Routing | 0/3 | Not started | - |
| 3. C - Kiro Ecosystem Operations | 0/3 | Not started | - |
