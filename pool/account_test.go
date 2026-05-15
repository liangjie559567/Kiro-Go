package pool

import (
	"errors"
	"kiro-go/config"
	"testing"
	"time"
)

func TestOverageAccountsAreSkippedByDefault(t *testing.T) {
	p := &AccountPool{}
	normal := config.Account{ID: "normal"}
	overLimit := config.Account{ID: "over", UsageCurrent: 10, UsageLimit: 10}

	p.accounts = []config.Account{normal, overLimit}

	for i := 0; i < 5; i++ {
		acc := p.GetNext()
		if acc == nil {
			t.Fatalf("expected an account")
		}
		if acc.ID == "over" {
			t.Fatalf("expected over-limit account to be skipped by default")
		}
	}
}

func TestOverageAccountsCanBeSelectedWhenAllowed(t *testing.T) {
	p := &AccountPool{}
	overLimit := config.Account{
		ID:            "over",
		UsageCurrent:  10,
		UsageLimit:    10,
		AllowOverage:  true,
		OverageWeight: 1,
	}

	p.accounts = []config.Account{overLimit}

	acc := p.GetNext()
	if acc == nil {
		t.Fatalf("expected allowed overage account")
	}
	if acc.ID != "over" {
		t.Fatalf("expected overage account, got %q", acc.ID)
	}
}

func TestOverageWeightIsLowerThanNormalWeight(t *testing.T) {
	normalWeight := effectiveWeight(1) * overageFrequencyScale
	overageWeight := effectiveOverageWeight(1)

	if overageWeight >= normalWeight {
		t.Fatalf("expected overage weight %d to be lower than normal weight %d", overageWeight, normalWeight)
	}
}

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
		{name: "server error", err: errors.New("HTTP 503 from Kiro IDE"), want: FailureReasonUpstream5xx},
	}

	for _, tc := range tests {
		if got := ClassifyFailureReason(tc.err); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestRecordFailureAppliesCooldownByReason(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}

	p.RecordFailure("acct-1", FailureReasonQuotaExhausted)
	if !p.IsCoolingDown("acct-1", time.Now()) {
		t.Fatal("expected quota failure to cool down account")
	}
	if got := p.GetAllAccounts()[0].LastFailureReason; got != string(FailureReasonQuotaExhausted) {
		t.Fatalf("expected failure reason to persist, got %q", got)
	}
}

func TestRecordSuccessClearsCooldownAndFailureState(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}

	p.RecordFailure("acct-1", FailureReasonRateLimited)
	p.RecordSuccess("acct-1")

	if p.IsCoolingDown("acct-1", time.Now()) {
		t.Fatal("expected success to clear cooldown")
	}
	got := p.GetAllAccounts()[0]
	if got.LastFailureReason != "" || got.CooldownUntil != 0 || got.FailureCount != 0 {
		t.Fatalf("expected cleared failure state, got %#v", got)
	}
}

func TestGetNextExceptSkipsExcludedAccount(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1"}, {ID: "acct-2"}},
		totalAccounts: 2,
	}

	got := p.GetNextExcept(map[string]bool{"acct-1": true})
	if got == nil {
		t.Fatal("expected an account")
	}
	if got.ID != "acct-2" {
		t.Fatalf("expected acct-2, got %q", got.ID)
	}
}

func TestGetNextSkipsCoolingAccount(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}

	p.RecordFailure("acct-1", FailureReasonQuotaExhausted)

	if got := p.GetNext(); got != nil {
		t.Fatalf("expected cooling account to be skipped, got %q", got.ID)
	}
}
