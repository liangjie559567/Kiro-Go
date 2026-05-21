<!-- generated-by: gsd-doc-writer -->
# Development

## Local Setup

Fork or clone the repository, then build and run the Go service locally:

```bash
git clone https://github.com/liangjie559567/Kiro-Go.git
cd Kiro-Go
go build -o kiro-go .
ADMIN_PASSWORD=your_secure_password ./kiro-go
```

The default runtime config path is `data/config.json`. Use `CONFIG_PATH` when you need an isolated development config:

```bash
CONFIG_PATH="$(mktemp -d)/config.json" ADMIN_PASSWORD=dev ./kiro-go
```

## Build Commands

There is no Node package manifest, Makefile, or project-specific build script in the repository. Use the Go toolchain directly.

| Command | Description |
|---|---|
| `go build -o kiro-go .` | Build the service binary from `main.go`. |
| `go run .` | Run the service from source. |
| `go test ./...` | Run all Go tests. |
| `go test ./... -cover` | Run tests with a package-level coverage summary. |
| `docker-compose up -d` | Build and run the containerized service locally. |
| `docker compose exec kiro-go kiro-cli --version` | Verify the Kiro CLI inside the running container. |

## Code Style

The repository does not include a separate formatter or linter config. Follow standard Go conventions:

- Format Go files with `gofmt`.
- Keep Go tests next to implementation files using the standard Go test filename suffix.
- Prefer package-owned helpers over generic utility packages.
- Keep runtime credential data under `data/` out of commits and documentation.

The single admin frontend file is `web/index.html`. Keep UI changes consistent with the existing single-file HTML/CSS/JS structure unless the project intentionally adopts a frontend build pipeline.

## Branch Conventions

No branch naming convention is documented in the repository. The GitHub Actions Docker workflow runs on pushes and pull requests targeting `main`, `master`, and `dev`, and on tags matching `v*`.

## PR Process

No pull request template is present. A practical contribution flow for this repository is:

1. Keep the change focused on one behavior or documentation update.
2. Run `gofmt` on changed Go files.
3. Run `go test ./...`.
4. Include docs updates when routes, configuration fields, deployment behavior, or operator workflows change.
5. Avoid committing local runtime state from `data/`, recovery artifacts, screenshots, or UAT scratch output unless the file is intentionally part of project evidence.

## Where To Make Changes

| Change type | Primary files |
|---|---|
| Public API route | `proxy/handler.go`, with tests in `proxy/handler_test.go`. |
| Protocol conversion | `proxy/translator.go`, with tests in `proxy/translator_test.go`. |
| Kiro streaming behavior | `proxy/kiro.go`, `proxy/kiro_headers.go`, and `proxy/kiro_test.go`. |
| Kiro REST behavior | `proxy/kiro_api.go` and `proxy/kiro_api_test.go`. |
| Account scheduling | `pool/account.go`, `pool/breaker.go`, and package tests under `pool/`. |
| Runtime configuration | `config/config.go` and `config/config_test.go`. |
| Auth flows | `auth/*.go` and auth package tests. |
| Admin UI | `web/index.html` plus route tests if new admin APIs are added. |

## Documentation Updates

Update the generated project docs when changing stable user-facing behavior:

- [Configuration](CONFIGURATION.md) for config fields or admin setting routes.
- [Architecture](ARCHITECTURE.md) for package boundaries or request flow changes.
- [Testing](TESTING.md) for new test patterns or commands.
- [Deployment](DEPLOYMENT.md) for Docker, Compose, CI, or runtime environment changes.
