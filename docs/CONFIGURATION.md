<!-- generated-by: gsd-doc-writer -->
# Configuration

## Configuration Files

Kiro-Go stores runtime configuration as JSON. At startup, `main.go` uses `CONFIG_PATH` when it is set; otherwise it uses `data/config.json`. The parent directory is created before `config.Init` loads or creates the file.

Do not commit or paste local `data/config.json` contents into documentation or issues. The account fields in `config.Account` include OAuth access tokens, refresh tokens, client secrets, profile ARNs, proxy settings, and account metadata.

## Environment Variables

| Variable | Used by | Purpose |
|---|---|---|
| `CONFIG_PATH` | `main.go` | Overrides the runtime JSON config path. |
| `ADMIN_PASSWORD` | `main.go` | Overrides the admin password after config load. |
| `LOG_LEVEL` | `logger.Init(config.GetLogLevel())` and `config.GetLogLevel()` | Overrides configured log verbosity. Accepted values are `debug`, `info`, `warn`, and `error`. |
| `KIRO_CLI_HOME` | Docker runtime / Kiro CLI | Points Kiro CLI state at `/app/data/kiro-cli` in the container image and Compose service. |

## Server Settings

`config.Config` defines these server-level fields:

| JSON field | Default | Notes |
|---|---:|---|
| `password` | `changeme` | Admin API and UI password. Override with `ADMIN_PASSWORD` for deployment. |
| `port` | `8080` | HTTP server port used by `main.go`. |
| `host` | `0.0.0.0` | HTTP bind address used by `main.go`. |
| `apiKey` | empty | Legacy single client API key. |
| `requireApiKey` | `false` | Enables client API key enforcement when true. |
| `clientApiKeys` | empty | Additional accepted client API keys. |
| `clientIPAllowlist` | empty | Optional client IP allowlist. |
| `logLevel` | `info` | Stored log level when `LOG_LEVEL` is not set. |

## Account Configuration

Accounts are stored in the `accounts` array. Each account is represented by `config.Account` in `config/config.go` and includes:

- Identity fields: `id`, `email`, `userId`, `nickname`
- Auth fields: `accessToken`, `refreshToken`, `clientId`, `clientSecret`, `authMethod`, `provider`, `region`, `startUrl`, `expiresAt`, `machineId`, `profileArn`
- Routing fields: `enabled`, `weight`, `proxyURL`, `allowOverage`, `overageWeight`
- Health and cooldown fields: `banStatus`, `lastFailureReason`, `cooldownUntil`, `failureCount`
- Usage and subscription fields: `usageCurrent`, `usageLimit`, `usagePercent`, `nextResetDate`, trial fields, and counters

Use the admin UI or `/admin/api/*` endpoints to manage accounts instead of editing token-bearing JSON by hand.

## Routing And Admission

| JSON field | Default | Purpose |
|---|---|---|
| `loadBalance.strategy` | `health` | Account selection strategy. Valid values are `health`, `round_robin`, and `least_connections`. |
| `modelAdmission.streamBypass` | `false` | Controls stream bypass behavior for model admission. |
| `modelAdmission.models["claude-opus-4.7"]` | `maxConcurrent: 2`, `maxWaiting: 200` | Default admission rule derived from the legacy Opus 4.7 admission config. |
| `stableDownstream.enabled` | `true` | Enables stable downstream handling. |
| `stableDownstream.sub2apiCompatible` | `true` | Enables sub2api-compatible stable downstream behavior. |
| `stableDownstream.models` | `["claude-opus-4.7"]` | Models covered by stable downstream behavior. |
| `contentContinuity.enabled` | `true` | Enables content continuity checks. |
| `contentContinuity.models` | `["claude-opus-4.7"]` | Models covered by continuity checks. |
| `claudeCodeGovernor.enabled` | `false` | Enables the Claude Code session governor when set true. |

## Background Jobs

| JSON field | Default | Purpose |
|---|---|---|
| `autoRefresh.enabled` | `true` | Enables scheduled account refresh. |
| `autoRefresh.intervalMinutes` | `60` | Refresh interval; validation allows 5 to 1440 minutes. |
| `autoRefresh.scope` | `enabled` | Valid values are `enabled` and `all`. |
| `healthCheck.enabled` | `false` | Enables scheduled account health checks. |
| `healthCheck.intervalMinutes` | `60` | Health interval; validation allows 5 to 1440 minutes. |
| `healthCheck.autoDisableUnhealthy` | `false` | Controls whether unhealthy accounts are automatically disabled. |

## Protocol And Prompt Settings

| JSON field | Purpose |
|---|---|
| `thinkingSuffix` | Model suffix that triggers thinking mode. The default from `GetThinkingConfig` is `-thinking`. |
| `openaiThinkingFormat` | OpenAI thinking output field format. Defaults to `reasoning_content`. |
| `claudeThinkingFormat` | Claude thinking output field format. Defaults to `thinking`. |
| `preferredEndpoint` | Upstream endpoint preference. `GetPreferredEndpoint` defaults to `auto` when unset. |
| `endpointFallback` | Controls fallback to alternate endpoints. `GetEndpointFallback` defaults to true when unset. |
| `proxyURL` | Global outbound proxy for Kiro API requests. Per-account `proxyURL` can override it. |
| `filterClaudeCode` | Replaces Claude Code CLI system prompt with a compact backend prompt when enabled. |
| `filterEnvNoise` | Strips environment metadata noise from system prompts when enabled. |
| `filterStripBoundaries` | Removes system prompt boundary markers when enabled. |
| `promptFilterRules` | User-defined regex or line-containing prompt filter rules. |

## Admin Configuration Endpoints

Configuration is exposed through admin APIs under `/admin/api/*`, protected by `X-Admin-Password` or an `admin_password` cookie. Relevant endpoints include:

| Endpoint | Method | Purpose |
|---|---|---|
| `/admin/api/settings` | `GET`, `POST` | Client access, host, port, model mappings, model admission, and over-usage settings. |
| `/admin/api/auto-refresh` | `GET`, `POST` | Scheduled refresh settings. |
| `/admin/api/health-check` | `GET`, `POST` | Scheduled health-check settings. |
| `/admin/api/load-balance` | `GET`, `POST` | Load-balancing strategy. |
| `/admin/api/thinking` | `GET`, `POST` | Thinking mode settings. |
| `/admin/api/endpoint` | `GET`, `POST` | Preferred endpoint and endpoint fallback settings. |
| `/admin/api/proxy` | `GET`, `POST` | Global outbound proxy settings. |
| `/admin/api/prompt-filter` | `GET`, `POST` | Prompt filtering settings. |

## Docker Configuration

The Compose service maps the local `./data` directory to `/app/data` and sets:

```yaml
CONFIG_PATH: /app/data/config.json
KIRO_CLI_HOME: /app/data/kiro-cli
```

The Docker image also declares `VOLUME /app/data` and exposes port `8080`.

<!-- VERIFY: The externally published image name depends on repository owner and registry settings at release time. -->
