# Requirements: Kiro-Go Opus 4.7 Sustainable Health

**Defined:** 2026-05-21
**Core Value:** sub2api should be able to call Opus 4.7 through Kiro-Go continuously whenever at least one real Kiro account remains viable, and Kiro-Go must report accurate degraded/blocked state when upstream capacity or account pool health makes success impossible.

## Milestone v1.1 Requirements

### Opus 4.7 Readiness Contract

- [x] **RDY-01**: sub2api and admins can query a versioned Kiro-Go Opus 4.7 readiness contract that returns `healthy`, `degraded`, or `blocked`, plus `safeConcurrency`, schedulable account counts, cooldown counts, `retryAfterSeconds`, `recommendedAction`, and reason codes.
- [x] **RDY-02**: Kiro-Go readiness distinguishes transport success, stable fallback, and real Opus 4.7 content success so fallback HTTP 200 responses never count as healthy model success.
- [x] **RDY-03**: Kiro-Go learns recent `account + claude-opus-4.7` live success and uses that evidence in scheduler preview, fleet readiness, and account routing priority.
- [x] **RDY-04**: Scheduler preview and fleet readiness explain the same account/model eligibility state, including per-account cooldown, model breaker state, token/session state, and Opus 4.7 model-list visibility.
- [x] **RDY-05**: Opus 4.7 requests have bounded attempt budgets for account tries, total wait time, and first-token retries, and budget exhaustion returns retryable pressure metadata rather than unbounded pool scanning.
- [x] **RDY-06**: Streaming retries are allowed only before any SSE content reaches the downstream client; once streaming has started, Kiro-Go records the failure and terminates through protocol-safe error behavior without transparent replay.

### sub2api Readiness Integration

- [x] **S2A-01**: sub2api can configure a Kiro-Go readiness provider for OpenAI-compatible Kiro-Go accounts or channels, including endpoint, timeout, status-specific TTLs, fail-open/fail-closed mode, and model match rules.
- [x] **S2A-02**: sub2api queries Kiro-Go readiness before dispatching Opus 4.7 traffic, using the effective upstream model after channel/account/compact model mapping rather than only the client-requested model.
- [x] **S2A-03**: sub2api skips or temporarily unschedules a Kiro-Go candidate when readiness is `blocked`, without writing permanent account errors or confusing model-level pressure with account-level failure.
- [x] **S2A-04**: sub2api lowers priority or caps concurrency for Kiro-Go candidates when readiness is `degraded`, while still allowing traffic within Kiro-Go `safeConcurrency`.
- [x] **S2A-05**: sub2api readiness logic covers previous-response sticky, session sticky, load-balance candidate selection, and fallback-wait paths before consuming account concurrency slots.
- [x] **S2A-06**: sub2api logs readiness status, cache hit, TTL, retry-after, safe concurrency, requested model, effective upstream model, and Kiro-Go account/channel identifiers in usage or ops logs with secrets redacted.

### Upstream Error Semantics

- [ ] **ERR-01**: Kiro-Go parses structured upstream errors before string fallback, including HTTP status, JSON name/code/reason, AWS-style error codes, request IDs, and `Retry-After` or retry-after-ms values.
- [ ] **ERR-02**: Kiro-Go classifies temporary account limit, model capacity pressure, rate limit, quota/monthly limit, auth expiration, suspended/banned account, network timeout, and upstream 5xx as distinct failure reasons.
- [ ] **ERR-03**: Kiro-Go applies different cooldown, retry, and circuit-breaker behavior for account-level failures versus model-level capacity pressure so one account failure never poisons the whole Opus 4.7 pool.
- [ ] **ERR-04**: Foreground Opus 4.7 pressure causes background generation-style probes and health checks to enter quiet mode with bounded concurrency, jitter, and cooldown awareness.
- [ ] **ERR-05**: Kiro-Go and sub2api preserve retryability and retry-after semantics through response headers, internal logs, and failover decisions without leaking unsafe upstream headers by default.

### Safe Kiro CLI Diagnostics

- [ ] **CLI-01**: Admins can view safe Kiro CLI diagnostics showing configured CLI path, executable availability, version, command-router state, `KIRO_CLI_HOME` presence, and supported official diagnostic commands without exposing secret content.
- [ ] **CLI-02**: Kiro-Go can run explicit read-only CLI checks through official commands such as `whoami`, `doctor`, `diagnostic`, `chat --list-models`, and `settings list`, with output redacted before storage or display.
- [ ] **CLI-03**: Kiro-Go can validate whether the local CLI account lists `claude-opus-4.7` and reports unavailable, unknown, or present model state without relying on static model assumptions.
- [ ] **CLI-04**: Any credit-consuming generation probe, WebSearch live probe, login/logout, update, settings write, API-key setup, router change, credential import, or token refresh action requires explicit admin triggering and audit logging.
- [ ] **CLI-05**: Kiro-Go does not read, parse, copy, or expose CLI token stores, SQLite auth databases, browser sessions, keychains, `KIRO_API_KEY`, recovery candidates, or runtime config secrets by default.

### Observability and Evidence

- [ ] **OBS-01**: Kiro-Go request logs include readiness state at admission, safe concurrency, retry-after, selected account, effective model, attempt trace, pressure reason, stable fallback flag, and content-success evidence.
- [ ] **OBS-02**: Kiro-Go exposes diagnostic headers or admin API fields for readiness state, retryability, circuit state, safe concurrency, and content success, while sub2api can explicitly choose which safe headers to propagate.
- [ ] **OBS-03**: sub2api ops logs can distinguish readiness-blocked scheduling decisions from upstream 429/529 responses, account rate limits, overloads, and temp-unschedulable rules.
- [ ] **OBS-04**: Admin UI or API evidence can show Opus 4.7 pool state, degraded/blocked reasons, CLI model-list status, and the latest real content-success timestamp without exposing credentials.

### UAT and Acceptance

- [ ] **UAT-01**: Latest-code sub2api non-stream Opus 4.7 UAT proves 100/100 real content successes only when Kiro-Go readiness reports viable capacity; transport-only 200 or stable fallback does not pass.
- [ ] **UAT-02**: Latest-code sub2api stream Opus 4.7 UAT proves 100/100 real content successes only when Kiro-Go readiness reports viable capacity; started streams are never transparently replayed after downstream content begins.
- [ ] **UAT-03**: Blocked-capacity UAT proves the correct outcome is explicit `blocked` or retryable exhausted-pool behavior with `Retry-After`, not fake success, when no viable Opus 4.7 account exists.
- [ ] **UAT-04**: UAT evidence captures aligned Kiro-Go request logs/API, fleet readiness, sub2api usage or ops logs, database scheduling state, response headers, and admin screenshots before PASS.

## Previous Milestone Baseline

These capabilities were implemented or validated before v1.1 and are treated as baseline dependencies, not new scope:

- Claude Code Anthropic compatibility for messages, streaming, tool loops, thinking, prompt cache handling, model aliases, large contexts, and count-token behavior.
- Account-aware cooldown, model admission, retry semantics, request logging, fleet readiness, scheduler preview, and admin fleet operations in Kiro-Go.
- sub2api model mapping, usage logs, temporary unschedulable state, 429/529 handling, failover, channel monitoring, and response header filtering.
- Historical 2026-05-20 sub2api 10x10 stream and non-stream Opus 4.7 PASS evidence, which remains regression context but is not sufficient for final v1.1 PASS.

## Future Requirements

### Operational Hardening

- **OPS-01**: Config writes use atomic temp-file, fsync, and rename behavior with corruption recovery tests.
- **OPS-02**: Admin auth moves away from raw password storage in browser localStorage toward safer session behavior and CSRF-aware admin calls.
- **OPS-03**: API keys and account secrets are redacted by default in admin APIs and logs, with explicit reveal/export gates.
- **OPS-04**: Race detector coverage is added for account pool, config persistence, token refresh, and background jobs.
- **OPS-05**: Request body size limits are enforced on public API and admin import endpoints.

### Post-v1.1 Kiro Ecosystem

- **KE-06**: Kiro-Go can support explicit, consented CLI credential import after a separate security review and rollback design.
- **KE-07**: Kiro-Go can show quota estimates from user-provided data or a documented official API if one becomes available.
- **KE-08**: Multi-replica account state coordination can be introduced if deployment moves beyond the current single-process JSON-backed gateway.

## Out of Scope

| Feature | Reason |
|---------|--------|
| Guaranteeing that Kiro upstream Opus 4.7 never returns capacity or account errors | Kiro-Go cannot control upstream Kiro availability; it can route while viable accounts exist and report accurate degraded/blocked/retryable state when they do not. |
| Counting stable fallback or transport-level HTTP 200 as real Opus 4.7 success | This hides upstream exhaustion and breaks downstream health decisions. |
| Treating one temporary-limited account as a global Opus 4.7 lockout without cross-account evidence | Existing evidence shows one account can fail while others still succeed. |
| Transparent replay after a streaming response has emitted content | This risks duplicated content, duplicated billing, and corrupted client state. |
| Copying source, tests, constants, or implementation structure from `jwadow/kiro-gateway` | The project is AGPL-3.0; Kiro-Go may borrow design ideas but not copy implementation. |
| Reading CLI token stores, SQLite auth databases, browser sessions, keychains, or runtime secret config by default | Secret material must remain out of diagnostics, logs, and planning artifacts. |
| Automatic token refresh, CLI database writes, machine ID mutation, or account switch rollback | These are brittle, high-risk, and outside the server gateway contract for v1.1. |
| Installing or managing Kiro CLI itself | The deployment already includes Kiro CLI; v1.1 only diagnoses and safely probes the existing CLI. |
| Real-time quota scraping from private Kiro endpoints | No stable public CLI quota API was identified; use explicit unknown state or operator-provided evidence. |
| Multi-replica distributed readiness state | Current milestone targets the existing single-process gateway architecture. |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| RDY-01 | Phase 4 | Complete |
| RDY-02 | Phase 4 | Complete |
| RDY-03 | Phase 4 | Complete |
| RDY-04 | Phase 4 | Complete |
| RDY-05 | Phase 4 | Complete |
| RDY-06 | Phase 4 | Complete |
| S2A-01 | Phase 5 | Complete |
| S2A-02 | Phase 5 | Complete |
| S2A-03 | Phase 5 | Complete |
| S2A-04 | Phase 5 | Complete |
| S2A-05 | Phase 5 | Complete |
| S2A-06 | Phase 5 | Complete |
| ERR-01 | Phase 6 | Pending |
| ERR-02 | Phase 6 | Pending |
| ERR-03 | Phase 6 | Pending |
| ERR-04 | Phase 6 | Pending |
| ERR-05 | Phase 6 | Pending |
| CLI-01 | Phase 6 | Pending |
| CLI-02 | Phase 6 | Pending |
| CLI-03 | Phase 6 | Pending |
| CLI-04 | Phase 6 | Pending |
| CLI-05 | Phase 6 | Pending |
| OBS-01 | Phase 7 | Pending |
| OBS-02 | Phase 7 | Pending |
| OBS-03 | Phase 7 | Pending |
| OBS-04 | Phase 7 | Pending |
| UAT-01 | Phase 7 | Pending |
| UAT-02 | Phase 7 | Pending |
| UAT-03 | Phase 7 | Pending |
| UAT-04 | Phase 7 | Pending |

**Coverage:**
- v1.1 requirements: 30 total
- Mapped to phases: 30
- Unmapped: 0

## Definition of Done

- Unit and integration tests pass for touched Go packages in Kiro-Go and touched packages in sub2api.
- Kiro-Go and sub2api changes include stream and non-stream wire evidence for Opus 4.7.
- PASS requires real content-success evidence, not only HTTP status or fallback content.
- sub2api evidence includes scheduling decisions, usage/ops logs, and database state for readiness-blocked, degraded, and healthy paths.
- Frontend/admin changes include screenshot evidence and screenshot analysis where UI is changed.
- A phase cannot pass when screenshot/API/log/database evidence disagree.

---
*Requirements defined: 2026-05-21*
*Last updated: 2026-05-21 after v1.1 research synthesis*
