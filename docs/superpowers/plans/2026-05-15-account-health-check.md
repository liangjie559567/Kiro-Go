# Account Health Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add configurable backend scheduled account health checks that verify enabled accounts by loading their available models and optionally disable unhealthy accounts.

**Architecture:** Add a `HealthCheckConfig` beside the existing auto-refresh config, then add a focused `proxy/account_health.go` helper for selection, batch checking, status, and auto-disable behavior. `proxy.Handler` owns the scheduler and admin API, mirroring the existing auto-refresh scheduler. `web/index.html` gets a compact settings card using the current single-file UI and i18n style.

**Tech Stack:** Go standard library, existing Kiro-Go config/pool/proxy packages, single-file HTML/CSS/JavaScript admin UI, `go test ./...`.

---

## File Structure

- Modify `config/config.go`: define `HealthCheckConfig`, defaults, normalization, validation, getter, and updater.
- Modify `config/config_test.go`: cover health check defaults, persisted normalization, sparse update behavior, and validation.
- Create `proxy/account_health.go`: health check result/status helpers, enabled-only selection, batch runner, auto-disable logic, scheduler status helpers, and next-run computation.
- Create `proxy/account_health_test.go`: cover selection, batch continuation, auto-disable behavior, status overlap protection, and next-run computation.
- Modify `proxy/handler.go`: add scheduler fields, start the scheduler, add `/admin/api/health-check` routes, implement GET/POST handlers, and run scheduled checks.
- Modify `web/index.html`: add settings card, i18n strings, load/save/status JavaScript.

## Task 1: Health Check Config

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Append these tests to `config/config_test.go`:

```go
func TestDefaultHealthCheckConfig(t *testing.T) {
	got := defaultHealthCheckConfig()
	if got.Enabled {
		t.Fatalf("expected health check disabled by default")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected default interval 60, got %d", got.IntervalMinutes)
	}
	if got.AutoDisableUnhealthy {
		t.Fatalf("expected auto-disable disabled by default")
	}
}

func TestNormalizePersistedHealthCheckConfigPreservesExplicitDisabled(t *testing.T) {
	data := []byte(`{"healthCheck":{"enabled":false,"autoDisableUnhealthy":true}}`)
	got := normalizePersistedHealthCheckConfig(data, HealthCheckConfig{
		AutoDisableUnhealthy: true,
	})
	if got.Enabled {
		t.Fatalf("expected explicit disabled config to be preserved")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if !got.AutoDisableUnhealthy {
		t.Fatalf("expected explicit auto-disable to be preserved")
	}
}

func TestNormalizePersistedHealthCheckConfigDefaultsAbsentEnabled(t *testing.T) {
	data := []byte(`{"healthCheck":{"intervalMinutes":60}}`)
	got := normalizePersistedHealthCheckConfig(data, HealthCheckConfig{IntervalMinutes: 60})
	if got.Enabled {
		t.Fatalf("expected absent enabled field to default false")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval 60, got %d", got.IntervalMinutes)
	}
	if got.AutoDisableUnhealthy {
		t.Fatalf("expected absent auto-disable to default false")
	}
}

func TestNormalizeHealthCheckConfigForUpdatePreservesSparseDisabled(t *testing.T) {
	got := normalizeHealthCheckConfigForUpdate(HealthCheckConfig{Enabled: false, AutoDisableUnhealthy: true})
	if got.Enabled {
		t.Fatalf("expected sparse disabled update to stay disabled")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if !got.AutoDisableUnhealthy {
		t.Fatalf("expected auto-disable to stay enabled")
	}
}

func TestValidateHealthCheckConfig(t *testing.T) {
	valid := HealthCheckConfig{Enabled: true, IntervalMinutes: 5, AutoDisableUnhealthy: true}
	if err := ValidateHealthCheckConfig(valid); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	tooSmall := HealthCheckConfig{Enabled: true, IntervalMinutes: 4}
	if err := ValidateHealthCheckConfig(tooSmall); err == nil {
		t.Fatalf("expected interval below minimum to fail")
	}

	tooLarge := HealthCheckConfig{Enabled: true, IntervalMinutes: 1441}
	if err := ValidateHealthCheckConfig(tooLarge); err == nil {
		t.Fatalf("expected interval above maximum to fail")
	}
}
```

- [ ] **Step 2: Run config tests to verify RED**

Run:

```bash
go test ./config
```

Expected: FAIL with undefined symbols such as `defaultHealthCheckConfig`, `HealthCheckConfig`, and `ValidateHealthCheckConfig`.

- [ ] **Step 3: Add health check config implementation**

In `config/config.go`, add constants and config type after the auto-refresh constants/type:

```go
const (
	HealthCheckMinIntervalMinutes     = 5
	HealthCheckMaxIntervalMinutes     = 1440
	HealthCheckDefaultIntervalMinutes = 60
)

type HealthCheckConfig struct {
	Enabled               bool `json:"enabled"`
	IntervalMinutes       int  `json:"intervalMinutes"`
	AutoDisableUnhealthy  bool `json:"autoDisableUnhealthy"`
}
```

Add `HealthCheck` to `Config` next to `AutoRefresh`:

```go
HealthCheck HealthCheckConfig `json:"healthCheck"`
```

Add these helpers after the persisted auto-refresh helpers:

```go
func defaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Enabled:              false,
		IntervalMinutes:      HealthCheckDefaultIntervalMinutes,
		AutoDisableUnhealthy: false,
	}
}

func normalizeHealthCheckConfig(in HealthCheckConfig) HealthCheckConfig {
	defaults := defaultHealthCheckConfig()
	if in == (HealthCheckConfig{}) {
		return defaults
	}
	return normalizeHealthCheckConfigForUpdate(in)
}

func normalizeHealthCheckConfigForUpdate(in HealthCheckConfig) HealthCheckConfig {
	defaults := defaultHealthCheckConfig()
	if in.IntervalMinutes == 0 {
		in.IntervalMinutes = defaults.IntervalMinutes
	}
	return in
}

func ValidateHealthCheckConfig(in HealthCheckConfig) error {
	if in.IntervalMinutes < HealthCheckMinIntervalMinutes || in.IntervalMinutes > HealthCheckMaxIntervalMinutes {
		return fmt.Errorf("intervalMinutes must be between %d and %d", HealthCheckMinIntervalMinutes, HealthCheckMaxIntervalMinutes)
	}
	return nil
}

type persistedHealthCheckConfig struct {
	Enabled              *bool `json:"enabled"`
	IntervalMinutes      int   `json:"intervalMinutes"`
	AutoDisableUnhealthy *bool `json:"autoDisableUnhealthy"`
}

func normalizePersistedHealthCheckConfig(data []byte, in HealthCheckConfig) HealthCheckConfig {
	var raw struct {
		HealthCheck *persistedHealthCheckConfig `json:"healthCheck"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.HealthCheck == nil {
		return normalizeHealthCheckConfig(in)
	}

	normalized := normalizeHealthCheckConfigForUpdate(in)
	if raw.HealthCheck.Enabled == nil {
		normalized.Enabled = defaultHealthCheckConfig().Enabled
	} else {
		normalized.Enabled = *raw.HealthCheck.Enabled
	}
	if raw.HealthCheck.AutoDisableUnhealthy == nil {
		normalized.AutoDisableUnhealthy = defaultHealthCheckConfig().AutoDisableUnhealthy
	} else {
		normalized.AutoDisableUnhealthy = *raw.HealthCheck.AutoDisableUnhealthy
	}
	return normalized
}
```

In the missing-file default config inside `Load`, add:

```go
HealthCheck: defaultHealthCheckConfig(),
```

After `c.AutoRefresh = normalizePersistedAutoRefreshConfig(data, c.AutoRefresh)`, add:

```go
c.HealthCheck = normalizePersistedHealthCheckConfig(data, c.HealthCheck)
```

Add getter/updater after `UpdateAutoRefreshConfig`:

```go
func GetHealthCheckConfig() HealthCheckConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return normalizeHealthCheckConfig(cfg.HealthCheck)
}

func UpdateHealthCheckConfig(healthCheck HealthCheckConfig) error {
	normalized := normalizeHealthCheckConfigForUpdate(healthCheck)
	if err := ValidateHealthCheckConfig(normalized); err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.HealthCheck = normalized
	return Save()
}
```

- [ ] **Step 4: Run config tests to verify GREEN**

Run:

```bash
go test ./config
```

Expected: PASS.

- [ ] **Step 5: Commit config changes**

Run:

```bash
git add config/config.go config/config_test.go
git commit -m "feat: add health check config"
```

## Task 2: Health Check Batch Helpers

**Files:**
- Create: `proxy/account_health.go`
- Create: `proxy/account_health_test.go`

- [ ] **Step 1: Write failing helper tests**

Create `proxy/account_health_test.go`:

```go
package proxy

import (
	"errors"
	"kiro-go/config"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelectHealthCheckAccountsOnlyEnabled(t *testing.T) {
	accounts := []config.Account{
		{ID: "enabled-1", Enabled: true},
		{ID: "disabled-1", Enabled: false},
		{ID: "enabled-2", Enabled: true},
	}

	got := selectHealthCheckAccounts(accounts)
	if len(got) != 2 {
		t.Fatalf("expected 2 enabled accounts, got %d", len(got))
	}
	if got[0].ID != "enabled-1" || got[1].ID != "enabled-2" {
		t.Fatalf("unexpected enabled account order: %#v", got)
	}
}

func TestRunHealthCheckBatchContinuesAfterFailure(t *testing.T) {
	accounts := []config.Account{{ID: "ok-1"}, {ID: "bad"}, {ID: "ok-2"}}

	var calls int32
	result := runHealthCheckBatch(accounts, false, func(account *config.Account) error {
		atomic.AddInt32(&calls, 1)
		if account.ID == "bad" {
			return errors.New("model load failed")
		}
		return nil
	}, func(account *config.Account, reason string, now int64) error {
		t.Fatalf("disable should not be called when auto-disable is off")
		return nil
	}, 100)

	if calls != 3 {
		t.Fatalf("expected all accounts to be attempted, got %d", calls)
	}
	if result.Success != 2 || result.Failed != 1 || result.Disabled != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRunHealthCheckBatchDisablesFailedAccountsWhenConfigured(t *testing.T) {
	accounts := []config.Account{{ID: "ok"}, {ID: "bad"}}
	var disabled []string

	result := runHealthCheckBatch(accounts, true, func(account *config.Account) error {
		if account.ID == "bad" {
			return errors.New("403 forbidden")
		}
		return nil
	}, func(account *config.Account, reason string, now int64) error {
		disabled = append(disabled, account.ID+":"+reason)
		if now != 123 {
			t.Fatalf("expected disable timestamp 123, got %d", now)
		}
		return nil
	}, 123)

	if result.Success != 1 || result.Failed != 1 || result.Disabled != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(disabled) != 1 || disabled[0] != "bad:403 forbidden" {
		t.Fatalf("unexpected disabled accounts: %#v", disabled)
	}
}

func TestTryBeginHealthCheckPreventsOverlap(t *testing.T) {
	h := &Handler{}

	if !h.tryBeginHealthCheck(100) {
		t.Fatalf("expected first run to start")
	}
	if h.tryBeginHealthCheck(200) {
		t.Fatalf("expected overlapping run to be rejected")
	}

	status := h.getHealthCheckStatus()
	if !status.Running {
		t.Fatalf("expected running status while first run is active")
	}
	if !status.LastSkipped {
		t.Fatalf("expected skipped flag after overlap attempt")
	}

	h.finishHealthCheck(healthCheckBatchResult{Success: 1, Failed: 1, Disabled: 1}, 300, 3600)
	status = h.getHealthCheckStatus()
	if status.Running {
		t.Fatalf("expected running false after finish")
	}
	if status.LastFinishedAt != 300 {
		t.Fatalf("expected finish timestamp 300, got %d", status.LastFinishedAt)
	}
	if status.NextRunAt != 3600 {
		t.Fatalf("expected next run 3600, got %d", status.NextRunAt)
	}
	if status.LastDisabled != 1 {
		t.Fatalf("expected last disabled 1, got %d", status.LastDisabled)
	}
}

func TestComputeNextHealthCheckRunAt(t *testing.T) {
	now := time.Unix(1000, 0)
	got := computeNextHealthCheckRunAt(now, config.HealthCheckConfig{Enabled: true, IntervalMinutes: 60})
	if got != 4600 {
		t.Fatalf("expected 4600, got %d", got)
	}

	disabled := computeNextHealthCheckRunAt(now, config.HealthCheckConfig{Enabled: false, IntervalMinutes: 60})
	if disabled != 0 {
		t.Fatalf("expected disabled next run 0, got %d", disabled)
	}
}
```

- [ ] **Step 2: Run helper tests to verify RED**

Run:

```bash
go test ./proxy
```

Expected: FAIL with undefined symbols such as `selectHealthCheckAccounts`, `runHealthCheckBatch`, and `healthCheckBatchResult`.

- [ ] **Step 3: Add helper implementation**

Create `proxy/account_health.go`:

```go
package proxy

import (
	"kiro-go/config"
	"kiro-go/logger"
	"time"
)

type healthCheckBatchResult struct {
	Success  int
	Failed   int
	Disabled int
}

type healthCheckStatus struct {
	Running        bool  `json:"running"`
	LastStartedAt  int64 `json:"lastStartedAt"`
	LastFinishedAt int64 `json:"lastFinishedAt"`
	NextRunAt      int64 `json:"nextRunAt"`
	LastSuccess    int   `json:"lastSuccess"`
	LastFailed     int   `json:"lastFailed"`
	LastDisabled   int   `json:"lastDisabled"`
	LastSkipped    bool  `json:"lastSkipped"`
}

func selectHealthCheckAccounts(accounts []config.Account) []config.Account {
	selected := make([]config.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Enabled {
			selected = append(selected, account)
		}
	}
	return selected
}

func runHealthCheckBatch(
	accounts []config.Account,
	autoDisable bool,
	check func(account *config.Account) error,
	disable func(account *config.Account, reason string, now int64) error,
	now int64,
) healthCheckBatchResult {
	var result healthCheckBatchResult
	for i := range accounts {
		err := check(&accounts[i])
		if err == nil {
			result.Success++
			continue
		}

		result.Failed++
		if !autoDisable {
			continue
		}
		if disableErr := disable(&accounts[i], err.Error(), now); disableErr != nil {
			logger.Warnf("[HealthCheck] Failed to disable %s: %v", accounts[i].Email, disableErr)
			continue
		}
		result.Disabled++
	}
	return result
}

func computeNextHealthCheckRunAt(now time.Time, settings config.HealthCheckConfig) int64 {
	if !settings.Enabled {
		return 0
	}

	interval := settings.IntervalMinutes
	if interval == 0 {
		interval = config.HealthCheckDefaultIntervalMinutes
	}
	return now.Add(time.Duration(interval) * time.Minute).Unix()
}

func (h *Handler) tryBeginHealthCheck(startedAt int64) bool {
	h.healthCheckMu.Lock()
	defer h.healthCheckMu.Unlock()

	if h.healthCheckStatus.Running {
		h.healthCheckStatus.LastSkipped = true
		return false
	}

	h.healthCheckStatus.Running = true
	h.healthCheckStatus.LastStartedAt = startedAt
	h.healthCheckStatus.LastSkipped = false
	return true
}

func (h *Handler) finishHealthCheck(result healthCheckBatchResult, finishedAt, nextRunAt int64) {
	h.healthCheckMu.Lock()
	defer h.healthCheckMu.Unlock()

	h.healthCheckStatus.Running = false
	h.healthCheckStatus.LastFinishedAt = finishedAt
	h.healthCheckStatus.NextRunAt = nextRunAt
	h.healthCheckStatus.LastSuccess = result.Success
	h.healthCheckStatus.LastFailed = result.Failed
	h.healthCheckStatus.LastDisabled = result.Disabled
}

func (h *Handler) setNextHealthCheckRun(nextRunAt int64) {
	h.healthCheckMu.Lock()
	defer h.healthCheckMu.Unlock()

	h.healthCheckStatus.NextRunAt = nextRunAt
}

func (h *Handler) getHealthCheckStatus() healthCheckStatus {
	h.healthCheckMu.RLock()
	defer h.healthCheckMu.RUnlock()

	return h.healthCheckStatus
}

func disableUnhealthyAccount(account *config.Account, reason string, now int64) error {
	accounts := config.GetAccounts()
	for i := range accounts {
		if accounts[i].ID != account.ID {
			continue
		}

		accounts[i].Enabled = false
		accounts[i].BanStatus = "UNHEALTHY"
		accounts[i].BanReason = reason
		accounts[i].BanTime = now

		account.Enabled = false
		account.BanStatus = accounts[i].BanStatus
		account.BanReason = accounts[i].BanReason
		account.BanTime = accounts[i].BanTime

		return config.UpdateAccount(account.ID, accounts[i])
	}
	return nil
}
```

- [ ] **Step 4: Add Handler fields needed by helper**

In `proxy/handler.go`, add these fields to `Handler` after auto-refresh fields:

```go
healthCheckUpdated chan struct{}
healthCheckMu      sync.RWMutex
healthCheckStatus  healthCheckStatus
```

In `NewHandler`, initialize:

```go
healthCheckUpdated: make(chan struct{}, 1),
```

- [ ] **Step 5: Run helper tests to verify GREEN**

Run:

```bash
go test ./proxy
```

Expected: PASS.

- [ ] **Step 6: Commit helper changes**

Run:

```bash
git add proxy/account_health.go proxy/account_health_test.go proxy/handler.go
git commit -m "feat: add account health check helpers"
```

## Task 3: Health Check Scheduler and Admin API

**Files:**
- Modify: `proxy/handler.go`

- [ ] **Step 1: Write failing admin API tests**

Append these tests to `proxy/handler_test.go`:

```go
func TestAdminHealthCheckConfigRejectsInvalidInterval(t *testing.T) {
	dir := t.TempDir()
	if err := config.Init(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("test-password")

	h := &Handler{
		pool:               pool.GetPool(),
		healthCheckUpdated: make(chan struct{}, 1),
	}

	body := strings.NewReader(`{"enabled":true,"intervalMinutes":4,"autoDisableUnhealthy":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/health-check", body)
	req.Header.Set("X-Admin-Password", "test-password")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body %s", w.Code, w.Body.String())
	}
}

func TestAdminHealthCheckConfigUpdateAndGet(t *testing.T) {
	dir := t.TempDir()
	if err := config.Init(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	config.SetPassword("test-password")

	h := &Handler{
		pool:               pool.GetPool(),
		healthCheckUpdated: make(chan struct{}, 1),
	}

	body := strings.NewReader(`{"enabled":true,"intervalMinutes":15,"autoDisableUnhealthy":true}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/health-check", body)
	req.Header.Set("X-Admin-Password", "test-password")
	w := httptest.NewRecorder()

	h.handleAdminAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/api/health-check", nil)
	getReq.Header.Set("X-Admin-Password", "test-password")
	getW := httptest.NewRecorder()

	h.handleAdminAPI(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected get status 200, got %d body %s", getW.Code, getW.Body.String())
	}
	if !strings.Contains(getW.Body.String(), `"intervalMinutes":15`) {
		t.Fatalf("expected saved interval in response, got %s", getW.Body.String())
	}
	if !strings.Contains(getW.Body.String(), `"autoDisableUnhealthy":true`) {
		t.Fatalf("expected saved auto-disable in response, got %s", getW.Body.String())
	}
}
```

If `proxy/handler_test.go` does not already import `net/http`, `net/http/httptest`, `path/filepath`, `strings`, `kiro-go/config`, or `kiro-go/pool`, add the missing imports to the existing import block.

- [ ] **Step 2: Run admin API tests to verify RED**

Run:

```bash
go test ./proxy -run 'TestAdminHealthCheckConfig'
```

Expected: FAIL because `/admin/api/health-check` returns 404.

- [ ] **Step 3: Add scheduler functions**

In `proxy/handler.go`, after `runAutoRefresh`, add:

```go
func (h *Handler) backgroundHealthCheck() {
	h.scheduleNextHealthCheck()

healthLoop:
	for {
		settings := config.GetHealthCheckConfig()
		if !settings.Enabled {
			timer := time.NewTimer(time.Minute)
			select {
			case <-timer.C:
				continue
			case <-h.healthCheckUpdated:
				stopTimer(timer)
				h.scheduleNextHealthCheck()
				continue
			case <-h.stopRefresh:
				stopTimer(timer)
				return
			}
			continue
		}

		interval := time.Duration(settings.IntervalMinutes) * time.Minute
		timer := time.NewTimer(interval)
		for {
			select {
			case <-timer.C:
				h.runHealthCheck()
				continue healthLoop
			case <-h.healthCheckUpdated:
				stopTimer(timer)
				h.scheduleNextHealthCheck()
				continue healthLoop
			case <-h.stopRefresh:
				stopTimer(timer)
				return
			}
		}
	}
}

func (h *Handler) scheduleNextHealthCheck() {
	h.setNextHealthCheckRun(computeNextHealthCheckRunAt(time.Now(), config.GetHealthCheckConfig()))
}

func (h *Handler) notifyHealthCheckUpdated() {
	if h.healthCheckUpdated == nil {
		return
	}
	select {
	case h.healthCheckUpdated <- struct{}{}:
	default:
	}
}

func (h *Handler) runHealthCheck() {
	settings := config.GetHealthCheckConfig()
	now := time.Now()
	if !settings.Enabled {
		h.setNextHealthCheckRun(0)
		return
	}
	if !h.tryBeginHealthCheck(now.Unix()) {
		h.scheduleNextHealthCheck()
		return
	}

	accounts := selectHealthCheckAccounts(config.GetAccounts())
	result := runHealthCheckBatch(accounts, settings.AutoDisableUnhealthy, func(account *config.Account) error {
		_, err := ListAvailableModels(account)
		if err != nil {
			logger.Warnf("[HealthCheck] Account %s unhealthy: %v", account.Email, err)
		}
		return err
	}, disableUnhealthyAccount, now.Unix())

	if result.Disabled > 0 {
		h.pool.Reload()
	}

	finishedAt := time.Now()
	nextSettings := config.GetHealthCheckConfig()
	h.finishHealthCheck(result, finishedAt.Unix(), computeNextHealthCheckRunAt(finishedAt, nextSettings))
	logger.Infof("[HealthCheck] Completed: success=%d failed=%d disabled=%d", result.Success, result.Failed, result.Disabled)
}
```

In `NewHandler`, after `go h.backgroundRefresh()`, add:

```go
go h.backgroundHealthCheck()
```

- [ ] **Step 4: Add admin routes and handlers**

In `handleAdminAPI`, after the auto-refresh routes, add:

```go
case path == "/health-check" && r.Method == "GET":
	h.apiGetHealthCheck(w, r)
case path == "/health-check" && r.Method == "POST":
	h.apiUpdateHealthCheck(w, r)
```

After `apiUpdateAutoRefresh`, add:

```go
func (h *Handler) apiGetHealthCheck(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"settings": config.GetHealthCheckConfig(),
		"status":   h.getHealthCheckStatus(),
	})
}

func (h *Handler) apiUpdateHealthCheck(w http.ResponseWriter, r *http.Request) {
	var req config.HealthCheckConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	if err := config.UpdateHealthCheckConfig(req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	h.scheduleNextHealthCheck()
	h.notifyHealthCheckUpdated()
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
```

- [ ] **Step 5: Run admin API tests to verify GREEN**

Run:

```bash
go test ./proxy -run 'TestAdminHealthCheckConfig'
```

Expected: PASS.

- [ ] **Step 6: Run broader proxy tests**

Run:

```bash
go test ./proxy
```

Expected: PASS.

- [ ] **Step 7: Commit scheduler/API changes**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat: add health check scheduler api"
```

## Task 4: Admin UI Controls

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Add settings card markup**

In `web/index.html`, after the auto-refresh settings card and before the thinking settings card, add:

```html
            <div class="card">
                <div class="card-header"><span class="card-title" data-i18n="settings.healthCheck"></span></div>
                <div class="form-group">
                    <label style="display:flex;align-items:center;gap:10px">
                        <label class="switch"><input type="checkbox" id="healthCheckEnabled"><span
                                class="slider"></span></label>
                        <span data-i18n="settings.healthCheckEnabled"></span>
                    </label>
                </div>
                <div class="form-group">
                    <label data-i18n="settings.healthCheckInterval"></label>
                    <input type="number" id="healthCheckInterval" min="5" max="1440" value="60">
                    <small style="color:#64748b;font-size:12px;margin-top:4px;display:block"
                        data-i18n="settings.healthCheckIntervalHint"></small>
                </div>
                <div class="form-group">
                    <label style="display:flex;align-items:center;gap:10px">
                        <label class="switch"><input type="checkbox" id="healthCheckAutoDisable"><span
                                class="slider"></span></label>
                        <span data-i18n="settings.healthCheckAutoDisable"></span>
                    </label>
                </div>
                <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:8px;margin-bottom:16px;font-size:13px;color:#475569">
                    <div><span data-i18n="settings.healthCheckRunning"></span>: <span id="healthCheckRunning">-</span></div>
                    <div><span data-i18n="settings.healthCheckLastRun"></span>: <span id="healthCheckLastRun">-</span></div>
                    <div><span data-i18n="settings.healthCheckNextRun"></span>: <span id="healthCheckNextRun">-</span></div>
                    <div><span data-i18n="settings.healthCheckLastResult"></span>: <span id="healthCheckLastResult">-</span></div>
                </div>
                <button class="btn btn-primary" onclick="saveHealthCheckConfig()"
                    data-i18n="settings.saveHealthCheck"></button>
            </div>
```

- [ ] **Step 2: Add Chinese i18n strings**

In the Chinese i18n object near existing `settings.autoRefresh*` strings, add:

```js
                'settings.healthCheck': '账号健康检测',
                'settings.healthCheckEnabled': '启用定时健康检测',
                'settings.healthCheckInterval': '检测间隔（分钟）',
                'settings.healthCheckIntervalHint': '范围 5-1440 分钟，仅检测启用账号；通过加载可用模型判断账号健康状态',
                'settings.healthCheckAutoDisable': '检测失败时自动禁用账号',
                'settings.healthCheckRunning': '正在运行',
                'settings.healthCheckLastRun': '上次执行',
                'settings.healthCheckNextRun': '下次执行',
                'settings.healthCheckLastResult': '上次结果',
                'settings.healthCheckSaved': '账号健康检测设置已保存',
                'settings.healthCheckInvalidInterval': '检测间隔必须是 5-1440 之间的整数',
                'settings.healthCheckDisabled': '已禁用',
                'settings.saveHealthCheck': '保存健康检测设置',
```

- [ ] **Step 3: Add English i18n strings**

In the English i18n object near existing `settings.autoRefresh*` strings, add:

```js
                'settings.healthCheck': 'Account Health Check',
                'settings.healthCheckEnabled': 'Enable scheduled health checks',
                'settings.healthCheckInterval': 'Check interval (minutes)',
                'settings.healthCheckIntervalHint': 'Allowed range: 5-1440 minutes. Only enabled accounts are checked by loading available models.',
                'settings.healthCheckAutoDisable': 'Automatically disable accounts when checks fail',
                'settings.healthCheckRunning': 'Running',
                'settings.healthCheckLastRun': 'Last run',
                'settings.healthCheckNextRun': 'Next run',
                'settings.healthCheckLastResult': 'Last result',
                'settings.healthCheckSaved': 'Account health check settings saved',
                'settings.healthCheckInvalidInterval': 'Check interval must be an integer from 5 to 1440',
                'settings.healthCheckDisabled': 'Disabled',
                'settings.saveHealthCheck': 'Save Health Check Settings',
```

- [ ] **Step 4: Load health check settings with settings page**

In `loadSettings()`, after `loadAutoRefreshConfig();`, add:

```js
            loadHealthCheckConfig();
```

- [ ] **Step 5: Add health check JavaScript functions**

After `saveAutoRefreshConfig()`, add:

```js
        function renderHealthCheckStatus(status) {
            status = status || {};
            document.getElementById('healthCheckRunning').textContent = status.running ? t('settings.autoRefreshYes') : t('settings.autoRefreshNo');
            document.getElementById('healthCheckLastRun').textContent = formatAutoRefreshTime(status.lastFinishedAt || status.lastStartedAt);
            document.getElementById('healthCheckNextRun').textContent = formatAutoRefreshTime(status.nextRunAt);
            const results = [
                formatAutoRefreshResultValue(t('stats.success'), status.lastSuccess),
                formatAutoRefreshResultValue(t('stats.failed'), status.lastFailed),
                formatAutoRefreshResultValue(t('settings.healthCheckDisabled'), status.lastDisabled),
                formatAutoRefreshResultValue(t('settings.autoRefreshSkipped'), status.lastSkipped)
            ].filter(Boolean);
            document.getElementById('healthCheckLastResult').textContent = results.length ? results.join(', ') : '-';
        }
        async function loadHealthCheckConfig() {
            try {
                const res = await fetch('/admin/api/health-check', { headers: { 'X-Admin-Password': password } });
                const d = await res.json();
                const settings = d.settings || {};
                document.getElementById('healthCheckEnabled').checked = !!settings.enabled;
                document.getElementById('healthCheckInterval').value = settings.intervalMinutes || 60;
                document.getElementById('healthCheckAutoDisable').checked = !!settings.autoDisableUnhealthy;
                renderHealthCheckStatus(d.status);
            } catch (e) {
                renderHealthCheckStatus();
            }
        }
        async function saveHealthCheckConfig() {
            const intervalRaw = document.getElementById('healthCheckInterval').value.trim();
            const intervalMinutes = Number(intervalRaw);
            if (!Number.isFinite(intervalMinutes) || !Number.isInteger(intervalMinutes) || intervalMinutes < 5 || intervalMinutes > 1440) {
                alert(t('settings.healthCheckInvalidInterval'));
                return;
            }
            try {
                const res = await fetch('/admin/api/health-check', {
                    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Admin-Password': password },
                    body: JSON.stringify({
                        enabled: document.getElementById('healthCheckEnabled').checked,
                        intervalMinutes,
                        autoDisableUnhealthy: document.getElementById('healthCheckAutoDisable').checked
                    })
                });
                let d = {};
                try { d = await res.json(); } catch (e) { d = {}; }
                if (!res.ok || !d.success) {
                    alert(t('common.saveFailed') + (d.error ? ': ' + d.error : ''));
                    return;
                }
                alert(t('settings.healthCheckSaved'));
                loadHealthCheckConfig();
            } catch (e) {
                alert(t('common.saveFailed') + ': ' + e.message);
            }
        }
```

- [ ] **Step 6: Run Go tests after UI edit**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit UI changes**

Run:

```bash
git add web/index.html
git commit -m "feat: add health check admin controls"
```

## Task 5: Final Verification

**Files:**
- Verify all changed files.

- [ ] **Step 1: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Check worktree**

Run:

```bash
git status --short
```

Expected: only pre-existing unrelated untracked `data/` entries may remain.

- [ ] **Step 3: Inspect recent commits**

Run:

```bash
git log --oneline -5
```

Expected: recent commits include:

```text
feat: add health check admin controls
feat: add health check scheduler api
feat: add account health check helpers
feat: add health check config
docs: design account health check
```

## Self-Review

Spec coverage:

- Independent health check settings: Task 1.
- Backend scheduler and enabled-only checks: Tasks 2 and 3.
- Reuse `ListAvailableModels`: Task 3.
- Optional auto-disable with `UNHEALTHY` status: Task 2 and Task 3.
- Admin API: Task 3.
- Admin UI and i18n: Task 4.
- Tests and final verification: Tasks 1, 2, 3, and 5.

Placeholder scan:

- No `TBD`, `TODO`, incomplete implementation placeholders, or "similar to" shortcuts remain.

Type consistency:

- Config type is `config.HealthCheckConfig`.
- Runtime status type is `healthCheckStatus`.
- Batch result type is `healthCheckBatchResult`.
- JSON setting name is `autoDisableUnhealthy` in Go and JavaScript.
