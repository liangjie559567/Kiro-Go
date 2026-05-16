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
		{name: "model rate limit body", err: errors.New(`HTTP 429 from Kiro IDE: {"message":"model claude-opus-4.7 is throttled","reason":"MODEL_RATE_LIMIT"}`), want: FailureReasonRateLimited},
		{name: "network", err: errors.New("dial tcp timeout"), want: FailureReasonTransientNetwork},
		{name: "http2 internal stream reset", err: errors.New("stream error: stream ID 397; INTERNAL_ERROR; received from peer"), want: FailureReasonTransientNetwork},
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

func TestRecordFailureUntilUsesExplicitRateLimitReset(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}
	resetAt := time.Now().Add(3 * time.Second).Truncate(time.Second)

	p.RecordFailureUntil("acct-1", FailureReasonRateLimited, resetAt)

	got := p.GetAllAccounts()[0]
	if got.LastFailureReason != string(FailureReasonRateLimited) {
		t.Fatalf("expected rate_limited reason, got %q", got.LastFailureReason)
	}
	if got.CooldownUntil != resetAt.Unix() {
		t.Fatalf("expected cooldown until %d, got %d", resetAt.Unix(), got.CooldownUntil)
	}
	if !p.IsCoolingDown("acct-1", time.Now()) {
		t.Fatalf("expected account to be cooling down")
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

func TestGetNextAllowsExpiredPersistedCooldown(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1", CooldownUntil: time.Now().Add(-time.Second).Unix(), LastFailureReason: string(FailureReasonRateLimited), FailureCount: 2}},
		totalAccounts: 1,
	}

	got := p.GetNext()
	if got == nil {
		t.Fatalf("expected expired cooldown account to be schedulable")
	}
	if got.ID != "acct-1" {
		t.Fatalf("expected acct-1, got %q", got.ID)
	}
	state := p.GetAllAccounts()[0]
	if state.LastFailureReason != "" || state.CooldownUntil != 0 || state.FailureCount != 0 {
		t.Fatalf("expected expired cooldown state to be cleared, got %#v", state)
	}
}

func TestAvailableCountSkipsPersistedCooldown(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1", CooldownUntil: time.Now().Add(time.Minute).Unix()}},
		totalAccounts: 1,
	}

	if got := p.AvailableCount(); got != 0 {
		t.Fatalf("expected persisted cooldown account to be unavailable, got %d", got)
	}
}

func TestGetNextKeepsFiveMinuteTokenAvailable(t *testing.T) {
	p := &AccountPool{}
	account := config.Account{
		ID:          "acct-1",
		AccessToken: "access-token",
		ExpiresAt:   time.Now().Unix() + 300,
	}

	p.accounts = []config.Account{account}

	got := p.GetNext()
	if got == nil {
		t.Fatalf("expected five-minute token to be available")
	}
	if got.ID != account.ID {
		t.Fatalf("expected account %q, got %q", account.ID, got.ID)
	}
}

func TestGetNextForModelExceptSkipsUnsupportedAndExcludedAccounts(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "acct-1"}, {ID: "acct-2"}, {ID: "acct-3"}},
		totalAccounts: 3,
	}
	p.SetModelList("acct-1", []string{"claude-sonnet-4.6"})
	p.SetModelList("acct-2", []string{"claude-opus-4.7"})
	p.SetModelList("acct-3", []string{"claude-opus-4.7"})

	got := p.GetNextForModelExcept("claude-opus-4.7", map[string]bool{"acct-2": true})
	if got == nil {
		t.Fatalf("expected account supporting model")
	}
	if got.ID != "acct-3" {
		t.Fatalf("expected acct-3, got %q", got.ID)
	}
}

func TestGetNextForModelPrefersLessBusyHealthyAccount(t *testing.T) {
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
	if got == nil {
		t.Fatalf("expected an account")
	}
	if got.ID != "acct-2" {
		t.Fatalf("expected less busy acct-2, got %q", got.ID)
	}
}

func TestGetNextForModelLeastConnectionsStrategyPrefersLessBusyAccount(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "busy"}, {ID: "idle"}},
		totalAccounts: 2,
		currentIndex:  1,
	}
	p.SetStrategy(StrategyLeastConnections)
	p.SetModelList("busy", []string{"claude-opus-4.7"})
	p.SetModelList("idle", []string{"claude-opus-4.7"})

	release := p.BeginRequest("busy")
	defer release()

	got := p.GetNextForModel("claude-opus-4.7")
	if got == nil {
		t.Fatalf("expected an account")
	}
	if got.ID != "idle" {
		t.Fatalf("expected idle account, got %q", got.ID)
	}
}

func TestGetNextForModelRoundRobinStrategyPreservesConfiguredOrder(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "busy"}, {ID: "next"}},
		totalAccounts: 2,
		currentIndex:  ^uint64(0),
	}
	p.SetStrategy(StrategyRoundRobin)
	p.SetModelList("busy", []string{"claude-opus-4.7"})
	p.SetModelList("next", []string{"claude-opus-4.7"})

	release := p.BeginRequest("busy")
	defer release()

	got := p.GetNextForModel("claude-opus-4.7")
	if got == nil {
		t.Fatalf("expected an account")
	}
	if got.ID != "busy" {
		t.Fatalf("expected round robin to keep weighted order, got %q", got.ID)
	}
}

func TestBeginNextForModelReservesDistinctIdleAccounts(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "acct-1"}, {ID: "acct-2"}, {ID: "acct-3"}},
		totalAccounts: 3,
		currentIndex:  ^uint64(0),
	}
	p.SetStrategy(StrategyLeastConnections)
	for _, id := range []string{"acct-1", "acct-2", "acct-3"} {
		p.SetModelList(id, []string{"claude-opus-4.7"})
	}

	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < 3; i++ {
		acc, release := p.BeginNextForModelExcept("claude-opus-4.7", nil)
		if acc == nil {
			t.Fatalf("expected account %d", i+1)
		}
		releases = append(releases, release)
	}

	for _, id := range []string{"acct-1", "acct-2", "acct-3"} {
		if got := p.GetRuntimeHealth(id).ActiveConnections; got != 1 {
			t.Fatalf("expected %s to have one reserved connection, got %d", id, got)
		}
	}
}

func TestBeginNextForModelSessionExceptPrefersStickyAccountWhenHealthy(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "acct-1"}, {ID: "acct-2"}},
		totalAccounts: 2,
		currentIndex:  ^uint64(0),
	}
	p.SetStrategy(StrategyRoundRobin)
	p.SetModelList("acct-1", []string{"claude-sonnet-4.5"})
	p.SetModelList("acct-2", []string{"claude-sonnet-4.5"})
	p.RememberSticky("session-1", "claude-sonnet-4.5", "acct-2")

	acc, release := p.BeginNextForModelSessionExcept("claude-sonnet-4.5", "session-1", nil)
	defer release()

	if acc == nil {
		t.Fatalf("expected sticky account")
	}
	if acc.ID != "acct-2" {
		t.Fatalf("expected sticky acct-2, got %q", acc.ID)
	}
	if got := p.GetRuntimeHealth("acct-2").ActiveConnections; got != 1 {
		t.Fatalf("expected sticky account reservation, got %d active connections", got)
	}
}

func TestBeginNextForModelSessionExceptSkipsBreakerOpenAccount(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "acct-1"}, {ID: "acct-2"}},
		totalAccounts: 2,
		currentIndex:  ^uint64(0),
	}
	p.SetStrategy(StrategyRoundRobin)
	p.SetModelList("acct-1", []string{"claude-sonnet-4.5"})
	p.SetModelList("acct-2", []string{"claude-sonnet-4.5"})
	p.RememberSticky("session-1", "claude-sonnet-4.5", "acct-1")
	p.RecordModelFailure("acct-1", "claude-sonnet-4.5", FailureReasonRateLimited, time.Now().Add(time.Minute))

	acc, release := p.BeginNextForModelSessionExcept("claude-sonnet-4.5", "session-1", nil)
	defer release()

	if acc == nil {
		t.Fatalf("expected fallback account")
	}
	if acc.ID != "acct-2" {
		t.Fatalf("expected acct-2 after breaker-open sticky escape, got %q", acc.ID)
	}
}

func TestRuntimeHealthRecordsLatencyAndFailureScore(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}

	release := p.BeginRequest("acct-1")
	p.RecordSuccessWithLatency("acct-1", 1500*time.Millisecond)
	release()
	p.RecordFailure("acct-1", FailureReasonTransientNetwork)

	health := p.GetRuntimeHealth("acct-1")
	if health.ActiveConnections != 0 {
		t.Fatalf("expected active connections to be released, got %d", health.ActiveConnections)
	}
	if health.RecentSuccesses != 1 || health.RecentFailures != 1 {
		t.Fatalf("unexpected success/failure counts: %#v", health)
	}
	if health.AvgLatencyMS != 1500 {
		t.Fatalf("expected avg latency 1500ms, got %d", health.AvgLatencyMS)
	}
	if health.Score >= 100 {
		t.Fatalf("expected mixed health score below 100, got %d", health.Score)
	}
}
