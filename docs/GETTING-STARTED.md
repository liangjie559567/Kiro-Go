<!-- generated-by: gsd-doc-writer -->
# Getting Started

## Prerequisites

- Go `1.21` or newer, as declared in `go.mod`.
- Git for cloning the repository.
- Docker and Docker Compose if you want to run the containerized service.
- A Kiro account to add through the admin console before serving real model traffic.

The Docker image builds from `golang:1.21-alpine` and installs the Kiro CLI in the runtime image.

## Installation Steps

1. Clone the repository:

   ```bash
   git clone https://github.com/liangjie559567/Kiro-Go.git
   cd Kiro-Go
   ```

2. Build the binary:

   ```bash
   go build -o kiro-go .
   ```

3. Create the runtime data directory:

   ```bash
   mkdir -p data
   ```

## First Run

Start the server locally:

```bash
ADMIN_PASSWORD=your_secure_password ./kiro-go
```

The service starts on `http://0.0.0.0:8080` by default. From the same machine, open:

```text
http://localhost:8080/admin
```

Add at least one account in the admin console, then call one of the compatible API endpoints such as `POST /v1/messages`.

## Docker First Run

Run the Compose service:

```bash
mkdir -p data
docker-compose up -d
```

Verify the bundled Kiro CLI:

```bash
docker compose exec kiro-go kiro-cli --version
```

The Compose file maps `./data` to `/app/data`, sets `CONFIG_PATH` to the container config file under `/app/data`, and checks health at `http://127.0.0.1:8080/health`.

## Common Setup Issues

| Issue | Fix |
|---|---|
| The admin console accepts the default password | Set `ADMIN_PASSWORD` before starting the service, or change the stored password through the admin UI. |
| Runtime configuration is lost after container recreation | Keep `./data:/app/data` mounted when using Docker Compose or mount another persistent directory to `/app/data`. |
| API requests return authentication errors | Check `requireApiKey`, `apiKey`, `clientApiKeys`, and `clientIPAllowlist` in the admin settings or runtime config. |
| No accounts are available for generation | Add and enable at least one account in the admin console, then test or refresh the account. |
| Port `8080` is already in use | Change the `port` config field or adjust the Compose port mapping. |

## Next Steps

- Read [Configuration](CONFIGURATION.md) for runtime config fields and admin settings.
- Read [Architecture](ARCHITECTURE.md) for request flow and package responsibilities.
- Read [Development](DEVELOPMENT.md) before changing code.
- Read [Testing](TESTING.md) before opening a pull request.
