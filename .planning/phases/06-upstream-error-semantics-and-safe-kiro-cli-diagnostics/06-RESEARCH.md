# Phase 6: Upstream Error Semantics and Safe Kiro CLI Diagnostics - Research

**Date:** 2026-05-21
**Mode:** Autonomous codebase research

## Current Error Flow

`proxy/kiro.go` reads upstream non-200 bodies with a 1 MiB cap. HTTP 429 currently becomes `rateLimitError`, which exposes `RateLimitResetAt()` to account cooldowns and `Retry-After` response headers. Other upstream statuses are formatted as strings such as `HTTP 503 from Kiro IDE: ...`.

`pool.ClassifyFailureReason` in `pool/account.go` is the central classifier. It returns fixed low-cardinality reasons already aligned with Phase 6: `quota_exhausted`, `auth_expired`, `suspended`, `rate_limited`, `temporary_limited`, `model_capacity`, `transient_network`, and `upstream_5xx`.

`proxy/handler.go` consumes those reasons in:

- `recordAccountFailure`
- `recordAccountModelFailure`
- `claudeUpstreamErrorStatusAndType`
- `openAIUpstreamErrorStatusAndType`
- `claudeErrorHeadersForUpstreamError`
- Opus admission pressure calls
- attempt trace logging

## Existing Safety Properties

`pool.RecordFailure` already avoids account health mutation for `model_capacity`. `RecordModelFailure` separately opens per-account/per-model breaker state. Account-level failures remain selected-account scoped.

Admin diagnostics in `proxy/ecosystem_ops.go` return masked emails and readiness checks. Local CLI credential discovery is intentionally unsupported. This is consistent with Phase 6: diagnostics may report environment and command results but must not parse token stores or auth databases.

## Gaps

- No normalized upstream error type exists for HTTP status, JSON fields, AWS-style fields, request IDs, source, retry-after variants, or safe summary.
- `retry-after-ms` is not explicitly normalized.
- String classifier order is doing too much semantic work.
- Request logs record attempt reason/retry-after but do not preserve a normalized upstream summary.
- No CLI diagnostics API exists.
- No CLI diagnostics UI card exists.

## Implementation Strategy

Use typed structured errors where upstream HTTP responses enter the proxy and leave fallback string behavior intact. This is lower risk than rewriting all handlers because existing call sites can continue to call `classifyFailureReason(err)`, `rateLimitResetFromError(err)`, and status/type helpers.

CLI diagnostics should be implemented as an isolated service with a fakeable runner. Tests should never invoke a real CLI or read runtime state. GET can inspect configured path and environment presence; explicit POST can run only allowlisted read-only commands through the fakeable runner.

## Test Strategy

- Parser tests: HTTP status, JSON `error` wrapper, flat JSON fields, AWS `__type`/`Type`/`Code`, request IDs, `Retry-After`, `retry-after-ms`, redaction.
- Classification tests: temporary account limit, model capacity, generic rate limit, quota, auth, suspended/banned, network timeout, upstream 5xx.
- Behavior tests: model capacity does not write account cooldown, temporary limit skips only affected account, retry-after survives attempt trace and safe headers.
- CLI API tests: missing CLI, present CLI, timeout, non-zero exit, JSON/text output, redaction, allowlist rejection, Opus 4.7 present/unavailable/unknown.
- UI check: static DOM/script assertions for safe diagnostics labels and escaped rendering, plus API tests for exact JSON shape.

## Risks

- Opus 4.7 pressure has a special downstream contract: pressure responses often map to 503 even when upstream evidence is 429. Preserve existing pressure-specific behavior.
- `temporary_limited` has a public retry-after floor. Preserve this floor while still keeping normalized reset evidence internally.
- Existing unstaged user edits include `web/index.html`, `proxy/kiro.go`, and `proxy/kiro_test.go`. Any edits there must be narrow and reviewed carefully before staging.
