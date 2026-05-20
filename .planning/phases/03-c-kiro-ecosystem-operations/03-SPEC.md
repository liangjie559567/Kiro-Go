# Phase 3: C - Kiro Ecosystem Operations - Specification

**Created:** 2026-05-20
**Ambiguity score:** 0.15 (gate: <= 0.20)
**Requirements:** 7 locked

## Goal

Operators managing many Kiro accounts can import or validate supported credential sources, diagnose account readiness, preview scheduler decisions, run fleet operations, and observe WebSearch/MCP behavior from Admin/API surfaces with pass/fail evidence.

## Background

Kiro-Go already has account storage, Kiro Account Manager-style credential import/export, SSO token import, account refresh, account test calls, model-cache refresh, per-account proxy settings, global proxy settings, load-balance configuration, health/runtime fields, request logs, Claude Code readiness, model readiness, WebSearch translation through Kiro MCP, and Admin UI controls for accounts, settings, request logs, and batch operations. Relevant code exists in `config/config.go`, `pool/account.go`, `proxy/handler.go`, `proxy/request_log.go`, `proxy/account_refresh.go`, `proxy/account_health.go`, `proxy/translator.go`, and `web/index.html`.

The current implementation exposes many raw capabilities, but Phase 3 needs to turn them into operator-grade workflows: imports must validate rather than fail opaquely, onboarding must explain why an account is or is not usable, scheduler behavior must be inspectable before and after requests, fleet operations must return per-account results, and WebSearch/MCP must be observable and operable enough that failures can be diagnosed without fragile manual debugging.

## Requirements

1. **Credential source import and validation**: Admin/API can import or validate supported Kiro credential sources with rollback-safe results.
   - Current: `/admin/api/auth/credentials`, `/admin/api/auth/sso-token`, IAM SSO, Builder ID, and export flows exist; Kiro Account Manager-style JSON import is supported in the Admin UI, but Kiro CLI / Amazon Q CLI source discovery and dry-run validation are not locked as a requirement.
   - Target: Admin/API accepts Kiro Account Manager JSON and, where local readable sources are available, Kiro CLI / Amazon Q CLI credential sources; validation reports source type, accounts discovered, missing required fields, parse errors, refresh viability, and whether import would create or update accounts without partially mutating stored config on failure.
   - Acceptance: A verifier can submit valid and invalid Kiro Account Manager JSON plus simulated Kiro CLI/Amazon Q CLI credential fixtures and receive per-source/per-account `valid`, `invalid`, or `unsupported` results; a failed validation/import leaves existing accounts unchanged.

2. **Account onboarding diagnostics**: Each account has a single diagnostic view/API response that explains readiness for generation traffic.
   - Current: Account list/detail already expose auth method, token expiry, profile ARN indirectly, model cache, quota, proxy, cooldown, failure reason, runtime health, and test-account behavior, but the fields are spread across endpoints and UI fragments.
   - Target: An onboarding diagnostic response and Admin view show auth method, token expiry, refresh-token viability, profile ARN state, cached/live model-list state, quota/trial state, proxy state, cooldown/failure state, and actionable error text for each account.
   - Acceptance: Diagnostics for accounts with expired token, missing profile ARN, empty model list, quota exhaustion, proxy failure, cooldown, disabled state, and healthy state each produce a deterministic status, machine-readable reason code, and human-actionable message visible through API and Admin UI.

3. **Scheduler policy controls and preview**: Operators can configure scheduler policy and inspect which accounts would be considered for a model before sending production traffic.
   - Current: `health`, `round_robin`, and `least_connections` strategies exist in config/pool; Admin can save the strategy; request logs include routing strategy, decision, pressure, account health, and admission fields.
   - Target: Admin/API supports visible scheduler controls plus a read-only dry-run/preview API that accepts a model and reports candidate accounts, exclusion reasons, runtime health, quota/cooldown/model-list eligibility, selected strategy, and the accounts that would be preferred without consuming upstream Kiro quota.
   - Acceptance: Tests or API checks prove strategy changes persist and immediately affect pool strategy, preview returns per-account eligibility/exclusion reasons for at least healthy, disabled, cooling, quota-blocked, missing-model, and expired-token accounts, and request logs still show actual routing decisions after real requests.

4. **Fleet admin operations**: Batch account operations provide per-account results and do not break existing single-account flows.
   - Current: Admin supports selected-account enable/disable, batch refresh, batch model refresh, all-enabled model refresh, filtering by enabled/disabled/banned, account export, and single-account refresh/test/detail operations.
   - Target: Batch refresh, health check/test, enable, disable, readiness filtering, model-cache refresh, and export return per-account results with success/failure/skipped status, reason code, and summary counts; the Admin UI displays these results and keeps existing single-account actions working.
   - Acceptance: A verifier can select multiple accounts and run batch enable/disable, refresh, health check/test, readiness filter, model refresh, and export; each operation reports per-account outcomes, summary counts match the row outcomes, screenshots show the results in Admin, and single-account refresh/test/update/delete still works.

5. **WebSearch/MCP operations and observability**: WebSearch/MCP behavior is visible and operable from Admin/API, with phase-specific failure classifications.
   - Current: Kiro-Go handles native Claude WebSearch by extracting a query, calling Kiro MCP `web_search`, converting results into Anthropic-compatible content, and request readiness can detect recent MCP tools; request logs do not lock query/result/status/injection evidence or Admin-triggered diagnostics.
   - Target: Admin/API can inspect and manage WebSearch/MCP-related settings or trigger a diagnostic test call; logs expose search query, result count, upstream MCP status, injected payload size, account used, latency, and failure reason classified as query extraction failure, no account available, Kiro MCP HTTP/status error, empty results, or payload injection/conversion failure.
   - Acceptance: Simulated or real WebSearch/MCP requests record query, result count, MCP status, injected payload size, latency, and classified failure reason in request logs or a dedicated diagnostics response; Admin can trigger a test call or manage the relevant WebSearch/MCP setting without starting or stopping local MCP servers.

6. **Operational evidence and documentation**: Phase 3 produces operator-facing documentation and evidence for supported workflows.
   - Current: README files mention account import/export, proxies, Claude Code, MCP host boundaries, request logs, and Admin surfaces, but they do not describe the Phase 3 diagnostic and fleet workflows as a locked operational contract.
   - Target: Documentation explains supported credential sources, validation/import behavior, diagnostic statuses, scheduler preview semantics, fleet operation results, WebSearch/MCP observability, and what remains outside Kiro-Go's responsibility.
   - Acceptance: Documentation includes concrete Admin/API examples for credential validation, account diagnostics, scheduler preview, fleet operations, and WebSearch/MCP diagnostics, and it explicitly states that Kiro-Go does not start or manage local MCP servers.

7. **Phase 3 verification bundle**: Phase 3 cannot pass without aligned tests and Admin/API evidence.
   - Current: The project requires Go tests and frontend/admin screenshot evidence for touched UI; previous UAT artifacts use API JSON, request logs, and Playwright screenshots.
   - Target: Final verification runs unit/integration tests for touched Go packages and captures API responses plus Playwright screenshots for import/validation, onboarding diagnostics, scheduler preview, fleet operations, and WebSearch/MCP diagnostics.
   - Acceptance: The final Phase 3 report includes `go test ./... -count=1`, representative API JSON, request-log evidence, Admin screenshots, and an explicit PASS/FAIL verdict where API, logs, and screenshots agree.

## Boundaries

**In scope:**
- Kiro Account Manager JSON import/validation and supported Kiro CLI / Amazon Q CLI credential-source discovery where local sources are readable and parseable.
- Account onboarding diagnostics for auth method, token expiry, refresh viability, profile ARN, model list, quota/trial, proxy, cooldown/failure, enabled state, and actionable messages.
- Scheduler strategy controls plus a read-only model/account preview API that does not consume upstream Kiro quota.
- Fleet Admin/API operations for batch refresh, health check/test, enable/disable, readiness filtering, model-cache refresh, and export with per-account results.
- WebSearch/MCP Admin/API diagnostics or settings plus request-log observability for query, result count, MCP status, injected payload size, latency, account, and classified failures.
- Tests, documentation, API evidence, request-log evidence, and Playwright screenshot evidence for KE-01 through KE-05.

**Out of scope:**
- Starting, stopping, configuring, or hosting local MCP servers inside Kiro-Go - Claude Code remains the MCP host.
- Editing `/www/sub2api` source code - Phase 3 is about Kiro-Go ecosystem operations, not downstream gateway behavior.
- Distributed multi-replica account state or cross-process scheduler coordination - current milestone targets the existing single-process architecture.
- Turning fleet operations into a durable asynchronous queue/task-history system - per-account synchronous or bounded batch results are sufficient for this phase.
- Admin auth/session hardening, CSRF work, atomic config persistence, secret redaction overhaul, request body size limits, and race detector expansion - these are v2 operational hardening items unless directly required by a Phase 3 acceptance check.
- Desktop machine ID mutation outside Kiro-Go account settings - desktop switcher tooling remains outside this API proxy.

## Constraints

- Do not read, expose, or commit runtime secrets from `data/config.json`, recovery snapshots, API keys, refresh tokens, admin passwords, or local CLI credential files.
- Validation and import flows must support dry-run behavior or equivalent rollback safety so failed imports do not partially mutate existing accounts.
- WebSearch/MCP diagnostics may trigger explicit test calls only when the operator requests them; normal inspection must not unexpectedly consume upstream quota.
- Scheduler preview must be read-only and must not reserve accounts, increment request counters, extend cooldowns, or call Kiro upstream.
- Batch operations must use bounded concurrency or sequential execution so Admin actions do not amplify upstream limits.
- Admin UI changes must remain compatible with the existing single-file `web/index.html` approach unless planning later decides otherwise.
- PASS requires aligned API, request-log, and screenshot evidence; contradictory evidence blocks PASS.

## Acceptance Criteria

- [ ] Credential validation/import supports Kiro Account Manager JSON and supported Kiro CLI / Amazon Q CLI fixtures with per-source/per-account results.
- [ ] Failed validation/import leaves existing account config unchanged.
- [ ] Account diagnostics expose auth method, token expiry, refresh viability, profile ARN state, model-list state, quota/trial state, proxy state, cooldown/failure state, enabled state, and actionable message.
- [ ] Scheduler strategy changes persist and immediately affect the active account pool.
- [ ] Scheduler preview for a model returns candidate accounts, preferred accounts, strategy, runtime health, and per-account exclusion reasons without consuming upstream quota.
- [ ] Batch refresh, health check/test, enable/disable, readiness filtering, model-cache refresh, and export return per-account results and accurate summary counts.
- [ ] Existing single-account refresh, test, update, delete, model refresh, and export flows still work after fleet-operation changes.
- [ ] WebSearch/MCP logs or diagnostics expose query, result count, upstream MCP status, injected payload size, account used, latency, and classified failure reason.
- [ ] Admin UI shows usable evidence for import/validation, diagnostics, scheduler preview, fleet results, and WebSearch/MCP diagnostics.
- [ ] Documentation explains supported credential sources, diagnostic statuses, scheduler preview, fleet operations, WebSearch/MCP observability, and local MCP server boundaries.
- [ ] `go test ./... -count=1` passes after Phase 3 implementation.
- [ ] Final verification includes representative API JSON, request-log evidence, Playwright screenshots, and a PASS verdict only when evidence agrees.

## Ambiguity Report

| Dimension           | Score | Min   | Status | Notes |
|---------------------|-------|-------|--------|-------|
| Goal Clarity        | 0.90  | 0.75  | OK     | Goal locks operator workflows across import, diagnostics, scheduler, fleet, and WebSearch/MCP. |
| Boundary Clarity    | 0.86  | 0.70  | OK     | User chose API+Admin UI, prioritized onboarding/fleet, and allowed WebSearch/MCP operations without local MCP hosting. |
| Constraint Clarity  | 0.78  | 0.65  | OK     | Dry-run/rollback safety, no secret exposure, read-only scheduler preview, bounded batch behavior, and evidence gates are explicit. |
| Acceptance Criteria | 0.84  | 0.70  | OK     | Pass/fail checks include API, logs, screenshots, tests, and per-account outcomes. |
| **Ambiguity**       | 0.15  | <=0.20| OK     | Gate passed. |

Status: OK = met minimum, WARN = below minimum (planner treats as assumption)

## Interview Log

| Round | Perspective | Question summary | Decision locked |
|-------|-------------|------------------|-----------------|
| 1 | Researcher | What form should Phase 3 deliver? | User selected API + Admin UI as the primary delivery shape. |
| 1 | Researcher | Which credential sources and checks must onboarding cover? | Cover Kiro Account Manager JSON plus discoverable Kiro CLI / Amazon Q CLI sources, checking auth, token expiry, profile ARN, model list, quota, and proxy. |
| 2 | Researcher + Simplifier | What is the irreducible core if scope is reduced? | Prioritize onboarding and fleet workflows; WebSearch/MCP can be narrower but must remain observable/operable. |
| 2 | Researcher + Simplifier | How inspectable must scheduler policy be? | Add a dry-run/preview API explaining which accounts a model would consider and prefer. |
| 3 | Boundary Keeper | Which adjacent systems are excluded? | `/www/sub2api` and distributed multi-replica state are excluded; MCP local server hosting remains excluded even though WebSearch/MCP Admin operations are allowed. |
| 3 | Boundary Keeper | What is the fleet-operation boundary? | Batch refresh, health check, enable/disable, readiness filtering, and export need per-account results visible in Admin/API. |
| 4 | Failure Analyst | What is the final WebSearch/MCP boundary? | Admin may manage WebSearch/MCP-related settings or trigger diagnostic test calls, but Kiro-Go must not start/stop local MCP servers. |
| 4 | Failure Analyst | How should WebSearch/MCP failures be classified? | Distinguish query extraction failure, no account available, Kiro MCP HTTP/status error, empty results, and payload injection/conversion failure. |

---

*Phase: 03-c-kiro-ecosystem-operations*
*Spec created: 2026-05-20*
*Next step: $gsd-discuss-phase 3 - implementation decisions (how to build what's specified above)*
