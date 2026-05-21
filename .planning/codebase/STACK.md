# Technology Stack

**Analysis Date:** 2026-05-21

## Languages

**Primary:**
- Go 1.21 - HTTP proxy, auth flows, scheduler, config persistence, request translation, and tests in `main.go`, `auth/`, `config/`, `pool/`, `proxy/`, and `logger/`; declared in `go.mod`.

**Secondary:**
- HTML/CSS/JavaScript - single-file admin console in `web/index.html`, served by `proxy/handler.go`.
- JavaScript/Node.js - UAT and operational scripts under `docs/superpowers/uat/`; not part of the production application build.
- Markdown/JSON - documentation and compatibility matrices under `README.md`, `README_CN.md`, `docs/claude-code-compatibility-matrix.json`, `docs/kiro-ecosystem-operations-matrix.json`, and `docs/kiro-ha-compatibility-matrix.json`.

## Runtime

**Environment:**
- Go 1.21+ for local development and production binary builds, declared by `go.mod`.
- Docker runtime uses a multi-stage build: `golang:1.21-alpine` builder and `alpine:latest` runtime in `Dockerfile`.
- The production binary is a static-style Go HTTP server started by `CMD ["./kiro-go"]` in `Dockerfile`.
- The default HTTP listen address is `0.0.0.0:8080`, configured by `config/defaultConfig()` in `config/config.go` and logged by `main.go`.

**Package Manager:**
- Go modules.
- Lockfile: present via `go.sum`.
- Primary commands:
```bash
go test ./...          # Run all Go tests
go build -o kiro-go .  # Build local binary
docker-compose up -d   # Build and run container stack
```

## Frameworks

**Core:**
- Go standard library `net/http` - HTTP server, request routing, upstream calls, SSE/event stream parsing, and static file serving in `main.go`, `proxy/handler.go`, `proxy/kiro.go`, `proxy/kiro_api.go`, and `auth/*.go`.
- Go standard library `encoding/json` - request/response translation, config serialization, and admin API payloads in `config/config.go`, `proxy/translator.go`, `proxy/handler.go`, and `auth/*.go`.
- Go standard library `sync`, `sync/atomic`, and goroutines - shared config locking, account pool routing, background refresh jobs, and swappable HTTP clients in `config/config.go`, `pool/account.go`, `auth/http_client.go`, and `proxy/kiro.go`.

**Testing:**
- Go standard library `testing` - unit and integration-style tests in `*_test.go` files across `auth/`, `config/`, `pool/`, `proxy/`, and root `main_test.go`.
- No external Go test framework detected in `go.mod`.

**Build/Dev:**
- Docker - container build defined in `Dockerfile`.
- Docker Compose - local deployment and health check defined in `docker-compose.yml`.
- GitHub Actions - multi-arch image build and GHCR publication in `.github/workflows/docker.yml`.
- Docker Buildx/QEMU - configured by `.github/workflows/docker.yml` for `linux/amd64` and `linux/arm64` images.

## Key Dependencies

**Critical:**
- `github.com/google/uuid` v1.6.0 - UUID generation for IAM SSO sessions, Builder ID sessions, account IDs, request/session identifiers, and machine/account metadata in `auth/iam_sso.go`, `auth/sso_token.go`, and `proxy/kiro.go`.

**Infrastructure:**
- Go standard library HTTP stack - no third-party router, middleware, SDK, ORM, or cloud SDK is used.
- Alpine Linux packages - `ca-certificates` installed in the runtime image by `Dockerfile` for outbound HTTPS calls.

## Configuration

**Environment:**
- `CONFIG_PATH` - overrides the JSON config file location; default is `data/config.json` in `main.go`.
- `ADMIN_PASSWORD` - overrides the admin password at startup in `main.go`.
- `LOG_LEVEL` - overrides configured logging verbosity in `logger/logger.go`; accepted values are `debug`, `info`, `warn`, and `error` from `config/config.go`.
- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and lowercase variants - supported by auth and Kiro HTTP clients in `auth/http_client.go` and `proxy/kiro.go`.

**Application config:**
- Main persisted config is JSON at `data/config.json` by default; `config/config.go` creates it when missing and writes it with mode `0600`.
- Important config keys are modeled by `config.Config` in `config/config.go`: `host`, `port`, `password`, `apiKey`, `requireApiKey`, `clientApiKeys`, `clientIPAllowlist`, `accounts`, `autoRefresh`, `healthCheck`, `loadBalance`, `modelAdmission`, `stableDownstream`, `contentContinuity`, `claudeCodeGovernor`, `preferredEndpoint`, `endpointFallback`, `proxyURL`, `promptFilterRules`, and `logLevel`.
- Do not treat `data/config.json` as a harmless sample file: `config.Account` in `config/config.go` stores OAuth access tokens, refresh tokens, client secrets, profile ARNs, API keys, and optional proxy URLs.

**Build:**
- `go.mod` - module name, Go version, and Go dependencies.
- `go.sum` - dependency checksums.
- `Dockerfile` - multi-stage Go build and Alpine runtime.
- `docker-compose.yml` - local container service, bind mount, environment, and health check.
- `.github/workflows/docker.yml` - CI image build, GHCR login, metadata tags, cache, and multi-architecture publish.

## Platform Requirements

**Development:**
- Go 1.21+.
- Docker and Docker Compose for containerized local runs.
- Network access to AWS/Kiro endpoints for real account auth, token refresh, model calls, usage refresh, health checks, and web search diagnostics.
- Optional Node.js for UAT scripts under `docs/superpowers/uat/`; production app does not require Node.js.

**Production:**
- A container or host capable of running the Go binary and serving HTTP on the configured `host`/`port`.
- Persistent writable storage for `/app/data` or the configured `CONFIG_PATH`; `docker-compose.yml` mounts `./data:/app/data`.
- Outbound HTTPS to AWS IAM Identity Center/OIDC, AWS SSO portal, Kiro desktop auth, Amazon Q, and CodeWhisperer endpoints.
- Optional outbound proxy support through config `proxyURL`, per-account `proxyURL`, or standard proxy environment variables.
- Deployment target detected: Docker image published to GitHub Container Registry (`ghcr.io`) by `.github/workflows/docker.yml`.

---

*Stack analysis: 2026-05-21*
