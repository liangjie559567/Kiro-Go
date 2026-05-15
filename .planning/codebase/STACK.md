# Technology Stack

**Analysis Date:** 2026-05-15

## Languages

**Primary:**
- Go 1.21 module target - all backend service code in `main.go`, `auth/`, `config/`, `logger/`, `pool/`, and `proxy/`; declared in `go.mod`.

**Secondary:**
- HTML/CSS/JavaScript - single-file admin UI in `web/index.html`.
- Dockerfile syntax - container build in `Dockerfile`.
- YAML - Docker Compose and GitHub Actions configuration in `docker-compose.yml` and `.github/workflows/docker.yml`.

## Runtime

**Environment:**
- Go HTTP server using the standard library `net/http`; service entry point is `main.go`.
- Container runtime supported through a static Go binary built in `Dockerfile` and run on `alpine:latest`.
- Current local toolchain observed during analysis: Go 1.22.2. The repository target remains Go 1.21 via `go.mod` and `Dockerfile`.

**Package Manager:**
- Go modules.
- Lockfile: `go.sum` present.
- Dependency manifest: `go.mod`.

## Frameworks

**Core:**
- Go standard library HTTP stack - request routing, handlers, outbound HTTP clients, SSE, and static file serving in `main.go`, `proxy/handler.go`, `proxy/kiro.go`, `auth/http_client.go`.
- No third-party web framework is used; routes are implemented manually in `proxy/handler.go`.

**Testing:**
- Go `testing` package - unit tests are co-located as `*_test.go` files under `auth/`, `config/`, `pool/`, and `proxy/`.
- No external assertion or mocking framework is detected.

**Build/Dev:**
- `go build` - source build command documented in `README.md`.
- `go test ./...` - standard test runner implied by Go package tests.
- Docker BuildKit multi-stage build - `Dockerfile` builds with `golang:1.21-alpine` and copies the binary plus `web/` into `alpine:latest`.
- Docker Compose - local/container deployment in `docker-compose.yml`.
- GitHub Actions + Docker Buildx - multi-architecture image build and GHCR publishing in `.github/workflows/docker.yml`.

## Key Dependencies

**Critical:**
- `github.com/google/uuid` v1.6.0 - generates account IDs, OAuth state values, and web search tool-use IDs in `auth/iam_sso.go`, `auth/sso_token.go`, `proxy/handler.go`, and `proxy/kiro.go`.

**Infrastructure:**
- Go standard library `net/http` - inbound server in `main.go` and outbound clients in `auth/` and `proxy/`.
- Go standard library `encoding/json` - request/response translation and persistent config serialization in `config/config.go`, `proxy/translator.go`, and `proxy/handler.go`.
- Go standard library `sync`, `sync/atomic` - thread-safe config, account pool, counters, cached model lists, and swappable HTTP clients in `config/config.go`, `pool/account.go`, `proxy/handler.go`, `proxy/kiro.go`, and `auth/http_client.go`.
- Go standard library `crypto/rand`, `crypto/sha256`, `encoding/base64` - machine IDs and PKCE login support in `config/config.go` and `auth/iam_sso.go`.

## Configuration

**Environment:**
- `CONFIG_PATH` selects the JSON config path; default is `data/config.json` in `main.go`.
- `ADMIN_PASSWORD` overrides the persisted admin password at process startup in `main.go`.
- `LOG_LEVEL` overrides the configured log level in `logger/logger.go`.
- Standard proxy environment variables are honored by Go transports when no explicit app proxy is configured because `auth/http_client.go` and `proxy/kiro.go` use `http.ProxyFromEnvironment`.
- Runtime application configuration is persisted as JSON through `config/config.go`. The default file is `data/config.json`; this file can contain tokens and is not read or quoted in codebase documentation.
- Docker Compose sets `CONFIG_PATH=/app/data/config.json` and mounts `./data` to `/app/data` in `docker-compose.yml`.

**Build:**
- `go.mod` declares module `kiro-go`, Go target `1.21`, and dependency `github.com/google/uuid v1.6.0`.
- `go.sum` pins module checksums.
- `Dockerfile` performs a two-stage cross-platform build with `CGO_ENABLED=0`.
- `docker-compose.yml` builds the local image, maps `8080:8080`, persists `/app/data`, and restarts unless stopped.
- `.github/workflows/docker.yml` builds `linux/amd64` and `linux/arm64` images with Docker Buildx and publishes to `ghcr.io`.

## Platform Requirements

**Development:**
- Go 1.21+.
- Docker and Docker Compose for containerized execution.
- Network access to AWS/Kiro endpoints is required for authentication, model listing, account refresh, health checks, generation requests, and Kiro MCP web search.
- Writable config directory for `data/config.json` or the path configured by `CONFIG_PATH`.

**Production:**
- HTTP service binds to `0.0.0.0:8080` by default through `config/config.go` and `main.go`.
- Persist `/app/data` when running the Docker image so `data/config.json` survives restarts.
- Set `ADMIN_PASSWORD` or change the persisted admin password before exposing `/admin`.
- Configure `apiKey` and `requireApiKey` through `/admin/api/settings` when client API authentication is required.
- Use the GHCR image published by `.github/workflows/docker.yml` or build from `Dockerfile`.

---

*Stack analysis: 2026-05-15*
