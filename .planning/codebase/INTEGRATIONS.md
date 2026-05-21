# External Integrations

**Analysis Date:** 2026-05-21

## APIs & External Services

**AWS/Kiro model generation:**
- Amazon Q / Kiro streaming generation - upstream generation backend for local Claude/OpenAI-compatible requests.
  - SDK/Client: Go standard library `net/http` in `proxy/kiro.go`.
  - Endpoints: `https://q.{region}.amazonaws.com/generateAssistantResponse` and `https://codewhisperer.{region}.amazonaws.com/generateAssistantResponse`, defined by `kiroEndpoints` in `proxy/kiro.go`.
  - Auth: per-account OAuth access token stored in `data/config.json` via `config.Account.AccessToken` in `config/config.go`; sent as bearer-style Kiro/AWS headers by `proxy/kiro_headers.go` and `proxy/kiro.go`.
  - Configuration: region comes from `config.Account.Region`; endpoint preference is `preferredEndpoint` (`auto`, `kiro`, `codewhisperer`, or `amazonq`) in `config/config.go`.

**AWS/Kiro REST account metadata:**
- CodeWhisperer REST APIs - usage limits, user info, available models, and profile discovery.
  - SDK/Client: Go standard library `net/http` in `proxy/kiro_api.go`.
  - Endpoints: `https://codewhisperer.{region}.amazonaws.com/getUsageLimits`, `/GetUserInfo`, `/ListAvailableModels`, and `/ListAvailableProfiles` in `proxy/kiro_api.go`.
  - Auth: per-account access token and profile ARN fields from `config.Account` in `config/config.go`.

**AWS IAM Identity Center / OIDC:**
- IAM SSO authorization-code login - admin-triggered login flow for AWS Identity Center accounts.
  - SDK/Client: Go standard library `net/http` plus PKCE helpers in `auth/iam_sso.go`.
  - Endpoints: `https://oidc.{region}.amazonaws.com/client/register`, `/authorize`, and `/token` in `auth/iam_sso.go`.
  - Auth: registered OIDC `clientId` and `clientSecret`; stored per account as `ClientID` and `ClientSecret` in `config.Account`.
  - Callback: user supplies a `callbackUrl` to local admin endpoint `POST /admin/api/auth/iam-sso/complete`, handled by `proxy/handler.go`.

**AWS Builder ID device login:**
- Builder ID device authorization - admin-triggered device-code login for Builder ID accounts.
  - SDK/Client: Go standard library `net/http` in `auth/builderid.go`.
  - Endpoints: `https://oidc.{region}.amazonaws.com/client/register`, `/device_authorization`, and `/token` in `auth/builderid.go`.
  - Auth: device code polling returns access/refresh tokens and OIDC client credentials, stored through account config in `config/config.go`.

**AWS SSO token import:**
- SSO bearer-token import - imports an existing `x-amz-sso_authn`-style bearer token into a Kiro account.
  - SDK/Client: Go standard library `net/http` in `auth/sso_token.go`.
  - Endpoints: `https://portal.sso.us-east-1.amazonaws.com/token/whoAmI`, `/session/device`, and OIDC device authorization endpoints in `auth/sso_token.go`.
  - Auth: bearer token is submitted to `POST /admin/api/auth/sso-token` in `proxy/handler.go`; the token is not a repo env var.

**Kiro desktop social token refresh:**
- Kiro desktop auth refresh - refreshes social login tokens such as GitHub/Google-backed accounts.
  - SDK/Client: Go standard library `net/http` in `auth/oidc.go`.
  - Endpoint: `https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken` in `auth/oidc.go`.
  - Auth: per-account refresh token stored as `config.Account.RefreshToken` in `config/config.go`.

**Kiro MCP web search:**
- Kiro MCP web search - diagnostic/native web search support when client requests compatible web search tool behavior.
  - SDK/Client: Go standard library `net/http` in `proxy/handler.go`.
  - Endpoints: `https://q.{region}.amazonaws.com/mcp` and `https://codewhisperer.{region}.amazonaws.com/mcp` in `callKiroMCPWebSearch` in `proxy/handler.go`.
  - Auth: per-account access token and Kiro headers from `config.Account`.

**GitHub / GitHub Container Registry:**
- GHCR image publishing - CI builds and publishes Docker images.
  - SDK/Client: GitHub Actions official actions in `.github/workflows/docker.yml`.
  - Auth: `${{ secrets.GITHUB_TOKEN }}` for `docker/login-action@v3`.
  - Registry: `ghcr.io/${{ github.repository }}` from `.github/workflows/docker.yml`.

**Version-check fetch from admin UI:**
- GitHub raw content - admin page checks upstream version metadata.
  - SDK/Client: browser `fetch` from `web/index.html`.
  - Endpoint: `https://raw.githubusercontent.com/Quorinex/Kiro-Go/main/version.json` in `web/index.html`.
  - Auth: none detected.

## Data Storage

**Databases:**
- Not detected for the Kiro-Go application.
  - Connection: Not applicable.
  - Client: Not detected; no ORM or database driver appears in `go.mod`.
  - Persistence is a local JSON file managed by `config/config.go`, not a database.

**File Storage:**
- Local filesystem only.
  - Main config and account state: `data/config.json`, path selected by `CONFIG_PATH` in `main.go` and read/written by `config/config.go`.
  - Static admin UI: `web/index.html`, served by `proxy/handler.go`.
  - Docker persistence: `docker-compose.yml` mounts `./data:/app/data`; `Dockerfile` declares `VOLUME /app/data`.
  - Recovery/backup artifacts exist under `recovery/`, `.uat-backups/`, and `docs/superpowers/uat/`, but they are not runtime storage backends.

**Caching:**
- In-memory only.
  - Account pool, health state, and routing decisions are in `pool/account.go` and `pool/breaker.go`.
  - HTTP clients are cached in `auth/http_client.go` and `proxy/kiro.go`.
  - Model cache and request log state are implemented in `proxy/cache_tracker.go`, `proxy/request_log.go`, `config/config.go`, and related handler code.
  - Redis or memcached are not detected in application code or `docker-compose.yml`.

## Authentication & Identity

**Auth Provider:**
- Local admin authentication - custom password-based auth.
  - Implementation: `X-Admin-Password` header or admin cookie checked by `proxy/handler.go`; password persisted in `config.Config.Password` and overrideable with `ADMIN_PASSWORD` in `main.go`.
- Local client API authentication - custom bearer/API-key auth.
  - Implementation: `Authorization: Bearer ...`, `X-Api-Key`, or `x-api-key` values checked by `validateApiKey`/`validateClientAccess` in `proxy/handler.go`.
  - Configuration: `apiKey`, `clientApiKeys`, `requireApiKey`, and `clientIPAllowlist` in `config.Config` in `config/config.go`.
- Upstream account authentication - AWS/Kiro OAuth.
  - Implementation: IAM SSO in `auth/iam_sso.go`, Builder ID in `auth/builderid.go`, SSO token import in `auth/sso_token.go`, and token refresh in `auth/oidc.go`.

## Monitoring & Observability

**Error Tracking:**
- None detected as an external service.

**Logs:**
- Local stdout/stderr logging through `logger/logger.go`; level comes from `LOG_LEVEL` or `logLevel` config.
- Request logs are maintained in application memory/config surfaces by `proxy/request_log.go` and exposed through admin API handlers in `proxy/handler.go`.
- Health endpoint is local `GET /health` and `/`, handled by `proxy/handler.go`; Docker health check uses it in `docker-compose.yml`.
- Admin readiness/diagnostic endpoints include Claude Code compatibility, fleet readiness, account diagnostics, request logs, and web search diagnostics in `proxy/handler.go`.

## CI/CD & Deployment

**Hosting:**
- Docker container is the detected deployment unit.
- Published image target is GitHub Container Registry through `.github/workflows/docker.yml`.
- Local deployment path is `docker-compose.yml` with service `kiro-go` on port `8080`.

**CI Pipeline:**
- GitHub Actions.
  - Workflow: `.github/workflows/docker.yml`.
  - Triggers: pushes to `main`, `master`, `dev`; tags matching `v*`; pull requests to those branches; manual dispatch.
  - Actions: `actions/checkout@v4`, `docker/setup-qemu-action@v3`, `docker/setup-buildx-action@v3`, `docker/login-action@v3`, `docker/metadata-action@v5`, and `docker/build-push-action@v6`.
  - Platforms: `linux/amd64` and `linux/arm64`.

## Environment Configuration

**Required env vars:**
- None strictly required for a default local start; missing config is created with defaults by `config.Init` and `config.Load` in `config/config.go`.

**Common env vars:**
- `CONFIG_PATH` - config file path, default `data/config.json`, used by `main.go`.
- `ADMIN_PASSWORD` - startup admin password override, used by `main.go`.
- `LOG_LEVEL` - log level override, used by `logger/logger.go`.
- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and lowercase variants - outbound proxy support in `auth/http_client.go` and `proxy/kiro.go`.

**Secrets location:**
- Runtime secrets are persisted in `data/config.json` by default via `config/config.go`.
- Account-level secrets include `accessToken`, `refreshToken`, `clientSecret`, `apiKey`, `clientApiKeys`, and optional proxy credentials represented in `config.Account` and `config.Config` in `config/config.go`.
- CI registry secret is `${{ secrets.GITHUB_TOKEN }}` in `.github/workflows/docker.yml`.
- No `.env` file detected in the repo root during this scan.

## Webhooks & Callbacks

**Incoming:**
- No third-party webhook receiver is detected.
- Local API endpoints exposed by `proxy/handler.go`:
  - `POST /v1/messages`, `/messages`, `/anthropic/v1/messages` for Anthropic-compatible messages.
  - `POST /v1/messages/count_tokens`, `/messages/count_tokens` for local token estimates.
  - `POST /v1/chat/completions`, `/chat/completions` for OpenAI-compatible chat.
  - `POST /v1/responses`, `/responses` for OpenAI-compatible responses.
  - `GET /v1/models`, `/models` for model metadata.
  - `GET /v1/stats` for authenticated runtime stats.
  - `POST /api/event_logging/batch` as a local Claude Code telemetry sink.
  - `GET /health` and `/` for health checks.
  - `/admin` and `/admin/api/*` for the admin console and account/config operations.
- OAuth completion is manual/local: `POST /admin/api/auth/iam-sso/complete` receives a user-provided `callbackUrl` parsed by `auth/iam_sso.go`.

**Outgoing:**
- AWS/Kiro auth and generation HTTP calls in `auth/*.go`, `proxy/kiro.go`, `proxy/kiro_api.go`, and `proxy/handler.go`.
- GitHub raw version metadata fetch from `web/index.html`.
- No outgoing webhook registration or callback delivery is detected.

---

*Integration audit: 2026-05-21*
