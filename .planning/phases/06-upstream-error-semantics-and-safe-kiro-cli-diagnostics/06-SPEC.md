# Phase 6: Upstream Error Semantics and Safe Kiro CLI Diagnostics - Specification

**Created:** 2026-05-21
**Ambiguity score:** 0.08 (gate: <= 0.20)
**Requirements:** 8 locked

## Goal

Kiro-Go classifies upstream failures from structured evidence and exposes safe, explicit Kiro CLI diagnostics through admin API and lightweight admin UI without reading secrets or performing unsafe account actions by default.

## Background

Kiro-Go already has string-based failure classification in `pool.ClassifyFailureReason`, per-account cooldowns, model breaker state, retry-after handling for some rate-limit errors, Opus 4.7 quiet-mode behavior for background refresh and health checks, scheduler preview, fleet readiness, and request logs. The current implementation can distinguish several important failure reasons, but the primary parser is still string-oriented and does not preserve a normalized upstream error object with HTTP status, JSON error fields, AWS-style error codes, request IDs, and retry-after variants.

Kiro-Go also has account diagnostics and credential validation surfaces, and it intentionally rejects local CLI credential discovery. The remaining gap is a safe Kiro CLI diagnostics surface: admins need to see configured CLI path, executable availability, version, command-router state, `KIRO_CLI_HOME` presence, supported read-only command output, and whether the local CLI can list `claude-opus-4.7`. This phase must not read CLI token stores, SQLite auth databases, browser sessions, keychains, API keys, or runtime secret config, and must not perform login/logout, updates, settings writes, token refresh, credential import, router mutation, or credit-consuming probes unless a later phase adds explicit admin-triggered workflows with separate review.

## Requirements

1. **Structured upstream error parser**: Kiro-Go parses upstream error evidence into a normalized error object before falling back to string matching.
   - Current: `pool.ClassifyFailureReason` classifies from `err.Error()` text, and `rateLimitError` preserves some body and reset timing, but there is no stable structured error object.
   - Target: Upstream HTTP failures expose normalized fields for HTTP status, JSON `name`, `code`, `reason`, `message`, and `type`, AWS-style error code, upstream request ID, safe endpoint/source name, `Retry-After`, `retry-after-ms`, and a redacted short summary.
   - Acceptance: Unit tests feed HTTP 429, 403, 500, JSON error bodies, AWS-style error payloads, request ID headers, `Retry-After`, and `retry-after-ms`; parser returns expected normalized fields and never exposes authorization tokens, cookies, API keys, or raw secret-bearing headers.

2. **Stable failure reason mapping**: Structured upstream errors map to distinct fixed reason codes.
   - Current: Existing reason codes include `quota_exhausted`, `auth_expired`, `suspended`, `rate_limited`, `temporary_limited`, `model_capacity`, `transient_network`, and `upstream_5xx`, but coverage depends on ad hoc text checks.
   - Target: Classification first uses normalized structured fields and then string fallback to produce stable reason codes for temporary account limit, model capacity pressure, generic rate limit, quota/monthly limit, auth expiration, suspended or banned account, network timeout, and upstream 5xx.
   - Acceptance: Table tests prove each required upstream scenario maps to exactly one expected reason code, including ambiguous 429 bodies where suspicious-account temporary limits and model capacity pressure must not collapse into generic `rate_limited`.

3. **Account-level versus model-level effects**: Failure reasons drive the correct cooldown, retry, and breaker behavior without poisoning unrelated viable accounts.
   - Current: Account cooldown and model breaker behavior exists, and model capacity avoids account cooldown in `RecordFailure`, but the structured taxonomy is not locked as the source of those decisions.
   - Target: Account-level failures affect only the selected account or its explicit persisted account state; model-level capacity pressure updates model breaker and retry behavior without marking every Opus 4.7 account as failed; network and upstream 5xx remain retryable pressure signals with bounded cooldown behavior.
   - Acceptance: Tests prove a temporary-limited account is skipped while another viable account can still be selected, model capacity opens model pressure without writing account cooldown for all accounts, auth/quota/suspended failures block only affected accounts, and no single account failure globally blocks the Opus 4.7 pool.

4. **Retry-after preservation**: Retryability and retry-after semantics survive classification, logs, headers, and failover decisions without leaking unsafe upstream headers.
   - Current: Some `Retry-After` behavior is preserved through `rateLimitError`, Claude error headers, and Opus pressure headers.
   - Target: Normalized errors preserve retryability and retry-after seconds from `Retry-After` and `retry-after-ms`; safe downstream headers and logs expose only approved Kiro-Go fields such as reason, retryability, circuit state, safe concurrency, and retry-after.
   - Acceptance: Tests verify retry-after values are respected for rate limits and temporary limits, `retry-after-ms` converts to seconds with sane rounding, request logs contain reason and retry-after, and unsafe upstream headers are not propagated by default.

5. **Background quiet-mode pressure behavior**: Foreground Opus 4.7 pressure keeps background probes and health checks bounded, jittered, and cooldown-aware.
   - Current: Auto refresh, model refresh, and health check paths already skip during Opus quiet mode in several places.
   - Target: Structured model-pressure evidence continues to trigger quiet-mode behavior for generation-style probes, model refreshes, WebSearch live probes, and health checks; quiet mode remains bounded and does not repeatedly hit cooling-down accounts.
   - Acceptance: Tests prove background refresh, all-account model refresh, health check, and any live diagnostic probe path either skips or returns a safe retryable response while Opus 4.7 quiet mode or account cooldown is active.

6. **Safe CLI diagnostics API**: Admin API exposes Kiro CLI diagnostics through a read-only allowlist.
   - Current: The Docker image installs `kiro-cli`/`kiro`, `KIRO_CLI_HOME` is configured in deployment, and credential validation explicitly rejects local CLI credential discovery.
   - Target: Admins can query configured CLI path, executable availability, version, command-router state, `KIRO_CLI_HOME` presence, and supported read-only diagnostic commands; explicit admin-triggered checks may run only allowlisted read-only commands: version, `whoami`, `doctor`, `diagnostic`, `chat --list-models`, and `settings list`, using JSON output flags when supported.
   - Acceptance: API tests cover CLI missing, CLI present, command timeout, non-zero exit, and successful JSON output; every response includes status/reason, command name, exit status, duration, redacted output, and no secret content.

7. **CLI Opus 4.7 model-list state**: Kiro-Go validates whether the local CLI account reports `claude-opus-4.7` as present, unavailable, or unknown.
   - Current: Kiro-Go caches model lists from upstream account APIs, but there is no safe local CLI model-list diagnostic contract.
   - Target: The CLI diagnostic surface reports `claude-opus-4.7` state as `present`, `unavailable`, or `unknown`, with source command, timestamp, and redacted evidence summary.
   - Acceptance: Tests with fixture outputs for present, absent, malformed, and command-failed model lists produce the expected state and do not rely on static model assumptions.

8. **Admin UI summary and audit trail**: Existing admin UI shows a lightweight safe diagnostics summary and explicit diagnostic runs are auditable.
   - Current: Admin UI exposes account/readiness/log surfaces but no dedicated safe CLI diagnostic summary.
   - Target: Existing Web admin page shows CLI path, availability, version, home presence, Opus 4.7 model-list state, and latest redacted diagnostic result; explicit diagnostic runs are recorded in logs or request-log-style audit evidence with admin action, allowlisted command, timestamp, status, and redaction status.
   - Acceptance: Browser or DOM tests verify the UI renders the diagnostic summary without exposing secret values; API/log tests verify explicit diagnostic runs write auditable metadata while raw command output remains redacted.

## Boundaries

**In scope:**
- Structured normalized upstream error parsing for Kiro upstream HTTP and transport failures.
- Stable mapping from normalized upstream errors to fixed Kiro-Go failure reason codes.
- Correct account cooldown, model breaker, retryability, and retry-after behavior based on those reason codes.
- Safe propagation of Kiro-Go-owned diagnostic headers and logs, not raw upstream secret-bearing headers.
- Background quiet-mode behavior for pressure-sensitive probes and health checks.
- Admin API for safe read-only Kiro CLI diagnostics.
- Lightweight existing admin UI summary for latest safe CLI diagnostic state.
- Redaction, timeout, output-size limit, and audit evidence for explicit diagnostic command runs.
- `claude-opus-4.7` local CLI model-list state as `present`, `unavailable`, or `unknown`.

**Out of scope:**
- Rewriting the scheduler, readiness contract, or sub2api readiness provider behavior - those belong to earlier phases or separate implementation work.
- Adding a new distributed readiness store or multi-replica coordination - the milestone targets the current single-process gateway.
- Reading, parsing, copying, or exposing CLI token stores, SQLite auth databases, browser sessions, keychains, `KIRO_API_KEY`, account recovery candidates, or runtime secret config.
- CLI login/logout, update, settings write/delete, API-key setup, router change, credential import, token refresh, or machine identity mutation - these are unsafe state-changing actions outside this phase.
- Credit-consuming generation probes, WebSearch live probes, or live model calls as default diagnostics - any such action requires explicit future design and audit gates.
- Historical trend charts, complex UI redesign, credential import flows, or raw/reveal/show-token functionality.
- Copying implementation from AGPL projects such as `jwadow/kiro-gateway`.

## Constraints

- Structured parsing must prefer typed fields and headers before string fallback.
- Reason codes must stay fixed and low-cardinality so logs, readiness, and downstream decisions can rely on them.
- Retry attempts remain bounded by the existing Opus 4.7 budgets; Phase 6 must not introduce unbounded account scanning or unbounded CLI execution.
- `Retry-After` and `retry-after-ms` must be normalized to safe seconds values with lower/upper bounds appropriate for existing cooldown behavior.
- Diagnostic CLI execution must use an allowlist, timeout, bounded output capture, redaction before storage/display, and explicit admin triggering.
- Admin UI and APIs must never expose raw tokens, cookies, authorization headers, API keys, CLI auth databases, keychains, or browser session material.
- Web UI scope is limited to summary and latest result display on existing admin surfaces.

## Acceptance Criteria

- [ ] Structured upstream parser returns normalized status, JSON error fields, AWS-style code, request ID, retry-after values, source, and redacted summary for fixture responses.
- [ ] Temporary account limit, model capacity, generic rate limit, quota/monthly limit, auth expiration, suspended/banned account, network timeout, and upstream 5xx map to distinct stable reason codes.
- [ ] Account-level failures cool down or block only affected accounts, while model-level pressure affects model breaker/retry behavior without globally poisoning viable accounts.
- [ ] Retry-after semantics are preserved in request logs and safe Kiro-Go headers; unsafe raw upstream headers are not propagated by default.
- [ ] Background refresh, health check, model refresh, and live diagnostic probes respect Opus 4.7 quiet mode and account cooldowns.
- [ ] Admin API reports CLI path, executable availability, version, command-router state, `KIRO_CLI_HOME` presence, allowlisted command status, and redacted output.
- [ ] CLI model-list diagnostics report `claude-opus-4.7` as `present`, `unavailable`, or `unknown` from command evidence rather than static assumptions.
- [ ] Explicit diagnostic runs produce audit metadata and never store or display unredacted secret content.
- [ ] Existing admin UI renders a lightweight safe CLI diagnostic summary and latest redacted result.

## Ambiguity Report

| Dimension           | Score | Min   | Status | Notes |
|---------------------|-------|-------|--------|-------|
| Goal Clarity        | 0.93  | 0.75  | met    | Phase locks structural upstream errors and safe CLI diagnostics only. |
| Boundary Clarity    | 0.89  | 0.70  | met    | Scheduler/readiness rewrites, sub2api work, unsafe CLI actions, and secret reading are excluded. |
| Constraint Clarity  | 0.86  | 0.65  | met    | Allowlist, redaction, retry-after normalization, bounded execution, and no-secret rules are explicit. |
| Acceptance Criteria | 0.84  | 0.70  | met    | Requirements have pass/fail parser, cooldown, API, UI, and audit checks. |
| **Ambiguity**       | 0.08  | <=0.20| met    | Gate passed after round 2. |

Status: met = meets minimum, below = planner treats as assumption.

## Interview Log

| Round | Perspective | Question summary | Decision locked |
|-------|-------------|------------------|-----------------|
| 1 | Researcher | What should the CLI diagnostic delivery surface be? | Deliver admin API plus lightweight visibility in the existing Web admin UI. |
| 1 | Researcher | May diagnostics execute Kiro CLI commands? | Yes, but only explicit admin-triggered read-only allowlisted commands with timeout, bounded output, redaction, and audit. |
| 1 | Researcher | What is the minimum structured upstream error evidence? | HTTP status, JSON error fields, AWS-style code, request ID, `Retry-After`, and `retry-after-ms` are required. |
| 2 | Researcher + Simplifier | What is the minimum upstream error deliverable? | Parser, classification, cooldown/breaker/retry-after propagation, and tests; no scheduler or readiness rewrite. |
| 2 | Researcher + Simplifier | What is the minimum CLI diagnostics deliverable? | Path, executable/version, `KIRO_CLI_HOME`, allowlisted commands, Opus 4.7 model-list state, redaction, and audit. |
| 2 | Researcher + Simplifier | How much Web UI is required? | Summary and latest result only; no complex interactions, historical trends, or credential import. |

---

*Phase: 06-upstream-error-semantics-and-safe-kiro-cli-diagnostics*
*Spec created: 2026-05-21*
*Next step: $gsd-discuss-phase 6 - implementation decisions for how to build the locked requirements above*
