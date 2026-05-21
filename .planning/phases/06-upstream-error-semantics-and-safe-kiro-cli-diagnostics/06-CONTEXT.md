# Phase 6: Upstream Error Semantics and Safe Kiro CLI Diagnostics - Context

**Gathered:** 2026-05-21
**Status:** Ready for planning
**Mode:** Autonomous, --auto

<domain>
## Phase Boundary

Kiro-Go must classify upstream failures from structured evidence before falling back to string matching, and expose safe Kiro CLI diagnostics through existing admin surfaces. This phase is not a scheduler rewrite and must not read runtime secrets, CLI token stores, browser sessions, keychains, recovery snapshots, or `data/config.json`.

</domain>

<decisions>
## Implementation Decisions

### Structured upstream errors

Introduce a normalized upstream error object in the proxy layer and make `pool.ClassifyFailureReason` prefer typed structured evidence when present. Keep existing string matching as fallback for old call sites and tests.

### Account versus model effects

Preserve the existing separation: `model_capacity` opens model breaker state without poisoning account health, while account-level reasons such as `temporary_limited`, `quota_exhausted`, `auth_expired`, and `suspended` affect only the selected account or explicit cooldown state.

### Retry-after semantics

Normalize `Retry-After` and `retry-after-ms` into safe seconds/reset times. Propagate only Kiro-Go-owned safe headers and log fields. Do not forward raw upstream headers.

### CLI diagnostics surface

Add a safe admin diagnostics surface with GET summary and explicit POST allowlisted command runs. The command allowlist is read-only: `version`, `whoami`, `doctor`, `diagnostic`, `chat --list-models`, and `settings list`. Execution must use timeout, bounded output, redaction, and audit metadata.

### UI scope

Use the existing single-file admin UI. Add a lightweight card under settings/diagnostics style surfaces showing path, availability, version, CLI home presence, Opus 4.7 model-list state, and latest redacted command result. Avoid complex historical views or credential flows.

</decisions>

<code_context>
## Existing Code Insights

- `proxy/kiro.go` constructs upstream HTTP errors. Today 429 uses `rateLimitError` with `endpoint`, `body`, and `resetAt`; non-429 errors are mostly formatted strings.
- `pool/account.go` owns `FailureReason` and `ClassifyFailureReason`. It currently classifies by string content.
- `proxy/handler.go` bridges classification into account cooldown, model breaker, Opus admission pressure, retry behavior, downstream status/type, and `Retry-After` headers.
- `pool/account.go` already keeps `model_capacity` from mutating account health in `RecordFailure`.
- `proxy/request_log.go` already has request log attempts with `Reason` and `RetryAfterSeconds`, plus a safe admin request log API.
- `proxy/ecosystem_ops.go` contains existing admin diagnostics/readiness surfaces and intentionally avoids local CLI credential discovery.
- `web/index.html` is the existing admin UI. Dynamic values should continue to use `escapeHtml`.
- Docker/Compose define `kiro-cli`, `kiro`, and `KIRO_CLI_HOME=/app/data/kiro-cli`, but the host may not have the CLI installed.

</code_context>

<specifics>
## Specific Ideas

- Add `proxy/upstream_error.go` for `UpstreamError`, parsing helpers, redaction, retry-after normalization, and reason mapping helpers.
- Extend or replace `rateLimitError` creation so upstream HTTP failures carry normalized status, JSON fields, AWS-style fields, request IDs, source, redacted summary, and retry-after.
- Add table tests for structured parser and failure reason mapping, including ambiguous 429 bodies.
- Add request log attempt retry-after assertions for structured errors.
- Add `proxy/kiro_cli_diagnostics.go` with command runner abstraction, allowlist validation, output bounding, redaction, model-list state detection, and latest-result state.
- Add admin routes under `/admin/api/kiro-cli/diagnostics` for GET and POST.
- Add focused API tests with fake runner; do not run real CLI in tests.
- Update `web/index.html` narrowly, preserving existing user changes and using safe fields only.

</specifics>

<deferred>
## Deferred Ideas

Persistent CLI diagnostic history, credential import from CLI stores, login/logout, updates, settings mutation, token refresh, live generation probes, WebSearch live probes, and distributed readiness state are deferred.

</deferred>
