# Opus 4.7 Health Governor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a bounded Opus 4.7 health governor so sub2api can call Kiro-Go sustainably without whole-pool retry amplification, data loss, or false PASS results.

**Architecture:** Extend the existing model admission gate, request retry loops, request logs, fleet readiness API, and background task status rather than adding a separate service. The governor adds a model circuit state, per-request retry budget, pressure headers, quiet-mode status, and UAT evidence scripts.

**Tech Stack:** Go standard library, existing Kiro-Go `proxy`, `pool`, and `config` packages, single-file Admin UI in `web/index.html`, Node/Playwright UAT scripts under `docs/superpowers/uat`.

---

## File Structure

- Modify `proxy/opus_gate.go`: add model circuit state, retry-after tracking, pressure reason, and safe concurrency snapshot fields.
- Modify `proxy/handler.go`: enforce Opus request budgets, use circuit state before admission/routing, emit retryable 429 pressure responses with headers, and update request logs with circuit metadata.
- Modify `proxy/request_log.go`: add circuit fields to `RequestLogEntry` and `RequestLogAttempt`.
- Modify `proxy/ecosystem_ops.go`: expand `/admin/api/fleet/readiness` into the sub2api health contract for model-specific readiness.
- Modify `proxy/account_refresh.go` and `proxy/account_health.go`: add pressure-aware quiet-mode skipping and jittered next-run metadata.
- Modify `web/index.html`: show a compact Opus 4.7 fleet health panel using the existing API tab/readiness style.
- Modify `README.md` and `README_CN.md`: document the Opus 4.7 health contract and operational UAT gate.
- Add `docs/superpowers/uat/opus47-health-governor/README.md`: define environment variables and evidence expectations.
- Add `docs/superpowers/uat/opus47-health-governor/run-opus47-governor-uat.js`: real Docker/API/Playwright-compatible UAT harness.
- Add or extend tests in `proxy/handler_test.go`, `proxy/request_log_test.go`, `proxy/account_refresh_test.go`, `proxy/account_health_test.go`, and `proxy/ecosystem_ops_test.go`.

## Task 1: Model Circuit State In Admission Gate

**Files:**
- Modify: `proxy/opus_gate.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing tests for circuit transitions**

Append these tests near the existing model admission pressure tests in `proxy/handler_test.go`:

```go
func TestModelAdmissionCircuitOpensAfterRepeatedCapacityPressure(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(45*time.Second))

	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "open" {
		t.Fatalf("CircuitState = %q, want open; snap=%#v", snap.CircuitState, snap)
	}
	if snap.RetryAfterSeconds < 40 || snap.RetryAfterSeconds > 45 {
		t.Fatalf("RetryAfterSeconds = %d, want around 45", snap.RetryAfterSeconds)
	}
	if snap.EffectiveMaxConcurrent != 1 {
		t.Fatalf("EffectiveMaxConcurrent = %d, want 1", snap.EffectiveMaxConcurrent)
	}
	if !snap.Active {
		t.Fatalf("expected active pressure snapshot")
	}
}

func TestModelAdmissionCircuitHalfOpenAfterRetryAfter(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))

	now = now.Add(11 * time.Second)
	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "half_open" {
		t.Fatalf("CircuitState = %q, want half_open; snap=%#v", snap.CircuitState, snap)
	}
	if snap.RetryAfterSeconds != 0 {
		t.Fatalf("RetryAfterSeconds = %d, want 0", snap.RetryAfterSeconds)
	}
}

func TestModelAdmissionCircuitSuccessClosesAfterHalfOpen(t *testing.T) {
	now := time.Unix(1000, 0)
	gate := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	gate.now = func() time.Time { return now }

	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	gate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(10*time.Second))
	now = now.Add(11 * time.Second)

	gate.recordSuccess("claude-opus-4.7", time.Second)
	gate.recordSuccess("claude-opus-4.7", time.Second)

	snap := gate.modelSnapshot("claude-opus-4.7")
	if snap.CircuitState != "closed" {
		t.Fatalf("CircuitState = %q, want closed; snap=%#v", snap.CircuitState, snap)
	}
	if snap.Score != 0 {
		t.Fatalf("Score = %d, want 0", snap.Score)
	}
	if snap.EffectiveMaxConcurrent != 4 {
		t.Fatalf("EffectiveMaxConcurrent = %d, want restored max 4", snap.EffectiveMaxConcurrent)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./proxy -run 'TestModelAdmissionCircuit' -count=1
```

Expected: FAIL because `AdmissionPressureSnapshot` has no `CircuitState` or `RetryAfterSeconds`, and `modelSnapshot` does not exist.

- [ ] **Step 3: Implement circuit fields and snapshot helper**

In `proxy/opus_gate.go`, update `admissionPressureState`:

```go
type admissionPressureState struct {
	score                  int
	expiresAt              time.Time
	retryAt                time.Time
	circuitState           string
	lastPressureReason     string
	effectiveMaxConcurrent int
	recentCapacityErrors   int
	recentQueueTimeouts    int
	recentSuccesses        int
	halfOpenSuccesses      int
	lastPressureAt         time.Time
	lastSuccessAt          time.Time
}
```

Update `AdmissionPressureSnapshot`:

```go
type AdmissionPressureSnapshot struct {
	Model                  string    `json:"model"`
	Score                  int       `json:"score"`
	Active                 bool      `json:"active"`
	ReducedConcurrency     bool      `json:"reducedConcurrency"`
	CircuitState           string    `json:"circuitState,omitempty"`
	RetryAfterSeconds      int       `json:"retryAfterSeconds,omitempty"`
	LastPressureReason     string    `json:"lastPressureReason,omitempty"`
	LastPressureAt         time.Time `json:"lastPressureAt,omitempty"`
	ExpiresAt              time.Time `json:"expiresAt,omitempty"`
	ExpiresInMs            int64     `json:"expiresInMs,omitempty"`
	MaxConcurrent          int       `json:"maxConcurrent,omitempty"`
	EffectiveMaxConcurrent int       `json:"effectiveMaxConcurrent,omitempty"`
	QueueDepth             int       `json:"queueDepth,omitempty"`
	ActiveRequests         int       `json:"activeRequests,omitempty"`
	RecentCapacityErrors   int       `json:"recentCapacityErrors,omitempty"`
	RecentQueueTimeouts    int       `json:"recentQueueTimeouts,omitempty"`
	RecentSuccesses        int       `json:"recentSuccesses,omitempty"`
}
```

Add helper functions near `admissionMetrics`:

```go
func (g *modelAdmissionGateSet) modelSnapshot(model string) AdmissionPressureSnapshot {
	model = normalizeAdmissionModel(model)
	snaps := g.snapshot()
	for _, snap := range snaps {
		if snap.Model == model {
			return snap
		}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	maxConcurrent := 0
	if gate != nil {
		maxConcurrent = gate.maxConcurrent
	}
	return AdmissionPressureSnapshot{
		Model:                  model,
		CircuitState:           "closed",
		MaxConcurrent:          maxConcurrent,
		EffectiveMaxConcurrent: maxConcurrent,
	}
}

func circuitStateForPressure(state *admissionPressureState, now time.Time) string {
	if state == nil || state.score <= 0 {
		return "closed"
	}
	if state.retryAt.After(now) {
		return "open"
	}
	if state.score >= 4 {
		return "half_open"
	}
	if now.Before(state.expiresAt) {
		return "degraded"
	}
	return "closed"
}
```

In `recordPressureUntil`, set:

```go
state.lastPressureReason = pressureReasonForStatus(statusCode)
if retryAt.After(state.retryAt) {
	state.retryAt = retryAt
}
if state.score >= 4 {
	if state.retryAt.IsZero() || state.retryAt.Before(expiresAt) {
		state.retryAt = expiresAt
	}
	state.effectiveMaxConcurrent = 1
}
state.circuitState = circuitStateForPressure(state, now)
state.halfOpenSuccesses = 0
```

Add:

```go
func pressureReasonForStatus(statusCode int) string {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return "rate_limited_or_model_capacity"
	case statusCode == http.StatusServiceUnavailable:
		return "service_unavailable"
	case statusCode >= 500:
		return "upstream_5xx"
	default:
		return "latency"
	}
}
```

In `recordQueueTimeout`, set `lastPressureReason = "queue_timeout"` and `retryAt = expiresAt` when score reaches 4.

In `recordSuccess`, when `circuitStateForPressure(state, now) == "half_open"`, increment `halfOpenSuccesses`; after two successes, set `score = 0`, `recentCapacityErrors = 0`, `recentQueueTimeouts = 0`, `halfOpenSuccesses = 0`, `retryAt = time.Time{}`, `expiresAt = time.Time{}`, and restore `effectiveMaxConcurrent = gate.maxConcurrent`.

Update `snapshot()` to populate `CircuitState`, `RetryAfterSeconds`, `LastPressureReason`, and `LastPressureAt`. Use `retryAfterSeconds := int((state.retryAt.Sub(now) + time.Second - 1) / time.Second)` only when `state.retryAt.After(now)`.

- [ ] **Step 4: Run circuit tests**

Run:

```bash
go test ./proxy -run 'TestModelAdmissionCircuit|TestModelAdmissionGate' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/opus_gate.go proxy/handler_test.go
git commit -m "feat: add opus model circuit state"
```

## Task 2: Request Budget Enforcement

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/request_log.go`
- Test: `proxy/handler_test.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Write failing tests for retry budget**

Add near the existing Opus retry tests in `proxy/handler_test.go`:

```go
func TestHandleClaudeOpus47StopsAtRequestAttemptBudget(t *testing.T) {
	oldBudget := opusCapacityRetryBudget
	oldSleep := sleepForOpusCapacityRetry
	opusCapacityRetryBudget = 25 * time.Second
	sleepForOpusCapacityRetry = func(time.Duration) {}
	t.Cleanup(func() {
		opusCapacityRetryBudget = oldBudget
		sleepForOpusCapacityRetry = oldSleep
	})

	h, upstreamHits := newOpus47RetryBudgetTestHandler(t, 8, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleMessages(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s, want 429", w.Code, w.Body.String())
	}
	if *upstreamHits != 4 {
		t.Fatalf("upstream hits = %d, want 4 attempt budget", *upstreamHits)
	}
	if got := w.Header().Get("X-Kiro-Go-Retryable"); got != "true" {
		t.Fatalf("X-Kiro-Go-Retryable = %q, want true", got)
	}
}
```

Add this helper in the same test file near other test helpers:

```go
func newOpus47RetryBudgetTestHandler(t *testing.T, accounts int, upstream http.HandlerFunc) (*Handler, *int) {
	t.Helper()
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		upstream(w, r)
	}))
	t.Cleanup(server.Close)

	p := pool.NewAccountPool()
	for i := 0; i < accounts; i++ {
		id := fmt.Sprintf("acct-%d", i+1)
		acc := config.Account{
			ID:           id,
			Email:        id + "@example.test",
			Enabled:      true,
			Healthy:      true,
			AccessToken:  "token",
			RefreshToken: "refresh",
			APIEndpoint:  server.URL,
			Region:       "us-east-1",
		}
		p.AddAccount(acc)
		p.SetModelList(id, []string{"claude-opus-4.7"})
	}

	h := &Handler{
		pool:        p,
		requestLogs: newRequestLogStore(defaultRequestLogCapacity),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
	}
	return h, &hits
}
```

If existing constructors require different field names for upstream endpoint, adjust only to match existing test fixtures in `proxy/handler_test.go`; keep the assertion at 4 hits.

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
go test ./proxy -run TestHandleClaudeOpus47StopsAtRequestAttemptBudget -count=1
```

Expected: FAIL because current code can continue beyond 4 attempts.

- [ ] **Step 3: Add budget type and enforcement**

In `proxy/handler.go`, add constants and type near `opusCapacityRetryBudget`:

```go
const (
	defaultOpus47MaxAttempts       = 4
	defaultOpus47RequestBudget     = 25 * time.Second
	defaultOpus47ReadinessCacheTTL = 3 * time.Second
)

type opus47RequestBudget struct {
	deadline    time.Time
	maxAttempts int
}

func newOpus47RequestBudget(model string) opus47RequestBudget {
	if !isOpus47Model(model) {
		return opus47RequestBudget{deadline: time.Now().Add(opusCapacityRetryBudget), maxAttempts: 0}
	}
	budget := opusCapacityRetryBudget
	if budget <= 0 || budget > defaultOpus47RequestBudget {
		budget = defaultOpus47RequestBudget
	}
	return opus47RequestBudget{deadline: time.Now().Add(budget), maxAttempts: defaultOpus47MaxAttempts}
}

func (b opus47RequestBudget) attemptsExhausted(attempt int, model string) bool {
	return isOpus47Model(model) && b.maxAttempts > 0 && attempt >= b.maxAttempts
}
```

In `handleClaudeWithAccountRetry`, replace:

```go
deadline := time.Now().Add(opusCapacityRetryBudget)
```

with:

```go
budget := newOpus47RequestBudget(model)
deadline := budget.deadline
```

At the top of the `for` loop, before selecting an account, add:

```go
if budget.attemptsExhausted(attempt, model) {
	h.recordFailure()
	h.sendClaudeOpusPressureError(w, model, lastErr, "attempt_budget_exhausted")
	return
}
```

Repeat the same budget creation and loop check in `handleOpenAIWithAccountRetry`, returning `h.sendOpenAIOpusPressureError(w, model, lastErr, "attempt_budget_exhausted")`.

- [ ] **Step 4: Add pressure error helpers**

In `proxy/handler.go`, add helpers near `sendNoAvailableAccountsError`:

```go
func (h *Handler) sendClaudeOpusPressureError(w http.ResponseWriter, model string, lastErr error, reason string) {
	h.applyOpusPressureHeaders(w, model, lastErr, reason)
	message := "Opus 4.7 upstream pressure: " + reason
	if lastErr != nil {
		message += ": " + lastErr.Error()
	}
	h.sendClaudeUpstreamError(w, http.StatusTooManyRequests, "rate_limit_error", message, lastErr)
}

func (h *Handler) sendOpenAIOpusPressureError(w http.ResponseWriter, model string, lastErr error, reason string) {
	h.applyOpusPressureHeaders(w, model, lastErr, reason)
	message := "Opus 4.7 upstream pressure: " + reason
	if lastErr != nil {
		message += ": " + lastErr.Error()
	}
	h.sendOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", message)
}

func (h *Handler) applyOpusPressureHeaders(w http.ResponseWriter, model string, err error, reason string) {
	if w == nil {
		return
	}
	w.Header().Set("X-Kiro-Go-Error-Reason", reason)
	w.Header().Set("X-Kiro-Go-Retryable", "true")
	snap := AdmissionPressureSnapshot{}
	if modelAdmissionGate != nil {
		snap = modelAdmissionGate.modelSnapshot(model)
	}
	if snap.CircuitState != "" {
		w.Header().Set("X-Kiro-Go-Circuit-State", snap.CircuitState)
	}
	if snap.EffectiveMaxConcurrent > 0 {
		w.Header().Set("X-Kiro-Go-Safe-Concurrency", strconv.Itoa(snap.EffectiveMaxConcurrent))
	}
	if retryAfter := retryAfterSecondsFromReset(rateLimitResetFromError(err)); retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	} else if snap.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(snap.RetryAfterSeconds))
	} else {
		w.Header().Set("Retry-After", strconv.Itoa(defaultRateLimitFallbackSeconds))
	}
}
```

- [ ] **Step 5: Extend request log circuit fields**

In `proxy/request_log.go`, add to `RequestLogEntry`:

```go
OpusCircuitState      string `json:"opusCircuitState,omitempty"`
OpusRetryAfterSeconds int    `json:"opusRetryAfterSeconds,omitempty"`
OpusRequestBudgetMs   int64  `json:"opusRequestBudgetMs,omitempty"`
OpusAttemptBudget     int    `json:"opusAttemptBudget,omitempty"`
```

Add to `RequestLogAttempt`:

```go
CircuitState string `json:"circuitState,omitempty"`
```

Add:

```go
func updateRequestLogOpusGovernor(r *http.Request, state string, retryAfterSeconds int, budget opus47RequestBudget) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.OpusCircuitState = strings.TrimSpace(state)
	ctx.entry.OpusRetryAfterSeconds = retryAfterSeconds
	ctx.entry.OpusAttemptBudget = budget.maxAttempts
	if !budget.deadline.IsZero() {
		ctx.entry.OpusRequestBudgetMs = time.Until(budget.deadline).Milliseconds()
	}
}
```

Call `updateRequestLogOpusGovernor` after creating `budget` in both retry handlers with `modelAdmissionGate.modelSnapshot(model)`.

In `updateRequestLogRoutingDecision`, set `CircuitState` in `RequestLogAttempt` from `modelAdmissionGate.modelSnapshot(model).CircuitState`.

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./proxy -run 'TestHandleClaudeOpus47StopsAtRequestAttemptBudget|TestRequestLog|TestClaude.*Retry|TestOpenAI.*Retry' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add proxy/handler.go proxy/request_log.go proxy/handler_test.go proxy/request_log_test.go
git commit -m "feat: enforce opus request budgets"
```

## Task 3: Fleet Readiness Contract For sub2api

**Files:**
- Modify: `proxy/ecosystem_ops.go`
- Test: `proxy/ecosystem_ops_test.go`

- [ ] **Step 1: Write failing readiness contract test**

Add to `proxy/ecosystem_ops_test.go`:

```go
func TestFleetReadinessIncludesOpusGovernorContract(t *testing.T) {
	oldGate := modelAdmissionGate
	now := time.Unix(2000, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(30*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(30*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(30*time.Second))
	t.Cleanup(func() { modelAdmissionGate = oldGate })

	h := &Handler{
		pool:          pool.NewAccountPool(),
		autoRefreshUpdated: make(chan struct{}, 1),
		healthCheckUpdated: make(chan struct{}, 1),
		requestLogs:   newRequestLogStore(defaultRequestLogCapacity),
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4-7", nil)
	w := httptest.NewRecorder()
	h.apiGetFleetReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["model"] != "claude-opus-4-7" {
		t.Fatalf("model = %#v", body["model"])
	}
	if body["status"] != "blocked" {
		t.Fatalf("status = %#v, want blocked; body=%#v", body["status"], body)
	}
	if body["circuitState"] != "open" {
		t.Fatalf("circuitState = %#v, want open; body=%#v", body["circuitState"], body)
	}
	if got, ok := body["retryAfterSeconds"].(float64); !ok || got < 25 {
		t.Fatalf("retryAfterSeconds = %#v, want >=25", body["retryAfterSeconds"])
	}
	if _, ok := body["safeConcurrency"].(float64); !ok {
		t.Fatalf("safeConcurrency missing: %#v", body)
	}
	if _, ok := body["locallySchedulableAccounts"].(float64); !ok {
		t.Fatalf("locallySchedulableAccounts missing: %#v", body)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
go test ./proxy -run TestFleetReadinessIncludesOpusGovernorContract -count=1
```

Expected: FAIL because current response does not include top-level `status`, `circuitState`, `safeConcurrency`, and related fields.

- [ ] **Step 3: Implement readiness contract**

In `proxy/ecosystem_ops.go`, update `apiGetFleetReadiness` after computing `summary`:

```go
snap := AdmissionPressureSnapshot{}
if modelAdmissionGate != nil {
	snap = modelAdmissionGate.modelSnapshot(mapped)
}
safeConcurrency := snap.EffectiveMaxConcurrent
if safeConcurrency <= 0 {
	safeConcurrency = summary["eligible"]
}
status := "healthy"
if snap.CircuitState == "open" || safeConcurrency <= 0 || summary["eligible"] == 0 {
	status = "blocked"
} else if snap.CircuitState == "degraded" || snap.CircuitState == "half_open" || snap.Score >= 2 || safeConcurrency < summary["eligible"] {
	status = "degraded"
}
```

Replace the response map with fields that preserve existing keys and add the new contract:

```go
json.NewEncoder(w).Encode(map[string]interface{}{
	"model":                       model,
	"requestedModel":              model,
	"mappedModel":                 mapped,
	"status":                      status,
	"circuitState":                firstNonEmpty(snap.CircuitState, "closed"),
	"retryAfterSeconds":           snap.RetryAfterSeconds,
	"safeConcurrency":             safeConcurrency,
	"currentInFlight":             snap.ActiveRequests,
	"enabledAccounts":             summary["enabled"],
	"modelListedAccounts":         summary["total"] - summary["modelNotListed"],
	"locallySchedulableAccounts":  summary["eligible"],
	"coolingDownAccounts":         summary["coolingDown"],
	"temporaryLimitedAccounts":    countFleetRowsByReason(rows, string(pool.FailureReasonTemporaryLimited)),
	"quotaBlockedAccounts":        summary["quotaBlocked"],
	"authBlockedAccounts":         countFleetRowsByReason(rows, string(pool.FailureReasonAuthExpired)),
	"admissionPressureScore":      snap.Score,
	"lastPressureReason":          snap.LastPressureReason,
	"lastPressureAt":              snap.LastPressureAt,
	"notes":                       fleetReadinessNotes(status, snap, summary),
	"strategy":                    config.GetLoadBalanceConfig().Strategy,
	"summary":                     summary,
	"accounts":                    rows,
	"autoRefresh":                 h.getAutoRefreshStatus(),
	"healthCheck":                 h.getHealthCheckStatus(),
})
```

Add helpers in `proxy/ecosystem_ops.go`:

```go
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func countFleetRowsByReason(rows []map[string]interface{}, reason string) int {
	count := 0
	for _, row := range rows {
		if fmt.Sprint(row["reason"]) == reason || fmt.Sprint(row["lastFailureReason"]) == reason {
			count++
		}
	}
	return count
}

func fleetReadinessNotes(status string, snap AdmissionPressureSnapshot, summary map[string]int) []string {
	notes := []string{}
	if status == "blocked" {
		notes = append(notes, "sub2api should not send new Opus 4.7 calls until retryAfterSeconds or schedulable capacity recovers")
	}
	if status == "degraded" {
		notes = append(notes, "sub2api should queue or limit Opus 4.7 calls to safeConcurrency")
	}
	if snap.CircuitState == "open" {
		notes = append(notes, "model circuit is open due to recent upstream pressure")
	}
	if summary["coolingDown"] > 0 {
		notes = append(notes, "some accounts are cooling down and must not be probed aggressively")
	}
	return notes
}
```

- [ ] **Step 4: Run readiness tests**

Run:

```bash
go test ./proxy -run 'TestFleetReadiness|TestSchedulerPreview|TestEcosystem' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/ecosystem_ops.go proxy/ecosystem_ops_test.go
git commit -m "feat: expose opus fleet readiness contract"
```

## Task 4: Background Quiet Mode

**Files:**
- Modify: `proxy/account_refresh.go`
- Modify: `proxy/account_health.go`
- Test: `proxy/account_refresh_test.go`
- Test: `proxy/account_health_test.go`

- [ ] **Step 1: Write failing tests for quiet-mode skip status**

Add to `proxy/account_refresh_test.go`:

```go
func TestAutoRefreshStatusRecordsQuietModeSkips(t *testing.T) {
	h := &Handler{}
	h.finishAutoRefresh(autoRefreshBatchResult{Success: 0, Failed: 0, Skipped: 3, QuietSkipped: 2}, 100, 200)

	status := h.getAutoRefreshStatus()
	if status.LastSkippedCount != 3 {
		t.Fatalf("LastSkippedCount = %d, want 3", status.LastSkippedCount)
	}
	if status.LastQuietSkipped != 2 {
		t.Fatalf("LastQuietSkipped = %d, want 2", status.LastQuietSkipped)
	}
}
```

Add to `proxy/account_health_test.go`:

```go
func TestHealthCheckStatusRecordsQuietModeSkips(t *testing.T) {
	h := &Handler{}
	h.finishHealthCheck(healthCheckBatchResult{Success: 0, Failed: 0, Disabled: 0, Skipped: 4, QuietSkipped: 3}, 100, 200)

	status := h.getHealthCheckStatus()
	if status.LastSkippedCount != 4 {
		t.Fatalf("LastSkippedCount = %d, want 4", status.LastSkippedCount)
	}
	if status.LastQuietSkipped != 3 {
		t.Fatalf("LastQuietSkipped = %d, want 3", status.LastQuietSkipped)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./proxy -run 'TestAutoRefreshStatusRecordsQuietModeSkips|TestHealthCheckStatusRecordsQuietModeSkips' -count=1
```

Expected: FAIL because `QuietSkipped` and `LastQuietSkipped` fields do not exist.

- [ ] **Step 3: Add quiet-mode fields**

In `proxy/account_refresh.go`, add to `autoRefreshBatchResult`:

```go
QuietSkipped int
```

Add to `autoRefreshStatus`:

```go
LastQuietSkipped int `json:"lastQuietSkipped"`
```

In `finishAutoRefresh`, set:

```go
h.autoRefreshStatus.LastQuietSkipped = result.QuietSkipped
```

In `proxy/account_health.go`, add the same fields to `healthCheckBatchResult` and `healthCheckStatus`, and set `LastQuietSkipped` in `finishHealthCheck`.

- [ ] **Step 4: Add quiet-mode decision helper**

Create in `proxy/account_health.go` near status helpers:

```go
func opusQuietModeActive() bool {
	if modelAdmissionGate == nil {
		return false
	}
	snap := modelAdmissionGate.modelSnapshot("claude-opus-4.7")
	return snap.CircuitState == "open" || snap.CircuitState == "degraded" || snap.Score >= 4
}
```

In account selection loops for health check and auto refresh, skip accounts with active cooldown when `opusQuietModeActive()` is true. Use the existing account fields:

```go
if opusQuietModeActive() && account.CooldownUntil > time.Now().Unix() {
	result.Skipped++
	result.QuietSkipped++
	continue
}
```

If the selection loop currently returns a slice before processing, add a small helper:

```go
func shouldSkipBackgroundAccountForQuietMode(account config.Account, now time.Time) bool {
	return opusQuietModeActive() && account.CooldownUntil > now.Unix()
}
```

Use this helper in both background paths so tests can be added later without live network calls.

- [ ] **Step 5: Run background tests**

Run:

```bash
go test ./proxy -run 'TestAutoRefresh|TestHealthCheck|QuietMode' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add proxy/account_refresh.go proxy/account_health.go proxy/account_refresh_test.go proxy/account_health_test.go
git commit -m "feat: quiet background checks during opus pressure"
```

## Task 5: Admin UI And README Operator Surface

**Files:**
- Modify: `web/index.html`
- Modify: `README.md`
- Modify: `README_CN.md`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write lightweight UI contract test**

Add to `proxy/handler_test.go` near existing `web/index.html` string tests:

```go
func TestAdminUIContainsOpusGovernorFleetFields(t *testing.T) {
	data, err := os.ReadFile("../web/index.html")
	if err != nil {
		t.Fatalf("read web/index.html: %v", err)
	}
	html := string(data)
	for _, want := range []string{
		"opusFleetHealth",
		"circuitState",
		"safeConcurrency",
		"retryAfterSeconds",
		"locallySchedulableAccounts",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("web/index.html missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
go test ./proxy -run TestAdminUIContainsOpusGovernorFleetFields -count=1
```

Expected: FAIL until UI fields are added.

- [ ] **Step 3: Add UI panel**

In `web/index.html`, inside the API tab near the existing Claude Code readiness block, add:

```html
<div class="card" id="opusFleetHealth">
    <div class="card-header">
        <span class="card-title" data-i18n="api.opusFleetHealth"></span>
        <button class="btn btn-secondary" onclick="loadOpusFleetHealth()" data-i18n="common.refresh"></button>
    </div>
    <div id="opusFleetHealthBody" class="status-grid"></div>
</div>
```

Add i18n keys in both `zh` and `en` maps:

```javascript
'api.opusFleetHealth': 'Opus 4.7 舰队健康',
'api.opusFleetStatus': '状态',
'api.opusFleetCircuit': '断路器',
'api.opusFleetSafeConcurrency': '安全并发',
'api.opusFleetRetryAfter': '重试等待',
'api.opusFleetSchedulable': '可调度账号',
```

English:

```javascript
'api.opusFleetHealth': 'Opus 4.7 Fleet Health',
'api.opusFleetStatus': 'Status',
'api.opusFleetCircuit': 'Circuit',
'api.opusFleetSafeConcurrency': 'Safe concurrency',
'api.opusFleetRetryAfter': 'Retry after',
'api.opusFleetSchedulable': 'Schedulable accounts',
```

Add JS:

```javascript
async function loadOpusFleetHealth() {
    const password = getAdminPassword();
    const body = document.getElementById('opusFleetHealthBody');
    if (!body) return;
    try {
        const resp = await fetch('/admin/api/fleet/readiness?model=claude-opus-4-7', {
            headers: { 'X-Admin-Password': password }
        });
        const data = await resp.json();
        body.innerHTML = `
            <div><strong>${t('api.opusFleetStatus')}:</strong> ${escapeHtml(data.status || '-')}</div>
            <div><strong>${t('api.opusFleetCircuit')}:</strong> ${escapeHtml(data.circuitState || '-')}</div>
            <div><strong>${t('api.opusFleetSafeConcurrency')}:</strong> ${data.safeConcurrency ?? '-'}</div>
            <div><strong>${t('api.opusFleetRetryAfter')}:</strong> ${data.retryAfterSeconds ?? 0}s</div>
            <div><strong>${t('api.opusFleetSchedulable')}:</strong> ${data.locallySchedulableAccounts ?? '-'}</div>
        `;
    } catch (err) {
        body.textContent = err.message || String(err);
    }
}
```

Call `loadOpusFleetHealth()` from the same place that loads API/readiness tab data.

- [ ] **Step 4: Update README docs**

In `README.md`, add under the existing Claude Code/Admin diagnostics notes:

```markdown
### Opus 4.7 Health Governor

For downstream sub2api routing, check `GET /admin/api/fleet/readiness?model=claude-opus-4-7` before sending Opus 4.7 traffic. Route normally only when `status=healthy`; queue or reduce concurrency when `status=degraded`; stop new Opus calls and honor `retryAfterSeconds` when `status=blocked`.

Kiro-Go returns retryable Opus pressure as HTTP 429 with `Retry-After`, `X-Kiro-Go-Error-Reason`, `X-Kiro-Go-Circuit-State`, `X-Kiro-Go-Retryable`, and `X-Kiro-Go-Safe-Concurrency`. Treat those as controlled backoff signals, not as permission to fan out more retries.
```

In `README_CN.md`, add:

```markdown
### Opus 4.7 健康治理

sub2api 下游路由前应先检查 `GET /admin/api/fleet/readiness?model=claude-opus-4-7`。`status=healthy` 时正常路由；`status=degraded` 时排队或降低并发；`status=blocked` 时停止新的 Opus 请求并遵守 `retryAfterSeconds`。

Kiro-Go 会把可重试的 Opus 压力返回为 HTTP 429，并带上 `Retry-After`、`X-Kiro-Go-Error-Reason`、`X-Kiro-Go-Circuit-State`、`X-Kiro-Go-Retryable`、`X-Kiro-Go-Safe-Concurrency`。这些是受控退避信号，不应触发下游放大重试。
```

- [ ] **Step 5: Run UI/doc tests**

Run:

```bash
go test ./proxy -run 'TestAdminUIContainsOpusGovernorFleetFields|TestAdmin' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/index.html README.md README_CN.md proxy/handler_test.go
git commit -m "docs: surface opus governor operations"
```

## Task 6: UAT Evidence Harness

**Files:**
- Create: `docs/superpowers/uat/opus47-health-governor/README.md`
- Create: `docs/superpowers/uat/opus47-health-governor/run-opus47-governor-uat.js`

- [ ] **Step 1: Create UAT README**

Create `docs/superpowers/uat/opus47-health-governor/README.md`:

```markdown
# Opus 4.7 Health Governor UAT

This harness verifies the latest Docker Kiro-Go + sub2api environment with real API, browser, log, and usage evidence.

Required environment:

- `KIRO_GO_BASE_URL`, default `http://127.0.0.1:8080`
- `KIRO_GO_ADMIN_PASSWORD`
- `KIRO_GO_API_KEY`
- `SUB2API_BASE_URL`, default `http://127.0.0.1:3000`
- `SUB2API_API_KEY`
- `UAT_DURATION_MS`, default `1800000`

Evidence rules:

- Do not print or save API keys, admin passwords, refresh tokens, cookies, or raw config contents.
- Save API summaries, request-log summaries, readiness snapshots, and screenshot paths under a timestamped `runs/` directory.
- Mark `PASS` only when health, fleet readiness, request logs, sub2api usage/database evidence, and screenshot analysis agree.
- Mark `BLOCKED_BY_UPSTREAM` when upstream Opus 4.7 is unavailable for the whole window.
```

- [ ] **Step 2: Create UAT script**

Create `docs/superpowers/uat/opus47-health-governor/run-opus47-governor-uat.js`:

```javascript
#!/usr/bin/env node
const fs = require('fs');
const path = require('path');

const startedAt = new Date();
const runId = startedAt.toISOString().replace(/[:.]/g, '-');
const outDir = path.join(__dirname, 'runs', runId);
fs.mkdirSync(outDir, { recursive: true });

const env = {
  kiroBase: process.env.KIRO_GO_BASE_URL || 'http://127.0.0.1:8080',
  subBase: process.env.SUB2API_BASE_URL || 'http://127.0.0.1:3000',
  adminPassword: process.env.KIRO_GO_ADMIN_PASSWORD || '',
  kiroApiKey: process.env.KIRO_GO_API_KEY || '',
  subApiKey: process.env.SUB2API_API_KEY || '',
  durationMs: Number(process.env.UAT_DURATION_MS || 1800000),
};

function redact(value) {
  if (!value) return '';
  return '[REDACTED]';
}

async function fetchJson(url, options = {}) {
  const started = Date.now();
  const resp = await fetch(url, options);
  const text = await resp.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = { raw: text.slice(0, 500) }; }
  return { status: resp.status, ok: resp.ok, durationMs: Date.now() - started, body };
}

async function sample() {
  const headers = env.adminPassword ? { 'X-Admin-Password': env.adminPassword } : {};
  const health = await fetchJson(`${env.kiroBase}/health`);
  const fleet = await fetchJson(`${env.kiroBase}/admin/api/fleet/readiness?model=claude-opus-4-7`, { headers });
  const model = await fetchJson(`${env.kiroBase}/admin/api/claude-code/model-readiness?model=claude-opus-4-7`, { headers });
  const logs = await fetchJson(`${env.kiroBase}/admin/api/request-logs?limit=50`, { headers });
  const subHealth = await fetchJson(`${env.subBase}/api/status`).catch(err => ({ ok: false, status: 0, error: err.message }));
  return { sampledAt: new Date().toISOString(), health, fleet, model, logs, subHealth };
}

function summarize(samples) {
  const last = samples[samples.length - 1] || {};
  const clientErrors = [];
  for (const sample of samples) {
    const entries = sample.logs && sample.logs.body && Array.isArray(sample.logs.body.logs) ? sample.logs.body.logs : [];
    for (const entry of entries) {
      if (entry.model === 'claude-opus-4.7' && entry.statusCode >= 400) {
        clientErrors.push({
          requestId: entry.requestId,
          statusCode: entry.statusCode,
          durationMs: entry.durationMs,
          attempts: entry.attempts,
          opusCircuitState: entry.opusCircuitState,
          opusAttemptBudget: entry.opusAttemptBudget,
          errorType: entry.errorType,
        });
      }
    }
  }
  const overBudget = clientErrors.filter(e => e.attempts > 4 || e.durationMs > 30000);
  const has503 = clientErrors.some(e => e.statusCode === 503);
  const fleet = last.fleet && last.fleet.body ? last.fleet.body : {};
  const pass = !has503 && overBudget.length === 0 && last.health && last.health.ok && last.subHealth && last.subHealth.ok;
  return { pass, has503, overBudget, clientErrorCount: clientErrors.length, lastFleet: fleet, clientErrors };
}

(async () => {
  const blockers = [];
  if (!env.adminPassword) blockers.push('KIRO_GO_ADMIN_PASSWORD missing');
  if (!env.kiroApiKey) blockers.push('KIRO_GO_API_KEY missing');
  if (!env.subApiKey) blockers.push('SUB2API_API_KEY missing');

  const samples = [];
  const end = Date.now() + env.durationMs;
  do {
    samples.push(await sample());
    await new Promise(resolve => setTimeout(resolve, Math.min(30000, Math.max(1000, end - Date.now()))));
  } while (Date.now() < end);

  const summary = {
    startedAt: startedAt.toISOString(),
    completedAt: new Date().toISOString(),
    env: {
      kiroBase: env.kiroBase,
      subBase: env.subBase,
      adminPassword: redact(env.adminPassword),
      kiroApiKey: redact(env.kiroApiKey),
      subApiKey: redact(env.subApiKey),
      durationMs: env.durationMs,
    },
    blockers,
    ...summarize(samples),
  };

  fs.writeFileSync(path.join(outDir, 'samples.json'), JSON.stringify(samples, null, 2));
  fs.writeFileSync(path.join(outDir, 'summary.json'), JSON.stringify(summary, null, 2));
  fs.writeFileSync(path.join(outDir, 'UAT-RESULT.md'), `# Opus 4.7 Health Governor UAT\n\nStatus: ${blockers.length ? 'BLOCKED_BY_ENV' : summary.pass ? 'PASS' : 'FAIL'}\n\nRun: ${runId}\n\nClient error count: ${summary.clientErrorCount}\n\nHas 503: ${summary.has503}\n\nOver budget: ${summary.overBudget.length}\n\nLast fleet status: ${summary.lastFleet.status || '-'}\n\nLast circuit state: ${summary.lastFleet.circuitState || '-'}\n`);
  console.log(JSON.stringify({ outDir, status: blockers.length ? 'BLOCKED_BY_ENV' : summary.pass ? 'PASS' : 'FAIL' }, null, 2));
  process.exit(blockers.length || !summary.pass ? 1 : 0);
})().catch(err => {
  fs.writeFileSync(path.join(outDir, 'error.txt'), err.stack || String(err));
  console.error(err);
  process.exit(1);
});
```

- [ ] **Step 3: Syntax check**

Run:

```bash
node --check docs/superpowers/uat/opus47-health-governor/run-opus47-governor-uat.js
```

Expected: PASS with no output.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/uat/opus47-health-governor/README.md docs/superpowers/uat/opus47-health-governor/run-opus47-governor-uat.js
git commit -m "test: add opus governor uat harness"
```

## Task 7: Final Verification And Real UAT

**Files:**
- Modify after run: `docs/superpowers/uat/opus47-health-governor/runs/<run-id>/UAT-RESULT.md`
- No source changes unless tests reveal defects.

- [ ] **Step 1: Run full Go tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Build latest Docker services without deleting data**

Run:

```bash
docker compose build
docker compose up -d
```

Expected: containers start without recreating data volumes destructively. Do not run `docker compose down -v`.

- [ ] **Step 3: Check service health**

Run:

```bash
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:3000/api/status
```

Expected: both return healthy JSON. If sub2api uses a different port, use the existing environment value from prior UAT scripts and record it in the UAT result.

- [ ] **Step 4: Run UAT harness**

Run with real environment values:

```bash
KIRO_GO_BASE_URL=http://127.0.0.1:8080 \
SUB2API_BASE_URL=http://127.0.0.1:3000 \
KIRO_GO_ADMIN_PASSWORD="$KIRO_GO_ADMIN_PASSWORD" \
KIRO_GO_API_KEY="$KIRO_GO_API_KEY" \
SUB2API_API_KEY="$SUB2API_API_KEY" \
UAT_DURATION_MS=1800000 \
node docs/superpowers/uat/opus47-health-governor/run-opus47-governor-uat.js
```

Expected: PASS, or `BLOCKED_BY_UPSTREAM`/FAIL with evidence. Do not edit the result to PASS manually.

- [ ] **Step 5: Browser evidence with Playwright MCP**

Use Playwright MCP if available in the session. If MCP tools are not hot-loaded, use the existing Playwright browser automation approach already used in this repo. Capture:

- Kiro-Go `/admin` API/readiness page showing Opus 4.7 fleet health.
- Kiro-Go request logs showing attempt budget and circuit fields.
- sub2api accounts page.
- sub2api usage page.

Expected: screenshots visually match fleet readiness and request-log API evidence.

- [ ] **Step 6: Analyze screenshots/API/database evidence**

Update the generated `UAT-RESULT.md` with:

```markdown
## Screenshot Analysis

- Kiro-Go fleet health screenshot: PASS/FAIL, reason.
- Kiro-Go request log screenshot: PASS/FAIL, reason.
- sub2api accounts screenshot: PASS/FAIL, reason.
- sub2api usage screenshot: PASS/FAIL, reason.

## Evidence Consistency

- API vs screenshot: PASS/FAIL.
- request logs vs sub2api usage/database: PASS/FAIL.
- retry budget respected: PASS/FAIL.
- no client-visible 503: PASS/FAIL.
- data loss check: PASS/FAIL.
```

- [ ] **Step 7: Commit UAT result if successful or diagnostically useful**

```bash
git add docs/superpowers/uat/opus47-health-governor
git commit -m "test: record opus governor uat results"
```

Expected: commit contains evidence only, no secrets.

## Self-Review

- Spec coverage: model circuit is Task 1; request budget and pressure headers are Task 2; fleet readiness contract is Task 3; quiet mode is Task 4; Admin UI and docs are Task 5; Docker/API/browser/database UAT evidence is Task 6 and Task 7; data safety is included in Task 7 and UAT README.
- Placeholder scan: no `TBD`, no `TODO`, no "implement later"; code snippets and commands are concrete.
- Type consistency: `AdmissionPressureSnapshot.CircuitState`, `RetryAfterSeconds`, `LastPressureReason`, request log Opus fields, and readiness response field names are used consistently across tasks.
