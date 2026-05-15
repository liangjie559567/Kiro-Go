# Codebase Concerns

**Analysis Date:** 2026-05-15

## Tech Debt

**Monolithic proxy handler:**
- Issue: `proxy/handler.go` contains routing, request validation, admin APIs, account operations, model cache management, streaming conversion, retry behavior, stats, and static file serving in one 4,223-line file.
- Files: `proxy/handler.go`, `proxy/translator.go`, `web/index.html`
- Impact: Small changes to one workflow create broad regression risk across unrelated API, admin, streaming, and model-routing behavior. Reviewers must reason about shared globals such as `opus47AdmissionGate`, `ensureValidTokenForHealthCheck`, `sleepForOpusCapacityRetry`, and handler state in `proxy/handler.go`.
- Fix approach: Split by responsibility using existing package boundaries: move admin endpoints to `proxy/admin_*.go`, OpenAI handlers to `proxy/openai_handler.go`, Claude handlers to `proxy/claude_handler.go`, model cache code to `proxy/model_cache.go`, and shared retry/account helpers to focused files. Keep `Handler` as the HTTP composition point in `proxy/handler.go`.

**Single-file admin frontend:**
- Issue: `web/index.html` combines all HTML, CSS, JavaScript, translations, admin state, account rendering, credential import, proxy settings, prompt filter settings, and polling in one 3,435-line file.
- Files: `web/index.html`
- Impact: UI changes are hard to review and easy to break because DOM rendering, translation keys, fetch calls, and state mutation are tightly coupled. String-built UI also increases escaping mistakes.
- Fix approach: Keep the no-build static deployment model if desired, but separate concerns into committed static files: `web/index.html`, `web/styles.css`, `web/i18n.js`, `web/api.js`, and focused modules such as `web/accounts.js`, `web/settings.js`, and `web/auth.js`. Serve them through the existing `/admin/` static path in `proxy/handler.go`.

**JSON file as transactional data store:**
- Issue: Runtime configuration, admin password, API key, account tokens, token refreshes, usage counters, health state, and prompt filters share one JSON file.
- Files: `config/config.go`, `data/config.json`, `main.go`
- Impact: Frequent request/stat updates call `Save()` and rewrite the full config file. A crash during `os.WriteFile` can leave a truncated config. Runtime counters and secrets have the same persistence and backup lifecycle.
- Fix approach: Replace `Save()` in `config/config.go` with atomic write-rename behavior and split volatile counters from credential/config data. Use a temp file in the same directory, `fsync`, then `rename`; add a corruption recovery test in `config/config_test.go`.

**Silent no-op updates for missing records:**
- Issue: Update/delete helpers return `nil` when an account ID is not found.
- Files: `config/config.go`
- Impact: Callers cannot distinguish successful updates from missing records. Admin and background workflows can report success while no account changed.
- Fix approach: Return an explicit `ErrAccountNotFound` from `UpdateAccount`, `DeleteAccount`, `UpdateAccountToken`, `UpdateAccountStats`, `UpdateAccountInfo`, `UpdateAccountProfileArn`, and `UpdateAccountHealth`; map that to 404 in admin handlers in `proxy/handler.go`.

## Known Bugs

**Settings saves clear unrelated settings:**
- Symptoms: `saveOverUsageConfig()` posts only `allowOverUsage`, and `changePassword()` posts only `password`; `apiUpdateSettings()` always calls `config.UpdateSettings(req.ApiKey, req.RequireApiKey, req.Password)`, so omitted `apiKey` becomes empty and omitted `requireApiKey` becomes false.
- Files: `web/index.html`, `proxy/handler.go`, `config/config.go`
- Trigger: Save over-usage settings or change the admin password from the Settings tab after enabling API-key enforcement.
- Workaround: Re-save API key settings after changing over-usage or password.

**Admin status reads unprotected handler fields:**
- Symptoms: `/admin/api/status` reads `h.totalRequests`, `h.successRequests`, `h.failedRequests`, `h.totalTokens`, and `h.totalCredits` directly instead of using atomics and `getCredits()`.
- Files: `proxy/handler.go`
- Trigger: Admin polling during concurrent request handling.
- Workaround: Use `/v1/stats` or `/admin/api/stats`, which read counters through atomics and the credits lock.

**Pool can deadlock through lock inversion:**
- Symptoms: `pool.AccountPool.RecordSuccess`, `clearExpiredCooldownsLocked`, `RecordFailure`, and `RecordFailureUntil` hold `p.mu` and call config persistence helpers. `config.Load()` holds `cfgLock` and can call `pool.Reload()` through normal handler startup/reload flows. The code has both `pool -> config` and `config -> pool` call paths.
- Files: `pool/account.go`, `config/config.go`, `proxy/handler.go`
- Trigger: Concurrent account routing failures/successes while admin updates or reloads account configuration.
- Workaround: Avoid high-concurrency admin account edits during heavy traffic.

**Global singleton pool leaks across tests and config reinitialization:**
- Symptoms: `pool.GetPool()` is guarded by `sync.Once` and keeps the same `AccountPool` instance across `config.Init()` calls. Tests reinitialize config in many packages but share pool singleton state unless they explicitly reload or construct a local pool.
- Files: `pool/account.go`, `proxy/handler_test.go`, `pool/account_test.go`, `config/config_test.go`
- Trigger: New tests that depend on pristine global pool state or run in parallel.
- Workaround: Avoid `t.Parallel()` for tests touching `config.Init()` or `pool.GetPool()`; prefer local `&pool.AccountPool{}` where possible.

## Security Considerations

**Default externally bound admin service with default password:**
- Risk: A new config binds to `0.0.0.0`, uses admin password `changeme`, and disables API-key enforcement by default.
- Files: `config/config.go`, `main.go`, `docker-compose.yml`, `README.md`
- Current mitigation: `ADMIN_PASSWORD` can override the admin password; config files are written with mode `0600`.
- Recommendations: Bind to `127.0.0.1` by default outside Docker, require `ADMIN_PASSWORD` or first-run password initialization when host is `0.0.0.0`, and warn/refuse startup when the password remains `changeme`.

**Permissive CORS across all routes:**
- Risk: `ServeHTTP` sets `Access-Control-Allow-Origin: *` and broad methods/headers for admin and API routes. Combined with password-in-localStorage admin auth, a malicious page can make cross-origin requests if it can obtain or influence the password/header context.
- Files: `proxy/handler.go`, `web/index.html`
- Current mitigation: Admin APIs require `X-Admin-Password` or `admin_password` cookie; browser clients cannot read localStorage across origins.
- Recommendations: Scope CORS to API routes only, make allowed origins configurable, and avoid enabling credential-bearing admin calls from arbitrary origins.

**Admin password stored in browser localStorage:**
- Risk: The admin password is persisted in `localStorage` for 72 hours and sent as `X-Admin-Password` on every admin request. Any script injection in the admin page can read it.
- Files: `web/index.html`, `proxy/handler.go`
- Current mitigation: Admin page responses include `Cache-Control: no-store`; account emails are masked by default in UI privacy mode.
- Recommendations: Replace localStorage password storage with a server-issued, HttpOnly, SameSite session cookie and CSRF token. Avoid accepting raw password cookies in `handleAdminAPI`.

**Credential-bearing admin endpoints return secrets:**
- Risk: `/admin/api/accounts/{id}/full` returns `accessToken`, `refreshToken`, and `clientSecret`; `/admin/api/export` returns account credentials for selected or all accounts.
- Files: `proxy/handler.go`, `web/index.html`
- Current mitigation: Endpoints require the admin password and list APIs hide token values.
- Recommendations: Keep export behind a distinct confirmation and short-lived session re-authentication. Redact secrets by default in account detail and require an explicit reveal action with audit logging.

**API key exposed back to admin UI:**
- Risk: `/admin/api/settings` returns the configured API key, and the UI writes it into a text input.
- Files: `proxy/handler.go`, `web/index.html`, `config/config.go`
- Current mitigation: Settings endpoint requires the admin password.
- Recommendations: Store a hash of the API key and return only a presence/fingerprint indicator. Generate one-time display values only when rotating keys.

**Debug logs can contain prompt and tool payload data:**
- Risk: `CallKiroAPI` logs the full upstream request payload at debug level. Payloads include user prompts, tool schemas, conversation history, and potentially sensitive prompt content.
- Files: `proxy/kiro.go`, `logger/logger.go`, `config/config.go`
- Current mitigation: Default log level is `info`.
- Recommendations: Redact or summarize payload logs, and keep full payload dumps behind a separate explicit unsafe diagnostic flag.

**Unescaped admin UI rendering:**
- Risk: Many fields from accounts, upstream model responses, prompt rules, and error messages are inserted with `innerHTML` string concatenation. The file has an `escapeHtml()` helper, but account list and prompt-rule rendering do not consistently use it.
- Files: `web/index.html`
- Current mitigation: Most displayed values originate from admin-controlled configuration or upstream service metadata.
- Recommendations: Use DOM APIs or centralized escaping for every dynamic value. Treat account email, nickname, provider, proxy URL, ban reason, model ID, prompt rule fields, and upstream errors as untrusted.

**Committed runtime data directories:**
- Risk: `data/config.json` exists in the working tree and `recovery/` contains many recovered config snapshots. These paths are likely to contain OAuth tokens, refresh tokens, client secrets, account emails, and API keys.
- Files: `data/config.json`, `recovery/`, `.gitignore`
- Current mitigation: `data/` and `recovery/` are untracked in the current worktree; `Save()` writes config as `0600`.
- Recommendations: Add `data/`, `recovery/`, screenshots with operational data, and generated config snapshots to `.gitignore`. Keep recovery artifacts outside the repository or encrypt them.

## Performance Bottlenecks

**Full-config write amplification:**
- Problem: Stats, account health, token refreshes, account info refreshes, prompt settings, and admin settings all rewrite the full JSON config file.
- Files: `config/config.go`, `proxy/handler.go`, `pool/account.go`, `proxy/account_refresh.go`
- Cause: `Save()` serializes the entire `Config` object and writes it on each mutation.
- Improvement path: Debounce non-critical stats writes, persist counters separately, batch background account updates, and introduce atomic file writes or a small embedded database for account state.

**Model cache refresh is synchronous and serial:**
- Problem: `refreshModelsCache()` walks enabled accounts one by one and calls token refresh/model list APIs before `/v1/models` can use fresh data.
- Files: `proxy/handler.go`, `proxy/kiro_api.go`
- Cause: The cache refresh runs inline and each account can hit 30-second REST timeouts.
- Improvement path: Refresh models in a bounded worker pool, return stale cache while refresh is in progress, and expose per-account refresh status.

**Weighted pool duplicates account structs:**
- Problem: `AccountPool.Reload()` expands account weight by appending full `config.Account` structs multiple times.
- Files: `pool/account.go`, `config/config.go`
- Cause: Weighted routing is implemented by duplicating account values rather than storing account references or schedule entries.
- Improvement path: Store compact schedule entries with account IDs and weight metadata; keep a single account record per ID.

**Proxy client cache has no eviction:**
- Problem: Per-account and global proxy URLs create cached `http.Client` instances in `sync.Map` without deletion.
- Files: `proxy/kiro.go`, `auth/http_client.go`
- Cause: `proxyClientCache` and `authProxyClientCache` are append-only by proxy URL.
- Improvement path: Clear cache entries on proxy updates, bound cache size, or key clients by account ID and replace them when account proxy settings change.

## Fragile Areas

**Streaming protocol conversion:**
- Files: `proxy/handler.go`, `proxy/kiro.go`, `proxy/translator.go`, `proxy/handler_test.go`, `proxy/kiro_test.go`, `proxy/translator_test.go`
- Why fragile: Streaming state tracks text buffers, reasoning-source precedence, thinking tags, OpenAI chunk formats, tool-call chunks, token estimates, retries, and explicit error chunks. Small ordering changes can produce empty streams or malformed chunks.
- Safe modification: Add focused tests for each streaming shape before editing: normal text, reasoning events, `<thinking>` tags, omitted thinking, tool calls, upstream error before first chunk, upstream error after first chunk, and high-concurrency Opus 4.7 behavior.
- Test coverage: Strong coverage exists for several Opus 4.7, thinking, and translator cases, but no browser/EventSource client test validates full wire-format streaming through `ServeHTTP`.

**Account health and routing state:**
- Files: `pool/account.go`, `proxy/account_health.go`, `proxy/account_refresh.go`, `proxy/handler.go`, `config/config.go`
- Why fragile: State is split across in-memory pool maps, duplicated weighted account structs, persisted account fields, async refresh goroutines, and admin-triggered updates.
- Safe modification: Keep routing decisions in `pool/account.go`; keep persistence in `config/config.go`; avoid calling config writes while holding pool locks. Add race tests for success/failure recording plus admin reloads.
- Test coverage: Unit tests cover cooldown selection, failure classification, auto refresh, health check, and admin config endpoints. Race detector execution is not configured in the repo.

**External Kiro/AWS compatibility surface:**
- Files: `proxy/kiro.go`, `proxy/kiro_headers.go`, `proxy/kiro_api.go`, `auth/iam_sso.go`, `auth/builderid.go`, `auth/oidc.go`, `auth/sso_token.go`
- Why fragile: Endpoint URLs, headers, event stream event names, OIDC payload shapes, and model list formats are hard-coded against external services.
- Safe modification: Isolate request/response fixtures in tests for each upstream endpoint and add compatibility tests before changing headers, auth payloads, or event names.
- Test coverage: Header format, rate-limit parsing, profile ARN resolution, and auth HTTP clients have tests; real upstream contract coverage is documented manually in `docs/superpowers/uat/`.

**Prompt filtering with user-provided regex:**
- Files: `config/config.go`, `proxy/translator.go`, `web/index.html`
- Why fragile: Admin-defined regex rules alter system prompts before upstream requests. Invalid or expensive regex patterns can break requests or create high CPU cost if applied to large prompts.
- Safe modification: Validate regex rules at save time in `config.UpdatePromptFilterConfig`, cap pattern length and rule count, and fail closed with a clear admin validation error.
- Test coverage: Prompt filter persistence and regex validation coverage is not visible in the current test set.

## Scaling Limits

**Single process, single config file:**
- Current capacity: One process owns one `data/config.json` file and in-memory singleton pool.
- Limit: Multiple replicas or processes writing the same config file can lose updates or corrupt state. Request counters and cooldown state are local to one process.
- Scaling path: Use an external state store or a leader-owned persistence service for accounts, cooldowns, stats, and token refresh locks.

**Global token refresh lock:**
- Current capacity: `Handler.ensureValidToken()` serializes all token refreshes through one `tokenRefreshMu`.
- Limit: Many expiring accounts refresh one at a time, delaying requests even when accounts are independent.
- Scaling path: Use per-account locks keyed by account ID.

**Opus 4.7 gate is global and hard-coded:**
- Current capacity: `opus47AdmissionGate` is initialized with fixed concurrency `2` and queue size `200`.
- Limit: The limit does not adapt to account count, deployment size, observed upstream capacity, or configured model availability.
- Scaling path: Make model admission limits configurable and account-aware, and expose queue depth/status in admin stats.

## Dependencies at Risk

**Go 1.21 baseline:**
- Risk: The Docker builder and `go.mod` use Go 1.21.
- Impact: The project misses newer standard-library fixes, tooling defaults, and runtime improvements.
- Migration plan: Validate with a newer Go toolchain, update `go.mod`, `Dockerfile`, and CI/test documentation together.

**Unpinned runtime image:**
- Risk: `Dockerfile` uses `alpine:latest`.
- Impact: Builds can change behavior as Alpine latest moves, and production images are not reproducible.
- Migration plan: Pin Alpine to a specific supported version and refresh deliberately.

**External protocol dependency without generated clients:**
- Risk: Kiro/AWS request contracts are handwritten in `proxy/kiro.go`, `proxy/kiro_headers.go`, `proxy/kiro_api.go`, and `auth/*.go`.
- Impact: Upstream schema or header changes can silently break runtime behavior.
- Migration plan: Keep high-signal fixtures from real responses, add contract tests for parsing and header generation, and centralize version/header constants.

## Missing Critical Features

**No CSRF/session model for admin:**
- Problem: Admin auth is a raw shared password sent as a custom header or cookie.
- Blocks: Safer browser admin exposure, least-privilege admin operations, and auditability.

**No automated CI configuration detected:**
- Problem: Tests pass locally with `go test ./...`, but no CI workflow file is visible in the scanned tree.
- Blocks: Automatic regression detection for PRs, dependency updates, and race/security checks.

**No backup/restore workflow for config:**
- Problem: Config corruption protection and structured backups are not implemented around `data/config.json`.
- Blocks: Safe operation with many accounts and frequent background writes.

**No request body size limits:**
- Problem: Handlers decode request bodies directly with `json.NewDecoder(r.Body)` and admin credential import accepts batch input without an explicit size cap.
- Blocks: Predictable memory usage under malformed or abusive requests.

## Test Coverage Gaps

**Security behavior:**
- What's not tested: Default password refusal/warning, CORS policy, admin session handling, API key redaction, account secret export guardrails, and XSS escaping in admin-rendered fields.
- Files: `proxy/handler.go`, `web/index.html`, `config/config.go`
- Risk: Security regressions can ship while `go test ./...` passes.
- Priority: High

**Race detector coverage:**
- What's not tested: Concurrent admin updates, token refresh, pool reloads, request stats, cooldown persistence, and background refresh scheduling under `go test -race`.
- Files: `pool/account.go`, `proxy/handler.go`, `config/config.go`, `proxy/account_refresh.go`, `proxy/account_health.go`
- Risk: Data races or deadlocks appear only under production concurrency.
- Priority: High

**Config durability:**
- What's not tested: Partial writes, invalid JSON recovery, permission failures, concurrent saves, and missing-account update errors.
- Files: `config/config.go`, `config/config_test.go`
- Risk: A crash or disk issue can lose all account credentials.
- Priority: High

**Admin frontend behavior:**
- What's not tested: Browser login/session behavior, settings forms preserving unrelated values, account rendering escaping, export/reveal flows, proxy URL editing, prompt filter editing, and mobile layout.
- Files: `web/index.html`, `proxy/handler_test.go`
- Risk: UI regressions and client-side security bugs are missed by backend-only tests.
- Priority: Medium

**Real upstream compatibility:**
- What's not tested: Automated live or recorded contract validation for Kiro streaming events, model list responses, OIDC flows, Builder ID polling, and SSO token imports.
- Files: `proxy/kiro.go`, `proxy/kiro_api.go`, `auth/iam_sso.go`, `auth/builderid.go`, `auth/oidc.go`, `auth/sso_token.go`
- Risk: Upstream changes can break auth or generation paths without local test failures.
- Priority: Medium

---

*Concerns audit: 2026-05-15*
