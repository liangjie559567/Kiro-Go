# Kiro Account Health Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add request-level retry, account switching, health cooldown, and failure classification so the proxy can recover from quota exhaustion and transient upstream failures without manual intervention.

**Architecture:** Extend the existing `pool.AccountPool` with per-account health state and typed failure reasons, then teach the proxy request path to retry once on a new account when the current one fails for retryable reasons. Keep the current config persistence model and reuse the existing account update helpers so health state stays visible in the admin data.

**Tech Stack:** Go 1.21, existing `proxy`, `pool`, and `config` packages, table-driven unit tests.

---

### Task 1: Add failure classification and account health state

**Files:**
- Modify: `pool/account.go`
- Modify: `pool/account_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestClassifyFailureReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want FailureReason
	}{
		{name: "quota", err: errors.New("quota exhausted on Kiro IDE"), want: FailureReasonQuotaExhausted},
		{name: "auth", err: errors.New("HTTP 401 from Kiro IDE"), want: FailureReasonAuthExpired},
		{name: "suspended", err: errors.New("TEMPORARILY_SUSPENDED"), want: FailureReasonSuspended},
		{name: "rate limited", err: errors.New("HTTP 429 from Kiro IDE"), want: FailureReasonRateLimited},
		{name: "network", err: errors.New("dial tcp timeout"), want: FailureReasonTransientNetwork},
	}
	for _, tc := range tests {
		if got := ClassifyFailureReason(tc.err); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestRecordFailureAppliesCooldownByReason(t *testing.T) {
	p := &AccountPool{cooldowns: make(map[string]time.Time), failureCounts: make(map[string]int), failureReasons: make(map[string]FailureReason)}
	p.RecordFailure("acct-1", FailureReasonQuotaExhausted)
	if !p.IsCoolingDown("acct-1", time.Now()) {
		t.Fatal("expected quota failure to cool down account")
	}
}

func TestRecordSuccessClearsCooldownAndFailureState(t *testing.T) {
	p := &AccountPool{cooldowns: make(map[string]time.Time), failureCounts: make(map[string]int), failureReasons: make(map[string]FailureReason)}
	p.RecordFailure("acct-1", FailureReasonRateLimited)
	p.RecordSuccess("acct-1")
	if p.IsCoolingDown("acct-1", time.Now()) {
		t.Fatal("expected success to clear cooldown")
	}
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `go test ./pool -run 'TestClassifyFailureReason|TestRecordFailureAppliesCooldownByReason|TestRecordSuccessClearsCooldownAndFailureState' -v`
Expected: fail because `FailureReason` and the new methods do not exist yet.

- [ ] **Step 3: Implement the minimal account-health state**

```go
type FailureReason string

const (
	FailureReasonUnknown          FailureReason = "unknown"
	FailureReasonQuotaExhausted    FailureReason = "quota_exhausted"
	FailureReasonAuthExpired       FailureReason = "auth_expired"
	FailureReasonSuspended         FailureReason = "suspended"
	FailureReasonRateLimited       FailureReason = "rate_limited"
	FailureReasonTransientNetwork  FailureReason = "transient_network"
	FailureReasonUpstream5xx       FailureReason = "upstream_5xx"
)

func ClassifyFailureReason(err error) FailureReason
func (p *AccountPool) RecordFailure(id string, reason FailureReason)
func (p *AccountPool) IsCoolingDown(id string, now time.Time) bool
```

- [ ] **Step 4: Run the tests again**

Run: `go test ./pool -run 'TestClassifyFailureReason|TestRecordFailureAppliesCooldownByReason|TestRecordSuccessClearsCooldownAndFailureState' -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add pool/account.go pool/account_test.go
git commit -m "feat: add account failure classification"
```

### Task 2: Route requests through retryable account switching

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`
- Modify: `proxy/kiro.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestHandleClaudeRetriesOnQuotaExhaustedAccount(t *testing.T) { /* ... */ }
func TestHandleOpenAISkipsCoolingAccount(t *testing.T) { /* ... */ }
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `go test ./proxy -run 'TestHandleClaudeRetriesOnQuotaExhaustedAccount|TestHandleOpenAISkipsCoolingAccount' -v`
Expected: fail because request-level retry plumbing does not exist yet.

- [ ] **Step 3: Implement request retry and account switching**

```go
func (h *Handler) getRetryableAccount() *config.Account
func (h *Handler) handleWithAccountRetry(...)
```

Use the first selected account normally, then on retryable failure:
- classify the error,
- record the failure reason and cooldown,
- ask the pool for the next available account,
- retry once with the next account,
- preserve current response behavior if the second attempt also fails.

- [ ] **Step 4: Run the tests again**

Run: `go test ./proxy -run 'TestHandleClaudeRetriesOnQuotaExhaustedAccount|TestHandleOpenAISkipsCoolingAccount' -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add proxy/handler.go proxy/handler_test.go proxy/kiro.go
git commit -m "feat: retry requests across healthy accounts"
```

### Task 3: Refresh selection rules and admin-visible health state

**Files:**
- Modify: `pool/account.go`
- Modify: `config/config.go`
- Modify: `proxy/account_health.go`
- Modify: `proxy/account_health_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestGetNextSkipsCoolingAccounts(t *testing.T) { /* ... */ }
func TestHealthCheckDisablesSuspendedAccountsWithReason(t *testing.T) { /* ... */ }
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `go test ./... -run 'TestGetNextSkipsCoolingAccounts|TestHealthCheckDisablesSuspendedAccountsWithReason' -v`
Expected: fail until the new state is wired through.

- [ ] **Step 3: Implement the selection and persistence updates**

Add account fields for last failure reason and cooldown until, persist them through existing `config.UpdateAccount*` helpers, and make `GetNext` prefer non-cooling, non-expired accounts first.

- [ ] **Step 4: Run the tests again**

Run: `go test ./... -run 'TestGetNextSkipsCoolingAccounts|TestHealthCheckDisablesSuspendedAccountsWithReason' -v`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add pool/account.go config/config.go proxy/account_health.go proxy/account_health_test.go
git commit -m "feat: persist account health state"
```

### Task 4: Verify full proxy behavior

**Files:**
- Modify: `proxy/kiro_api_test.go`
- Modify: `proxy/translator_test.go` if needed for request retry coverage

- [ ] **Step 1: Add end-to-end retry coverage**
- [ ] **Step 2: Run `go test ./...`**
- [ ] **Step 3: Fix any regressions**
- [ ] **Step 4: Run `go test ./...` again**
- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "test: cover account health failover"
```

---

**Self-review**

- Spec coverage: retry, account switching, health cooldown, failure classification, and admin-visible state are all mapped to tasks.
- Placeholder scan: no TBD/TODO placeholders remain.
- Type consistency: `FailureReason`, `RecordFailure`, and `IsCoolingDown` are used consistently across tasks.
