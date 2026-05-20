# Kiro-Go

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat&logo=docker)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Convert Kiro accounts into OpenAI and Anthropic-compatible API services with a multi-account scheduler, Claude Code compatibility tooling, and an operator web console.

[English](README.md) | [中文](README_CN.md)

If this project helps you, a Star would mean a lot.

## What It Supports

### Compatible API endpoints

| Surface | Endpoint | Notes |
|---|---|---|
| Anthropic Messages | `POST /v1/messages` | Also accepts `/messages` and `/anthropic/v1/messages`; supports streaming and non-streaming responses. |
| Anthropic Count Tokens | `POST /v1/messages/count_tokens` | Local estimate for Claude Code compatibility; not an official upstream exact count. |
| OpenAI Chat Completions | `POST /v1/chat/completions` | Also accepts `/chat/completions`; supports tools and streaming. |
| OpenAI Responses | `POST /v1/responses` | Compatibility conversion layer for `instructions`, `input`, function tools, tool outputs, and `previous_response_id`. |
| Models | `GET /v1/models` | Aggregates cached account model lists and built-in aliases. |
| Stats | `GET /v1/stats` | Client-authenticated runtime statistics. |
| Health | `GET /health` and `/` | Container and uptime health checks. |
| Claude Code telemetry sink | `POST /api/event_logging/batch` | Returns OK so Claude Code gateway telemetry calls do not fail locally. |

### Gateway features

- Multi-account Kiro pool with health-based routing by default, plus `round_robin` and `least_connections` strategies.
- Per-account runtime health, model cache, quota/subscription metadata, temporary-limit cooldowns, and failure classification.
- Model mapping rules: alias, replacement, and weighted load-balance targets.
- Model admission control for high-pressure models, including Opus 4.7 concurrency limits, waiting queues, pressure scoring, and `Retry-After` hints.
- Stable downstream mode for sub2api-compatible Opus 4.7 generation requests: suppresses downstream `429`, `502`, and `503` failures and records the suppressed internal reason in request logs.
- Automatic OAuth token refresh, optional scheduled account refresh, and optional scheduled health checks.
- Prompt filtering for Claude Code system prompts, environment noise, boundary markers, and custom regex/line rules.
- Thinking mode via model suffix or Claude `thinking` config, with configurable Claude/OpenAI output formats.
- Global and per-account outbound proxy support.
- Client API key validation, multiple client keys, and client IP allowlist.
- Request logs with account, route decision, attempt trace, tool metadata, latency, cache usage, admission pressure, and stable fallback metadata.

### Claude Code compatibility

Kiro-Go is designed to work as an Anthropic-compatible backend for Claude Code. It accepts the request shapes Claude Code emits for:

- `tools`, `tool_use`, `tool_result`, `tool_choice`, and `tool_reference`
- MCP tool references, deferred/materialized tools, and large tool payloads
- image content blocks
- prompt cache controls and `max_tokens=0` cache-warmup-shaped requests
- fine-grained tool streaming compatibility events
- text assistant prefill as an emulated continuation instruction

Important boundaries:

- Kiro-Go does not start or manage local MCP servers. Claude Code remains the MCP host.
- `count_tokens` is estimated locally.
- `max_tokens=0` returns a local zero-output compatible response; it is not proof of official Anthropic cache-warmup parity.
- Text assistant prefill is emulated; final assistant `tool_use` prefill is rejected.
- Fine-grained tool streaming emits Claude Code-compatible events, but exact upstream partial JSON parity depends on Kiro stream behavior.

## Quick Start

### Docker Compose

```bash
git clone https://github.com/liangjie559567/Kiro-Go.git
cd Kiro-Go
mkdir -p data
docker-compose up -d
```

Open the admin panel:

```text
http://localhost:8080/admin
```

The default admin password is `changeme`. Override it with `ADMIN_PASSWORD` or change it in the admin panel before production use.

### Docker Run

```bash
docker run -d \
  --name kiro-go \
  -p 8080:8080 \
  -e ADMIN_PASSWORD=your_secure_password \
  -e CONFIG_PATH=/app/data/config.json \
  -v /path/to/data:/app/data \
  --restart unless-stopped \
  ghcr.io/liangjie559567/kiro-go:latest
```

### Build From Source

```bash
git clone https://github.com/liangjie559567/Kiro-Go.git
cd Kiro-Go
go build -o kiro-go .
ADMIN_PASSWORD=your_secure_password ./kiro-go
```

Configuration is created at `data/config.json` by default. Mount or back up `/app/data` for persistence.

## Basic Usage

Add accounts in the admin panel, then call compatible APIs.

### Anthropic Messages

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -H "Authorization: Bearer any" \
  -d '{
    "model": "claude-sonnet-4.5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### OpenAI Chat Completions

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### OpenAI Responses

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any" \
  -d '{
    "model": "gpt-4o",
    "instructions": "Be concise.",
    "input": "Explain Kiro-Go in one sentence."
  }'
```

## Claude Code Setup

Use the admin compatibility endpoint to get the current recommended environment:

```bash
curl http://localhost:8080/admin/api/claude-code/compat \
  -H "X-Admin-Password: your_admin_password"
```

Typical local setup:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=any
export ANTHROPIC_API_KEY=any
export ANTHROPIC_MODEL=claude-sonnet-4.5
export ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4.5
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
export CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING=1
export ENABLE_TOOL_SEARCH=true
```

Useful Claude Code diagnostics:

- `GET /admin/api/claude-code/compat`
- `GET /admin/api/claude-code/readiness`
- `GET /admin/api/claude-code/model-readiness?model=claude-opus-4-7`
- `GET /admin/api/fleet/readiness?model=claude-opus-4-7`
- `GET /admin/api/request-logs`

## Opus 4.7 And sub2api

Kiro-Go includes specific handling for Opus 4.7 because it is more likely to hit upstream capacity and temporary-limit behavior.

- `claude-opus-4-7`, `claude-opus-4.7`, dated suffixes, and thinking suffixes are normalized to Kiro's Opus 4.7 model.
- Claude Code Opus 4.7 requests are normalized to adaptive thinking and sampling parameters are dropped.
- Model admission control tracks pressure separately from account health.
- `GET /admin/api/fleet/readiness?model=claude-opus-4-7` reports `healthy`, `degraded`, or `blocked`, plus safe concurrency and retry timing.
- Stable downstream mode can keep sub2api-facing generation responses syntactically valid and HTTP `200` while Kiro-Go records the internally suppressed retryable failure.

Run the stable downstream UAT when Kiro-Go and sub2api are both available:

```bash
SUB2API_BASE_URL=http://127.0.0.1:18080 \
SUB2API_API_KEY="$SUB2API_API_KEY" \
ROUNDS=10 \
CONCURRENCY=10 \
node docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

## Admin Operations

The web admin panel and `/admin/api/*` endpoints support:

- Account CRUD, batch enable/disable/refresh, account test, and account export.
- IAM Identity Center login, Builder ID login, SSO token import, and credentials JSON import.
- Dry-run credential validation with `POST /admin/api/auth/credentials/validate`.
- Model cache refresh for one account or all accounts.
- Account diagnostics, scheduler preview, fleet readiness, WebSearch diagnostics, and admission pressure.
- Settings for API keys, client IP allowlist, model mappings, model admission, over-usage behavior, auto refresh, health check, load balancing, endpoint preference, proxy, thinking mode, and prompt filters.
- Request log browsing, request statistics, and stats reset.

Admin API calls use `X-Admin-Password` or the admin password cookie.

## Configuration

Main configuration is stored in `data/config.json`. Most settings are also editable in the admin panel.

| Area | Keys / UI |
|---|---|
| Server | `host`, `port`, `password` |
| Client access | `requireApiKey`, `apiKey`, `clientApiKeys`, `clientIPAllowlist` |
| Model routing | `modelMappings`, `loadBalance`, per-account `weight` |
| Admission | `modelAdmission`, legacy `opus47Admission` |
| Stable downstream | `stableDownstream.enabled`, `stableDownstream.sub2apiCompatible`, `stableDownstream.models` |
| Accounts | tokens, auth method, region, machine ID, profile ARN, usage, quota, cooldown, proxy |
| Background jobs | `autoRefresh`, `healthCheck` |
| Thinking | `thinkingSuffix`, `openaiThinkingFormat`, `claudeThinkingFormat` |
| Endpoint | `preferredEndpoint`, `endpointFallback` |
| Prompt filtering | `filterClaudeCode`, `filterEnvNoise`, `filterStripBoundaries`, `promptFilterRules` |
| Proxy | global `proxyURL` and per-account `proxyURL` |
| Logging | `logLevel` or `LOG_LEVEL` |

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `CONFIG_PATH` | Config file path | `data/config.json` |
| `ADMIN_PASSWORD` | Admin password override | Config value or `changeme` on first run |
| `LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | Config value or `info` |

## More Documentation

- [Claude Code compatibility matrix](docs/claude-code-compatibility-matrix.md)
- [High availability matrix](docs/kiro-ha-compatibility-matrix.md)
- [Kiro ecosystem operations](docs/kiro-ecosystem-operations.md)
- [sub2api Opus 4.7 Stable 200 UAT](docs/superpowers/uat/sub2api-opus47-stable-200/README.md)

## Contributing

Friendly discussion is welcome. If you run into issues, try asking Claude Code, Codex, or similar tools for help first. PRs with tests or UAT evidence are even better.

## Friend Links

- [LINUX DO](https://linux.do)

## Disclaimer

For educational and research purposes only. Not affiliated with Amazon, AWS, or Kiro. Users are responsible for complying with applicable terms of service and laws. Use at your own risk.

## License

[MIT](LICENSE)
