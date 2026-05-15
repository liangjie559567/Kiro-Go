# Testing Patterns

**Analysis Date:** 2026-05-15

## Test Framework

**Runner:**
- Go standard `testing` package with Go 1.21 from `go.mod:3`.
- Config: Not detected. No separate `go test` config file exists.

**Assertion Library:**
- Standard library only. Assertions are explicit `if` checks with `t.Fatalf` or `t.Errorf`, as in `pool/account_test.go:73` and `proxy/handler_test.go:221`.

**Run Commands:**
```bash
go test ./...              # Run all tests
go test -v ./...           # Verbose test output
go test -cover ./...       # Package coverage summary
```

## Test File Organization

**Location:**
- Tests are co-located beside production files in the same package directory: `auth/http_client_test.go`, `config/config_test.go`, `pool/account_test.go`, `proxy/handler_test.go`.
- Test packages use the same package name as the implementation (`package proxy`, `package config`, `package pool`, `package auth`), allowing tests to cover unexported helpers.

**Naming:**
- Test files use `*_test.go`.
- Test functions use `Test<Behavior>` names that describe expected behavior: `TestClassifyFailureReason` in `pool/account_test.go:58`, `TestResolveProfileArnFetchesAndCachesProfile` in `proxy/kiro_api_test.go:31`.
- Table-driven subcases use `name`, `err`, `want` fields when several inputs share one assertion path, as in `pool/account_test.go:58`.

**Structure:**
```text
<package>/
├── feature.go
└── feature_test.go
```

## Test Structure

**Suite Organization:**
```go
func TestClassifyFailureReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want FailureReason
	}{
		{name: "quota", err: errors.New("quota exhausted on Kiro IDE"), want: FailureReasonQuotaExhausted},
	}

	for _, tc := range tests {
		if got := ClassifyFailureReason(tc.err); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
```

**Patterns:**
- Arrange state directly with structs and package constructors, then call one function/method and assert the externally visible result: `pool/account_test.go:80`, `proxy/kiro_api_test.go:31`.
- Initialize persisted config against a temp file for tests that mutate global config: `config.Init(filepath.Join(t.TempDir(), "config.json"))` in `proxy/account_refresh_test.go:105` and `proxy/handler_test.go:167`.
- Use `t.Cleanup` to restore global HTTP clients and package-level hooks after injection: `proxy/kiro_api_test.go:19`, `proxy/account_refresh_test.go:139`.
- Use deadline loops for asynchronous side effects instead of fixed sleeps alone: `proxy/handler_test.go:258`.

## Mocking

**Framework:** Standard library fakes; no mocking framework detected.

**Patterns:**
```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
```

**What to Mock:**
- Mock outbound HTTP by storing a custom `*http.Client` with `Transport: roundTripFunc(...)`, as in `proxy/kiro_api_test.go:47` and `proxy/account_refresh_test.go:126`.
- Mock handler dependencies through package-level function variables when code already exposes a hook, such as `listAvailableModelsForHealthCheck` in `proxy/handler.go:23` and `proxy/account_health_test.go`.
- Mock environment variables with `t.Setenv`, as in `auth/http_client_test.go:20` and `proxy/kiro_test.go`.
- Mock time-sensitive waits by replacing package-level sleep/budget hooks when available, as in Opus retry tests in `proxy/handler_test.go`.

**What NOT to Mock:**
- Do not mock pure conversion and validation helpers; instantiate real request structs and assert converted payloads directly, as in `proxy/translator_test.go`.
- Do not perform real network calls in tests. Use injected `http.Client` transports for auth, REST, streaming, and MCP paths.
- Do not read or write the real `data/config.json` in tests. Use `t.TempDir()` plus `config.Init`.

## Fixtures and Factories

**Test Data:**
```go
account := config.Account{
	ID:          "acct-1",
	Enabled:     true,
	AccessToken: "token-1",
	ProfileArn:  "arn:aws:codewhisperer:profile/test-1",
	ExpiresAt:   time.Now().Add(time.Hour).Unix(),
}
```

**Location:**
- Fixtures are inline in test functions rather than stored in separate fixture files.
- JSON request/response bodies are inline string literals, often passed through `strings.NewReader` or `io.NopCloser`, as in `proxy/handler_test.go:195` and `proxy/account_refresh_test.go:148`.
- Helper functions stay in the package test file where they are needed: `mustParseURL` and `assertProxyURL` in `auth/http_client_test.go:35`, `roundTripFunc` in `proxy/kiro_api_test.go:92`.

## Coverage

**Requirements:** None enforced in repo configuration.

**View Coverage:**
```bash
go test -cover ./...
```

## Test Types

**Unit Tests:**
- Core coverage is unit-level for config normalization/validation, pool scheduling, failure classification, prompt cache tracking, model mapping, header construction, and request translation.
- Use direct constructor/state setup: `pool.AccountPool{...}` in `pool/account_test.go:80`, `newPromptCacheTracker` in `proxy/cache_tracker_test.go`.

**Integration Tests:**
- Lightweight in-process integration tests cover HTTP handler and proxy flows using `httptest.NewRequest`, `httptest.NewRecorder`, temp config files, and fake outbound transports: `proxy/handler_test.go:216`.
- Persistence behavior is tested through real temp config files via `config.Init` and `config.GetAccounts`, as in `proxy/kiro_api_test.go:31`.

**E2E Tests:**
- Not used. No Playwright, Cypress, or external E2E test runner is detected.

## Common Patterns

**Async Testing:**
```go
deadline := time.Now().Add(time.Second)
for time.Now().Before(deadline) {
	if config.GetAccounts()[0].RequestCount > 0 {
		return
	}
	time.Sleep(10 * time.Millisecond)
}
t.Fatalf("expected async account stats update to complete")
```

**Error Testing:**
```go
err := CallKiroAPI(account, payload, callback)
if err == nil {
	t.Fatalf("expected 429 error")
}
if !strings.Contains(err.Error(), "MODEL_RATE_LIMIT") {
	t.Fatalf("expected 429 body to be preserved, got %q", err.Error())
}
```

---

*Testing analysis: 2026-05-15*
