<!-- generated-by: gsd-doc-writer -->
# Contributing

## Getting Started

1. Fork or clone the repository.
2. Install Go `1.21` or newer.
3. Build the service:

   ```bash
   go build -o kiro-go .
   ```

4. Run tests:

   ```bash
   go test ./...
   ```

5. Start a local development server with an isolated config when needed:

   ```bash
   CONFIG_PATH="$(mktemp -d)/config.json" ADMIN_PASSWORD=dev ./kiro-go
   ```

## Development Guidelines

- Keep Go code formatted with `gofmt`.
- Keep changes focused and avoid unrelated formatting churn.
- Add or update tests beside the package being changed.
- Do not commit local runtime config, account credentials, recovery artifacts, or scratch UAT output.
- Treat `data/config.json` as secret-bearing runtime state.
- Update documentation when changing endpoints, configuration fields, deployment behavior, or operator workflows.

## Test Expectations

Run the full Go test suite before submitting a change:

```bash
go test ./...
```

For targeted iteration, run package-specific tests:

```bash
go test ./proxy -run TestName -count=1
```

Use fake HTTP transports, `httptest`, temporary config files, and `t.Setenv` for deterministic tests. Do not add tests that depend on live upstream Kiro services.

## Pull Requests

The repository does not define a pull request template. Include these details in a PR description:

- What changed and why.
- How the change was tested.
- Any configuration, deployment, or migration impact.
- Any known limitations or follow-up work.

## Documentation

Project docs live under `docs/`. Update the relevant file when behavior changes:

- `docs/ARCHITECTURE.md` for request flow or package boundary changes.
- `docs/CONFIGURATION.md` for config fields and admin settings.
- `docs/DEPLOYMENT.md` for Docker, Compose, CI, or runtime environment changes.
- `docs/TESTING.md` for test commands or conventions.
- `README.md` for user-facing setup and endpoint changes.

## License

By contributing, you agree that your contributions are provided under the repository license. See [LICENSE](LICENSE).
