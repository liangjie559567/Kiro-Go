# Phase 04: Opus 4.7 Readiness Contract and Scheduler Truth - Pattern Map

**Mapped:** 2026-05-21
**Files analyzed:** 10
**Analogs found:** 10 / 10

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `proxy/ecosystem_ops.go` | controller/utility | request-response + transform | `proxy/ecosystem_ops.go` scheduler/readiness handlers | exact |
| `proxy/request_log.go` | utility/store | event-driven request logging + transform | `proxy/request_log.go` content/fallback helpers | exact |
| `proxy/handler.go` | controller/service | request-response + streaming + retry | `proxy/handler.go` Opus retry/content paths | exact |
| `pool/account.go` | service/store | CRUD-ish runtime state + request selection | `pool/account.go` model-aware routing and health comparator | exact |
| `pool/breaker.go` | utility/store | event-driven model breaker state | `pool/breaker.go` breaker key/state helpers | exact |
| `web/index.html` | component | client-side fetch + DOM render | `web/index.html` existing fleet readiness card | exact |
| `proxy/ecosystem_ops_test.go` | test | HTTP handler/request-response | existing fleet readiness and scheduler tests | exact |
| `proxy/request_log_test.go` | test | logging utility + event assertions | existing request-log content/fallback tests | exact |
| `proxy/handler_test.go` | test | handler integration + retry/streaming | existing Opus retry and content tests | exact |
| `pool/account_test.go` | test | service unit + routing selection | existing pool routing/breaker tests | exact |

## Pattern Assignments

### `proxy/ecosystem_ops.go` (controller/utility, request-response + transform)

**Analog:** `proxy/ecosystem_ops.go`

**Imports pattern** (lines 3-11):
```go
import (
	"encoding/json"
	"fmt"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"sort"
	"strings"
	"time"
)
```

**Admin API response pattern** (lines 268-282):
```go
func (h *Handler) apiGetSchedulerPreview(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		model = "claude-sonnet-4.5"
	}
	mapped, _ := resolveClaudeThinkingMode(model, nil, config.GetThinkingConfig().Suffix)
	rows := h.schedulerPreviewRows(mapped)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"requestedModel": model,
		"mappedModel":    mapped,
		"strategy":       config.GetLoadBalanceConfig().Strategy,
		"accounts":       rows,
		"preferred":      preferredSchedulerPreviewRows(rows),
		"readOnly":       true,
	})
}
```

**Shared eligibility row pattern to replace/extend** (lines 285-315):
```go
func (h *Handler) schedulerPreviewRows(model string) []map[string]interface{} {
	accounts := config.GetAccounts()
	rows := make([]map[string]interface{}, 0, len(accounts))
	now := time.Now()
	nowUnix := now.Unix()
	for _, account := range accounts {
		reason := "eligible"
		eligible := true
		if !account.Enabled {
			eligible, reason = false, "disabled"
		} else if account.CooldownUntil > nowUnix {
			eligible, reason = false, nonEmpty(account.LastFailureReason, "cooling_down")
		} else if account.ExpiresAt > 0 && nowUnix > account.ExpiresAt-tokenRefreshSkewSeconds {
			eligible, reason = false, "token_expired"
		} else if readinessAccountUsageBlocked(account) {
			eligible, reason = false, "usage_limit_reached"
		} else if h != nil && h.pool != nil {
			models := h.pool.GetModelList(account.ID)
			if len(models) > 0 && !stringSliceEqualFoldContains(models, model) {
				eligible, reason = false, "model_not_listed"
			}
		}
		rows = append(rows, map[string]interface{}{
			"id":            account.ID,
			"email":         maskReadinessEmail(account.Email),
			"eligible":      eligible,
			"reason":        reason,
			"weight":        account.Weight,
			"runtimeHealth": runtimeHealthForAccount(h, account.ID),
			"modelsCached":  modelListForAccount(h, account.ID),
		})
	}
```

**Fleet readiness contract pattern** (lines 342-435):
```go
func (h *Handler) apiGetFleetReadiness(w http.ResponseWriter, r *http.Request) {
	accounts := config.GetAccounts()
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		model = "claude-sonnet-4.5"
	}
	mapped, _ := resolveClaudeThinkingMode(model, nil, config.GetThinkingConfig().Suffix)
	rows := h.schedulerPreviewRows(mapped)
	summary := map[string]int{"total": len(accounts), "enabled": 0, "eligible": 0, "disabled": 0, "coolingDown": 0, "quotaBlocked": 0, "modelNotListed": 0}
	// ...
	snap := AdmissionPressureSnapshot{}
	if modelAdmissionGate != nil {
		snap = modelAdmissionGate.modelSnapshot(mapped)
	}
	blockState := pool.ModelBlockState{}
	if h != nil && h.pool != nil {
		blockState = h.pool.ModelBlockState(mapped, time.Now())
	}
	// ...
	json.NewEncoder(w).Encode(map[string]interface{}{
		"model":                      model,
		"requestedModel":             model,
		"mappedModel":                mapped,
		"status":                     status,
		"circuitState":               firstNonEmpty(snap.CircuitState, "closed"),
		"retryAfterSeconds":          snap.RetryAfterSeconds,
		"safeConcurrency":            safeConcurrency,
		"locallySchedulableAccounts": locallySchedulable,
		"summary":                    summary,
		"accounts":                   rows,
	})
}
```

**Request-log aggregate evidence pattern** (lines 438-471):
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
		if !entry.ContentSuccess && (entry.StableDownstreamFallback || strings.TrimSpace(entry.ContentFailureReason) != "") {
			emptyCompletions++
		}
	}
```

**Helper style** (lines 554-590):
```go
func runtimeHealthForAccount(h *Handler, id string) pool.RuntimeHealth {
	if h == nil || h.pool == nil {
		return pool.RuntimeHealth{}
	}
	return h.pool.GetRuntimeHealth(id)
}

func modelListForAccount(h *Handler, id string) []string {
	if h == nil || h.pool == nil {
		return nil
	}
	return h.pool.GetModelList(id)
}
```

**Executor guidance:**
- Put the versioned readiness contract shape in `apiGetFleetReadiness`; keep the existing route.
- Replace duplicated account-state decisions with one package-local helper consumed by both `schedulerPreviewRows` and fleet readiness.
- Preserve `reason` for compatibility, but add `reasonCodes []string` from the shared helper.
- Compute `safeConcurrency` after status resolution: non-blocked `min(locallySchedulableAccounts, admissionEffectiveConcurrency)`, blocked `0`.
- Add account-level success evidence to rows by calling pool read APIs; do not derive account success from `Outcome` or status alone.

---

### `proxy/request_log.go` (utility/store, event-driven request logging + transform)

**Analog:** `proxy/request_log.go`

**Imports pattern** (lines 3-15):
```go
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)
```

**Schema pattern for evidence fields** (lines 101-121):
```go
Stream                              bool                      `json:"stream"`
StatusCode                          int                       `json:"statusCode"`
Outcome                             string                    `json:"outcome"`
DurationMs                          int64                     `json:"durationMs"`
StableDownstreamFallback            bool                      `json:"stableDownstreamFallback,omitempty"`
StableFallbackReason                string                    `json:"stableFallbackReason,omitempty"`
SuppressedDownstreamStatus          int                       `json:"suppressedDownstreamStatus,omitempty"`
ContentSuccess                      bool                      `json:"contentSuccess,omitempty"`
ContentFailureReason                string                    `json:"contentFailureReason,omitempty"`
UpstreamContentTokens               int                       `json:"upstreamContentTokens,omitempty"`
StableFallbackFinal                 bool                      `json:"stableFallbackFinal,omitempty"`
Attempts                            int                       `json:"attempts,omitempty"`
AttemptTrace                        []RequestLogAttempt       `json:"attemptTrace,omitempty"`
```

**Store pattern** (lines 195-242):
```go
type requestLogStore struct {
	mu       sync.RWMutex
	capacity int
	entries  []RequestLogEntry
}

func (s *requestLogStore) Add(entry RequestLogEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) >= s.capacity {
		copy(s.entries, s.entries[1:])
		s.entries[len(s.entries)-1] = entry
		return
	}
	s.entries = append(s.entries, entry)
}
```

**Context update pattern** (lines 556-573):
```go
func updateRequestLogUpstream(r *http.Request, accountID, region string, health ...AccountRequestHealthSnapshot) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.AccountID = strings.TrimSpace(accountID)
	ctx.entry.Region = strings.TrimSpace(region)
	// optional health snapshot fields follow
}
```

**Real content/fallback truth pattern** (lines 755-825):
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

**Attempt trace pattern** (lines 842-864):
```go
func appendRequestLogAttempt(r *http.Request, attempt RequestLogAttempt) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	if attempt.Attempt <= 0 {
		attempt.Attempt = 1
	}
	attempt.AccountID = strings.TrimSpace(attempt.AccountID)
	attempt.Model = strings.TrimSpace(attempt.Model)
	attempt.Event = strings.TrimSpace(attempt.Event)
	if attempt.Timestamp.IsZero() {
		attempt.Timestamp = time.Now().UTC()
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if len(ctx.entry.AttemptTrace) < 50 {
		ctx.entry.AttemptTrace = append(ctx.entry.AttemptTrace, attempt)
	}
}
```

**Executor guidance:**
- Keep request-log mutation through small helpers that pull `requestLogContext` from the request.
- If the real-content predicate moves, keep `markRequestLogStableFallback` forcing `ContentSuccess=false`.
- Do not use `Outcome`, HTTP status, or stable fallback headers as account-level real success evidence.

---

### `proxy/handler.go` (controller/service, request-response + streaming + retry)

**Analog:** `proxy/handler.go`

**Admin route dispatch pattern** (lines 1417-1423, 5188-5270):
```go
case strings.HasPrefix(path, "/admin/api/"):
	h.handleAdminAPI(w, r)
```

```go
func (h *Handler) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	password := r.Header.Get("X-Admin-Password")
	if password == "" {
		cookie, _ := r.Cookie("admin_password")
		if cookie != nil {
			password = cookie.Value
		}
	}
	if password != config.GetPassword() {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/admin/api")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch {
	case path == "/scheduler/preview" && r.Method == "GET":
		h.apiGetSchedulerPreview(w, r)
	case path == "/fleet/readiness" && r.Method == "GET":
		h.apiGetFleetReadiness(w, r)
	}
}
```

**Real content predicate pattern** (lines 594-613):
```go
func contentSuccessTokenCount(outputTokens, structuredOutputCount int, textParts ...string) int {
	if outputTokens > 0 {
		return outputTokens
	}
	if structuredOutputCount > 0 {
		return 1
	}
	for _, part := range textParts {
		if strings.TrimSpace(part) != "" {
			return 1
		}
	}
	return 0
}

func updateRequestLogContentSuccessIfPresent(r *http.Request, outputTokens, structuredOutputCount int, textParts ...string) {
	if tokens := contentSuccessTokenCount(outputTokens, structuredOutputCount, textParts...); tokens > 0 {
		updateRequestLogContentSuccess(r, tokens)
	}
}
```

**Claude non-stream success pattern** (lines 3700-3720):
```go
outputTokens = estimateClaudeOutputTokens(finalContent, rawThinkingContent, toolUses)

updateRequestLogUsage(r, billedClaudeInputTokens(inputTokens, cacheUsage), outputTokens, cacheUsage.CacheReadInputTokens, cacheUsage.CacheCreationInputTokens)
updateRequestLogContentSuccessIfPresent(r, outputTokens, len(toolUses), finalContent, rawThinkingContent)
appendRequestLogAttempt(r, RequestLogAttempt{
	Attempt:    attempt + 1,
	AccountID:  account.ID,
	Model:      model,
	Region:     resolveAccountKiroRegion(account),
	Event:      "success",
	DurationMs: latency.Milliseconds(),
})
h.recordSuccess(inputTokens, outputTokens, credits)
h.pool.RecordSuccessWithLatency(account.ID, latency)
h.pool.RecordModelSuccess(account.ID, model)
h.pool.UpdateStats(account.ID, inputTokens+outputTokens, credits)
```

**Streaming no-replay guard pattern** (Claude, lines 3440-3462):
```go
if (shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model)) && !streamStarted {
	return false, err
}
if !streamStarted {
	status, errType := claudeUpstreamErrorStatusAndType(err)
	h.sendClaudeUpstreamError(w, status, errType, err.Error(), err)
	return true, err
}
startMessage()
_, errType := claudeUpstreamErrorStatusAndType(err)
sseMu.Lock()
sse.Error(errType, err.Error())
sseMu.Unlock()
return true, err
```

**OpenAI stream error pattern** (lines 4622-4640):
```go
if (shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model)) && !streamStarted {
	return false, err
}
if streamStarted {
	chunk := map[string]interface{}{
		"error": map[string]string{
			"type":    "server_error",
			"message": err.Error(),
		},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", string(data))
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}
return true, err
```

**Retry budget pressure contract pattern** (lines 2466-2489):
```go
if shouldWaitAndRetryOpus47(lastErr, model) {
	if delay, ok := opusCapacityRetryDelay(lastErr, deadline); ok {
		capacityRetryCount++
		updateRequestLogCapacityRetryCount(r, capacityRetryCount)
		sleepForOpusCapacityRetry(delay)
		used = make(map[string]bool)
		continue
	}
	h.recordFailure()
	h.sendClaudeOpusPressureError(w, model, lastErr, "capacity_recovery_timeout")
	return
}
```

**Executor guidance:**
- Use `contentSuccessTokenCount` as the single real-content predicate for request log and account-level success evidence.
- Add account-level success recording at the same sites as `updateRequestLogContentSuccessIfPresent`, guarded by the same predicate.
- Add `RecordModelSuccess` or equivalent model breaker success only where current code already does it; do not let fallback paths record content success.
- Streaming retry tests should preserve the `!streamStarted` retry boundary.

---

### `pool/account.go` (service/store, runtime state + request selection)

**Analog:** `pool/account.go`

**Imports/state pattern** (lines 5-11, 158-173):
```go
import (
	"kiro-go/config"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)
```

```go
type AccountPool struct {
	mu             sync.RWMutex
	accounts       []config.Account
	totalAccounts  int
	currentIndex   uint64
	cooldowns      map[string]time.Time
	errorCounts    map[string]int
	failures       map[string]FailureReason
	groupCooldowns map[string]time.Time
	groupFailures  map[string]FailureReason
	modelLists     map[string]map[string]bool
	runtimeHealth  map[string]*runtimeHealthState
	breakers       *modelBreakerState
	strategy       Strategy
}
```

**State initialization pattern** (lines 199-227):
```go
func (p *AccountPool) ensureStateLocked() {
	if p.cooldowns == nil {
		p.cooldowns = make(map[string]time.Time)
	}
	if p.modelLists == nil {
		p.modelLists = make(map[string]map[string]bool)
	}
	if p.runtimeHealth == nil {
		p.runtimeHealth = make(map[string]*runtimeHealthState)
	}
	if p.breakers == nil {
		p.breakers = newModelBreakerState()
	}
	if p.strategy == "" {
		p.strategy = StrategyHealth
	}
}
```

**Model-aware routing filter pattern** (lines 423-489):
```go
func (p *AccountPool) getNextForModelExceptLocked(model string, excluded map[string]bool) *config.Account {
	p.ensureStateLocked()
	if len(p.accounts) == 0 {
		return nil
	}
	allowOverUsage := config.GetAllowOverUsage()
	now := time.Now()
	p.clearExpiredCooldownsLocked(now)
	seen := make(map[string]bool)
	var best *config.Account

	for i := 0; i < len(p.accounts); i++ {
		idx := atomic.AddUint64(&p.currentIndex, 1) % uint64(len(p.accounts))
		acc := &p.accounts[idx]
		if seen[acc.ID] || (excluded != nil && excluded[acc.ID]) {
			seen[acc.ID] = true
			continue
		}
		if !p.accountHasModel(acc.ID, model) || !p.accountBaseUsableLocked(acc, now, allowOverUsage) || !p.breakers.isClosed(acc.ID, model) {
			seen[acc.ID] = true
			continue
		}
		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID) {
			best = acc
		}
		if p.strategy == StrategyRoundRobin || p.isIdleHealthyLocked(acc.ID) {
			return acc
		}
	}
	return best
}
```

**Eligibility guard pattern** (lines 571-595):
```go
func (p *AccountPool) accountUsableForModelLocked(acc *config.Account, model string, now time.Time) bool {
	if acc == nil {
		return false
	}
	if !p.accountBaseUsableLocked(acc, now, config.GetAllowOverUsage()) {
		return false
	}
	return p.breakers.canUse(acc.ID, model, now)
}

func (p *AccountPool) accountBaseUsableLocked(acc *config.Account, now time.Time, allowOverUsage bool) bool {
	if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
		return false
	}
	if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
		return false
	}
	if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-tokenRefreshSkewSeconds {
		return false
	}
	if isOverUsageLimit(*acc) && !acc.AllowOverage && !allowOverUsage {
		return false
	}
	return true
}
```

**Candidate comparator pattern** (lines 605-627):
```go
func (p *AccountPool) isBetterCandidateLocked(candidateID, currentID string) bool {
	if p.strategy == StrategyRoundRobin {
		return false
	}
	candidate := p.runtimeHealth[candidateID]
	current := p.runtimeHealth[currentID]
	if candidate == nil && current == nil {
		return false
	}
	if candidate == nil {
		return current.activeConnections > 0 || current.score() < 100
	}
	if current == nil {
		return candidate.activeConnections == 0 && candidate.score() >= 100
	}
	if candidate.activeConnections != current.activeConnections {
		return candidate.activeConnections < current.activeConnections
	}
	if p.strategy != StrategyLeastConnections && candidate.score() != current.score() {
		return candidate.score() > current.score()
	}
	return candidate.avgLatencyMS < current.avgLatencyMS
}
```

**Model block state pattern** (lines 906-969):
```go
func (p *AccountPool) ModelBlockState(model string, now time.Time) ModelBlockState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.clearExpiredCooldownsLocked(now)

	state := ModelBlockState{}
	seen := make(map[string]bool)
	allowOverUsage := config.GetAllowOverUsage()
	for i := range p.accounts {
		acc := p.accounts[i]
		if seen[acc.ID] {
			continue
		}
		seen[acc.ID] = true
		if !p.accountHasModel(acc.ID, model) {
			continue
		}
		if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-tokenRefreshSkewSeconds {
			continue
		}
		if isOverUsageLimit(acc) && !acc.AllowOverage && !allowOverUsage {
			continue
		}
		state.AccountsEvaluated++
		// cooldown and breaker reasons update state.Blocked/RetryAt
	}
	state.AllBlocked = state.AccountsEvaluated > 0 && state.Blocked == state.AccountsEvaluated
	return state
}
```

**Executor guidance:**
- Add account+model content-success evidence as in-memory pool state guarded by `mu`.
- Initialize the new map in `ensureStateLocked` and `GetPool`.
- Prefer fresher content-success evidence inside the model-aware selection path after `accountHasModel`, base usability, and breaker checks pass.
- If comparator needs the model key, introduce a nearby model-aware comparator rather than reading model state from admin code.

---

### `pool/breaker.go` (utility/store, event-driven model breaker state)

**Analog:** `pool/breaker.go`

**State/key pattern** (lines 18-29, 43-53):
```go
type modelBreakerState struct {
	entries map[string]*breakerEntry
	sticky  map[string]stickyEntry
}

type breakerEntry struct {
	Status  breakerStatus
	Reason  FailureReason
	OpenAt  time.Time
	RetryAt time.Time
	Probing bool
}

func breakerKey(accountID, model string) string {
	return strings.TrimSpace(accountID) + "\x00" + normalizedBreakerModel(model)
}

func normalizedBreakerModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}
```

**Open/success lifecycle pattern** (lines 94-114):
```go
func (b *modelBreakerState) open(accountID, model string, reason FailureReason, now time.Time, delay time.Duration) {
	if b == nil || strings.TrimSpace(accountID) == "" {
		return
	}
	if delay <= 0 {
		delay = 30 * time.Second
	}
	b.entries[breakerKey(accountID, model)] = &breakerEntry{
		Status:  breakerOpen,
		Reason:  reason,
		OpenAt:  now,
		RetryAt: now.Add(delay),
	}
}

func (b *modelBreakerState) success(accountID, model string) {
	if b == nil {
		return
	}
	delete(b.entries, breakerKey(accountID, model))
}
```

**Executor guidance:**
- Reuse `normalizedBreakerModel` style for any account+model success evidence key.
- Keep breaker lifecycle separate from real content-success evidence; breaker success means circuit recovery, not necessarily user-visible content success.

---

### `web/index.html` (component, client-side fetch + DOM render)

**Analog:** `web/index.html`

**Placement pattern** (lines 1234-1238):
```html
<div id="tabApi" class="tab-content hidden">
    <div id="claude-code-readiness"></div>
    <div id="claude-code-model-readiness"></div>
    <div id="fleet-readiness"></div>
    <div id="websearch-diagnostics"></div>
```

**Fetch/render pattern** (lines 3100-3129):
```javascript
async function loadFleetReadiness() {
    if (!password) return;
    try {
        const resp = await fetch('/admin/api/fleet/readiness?model=claude-opus-4-7', { headers: { 'X-Admin-Password': password } });
        const data = await resp.json();
        const el = document.getElementById('fleet-readiness');
        if (!el) return;
        const s = data.summary || {};
        const autoRefresh = data.autoRefresh || {};
        const healthCheck = data.healthCheck || {};
        const notes = Array.isArray(data.notes) ? data.notes : [];
        el.innerHTML = '<div class="card">' +
            '<div class="card-header"><span class="card-title">Opus 4.7 fleet health</span><button class="btn btn-sm btn-secondary" onclick="loadFleetReadiness()">' + escapeHtml(t('requestLogs.refresh')) + '</button></div>' +
            '<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));gap:8px;font-size:12px;color:#475569">' +
            '<span>Status: ' + escapeHtml(data.status || '-') + '</span>' +
            '<span>Safe concurrency: ' + escapeHtml(String(data.safeConcurrency || 0)) + '</span>' +
            '<span>Schedulable: ' + escapeHtml(String(data.locallySchedulableAccounts || 0)) + '</span>' +
            '</div>' +
            '<div style="font-size:12px;color:#64748b;margin-top:8px">Strategy: ' + escapeHtml(data.strategy || '-') + ' · Model: ' + escapeHtml(data.mappedModel || data.model || '-') + '</div>' +
            '<div style="font-size:12px;color:#64748b;margin-top:6px">' + escapeHtml(notes.join(' | ') || 'sub2api can use this contract to route, queue, or back off Opus 4.7 traffic.') + '</div>' +
            '</div>';
    } catch (e) { }
}
```

**Executor guidance:**
- Extend the existing card only; no build step, dependency, or large layout rewrite.
- Use `escapeHtml` for all API data and continue displaying masked account fields only.
- Add compact rows/counts for `contractVersion`, `recommendedAction`, `reasonCodes`, real content success, fallback counts, and account eligibility explanations.

---

### `proxy/ecosystem_ops_test.go` (test, HTTP handler/request-response)

**Analog:** `proxy/ecosystem_ops_test.go`

**Imports/setup pattern** (lines 3-13):
```go
import (
	"encoding/json"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)
```

**Read-only scheduler/readiness test pattern** (lines 82-136):
```go
func TestSchedulerPreviewAndFleetReadinessAreReadOnly(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{ID: "healthy", Email: "healthy@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "disabled", Email: "disabled@example.com", Enabled: false, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.SetModelList("healthy", []string{"claude-opus-4.7"})
	h := &Handler{pool: p}
	before := config.GetAccounts()
	// call apiGetSchedulerPreview and apiGetFleetReadiness with httptest
	after := config.GetAccounts()
	if before[0].RequestCount != after[0].RequestCount {
		t.Fatalf("preview/readiness should not mutate accounts")
	}
}
```

**Fleet readiness field test pattern** (lines 139-237):
```go
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
wantFields := []string{
	"model", "status", "circuitState", "retryAfterSeconds", "safeConcurrency",
	"enabledAccounts", "locallySchedulableAccounts", "notes",
}
for _, field := range wantFields {
	if _, ok := body[field]; !ok {
		t.Fatalf("%s missing: %#v", field, body)
	}
}
```

**Fallback evidence test pattern** (lines 239-280):
```go
h.ensureRequestLogStore().Add(RequestLogEntry{
	Timestamp:                  time.Now(),
	Model:                      "claude-opus-4.7",
	StatusCode:                 http.StatusOK,
	Outcome:                    "success",
	StableDownstreamFallback:   true,
	StableFallbackReason:       "admission_pressure",
	ContentFailureReason:       "admission_pressure",
	SuppressedDownstreamStatus: http.StatusServiceUnavailable,
})
// assert recentContentRequests=1, contentSuccessRate=0, recentStableFallbacks=1
```

**Executor guidance:**
- Add matrix tests for healthy/degraded/blocked safe concurrency.
- Add one fixture that compares scheduler preview and fleet readiness account rows by account ID, eligibility, and reason codes.
- Keep tests direct with `httptest.NewRecorder`, `json.Unmarshal`, and `t.Fatalf`.

---

### `proxy/request_log_test.go` (test, logging utility + event assertions)

**Analog:** `proxy/request_log_test.go`

**Metadata/attempt trace test pattern** (lines 135-213):
```go
ctx, loggedReq, recorder, _ := h.beginRequestLog(httptest.NewRecorder(), req)
updateRequestLogMetadata(loggedReq, "claude-opus-4.7", false)
updateRequestLogUpstream(loggedReq, "acct-1", "eu-west-1", AccountRequestHealthSnapshot{
	ActiveConnections: 2,
	RecentFailures:    1,
	RecentSuccesses:   9,
	AvgLatencyMS:      345,
	Score:             87,
})
appendRequestLogAttempt(loggedReq, RequestLogAttempt{
	Attempt:           1,
	AccountID:         "acct-1",
	Model:             "claude-opus-4.7",
	Region:            "eu-west-1",
	Event:             "failure",
	Reason:            "temporary_limited",
	CircuitState:      "open",
	RetryAfterSeconds: 60,
	DurationMs:        42,
})
recorder.WriteHeader(http.StatusOK)
h.finishRequestLog(ctx, recorder)
```

**Stable fallback is not content success** (lines 329-357):
```go
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
```

**Real content success test pattern** (lines 360-371):
```go
entry := RequestLogEntry{}
markRequestLogContentSuccess(&entry, 17)
if !entry.ContentSuccess {
	t.Fatalf("expected content success")
}
if entry.UpstreamContentTokens != 17 {
	t.Fatalf("UpstreamContentTokens = %d, want 17", entry.UpstreamContentTokens)
}
```

**Executor guidance:**
- Add tests for any shared predicate: output tokens, structured output/tool use, non-empty text/reasoning succeed; empty text and stable fallback fail.
- Keep direct helper tests in `request_log_test.go` and protocol-level evidence tests in `handler_test.go`.

---

### `pool/account_test.go` (test, service unit + routing selection)

**Analog:** `pool/account_test.go`

**Pool construction pattern** (lines 562-584):
```go
p := &AccountPool{
	cooldowns:     make(map[string]time.Time),
	errorCounts:   make(map[string]int),
	failures:      make(map[string]FailureReason),
	modelLists:    make(map[string]map[string]bool),
	accounts:      []config.Account{{ID: "acct-1"}, {ID: "acct-2"}},
	totalAccounts: 2,
	currentIndex:  1,
}
p.SetModelList("acct-1", []string{"claude-opus-4.7"})
p.SetModelList("acct-2", []string{"claude-opus-4.7"})

release := p.BeginRequest("acct-1")
defer release()

got := p.GetNextForModel("claude-opus-4.7")
if got == nil || got.ID != "acct-2" {
	t.Fatalf("expected less busy acct-2, got %#v", got)
}
```

**Strategy-specific test pattern** (lines 587-610, 644-667):
```go
p.SetStrategy(StrategyLeastConnections)
// assert idle account wins when busy has active connection
```

```go
p.SetStrategy(StrategyRoundRobin)
// assert round-robin preserves configured order
```

**Breaker/model eligibility pattern** (lines 819-843):
```go
p.RememberSticky("session-1", "claude-sonnet-4.5", "acct-1")
p.RecordModelFailure("acct-1", "claude-sonnet-4.5", FailureReasonRateLimited, time.Now().Add(time.Minute))

acc, release := p.BeginNextForModelSessionExcept("claude-sonnet-4.5", "session-1", nil)
defer release()

if acc == nil || acc.ID != "acct-2" {
	t.Fatalf("expected acct-2 after breaker-open sticky escape, got %q", acc.ID)
}
```

**Executor guidance:**
- Add tests proving fresher real content success wins only after normal eligibility filters pass.
- Test blockers separately: disabled/not loaded into pool, cooldown, breaker open, token near expiry, usage limit, model not listed.
- Pin strategy behavior if success evidence changes `isBetterCandidateLocked` semantics.

---

### `proxy/handler_test.go` (test, handler integration + retry/streaming)

**Analog:** `proxy/handler_test.go`

**Bounded Opus retry test pattern** (lines 2323-2415):
```go
oldBudget := opusCapacityRetryBudget
oldSleep := sleepForOpusCapacityRetry
opusCapacityRetryBudget = time.Second
var sleeps []time.Duration
sleepForOpusCapacityRetry = func(d time.Duration) {
	sleeps = append(sleeps, d)
}
t.Cleanup(func() {
	opusCapacityRetryBudget = oldBudget
	sleepForOpusCapacityRetry = oldSleep
	InitKiroHttpClient("")
})

kiroHttpStore.Store(&http.Client{
	Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"reason":"INSUFFICIENT_MODEL_CAPACITY"}`)),
			Header:     make(http.Header),
		}, nil
	}),
})
// assert status 503, X-Kiro-Go-Retryable=true, X-Kiro-Go-Error-Reason=attempt_budget_exhausted
```

**Attempt budget pattern** (lines 3080-3119):
```go
h, upstreamHits := newOpus47RetryBudgetTestHandler(t, 8, func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`))
})
// assert upstreamHits == 4, status 503, Retry-After present
```

**Streaming pressure test pattern** (lines 5274-5329):
```go
body := strings.NewReader(`{"model":"claude-opus-4.7","stream":true,"max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`)
req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
w := httptest.NewRecorder()

h.handleClaudeMessagesInternal(w, req)

if w.Code == http.StatusTooManyRequests {
	t.Fatalf("sub2api Opus 4.7 capacity contract must not return 429, body %q", w.Body.String())
}
if w.Code != http.StatusServiceUnavailable {
	t.Fatalf("expected explicit 503 after capacity budget, got status %d body %q", w.Code, w.Body.String())
}
```

**Pressure error helper test pattern** (lines 5515-5545):
```go
rateErr := &rateLimitError{
	endpoint: "Kiro IDE",
	body:     `{"message":"rate limited"}`,
	resetAt:  time.Now().Add(2500 * time.Millisecond),
}
h.sendClaudeOpusPressureError(claudeW, "claude-opus-4.7", rateErr, "attempt_budget_exhausted")
if claudeW.Code != http.StatusServiceUnavailable {
	t.Fatalf("Claude Opus pressure status = %d body=%s, want 503", claudeW.Code, claudeW.Body.String())
}
```

**Attempt trace assertion pattern** (lines 7235-7249):
```go
trace := logs[0].AttemptTrace
if trace[0].Event != "selected" || trace[0].AccountID != "acct-1" || trace[0].Attempt != 1 {
	t.Fatalf("expected first trace entry to select acct-1, got %#v", trace[0])
}
if trace[1].Event != "failure" || trace[1].Reason != string(pool.FailureReasonTemporaryLimited) {
	t.Fatalf("expected second trace entry to record acct-1 temporary limit, got %#v", trace[1])
}
if trace[3].Event != "success" || trace[3].AccountID != "acct-2" || trace[3].Attempt != 2 {
	t.Fatalf("expected fourth trace entry to record acct-2 success, got %#v", trace[3])
}
```

**Executor guidance:**
- Add handler tests that prove account-level content evidence advances only when `contentSuccessTokenCount > 0`.
- Add stream tests for pre-first-content retry and post-content no-replay across Claude, OpenAI Chat, and OpenAI Responses.
- Continue using fake `http.Client` transports and `t.Cleanup` to restore globals.

## Shared Patterns

### Admin Auth And JSON Responses
**Source:** `proxy/handler.go` lines 5188-5206  
**Apply to:** `proxy/ecosystem_ops.go`, route tests
```go
if password != config.GetPassword() {
	w.WriteHeader(401)
	json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
	return
}
path := strings.TrimPrefix(r.URL.Path, "/admin/api")
w.Header().Set("Content-Type", "application/json; charset=utf-8")
```

### Model Normalization
**Source:** `proxy/ecosystem_ops.go` lines 268-274 and `pool/breaker.go` lines 43-53  
**Apply to:** readiness, scheduler preview, pool success evidence
```go
mapped, _ := resolveClaudeThinkingMode(model, nil, config.GetThinkingConfig().Suffix)
```

```go
func breakerKey(accountID, model string) string {
	return strings.TrimSpace(accountID) + "\x00" + normalizedBreakerModel(model)
}
```

### Secret-Safe Account Display
**Source:** `proxy/ecosystem_ops.go` lines 307-315 and 554-566  
**Apply to:** readiness rows and UI
```go
rows = append(rows, map[string]interface{}{
	"id":            account.ID,
	"email":         maskReadinessEmail(account.Email),
	"eligible":      eligible,
	"reason":        reason,
	"runtimeHealth": runtimeHealthForAccount(h, account.ID),
	"modelsCached":  modelListForAccount(h, account.ID),
})
```

### Real Content Success
**Source:** `proxy/handler.go` lines 594-613 and `proxy/request_log.go` lines 755-764  
**Apply to:** request log, account-level evidence, readiness aggregates
```go
if outputTokens > 0 {
	return outputTokens
}
if structuredOutputCount > 0 {
	return 1
}
for _, part := range textParts {
	if strings.TrimSpace(part) != "" {
		return 1
	}
}
```

### Stable Fallback Is Content Failure
**Source:** `proxy/request_log.go` lines 805-825  
**Apply to:** fallback paths and tests
```go
entry.StableDownstreamFallback = true
entry.StableFallbackReason = reason
entry.SuppressedDownstreamStatus = suppressedStatus
entry.StableFallbackFinal = true
markRequestLogContentFailure(entry, reason)
```

### Pool State Locking
**Source:** `pool/account.go` lines 387-421 and 897-904  
**Apply to:** account+model success evidence APIs
```go
func (p *AccountPool) RecordModelSuccess(accountID, model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.breakers.success(accountID, model)
}

func (p *AccountPool) GetRuntimeHealth(id string) RuntimeHealth {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.runtimeHealth == nil || p.runtimeHealth[id] == nil {
		return runtimeHealthState{}.export()
	}
	return p.runtimeHealth[id].export()
}
```

### Test Global Cleanup
**Source:** `proxy/handler_test.go` lines 2341-2352 and 3086-3094  
**Apply to:** handler tests that swap budgets/transports/globals
```go
oldBudget := opusCapacityRetryBudget
oldSleep := sleepForOpusCapacityRetry
opusCapacityRetryBudget = time.Second
sleepForOpusCapacityRetry = func(d time.Duration) {}
t.Cleanup(func() {
	opusCapacityRetryBudget = oldBudget
	sleepForOpusCapacityRetry = oldSleep
	InitKiroHttpClient("")
})
```

## No Analog Found

None. Every expected Phase 04 file has an exact local analog. New helper APIs such as `RecordModelContentSuccess` / `ModelContentSuccess` should follow `pool/account.go` state and locking patterns rather than introducing new persistence or packages.

## Metadata

**Analog search scope:** `proxy/*.go`, `pool/*.go`, `web/index.html`, related `*_test.go`, `.planning/codebase/*.md`  
**Files scanned:** 39 source/test files plus phase docs  
**Pattern extraction date:** 2026-05-21  
**Local project skills:** none found under `.codex/skills` or `.agents/skills`  
**Security note:** pattern mapping did not read runtime secret files such as `data/config.json`, token stores, browser sessions, keychains, or CLI auth databases.

## PATTERN MAPPING COMPLETE
