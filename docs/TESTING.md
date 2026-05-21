<!-- generated-by: gsd-doc-writer -->
# Testing

## Test Framework And Setup

Kiro-Go uses the Go standard `testing` package. The module targets Go `1.21` in `go.mod`; there is no external test framework, Jest/Vitest config, or repository-level coverage threshold.

Run all tests from the repository root:

```bash
go test ./...
```

## Test Commands

| Command | Purpose |
|---|---|
| `go test ./...` | Run all package tests. |
| `go test ./proxy -run TestName -count=1` | Run one proxy test or matching test group without cached results. |
| `go test ./... -cover` | Run all tests with coverage summaries. |

## Test Organization

Tests are co-located with package code:

```text
main_test.go
auth/*_test.go
config/*_test.go
pool/*_test.go
proxy/*_test.go
proxy/testdata/*.json
```

Important test areas include:

| Area | Files |
|---|---|
| Server startup | `main_test.go` |
| Config defaults and validation | `config/config_test.go` |
| Account routing, cooldowns, health, breakers | `pool/account_test.go`, `pool/breaker_test.go` |
| Protocol translation | `proxy/translator_test.go` |
| HTTP routing and admin behavior | `proxy/handler_test.go`, `proxy/request_log_test.go`, `proxy/ecosystem_ops_test.go` |
| Kiro upstream clients | `proxy/kiro_test.go`, `proxy/kiro_api_test.go`, `proxy/kiro_headers_test.go` |
| Claude Code compatibility | `proxy/compatibility_matrix_test.go`, `proxy/claude_sse_writer_test.go`, `proxy/claude_code_concurrency_governor_test.go` |
| Documentation matrix integrity | `proxy/compatibility_matrix_test.go`, `proxy/ha_matrix_test.go` |

## Mocking And Fakes

The repository uses standard-library fakes instead of a mocking framework.

- Use `httptest.NewRequest` and `httptest.NewRecorder` for HTTP handler tests.
- Use `t.TempDir()` and `config.Init` with a temp-directory config path for isolated config tests.
- Use `t.Setenv` for environment-dependent behavior.
- Use custom `http.RoundTripper` fakes such as `roundTripFunc` for upstream HTTP client tests.
- Restore package-level test hooks with `t.Cleanup` when replacing functions or client stores.

## Fixtures

Protocol fixtures live under `proxy/testdata/`:

- `proxy/testdata/claude_code_2_1_143_wire_request.json`
- `proxy/testdata/claude_code_tool_reference_message.json`

Compatibility and operations matrix tests read JSON files under `docs/`, including:

- `docs/claude-code-compatibility-matrix.json`
- `docs/kiro-ha-compatibility-matrix.json`
- `docs/kiro-ecosystem-operations-matrix.json`

## Adding Tests

- Add pure conversion tests in `proxy/translator_test.go`.
- Add route, status code, and response body tests in `proxy/handler_test.go` or a focused proxy package test file.
- Add account routing tests in `pool/account_test.go`.
- Add config persistence and validation tests in `config/config_test.go`.
- Add upstream-client tests with fake transports in `proxy/kiro_test.go` or `proxy/kiro_api_test.go`.

Avoid live Kiro upstream calls in unit tests. Use fake transports and local handlers so tests stay deterministic.

## Manual And UAT Evidence

The `docs/superpowers/uat/` tree contains manual, browser, load, and full-stack evidence from prior work. Those files are not part of `go test ./...`; treat them as historical evidence or scenario-specific validation material.
