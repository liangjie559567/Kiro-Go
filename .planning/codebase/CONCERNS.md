# Codebase Concerns

**Analysis Date:** 2026-05-21

## Tech Debt

**Monolithic proxy handler:**
- Issue: `proxy/handler.go` is the central HTTP router, admin API, request translator coordinator, retry loop owner, model cache manager, token refresh coordinator, background scheduler, and compatibility/readiness API implementation.
- Files: `proxy/handler.go`, `proxy/translator.go`, `proxy/kiro.go`, `proxy/request_log.go`
- Impact: Local changes have a large blast radius. Adding endpoints, compatibility behavior, or routing policy in `proxy/handler.go` requires understanding unrelated state such as account health, model admission, stable fallback, request logs, and OpenAI Responses sessions.
- Fix approach: Move coherent groups out of `proxy/handler.go`: admin account APIs, client generation endpoints, readiness/compatibility endpoints, token refresh, and background jobs. Keep `Handler` as composition and routing glue.

**Global mutable runtime gates:**
- Issue: Admission and continuity controls are package-level globals, including `opus47AdmissionGate`, `modelAdmissionGate`, and `contentContinuityGateGlobal`.
- Files: `proxy/handler.go`, `proxy/opus_gate.go`, `proxy/content_continuity.go`
- Impact: Tests and handlers share mutable process-wide state. Reconfiguration in one request or test can affect other concurrent requests, other `Handler` instances, and later tests.
- Fix approach: Store admission gates and content-continuity gates on `Handler`; update them through handler methods under one lock. Avoid direct package global mutation from config update paths.

**Compatibility modes are emulated or estimated in important places:**
- Issue: Anthropic count-tokens uses local estimation, `max_tokens=0` is locally shaped, assistant prefill is emulated, and fine-grained tool streaming is a gateway reconstruction rather than proven upstream parity.
- Files: `proxy/handler.go`, `proxy/token_estimator.go`, `proxy/claude_sse_writer.go`, `docs/claude-code-compatibility-matrix.md`
- Impact: Claude Code compatibility can pass local tests while still diverging from official Anthropic behavior on billing, cache warmup, stream granularity, and assistant prefill semantics.
- Fix approach: Keep compatibility/readiness outputs explicit about `estimated`, `local_zero_output`, `emulated_text_prefill`, and `kiro_go_chunked_complete_input`. Add upstream-backed evidence before marking official parity as pass.

**Runtime config is the persistence layer:**
- Issue: Credentials, account health, stats, model mappings, and admin/client access settings all persist through a single JSON config file.
- Files: `config/config.go`, `data/config.json`, `.gitignore`
- Impact: Every settings or runtime stats write rewrites the same file. This couples secrets, mutable health state, counters, and operator config, increasing write contention and recovery risk.
- Fix approach: Split credential storage, runtime health, request stats, and operator settings. Use atomic file replacement or a small embedded database for high-churn runtime state.

## Known Bugs

**Race in concurrent token refresh:**
- Symptoms: `go test -race ./...` reports data races between token refresh writers and readers.
- Files: `proxy/handler.go`, `pool/account.go`, `proxy/handler_test.go`
- Trigger: `TestEnsureValidTokenCoalescesConcurrentRefreshesPerAccount` runs concurrent calls to `ensureValidToken`; `pool.AccountPool.UpdateToken` writes account fields while `Handler.syncLatestAccountToken` copies fields into another account pointer.
- Workaround: Normal `go test ./...` passes, but race-enabled validation fails. Do not treat token refresh concurrency as race-clean until this is fixed.

**Race in model admission pressure state:**
- Symptoms: `go test -race ./...` reports a race between queue-timeout pressure writes and routing/readiness reads.
- Files: `proxy/opus_gate.go`, `proxy/handler.go`, `proxy/handler_test.go`
- Trigger: `TestHandleClaudeStreamOpus47CapacityLimitNeverReturnsEmptyBodyUnderConcurrency` concurrently records queue timeout pressure and reads `hasPressure` for routing decision logging.
- Workaround: Normal tests pass, but production concurrency can observe inconsistent pressure state. Keep admission pressure updates and reads behind the same lock.

**Request bodies are read without server-side size limits on core endpoints:**
- Symptoms: Large client bodies are fully read or decoded before payload guard limits run.
- Files: `proxy/handler.go`
- Trigger: `handleClaudeMessagesInternal` and `handleOpenAIChat` call `io.ReadAll(r.Body)`; `handleOpenAIResponses` decodes directly from `r.Body`; admin JSON endpoints decode bodies without `http.MaxBytesReader`.
- Workaround: Put a reverse proxy/body-size limit in front of the service. In code, wrap all JSON endpoints with `http.MaxBytesReader` before reading or decoding.

## Security Considerations

**Insecure-by-default public binding and auth settings:**
- Risk: First-run defaults bind the service to all interfaces, use admin password `changeme`, and do not require client API keys.
- Files: `config/config.go`, `main.go`, `docker-compose.yml`, `README.md`
- Current mitigation: `README.md` tells operators to set `ADMIN_PASSWORD`; `config.Save` writes the JSON config with `0600` permissions.
- Recommendations: Require an explicit admin password on first startup when `Host` is not loopback. Default `RequireApiKey` to true for non-local binds and make `docker-compose.yml` set `ADMIN_PASSWORD` through deployment secrets.

**Admin password is stored in browser localStorage and sent on every request:**
- Risk: Any script execution in the admin origin can read the admin password, and every admin API call carries it in `X-Admin-Password`.
- Files: `web/index.html`, `proxy/handler.go`
- Current mitigation: The browser clears localStorage after 72 hours and the backend accepts the password through a header or cookie.
- Recommendations: Replace localStorage password persistence with an HttpOnly, Secure, SameSite session cookie. Add CSRF protection for state-changing admin endpoints.

**Credentials are persisted and exported in plaintext:**
- Risk: Access tokens, refresh tokens, client IDs, and client secrets are stored in config JSON and exported from the admin API.
- Files: `config/config.go`, `proxy/handler.go`, `data/config.json`
- Current mitigation: `config.Save` uses `0600` file permissions; list endpoints hide token values.
- Recommendations: Encrypt credentials at rest or support OS/key-management backed secret storage. Make full credential export require a separate confirmation or one-time export token, and audit export events.

**Wildcard CORS is applied globally:**
- Risk: `Access-Control-Allow-Origin: *` and broad allowed headers are sent for API and admin routes.
- Files: `proxy/handler.go`
- Current mitigation: Admin APIs require `X-Admin-Password`; client APIs can require API keys.
- Recommendations: Scope CORS to configured origins, especially for `/admin/api/*`. Avoid wildcard CORS when admin credentials are accepted from browser-controlled storage.

**Settings APIs expose API keys back to the admin client:**
- Risk: Admin settings and Claude Code compatibility endpoints return client API keys or environment snippets containing keys.
- Files: `proxy/handler.go`
- Current mitigation: Endpoints are admin-password protected.
- Recommendations: Mask API keys in normal GET responses and provide explicit rotate/copy flows for sensitive values.

## Performance Bottlenecks

**High-churn config writes:**
- Problem: Runtime stats, account stats, health state, token updates, and settings all call `Save`, rewriting the full config JSON.
- Files: `config/config.go`, `proxy/handler.go`, `pool/account.go`
- Cause: Config is used for both stable settings and frequently changing runtime state.
- Improvement path: Batch or debounce runtime stats writes, separate runtime state from secret/config state, and use atomic writes to a dedicated state file or database.

**Request log ring buffer uses slice shifting:**
- Problem: Once the in-memory request log reaches capacity, every append shifts the full slice.
- Files: `proxy/request_log.go`
- Cause: `requestLogStore.Add` copies `entries[1:]` into `entries[0:]` at capacity.
- Improvement path: Replace with a circular buffer. Preserve `List(limit)` newest-first behavior so admin APIs stay compatible.

**Model refresh can run synchronously on `/models`:**
- Problem: A cold `/v1/models` or `/models` request can trigger `refreshModelsCache` before returning.
- Files: `proxy/handler.go`
- Cause: `handleModels` refreshes cache inline when `cachedModels` is empty.
- Improvement path: Serve fallback models immediately and refresh asynchronously, or bound the refresh with a short context deadline.

**Stable continuity waits can hold requests for up to minutes:**
- Problem: Opus 4.7 continuity defaults allow waits up to 120 seconds and queue depth up to 300.
- Files: `config/config.go`, `proxy/content_continuity.go`, `proxy/handler.go`
- Cause: Defaults prioritize preserving downstream success during upstream capacity pressure.
- Improvement path: Keep these values configurable per deployment and expose queue depth/age metrics. Use lower defaults for interactive clients unless a downstream explicitly opts into long waits.

## Fragile Areas

**Token refresh and account pool synchronization:**
- Files: `proxy/handler.go`, `pool/account.go`, `config/config.go`
- Why fragile: Token state exists in request-local account copies, the account pool, and persisted config. Race testing already catches unsynchronized field access around refresh.
- Safe modification: Treat account structs as immutable snapshots outside pool locks. Return copied token snapshots from pool methods and update all refresh paths under a single synchronization contract.
- Test coverage: Unit tests cover coalescing behavior, but `go test -race ./...` fails.

**Admission pressure and Opus 4.7 routing:**
- Files: `proxy/opus_gate.go`, `proxy/handler.go`, `proxy/account_health.go`, `pool/account.go`
- Why fragile: Routing depends on model capacity classification, temporary account limits, circuit pressure score, queue timeouts, stable fallback, and request logging. Race testing catches unsynchronized pressure state.
- Safe modification: Keep account-health failures, model-capacity pressure, and temporary-limit cooldowns separated. Add race tests around any new admission fields.
- Test coverage: Many unit tests cover behavior, but race-enabled test suite fails.

**Payload guard and Claude Code tool compatibility:**
- Files: `proxy/payload_guard.go`, `proxy/translator.go`, `proxy/claude_sse_writer.go`, `proxy/request_log.go`
- Why fragile: The gateway trims tools, relocates descriptions, compacts tool-result history, suppresses invalid tool use, and emits synthetic Anthropic stream events. Small changes can break client tool loops while still returning HTTP 200.
- Safe modification: Update request-log metadata and compatibility matrix entries whenever payload transformations change. Add fixture tests for Claude Code wire requests.
- Test coverage: Extensive tests exist in `proxy/payload_guard_test.go`, `proxy/translator_test.go`, and `proxy/claude_sse_writer_test.go`; upstream parity remains partially unproven.

**OpenAI Responses session reconstruction:**
- Files: `proxy/handler.go`
- Why fragile: Previous response state is kept in an in-memory map capped at 128 sessions with a one-hour TTL. Restarts or cache pruning lose chain context.
- Safe modification: Keep response session semantics best-effort unless persistent session storage is added. Avoid assuming `previous_response_id` is durable across process restarts.
- Test coverage: Unit tests cover restore and pruning behavior, but no persistence or multi-instance coverage exists.

## Scaling Limits

**Single-process in-memory state:**
- Current capacity: Request logs are capped at 5,000 entries, OpenAI Responses sessions at 128, and model/request caches live in one process.
- Limit: Multiple instances do not share request logs, previous response sessions, model cache, admission state, or in-flight token refresh coalescing.
- Scaling path: Externalize runtime coordination to shared storage or keep deployments single-instance behind a process supervisor.

**Default long queues can amplify upstream pressure:**
- Current capacity: Content continuity defaults allow 300 queued waits for supported models; model admission defaults are driven by config.
- Limit: Under upstream temporary limits, long waits can consume server goroutines and client connections.
- Scaling path: Add explicit queue metrics, enforce per-client limits, and make stable fallback/continuity opt-in per downstream API key.

## Dependencies at Risk

**Very small dependency surface with old Go target:**
- Risk: `go.mod` targets Go 1.21 while local validation ran under Go 1.22; Docker builds from `golang:1.21-alpine`.
- Impact: Race behavior and standard-library HTTP/runtime behavior should be validated against the production builder version, not just the local toolchain.
- Migration plan: Pin and test one Go version in CI and Docker. Upgrade `go.mod` and Docker builder together when adopting a newer runtime.

## Missing Critical Features

**No first-run setup gate:**
- Problem: The app can start with `changeme`, public bind, and optional client API keys.
- Blocks: Safe internet-facing deployments without an external reverse proxy and secret-management wrapper.

**No durable audit trail for sensitive admin actions:**
- Problem: Credential import, full account export, settings changes, request-log clearing, and account deletes are not written to a durable audit log.
- Blocks: Operational forensics after accidental credential export or malicious admin access.

**No built-in secret encryption or rotation workflow:**
- Problem: Refresh tokens and client secrets are plain JSON fields and rotation is manual through admin APIs.
- Blocks: Meeting stricter secret-handling requirements for shared deployments.

## Test Coverage Gaps

**Race-clean concurrency coverage:**
- What's not tested: The suite is not race-clean for token refresh and model admission pressure paths.
- Files: `proxy/handler.go`, `pool/account.go`, `proxy/opus_gate.go`
- Risk: Data races can produce inconsistent token state, stale routing decisions, or corrupted pressure counters under load.
- Priority: High

**Body-size and abuse tests:**
- What's not tested: Oversized request bodies for generation and admin endpoints.
- Files: `proxy/handler.go`
- Risk: A client can force memory growth before payload guards reject or trim.
- Priority: High

**Browser/admin security tests:**
- What's not tested: Admin session storage behavior, CSRF behavior, CORS scoping, and masking of sensitive settings.
- Files: `web/index.html`, `proxy/handler.go`
- Risk: Admin credentials and API keys are easier to exfiltrate if the admin origin is compromised.
- Priority: Medium

**Live upstream parity evidence:**
- What's not tested: Official upstream parity for count-tokens, max-token-zero cache warmup, assistant prefill, and fine-grained partial tool streaming.
- Files: `docs/claude-code-compatibility-matrix.md`, `proxy/handler.go`, `proxy/claude_sse_writer.go`
- Risk: Local compatibility can drift from official Anthropic behavior.
- Priority: Medium

---

*Concerns audit: 2026-05-21*
