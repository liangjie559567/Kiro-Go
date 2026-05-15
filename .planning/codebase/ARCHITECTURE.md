<!-- refreshed: 2026-05-15 -->
# Architecture

**Analysis Date:** 2026-05-15

## System Overview

```text
┌─────────────────────────────────────────────────────────────┐
│                    HTTP Service Process                      │
│                         `main.go`                            │
├──────────────────┬──────────────────┬───────────────────────┤
│ Claude API       │ OpenAI API       │ Admin/Web API          │
│ `/v1/messages`   │ `/v1/chat/...`   │ `/admin`, `/admin/api` │
│ `proxy/handler.go` │ `proxy/handler.go` │ `proxy/handler.go`  │
└────────┬─────────┴────────┬─────────┴──────────┬────────────┘
         │                  │                    │
         ▼                  ▼                    ▼
┌─────────────────────────────────────────────────────────────┐
│                Translation and Routing Layer                 │
│ `proxy/translator.go`, `proxy/handler.go`, `pool/account.go` │
└────────┬──────────────────┬────────────────────┬────────────┘
         │                  │                    │
         ▼                  ▼                    ▼
┌──────────────────┐ ┌──────────────────┐ ┌───────────────────┐
│ Kiro Upstream    │ │ Config Store     │ │ Auth Providers    │
│ `proxy/kiro.go`  │ │ `config/config.go` │ │ `auth/*.go`      │
│ `proxy/kiro_api.go` │ `data/config.json` │ AWS OIDC/Kiro    │
└──────────────────┘ └──────────────────┘ └───────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| Process bootstrap | Resolve config path, initialize config/logger/account pool/handler, start `http.ListenAndServe`. | `main.go` |
| HTTP router and orchestration | Dispatch public API, admin API, health/stats routes; manage request validation, retries, token refresh, response streaming, background workers. | `proxy/handler.go` |
| Protocol translation | Convert Claude/OpenAI request and response shapes to/from Kiro payloads, model aliases, thinking mode, tools, images, prompt filters. | `proxy/translator.go` |
| Kiro streaming client | Build Kiro request payload types, HTTP clients, proxy-aware transports, endpoint fallback, AWS event-stream parsing callbacks. | `proxy/kiro.go` |
| Kiro REST client | Fetch usage limits, user info, available models, profile ARN, and account metadata. | `proxy/kiro_api.go` |
| Account pool | Maintain weighted round-robin account selection, model support cache, cooldowns, failure classification, per-account stats. | `pool/account.go` |
| Persistent config | Own JSON-backed runtime configuration, account records, settings, stats, prompt filters, thinking settings, proxy settings. | `config/config.go` |
| Authentication flows | Start/complete AWS IAM SSO, Builder ID device auth, SSO token import, credentials import, OAuth token refresh. | `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go`, `auth/oidc.go` |
| Logging | Provide lightweight leveled logging configured at startup. | `logger/logger.go` |
| Admin frontend | Single-page admin panel served by the Go handler. | `web/index.html` |

## Pattern Overview

**Overall:** Single-binary layered proxy with package-level singletons and handler-centered orchestration.

**Key Characteristics:**
- Use the standard library `net/http` `Handler` pattern; `proxy.Handler` implements `ServeHTTP` in `proxy/handler.go`.
- Keep persistent application state in `config.Config`, protected by package-level `sync.RWMutex` in `config/config.go`.
- Keep runtime routing state in the global `pool.AccountPool` singleton from `pool.GetPool()` in `pool/account.go`.
- Place upstream protocol and response shape logic in the `proxy` package instead of adding separate service packages.
- Start background workers from `proxy.NewHandler()` for model cache refresh, auto-refresh, health checks, and stats persistence.

## Layers

**Bootstrap Layer:**
- Purpose: Assemble the process and bind the HTTP server.
- Location: `main.go`
- Contains: Config path resolution, data directory creation, config initialization, logger initialization, admin password env override, account pool warm-up, handler construction.
- Depends on: `config`, `logger`, `pool`, `proxy`, Go `net/http`.
- Used by: The compiled `kiro-go` executable and Docker entrypoint.

**HTTP/API Layer:**
- Purpose: Route incoming requests and enforce API/admin authentication.
- Location: `proxy/handler.go`
- Contains: `Handler`, `NewHandler()`, `ServeHTTP()`, public Claude/OpenAI endpoints, admin API endpoints, health/stats endpoints, SSE response writers.
- Depends on: `config`, `pool`, `auth`, `logger`, translator/client helpers in the same `proxy` package.
- Used by: `main.go`.

**Translation Layer:**
- Purpose: Normalize client-facing Claude/OpenAI schemas into Kiro payloads and translate Kiro output back to client-compatible responses.
- Location: `proxy/translator.go`
- Contains: `ClaudeRequest`, `OpenAIRequest`, `ClaudeToKiro()`, `OpenAIToKiro()`, response builders, model mapping, prompt filter application, tool/image conversion.
- Depends on: `config`, `github.com/google/uuid`, standard JSON/string/time helpers.
- Used by: `proxy/handler.go`.

**Upstream Client Layer:**
- Purpose: Call Kiro/Amazon Q/CodeWhisperer streaming and REST APIs.
- Location: `proxy/kiro.go`, `proxy/kiro_api.go`, `proxy/kiro_headers.go`
- Contains: `KiroPayload`, stream callback types, endpoint definitions, proxy-aware HTTP clients, REST account/model/profile functions, Kiro header construction.
- Depends on: `config`, `auth`, `logger`, Go HTTP client.
- Used by: `proxy/handler.go`, `proxy/account_refresh.go`, health/model cache jobs.

**State Layer:**
- Purpose: Store and expose persistent configuration plus runtime account scheduling state.
- Location: `config/config.go`, `pool/account.go`
- Contains: `Config`, `Account`, settings accessors/updaters, JSON persistence, `AccountPool`, weighted selection, cooldown state, model support cache.
- Depends on: Standard library synchronization, filesystem, JSON.
- Used by: `main.go`, `proxy/*`, `auth/*`.

**Auth Layer:**
- Purpose: Implement token acquisition and refresh for all supported account import flows.
- Location: `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go`, `auth/oidc.go`, `auth/http_client.go`
- Contains: AWS IAM SSO PKCE flow, Builder ID device flow, bearer token import, credentials parsing, token refresh, auth HTTP clients.
- Depends on: `config`, Go HTTP/crypto packages.
- Used by: Admin API methods and `Handler.ensureValidToken()` in `proxy/handler.go`.

**Frontend Asset Layer:**
- Purpose: Provide the admin UI served directly by the backend.
- Location: `web/index.html`
- Contains: Static HTML/CSS/JS admin panel.
- Depends on: `/admin/api/*` routes in `proxy/handler.go`.
- Used by: `serveAdminPage()` and `serveStaticFile()` in `proxy/handler.go`.

## Data Flow

### Claude Request Path

1. `main.main()` initializes config, logger, pool, and `proxy.NewHandler()` before starting `http.ListenAndServe` (`main.go:28`).
2. `Handler.ServeHTTP()` matches `/v1/messages`, validates API key when required, and calls `handleClaudeMessages()` (`proxy/handler.go:584`).
3. `handleClaudeMessagesInternal()` reads JSON, validates the Claude request, resolves thinking mode, estimates tokens, and calls `ClaudeToKiro()` (`proxy/handler.go:1022`, `proxy/translator.go:179`).
4. `handleClaudeWithAccountRetry()` selects accounts using `h.pool.GetNextForModelExcept()`, refreshes tokens with `ensureValidToken()`, computes prompt cache usage, and delegates to stream/non-stream attempt handlers (`proxy/handler.go:1400`, `pool/account.go:248`, `proxy/handler.go:2715`).
5. Kiro streaming/non-stream helpers call upstream endpoints through proxy-aware clients and Kiro payload/callback types (`proxy/kiro.go:42`, `proxy/kiro.go:164`).
6. Handler response builders emit Claude-compatible JSON or SSE and update handler stats plus account stats (`proxy/handler.go`, `pool/account.go`).

### OpenAI Request Path

1. `Handler.ServeHTTP()` matches `/v1/chat/completions`, validates API key when required, and calls `handleOpenAIChat()` (`proxy/handler.go:617`, `proxy/handler.go:2202`).
2. The OpenAI request is validated, model/thinking mode is resolved, and `OpenAIToKiro()` converts messages/tools/images into a `KiroPayload` (`proxy/translator.go:935`).
3. `handleOpenAIWithAccountRetry()` performs the same account selection, token refresh, Opus 4.7 admission, retry, and stream/non-stream split as the Claude path (`proxy/handler.go:1481`).
4. Kiro output is converted to OpenAI-compatible responses using response builders in `proxy/translator.go`.

### Admin Account Flow

1. `Handler.ServeHTTP()` routes `/admin/api/*` to `handleAdminAPI()` (`proxy/handler.go:633`).
2. `handleAdminAPI()` checks `X-Admin-Password` or `admin_password` cookie against `config.GetPassword()` (`proxy/handler.go:2759`).
3. Auth endpoints call `auth.StartIamSsoLogin()`, `auth.CompleteIamSsoLogin()`, `auth.StartBuilderIdLogin()`, `auth.PollBuilderIdAuth()`, or import helpers (`proxy/handler.go:2811`, `auth/iam_sso.go:43`, `auth/builderid.go:31`).
4. Account changes are persisted through `config.AddAccount()`, `config.UpdateAccount()`, `config.DeleteAccount()`, and then the account pool is reloaded where needed (`config/config.go`, `pool/account.go:98`).

### Background Maintenance Flow

1. `NewHandler()` starts `backgroundRefresh()`, `backgroundHealthCheck()`, and `backgroundStatsSaver()` goroutines (`proxy/handler.go:324`).
2. `backgroundRefresh()` periodically runs `runAutoRefresh()` and `refreshModelsCache()` (`proxy/handler.go:351`).
3. `runAutoRefresh()` selects configured accounts, calls `refreshAccountData()`, reloads the pool, and updates refresh status (`proxy/handler.go:426`, `proxy/account_refresh.go`).
4. `backgroundHealthCheck()` periodically calls `runHealthCheck()`; unhealthy accounts can be disabled and persisted (`proxy/handler.go:454`).
5. `backgroundStatsSaver()` persists aggregate stats back to `data/config.json` through `config.UpdateStats()` (`proxy/handler.go:2020`, `config/config.go`).

**State Management:**
- Persistent state lives in `data/config.json` by default and is accessed through package-level functions in `config/config.go`.
- Runtime account scheduling state lives in the `pool.AccountPool` singleton, with mutex-protected cooldowns, model cache, and weighted account list in `pool/account.go`.
- Per-handler runtime stats and caches live on `proxy.Handler`, using atomics, mutexes, and background goroutines in `proxy/handler.go`.
- Auth login sessions are in-memory package-level maps protected by mutexes in `auth/iam_sso.go` and `auth/builderid.go`.

## Key Abstractions

**`proxy.Handler`:**
- Purpose: HTTP router plus orchestration boundary for public API, admin API, retries, stats, and background jobs.
- Examples: `proxy/handler.go`
- Pattern: Stateful `http.Handler` with internal goroutines and shared dependencies.

**`config.Config` and `config.Account`:**
- Purpose: Canonical persisted shape for service settings, account credentials, account health, and usage counters.
- Examples: `config/config.go`
- Pattern: Package-level JSON store with exported accessor/update functions and `sync.RWMutex`.

**`pool.AccountPool`:**
- Purpose: Runtime account scheduler and health/cooldown tracker.
- Examples: `pool/account.go`
- Pattern: Global singleton initialized with `sync.Once`; methods protect mutable slices/maps with `sync.RWMutex`.

**`proxy.KiroPayload`:**
- Purpose: Internal upstream request shape sent to Kiro/Amazon Q/CodeWhisperer endpoints.
- Examples: `proxy/kiro.go`, `proxy/translator.go`
- Pattern: Shared DTO used by both Claude and OpenAI adapters.

**Client Protocol DTOs:**
- Purpose: Represent input/output schemas for Anthropic Claude and OpenAI-compatible APIs.
- Examples: `proxy/translator.go`
- Pattern: Structs with JSON tags and conversion helpers colocated with translation logic.

**Auth Session Records:**
- Purpose: Track short-lived IAM SSO and Builder ID login state across admin API requests.
- Examples: `auth/iam_sso.go`, `auth/builderid.go`
- Pattern: Package-level map plus mutex; cleanup goroutine removes expired sessions.

## Entry Points

**Executable:**
- Location: `main.go`
- Triggers: `go run .`, compiled binary, Docker container command.
- Responsibilities: Initialize all global state and start the HTTP server.

**Public Claude API:**
- Location: `proxy/handler.go`
- Triggers: `POST /v1/messages`, `POST /messages`, `POST /anthropic/v1/messages`
- Responsibilities: Validate request/API key, translate to Kiro, route through account pool, emit Claude-compatible JSON/SSE.

**Claude Token Count API:**
- Location: `proxy/handler.go`
- Triggers: `POST /v1/messages/count_tokens`, `POST /messages/count_tokens`
- Responsibilities: Estimate Claude request input tokens without upstream generation.

**Public OpenAI API:**
- Location: `proxy/handler.go`
- Triggers: `POST /v1/chat/completions`, `POST /chat/completions`
- Responsibilities: Validate request/API key, translate to Kiro, route through account pool, emit OpenAI-compatible JSON/SSE.

**Models API:**
- Location: `proxy/handler.go`
- Triggers: `GET /v1/models`, `GET /models`
- Responsibilities: Return cached or fallback model list with thinking variants and OpenAI aliases.

**Admin UI:**
- Location: `web/index.html`, `proxy/handler.go`
- Triggers: `GET /admin`, `GET /admin/*`
- Responsibilities: Serve static admin frontend and assets.

**Admin API:**
- Location: `proxy/handler.go`
- Triggers: `/admin/api/*`
- Responsibilities: Authenticate admin password, manage accounts/settings/auth flows/stats/model refresh.

**Health and Stats:**
- Location: `proxy/handler.go`
- Triggers: `GET /health`, `GET /`, `GET /v1/stats`
- Responsibilities: Report process health, uptime, version, and authenticated aggregate stats.

## Architectural Constraints

- **Threading:** Single Go process with `net/http` goroutine-per-request behavior plus background goroutines started in `proxy.NewHandler()` for refresh, health checks, and stats persistence.
- **Global state:** Use package-level globals in `config/config.go` (`cfg`, `cfgLock`, `cfgPath`), `pool/account.go` (`pool`, `poolOnce`), `auth/iam_sso.go` (`sessions`), `auth/builderid.go` (`builderIdSessions`), `proxy/kiro.go` (`kiroHttpStore`, `kiroRestHttpStore`, `proxyClientCache`), and `proxy/handler.go` (`opus47AdmissionGate`, injectable test variables).
- **Circular imports:** Package dependencies are directed: `main` imports `config/logger/pool/proxy`; `proxy` imports `auth/config/logger/pool`; `pool` imports `config`; `auth` imports `config`. No circular package import is present.
- **Persistence model:** `data/config.json` is both settings storage and account/stat persistence. Do not read or quote live `data/config.json` contents because it contains credentials/tokens.
- **Dependency injection:** Most production dependencies are package-level functions or globals. Tests replace selected variables such as `ensureValidTokenForHealthCheck` in `proxy/handler.go`.
- **Routing:** Routes are switch-based in `proxy/handler.go`; add new routes by extending `ServeHTTP()` for public paths or `handleAdminAPI()` for admin paths.

## Anti-Patterns

### Bypassing Config Accessors

**What happens:** Code directly mutates or reads `config.Get()` state outside the accessor/update functions.
**Why it's wrong:** `config.Config` is guarded by `cfgLock`, and direct mutation can skip persistence or race with background workers.
**Do this instead:** Add or use an accessor/update function in `config/config.go`, then call it from `proxy/handler.go` or `pool/account.go`.

### Duplicating Account Selection

**What happens:** Request handlers manually scan `config.GetAccounts()` to pick a serving account.
**Why it's wrong:** Manual selection bypasses weights, cooldowns, token-expiry skipping, overage rules, and model support cache in `pool.AccountPool`.
**Do this instead:** Use `h.pool.GetNextForModelExcept()` or related `pool.AccountPool` methods in `pool/account.go`.

### Adding Upstream HTTP Calls in Admin Handlers

**What happens:** Admin route methods construct Kiro/AWS HTTP requests inline.
**Why it's wrong:** Header construction, proxy selection, endpoint details, and REST/stream timeouts are already centralized.
**Do this instead:** Put upstream Kiro calls in `proxy/kiro_api.go` or streaming client code in `proxy/kiro.go`; keep admin handlers in `proxy/handler.go` as orchestration.

### Reading Runtime Secret Files in Tooling

**What happens:** Development scripts or analysis read `data/config.json`, `recovery/*.json`, or credentials-like files.
**Why it's wrong:** The config and recovery files can contain account tokens, refresh tokens, client secrets, and credentials.
**Do this instead:** Treat those files as persistent data stores; inspect schema in `config/config.go` and note file existence only.

## Error Handling

**Strategy:** Return explicit HTTP errors at the API boundary, classify account/upstream failures for retry/cooldown behavior, and persist account health when needed.

**Patterns:**
- Public Claude errors use `sendClaudeError()` from `proxy/handler.go`.
- Public OpenAI errors use `sendOpenAIError()` from `proxy/handler.go`.
- Upstream/account failures are classified by `pool.ClassifyFailureReason()` and recorded with `recordAccountFailure()` in `proxy/handler.go`.
- Rate-limit errors can carry a reset time through the `rateLimitResetError` interface in `proxy/handler.go` and `proxy/kiro.go`.
- Startup failures are fatal in `main.go` using standard `log.Fatalf` before `logger.Init()` and `logger.Fatalf()` afterward.

## Cross-Cutting Concerns

**Logging:** Use `logger.Debugf/Infof/Warnf/Errorf/Fatalf` from `logger/logger.go`; log level comes from `LOG_LEVEL` or config.

**Validation:** Request shape validation is in `validateClaudeRequestShape()`, `validateClaudeThinkingConfig()`, and `validateOpenAIRequestShape()` in `proxy/handler.go`; config validation functions live in `config/config.go`.

**Authentication:** Public API key checks are handled by `Handler.validateApiKey()` in `proxy/handler.go`; admin auth checks `X-Admin-Password` or `admin_password` cookie in `handleAdminAPI()`; upstream token refresh is in `auth/oidc.go` and `Handler.ensureValidToken()`.

**Concurrency:** Use mutexes/atomics already present on `proxy.Handler`, `config`, `pool`, and auth session maps. Preserve lock boundaries and avoid calling slow upstream network operations while holding locks unless the existing function already does so.

---

*Architecture analysis: 2026-05-15*
