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
