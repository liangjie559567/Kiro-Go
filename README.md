<!-- generated-by: gsd-doc-writer -->
# Kiro-Go

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat&logo=docker)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Kiro-Go is a Go reverse proxy that exposes Kiro accounts through Anthropic-compatible and OpenAI-compatible API surfaces, with multi-account routing, Claude Code compatibility, and a browser admin console.

[English](README.md) | [中文](README_CN.md)

## Installation

Clone the repository and build the Go binary:

```bash
git clone https://github.com/liangjie559567/Kiro-Go.git
cd Kiro-Go
go build -o kiro-go .
```

Or run it with Docker Compose:

```bash
git clone https://github.com/liangjie559567/Kiro-Go.git
cd Kiro-Go
mkdir -p data
docker-compose up -d
```

## Quick Start

1. Start the service:

   ```bash
   ADMIN_PASSWORD=your_secure_password ./kiro-go
   ```

2. Open the admin console:

   ```text
   http://localhost:8080/admin
   ```

3. Add at least one Kiro account in the admin console.

4. Send an API request to a compatible endpoint such as `POST /v1/messages` or `POST /v1/chat/completions`.

By default, the service binds to `0.0.0.0:8080` and stores runtime configuration under `data/config.json`.

## Usage Examples

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

## Supported Endpoints

| Surface | Endpoint | Notes |
|---|---|---|
| Anthropic Messages | `POST /v1/messages` | Also accepts `/messages` and `/anthropic/v1/messages`. |
| Anthropic Count Tokens | `POST /v1/messages/count_tokens` | Also accepts `/messages/count_tokens`; estimates token counts locally. |
| OpenAI Chat Completions | `POST /v1/chat/completions` | Also accepts `/chat/completions`. |
| OpenAI Responses | `POST /v1/responses` | Also accepts `/responses`. |
| Models | `GET /v1/models` | Also accepts `/models`. |
| Stats | `GET /v1/stats` | Requires the same client access checks as generation APIs. |
| Health | `GET /health` and `/` | Returns service health. |
| Claude Code telemetry sink | `POST /api/event_logging/batch` | Returns a local OK response for Claude Code telemetry calls. |

## Claude Code Setup

Use the admin compatibility endpoint to retrieve the current recommended Claude Code environment values:

```bash
curl http://localhost:8080/admin/api/claude-code/compat \
  -H "X-Admin-Password: your_admin_password"
```

The response includes `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, and compatibility toggles such as `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY`.

## Docker

The Dockerfile builds a static Go binary in a `golang:1.21-alpine` builder image, installs the Kiro CLI in the runtime image, exposes port `8080`, and stores runtime state under `/app/data`.

```bash
docker compose up -d
docker compose exec kiro-go kiro-cli --version
```

The Compose service maps `./data` to `/app/data`, sets `CONFIG_PATH` to the container config file under `/app/data`, and defines a health check against `http://127.0.0.1:8080/health`.

## Project Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Getting Started](docs/GETTING-STARTED.md)
- [Development](docs/DEVELOPMENT.md)
- [Testing](docs/TESTING.md)
- [Configuration](docs/CONFIGURATION.md)
- [Deployment](docs/DEPLOYMENT.md)

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
