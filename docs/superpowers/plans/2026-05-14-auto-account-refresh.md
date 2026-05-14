# Auto Account Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build configurable backend scheduled account refresh that keeps running when the admin page is closed.

**Architecture:** Upgrade the existing fixed `backgroundRefresh` loop into a configurable scheduler owned by `proxy.Handler`. Store settings in `config.Config`, expose an admin API for settings and runtime status, reuse one account refresh helper across manual single refresh, manual batch refresh, and automatic runs, then add controls to the existing settings tab in `web/index.html`.

**Tech Stack:** Go standard library HTTP/timers/sync, existing `config`, `proxy`, `pool`, and `auth` packages, plain HTML/CSS/JavaScript single-page admin UI, `go test ./...`.

---

## File Structure

- Modify `config/config.go`: add persisted `AutoRefreshConfig`, defaults, validation, getters, and updater.
- Create `config/config_test.go`: cover config defaults and validation.
- Create `proxy/account_refresh.go`: shared account refresh helper, batch runner, scheduler status structs, and status helpers.
- Modify `proxy/handler.go`: add scheduler fields, replace fixed background account refresh with configurable scheduling, add `/admin/api/auto-refresh` routes, update manual refresh endpoints to reuse shared helper.
- Create `proxy/account_refresh_test.go`: cover scope filtering and overlap protection with dependency-injected refresh functions.
- Modify `web/index.html`: add settings UI, i18n strings, load/save/status JavaScript for automatic refresh.

## Task 1: Config Model And Validation

**Files:**
- Modify: `config/config.go`
- Create: `config/config_test.go`

- [ ] **Step 1: Write failing tests for defaults and validation**

Create `config/config_test.go`:

```go
package config

import "testing"

func TestDefaultAutoRefreshConfig(t *testing.T) {
	got := defaultAutoRefreshConfig()
	if !got.Enabled {
		t.Fatalf("expected auto refresh enabled by default")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected default interval 60, got %d", got.IntervalMinutes)
	}
	if got.Scope != AutoRefreshScopeEnabled {
		t.Fatalf("expected default scope %q, got %q", AutoRefreshScopeEnabled, got.Scope)
	}
}

func TestNormalizeAutoRefreshConfigAppliesDefaults(t *testing.T) {
	got := normalizeAutoRefreshConfig(AutoRefreshConfig{})
	if !got.Enabled {
		t.Fatalf("expected zero config to default to enabled")
	}
	if got.IntervalMinutes != 60 {
		t.Fatalf("expected interval default 60, got %d", got.IntervalMinutes)
	}
	if got.Scope != AutoRefreshScopeEnabled {
		t.Fatalf("expected scope default %q, got %q", AutoRefreshScopeEnabled, got.Scope)
	}
}

func TestValidateAutoRefreshConfig(t *testing.T) {
	valid := AutoRefreshConfig{Enabled: true, IntervalMinutes: 5, Scope: AutoRefreshScopeAll}
	if err := ValidateAutoRefreshConfig(valid); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	tooSmall := AutoRefreshConfig{Enabled: true, IntervalMinutes: 4, Scope: AutoRefreshScopeEnabled}
	if err := ValidateAutoRefreshConfig(tooSmall); err == nil {
		t.Fatalf("expected interval below minimum to fail")
	}

	tooLarge := AutoRefreshConfig{Enabled: true, IntervalMinutes: 1441, Scope: AutoRefreshScopeEnabled}
	if err := ValidateAutoRefreshConfig(tooLarge); err == nil {
		t.Fatalf("expected interval above maximum to fail")
	}

	badScope := AutoRefreshConfig{Enabled: true, IntervalMinutes: 60, Scope: "disabled"}
	if err := ValidateAutoRefreshConfig(badScope); err == nil {
		t.Fatalf("expected invalid scope to fail")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./config`

Expected: FAIL with undefined names such as `defaultAutoRefreshConfig`, `AutoRefreshConfig`, and `AutoRefreshScopeEnabled`.

- [ ] **Step 3: Implement auto-refresh config**

In `config/config.go`, add constants and type near `Config`:

```go
const (
	AutoRefreshScopeEnabled = "enabled"
	AutoRefreshScopeAll     = "all"

	AutoRefreshMinIntervalMinutes     = 5
	AutoRefreshMaxIntervalMinutes     = 1440
	AutoRefreshDefaultIntervalMinutes = 60
)

type AutoRefreshConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"intervalMinutes"`
	Scope           string `json:"scope"`
}
```

Add the field to `Config` after `Accounts`:

```go
	AutoRefresh AutoRefreshConfig `json:"autoRefresh"`
```

Add helper functions after the `var` block:

```go
func defaultAutoRefreshConfig() AutoRefreshConfig {
	return AutoRefreshConfig{
		Enabled:         true,
		IntervalMinutes: AutoRefreshDefaultIntervalMinutes,
		Scope:           AutoRefreshScopeEnabled,
	}
}

func normalizeAutoRefreshConfig(in AutoRefreshConfig) AutoRefreshConfig {
	defaults := defaultAutoRefreshConfig()
	if in.IntervalMinutes == 0 {
		in.IntervalMinutes = defaults.IntervalMinutes
	}
	if in.Scope == "" {
		in.Scope = defaults.Scope
	}
	return in
}

func ValidateAutoRefreshConfig(in AutoRefreshConfig) error {
	if in.IntervalMinutes < AutoRefreshMinIntervalMinutes || in.IntervalMinutes > AutoRefreshMaxIntervalMinutes {
		return fmt.Errorf("intervalMinutes must be between %d and %d", AutoRefreshMinIntervalMinutes, AutoRefreshMaxIntervalMinutes)
	}
	if in.Scope != AutoRefreshScopeEnabled && in.Scope != AutoRefreshScopeAll {
		return fmt.Errorf("scope must be %q or %q", AutoRefreshScopeEnabled, AutoRefreshScopeAll)
	}
	return nil
}
```

In the missing-file default config inside `Load`, set:

```go
				AutoRefresh:   defaultAutoRefreshConfig(),
```

After JSON unmarshal in `Load`, before `cfg = &c`, add:

```go
	c.AutoRefresh = normalizeAutoRefreshConfig(c.AutoRefresh)
```

Add public accessors near `GetAccounts`:

```go
func GetAutoRefreshConfig() AutoRefreshConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return normalizeAutoRefreshConfig(cfg.AutoRefresh)
}

func UpdateAutoRefreshConfig(autoRefresh AutoRefreshConfig) error {
	normalized := normalizeAutoRefreshConfig(autoRefresh)
	if err := ValidateAutoRefreshConfig(normalized); err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg.AutoRefresh = normalized
	return Save()
}
```

- [ ] **Step 4: Run config tests**

Run: `go test ./config`

Expected: PASS.

- [ ] **Step 5: Commit config task**

Run:

```bash
git add config/config.go config/config_test.go
git commit -m "feat: add auto refresh config"
```

Expected: commit succeeds.

## Task 2: Shared Refresh Runner

**Files:**
- Create: `proxy/account_refresh.go`
- Create: `proxy/account_refresh_test.go`

- [ ] **Step 1: Write failing tests for account selection and overlap guard**

Create `proxy/account_refresh_test.go`:

```go
package proxy

import (
	"errors"
	"kiro-go/config"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelectAutoRefreshAccountsHonorsScope(t *testing.T) {
	accounts := []config.Account{
		{ID: "enabled-1", Enabled: true},
		{ID: "disabled-1", Enabled: false},
		{ID: "enabled-2", Enabled: true},
	}

	enabledOnly := selectAutoRefreshAccounts(accounts, config.AutoRefreshScopeEnabled)
	if len(enabledOnly) != 2 {
		t.Fatalf("expected 2 enabled accounts, got %d", len(enabledOnly))
	}
	if enabledOnly[0].ID != "enabled-1" || enabledOnly[1].ID != "enabled-2" {
		t.Fatalf("unexpected enabled-only order: %#v", enabledOnly)
	}

	all := selectAutoRefreshAccounts(accounts, config.AutoRefreshScopeAll)
	if len(all) != 3 {
		t.Fatalf("expected all 3 accounts, got %d", len(all))
	}
}

func TestRunRefreshBatchContinuesAfterFailure(t *testing.T) {
	accounts := []config.Account{
		{ID: "ok-1"},
		{ID: "bad"},
		{ID: "ok-2"},
	}

	var calls int32
	result := runRefreshBatch(accounts, func(account *config.Account) error {
		atomic.AddInt32(&calls, 1)
		if account.ID == "bad" {
			return errors.New("refresh failed")
		}
		return nil
	})

	if calls != 3 {
		t.Fatalf("expected all accounts to be attempted, got %d", calls)
	}
	if result.Success != 2 || result.Failed != 1 {
		t.Fatalf("expected 2 success and 1 failed, got %#v", result)
	}
}

func TestTryBeginAutoRefreshPreventsOverlap(t *testing.T) {
	h := &Handler{}

	if !h.tryBeginAutoRefresh(100) {
		t.Fatalf("expected first run to start")
	}
	if h.tryBeginAutoRefresh(200) {
		t.Fatalf("expected overlapping run to be rejected")
	}

	status := h.getAutoRefreshStatus()
	if !status.Running {
		t.Fatalf("expected running status while first run is active")
	}
	if !status.LastSkipped {
		t.Fatalf("expected skipped flag after overlap attempt")
	}

	h.finishAutoRefresh(refreshBatchResult{Success: 1, Failed: 0}, 300, 3600)
	status = h.getAutoRefreshStatus()
	if status.Running {
		t.Fatalf("expected running false after finish")
	}
	if status.LastFinishedAt != 300 {
		t.Fatalf("expected finish timestamp 300, got %d", status.LastFinishedAt)
	}
	if status.NextRunAt != 3600 {
		t.Fatalf("expected next run 3600, got %d", status.NextRunAt)
	}
}

func TestComputeNextRunAt(t *testing.T) {
	now := time.Unix(1000, 0)
	got := computeNextRunAt(now, config.AutoRefreshConfig{Enabled: true, IntervalMinutes: 60})
	if got != 4600 {
		t.Fatalf("expected 4600, got %d", got)
	}

	disabled := computeNextRunAt(now, config.AutoRefreshConfig{Enabled: false, IntervalMinutes: 60})
	if disabled != 0 {
		t.Fatalf("expected disabled next run 0, got %d", disabled)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `go test ./proxy -run 'TestSelectAutoRefreshAccountsHonorsScope|TestRunRefreshBatchContinuesAfterFailure|TestTryBeginAutoRefreshPreventsOverlap|TestComputeNextRunAt'`

Expected: FAIL with undefined functions/types.

- [ ] **Step 3: Implement shared refresh runner and scheduler status helpers**

Create `proxy/account_refresh.go`:

```go
package proxy

import (
	"fmt"
	"kiro-go/auth"
	"kiro-go/config"
	"kiro-go/logger"
	"strings"
	"time"
)

type refreshBatchResult struct {
	Success int
	Failed  int
}

type autoRefreshStatus struct {
	Running        bool  `json:"running"`
	LastStartedAt  int64 `json:"lastStartedAt"`
	LastFinishedAt int64 `json:"lastFinishedAt"`
	NextRunAt      int64 `json:"nextRunAt"`
	LastSuccess    int   `json:"lastSuccess"`
	LastFailed     int   `json:"lastFailed"`
	LastSkipped    bool  `json:"lastSkipped"`
}

func selectAutoRefreshAccounts(accounts []config.Account, scope string) []config.Account {
	if scope == config.AutoRefreshScopeAll {
		result := make([]config.Account, len(accounts))
		copy(result, accounts)
		return result
	}

	result := make([]config.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Enabled {
			result = append(result, account)
		}
	}
	return result
}

func runRefreshBatch(accounts []config.Account, refresh func(account *config.Account) error) refreshBatchResult {
	result := refreshBatchResult{}
	for i := range accounts {
		if err := refresh(&accounts[i]); err != nil {
			result.Failed++
			continue
		}
		result.Success++
	}
	return result
}

func computeNextRunAt(now time.Time, settings config.AutoRefreshConfig) int64 {
	if !settings.Enabled {
		return 0
	}
	normalized := settings
	if normalized.IntervalMinutes == 0 {
		normalized.IntervalMinutes = config.AutoRefreshDefaultIntervalMinutes
	}
	return now.Add(time.Duration(normalized.IntervalMinutes) * time.Minute).Unix()
}

func (h *Handler) tryBeginAutoRefresh(startedAt int64) bool {
	h.autoRefreshMu.Lock()
	defer h.autoRefreshMu.Unlock()
	if h.autoRefreshStatus.Running {
		h.autoRefreshStatus.LastSkipped = true
		return false
	}
	h.autoRefreshStatus.Running = true
	h.autoRefreshStatus.LastStartedAt = startedAt
	h.autoRefreshStatus.LastSkipped = false
	return true
}

func (h *Handler) finishAutoRefresh(result refreshBatchResult, finishedAt int64, nextRunAt int64) {
	h.autoRefreshMu.Lock()
	defer h.autoRefreshMu.Unlock()
	h.autoRefreshStatus.Running = false
	h.autoRefreshStatus.LastFinishedAt = finishedAt
	h.autoRefreshStatus.NextRunAt = nextRunAt
	h.autoRefreshStatus.LastSuccess = result.Success
	h.autoRefreshStatus.LastFailed = result.Failed
}

func (h *Handler) setNextAutoRefreshRun(nextRunAt int64) {
	h.autoRefreshMu.Lock()
	defer h.autoRefreshMu.Unlock()
	h.autoRefreshStatus.NextRunAt = nextRunAt
}

func (h *Handler) getAutoRefreshStatus() autoRefreshStatus {
	h.autoRefreshMu.RLock()
	defer h.autoRefreshMu.RUnlock()
	return h.autoRefreshStatus
}

func (h *Handler) refreshAccountData(account *config.Account) error {
	if account == nil {
		return fmt.Errorf("account is nil")
	}

	refreshTokenIfNeeded := func() error {
		if account.RefreshToken == "" {
			return nil
		}
		newAccessToken, newRefreshToken, newExpiresAt, profileArn, err := auth.RefreshToken(account)
		if err != nil {
			return err
		}
		account.AccessToken = newAccessToken
		if newRefreshToken != "" {
			account.RefreshToken = newRefreshToken
		}
		account.ExpiresAt = newExpiresAt
		if err := config.UpdateAccountToken(account.ID, newAccessToken, newRefreshToken, newExpiresAt); err != nil {
			return err
		}
		h.pool.UpdateToken(account.ID, newAccessToken, newRefreshToken, newExpiresAt)
		if profileArn != "" {
			account.ProfileArn = profileArn
			if err := config.UpdateAccountProfileArn(account.ID, profileArn); err != nil {
				return err
			}
		}
		return nil
	}

	if account.ExpiresAt > 0 && time.Now().Unix() > account.ExpiresAt-300 {
		if err := refreshTokenIfNeeded(); err != nil {
			return fmt.Errorf("token refresh failed: %w", err)
		}
	}

	info, err := RefreshAccountInfo(account)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "TEMPORARILY_SUSPENDED") || strings.Contains(errMsg, "Account suspended") {
			return nil
		}
		if strings.Contains(errMsg, "403") || strings.Contains(errMsg, "401") || strings.Contains(errMsg, "invalid") || strings.Contains(errMsg, "expired") {
			if refreshErr := refreshTokenIfNeeded(); refreshErr == nil {
				info, err = RefreshAccountInfo(account)
				if err != nil {
					if strings.Contains(err.Error(), "TEMPORARILY_SUSPENDED") || strings.Contains(err.Error(), "Account suspended") {
						return nil
					}
				}
			}
		}
		if err != nil {
			return err
		}
	}

	if err := config.UpdateAccountInfo(account.ID, *info); err != nil {
		return err
	}
	logger.Infof("[AccountRefresh] Refreshed %s: %s %.1f/%.1f", account.Email, info.SubscriptionType, info.UsageCurrent, info.UsageLimit)
	return nil
}
```

- [ ] **Step 4: Add fields to Handler**

In `proxy/handler.go`, add fields to `Handler` after `stopStatsSaver`:

```go
	autoRefreshMu     sync.RWMutex
	autoRefreshStatus autoRefreshStatus
```

- [ ] **Step 5: Run proxy unit tests**

Run: `go test ./proxy -run 'TestSelectAutoRefreshAccountsHonorsScope|TestRunRefreshBatchContinuesAfterFailure|TestTryBeginAutoRefreshPreventsOverlap|TestComputeNextRunAt'`

Expected: PASS.

- [ ] **Step 6: Commit shared runner task**

Run:

```bash
git add proxy/handler.go proxy/account_refresh.go proxy/account_refresh_test.go
git commit -m "feat: share account refresh runner"
```

Expected: commit succeeds.

## Task 3: Configurable Scheduler And Admin API

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/account_refresh.go`

- [ ] **Step 1: Replace fixed background account refresh with configurable scheduler**

In `proxy/handler.go`, replace `backgroundRefresh` and `refreshAllAccounts` with:

```go
func (h *Handler) backgroundRefresh() {
	h.refreshModelsCache()
	h.scheduleNextAutoRefresh()

	for {
		settings := config.GetAutoRefreshConfig()
		if !settings.Enabled {
			select {
			case <-time.After(time.Minute):
				continue
			case <-h.stopRefresh:
				return
			}
		}

		interval := time.Duration(settings.IntervalMinutes) * time.Minute
		select {
		case <-time.After(interval):
			h.runAutoRefresh()
			h.refreshModelsCache()
		case <-h.stopRefresh:
			return
		}
	}
}

func (h *Handler) scheduleNextAutoRefresh() {
	h.setNextAutoRefreshRun(computeNextRunAt(time.Now(), config.GetAutoRefreshConfig()))
}

func (h *Handler) runAutoRefresh() {
	settings := config.GetAutoRefreshConfig()
	now := time.Now()
	if !settings.Enabled {
		h.setNextAutoRefreshRun(0)
		return
	}
	if !h.tryBeginAutoRefresh(now.Unix()) {
		h.scheduleNextAutoRefresh()
		return
	}

	accounts := selectAutoRefreshAccounts(config.GetAccounts(), settings.Scope)
	result := runRefreshBatch(accounts, func(account *config.Account) error {
		err := h.refreshAccountData(account)
		if err != nil {
			logger.Warnf("[AutoRefresh] Failed to refresh %s: %v", account.Email, err)
		}
		return err
	})
	h.pool.Reload()

	finishedAt := time.Now()
	nextSettings := config.GetAutoRefreshConfig()
	h.finishAutoRefresh(result, finishedAt.Unix(), computeNextRunAt(finishedAt, nextSettings))
	logger.Infof("[AutoRefresh] Completed: success=%d failed=%d", result.Success, result.Failed)
}
```

- [ ] **Step 2: Add admin routes**

In `handleAdminAPI` in `proxy/handler.go`, add cases after settings routes:

```go
	case path == "/auto-refresh" && r.Method == "GET":
		h.apiGetAutoRefresh(w, r)
	case path == "/auto-refresh" && r.Method == "POST":
		h.apiUpdateAutoRefresh(w, r)
```

- [ ] **Step 3: Add admin handlers**

In `proxy/handler.go`, after `apiUpdateSettings`, add:

```go
func (h *Handler) apiGetAutoRefresh(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"settings": config.GetAutoRefreshConfig(),
		"status":   h.getAutoRefreshStatus(),
	})
}

func (h *Handler) apiUpdateAutoRefresh(w http.ResponseWriter, r *http.Request) {
	var req config.AutoRefreshConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	if err := config.UpdateAutoRefreshConfig(req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.scheduleNextAutoRefresh()
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
```

- [ ] **Step 4: Update single refresh API to use shared helper**

In `apiRefreshAccount`, keep the account lookup and 404 handling, then replace the duplicated refresh logic with:

```go
	if err := h.refreshAccountData(account); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
```

- [ ] **Step 5: Update manual batch refresh to use shared helper**

In `apiBatchAccounts`, replace the `case "refresh":` body with:

```go
	case "refresh":
		successCount := 0
		failCount := 0
		for _, id := range req.IDs {
			accounts := config.GetAccounts()
			var account *config.Account
			for i := range accounts {
				if accounts[i].ID == id {
					account = &accounts[i]
					break
				}
			}
			if account == nil {
				failCount++
				continue
			}
			if err := h.refreshAccountData(account); err != nil {
				logger.Warnf("[BatchRefresh] Failed to refresh %s: %v", account.Email, err)
				failCount++
				continue
			}
			successCount++
		}
		h.pool.Reload()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"refreshed": successCount,
			"failed":    failCount,
		})
```

- [ ] **Step 6: Run focused proxy tests**

Run: `go test ./proxy`

Expected: PASS.

- [ ] **Step 7: Commit scheduler and API task**

Run:

```bash
git add proxy/handler.go proxy/account_refresh.go
git commit -m "feat: add auto refresh scheduler api"
```

Expected: commit succeeds.

## Task 4: Admin UI Controls

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Add settings card markup**

In `web/index.html`, in `tabSettings` after the API settings card and before Thinking settings, add:

```html
            <div class="card">
                <div class="card-header"><span class="card-title" data-i18n="settings.autoRefresh"></span></div>
                <div class="form-group">
                    <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
                        <input type="checkbox" id="autoRefreshEnabled" checked>
                        <span data-i18n="settings.autoRefreshEnabled"></span>
                    </label>
                </div>
                <div class="form-group">
                    <label data-i18n="settings.autoRefreshInterval"></label>
                    <input type="number" id="autoRefreshInterval" min="5" max="1440" value="60">
                    <small style="color:#64748b;font-size:12px;margin-top:4px;display:block"
                        data-i18n="settings.autoRefreshIntervalHint"></small>
                </div>
                <div class="form-group">
                    <label data-i18n="settings.autoRefreshScope"></label>
                    <select id="autoRefreshScope">
                        <option value="enabled" data-i18n="settings.autoRefreshScopeEnabled"></option>
                        <option value="all" data-i18n="settings.autoRefreshScopeAll"></option>
                    </select>
                </div>
                <div class="endpoint" style="margin-bottom:12px;display:block;line-height:1.7">
                    <div><span data-i18n="settings.autoRefreshRunning"></span>: <span id="autoRefreshRunning">-</span></div>
                    <div><span data-i18n="settings.autoRefreshLastRun"></span>: <span id="autoRefreshLastRun">-</span></div>
                    <div><span data-i18n="settings.autoRefreshNextRun"></span>: <span id="autoRefreshNextRun">-</span></div>
                    <div><span data-i18n="settings.autoRefreshLastResult"></span>: <span id="autoRefreshLastResult">-</span></div>
                </div>
                <button class="btn btn-primary" onclick="saveAutoRefreshConfig()" data-i18n="settings.saveAutoRefresh"></button>
            </div>
```

- [ ] **Step 2: Add Chinese i18n keys**

In the Chinese i18n block near other `settings.*` keys, add:

```js
                'settings.autoRefresh': '自动刷新账号',
                'settings.autoRefreshEnabled': '启用自动刷新',
                'settings.autoRefreshInterval': '刷新间隔（分钟）',
                'settings.autoRefreshIntervalHint': '范围 5-1440 分钟，管理页面关闭后仍会在后端执行',
                'settings.autoRefreshScope': '刷新范围',
                'settings.autoRefreshScopeEnabled': '仅启用账号',
                'settings.autoRefreshScopeAll': '所有账号',
                'settings.autoRefreshRunning': '正在运行',
                'settings.autoRefreshLastRun': '上次执行',
                'settings.autoRefreshNextRun': '下次执行',
                'settings.autoRefreshLastResult': '上次结果',
                'settings.autoRefreshSaved': '自动刷新设置已保存',
                'settings.saveAutoRefresh': '保存自动刷新设置',
```

- [ ] **Step 3: Add English i18n keys**

In the English i18n block near other `settings.*` keys, add:

```js
                'settings.autoRefresh': 'Auto Account Refresh',
                'settings.autoRefreshEnabled': 'Enable automatic refresh',
                'settings.autoRefreshInterval': 'Refresh interval (minutes)',
                'settings.autoRefreshIntervalHint': 'Allowed range: 5-1440 minutes. Runs in the backend even when the admin page is closed.',
                'settings.autoRefreshScope': 'Refresh scope',
                'settings.autoRefreshScopeEnabled': 'Enabled accounts only',
                'settings.autoRefreshScopeAll': 'All accounts',
                'settings.autoRefreshRunning': 'Running',
                'settings.autoRefreshLastRun': 'Last run',
                'settings.autoRefreshNextRun': 'Next run',
                'settings.autoRefreshLastResult': 'Last result',
                'settings.autoRefreshSaved': 'Auto refresh settings saved',
                'settings.saveAutoRefresh': 'Save Auto Refresh Settings',
```

- [ ] **Step 4: Add JavaScript load/save helpers**

After `loadSettings`, add:

```js
        function formatAutoRefreshTime(ts) {
            if (!ts) return '-';
            return new Date(ts * 1000).toLocaleString();
        }
        function renderAutoRefreshStatus(status) {
            document.getElementById('autoRefreshRunning').textContent = status?.running ? 'Yes' : 'No';
            document.getElementById('autoRefreshLastRun').textContent = formatAutoRefreshTime(status?.lastFinishedAt || status?.lastStartedAt);
            document.getElementById('autoRefreshNextRun').textContent = formatAutoRefreshTime(status?.nextRunAt);
            if (status && (status.lastSuccess || status.lastFailed)) {
                document.getElementById('autoRefreshLastResult').textContent = status.lastSuccess + ' / ' + status.lastFailed;
            } else {
                document.getElementById('autoRefreshLastResult').textContent = '-';
            }
        }
        async function loadAutoRefreshConfig() {
            const res = await fetch('/admin/api/auto-refresh', { headers: { 'X-Admin-Password': password } });
            const d = await res.json();
            const settings = d.settings || {};
            document.getElementById('autoRefreshEnabled').checked = settings.enabled !== false;
            document.getElementById('autoRefreshInterval').value = settings.intervalMinutes || 60;
            document.getElementById('autoRefreshScope').value = settings.scope || 'enabled';
            renderAutoRefreshStatus(d.status || {});
        }
        async function saveAutoRefreshConfig() {
            const intervalMinutes = parseInt(document.getElementById('autoRefreshInterval').value, 10);
            const res = await fetch('/admin/api/auto-refresh', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-Admin-Password': password },
                body: JSON.stringify({
                    enabled: document.getElementById('autoRefreshEnabled').checked,
                    intervalMinutes,
                    scope: document.getElementById('autoRefreshScope').value
                })
            });
            const d = await res.json();
            if (d.success) {
                alert(t('settings.autoRefreshSaved'));
                loadAutoRefreshConfig();
            } else {
                alert(t('common.saveFailed') + ': ' + d.error);
            }
        }
```

- [ ] **Step 5: Load auto-refresh settings with the settings page**

Inside `loadSettings`, after `loadProxyConfig();`, add:

```js
            loadAutoRefreshConfig();
```

- [ ] **Step 6: Run a static sanity check**

Run: `rg -n "autoRefresh|settings.autoRefresh" web/index.html`

Expected: shows markup, i18n keys, and JavaScript functions.

- [ ] **Step 7: Commit UI task**

Run:

```bash
git add web/index.html
git commit -m "feat: add auto refresh admin controls"
```

Expected: commit succeeds.

## Task 5: Full Verification

**Files:**
- Modify only if verification finds defects in files changed by earlier tasks.

- [ ] **Step 1: Run all Go tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Build the binary**

Run: `go build ./...`

Expected: PASS with no output.

- [ ] **Step 3: Start local server**

Run: `CONFIG_PATH=/tmp/kiro-go-auto-refresh-test.json ADMIN_PASSWORD=testpass go run .`

Expected: server logs include admin URL and keep running.

- [ ] **Step 4: Verify API defaults**

In a second shell while the server is running, run:

```bash
curl -s -H 'X-Admin-Password: testpass' http://127.0.0.1:8080/admin/api/auto-refresh
```

Expected JSON includes:

```json
"settings":{"enabled":true,"intervalMinutes":60,"scope":"enabled"}
```

- [ ] **Step 5: Verify API validation**

Run:

```bash
curl -s -i -X POST -H 'Content-Type: application/json' -H 'X-Admin-Password: testpass' \
  -d '{"enabled":true,"intervalMinutes":4,"scope":"enabled"}' \
  http://127.0.0.1:8080/admin/api/auto-refresh
```

Expected: HTTP 400 and JSON error mentioning `intervalMinutes`.

- [ ] **Step 6: Verify API update**

Run:

```bash
curl -s -X POST -H 'Content-Type: application/json' -H 'X-Admin-Password: testpass' \
  -d '{"enabled":false,"intervalMinutes":120,"scope":"all"}' \
  http://127.0.0.1:8080/admin/api/auto-refresh
curl -s -H 'X-Admin-Password: testpass' http://127.0.0.1:8080/admin/api/auto-refresh
```

Expected: first response is `{"success":true}` and second response includes `"enabled":false`, `"intervalMinutes":120`, and `"scope":"all"`.

- [ ] **Step 7: Browser-check settings UI**

Open `http://127.0.0.1:8080/admin`, log in with `testpass`, go to Settings, verify:

- Auto Account Refresh card is visible.
- Switch, interval, scope, status, and save button are visible.
- Saving invalid interval shows backend error.
- Saving valid values updates the status without reloading the page.

- [ ] **Step 8: Commit verification fixes if needed**

If any fixes were needed, run:

```bash
git add config proxy web
git commit -m "fix: stabilize auto refresh verification"
```

Expected: commit succeeds only if there were changes.
