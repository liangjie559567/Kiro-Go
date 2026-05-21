# Codebase Structure

**Analysis Date:** 2026-05-21

## Directory Layout

```text
Kiro-Go/
├── main.go                     # Process entrypoint and HTTP server lifecycle
├── go.mod                      # Go module definition (`kiro-go`)
├── Dockerfile                  # Container build for the Go service
├── docker-compose.yml          # Local/container runtime wiring
├── auth/                       # Token refresh and login/import flows
├── config/                     # Persistent JSON configuration and account model
├── logger/                     # Lightweight leveled logger
├── pool/                       # Account scheduling, cooldowns, breakers, health
├── proxy/                      # HTTP handler, protocol translation, Kiro clients, observability
├── proxy/testdata/             # JSON fixtures for proxy compatibility tests
├── web/                        # Single-file admin UI
├── docs/                       # Compatibility/readiness docs and matrices
├── data/                       # Runtime config storage, not source code
├── recovery/                   # Local recovery artifacts, not application source
├── .github/workflows/          # Docker image CI workflow
└── .planning/                  # GSD planning and codebase map artifacts
```

## Directory Purposes

**`auth/`:**
- Purpose: Implement authentication flows used to obtain or refresh Kiro/AWS tokens.
- Contains: OIDC/social refresh, IAM SSO helpers, Builder ID flow, SSO token parsing, proxy-aware auth HTTP clients.
- Key files: `auth/oidc.go`, `auth/iam_sso.go`, `auth/builderid.go`, `auth/sso_token.go`, `auth/http_client.go`.

**`config/`:**
- Purpose: Own persisted application configuration and account records.
- Contains: `Config` and `Account` types, default values, normalization, validation, getters/setters, JSON file persistence.
- Key files: `config/config.go`, `config/config_test.go`.

**`logger/`:**
- Purpose: Provide the process-wide leveled logger.
- Contains: Level parsing, environment override, level-specific log functions.
- Key files: `logger/logger.go`.

**`pool/`:**
- Purpose: Convert persisted accounts into runtime routing state.
- Contains: Account weighting, load-balance strategy, cooldown maps, failure classification, runtime health, model circuit breakers, sticky account selection.
- Key files: `pool/account.go`, `pool/breaker.go`, `pool/account_test.go`, `pool/breaker_test.go`.

**`proxy/`:**
- Purpose: Main application package for HTTP routing, public API compatibility, Kiro upstream calls, admin API, background operations, and observability.
- Contains: `Handler`, route table, Claude/OpenAI/Kiro DTOs, translators, Kiro streaming/REST clients, payload guards, token estimation, request logs, admission gates, readiness diagnostics.
- Key files: `proxy/handler.go`, `proxy/translator.go`, `proxy/kiro.go`, `proxy/kiro_api.go`, `proxy/kiro_headers.go`, `proxy/request_log.go`, `proxy/ecosystem_ops.go`.

**`proxy/testdata/`:**
- Purpose: Store stable JSON fixtures for proxy tests.
- Contains: Claude Code wire request and tool-reference sample payloads.
- Key files: `proxy/testdata/claude_code_2_1_143_wire_request.json`, `proxy/testdata/claude_code_tool_reference_message.json`.

**`web/`:**
- Purpose: Admin panel served by the Go handler.
- Contains: One self-contained HTML file with CSS and JavaScript.
- Key files: `web/index.html`.

**`docs/`:**
- Purpose: Human-readable and machine-readable compatibility/readiness documentation.
- Contains: Claude Code compatibility matrix, Kiro HA matrix, ecosystem operations matrix, UAT evidence under `docs/superpowers/`.
- Key files: `docs/claude-code-compatibility-matrix.md`, `docs/claude-code-compatibility-matrix.json`, `docs/kiro-ha-compatibility-matrix.md`, `docs/kiro-ecosystem-operations.md`.

**`data/`:**
- Purpose: Runtime storage for config and account credentials.
- Contains: `data/config.json` at runtime by default.
- Key files: Do not read or quote contents from `data/config.json`; treat it as secret-bearing runtime state.

**`.github/workflows/`:**
- Purpose: CI workflow definitions.
- Contains: Docker workflow.
- Key files: `.github/workflows/docker.yml`.

**`.planning/`:**
- Purpose: GSD project context, roadmap, phase plans, and generated codebase maps.
- Contains: Project documents and `.planning/codebase/` analysis docs.
- Key files: `.planning/PROJECT.md`, `.planning/ROADMAP.md`, `.planning/codebase/ARCHITECTURE.md`, `.planning/codebase/STRUCTURE.md`.

## Key File Locations

**Entry Points:**
- `main.go`: Process startup, config initialization, handler creation, graceful shutdown.
- `proxy/handler.go`: HTTP entrypoint via `Handler.ServeHTTP`.
- `web/index.html`: Browser admin UI entrypoint served at `/admin`.
- `Dockerfile`: Container image build entrypoint.

**Configuration:**
- `config/config.go`: Source of config schema, defaults, validation, and persistence behavior.
- `data/config.json`: Runtime config file created/loaded by default; secret-bearing and not safe to inspect in docs.
- `docker-compose.yml`: Local container settings and volumes.
- `.github/workflows/docker.yml`: Docker build/publish automation.
- `version.json`: Release/version metadata consumed by project tooling or packaging.

**Core Logic:**
- `proxy/handler.go`: Route table, request orchestration, admin API, background jobs, API-compatible errors.
- `proxy/translator.go`: Claude/OpenAI/Kiro type definitions and conversion functions.
- `proxy/kiro.go`: Kiro streaming generation client and endpoint fallback.
- `proxy/kiro_api.go`: Kiro REST operations for usage, users, models, profiles.
- `proxy/kiro_headers.go`: Kiro-compatible headers and user-agent construction.
- `pool/account.go`: Account routing and runtime health.
- `pool/breaker.go`: Per-account/model breaker and sticky session state.
- `auth/oidc.go`: Token refresh for OIDC/social auth.

**Operations and Observability:**
- `proxy/request_log.go`: Request log entry schema, in-memory store, stats APIs.
- `proxy/request_classifier.go`: Interactive/subagent/background request classification.
- `proxy/ecosystem_ops.go`: Admin diagnostics, scheduler preview, fleet readiness, web-search diagnostics.
- `proxy/account_refresh.go`: Auto-refresh status and batch helpers.
- `proxy/account_health.go`: Health-check status and batch helpers.
- `logger/logger.go`: Leveled process logging.

**Compatibility and Capacity:**
- `proxy/anthropic_envelope.go`: Anthropic envelope and Claude Code metadata parsing.
- `proxy/claude_sse_writer.go`: Claude SSE event writer.
- `proxy/opus_gate.go`: Model admission gates and pressure tracking.
- `proxy/claude_code_concurrency_governor.go`: Per-session Claude Code concurrency limits.
- `proxy/content_continuity.go`: Stable downstream/content continuity helpers.
- `proxy/payload_guard.go`: Kiro payload size and tool schema trimming.
- `proxy/cache_tracker.go`: Prompt cache usage estimation.
- `proxy/token_estimator.go`: Token estimates for count-tokens and logs.

**Testing:**
- `main_test.go`: Server construction and shutdown behavior.
- `config/config_test.go`: Config defaults, normalization, validation, persistence behavior.
- `auth/http_client_test.go`: Auth HTTP client/proxy behavior.
- `pool/*_test.go`: Pool selection and breaker behavior.
- `proxy/*_test.go`: Handler, translator, Kiro API, headers, admission, logging, compatibility, and diagnostics behavior.
- `proxy/testdata/*.json`: Test fixtures for Claude Code wire compatibility.

## Naming Conventions

**Files:**
- Use lowercase package-oriented Go files: `proxy/handler.go`, `proxy/translator.go`, `pool/account.go`.
- Use focused suffixes for specialized proxy helpers: `proxy/request_log.go`, `proxy/request_classifier.go`, `proxy/claude_sse_writer.go`.
- Co-locate tests with implementation and name them `*_test.go`: `proxy/handler_test.go`, `config/config_test.go`.
- Store static JSON fixtures under `proxy/testdata/` with descriptive snake-case names.
- Keep docs as lowercase/kebab-case Markdown/JSON under `docs/`.

**Directories:**
- Use top-level package directories for Go packages: `auth/`, `config/`, `logger/`, `pool/`, `proxy/`.
- Use `web/` only for static admin assets.
- Use `docs/` for committed documentation and compatibility matrices.
- Use `.planning/codebase/` for generated mapper documents.

## Where to Add New Code

**New Public API Endpoint:**
- Primary code: Add route case and handler method in `proxy/handler.go`.
- Request/response conversion: Add DTOs and conversion helpers in `proxy/translator.go` when the endpoint uses Claude/OpenAI/Kiro protocol shapes.
- Tests: Add focused cases to `proxy/handler_test.go` and translator cases to `proxy/translator_test.go`.

**New Admin API Endpoint:**
- Primary code: Add authenticated route case in `handleAdminAPI` inside `proxy/handler.go`.
- UI integration: Add JavaScript/UI controls in `web/index.html`.
- Tests: Add route/auth/response coverage to `proxy/handler_test.go` or domain-specific `proxy/*_test.go`.

**New Kiro REST Operation:**
- Implementation: Add function to `proxy/kiro_api.go`.
- Headers/proxy: Use existing helpers from `proxy/kiro_headers.go` and `proxy/kiro.go`.
- Tests: Add coverage in `proxy/kiro_api_test.go`.

**New Kiro Streaming Behavior:**
- Implementation: Add endpoint/wire behavior in `proxy/kiro.go`.
- Header behavior: Add or adjust helpers in `proxy/kiro_headers.go`.
- Tests: Add coverage in `proxy/kiro_test.go` and `proxy/kiro_headers_test.go`.

**New Protocol Translation Feature:**
- Implementation: Add DTO fields and conversion logic in `proxy/translator.go`.
- Payload safety: Update `proxy/payload_guard.go` if the new feature affects payload size, tool schemas, or history compaction.
- Tests: Add coverage in `proxy/translator_test.go` and handler integration coverage in `proxy/handler_test.go`.

**New Account Routing Strategy:**
- Implementation: Add runtime behavior to `pool/account.go`.
- Config: Add persisted setting, defaults, normalization, and validation in `config/config.go`.
- Admin API/UI: Add settings route handling in `proxy/handler.go` and controls in `web/index.html`.
- Tests: Add `pool/account_test.go`, `config/config_test.go`, and handler tests as needed.

**New Auth Flow:**
- Implementation: Add provider-specific code under `auth/`.
- Admin API: Wire start/poll/import/complete endpoints in `proxy/handler.go`.
- Config impact: Store only necessary fields in `config.Account` within `config/config.go`.
- Tests: Add auth package tests and admin handler tests.

**New Observability Field:**
- Implementation: Add field to `RequestLogEntry` and updater helpers in `proxy/request_log.go`.
- Route usage: Call updater from `proxy/handler.go` or related proxy helper.
- Admin/UI: Display from `/admin/api/request-logs` in `web/index.html` if user-facing.
- Tests: Add coverage in `proxy/request_log_test.go` and relevant handler tests.

**Utilities:**
- Shared helper for config/account/runtime behavior: place in the owning package (`config/`, `pool/`, or `proxy/`), not a generic utility package.
- Logging helper: extend `logger/logger.go` only when it is process-wide logging behavior.

## Special Directories

**`data/`:**
- Purpose: Runtime config and account credential storage.
- Generated: Yes.
- Committed: Directory may exist locally; secret-bearing contents should not be documented or inspected.

**`recovery/`:**
- Purpose: Local recovery/candidate artifacts.
- Generated: Yes.
- Committed: Treat as local operational data, not application source.

**`.uat-backups/`:**
- Purpose: Local UAT backup captures.
- Generated: Yes.
- Committed: No source code should be added here.

**`.worktrees/`:**
- Purpose: Local git worktrees for parallel development.
- Generated: Yes.
- Committed: No. Do not treat files under `.worktrees/` as part of the primary source tree map.

**`docs/superpowers/`:**
- Purpose: UAT reports, screenshots, scripts, and evidence from external workflow tooling.
- Generated: Mixed.
- Committed: Some artifacts are committed; add new product docs to top-level `docs/` unless the artifact is specifically UAT evidence.

**`proxy/testdata/`:**
- Purpose: Test fixtures loaded by proxy tests.
- Generated: No.
- Committed: Yes.

---

*Structure analysis: 2026-05-21*
