<!-- refreshed: 2026-05-21 -->
# Architecture

**Analysis Date:** 2026-05-21

## System Overview

```text
┌─────────────────────────────────────────────────────────────┐
│                    HTTP Proxy Process                       │
│                     `main.go`                               │
├──────────────────┬──────────────────┬───────────────────────┤
│  Client APIs     │  Admin UI/API    │ Background Jobs       │
│ `proxy/handler.go`│ `web/index.html` │ `proxy/handler.go`    │
└────────┬─────────┴────────┬─────────┴──────────┬────────────┘
         │                  │                    │
         ▼                  ▼                    ▼
┌─────────────────────────────────────────────────────────────┐
│          Translation, Routing, Admission, Observability      │
│ `proxy/translator.go`, `proxy/request_log.go`,               │
│ `proxy/opus_gate.go`, `proxy/claude_code_concurrency_governor.go` │
└────────┬──────────────────┬────────────────────┬────────────┘
         │                  │                    │
         ▼                  ▼                    ▼
┌─────────────────┐ ┌─────────────────┐ ┌─────────────────────┐
│ Account Pool    │ │ Persistent Config│ │ Auth Clients        │
│ `pool/account.go`│ │ `config/config.go`│ │ `auth/*.go`         │
└────────┬────────┘ └────────┬────────┘ └─────────┬───────────┘
         │                   │                    │
         ▼                   ▼                    ▼
┌─────────────────────────────────────────────────────────────┐
│ Kiro / CodeWhisperer / Amazon Q upstream APIs                │
│ `proxy/kiro.go`, `proxy/kiro_api.go`, `proxy/kiro_headers.go` │
└─────────────────────────────────────────────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| Process bootstrap | Loads config, initializes logger and account pool, creates the handler, starts HTTP server with graceful shutdown | `main.go` |
| HTTP handler | Owns request routing, CORS, client auth, generation endpoints, admin API, background refresh, health checks, stats persistence | `proxy/handler.go` |
| Request translation | Converts Anthropic Claude and OpenAI payloads to Kiro payloads, then converts Kiro outputs back to Claude/OpenAI response shapes | `proxy/translator.go` |
| Kiro streaming client | Sends generation payloads to ordered Kiro/CodeWhisperer/Amazon Q streaming endpoints with fallback and Kiro-compatible headers | `proxy/kiro.go`, `proxy/kiro_headers.go` |
| Kiro REST client | Reads usage limits, user info, available models, profiles, and readiness metadata | `proxy/kiro_api.go` |
| Account pool | Maintains enabled account routing state, weighted selection, cooldowns, model breakers, runtime health, and load-balance strategy | `pool/account.go`, `pool/breaker.go` |
| Persistent config | Stores accounts, credentials, client access, feature toggles, admission settings, proxy settings, prompt filters, and stats in JSON | `config/config.go` |
| Authentication | Refreshes OIDC/social tokens and supports IAM SSO, Builder ID, and SSO token imports | `auth/oidc.go`, `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go` |
| Admin frontend | Single-file HTML/CSS/JS admin panel served by the Go handler | `web/index.html` |
| Observability | In-memory request logs, request classification, compatibility/readiness status, and leveled logging | `proxy/request_log.go`, `proxy/request_classifier.go`, `logger/logger.go` |

## Pattern Overview

**Overall:** Modular monolith reverse proxy with package-level subsystems and in-process state.

**Key Characteristics:**
- Use one `http.Handler` implementation in `proxy/handler.go` as the application controller.
- Keep protocol adaptation in `proxy/translator.go`; do not put Claude/OpenAI/Kiro shape conversion in `main.go`, `pool/`, or `auth/`.
- Use package-level singletons for persistent configuration (`config/config.go`) and account routing (`pool/account.go`).
- Keep upstream Kiro wire behavior in `proxy/kiro.go`, `proxy/kiro_api.go`, and `proxy/kiro_headers.go`.
- Add tests beside the package they exercise, using `*_test.go` files such as `proxy/handler_test.go` and `pool/account_test.go`.

## Layers

**Bootstrap Layer:**
- Purpose: Process startup, config path resolution, environment overrides, server lifecycle.
- Location: `main.go`
- Contains: `main`, `newHTTPServer`, `runHTTPServerWithGracefulShutdown`.
- Depends on: `config`, `logger`, `pool`, `proxy`, Go standard library.
- Used by: The compiled `kiro-go` process.

**HTTP/API Layer:**
- Purpose: Route inbound HTTP requests, enforce client access, return API-compatible errors, serve admin UI.
- Location: `proxy/handler.go`
- Contains: `Handler`, `NewHandler`, `ServeHTTP`, `handleClaudeMessages`, `handleOpenAIChat`, `handleOpenAIResponses`, `handleAdminAPI`.
- Depends on: `config`, `pool`, `auth`, `logger`, translator and Kiro client helpers in `proxy/`.
- Used by: `main.go` through `proxy.NewHandler()`.

**Translation Layer:**
- Purpose: Map public API contracts to Kiro conversation state and map Kiro streaming/non-streaming results back to public contracts.
- Location: `proxy/translator.go`, `proxy/anthropic_envelope.go`, `proxy/payload_guard.go`, `proxy/token_estimator.go`
- Contains: `ClaudeRequest`, `OpenAIRequest`, `KiroPayload`, `ClaudeToKiro`, `OpenAIToKiro`, `KiroToClaudeResponse`, `KiroToOpenAIResponse`, request envelope parsing, payload trimming, token estimates.
- Depends on: `config`, standard JSON/string/time packages, UUID generation.
- Used by: `proxy/handler.go` generation paths.

**Upstream Client Layer:**
- Purpose: Execute outbound calls to Kiro streaming and REST APIs with endpoint fallback, per-account proxy handling, and Kiro header construction.
- Location: `proxy/kiro.go`, `proxy/kiro_api.go`, `proxy/kiro_headers.go`
- Contains: `CallKiroAPIWithContext`, `ListAvailableModels`, `GetUsageLimits`, `GetUserInfo`, `ResolveProfileArn`.
- Depends on: `config.Account`, `auth.RefreshToken`, `logger`, HTTP client stores.
- Used by: `proxy/handler.go`, background jobs, account diagnostics.

**Account Routing Layer:**
- Purpose: Select accounts, track runtime health, apply cooldowns, model circuit breakers, sticky sessions, and load-balancing strategies.
- Location: `pool/account.go`, `pool/breaker.go`
- Contains: `AccountPool`, `GetPool`, `Reload`, `GetNextForModelExcept`, `ClassifyFailureReason`, model breaker state.
- Depends on: `config` for persisted accounts and load-balance settings.
- Used by: `proxy.Handler` request retry loops and admin diagnostics.

**Configuration Layer:**
- Purpose: Normalize, validate, persist, and expose application configuration and account records.
- Location: `config/config.go`
- Contains: `Config`, `Account`, `Init`, getters/setters, normalization and validation functions.
- Depends on: filesystem JSON storage and `sync.RWMutex`.
- Used by: all runtime packages except `logger`.

**Authentication Layer:**
- Purpose: Refresh access tokens and import/complete login flows.
- Location: `auth/oidc.go`, `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go`, `auth/http_client.go`
- Contains: `RefreshToken`, IAM SSO flow helpers, Builder ID polling, SSO token parsing, proxy-aware auth HTTP clients.
- Depends on: `config` for proxy URL and account credentials.
- Used by: `proxy/handler.go`, `proxy/kiro_api.go`.

## Data Flow

### Primary Claude Messages Request Path

1. Process starts, loads `data/config.json` or `CONFIG_PATH`, initializes config/logger/pool, creates `proxy.Handler` (`main.go:34`).
2. `proxy.NewHandler()` applies proxy/admission settings, restores persisted stats, creates request log/cache/governor state, and starts background goroutines (`proxy/handler.go:1048`).
3. `Handler.ServeHTTP` routes `/v1/messages`, `/messages`, and `/anthropic/v1/messages` after API key/IP checks (`proxy/handler.go:1358`).
4. `handleClaudeMessagesInternal` reads the body, parses Anthropic envelope metadata, records request classification, normalizes Opus 4.7 requests, validates request shape and tool names (`proxy/handler.go:1870`).
5. `ClaudeToKiro` builds Kiro conversation state, history, current user message, tool schemas, images, tool results, and Kiro metadata (`proxy/translator.go:258`).
6. `handleClaudeWithAccountRetry` selects accounts through `pool.AccountPool`, applies admission/governor controls, refreshes tokens if needed, and attempts upstream calls (`proxy/handler.go:2404`).
7. `CallKiroAPIWithContext` orders Kiro/CodeWhisperer/Amazon Q endpoints, finalizes profile ARN, builds Kiro headers, sends the request, and retries/falls back on endpoint failures (`proxy/kiro.go:1412`).
8. Kiro output is converted back into Claude response blocks by `KiroToClaudeResponse` or streamed through `claudeSSEWriter` (`proxy/translator.go:1771`, `proxy/claude_sse_writer.go`).
9. Request logs, counters, account success/failure, and persisted stats are updated by `proxy/request_log.go`, `proxy/handler.go`, and `pool/account.go`.

### OpenAI Chat/Responses Path

1. `Handler.ServeHTTP` routes `/v1/chat/completions`, `/chat/completions`, `/v1/responses`, and `/responses` to OpenAI handlers (`proxy/handler.go:1396`, `proxy/handler.go:1402`).
2. OpenAI request bodies are parsed and classified in `handleOpenAIChat` / `handleOpenAIResponses` (`proxy/handler.go:3839`, `proxy/handler.go:3885`).
3. `OpenAIToKiro` maps OpenAI system/user/assistant/tool messages into Kiro payload shape (`proxy/translator.go:2104`).
4. The handler uses the same account retry and Kiro upstream path as Claude requests (`proxy/handler.go:2585`, `proxy/handler.go:3935`).
5. Responses are returned via `KiroToOpenAIResponse` or OpenAI-compatible streaming writers (`proxy/translator.go:2656`).

### Admin and Operations Path

1. `Handler.ServeHTTP` serves `/admin` from `web/index.html` and routes `/admin/api/*` to `handleAdminAPI` (`proxy/handler.go:1418`, `proxy/handler.go:1420`).
2. `handleAdminAPI` authenticates with `X-Admin-Password` or `admin_password` cookie, then dispatches accounts, auth, settings, status, readiness, request logs, and export routes (`proxy/handler.go:5188`).
3. Account and setting mutations call `config` update functions, then reload `pool.AccountPool` when routing inputs change (`config/config.go`, `pool/account.go`).
4. Diagnostics and readiness endpoints combine persisted account data, runtime pool state, model lists, request logs, and Kiro REST probes (`proxy/ecosystem_ops.go`, `proxy/request_log.go`, `proxy/kiro_api.go`).

### Background Refresh and Health Flow

1. `NewHandler` starts `backgroundRefresh`, `backgroundHealthCheck`, and `backgroundStatsSaver` goroutines (`proxy/handler.go:1071`).
2. `backgroundRefresh` periodically refreshes account data and model cache according to `config.AutoRefreshConfig` (`proxy/handler.go:1098`).
3. `backgroundHealthCheck` probes token validity and available models, optionally disabling unhealthy accounts (`proxy/handler.go:1209`).
4. `backgroundStatsSaver` persists process counters into config storage every 30 seconds (`proxy/handler.go:3537`).

**State Management:**
- Persistent state is a JSON config file controlled by `config.Init` and `cfgLock` in `config/config.go`.
- Runtime account selection state is in the singleton `pool.AccountPool` guarded by `sync.RWMutex` in `pool/account.go`.
- Per-process request logs, prompt cache, admission gates, response sessions, and counters live on `proxy.Handler` in `proxy/handler.go`.
- HTTP client stores for Kiro and auth are atomic pointers with proxy-specific caches in `proxy/kiro.go` and `auth/http_client.go`.

## Key Abstractions

**`proxy.Handler`:**
- Purpose: Application controller and in-memory runtime holder.
- Examples: `proxy/handler.go`
- Pattern: Single `http.Handler` with method-based route handlers and background goroutines.

**`config.Config` and `config.Account`:**
- Purpose: Persisted application settings and account credentials/usage data.
- Examples: `config/config.go`
- Pattern: Package-level config singleton with validation, normalization, and JSON persistence.

**`pool.AccountPool`:**
- Purpose: Runtime routing model over persisted accounts.
- Examples: `pool/account.go`, `pool/breaker.go`
- Pattern: Singleton account scheduler with weighted routing, cooldowns, breakers, and health scores.

**Protocol DTOs:**
- Purpose: Typed request/response shapes for Claude, OpenAI, and Kiro.
- Examples: `proxy/translator.go`, `proxy/kiro.go`
- Pattern: Struct-based DTOs plus explicit conversion functions.

**Admission Controls:**
- Purpose: Protect capacity-sensitive models and Claude Code sessions from overload.
- Examples: `proxy/opus_gate.go`, `proxy/claude_code_concurrency_governor.go`, `proxy/content_continuity.go`
- Pattern: In-memory gates with queue limits, per-model pressure, and release callbacks.

**Request Logs:**
- Purpose: Capture routing, compatibility, payload, usage, latency, and error metadata for admin APIs.
- Examples: `proxy/request_log.go`
- Pattern: Bounded in-memory ring-like store on `proxy.Handler`.

## Entry Points

**Process Entrypoint:**
- Location: `main.go`
- Triggers: Running the compiled binary or `go run .`.
- Responsibilities: Resolve config path, create data dir, initialize packages, start server, handle shutdown.

**Public API Entrypoint:**
- Location: `proxy/handler.go`
- Triggers: `ServeHTTP` requests for `/v1/messages`, `/v1/chat/completions`, `/v1/responses`, `/v1/models`, `/v1/stats`, `/health`.
- Responsibilities: Authentication, routing, protocol-specific parsing, upstream orchestration, response formatting.

**Admin Entrypoint:**
- Location: `web/index.html`, `proxy/handler.go`
- Triggers: Browser visits `/admin`; JS calls `/admin/api/*`.
- Responsibilities: Account management, login/import flows, settings, readiness, request logs, diagnostics.

**Background Entrypoints:**
- Location: `proxy/handler.go`
- Triggers: Goroutines started by `NewHandler`.
- Responsibilities: Auto refresh, health checks, model cache refresh, stats saving.

**Container Entrypoint:**
- Location: `Dockerfile`, `docker-compose.yml`
- Triggers: Docker image/container startup.
- Responsibilities: Build and run the Go binary with data volume/config environment.

## Architectural Constraints

- **Threading:** The service uses Go goroutines. `main.go` starts the HTTP server in one goroutine and `proxy.NewHandler()` starts background refresh, health check, and stats saver goroutines.
- **Global state:** `config/config.go` uses package-level `cfg`, `cfgLock`, and `cfgPath`; `pool/account.go` uses package-level `pool` and `poolOnce`; `proxy/handler.go` uses package-level admission gates and test-swappable function variables; `proxy/kiro.go` and `auth/http_client.go` use atomic global HTTP client stores.
- **Circular imports:** Package direction is one-way in source: `main` imports `config`, `logger`, `pool`, `proxy`; `proxy` imports `auth`, `config`, `logger`, `pool`; `pool` imports `config`; `auth` imports `config`; `config` imports no local package.
- **Storage:** Runtime configuration and credentials are persisted under `data/config.json` by default. Do not read or log secret values from `data/`.
- **Frontend packaging:** The admin UI is a single static file at `web/index.html`; backend serves it directly instead of using a separate frontend build pipeline.
- **Route table:** Public and admin routes are switch statements in `proxy/handler.go`; route order matters for overlapping admin account paths.

## Anti-Patterns

### Translation Logic Outside `proxy/translator.go`

**What happens:** Protocol-specific request/response mapping is added inside route handlers or upstream client functions.
**Why it's wrong:** It mixes public API semantics with routing/retry concerns and makes compatibility tests harder to target.
**Do this instead:** Add Claude/OpenAI/Kiro DTO and conversion behavior in `proxy/translator.go`, with handler orchestration in `proxy/handler.go`.

### Direct Config Mutation Without Reload

**What happens:** Account or load-balance settings are changed without reloading `pool.AccountPool`.
**Why it's wrong:** The pool keeps weighted accounts, cooldowns, model lists, and strategy in runtime state separate from persisted config.
**Do this instead:** Use config update helpers in `config/config.go`, then call `pool.GetPool().Reload()` or `SetStrategy` from the relevant admin handler in `proxy/handler.go`.

### Upstream Calls Without Kiro Header Helpers

**What happens:** New Kiro REST/streaming calls hand-build partial headers.
**Why it's wrong:** Kiro compatibility depends on consistent user-agent, machine id, profile ARN, auth, region, and service headers.
**Do this instead:** Use `setKiroHeaders`, `buildStreamingHeaderValues`, and `applyKiroBaseHeaders` from `proxy/kiro_headers.go`.

### Unbounded Runtime State

**What happens:** New request/session/cache collections grow without capacity or TTL.
**Why it's wrong:** The proxy is a long-running process and existing stores are intentionally bounded.
**Do this instead:** Follow `requestLogStore` capacity in `proxy/request_log.go`, OpenAI response session TTL/capacity in `proxy/handler.go`, and prompt cache TTL in `proxy/cache_tracker.go`.

## Error Handling

**Strategy:** Return client-compatible JSON/SSE errors at the public API boundary, classify upstream failures for pool health, and expose operational details through admin request logs.

**Patterns:**
- Use `sendClaudeError`, `sendClaudeUpstreamError`, and `sendOpenAIError` in `proxy/handler.go` for API responses.
- Classify upstream failures with `pool.ClassifyFailureReason` in `pool/account.go` and record account/model failures through handler helpers.
- Preserve Anthropic request IDs via `parseAnthropicEnvelope` and `writeAnthropicRequestIDHeaders` in `proxy/anthropic_envelope.go`.
- For admin APIs, write JSON objects with HTTP status codes directly from `proxy/handler.go`.
- For fatal startup problems, `main.go` logs and exits.

## Cross-Cutting Concerns

**Logging:** Use `logger.Debugf`, `Infof`, `Warnf`, `Errorf`, and `Fatalf` from `logger/logger.go`; log level comes from `LOG_LEVEL` or persisted config.
**Validation:** Config validation lives in `config/config.go`; request shape, tool names, payload guard, and thinking-mode validation live in `proxy/handler.go` and `proxy/payload_guard.go`.
**Authentication:** Client API access uses API keys and IP allowlist in `config.ClientAccessConfig`; admin API uses the configured admin password; upstream Kiro auth uses per-account OAuth tokens refreshed by `auth/oidc.go`.
**Observability:** Request logs are collected in `proxy/request_log.go`, readiness/diagnostics in `proxy/ecosystem_ops.go`, and compatibility matrices in `docs/`.
**Concurrency:** Use mutexes/atomics around shared state: `config.cfgLock`, `pool.AccountPool.mu`, handler mutexes, request log store locks, and admission gate locks.

---

*Architecture analysis: 2026-05-21*
