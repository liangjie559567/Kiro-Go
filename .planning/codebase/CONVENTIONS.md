# Coding Conventions

**Analysis Date:** 2026-05-21

## Naming Patterns

**Files:**
- Use Go package directories as the primary module boundary: `auth/`, `config/`, `logger/`, `pool/`, and `proxy/`.
- Use lower_snake_case file names for multi-word Go files in feature-heavy packages: `proxy/claude_sse_writer.go`, `proxy/request_classifier.go`, `proxy/account_refresh.go`, `proxy/content_continuity.go`.
- Use short package topic names for central files: `main.go`, `config/config.go`, `pool/account.go`, `proxy/handler.go`, `proxy/translator.go`.
- Use `_test.go` beside the implementation under test: `proxy/translator_test.go`, `pool/account_test.go`, `config/config_test.go`.
- Keep JSON fixtures under the package that consumes them: `proxy/testdata/claude_code_2_1_143_wire_request.json`, `proxy/testdata/claude_code_tool_reference_message.json`.

**Functions:**
- Use Go MixedCaps for exported functions: `config.GenerateMachineId` in `config/config.go`, `proxy.ListAvailableModels` in `proxy/kiro_api.go`, `pool.ClassifyFailureReason` in `pool/account.go`.
- Use lower camelCase for package-private helpers: `normalizeClaudeModelName` in `proxy/translator.go`, `defaultLoadBalanceConfig` in `config/config.go`, `buildKiroTransport` in `proxy/kiro.go`.
- Use `TestXxx` names that state the behavior under test, not the implementation line: `TestClassifyGenerationRequestBackgroundEndpoints` in `proxy/request_classifier_test.go`, `TestBuildKiroTransportConcurrencyAndTimeouts` in `proxy/kiro_test.go`.
- Use small package-local test helper names with intent prefixes: `mustParseURL` and `assertProxyURL` in `auth/http_client_test.go`, `mustContainInOrder` and `assertFrameEvent` in `proxy/claude_sse_writer_test.go`.

**Variables:**
- Use short receiver names for methods when the type is obvious: `p *AccountPool` in `pool/account.go`, `s *claudeSSEWriter` in `proxy/claude_sse_writer.go`, `g *modelAdmissionGateSet` in `proxy/opus_gate.go`.
- Use `got` and `want` in tests for direct comparisons: `auth/http_client_test.go`, `proxy/request_classifier_test.go`, `pool/breaker_test.go`.
- Use `tt` or `tc` for table-driven test cases: `proxy/translator_test.go`, `proxy/request_classifier_test.go`, `pool/account_test.go`.
- Use descriptive state names for long-lived package globals: `kiroHttpStore` and `kiroRestHttpStore` in `proxy/kiro.go`, `httpClientStore` in `auth/http_client.go`, `modelAdmissionGate` in `proxy/handler.go`.

**Types:**
- Use exported struct names for cross-package configuration and API contracts: `config.Account`, `config.ModelAdmissionConfig`, `pool.RuntimeHealth`, `proxy.ClaudeRequest`.
- Use unexported structs for package-private machinery: `proxy.claudeSSEWriter`, `proxy.modelAdmissionGateSet`, `pool.modelBreakerState`.
- Use typed strings for enumerations that cross call sites: `pool.FailureReason`, `pool.Strategy`, `proxy.RequestPriorityLane`.
- Preserve external API field casing through struct tags: `config.Account.UserId` uses `json:"userId"` in `config/config.go`; `proxy.ClaudeRequest.MaxTokens` uses `json:"max_tokens"` in `proxy/translator.go`.

## Code Style

**Formatting:**
- Use standard `gofmt` formatting for all Go files: `main.go`, `proxy/handler.go`, `config/config.go`.
- Imports are grouped by `gofmt`: standard library first, internal module imports such as `kiro-go/config`, then third-party imports such as `github.com/google/uuid` in `proxy/handler.go`.
- HTML/CSS/JavaScript live in a single static file with four-space indentation and embedded `<style>` / `<script>` sections: `web/index.html`.
- Do not introduce custom formatting config; no `.golangci.yml`, `.prettierrc`, `biome.json`, or ESLint config is present at repo root.

**Linting:**
- No dedicated lint runner is configured in repo root files. Use `go test ./...` as the enforced automated quality gate for code changes.
- GitHub Actions builds Docker images only; `.github/workflows/docker.yml` does not run `go test`, `go vet`, or lint checks.
- Keep code `go vet` friendly: avoid unchecked formatting mismatches, malformed struct tags, and impossible type assertions in `*.go`.

## Import Organization

**Order:**
1. Standard library imports: `context`, `encoding/json`, `net/http`, `sync`, `time` in `proxy/handler.go`.
2. Internal module imports: `kiro-go/auth`, `kiro-go/config`, `kiro-go/logger`, `kiro-go/pool` in `proxy/handler.go`.
3. Third-party imports: `github.com/google/uuid` in `proxy/handler.go` and `proxy/translator.go`.

**Path Aliases:**
- No Go path aliases are configured. Use the module path from `go.mod`: `kiro-go`.
- Import internal packages with `kiro-go/<package>` paths: `kiro-go/config` in `main.go`, `kiro-go/pool` in `proxy/handler.go`.
- Use import aliases only when avoiding name collisions: `neturl "net/url"` in `proxy/kiro_api.go`.

## Error Handling

**Patterns:**
- Return `error` values from helpers and service functions instead of panicking: `config.Init` in `config/config.go`, `proxy.GetUsageLimits` in `proxy/kiro_api.go`, `proxy.CallKiroAPI` in `proxy/kiro.go`.
- Wrap lower-level errors with context when preserving the cause matters: `fmt.Errorf("GetUsageLimits: %w", err)` in `proxy/kiro_api.go`.
- Return validation failures as human-readable strings for request-shape validators used by HTTP handlers: `validateClaudeRequestShapeWithOptions` and `validateClaudeThinkingConfigWithOptions` in `proxy/handler.go`.
- Include upstream HTTP status and body in external API errors for operator visibility: `fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))` in `proxy/kiro_api.go`.
- Classify operational failures into typed reasons before mutating pool state: `pool.ClassifyFailureReason` and `pool.RecordFailureUntil` in `pool/account.go`.
- Use context cancellation and deadlines in queueing/gating code: `claudeCodeConcurrencyGovernor.Acquire` in `proxy/claude_code_concurrency_governor.go`.

## Logging

**Framework:** `logger` package backed by the standard `log` package.

**Patterns:**
- Use `logger.Infof`, `logger.Warnf`, and `logger.Errorf` for service logs after startup initialization: `main.go`, `proxy/kiro_api.go`, `proxy/handler.go`.
- Use `logger.Fatalf` only for unrecoverable process startup failures: `main.go`.
- Use bracketed subsystem prefixes in log messages when the component is not obvious: `[ProfileArn]` and `[RefreshAccountInfo]` in `proxy/kiro_api.go`.
- Do not use package-level `log.Printf` in new runtime code outside early startup; `main.go` uses standard `log.Fatalf` only before `logger.Init`.
- Keep tests assertion-driven instead of log-driven; test helpers use `t.Fatalf` in `proxy/claude_sse_writer_test.go`, `proxy/kiro_api_test.go`, and `auth/http_client_test.go`.

## Comments

**When to Comment:**
- Add package comments for top-level packages that explain responsibility: `config/config.go`, `proxy/kiro.go`, `logger/logger.go`, `main.go`.
- Add comments to public functions and exported types used across packages: `config.GenerateMachineId` in `config/config.go`, `proxy.GetAuthClientForProxy` in `auth/http_client.go`, `logger.ParseLevel` in `logger/logger.go`.
- Use short implementation comments for non-obvious protocol or runtime constraints: HTTP/2 proxy handling in `proxy/kiro.go`, profile ARN fallback behavior in `proxy/kiro_api.go`.
- Existing comments are bilingual in places. Keep nearby language style when editing local blocks, such as Chinese comments in `pool/account.go`, `auth/http_client.go`, and `main.go`.

**JSDoc/TSDoc:**
- Not applicable for the active Go service. Embedded JavaScript in `web/index.html` does not use JSDoc.

## Function Design

**Size:** Keep new helpers focused and package-local unless they form a cross-package contract. Large coordinator files exist (`proxy/handler.go`, `proxy/translator.go`), so new protocol-specific behavior should prefer small helpers near the calling path.

**Parameters:** Pass domain structs and option structs when behavior has multiple knobs: `guardKiroPayload(payload, opts)` in `proxy/payload_guard.go`, `newModelAdmissionGateSet(admission)` in `proxy/opus_gate.go`.

**Return Values:** Return structured results for decision/summary paths and `(value, error)` for I/O: `payloadGuardResult` in `proxy/payload_guard.go`, `AdmissionPressureSnapshot` in `proxy/opus_gate.go`, `(*UsageLimitsResponse, error)` in `proxy/kiro_api.go`.

**Concurrency:** Protect mutable shared state with mutexes or atomics: `sync.RWMutex` in `config/config.go`, `sync.Mutex` in `proxy/claude_code_concurrency_governor.go`, `atomic.Pointer[http.Client]` in `proxy/kiro.go` and `auth/http_client.go`.

## Module Design

**Exports:** Export only cross-package APIs. Keep protocol internals unexported inside `proxy/`, such as `parseAnthropicEnvelope` in `proxy/anthropic_envelope.go` and `classifyGenerationRequest` in `proxy/request_classifier.go`.

**Barrel Files:** No barrel files are used. Packages expose identifiers directly from implementation files: `proxy/kiro.go`, `proxy/handler.go`, `config/config.go`.

**Package Boundaries:**
- Put persistent settings and account data behavior in `config/config.go`.
- Put account selection, cooldowns, and health scoring in `pool/account.go` and `pool/breaker.go`.
- Put auth HTTP client behavior in `auth/http_client.go` and auth flows in `auth/builderid.go`, `auth/iam_sso.go`, `auth/oidc.go`, `auth/sso_token.go`.
- Put HTTP routing, API translation, Kiro upstream calls, SSE writing, request logging, and admission control in `proxy/`.
- Put process bootstrap only in `main.go`.

---

*Convention analysis: 2026-05-21*
