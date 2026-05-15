# Coding Conventions

**Analysis Date:** 2026-05-15

## Naming Patterns

**Files:**
- Use lowercase package-oriented Go file names with underscores for multi-word concepts: `proxy/account_refresh.go`, `proxy/cache_tracker.go`, `auth/http_client.go`.
- Keep tests co-located with the code they exercise and use the same base name plus `_test.go`: `proxy/account_refresh_test.go`, `proxy/cache_tracker_test.go`, `auth/http_client_test.go`.
- Use package directories as the main module boundary: `auth/`, `config/`, `logger/`, `pool/`, `proxy/`.

**Functions:**
- Exported functions and methods use PascalCase and should be documented when they are part of package API: `config.GenerateMachineId` in `config/config.go:22`, `logger.ParseLevel` in `logger/logger.go:44`, `pool.GetPool` in `pool/account.go:69`.
- Unexported helpers use camelCase and often encode behavior directly in the name: `normalizeAutoRefreshConfigForUpdate` in `config/config.go:256`, `validateClaudeRequestShape` in `proxy/handler.go:89`, `resolveClaudeThinkingMode` in `proxy/translator.go:80`.
- Boolean helpers should read as predicates or capability checks: `allowReasoningSource` and `allowTagSource` in `proxy/handler.go:71`.

**Variables:**
- Package-level state uses short, descriptive lowerCamelCase names grouped in `var (...)` blocks: `currentLevel` in `logger/logger.go:31`, `poolOnce` in `pool/account.go:64`, and test-overridable hooks in `proxy/handler.go:22`.
- Constants use PascalCase when exported and lowerCamelCase when package-private: `AutoRefreshScopeEnabled` in `config/config.go:103`, `overageFrequencyScale` in `pool/account.go:13`, `minimalFallbackUserContent` in `proxy/translator.go:49`.
- Test locals use concise names such as `got`, `want`, `req`, `resp`, `account`, and `result`, as shown in `pool/account_test.go:58` and `proxy/kiro_api_test.go:21`.

**Types:**
- Exported types use PascalCase and model domain concepts: `config.Account` in `config/config.go:34`, `pool.AccountPool` in `pool/account.go:52`, `proxy.ClaudeRequest` in `proxy/translator.go:100`.
- JSON DTO structs carry explicit `json` tags and `omitempty` where fields are optional: `config.Account` in `config/config.go:34`, `proxy.ClaudeRequest` in `proxy/translator.go:100`.
- Internal enum-like types should be named types over `string` or `int` with grouped constants: `pool.FailureReason` in `pool/account.go:16`, `proxy.thinkingStreamSource` in `proxy/handler.go:63`.

## Code Style

**Formatting:**
- Use `gofmt` formatting for all Go files. `gofmt -l` returned no files during this scan.
- Imports follow Go standard grouping: standard library imports first, internal module imports such as `kiro-go/config` next, external dependencies after a blank line, as in `proxy/handler.go:3` and `proxy/translator.go:3`.
- Prefer simple control flow with early returns for validation and error paths: `config.ValidateAutoRefreshConfig` in `config/config.go:267`, `pool.ClassifyFailureReason` in `pool/account.go:28`.

**Linting:**
- Dedicated lint configuration is not detected. No `.golangci.yml`, `Makefile`, `Taskfile.yml`, or `justfile` is present.
- Use Go compiler, `gofmt`, and `go test ./...` as the effective quality gate.
- Do not introduce formatting that requires non-standard tooling.

## Import Organization

**Order:**
1. Go standard library packages, alphabetized by `gofmt`: `encoding/json`, `net/http`, `sync`, `time`.
2. Local module packages: `kiro-go/auth`, `kiro-go/config`, `kiro-go/logger`, `kiro-go/pool`.
3. Third-party packages after a blank line: `github.com/google/uuid` in `proxy/handler.go:19` and `proxy/translator.go:11`.

**Path Aliases:**
- Go module path is `kiro-go` in `go.mod:1`; import internal packages with `kiro-go/<package>`.
- No TypeScript-style or build-tool path aliases are used.

## Error Handling

**Patterns:**
- Return `error` values from operations that can fail; wrap lower-level failures with context using `fmt.Errorf(...: %w, err)` when the caller needs causality, as in auth flows under `auth/builderid.go` and `auth/oidc.go`.
- Validation functions return `nil` on success and descriptive `fmt.Errorf` messages on failure: `config.ValidateAutoRefreshConfig` in `config/config.go:267`, `config.ValidateHealthCheckConfig` in `config/config.go:324`.
- Request-shape validators in `proxy/handler.go` return an empty string for success and a client-facing error message for rejection: `validateClaudeRequestShape` in `proxy/handler.go:89`.
- Preserve upstream HTTP status/body context when surfacing proxy errors; tests assert this in `proxy/kiro_test.go` with `TestCallKiroAPIRetainsTooManyRequestsBody`.
- Use typed or interface-based behavior for special error metadata, such as `RateLimitResetAt() time.Time` checked in `proxy/handler.go:179` and tested in `proxy/kiro_test.go`.

## Logging

**Framework:** Custom `logger` package using Go `log`

**Patterns:**
- Application code should use `logger.Debugf`, `logger.Infof`, `logger.Warnf`, `logger.Errorf`, or `logger.Fatalf` from `logger/logger.go:109` instead of direct `fmt.Println`.
- Startup uses `logger.Init(config.GetLogLevel())` after config load in `main.go:46`, with `LOG_LEVEL` override support in `logger/logger.go:93`.
- Use `logger.Fatalf` only for unrecoverable process startup/server failures, as in `main.go:67`.
- Tests can redirect logger output through `logger.SetOutput` in `logger/logger.go:85`.

## Comments

**When to Comment:**
- Add package comments for public packages and exported API responsibilities: `config/config.go:1`, `logger/logger.go:1`, `main.go:1`.
- Use struct field comments when persisted or externally visible JSON fields have operational meaning: `config.Account` in `config/config.go:34`.
- Add short comments for non-obvious protocol behavior, compatibility, or concurrency rules: ordered model matching in `proxy/translator.go:14`, runtime stats locking in `proxy/handler.go:39`.
- Existing comments are bilingual English/Chinese. Preserve nearby style, but prefer concise English for new cross-cutting code unless editing a Chinese-commented block.

**JSDoc/TSDoc:**
- Not applicable. This is a Go codebase.

## Function Design

**Size:** Keep pure helpers small and focused where possible, following `pool.ClassifyFailureReason` in `pool/account.go:28` and `config.ValidateAutoRefreshConfig` in `config/config.go:267`. Larger HTTP and translation flows live in `proxy/handler.go`, `proxy/kiro.go`, and `proxy/translator.go`; add new behavior through small helpers when it can be tested independently.

**Parameters:** Pass pointers for mutable domain objects and larger request structs (`*config.Account`, `*ClaudeRequest`, `*Handler`). Pass values for small config structs when normalizing or validating (`config.AutoRefreshConfig`, `config.HealthCheckConfig`).

**Return Values:** Use Go idioms: `(value, error)` for fallible operations, `error` for commands, and simple booleans for admission/predicate helpers. For validators that feed HTTP responses, return a message string where an empty string means valid, matching `validateOpenAIRequestShape` in `proxy/handler.go`.

## Module Design

**Exports:** Export only package APIs needed by other packages or external handlers. Keep implementation helpers unexported, especially protocol conversion internals in `proxy/translator.go` and scheduling helpers in `proxy/account_refresh.go`.

**Barrel Files:** Not applicable. Go packages expose identifiers directly; no barrel/export index files are used.

---

*Convention analysis: 2026-05-15*
