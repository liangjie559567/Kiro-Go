# Testing Patterns

**Analysis Date:** 2026-05-21

## Test Framework

**Runner:**
- Go standard `testing` package from Go 1.21, configured by `go.mod`.
- Config: Not detected. There is no `jest.config.*`, `vitest.config.*`, custom Go test config, or repo-root lint config.

**Assertion Library:**
- Standard `testing.T` only. Tests use `t.Fatalf`, `t.Fatal`, and package-local helper functions in files such as `proxy/claude_sse_writer_test.go` and `auth/http_client_test.go`.

**Run Commands:**
```bash
go test ./...                         # Run all Go tests
go test ./proxy -run TestName -count=1 # Run one package/test without cache
go test ./... -cover                  # Run all tests with Go coverage summary
```

**Verified Command:**
```bash
go test ./...
```
- Passing packages: `kiro-go`, `kiro-go/auth`, `kiro-go/config`, `kiro-go/pool`, `kiro-go/proxy`.
- Package without tests: `kiro-go/logger`.

## Test File Organization

**Location:**
- Tests are co-located with package code using Go `_test.go` files: `config/config_test.go`, `pool/account_test.go`, `proxy/handler_test.go`, `auth/http_client_test.go`.
- Proxy fixtures live in `proxy/testdata/`: `proxy/testdata/claude_code_2_1_143_wire_request.json`, `proxy/testdata/claude_code_tool_reference_message.json`.
- Documentation/contract matrix tests read committed docs from `docs/`: `proxy/compatibility_matrix_test.go`, `proxy/ha_matrix_test.go`.
- UAT and browser evidence scripts live under `docs/superpowers/uat/`; these are not part of `go test ./...`.

**Naming:**
- Use `Test<FunctionOrBehavior>` names: `TestBuildAuthTransportUsesExplicitProxyURL` in `auth/http_client_test.go`, `TestRecordFailureUntilUsesExplicitRateLimitReset` in `pool/account_test.go`.
- Use behavior-rich names for protocol regressions: `TestClaudeSSEWriterMixedThinkingTextToolOrder` in `proxy/claude_sse_writer_test.go`, `TestClaudeToKiroConvertsOrphanedToolResultToText` in `proxy/translator_test.go`.
- Use package-level helper types/functions in test files when shared by multiple tests in that package: `roundTripFunc` in `proxy/kiro_api_test.go`, `sseFrame` in `proxy/claude_sse_writer_test.go`.

**Structure:**
```text
<package>/
├── feature.go
├── feature_test.go
└── testdata/
    └── fixture.json
```

Examples:
- `proxy/translator.go` and `proxy/translator_test.go`
- `proxy/kiro.go` and `proxy/kiro_test.go`
- `pool/breaker.go` and `pool/breaker_test.go`
- `auth/http_client.go` and `auth/http_client_test.go`

## Test Structure

**Suite Organization:**
```go
func TestParseModelAndThinkingNormalizesOfficialOpus47Names(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantModel    string
		wantThinking bool
	}{
		{name: "official dashed", model: "claude-opus-4-7", wantModel: "claude-opus-4.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotModel, gotThinking := ParseModelAndThinking(tt.model, "-thinking")
			if gotModel != tt.wantModel || gotThinking != tt.wantThinking {
				t.Fatalf("ParseModelAndThinking(%q) = %q/%v, want %q/%v", tt.model, gotModel, gotThinking, tt.wantModel, tt.wantThinking)
			}
		})
	}
}
```
- Source pattern: `proxy/translator_test.go`.

**Patterns:**
- Use direct Arrange/Act/Assert blocks without external DSLs: `main_test.go`, `pool/breaker_test.go`, `proxy/request_classifier_test.go`.
- Use table-driven tests when checking variants of parsing, classification, endpoints, or model names: `proxy/translator_test.go`, `proxy/request_classifier_test.go`, `pool/account_test.go`.
- Use `t.Run` for named table rows or endpoint/model variants: `proxy/translator_test.go`, `proxy/request_classifier_test.go`, `proxy/handler_test.go`.
- Use package-local helpers and `t.Helper()` for repeated assertions: `mustParseURL` in `auth/http_client_test.go`, `parseSSEFrames` in `proxy/claude_sse_writer_test.go`.
- Avoid `t.Parallel` in tests that mutate package globals, environment variables, config files, or HTTP client stores; no active test file uses `t.Parallel`.

## Mocking

**Framework:** No mocking framework. Use standard-library fakes and function replacement.

**Patterns:**
```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

kiroRestHttpStore.Store(&http.Client{
	Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"models":[]}`)),
			Header:     make(http.Header),
		}, nil
	}),
})
t.Cleanup(func() { InitKiroHttpClient("") })
```
- Source pattern: `proxy/kiro_api_test.go` and `proxy/kiro_test.go`.

**What to Mock:**
- Mock outbound HTTP calls by swapping `http.Client.Transport` with `roundTripFunc`: `proxy/kiro_api_test.go`, `proxy/kiro_test.go`, `proxy/handler_test.go`.
- Mock handler dependencies by replacing package-level function variables and restoring them with `t.Cleanup`: `ensureValidTokenForHealthCheck`, `listAvailableModelsForHealthCheck`, and `modelAdmissionGate` in `proxy/handler.go` / `proxy/account_health_test.go`.
- Mock environment proxy behavior with `t.Setenv`: `auth/http_client_test.go`, `proxy/kiro_test.go`.
- Mock persisted configuration with `config.Init(filepath.Join(t.TempDir(), "config.json"))`: `config/config_test.go`, `proxy/handler_test.go`, `proxy/ecosystem_ops_test.go`.

**What NOT to Mock:**
- Do not mock pure translation and classification helpers. Call them directly: `proxy/translator_test.go`, `proxy/request_classifier_test.go`, `proxy/token_estimator_test.go`.
- Do not mock Go synchronization primitives in concurrency tests. Exercise real goroutines, channels, contexts, and timeouts: `proxy/opus_gate_test.go`, `proxy/claude_code_concurrency_governor_test.go`.
- Do not use live upstream Kiro services in unit tests. Upstream API tests use fake `http.Client` transports: `proxy/kiro_api_test.go`, `proxy/kiro_test.go`.

## Fixtures and Factories

**Test Data:**
```go
raw, err := os.ReadFile("testdata/claude_code_2_1_143_wire_request.json")
if err != nil {
	t.Fatalf("read fixture: %v", err)
}
var fixture struct {
	Headers map[string]string      `json:"headers"`
	Body    map[string]interface{} `json:"body"`
}
if err := json.Unmarshal(raw, &fixture); err != nil {
	t.Fatalf("decode fixture: %v", err)
}
```
- Source pattern: `proxy/anthropic_envelope_test.go`.

**Location:**
- Protocol fixture JSON: `proxy/testdata/claude_code_2_1_143_wire_request.json`, `proxy/testdata/claude_code_tool_reference_message.json`.
- Compatibility matrix data: `docs/claude-code-compatibility-matrix.json`, `docs/kiro-ha-compatibility-matrix.json`, `docs/kiro-ecosystem-operations-matrix.json`.
- Manual/full-stack UAT evidence and Playwright scripts: `docs/superpowers/uat/`.

**Factories:**
- Build request structs inline for clarity: `proxy/translator_test.go`, `proxy/payload_guard_test.go`.
- Use small helper config functions for repeated setup: `testClaudeCodeGovernorConfig` in `proxy/claude_code_concurrency_governor_test.go`.
- Use `httptest.NewRequest` and `httptest.NewRecorder` for handler tests: `proxy/request_classifier_test.go`, `proxy/request_log_test.go`, `proxy/ecosystem_ops_test.go`, `proxy/handler_test.go`.

## Coverage

**Requirements:** None enforced by config. No coverage threshold file is present.

**View Coverage:**
```bash
go test ./... -cover
```

**Practical Coverage Expectations:**
- Add focused unit tests for pure transforms and protocol compatibility changes in `proxy/translator_test.go`, `proxy/anthropic_envelope_test.go`, `proxy/token_estimator_test.go`, or `proxy/payload_guard_test.go`.
- Add handler-level tests for endpoint behavior, request logging, admin APIs, and status/body contracts in `proxy/handler_test.go` or `proxy/request_log_test.go`.
- Add pool tests for routing, cooldowns, load balancing, account health, or breaker behavior in `pool/account_test.go` and `pool/breaker_test.go`.
- Add config tests for persisted defaults, validation, and update helpers in `config/config_test.go`.

## Test Types

**Unit Tests:**
- Pure helper tests dominate translation, parsing, normalization, payload guarding, token estimation, and routing state: `proxy/translator_test.go`, `proxy/request_classifier_test.go`, `proxy/payload_guard_test.go`, `pool/breaker_test.go`.
- Error classification and state transitions are tested directly: `pool/account_test.go`, `proxy/opus_gate_test.go`.

**Integration Tests:**
- HTTP handler tests use `httptest` against `Handler` methods and in-memory config files: `proxy/handler_test.go`, `proxy/request_log_test.go`, `proxy/ecosystem_ops_test.go`.
- Upstream-client integration shape is tested with fake `http.Client` transports: `proxy/kiro_api_test.go`, `proxy/kiro_test.go`.
- Documentation matrix integrity is tested by reading docs JSON files: `proxy/compatibility_matrix_test.go`, `proxy/ha_matrix_test.go`.

**E2E Tests:**
- Not part of `go test ./...`.
- Full-stack/UAT scripts and evidence are stored under `docs/superpowers/uat/`, including Node/Playwright scripts such as `docs/superpowers/uat/sub2api-kiro-10x10-20260520135054/playwright-uat.js`.
- README documents a Node UAT command for stable downstream testing: `README.md`.

## Common Patterns

**Async Testing:**
```go
waiterStarted := make(chan struct{})
waiterDone := make(chan error, 1)
waiterCtx, cancelWaiter := context.WithCancel(context.Background())
defer cancelWaiter()
go func() {
	close(waiterStarted)
	decision, err := gov.Acquire(waiterCtx, req, time.Second)
	decision.Release()
	waiterDone <- err
}()
```
- Source pattern: `proxy/claude_code_concurrency_governor_test.go`.

**Error Testing:**
```go
_, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 128, HardLimitBytes: 512})
if err == nil {
	t.Fatalf("expected invalid payload error")
}
if !strings.Contains(err.Error(), "payload remains too large") {
	t.Fatalf("expected payload-too-large error, got %v", err)
}
```
- Source pattern: `proxy/payload_guard_test.go`.

**HTTP Handler Testing:**
```go
req := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4-7", nil)
w := httptest.NewRecorder()
h.handleFleetReadiness(w, req)
if w.Code != http.StatusOK {
	t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
}
```
- Source pattern: `proxy/ecosystem_ops_test.go` and `proxy/handler_test.go`.

**SSE Testing:**
```go
frames := parseSSEFrames(t, w.Body.String())
assertFrameEvent(t, frames, 0, "message_start")
assertNestedString(t, frames[1], "content_block", "type", "thinking")
```
- Source pattern: `proxy/claude_sse_writer_test.go`.

**Temporary State Cleanup:**
```go
oldGate := modelAdmissionGate
modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{})
t.Cleanup(func() { modelAdmissionGate = oldGate })
```
- Source pattern: `proxy/account_health_test.go`, `proxy/ecosystem_ops_test.go`, and `proxy/handler_test.go`.

---

*Testing analysis: 2026-05-21*
