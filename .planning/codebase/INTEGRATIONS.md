# External Integrations

**Analysis Date:** 2026-05-15

## APIs & External Services

**Kiro / AWS AI Runtime:**
- Kiro IDE generation endpoint - primary backend for translated Claude/OpenAI requests.
  - Endpoint: `https://q.us-east-1.amazonaws.com/generateAssistantResponse`
  - Implementation: `proxy/kiro.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: per-account OAuth bearer token from `config.Account.AccessToken` in `config/config.go`
- CodeWhisperer generation endpoint - fallback or preferred backend depending on endpoint settings.
  - Endpoint: `https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse`
  - Implementation: `proxy/kiro.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: per-account OAuth bearer token from `config.Account.AccessToken`
- Amazon Q generation mode - uses the Q endpoint with Amazon Q target headers.
  - Endpoint: `https://q.us-east-1.amazonaws.com/generateAssistantResponse`
  - Implementation: `proxy/kiro.go` and `proxy/kiro_headers.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: per-account OAuth bearer token from `config.Account.AccessToken`

**Kiro / AWS REST Metadata:**
- Usage limits and user info - refreshes account profile, subscription, quota, trial, and user metadata.
  - Endpoint: `https://codewhisperer.us-east-1.amazonaws.com/getUsageLimits` in `proxy/kiro_api.go`
  - Endpoint: `https://q.us-east-1.amazonaws.com/getUsageLimits` in `auth/sso_token.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: per-account OAuth bearer token
- User info - retrieves account identity metadata.
  - Endpoint: `https://codewhisperer.us-east-1.amazonaws.com/GetUserInfo`
  - Implementation: `proxy/kiro_api.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: per-account OAuth bearer token
- Available models and profiles - populates model cache and profile ARN routing data.
  - Endpoints: `https://codewhisperer.us-east-1.amazonaws.com/ListAvailableModels`, `https://codewhisperer.us-east-1.amazonaws.com/ListAvailableProfiles`
  - Implementation: `proxy/kiro_api.go`, `proxy/handler.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: per-account OAuth bearer token

**Kiro MCP:**
- Kiro MCP web search - implements Claude native web search tool responses by calling Kiro MCP.
  - Endpoints: `https://q.us-east-1.amazonaws.com/mcp`, `https://codewhisperer.us-east-1.amazonaws.com/mcp`
  - Implementation: `proxy/handler.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: per-account OAuth bearer token plus Kiro-compatible headers

**Authentication Services:**
- AWS OIDC - registers clients, starts device authorization, exchanges authorization codes, polls device codes, and refreshes IdC tokens.
  - Endpoints: `https://oidc.{region}.amazonaws.com/client/register`, `/authorize`, `/device_authorization`, `/token`
  - Implementation: `auth/builderid.go`, `auth/iam_sso.go`, `auth/oidc.go`, `auth/sso_token.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: OIDC client credentials, refresh token, device code, or authorization code stored in `config.Account`
- AWS SSO Portal - imports accounts from `x-amz-sso_authn` bearer tokens.
  - Endpoints: `https://portal.sso.us-east-1.amazonaws.com/token/whoAmI`, `/session/device`
  - Implementation: `auth/sso_token.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: user-provided bearer token submitted to `/admin/api/auth/sso-token`
- Kiro social auth - refreshes social-provider accounts such as Google/GitHub/AWS Builder ID.
  - Endpoint: `https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken`
  - Implementation: `auth/oidc.go`
  - SDK/Client: Go standard library `net/http`
  - Auth: `config.Account.RefreshToken`

**Container Registry:**
- GitHub Container Registry - publishes Docker images.
  - Registry: `ghcr.io`
  - Implementation: `.github/workflows/docker.yml`
  - SDK/Client: Docker Buildx GitHub Actions
  - Auth: `secrets.GITHUB_TOKEN` in GitHub Actions

**Outbound Proxy:**
- HTTP, HTTPS, SOCKS5, and SOCKS5H proxies can be configured globally or per account.
  - Implementation: `config/config.go`, `auth/http_client.go`, `proxy/kiro.go`, `proxy/handler.go`
  - SDK/Client: Go `http.Transport.Proxy`
  - Auth: optional proxy credentials embedded in configured proxy URLs

## Data Storage

**Databases:**
- Local JSON configuration store.
  - Connection: `CONFIG_PATH` environment variable or default `data/config.json`
  - Client: custom JSON load/save layer in `config/config.go`
  - Contents: admin password, API key settings, accounts, OAuth tokens, refresh tokens, client secrets, usage stats, endpoint preferences, prompt filters, auto-refresh settings, health-check settings, and proxy settings
- No SQL, NoSQL, or external database service is detected.

**File Storage:**
- Local filesystem only.
- Persistent data path: `data/config.json` by default; `/app/data/config.json` in Docker Compose via `docker-compose.yml`.
- Admin UI static assets are served from `web/index.html` through `proxy/handler.go`.
- Recovery candidate JSON files exist under `recovery/`; these were not read because they may contain account credential material.

**Caching:**
- In-memory model cache in `proxy/handler.go` (`cachedModels`, per-account model lists in `pool/account.go`).
- In-memory prompt cache/account billing estimation support in `proxy/cache_tracker.go` and `proxy/handler.go`.
- In-memory OAuth login sessions in `auth/builderid.go` and `auth/iam_sso.go`.
- In-memory per-proxy HTTP client caches in `auth/http_client.go` and `proxy/kiro.go`.
- No external cache service is detected.

## Authentication & Identity

**Auth Provider:**
- Local admin authentication uses a password checked from `X-Admin-Password` or `admin_password` cookie in `proxy/handler.go`.
  - Password source: persisted config in `data/config.json`, overridden at startup by `ADMIN_PASSWORD` in `main.go`
- Local API authentication is optional bearer/API-key validation in `proxy/handler.go`.
  - Client headers: `Authorization: Bearer ...` or `X-Api-Key`
  - Settings: `apiKey` and `requireApiKey` persisted by `config.UpdateSettings` in `config/config.go`
- External account identity uses AWS Builder ID, IAM Identity Center, SSO token import, social-provider refresh tokens, local cache import, and credentials JSON import.
  - Implementation: `auth/builderid.go`, `auth/iam_sso.go`, `auth/sso_token.go`, `auth/oidc.go`, `proxy/handler.go`, `web/index.html`
  - Stored credentials: `accessToken`, `refreshToken`, `clientId`, `clientSecret`, `region`, `profileArn`, and `authMethod` fields in `config.Account`

## Monitoring & Observability

**Error Tracking:**
- None external.
- Errors are logged through the local logger in `logger/logger.go` and returned as JSON API errors in `proxy/handler.go`.

**Logs:**
- Local stdout/stderr logging through `logger/logger.go`.
- Log level is configured by `LOG_LEVEL` or `logLevel` in `data/config.json`.
- Runtime counters are maintained in memory and persisted through `config.UpdateStats` in `config/config.go`.
- Health endpoint: `/health` in `proxy/handler.go`.
- Stats endpoint: `/v1/stats` in `proxy/handler.go`, protected by the optional API key mechanism.

## CI/CD & Deployment

**Hosting:**
- Docker container deployment is first-class through `Dockerfile` and `docker-compose.yml`.
- Published image target is GitHub Container Registry (`ghcr.io/quorinex/kiro-go:latest`) as documented in `README.md` and configured in `.github/workflows/docker.yml`.
- The service exposes port `8080` in `Dockerfile` and maps `8080:8080` in `docker-compose.yml`.

**CI Pipeline:**
- GitHub Actions workflow in `.github/workflows/docker.yml`.
- Triggers: pushes to `main`, `master`, and `dev`; `v*` tags; pull requests to `main`, `master`, and `dev`; manual workflow dispatch.
- Actions used: `actions/checkout@v4`, `docker/setup-qemu-action@v3`, `docker/setup-buildx-action@v3`, `docker/login-action@v3`, `docker/metadata-action@v5`, `docker/build-push-action@v6`.
- Platforms: `linux/amd64`, `linux/arm64`.

## Environment Configuration

**Required env vars:**
- None strictly required for local development because defaults exist in `main.go` and `config/config.go`.

**Supported env vars:**
- `CONFIG_PATH` - config file path; default `data/config.json`.
- `ADMIN_PASSWORD` - startup override for admin password.
- `LOG_LEVEL` - logger verbosity override; accepted values are `debug`, `info`, `warn`, `error`.
- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and Go-supported proxy environment variables - honored when no explicit app proxy is configured.

**Secrets location:**
- Runtime secrets are persisted in `data/config.json` or the file selected by `CONFIG_PATH`; this includes account tokens, refresh tokens, client secrets, admin password, and API key settings.
- Docker deployment persists secrets under the mounted `/app/data` volume from `docker-compose.yml`.
- GitHub Actions registry publishing uses `secrets.GITHUB_TOKEN` in `.github/workflows/docker.yml`.
- No `.env` files were detected during the repository scan.

## Webhooks & Callbacks

**Incoming:**
- Client-compatible API endpoints are exposed by `proxy/handler.go`: `/v1/messages`, `/messages`, `/anthropic/v1/messages`, `/v1/messages/count_tokens`, `/v1/chat/completions`, `/chat/completions`, `/v1/models`, `/models`, `/v1/stats`, `/health`, and `/admin/api/...`.
- Admin auth import endpoints are exposed by `proxy/handler.go`: `/admin/api/auth/iam-sso/start`, `/admin/api/auth/iam-sso/complete`, `/admin/api/auth/builderid/start`, `/admin/api/auth/builderid/poll`, `/admin/api/auth/sso-token`, `/admin/api/auth/credentials`.
- No third-party webhook receiver is detected.

**Outgoing:**
- OAuth browser redirect URL for IAM SSO is `http://127.0.0.1/oauth/callback` in `auth/iam_sso.go`; the app expects the callback URL to be submitted back to `/admin/api/auth/iam-sso/complete`.
- Outbound callbacks/webhooks to user-defined services are not detected.
- Outbound API calls to AWS/Kiro services are implemented in `auth/` and `proxy/` using Go `net/http`.

---

*Integration audit: 2026-05-15*
