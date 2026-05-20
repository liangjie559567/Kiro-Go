# Requirements: Kiro-Go Claude Code Compatibility and High Availability

**Defined:** 2026-05-20
**Core Value:** Claude Code through Kiro-Go should behave like the official Anthropic API while routing Kiro accounts correctly enough that one account's 429 never poisons the whole downstream path.

## v1 Requirements

### Claude Code Compatibility

- [ ] **CC-01**: Kiro-Go documents and tests a Claude Code compatibility matrix covering endpoints, auth headers, models, streaming, tool use, tool result, thinking, prompt cache behavior, large context, and count-token behavior.
- [ ] **CC-02**: Claude model aliases including dot, hyphen, versioned, Opus/Sonnet/Haiku, and `ANTHROPIC_SMALL_FAST_MODEL` forms resolve deterministically and are visible in readiness and request logs.
- [ ] **CC-03**: Claude Code tool loops preserve `tools`, `tool_choice`, `tool_use`, `tool_result`, and `tool_reference` request/response shapes without dropping tool calls or creating empty assistant turns.
- [ ] **CC-04**: Anthropic SSE output is valid for normal text, thinking/reasoning, tool use, first-token retry, upstream errors before first chunk, and upstream errors after streaming starts.
- [ ] **CC-05**: Large Claude Code context requests are protected by explicit, configurable payload policy that logs size, trimming/rejection decisions, and does not silently corrupt current user input.
- [ ] **CC-06**: Prompt cache controls and thinking settings are preserved, normalized, or rejected with clear compatibility errors and tests.
- [ ] **CC-07**: Automated UAT verifies real Claude Code-style stream, non-stream, and tool-loop calls through Kiro-Go and records API/log/screenshot evidence before PASS.

### High Availability and sub2api

- [ ] **HA-01**: Kiro temporary-limit 429 is isolated to the account that returned it unless direct evidence proves a shared upstream state.
- [ ] **HA-02**: Kiro-Go classifies upstream failures separately as `model_capacity`, `temporary_limited`, `rate_limited`, `quota`, `auth`, `network`, and `unknown`.
- [ ] **HA-03**: Account scheduler continues trying other viable accounts after per-account 429 while avoiding same-account retry amplification.
- [ ] **HA-04**: Model admission control is configurable and account-aware for Opus 4.7 and other high-pressure models.
- [ ] **HA-05**: Background auto-refresh and health-check jobs use bounded concurrency, jitter, and cooldown awareness so they do not amplify user traffic limits.
- [ ] **HA-06**: sub2api integration receives clear retryability and exhausted-pool semantics from Kiro-Go and does not turn one Kiro account 429 into repeated downstream failure.
- [ ] **HA-07**: Real 10 concurrent x 10 non-stream and 10 concurrent x 10 stream Opus 4.7 calls through `/www/sub2api` return correct content, record max latency, and leave no stale database unschedulable state.

### Kiro Ecosystem Operations

- [ ] **KE-01**: Admin can import or validate Kiro CLI / Amazon Q CLI credential sources where supported, with rollback-safe error handling.
- [ ] **KE-02**: Account onboarding diagnostics show auth method, token expiry, profile ARN state, model list state, quota state, proxy state, and actionable error text.
- [ ] **KE-03**: Operators can choose and inspect scheduler policies such as round-robin, least-recently-used, quota-aware, and latency-aware routing.
- [ ] **KE-04**: WebSearch/MCP observability shows search query, result count, upstream MCP status, injected payload size, and failure reason.
- [ ] **KE-05**: Admin fleet operations support batch refresh, batch health check, enable/disable, readiness filtering, and export without breaking existing single-account flows.

## v2 Requirements

### Operational Hardening

- **OPS-01**: Config writes use atomic temp-file, fsync, and rename behavior with corruption recovery tests.
- **OPS-02**: Admin auth moves away from raw password storage in browser localStorage toward safer session behavior and CSRF-aware admin calls.
- **OPS-03**: API keys and account secrets are redacted by default in admin APIs and logs, with explicit reveal/export gates.
- **OPS-04**: Race detector coverage is added for account pool, config persistence, token refresh, and background jobs.
- **OPS-05**: Request body size limits are enforced on public API and admin import endpoints.

## Out of Scope

| Feature | Reason |
|---------|--------|
| Kiro-Go starts or manages local MCP servers | Claude Code remains the MCP host; Kiro-Go should preserve API/tool compatibility. |
| Fake success when all upstream accounts are exhausted | This would hide real upstream state and break debugging. |
| Global risk-group lockout from one temporary-limited account | User evidence shows one account can 429 while others succeed. |
| Desktop machine ID mutation in server gateway | This belongs to desktop switcher tooling, not the API proxy core. |
| Multi-replica distributed account state | Current milestone targets the existing single-process architecture. |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| CC-01 | Phase 1 | Pending |
| CC-02 | Phase 1 | Pending |
| CC-03 | Phase 1 | Pending |
| CC-04 | Phase 1 | Pending |
| CC-05 | Phase 1 | Pending |
| CC-06 | Phase 1 | Pending |
| CC-07 | Phase 1 | Pending |
| HA-01 | Phase 2 | Pending |
| HA-02 | Phase 2 | Pending |
| HA-03 | Phase 2 | Pending |
| HA-04 | Phase 2 | Pending |
| HA-05 | Phase 2 | Pending |
| HA-06 | Phase 2 | Pending |
| HA-07 | Phase 2 | Pending |
| KE-01 | Phase 3 | Pending |
| KE-02 | Phase 3 | Pending |
| KE-03 | Phase 3 | Pending |
| KE-04 | Phase 3 | Pending |
| KE-05 | Phase 3 | Pending |

**Coverage:**
- v1 requirements: 19 total
- Mapped to phases: 19
- Unmapped: 0

## Definition of Done

- Unit and integration tests pass for touched Go packages.
- Claude Code compatibility changes include stream and non-stream wire evidence.
- sub2api changes include database/log evidence for account, group, usage, and error state.
- Frontend/admin changes include screenshot evidence and screenshot analysis.
- A phase cannot pass when screenshot/API/database evidence disagree.

---
*Requirements defined: 2026-05-20*
*Last updated: 2026-05-20 after initial definition*
