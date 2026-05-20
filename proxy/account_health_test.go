package proxy

import (
	"errors"
	"kiro-go/config"
	"kiro-go/pool"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelectHealthCheckAccountsOnlyEnabled(t *testing.T) {
	now := time.Unix(2000, 0)
	accounts := []config.Account{
		{ID: "enabled-1", Enabled: true},
		{ID: "disabled-1", Enabled: false},
		{ID: "enabled-2", Enabled: true, CooldownUntil: now.Add(-time.Hour).Unix(), LastFailureReason: "quota_exhausted"},
		{ID: "cooling-1", Enabled: true, CooldownUntil: now.Add(time.Hour).Unix(), LastFailureReason: string(pool.FailureReasonQuotaExhausted)},
	}

	got, skipped := selectHealthCheckAccountsForTime(accounts, now)
	if len(got) != 2 {
		t.Fatalf("expected 2 enabled accounts, got %d", len(got))
	}
	if got[0].ID != "enabled-1" || got[1].ID != "enabled-2" {
		t.Fatalf("unexpected enabled account order: %#v", got)
	}
	if skipped != 1 {
		t.Fatalf("expected one cooling account skipped, got %d", skipped)
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

func TestRunHealthCheckBatchDoesNotDisableQuotaExhaustedAccounts(t *testing.T) {
	accounts := []config.Account{{ID: "quota"}}
	var disabled []string

	result := runHealthCheckBatch(accounts, true, func(account *config.Account) error {
		return errors.New("quota exhausted on Kiro IDE")
	}, func(account *config.Account, reason string, now int64) error {
		disabled = append(disabled, account.ID)
		return nil
	}, 123)

	if result.Success != 0 || result.Failed != 1 || result.Disabled != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(disabled) != 0 {
		t.Fatalf("quota exhaustion should not disable accounts, disabled %#v", disabled)
	}
}

func TestRunHealthCheckBatchQuietModeSkipsCooldownAccount(t *testing.T) {
	oldGate := modelAdmissionGate
	now := time.Unix(2000, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", 429, time.Second, now.Add(time.Minute))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", 429, time.Second, now.Add(time.Minute))
	t.Cleanup(func() { modelAdmissionGate = oldGate })

	accounts := []config.Account{
		{ID: "ok", Enabled: true},
		{ID: "cooling", Enabled: true, CooldownUntil: now.Add(time.Hour).Unix()},
	}

	selected, skipped := selectHealthCheckAccountsForTime(accounts, now)
	if skipped != 1 {
		t.Fatalf("expected one quiet-mode selection skip, got %d", skipped)
	}
	if len(selected) != 2 {
		t.Fatalf("expected quiet-mode account to be deferred to batch skip, got %#v", selected)
	}

	var checked []string
	result := runHealthCheckBatch(selected, true, func(account *config.Account) error {
		checked = append(checked, account.ID)
		return nil
	}, func(account *config.Account, reason string, now int64) error {
		t.Fatalf("disable should not be called")
		return nil
	}, now.Unix())
	result.Skipped = skipped

	if result.Success != 1 || result.Failed != 0 || result.Disabled != 0 || result.Skipped != 1 || result.QuietSkipped != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(checked) != 1 || checked[0] != "ok" {
		t.Fatalf("quiet-mode cooldown account should not be checked, got calls %#v", checked)
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

	h.finishHealthCheck(healthCheckBatchResult{Success: 1, Failed: 0, Disabled: 0, Skipped: 2}, 400, 7200)
	status = h.getHealthCheckStatus()
	if status.LastSkippedCount != 2 {
		t.Fatalf("expected last skipped count 2, got %d", status.LastSkippedCount)
	}
}

func TestHealthCheckStatusRecordsQuietModeSkips(t *testing.T) {
	h := &Handler{}

	h.finishHealthCheck(healthCheckBatchResult{Success: 1, Failed: 0, Disabled: 0, Skipped: 2, QuietSkipped: 1}, 400, 7200)

	status := h.getHealthCheckStatus()
	if status.LastSkippedCount != 2 {
		t.Fatalf("expected last skipped count 2, got %d", status.LastSkippedCount)
	}
	if status.LastQuietSkipped != 1 {
		t.Fatalf("expected last quiet skipped 1, got %d", status.LastQuietSkipped)
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

func TestDisableUnhealthyAccountReturnsErrorWhenAccountMissing(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	err := disableUnhealthyAccount(&config.Account{ID: "missing", Email: "missing@example.com"}, "boom", 123)
	if err == nil {
		t.Fatalf("expected missing account to return error")
	}
}

func TestDisableUnhealthyAccountPersistsUnhealthyStatus(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "account-1", Email: "account@example.com", Enabled: true}); err != nil {
		t.Fatalf("add account: %v", err)
	}

	account := config.Account{ID: "account-1", Email: "account@example.com", Enabled: true}
	if err := disableUnhealthyAccount(&account, "boom", 123); err != nil {
		t.Fatalf("disable unhealthy account: %v", err)
	}

	accounts := config.GetAccounts()
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	got := accounts[0]
	if got.Enabled {
		t.Fatalf("expected account to be disabled")
	}
	if got.BanStatus != "UNHEALTHY" {
		t.Fatalf("expected unhealthy ban status, got %q", got.BanStatus)
	}
	if got.BanReason != "boom" {
		t.Fatalf("expected ban reason boom, got %q", got.BanReason)
	}
	if got.BanTime != 123 {
		t.Fatalf("expected ban time 123, got %d", got.BanTime)
	}
}

func TestCheckAccountHealthClearsCooldownAfterSuccessfulModelList(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:                "account-1",
		Email:             "account@example.com",
		Enabled:           true,
		AccessToken:       "token",
		ProfileArn:        "arn:aws:codewhisperer:profile/test",
		LastFailureReason: "quota_exhausted",
		LastFailureAt:     time.Now().Add(-time.Minute).Unix(),
		CooldownUntil:     time.Now().Add(time.Hour).Unix(),
		FailureCount:      1,
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}

	var loadedAccountID string
	listAvailableModelsForHealthCheck = func(account *config.Account) ([]ModelInfo, error) {
		loadedAccountID = account.ID
		return []ModelInfo{{ModelId: "claude-sonnet-4.5"}}, nil
	}
	t.Cleanup(func() {
		listAvailableModelsForHealthCheck = ListAvailableModels
	})

	if err := h.checkAccountHealth(&account); err != nil {
		t.Fatalf("expected health check to pass, got %v", err)
	}
	if loadedAccountID != account.ID {
		t.Fatalf("expected model list check for account %q, got %q", account.ID, loadedAccountID)
	}
	if p.IsCoolingDown(account.ID, time.Now()) {
		t.Fatalf("expected successful probe to clear pool cooldown")
	}
	got := config.GetAccounts()[0]
	if got.LastFailureReason != "" || got.LastFailureAt != 0 || got.CooldownUntil != 0 || got.FailureCount != 0 {
		t.Fatalf("expected successful probe to clear persisted health fields, got %#v", got)
	}
}

func TestCheckAccountHealthRecordsFailureWhenModelListFails(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:          "account-1",
		Email:       "account@example.com",
		Enabled:     true,
		AccessToken: "token",
		ProfileArn:  "arn:aws:codewhisperer:profile/test",
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}

	listAvailableModelsForHealthCheck = func(account *config.Account) ([]ModelInfo, error) {
		return nil, errors.New("quota exhausted on Kiro IDE")
	}
	t.Cleanup(func() {
		listAvailableModelsForHealthCheck = ListAvailableModels
	})

	if err := h.checkAccountHealth(&account); err == nil {
		t.Fatalf("expected health check to fail")
	}
	if !p.IsCoolingDown(account.ID, time.Now()) {
		t.Fatalf("expected failed probe to put account in cooldown")
	}
	got := config.GetAccounts()[0]
	if got.LastFailureReason != "quota_exhausted" || got.FailureCount != 1 || got.CooldownUntil == 0 {
		t.Fatalf("expected quota health fields after failed probe, got %#v", got)
	}
}
