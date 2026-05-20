# sub2api Opus 4.7 Content Continuity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure sub2api-facing Opus 4.7 requests are not counted as successful unless Kiro-Go returns real model content, and reduce HTTP 200 empty completions by queueing through short upstream pressure windows.

**Architecture:** Add content-success observability first, then add a bounded Opus 4.7 continuity gate that waits for admission recovery before StableDownstream fallback. Keep StableDownstream HTTP status protection, but demote empty fallbacks to transport-only completion in logs, readiness, and UAT.

**Tech Stack:** Go 1.21, standard `testing`, Docker Compose, Kiro-Go admin APIs, sub2api PostgreSQL, Node.js UAT scripts.

---

## File Structure

- Modify `config/config.go`: add `ContentContinuityConfig` defaults, normalization, and validation.
- Modify `proxy/request_log.go`: add content-success log fields and helpers.
- Modify `proxy/request_log_test.go`: cover fallback/content-success marking and stats aggregation.
- Add `proxy/content_continuity.go`: bounded per-model wait gate for StableDownstream Opus 4.7 pressure windows.
- Add `proxy/content_continuity_test.go`: unit tests for the continuity wait gate.
- Modify `proxy/handler.go`: mark content success/failure, route admission pressure through continuity wait, and preserve non-stable behavior.
- Modify `proxy/handler_test.go`: contract tests for StableDownstream fallback demotion and admission wait behavior.
- Modify `proxy/ecosystem_ops.go`: expose recent content success rate and empty completion/fallback counts in fleet readiness.
- Modify `proxy/ecosystem_ops_test.go`: readiness tests for content-success diagnostics.
- Modify `docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js`: fail UAT on empty completions, `output_tokens=0`, or stable fallback evidence.
- Modify `docs/superpowers/uat/sub2api-opus47-stable-200/README.md`: document content correctness criteria.
- Modify `README.md` and `README_CN.md`: clarify that StableDownstream protects transport, while content continuity requires real upstream output.

---

### Task 1: Add Content Continuity Configuration

**Files:**
- Modify: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 1: Write failing config default test**

Add to `config/config_test.go`:

```go
func TestContentContinuityDefaultsEnableOpus47(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := Get()
	if cfg == nil {
		t.Fatalf("config is nil")
	}
	if !cfg.ContentContinuity.Enabled {
		t.Fatalf("ContentContinuity.Enabled = false, want true")
	}
	if cfg.ContentContinuity.MaxQueueWaitSeconds != 120 {
		t.Fatalf("MaxQueueWaitSeconds = %d, want 120", cfg.ContentContinuity.MaxQueueWaitSeconds)
	}
	if cfg.ContentContinuity.MaxQueueDepth != 300 {
		t.Fatalf("MaxQueueDepth = %d, want 300", cfg.ContentContinuity.MaxQueueDepth)
	}
	if cfg.ContentContinuity.MinContentTokens != 1 {
		t.Fatalf("MinContentTokens = %d, want 1", cfg.ContentContinuity.MinContentTokens)
	}
	if !cfg.ContentContinuity.SupportsModel("claude-opus-4.7") {
		t.Fatalf("expected content continuity to support claude-opus-4.7")
	}
	if cfg.ContentContinuity.SupportsModel("claude-sonnet-4.5") {
		t.Fatalf("did not expect content continuity to support sonnet by default")
	}
}
```

- [ ] **Step 2: Run config test to verify it fails**

Run:

```bash
go test ./config -run TestContentContinuityDefaultsEnableOpus47 -count=1 -v
```

Expected: FAIL because `ContentContinuity` is not defined on `Config`.

- [ ] **Step 3: Implement config type and defaults**

In `config/config.go`, add near `StableDownstreamConfig`:

```go
type ContentContinuityConfig struct {
	Enabled                bool     `json:"enabled"`
	Models                 []string `json:"models"`
	MaxQueueWaitSeconds    int      `json:"maxQueueWaitSeconds"`
	MaxQueueDepth          int      `json:"maxQueueDepth"`
	MinContentTokens       int      `json:"minContentTokens"`
	StreamHeartbeatSeconds int      `json:"streamHeartbeatSeconds"`
}

func (c ContentContinuityConfig) SupportsModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if !c.Enabled || model == "" {
		return false
	}
	for _, candidate := range c.Models {
		if strings.ToLower(strings.TrimSpace(candidate)) == model {
			return true
		}
	}
	return false
}

func defaultContentContinuityConfig() ContentContinuityConfig {
	return ContentContinuityConfig{
		Enabled:                true,
		Models:                 []string{"claude-opus-4.7"},
		MaxQueueWaitSeconds:    120,
		MaxQueueDepth:          300,
		MinContentTokens:       1,
		StreamHeartbeatSeconds: 10,
	}
}

func normalizeContentContinuityConfig(in ContentContinuityConfig) ContentContinuityConfig {
	defaults := defaultContentContinuityConfig()
	if len(in.Models) == 0 {
		in.Models = defaults.Models
	}
	if in.MaxQueueWaitSeconds <= 0 {
		in.MaxQueueWaitSeconds = defaults.MaxQueueWaitSeconds
	}
	if in.MaxQueueDepth <= 0 {
		in.MaxQueueDepth = defaults.MaxQueueDepth
	}
	if in.MinContentTokens <= 0 {
		in.MinContentTokens = defaults.MinContentTokens
	}
	if in.StreamHeartbeatSeconds <= 0 {
		in.StreamHeartbeatSeconds = defaults.StreamHeartbeatSeconds
	}
	return in
}
```

Add field to `Config`:

```go
ContentContinuity ContentContinuityConfig `json:"contentContinuity"`
```

Initialize it in `defaultConfig()`:

```go
ContentContinuity: defaultContentContinuityConfig(),
```

Normalize it in config load after JSON unmarshal:

```go
c.ContentContinuity = normalizeContentContinuityConfig(c.ContentContinuity)
```

- [ ] **Step 4: Run config tests**

Run:

```bash
go test ./config -run 'ContentContinuity|StableDownstream' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): add content continuity defaults"
```

---

### Task 2: Add Content Success Request Log Semantics

**Files:**
- Modify: `proxy/request_log.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Write failing request log tests**

Add to `proxy/request_log_test.go`:

```go
func TestRequestLogMarksStableFallbackAsContentFailure(t *testing.T) {
	entry := RequestLogEntry{}
	markRequestLogStableFallback(&entry, "admission_pressure", http.StatusServiceUnavailable)
	if !entry.StableDownstreamFallback {
		t.Fatalf("expected stable fallback")
	}
	if entry.ContentSuccess {
		t.Fatalf("stable fallback must not be content success")
	}
	if entry.ContentFailureReason != "admission_pressure" {
		t.Fatalf("ContentFailureReason = %q, want admission_pressure", entry.ContentFailureReason)
	}
	if !entry.StableFallbackFinal {
		t.Fatalf("expected StableFallbackFinal true")
	}
}

func TestRequestLogMarksRealContentSuccess(t *testing.T) {
	entry := RequestLogEntry{}
	markRequestLogContentSuccess(&entry, 17)
	if !entry.ContentSuccess {
		t.Fatalf("expected content success")
	}
	if entry.UpstreamContentTokens != 17 {
		t.Fatalf("UpstreamContentTokens = %d, want 17", entry.UpstreamContentTokens)
	}
	if entry.ContentFailureReason != "" {
		t.Fatalf("ContentFailureReason = %q, want empty", entry.ContentFailureReason)
	}
}
```

- [ ] **Step 2: Run request log tests to verify failure**

Run:

```bash
go test ./proxy -run 'TestRequestLogMarksStableFallbackAsContentFailure|TestRequestLogMarksRealContentSuccess' -count=1 -v
```

Expected: FAIL because fields and helper do not exist.

- [ ] **Step 3: Add request log fields and helpers**

In `RequestLogEntry`, add near StableDownstream fields:

```go
ContentSuccess       bool   `json:"contentSuccess,omitempty"`
ContentFailureReason string `json:"contentFailureReason,omitempty"`
UpstreamContentTokens int   `json:"upstreamContentTokens,omitempty"`
StableFallbackFinal  bool   `json:"stableFallbackFinal,omitempty"`
QueuedForCapacity    bool   `json:"queuedForCapacity,omitempty"`
CapacityQueueWaitMs  int64  `json:"capacityQueueWaitMs,omitempty"`
```

Add helpers in `proxy/request_log.go` near `markRequestLogStableFallback`:

```go
func markRequestLogContentSuccess(entry *RequestLogEntry, tokens int) {
	if entry == nil {
		return
	}
	entry.ContentSuccess = true
	entry.ContentFailureReason = ""
	if tokens > 0 {
		entry.UpstreamContentTokens = tokens
	}
}

func updateRequestLogContentSuccess(r *http.Request, tokens int) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	markRequestLogContentSuccess(&ctx.entry, tokens)
}

func markRequestLogContentFailure(entry *RequestLogEntry, reason string) {
	if entry == nil {
		return
	}
	entry.ContentSuccess = false
	entry.ContentFailureReason = strings.TrimSpace(reason)
}

func updateRequestLogContentFailure(r *http.Request, reason string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	markRequestLogContentFailure(&ctx.entry, reason)
}

func updateRequestLogCapacityQueue(r *http.Request, waited time.Duration) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.QueuedForCapacity = true
	ctx.entry.CapacityQueueWaitMs = waited.Milliseconds()
}
```

Update `markRequestLogStableFallback`:

```go
func markRequestLogStableFallback(entry *RequestLogEntry, reason string, suppressedStatus int) {
	if entry == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	entry.StableDownstreamFallback = true
	entry.StableFallbackReason = reason
	entry.SuppressedDownstreamStatus = suppressedStatus
	entry.StableFallbackFinal = true
	markRequestLogContentFailure(entry, reason)
}
```

- [ ] **Step 4: Run request log tests**

Run:

```bash
go test ./proxy -run 'TestRequestLogMarksStableFallbackAsContentFailure|TestRequestLogMarksRealContentSuccess|TestRequestLogRecordsStableDownstreamFallback' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/request_log.go proxy/request_log_test.go
git commit -m "feat(proxy): log content success separately"
```

---

### Task 3: Add Content Continuity Gate

**Files:**
- Add: `proxy/content_continuity.go`
- Add: `proxy/content_continuity_test.go`

- [ ] **Step 1: Write failing continuity gate tests**

Create `proxy/content_continuity_test.go`:

```go
package proxy

import (
	"testing"
	"time"
)

func TestContentContinuityGateWaitsUntilCapacityRecovers(t *testing.T) {
	gate := newContentContinuityGate()
	started := make(chan struct{})
	done := make(chan contentContinuityWaitResult, 1)
	go func() {
		close(started)
		done <- gate.wait("claude-opus-4.7", 100*time.Millisecond, func() bool {
			return false
		})
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
	gate.broadcast("claude-opus-4.7")
	select {
	case got := <-done:
		if !got.Waited {
			t.Fatalf("expected waited result")
		}
		if got.TimedOut {
			t.Fatalf("did not expect timeout")
		}
	case <-time.After(time.Second):
		t.Fatalf("wait did not return after broadcast")
	}
}

func TestContentContinuityGateTimesOut(t *testing.T) {
	gate := newContentContinuityGate()
	got := gate.wait("claude-opus-4.7", time.Millisecond, func() bool {
		return true
	})
	if !got.Waited {
		t.Fatalf("expected waited result")
	}
	if !got.TimedOut {
		t.Fatalf("expected timeout")
	}
	if got.Duration <= 0 {
		t.Fatalf("expected positive duration")
	}
}

func TestContentContinuityGateRejectsWhenQueueFull(t *testing.T) {
	gate := newContentContinuityGate()
	gate.setMaxDepthForTest(1)
	release, ok := gate.tryEnter("claude-opus-4.7")
	if !ok {
		t.Fatalf("expected first waiter to enter")
	}
	defer release()
	if _, ok := gate.tryEnter("claude-opus-4.7"); ok {
		t.Fatalf("expected second waiter to be rejected")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./proxy -run 'TestContentContinuityGate' -count=1 -v
```

Expected: FAIL because the gate does not exist.

- [ ] **Step 3: Implement continuity gate**

Create `proxy/content_continuity.go`:

```go
package proxy

import (
	"strings"
	"sync"
	"time"
)

type contentContinuityGate struct {
	mu       sync.Mutex
	notify   map[string]chan struct{}
	waiting  map[string]int
	maxDepth int
}

type contentContinuityWaitResult struct {
	Waited   bool
	TimedOut bool
	Duration time.Duration
}

func newContentContinuityGate() *contentContinuityGate {
	return &contentContinuityGate{
		notify:   make(map[string]chan struct{}),
		waiting:  make(map[string]int),
		maxDepth: 300,
	}
}

func (g *contentContinuityGate) setMaxDepthForTest(maxDepth int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.maxDepth = maxDepth
}

func (g *contentContinuityGate) tryEnter(model string) (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	model = normalizeAdmissionModel(model)
	if model == "" {
		model = "default"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maxDepth > 0 && g.waiting[model] >= g.maxDepth {
		return nil, false
	}
	g.waiting[model]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			if g.waiting[model] > 0 {
				g.waiting[model]--
			}
			g.mu.Unlock()
		})
	}, true
}

func (g *contentContinuityGate) channelLocked(model string) chan struct{} {
	ch := g.notify[model]
	if ch == nil {
		ch = make(chan struct{})
		g.notify[model] = ch
	}
	return ch
}

func (g *contentContinuityGate) wait(model string, timeout time.Duration, stillBlocked func() bool) contentContinuityWaitResult {
	start := time.Now()
	result := contentContinuityWaitResult{Waited: true}
	if g == nil || timeout <= 0 {
		result.TimedOut = true
		result.Duration = time.Since(start)
		return result
	}
	release, ok := g.tryEnter(model)
	if !ok {
		result.TimedOut = true
		result.Duration = time.Since(start)
		return result
	}
	defer release()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	model = normalizeAdmissionModel(model)
	if strings.TrimSpace(model) == "" {
		model = "default"
	}
	for {
		if stillBlocked != nil && !stillBlocked() {
			result.Duration = time.Since(start)
			return result
		}
		g.mu.Lock()
		ch := g.channelLocked(model)
		g.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			result.TimedOut = true
			result.Duration = time.Since(start)
			return result
		}
	}
}

func (g *contentContinuityGate) broadcast(model string) {
	if g == nil {
		return
	}
	model = normalizeAdmissionModel(model)
	if model == "" {
		model = "default"
	}
	g.mu.Lock()
	ch := g.channelLocked(model)
	close(ch)
	g.notify[model] = make(chan struct{})
	g.mu.Unlock()
}
```

- [ ] **Step 4: Run gate tests**

Run:

```bash
go test ./proxy -run 'TestContentContinuityGate' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/content_continuity.go proxy/content_continuity_test.go
git commit -m "feat(proxy): add content continuity gate"
```

---

### Task 4: Mark Real Claude/OpenAI Content Success

**Files:**
- Modify: `proxy/handler.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing tests for content success and fallback failure**

Add to `proxy/handler_test.go`:

```go
func TestStableClaudeFallbackMarksContentFailure(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	loggedReq := requestWithLogContext(httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	w := httptest.NewRecorder()

	h.sendStableClaudeFallback(w, loggedReq, "claude-opus-4.7", "admission_pressure", errors.New("queue timeout"))

	entry := requestLogEntryFromRequest(t, loggedReq)
	if entry.ContentSuccess {
		t.Fatalf("stable fallback must not be content success: %#v", entry)
	}
	if entry.ContentFailureReason != "admission_pressure" {
		t.Fatalf("ContentFailureReason = %q, want admission_pressure", entry.ContentFailureReason)
	}
	if !entry.StableFallbackFinal {
		t.Fatalf("expected StableFallbackFinal")
	}
}

func TestMarkClaudeContentSuccessFromUsage(t *testing.T) {
	loggedReq := requestWithLogContext(httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	updateRequestLogContentSuccess(loggedReq, 3)

	entry := requestLogEntryFromRequest(t, loggedReq)
	if !entry.ContentSuccess {
		t.Fatalf("expected content success")
	}
	if entry.UpstreamContentTokens != 3 {
		t.Fatalf("UpstreamContentTokens = %d, want 3", entry.UpstreamContentTokens)
	}
}
```

If helper names differ, add these test helpers near existing request-log test helpers:

```go
func requestWithLogContext(r *http.Request) *http.Request {
	ctx := &requestLogContext{entry: RequestLogEntry{Timestamp: time.Now()}}
	return r.WithContext(context.WithValue(r.Context(), requestLogContextKey{}, ctx))
}

func requestLogEntryFromRequest(t *testing.T, r *http.Request) RequestLogEntry {
	t.Helper()
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		t.Fatalf("missing request log context")
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.entry
}
```

- [ ] **Step 2: Run tests**

Run:

```bash
go test ./proxy -run 'TestStableClaudeFallbackMarksContentFailure|TestMarkClaudeContentSuccessFromUsage' -count=1 -v
```

Expected: PASS after Task 2 helpers exist. If helper duplicate names conflict, reuse existing helpers and keep assertions unchanged.

- [ ] **Step 3: Add content success marking at final response points**

In `proxy/handler.go`, after successful non-fallback responses have usage/output tokens, call:

```go
if outputTokens > 0 || len(contentBlocks) > 0 || toolUseCount > 0 {
	updateRequestLogContentSuccess(r, outputTokens)
}
```

Use the actual local variable names in each successful path:

- Claude non-stream final JSON path: mark success when response content has non-empty text or tool_use.
- Claude stream path: mark success when first text delta, thinking delta, or tool_use is emitted.
- OpenAI chat path: mark success when choices contain non-empty message content or tool calls.
- OpenAI Responses path: mark success when output text or tool output exists.

If a path already updates `OutputTokens`, use that count as the token argument. If only text is available, pass `1` to indicate content exists.

- [ ] **Step 4: Add focused regression test for stable fallback body still empty**

Run existing test:

```bash
go test ./proxy -run 'TestStableDownstreamClaudeNoAccountsReturnsHTTP200|TestStableDownstreamClaudeStreamFallbackStartsHTTP200|TestStableDownstreamOpenAI' -count=1 -v
```

Expected: PASS; fallback remains protocol-valid and marker-free.

- [ ] **Step 5: Commit**

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat(proxy): mark real content success"
```

---

### Task 5: Route Stable Admission Pressure Through Continuity Wait

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/content_continuity.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing admission wait test**

Add to `proxy/handler_test.go`:

```go
func TestStableAdmissionPressureWaitsBeforeFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	loggedReq := requestWithLogContext(httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	loggedReq.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	w := httptest.NewRecorder()

	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	release, _, err := modelAdmissionGate.acquire("claude-opus-4.7", time.Second)
	if err != nil {
		t.Fatalf("pre-acquire model gate: %v", err)
	}
	defer release()

	start := time.Now()
	releaseReq, ok := h.acquireOpus47AdmissionForRequest(w, loggedReq, "claude-opus-4.7", false, true, time.Now().Add(30*time.Millisecond))
	if ok {
		releaseReq()
		t.Fatalf("expected admission fallback after continuity wait timeout")
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatalf("expected continuity wait before fallback, waited %s", time.Since(start))
	}
	entry := requestLogEntryFromRequest(t, loggedReq)
	if !entry.QueuedForCapacity {
		t.Fatalf("expected queuedForCapacity")
	}
	if entry.CapacityQueueWaitMs <= 0 {
		t.Fatalf("expected positive CapacityQueueWaitMs")
	}
	if entry.StableFallbackReason != "admission_pressure" {
		t.Fatalf("StableFallbackReason = %q, want admission_pressure", entry.StableFallbackReason)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
go test ./proxy -run TestStableAdmissionPressureWaitsBeforeFallback -count=1 -v
```

Expected: FAIL because `contentContinuityGateGlobal` and wait integration do not exist.

- [ ] **Step 3: Add global gate and config-aware timeout**

In `proxy/content_continuity.go`, add:

```go
var contentContinuityGateGlobal = newContentContinuityGate()

func contentContinuityWaitDuration(model string, deadline time.Time) time.Duration {
	cfg := config.Get()
	if cfg == nil || !cfg.ContentContinuity.SupportsModel(model) {
		return 0
	}
	wait := time.Duration(cfg.ContentContinuity.MaxQueueWaitSeconds) * time.Second
	if !deadline.IsZero() {
		if remaining := time.Until(deadline); remaining > 0 && remaining < wait {
			wait = remaining
		}
	}
	return wait
}
```

Add `kiro-go/config` import to `proxy/content_continuity.go`.

- [ ] **Step 4: Integrate wait before stable admission fallback**

In `acquireOpus47AdmissionForRequest`, replace direct stable fallback after `modelAdmissionGate.acquire` error with:

```go
if err == nil {
	updateRequestLogReliability(r, wait.Milliseconds(), 0, 0, -1)
	return release, true
}
if stableWait := contentContinuityWaitDuration(model, deadline); stableWait > 0 {
	result := contentContinuityGateGlobal.wait(model, stableWait, func() bool {
		_, _, acquireErr := modelAdmissionGate.acquire(model, time.Millisecond)
		if acquireErr == nil {
			return false
		}
		return true
	})
	updateRequestLogCapacityQueue(r, result.Duration)
	if !result.TimedOut {
		release, gated, retryErr := modelAdmissionGate.acquire(model, time.Until(deadline))
		if retryErr == nil {
			if !gated {
				return func() {}, true
			}
			updateRequestLogReliability(r, wait.Milliseconds()+result.Duration.Milliseconds(), 0, 0, -1)
			return release, true
		}
		err = retryErr
	}
}
h.recordFailure()
h.sendStableAdmissionFallback(w, r, model, stream, claudeFormat, err)
return nil, false
```

Important: if this exact probe acquisition leaks a slot, adjust implementation to immediately release the probe:

```go
probeRelease, _, acquireErr := modelAdmissionGate.acquire(model, time.Millisecond)
if acquireErr == nil {
	probeRelease()
	return false
}
return true
```

- [ ] **Step 5: Broadcast on pressure recovery**

In `modelAdmissionGateSet.recordSuccess`, after paths that increase effective concurrency or close half-open state, call:

```go
contentContinuityGateGlobal.broadcast(model)
```

Use the normalized model string already available in `recordSuccess`.

- [ ] **Step 6: Run admission wait tests**

Run:

```bash
go test ./proxy -run 'TestStableAdmissionPressureWaitsBeforeFallback|TestContentContinuityGate' -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add proxy/handler.go proxy/content_continuity.go proxy/handler_test.go
git commit -m "feat(proxy): queue stable opus admission pressure"
```

---

### Task 6: Expose Content Continuity In Fleet Readiness

**Files:**
- Modify: `proxy/ecosystem_ops.go`
- Test: `proxy/ecosystem_ops_test.go`

- [ ] **Step 1: Write failing readiness diagnostics test**

Add to `proxy/ecosystem_ops_test.go`:

```go
func TestFleetReadinessReportsRecentContentFailures(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	h.ensureRequestLogStore().Add(RequestLogEntry{
		Timestamp:              time.Now(),
		Model:                  "claude-opus-4.7",
		StatusCode:             http.StatusOK,
		Outcome:                "success",
		StableDownstreamFallback: true,
		StableFallbackReason:   "admission_pressure",
		ContentFailureReason:   "admission_pressure",
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4-7", nil)
	w := httptest.NewRecorder()

	h.apiFleetReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["recentStableFallbacks"] != float64(1) {
		t.Fatalf("recentStableFallbacks = %#v, want 1; body=%#v", body["recentStableFallbacks"], body)
	}
	if body["recentEmptyCompletions"] != float64(1) {
		t.Fatalf("recentEmptyCompletions = %#v, want 1; body=%#v", body["recentEmptyCompletions"], body)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
go test ./proxy -run TestFleetReadinessReportsRecentContentFailures -count=1 -v
```

Expected: FAIL because readiness fields do not exist.

- [ ] **Step 3: Implement readiness aggregation**

In `proxy/ecosystem_ops.go`, add helper:

```go
func contentContinuityReadinessStats(logs []RequestLogEntry, model string, now time.Time) map[string]interface{} {
	model = normalizeAdmissionModel(model)
	recent := 0
	contentSuccess := 0
	stableFallbacks := 0
	emptyCompletions := 0
	for _, entry := range logs {
		if now.Sub(entry.Timestamp) > 10*time.Minute {
			continue
		}
		if normalizeAdmissionModel(entry.Model) != model {
			continue
		}
		recent++
		if entry.ContentSuccess {
			contentSuccess++
		}
		if entry.StableDownstreamFallback {
			stableFallbacks++
		}
		if !entry.ContentSuccess && (entry.StableDownstreamFallback || entry.ContentFailureReason != "") {
			emptyCompletions++
		}
	}
	rate := 1.0
	if recent > 0 {
		rate = float64(contentSuccess) / float64(recent)
	}
	return map[string]interface{}{
		"recentContentRequests": recent,
		"contentSuccessRate":    rate,
		"recentStableFallbacks": stableFallbacks,
		"recentEmptyCompletions": emptyCompletions,
	}
}
```

In `apiFleetReadiness`, before encoding response:

```go
continuity := contentContinuityReadinessStats(h.ensureRequestLogStore().List(maxRequestLogLimit), mapped, time.Now())
```

Add fields to the response map:

```go
"recentContentRequests": continuity["recentContentRequests"],
"contentSuccessRate": continuity["contentSuccessRate"],
"recentStableFallbacks": continuity["recentStableFallbacks"],
"recentEmptyCompletions": continuity["recentEmptyCompletions"],
"recommendedQueueWaitSeconds": config.Get().ContentContinuity.MaxQueueWaitSeconds,
```

- [ ] **Step 4: Run readiness tests**

Run:

```bash
go test ./proxy -run 'TestFleetReadinessReportsRecentContentFailures|TestAPIFleetReadiness' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/ecosystem_ops.go proxy/ecosystem_ops_test.go
git commit -m "feat(proxy): expose content continuity readiness"
```

---

### Task 7: Strengthen sub2api Stable 200 UAT To Require Content

**Files:**
- Modify: `docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js`
- Modify: `docs/superpowers/uat/sub2api-opus47-stable-200/README.md`

- [ ] **Step 1: Update UAT script checks**

In `run-stable-200-uat.js`, replace the `ok` decision with content-aware parsing:

```js
function hasAnthropicContent(text, stream) {
  if (stream) {
    return /content_block_delta/.test(text) && /text_delta|input_json_delta|thinking_delta/.test(text);
  }
  try {
    const body = JSON.parse(text);
    const content = Array.isArray(body.content) ? body.content : [];
    return content.some(block => {
      if (!block || typeof block !== "object") return false;
      if (block.type === "text") return typeof block.text === "string" && block.text.trim().length > 0;
      if (block.type === "tool_use") return true;
      return false;
    });
  } catch {
    return false;
  }
}
```

Then set:

```js
const forbidden = [429, 502, 503].includes(res.status) ||
  /HTTP 429|HTTP 502|HTTP 503|kiro_go_stable_fallback|Opus 4\.7 is temporarily waiting/.test(text);
const contentOk = hasAnthropicContent(text, stream);
return {
  index: i,
  stream,
  status: res.status,
  ok: res.status === 200 && !forbidden && contentOk,
  contentOk,
  forbidden,
  sample: text.slice(0, 240)
};
```

- [ ] **Step 2: Update README pass criteria**

In `README.md`, add pass criteria:

```markdown
- Every successful response includes real assistant text, thinking, or tool_use content.
- Empty HTTP 200 completions fail this UAT even if the JSON/SSE envelope is valid.
- Kiro-Go request logs must show `contentSuccess=true` for successful samples.
```

- [ ] **Step 3: Check Node syntax**

Run:

```bash
node --check docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js docs/superpowers/uat/sub2api-opus47-stable-200/README.md
git commit -m "test(uat): require opus47 content success"
```

---

### Task 8: Documentation Updates

**Files:**
- Modify: `README.md`
- Modify: `README_CN.md`

- [ ] **Step 1: Update English README**

In `README.md` under `Opus 4.7 And sub2api`, replace the StableDownstream description with:

```markdown
- Stable downstream mode protects sub2api-facing generation responses from gateway-level HTTP `429`, `502`, and `503`.
- Content continuity is tracked separately: a response is only considered a content success when Kiro-Go receives real upstream assistant text, thinking, or tool calls.
- Empty StableDownstream fallback responses are transport-only completions and are recorded with `contentSuccess=false`.
```

- [ ] **Step 2: Update Chinese README**

In `README_CN.md` under the corresponding Opus 4.7/sub2api section, add:

```markdown
- StableDownstream 只保护 sub2api 下游 HTTP 契约，避免生成链路泄漏网关级 `429`、`502`、`503`。
- 内容连续性单独统计：只有收到真实上游 assistant 文本、thinking 或 tool_use 时才算 `contentSuccess=true`。
- 空的 StableDownstream fallback 只是传输层兜底，会记录为 `contentSuccess=false`，不能算正确回复。
```

- [ ] **Step 3: Verify docs mention content success**

Run:

```bash
rg -n 'contentSuccess|Content continuity|内容连续性|StableDownstream' README.md README_CN.md
```

Expected: output includes both English and Chinese content-success descriptions.

- [ ] **Step 4: Commit**

```bash
git add README.md README_CN.md
git commit -m "docs: clarify opus47 content continuity"
```

---

### Task 9: Focused Regression Tests

**Files:**
- No planned edits. If a regression fails, fix only the file named in the failing test output and re-run the same command before continuing.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
go test ./config ./proxy -run 'ContentContinuity|StableDownstream|FleetReadiness|RequestLogMarks|AdmissionPressure' -count=1 -v
```

Expected: PASS.

- [ ] **Step 2: Run Opus 4.7 focused regression**

Run:

```bash
go test ./pool ./proxy -run 'Opus47|ModelAdmission|NoAvailable|Stable' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full Go test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run UAT syntax check**

Run:

```bash
node --check docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Expected: PASS.

- [ ] **Step 5: Commit any regression fixes**

If any fixes were needed, stage only the files changed for that regression fix. Example:

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "fix: stabilize content continuity regressions"
```

If no fixes were needed, do not create an empty commit.

---

### Task 10: Docker Verification And Live sub2api UAT

**Files:**
- Modify: `docs/superpowers/uat/sub2api-opus47-stable-200/README.md` only if recording new run notes.

- [ ] **Step 1: Rebuild Kiro-Go container**

Run:

```bash
docker compose up -d --build kiro-go
```

Expected: `kiro-go-kiro-go-1` recreates and becomes healthy.

- [ ] **Step 2: Check Docker health**

Run:

```bash
docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}' | rg 'kiro-go|sub2api'
```

Expected: both `kiro-go-kiro-go-1` and `sub2api` are healthy.

- [ ] **Step 3: Check Kiro-Go readiness**

Run:

```bash
ADMIN_PASSWORD=$(jq -r '.password // empty' data/config.json)
curl -fsS -H "X-Admin-Password: $ADMIN_PASSWORD" \
  'http://127.0.0.1:8080/admin/api/fleet/readiness?model=claude-opus-4-7' | jq '{status,circuitState,safeConcurrency,contentSuccessRate,recentStableFallbacks,recentEmptyCompletions,recommendedQueueWaitSeconds}'
```

Expected: JSON includes the new content continuity fields.

- [ ] **Step 4: Run live content UAT when API key is available**

Run:

```bash
SUB2API_BASE_URL=http://127.0.0.1:18080 \
SUB2API_API_KEY="$SUB2API_API_KEY" \
ROUNDS=3 \
CONCURRENCY=2 \
node docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

Expected: PASS only if every response is HTTP `200` and content-bearing. If upstream Opus 4.7 is blocked, UAT should fail with `contentOk=false`; record this as upstream capacity pressure rather than a transport regression.

- [ ] **Step 5: Inspect recent Kiro-Go logs**

Run:

```bash
docker logs --since 10m kiro-go-kiro-go-1 2>&1 | rg -i 'panic|fatal|error|429|503|temporary|capacity|content|fallback|admission'
```

Expected: no panic/fatal; capacity pressure may appear and should correlate with readiness.

- [ ] **Step 6: Inspect recent request logs**

Run:

```bash
ADMIN_PASSWORD=$(jq -r '.password // empty' data/config.json)
curl -fsS -H "X-Admin-Password: $ADMIN_PASSWORD" \
  'http://127.0.0.1:8080/admin/api/request-logs?limit=100' |
  jq '{total:(.logs|length), statuses:(.logs|group_by(.statusCode)|map({status:.[0].statusCode,count:length})), contentFailures:(.logs|map(select(.contentSuccess != true and (.stableDownstreamFallback==true or (.contentFailureReason//"")!="")))|length), latest:(.logs|sort_by(.timestamp)|reverse|.[0:10]|map({ts:.timestamp,model:.model,status:.statusCode,contentSuccess:(.contentSuccess//false),reason:(.contentFailureReason//""),stable:(.stableDownstreamFallback//false),duration:.durationMs,queue:.capacityQueueWaitMs}))}'
```

Expected: content failures are visible and not hidden as healthy content success.

- [ ] **Step 7: Commit UAT result note if created**

If a UAT result file is added:

```bash
git add docs/superpowers/uat/sub2api-opus47-stable-200
git commit -m "test(uat): record opus47 content continuity run"
```

---

## Plan Self-Review

- Spec coverage: Tasks cover config, request log semantics, bounded queue, StableDownstream demotion, readiness fields, UAT content checks, docs, Go tests, and Docker/sub2api verification.
- Placeholder scan: No task uses TBD/TODO/fill-in placeholders. Code steps include exact snippets and commands.
- Type consistency: `ContentContinuityConfig`, `contentContinuityGate`, `ContentSuccess`, `ContentFailureReason`, `StableFallbackFinal`, `QueuedForCapacity`, and `CapacityQueueWaitMs` are introduced before later tasks reference them.
