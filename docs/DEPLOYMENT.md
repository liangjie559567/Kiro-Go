<!-- generated-by: gsd-doc-writer -->
# Deployment

## Deployment Targets

Kiro-Go can be deployed as:

- A compiled Go binary built from `main.go`.
- A Docker container built from the repository `Dockerfile`.
- A Docker Compose service using `docker-compose.yml`.

The runtime service listens on the configured `host` and `port`; defaults are `0.0.0.0` and `8080`.

## Docker Compose Deployment

The repository includes a Compose service named `kiro-go`:

```bash
mkdir -p data
docker-compose up -d
```

The Compose file:

- Builds from the local Dockerfile.
- Maps host port `8080` to container port `8080`.
- Mounts `./data` at `/app/data`.
- Sets `CONFIG_PATH` to the container config file under `/app/data`.
- Sets `KIRO_CLI_HOME=/app/data/kiro-cli`.
- Checks `http://127.0.0.1:8080/health`.
- Uses `restart: unless-stopped`.

## Docker Image Behavior

The Dockerfile uses a multi-stage build:

1. A `golang:1.21-alpine` builder downloads Go modules and builds `kiro-go`.
2. An `alpine:latest` runtime installs `ca-certificates`, `curl`, and `unzip`.
3. The runtime image downloads the Kiro CLI for `amd64` or `arm64`, links `kiro-cli` and `kiro`, copies the Go binary and `web/` assets, exposes port `8080`, and declares `VOLUME /app/data`.

`KIRO_CLI_HOME` is set to `/app/data/kiro-cli` in the image so CLI state can persist with the data volume.

## Binary Deployment

Build and run the binary:

```bash
go build -o kiro-go .
CONFIG_PATH=/opt/kiro-go/config.json ADMIN_PASSWORD=your_secure_password ./kiro-go
```

Ensure the process can create the parent directory for `CONFIG_PATH`, because `main.go` creates that directory before loading configuration.

## Required Runtime State

Persist the directory containing the runtime JSON config. For Docker and Compose, that is `/app/data`.

Do not bake local credentials or generated `data/config.json` into a container image. Account records can include OAuth access tokens, refresh tokens, client secrets, profile ARNs, and usage metadata.

## Security Checklist

- Set `ADMIN_PASSWORD` for every deployment.
- Enable client API key validation when exposing public API endpoints outside a trusted network.
- Use `clientIPAllowlist` when only known clients should reach the proxy.
- Protect the mounted data directory because it contains token-bearing account configuration.
- Avoid logging or sharing `data/config.json`.
- Configure `proxyURL` or per-account `proxyURL` only with trusted proxy endpoints.

## Health Checks

The service exposes health checks at:

```text
GET /health
GET /
```

The Compose health check uses:

```text
http://127.0.0.1:8080/health
```

## CI And Image Publishing

`.github/workflows/docker.yml` builds Docker images on pushes to `main`, `master`, and `dev`, pull requests targeting those branches, tags matching `v*`, and manual workflow dispatches. It builds `linux/amd64` and `linux/arm64` images with Docker Buildx and pushes only when the event is not a pull request.

<!-- VERIFY: Published image tags and registry package visibility depend on the GitHub repository and workflow runtime permissions. -->

## Rollback

Kiro-Go stores runtime settings separately from the binary or container image. To roll back:

1. Stop the current process or container.
2. Start the previous binary or image with the same persistent data directory.
3. Check `GET /health`.
4. Open `/admin` and verify accounts, settings, and request logs.

If a configuration edit caused the issue, restore a previous copy of the JSON config before starting the service.
