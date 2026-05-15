# Codebase Structure

**Analysis Date:** 2026-05-15

## Directory Layout

```text
Kiro-Go/
├── main.go                 # Service bootstrap and HTTP server startup
├── go.mod                  # Go module definition (`kiro-go`, Go 1.21)
├── go.sum                  # Go dependency checksums
├── Dockerfile              # Container image build
├── docker-compose.yml      # Local/container deployment
├── README.md               # English project usage docs
├── README_CN.md            # Chinese project usage docs
├── version.json            # Release/version metadata
├── auth/                   # AWS/Kiro authentication flows and auth HTTP clients
├── config/                 # JSON-backed config schema and persistence
├── logger/                 # Lightweight leveled logger
├── pool/                   # Account pool, routing, health/cooldown state
├── proxy/                  # Core HTTP handler, translators, Kiro clients, retry logic
├── web/                    # Admin panel static frontend
├── data/                   # Runtime config data; contains secrets, do not inspect contents
├── docs/                   # Planning/spec/UAT documentation
├── recovery/               # Recovery artifacts; may contain account data, do not inspect contents
├── .github/workflows/      # GitHub Actions workflows
├── .planning/codebase/     # Generated codebase maps
└── .worktrees/             # Local git worktrees; exclude from primary scans
```

## Directory Purposes

**Root:**
- Purpose: Buildable Go module and service entry point.
- Contains: `main.go`, `go.mod`, `go.sum`, deployment files, README files, release metadata.
- Key files: `main.go`, `go.mod`, `Dockerfile`, `docker-compose.yml`, `README.md`.

**`auth/`:**
- Purpose: Authentication and token-management integrations for Kiro/AWS accounts.
- Contains: IAM SSO PKCE login, Builder ID device login, SSO token import, credentials import, token refresh, proxy-aware auth HTTP clients.
- Key files: `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go`, `auth/oidc.go`, `auth/http_client.go`.

**`config/`:**
- Purpose: Persistent application configuration, account schema, settings, validation, and JSON storage.
- Contains: `Config`, `Account`, typed settings structs, defaulting/normalization, update/accessor functions, tests.
- Key files: `config/config.go`, `config/config_test.go`.

**`logger/`:**
- Purpose: Shared leveled logging wrapper around Go `log`.
- Contains: Level parsing, level state, output redirection for tests, formatted logging functions.
- Key files: `logger/logger.go`.

**`pool/`:**
- Purpose: Account routing and runtime account health state.
- Contains: `AccountPool`, weighted account list construction, selection methods, cooldown/failure state, model support cache, stats updates, tests.
- Key files: `pool/account.go`, `pool/account_test.go`.

**`proxy/`:**
- Purpose: Main backend application layer.
- Contains: HTTP handler/router, public API orchestration, admin API, Claude/OpenAI translators, Kiro upstream clients, headers, account refresh, health checks, prompt cache tracking, model-specific admission gates, token estimation, tests.
- Key files: `proxy/handler.go`, `proxy/translator.go`, `proxy/kiro.go`, `proxy/kiro_api.go`, `proxy/kiro_headers.go`, `proxy/account_refresh.go`, `proxy/account_health.go`, `proxy/cache_tracker.go`, `proxy/opus_gate.go`, `proxy/token_estimator.go`.

**`web/`:**
- Purpose: Static admin frontend served by the Go backend.
- Contains: Single HTML page with embedded or linked admin UI assets.
- Key files: `web/index.html`.

**`docs/`:**
- Purpose: Project planning and workflow documentation.
- Contains: Superpowers/GSD specs, plans, and UAT artifacts.
- Key files: `docs/superpowers/specs/*`, `docs/superpowers/plans/*`, `docs/superpowers/uat/*`.

**`data/`:**
- Purpose: Runtime persistence directory.
- Contains: `data/config.json` by default.
- Key files: `data/config.json` exists and may contain secrets; do not read or quote contents.

**`recovery/`:**
- Purpose: Local recovery artifacts for configuration/account restoration.
- Contains: JSON/text recovery candidates and snapshots.
- Key files: `recovery/*.json`, `recovery/candidates/*.json`; treat as sensitive account data and avoid content inspection.

**`.planning/codebase/`:**
- Purpose: Generated GSD codebase intelligence documents.
- Contains: Architecture and structure docs for planning agents.
- Key files: `.planning/codebase/ARCHITECTURE.md`, `.planning/codebase/STRUCTURE.md`.

**`.worktrees/`:**
- Purpose: Local git worktree storage.
- Contains: Alternate checkout(s), including `.worktrees/merge-upstream-1.0.8`.
- Key files: Exclude from normal codebase analysis unless explicitly scoped.

## Key File Locations

**Entry Points:**
- `main.go`: Process entry point; initializes config/logger/pool/handler and starts HTTP server.
- `proxy/handler.go`: Runtime HTTP entry point through `Handler.ServeHTTP()`.
- `web/index.html`: Browser entry point for the admin UI.

**Configuration:**
- `config/config.go`: Config schema, defaults, validation, getters/updaters, JSON load/save.
- `data/config.json`: Runtime config file path created/used by default; contains account credentials and tokens.
- `go.mod`: Module path and dependency declaration.
- `Dockerfile`: Container build instructions.
- `docker-compose.yml`: Compose deployment wiring; inspect carefully because compose files can contain sensitive environment values.

**Core Logic:**
- `proxy/handler.go`: API/admin route dispatch, request orchestration, retries, token refresh, stats, background jobs.
- `proxy/translator.go`: Claude/OpenAI schema conversion and response construction.
- `proxy/kiro.go`: Kiro streaming request types, endpoint definitions, HTTP clients, event-stream handling.
- `proxy/kiro_api.go`: Kiro REST calls for usage, user info, profiles, and model lists.
- `pool/account.go`: Account scheduling and failure/cooldown state.
- `auth/oidc.go`: OAuth refresh path used before upstream requests.
- `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go`: Admin account import/login flows.

**Testing:**
- `config/config_test.go`: Config defaulting, validation, and health field persistence tests.
- `pool/account_test.go`: Account routing, overage, cooldown, failure classification, model selection tests.
- `proxy/handler_test.go`: Handler validation, retry behavior, admission gate, admin config, health/model cache tests.
- `proxy/translator_test.go`: Claude/OpenAI conversion and conversation ID behavior tests.
- `proxy/kiro_api_test.go`: Kiro API helper behavior and event stream parsing tests.
- `proxy/*_test.go`: Co-located package tests.

**Generated/Runtime Data:**
- `data/config.json`: Runtime state and secrets; do not inspect contents.
- `recovery/`: Recovery snapshots/candidates; treat as sensitive.
- `sub2api-playwright-blocked.png`, `kiro-playwright-blocked-20260515.png`: Local screenshot artifacts.

## Naming Conventions

**Files:**
- Go implementation files use lowercase snake_case for multiword package files: `proxy/account_refresh.go`, `proxy/cache_tracker.go`, `proxy/kiro_headers.go`.
- Tests are co-located with implementation and use `_test.go`: `proxy/handler_test.go`, `pool/account_test.go`.
- Package directories are short lowercase names: `auth`, `config`, `logger`, `pool`, `proxy`, `web`.
- Static frontend uses `web/index.html`.

**Directories:**
- One Go package per top-level directory for core backend packages.
- Keep runtime data under `data/` and recovery artifacts under `recovery/`.
- Keep generated planning/codebase intelligence under `.planning/codebase/`.
- Keep local alternate checkouts under `.worktrees/` and exclude them from normal implementation searches.

## Where to Add New Code

**New Public API Route:**
- Primary code: Add route matching in `Handler.ServeHTTP()` in `proxy/handler.go`.
- Handler implementation: Add method near related handlers in `proxy/handler.go`.
- Tests: Add focused tests in `proxy/handler_test.go` or a new `proxy/<feature>_test.go`.

**New Admin API Route:**
- Primary code: Add route case in `handleAdminAPI()` in `proxy/handler.go`.
- Handler implementation: Add `api...` method near existing admin methods in `proxy/handler.go`.
- Frontend integration: Update `web/index.html`.
- Tests: Add tests in `proxy/handler_test.go`.

**New Claude/OpenAI Translation Behavior:**
- Primary code: Update DTOs or conversion helpers in `proxy/translator.go`.
- Token estimate adjustments: Update `proxy/token_estimator.go`.
- Tests: Add cases in `proxy/translator_test.go` and handler tests when HTTP behavior changes.

**New Kiro Streaming Upstream Behavior:**
- Primary code: Add endpoint/client/event-stream logic in `proxy/kiro.go`.
- Header changes: Add or update helpers in `proxy/kiro_headers.go`.
- Tests: Add cases in `proxy/kiro_test.go` or `proxy/kiro_headers_test.go`.

**New Kiro REST Operation:**
- Primary code: Add function and response types in `proxy/kiro_api.go`.
- Route orchestration: Call it from `proxy/handler.go` or refresh/health code.
- Tests: Add cases in `proxy/kiro_api_test.go`.

**New Account Routing Rule:**
- Primary code: Update `pool/account.go`.
- Handler integration: Use pool methods from `proxy/handler.go`; do not duplicate scheduling.
- Tests: Add cases in `pool/account_test.go` and handler retry tests where behavior crosses HTTP boundaries.

**New Persistent Setting:**
- Primary code: Add field to `config.Config` in `config/config.go`.
- Accessors: Add `Get...()` and `Update...()` functions in `config/config.go`.
- Admin API: Add GET/POST handling in `proxy/handler.go`.
- Frontend: Update `web/index.html`.
- Tests: Add config normalization/validation tests in `config/config_test.go` and admin API tests in `proxy/handler_test.go`.

**New Authentication Flow:**
- Primary code: Add flow-specific implementation in `auth/`.
- Admin API: Add route and orchestration in `proxy/handler.go`.
- Config persistence: Store account fields through `config.Account` in `config/config.go`.
- Tests: Add unit tests in `auth/*_test.go` where possible and handler tests for admin API behavior.

**Utilities:**
- Shared logging: Use `logger/logger.go`.
- Shared backend helpers tightly coupled to proxy behavior: Place in `proxy/<topic>.go`.
- Cross-package account/config helpers: Prefer `config/config.go` or `pool/account.go` based on ownership.

## Special Directories

**`data/`:**
- Purpose: Runtime persistence.
- Generated: Yes.
- Committed: A `data/config.json` file is present in the working tree, but contents are sensitive and should not be read or quoted.

**`recovery/`:**
- Purpose: Local config/account recovery artifacts.
- Generated: Yes.
- Committed: Present in the working tree; treat contents as sensitive.

**`.planning/`:**
- Purpose: GSD planning and codebase intelligence artifacts.
- Generated: Yes.
- Committed: Project-dependent; generated maps live in `.planning/codebase/`.

**`.worktrees/`:**
- Purpose: Local alternate git checkouts.
- Generated: Yes.
- Committed: No; exclude from code scans unless explicitly scoped.

**`.github/workflows/`:**
- Purpose: CI/CD workflow definitions.
- Generated: No.
- Committed: Yes.

---

*Structure analysis: 2026-05-15*
