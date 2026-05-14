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
	accounts := []config.Account{{ID: "ok-1"}, {ID: "bad"}, {ID: "ok-2"}}

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
