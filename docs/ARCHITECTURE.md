<!-- generated-by: gsd-doc-writer -->
# Architecture

## System Overview

Kiro-Go is a modular Go reverse proxy. The process accepts Anthropic-compatible, OpenAI-compatible, admin, and health HTTP requests, converts generation requests into Kiro-compatible payloads, routes them through a runtime account pool, sends them to Kiro/CodeWhisperer/Amazon Q upstream endpoints, and converts responses back to the requested public API shape.

## Component Diagram

```text
HTTP clients / Claude Code / sub2api
        |
        v
main.go -> proxy.Handler (proxy/handler.go) -> admin UI (web/index.html)
        |             |
        |             +-> request logs, stats, readiness, admin APIs
        |
        v
proxy translation and controls
  - proxy/translator.go
  - proxy/anthropic_envelope.go
  - proxy/opus_gate.go
  - proxy/claude_code_concurrency_governor.go
  - proxy/content_continuity.go
        |
        v
pool/account.go and pool/breaker.go
        |
        v
config/config.go + auth/*.go
        |
        v
proxy/kiro.go, proxy/kiro_api.go, proxy/kiro_headers.go
        |
        v
Kiro / CodeWhisperer / Amazon Q upstream APIs
```

## Data Flow

1. `main.go` resolves `CONFIG_PATH` or defaults to `data/config.json`, creates the data directory, initializes `config`, `logger`, and `pool`, then creates `proxy.NewHandler()`.
2. `proxy.Handler.ServeHTTP` applies CORS headers and routes public API, admin, static, health, telemetry, and stats paths.
3. Public generation routes validate client access with IP allowlist and API key checks before dispatching to protocol-specific handlers.
4. Anthropic requests are parsed by handlers in `proxy/handler.go`, converted by `ClaudeToKiro` in `proxy/translator.go`, routed through account selection and admission controls, and sent through `CallKiroAPIWithContext` in `proxy/kiro.go`.
5. OpenAI Chat Completions and Responses requests follow the same account retry and upstream path after conversion through `OpenAIToKiro`.
6. Streaming responses are written through protocol-specific SSE writers; non-streaming responses are converted back through translator helpers.
7. Runtime request logs, account success/failure state, token usage, and persisted stats are updated in `proxy/request_log.go`, `pool/account.go`, and `config/config.go`.

## Key Abstractions

| Abstraction | Location | Purpose |
|---|---|---|
| `proxy.Handler` | `proxy/handler.go` | Central HTTP controller, admin API router, background job owner, and in-memory runtime holder. |
| `config.Config` | `config/config.go` | Persisted settings for accounts, access control, admission, prompt filtering, proxy, thinking, endpoints, and stats. |
| `config.Account` | `config/config.go` | Stored Kiro account identity, credentials, routing metadata, usage, and health fields. |
| `pool.AccountPool` | `pool/account.go` | Runtime scheduler for enabled accounts with health, cooldown, model breaker, and load-balancing state. |
| `ModelAdmissionConfig` | `config/config.go` | Per-model admission limits used by the proxy admission path. |
| `StableDownstreamConfig` | `config/config.go` | Controls downstream-compatible handling for configured models such as `claude-opus-4.7`. |
| Protocol DTOs and converters | `proxy/translator.go` | Claude, OpenAI, and Kiro request/response shapes and conversion functions. |
| Kiro clients | `proxy/kiro.go`, `proxy/kiro_api.go`, `proxy/kiro_headers.go` | Upstream streaming, REST, endpoint fallback, profile, and header behavior. |
| Auth helpers | `auth/oidc.go`, `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go` | Token refresh and login/import flows. |

## Directory Structure Rationale

```text
.
├── main.go              # process bootstrap and HTTP server lifecycle
├── auth/                # Kiro/AWS auth flows and proxy-aware auth clients
├── config/              # persisted JSON configuration and account schema
├── logger/              # leveled process logger
├── pool/                # account scheduling, health, cooldowns, and breakers
├── proxy/               # HTTP routes, protocol translation, upstream clients, admin APIs, observability
├── proxy/testdata/      # JSON fixtures for compatibility tests
├── web/                 # single-file admin UI
├── docs/                # project documentation and compatibility matrices
├── Dockerfile           # multi-stage container build
└── docker-compose.yml   # local container runtime wiring
```

The repository uses top-level Go package directories for major runtime responsibilities. The `proxy/` package is intentionally broad because the HTTP handler, protocol conversion, upstream calls, and admin operations share request-scoped runtime state. The `config/`, `pool/`, and `auth/` packages isolate persistence, account routing, and credential flows from HTTP routing.

## Runtime State

- Persistent configuration is stored in a JSON file controlled by `config.Init`; the default path is `data/config.json`.
- Runtime account routing state lives in the singleton account pool from `pool.GetPool()`.
- Request logs, response sessions, admission state, prompt cache metadata, background refresh state, and stats live on `proxy.Handler`.
- `data/` is runtime storage and can contain credentials; documentation should describe its purpose without quoting local secret-bearing contents.

## Entry Points

| Entry point | Location | Trigger |
|---|---|---|
| Process startup | `main.go` | Running `go run .` or the compiled `kiro-go` binary. |
| Public API router | `proxy/handler.go` | Requests to `/v1/messages`, `/v1/chat/completions`, `/v1/responses`, `/v1/models`, `/v1/stats`, `/health`, and related aliases. |
| Admin API router | `proxy/handler.go` | Requests under `/admin/api/*` after admin password validation. |
| Admin frontend | `web/index.html` | Browser requests to `/admin`. |
| Container runtime | `Dockerfile`, `docker-compose.yml` | Docker or Docker Compose startup. |
| CI build | `.github/workflows/docker.yml` | Push, pull request, tag, or workflow dispatch events. |
