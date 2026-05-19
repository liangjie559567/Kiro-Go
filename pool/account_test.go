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
		{name: "suspicious temporary limit", err: errors.New(`HTTP 429 from AmazonQ: {"message":"Due to suspicious activity, we are imposing temporary limits on how frequently your account can send a request to Kiro while we investigate.","reason":null}`), want: FailureReasonTemporaryLimited},
		{name: "model capacity", err: errors.New(`HTTP 429 from Kiro IDE: {"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`), want: FailureReasonModelCapacity},
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

func TestRecordModelFailureModelCapacityUsesThreeSecondBaseCooldown(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}
	p.SetModelList("acct-1", []string{"claude-opus-4.7"})
	start := time.Now()

	p.RecordModelFailure("acct-1", "claude-opus-4.7", FailureReasonModelCapacity, time.Time{})

	state := p.ModelBlockState("claude-opus-4.7", start)
	remaining := time.Until(state.RetryAt)
	if state.LastReason != FailureReasonModelCapacity {
		t.Fatalf("expected model capacity reason, got %q", state.LastReason)
	}
	if remaining < 2*time.Second || remaining > 4*time.Second {
		t.Fatalf("expected model capacity cooldown around 3s, got %s", remaining)
	}
}

func TestRecordFailureUntilSuspiciousTemporaryLimitUsesAdaptiveFloor(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}
	shortRetryAfter := time.Now().Add(5 * time.Second)

	p.RecordFailureUntil("acct-1", FailureReasonTemporaryLimited, shortRetryAfter)

	got := p.GetAllAccounts()[0]
	if got.LastFailureReason != string(FailureReasonTemporaryLimited) {
		t.Fatalf("expected temporary_limited reason, got %q", got.LastFailureReason)
	}
	remaining := got.CooldownUntil - time.Now().Unix()
	if remaining < 2 || remaining > 5 {
		t.Fatalf("expected first single-account suspicious temporary limit cooldown around 3s, got %ds", remaining)
	}

	p.RecordFailureUntil("acct-1", FailureReasonTemporaryLimited, shortRetryAfter)

	got = p.GetAllAccounts()[0]
	remaining = got.CooldownUntil - time.Now().Unix()
	if remaining < 5 || remaining > 8 {
		t.Fatalf("expected second single-account suspicious temporary limit cooldown around 6s, got %ds", remaining)
	}
}

func TestRecordFailureUntilSuspiciousTemporaryLimitKeepsMultiAccountFloor(t *testing.T) {
	p := &AccountPool{
		cooldowns:   make(map[string]time.Time),
		errorCounts: make(map[string]int),
		failures:    make(map[string]FailureReason),
		accounts: []config.Account{
			{ID: "acct-1"},
			{ID: "acct-2"},
		},
		totalAccounts: 2,
	}
	shortRetryAfter := time.Now().Add(5 * time.Second)

	p.RecordFailureUntil("acct-1", FailureReasonTemporaryLimited, shortRetryAfter)

	got := p.GetAllAccounts()[0]
	remaining := got.CooldownUntil - time.Now().Unix()
	if remaining < 55 || remaining > 65 {
		t.Fatalf("expected multi-account suspicious temporary limit cooldown around 60s, got %ds", remaining)
	}
}

func TestTemporaryLimitCoolsSharedProfileRiskGroup(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		runtimeHealth: make(map[string]*runtimeHealthState),
		breakers:      newModelBreakerState(),
		accounts: []config.Account{
			{ID: "acct-1", Enabled: true, ProfileArn: "arn:shared"},
			{ID: "acct-2", Enabled: true, ProfileArn: "arn:shared"},
			{ID: "acct-3", Enabled: true, ProfileArn: "arn:other"},
		},
		totalAccounts: 3,
	}
	for _, id := range []string{"acct-1", "acct-2", "acct-3"} {
		p.SetModelList(id, []string{"claude-opus-4.7"})
	}

	p.RecordFailureUntil("acct-1", FailureReasonTemporaryLimited, time.Now().Add(5*time.Second))

	if !p.IsCoolingDown("acct-2", time.Now()) {
		t.Fatalf("expected shared-profile account to be cooling down")
	}
	if p.IsCoolingDown("acct-3", time.Now()) {
		t.Fatalf("did not expect different-profile account to be cooling down")
	}
	next := p.GetNextForModelExcept("claude-opus-4.7", map[string]bool{"acct-1": true})
	if next == nil || next.ID != "acct-3" {
		t.Fatalf("expected routing to skip shared profile and choose acct-3, got %#v", next)
	}
}

func TestModelBlockStateReportsTemporaryLimitedModelCooldown(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		runtimeHealth: make(map[string]*runtimeHealthState),
		breakers:      newModelBreakerState(),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
	}
	p.SetModelList("acct-1", []string{"claude-opus-4.7"})
	resetAt := time.Now().Add(time.Minute)
	p.RecordFailureUntil("acct-1", FailureReasonTemporaryLimited, resetAt)

	state := p.ModelBlockState("claude-opus-4.7", time.Now())

	if state.AccountsEvaluated != 1 || state.Blocked != 1 {
		t.Fatalf("expected one blocked account, got %#v", state)
	}
	if state.LastReason != FailureReasonTemporaryLimited {
		t.Fatalf("expected temporary_limited state, got %#v", state)
	}
	if state.RetryAt.IsZero() {
		t.Fatalf("expected retry time, got %#v", state)
	}
}

func TestModelBlockStateOnlyTreatsAllEvaluatedAccountsAsBlocked(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		runtimeHealth: make(map[string]*runtimeHealthState),
		breakers:      newModelBreakerState(),
		accounts: []config.Account{
			{ID: "limited"},
			{ID: "healthy"},
		},
		totalAccounts: 2,
	}
	p.SetModelList("limited", []string{"claude-opus-4.7"})
	p.SetModelList("healthy", []string{"claude-opus-4.7"})
	p.RecordFailureUntil("limited", FailureReasonTemporaryLimited, time.Now().Add(time.Minute))

	state := p.ModelBlockState("claude-opus-4.7", time.Now())

	if state.AccountsEvaluated != 2 || state.Blocked != 1 {
		t.Fatalf("expected one blocked account and one available account, got %#v", state)
	}
	if state.AllBlocked {
		t.Fatalf("expected allBlocked=false while at least one account is still unblocked")
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

func TestBeginNextForModelExceptSkipsRecentlyReservedAccount(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		runtimeHealth: make(map[string]*runtimeHealthState),
		breakers:      newModelBreakerState(),
		accounts:      []config.Account{{ID: "acct-1", Enabled: true}, {ID: "acct-2", Enabled: true}},
		totalAccounts: 2,
		currentIndex:  ^uint64(0),
	}
	p.SetModelList("acct-1", []string{"claude-opus-4.7"})
	p.SetModelList("acct-2", []string{"claude-opus-4.7"})

	first, releaseFirst := p.BeginNextForModelExcept("claude-opus-4.7", nil)
	if first == nil {
		t.Fatalf("expected first account")
	}
	defer releaseFirst()

	second, releaseSecond := p.BeginNextForModelExcept("claude-opus-4.7", nil)
	if second == nil {
		t.Fatalf("expected second account")
	}
	defer releaseSecond()
	if second.ID == first.ID {
		t.Fatalf("expected recently reserved account to be skipped, got %s twice", first.ID)
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

func TestGetNextForModelExceptSkipsExpiredOpenBreakerWhenAlternativeAvailable(t *testing.T) {
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
	p.RecordModelFailure("acct-1", "claude-sonnet-4.5", FailureReasonRateLimited, time.Now().Add(-time.Second))

	got := p.GetNextForModelExcept("claude-sonnet-4.5", nil)
	if got == nil {
		t.Fatalf("expected healthy alternative")
	}
	if got.ID != "acct-2" {
		t.Fatalf("expected acct-2 while acct-1 breaker awaits probe lifecycle, got %q", got.ID)
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

func TestBeginNextForModelExceptDoesNotMarkExpiredOpenBreakerHalfOpen(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		failures:      make(map[string]FailureReason),
		modelLists:    make(map[string]map[string]bool),
		accounts:      []config.Account{{ID: "acct-1"}},
		totalAccounts: 1,
		currentIndex:  ^uint64(0),
	}
	p.SetStrategy(StrategyRoundRobin)
	p.SetModelList("acct-1", []string{"claude-sonnet-4.5"})
	p.RecordModelFailure("acct-1", "claude-sonnet-4.5", FailureReasonRateLimited, time.Now().Add(-time.Second))

	acc, release := p.BeginNextForModelExcept("claude-sonnet-4.5", nil)
	release()
	if acc != nil {
		t.Fatalf("expected generic begin to skip expired-open breaker account, got %q", acc.ID)
	}

	p.mu.RLock()
	entry := p.breakers.entries[breakerKey("acct-1", "claude-sonnet-4.5")]
	p.mu.RUnlock()
	if entry == nil {
		t.Fatalf("expected breaker entry to remain for Claude probe lifecycle")
	}
	if entry.Status != breakerOpen || entry.Probing {
		t.Fatalf("expected generic begin not to mark half-open probe, got %#v", entry)
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

func TestBeginNextForModelSessionExceptKeepsAgentStickyKeysIndependent(t *testing.T) {
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
	p.RememberSticky("session-1/agent-a", "claude-sonnet-4.5", "acct-1")
	p.RememberSticky("session-1/agent-b", "claude-sonnet-4.5", "acct-2")

	accA, releaseA := p.BeginNextForModelSessionExcept("claude-sonnet-4.5", "session-1/agent-a", nil)
	defer releaseA()
	accB, releaseB := p.BeginNextForModelSessionExcept("claude-sonnet-4.5", "session-1/agent-b", nil)
	defer releaseB()

	if accA == nil || accA.ID != "acct-1" {
		t.Fatalf("expected agent-a to stay on acct-1, got %#v", accA)
	}
	if accB == nil || accB.ID != "acct-2" {
		t.Fatalf("expected agent-b to stay on acct-2, got %#v", accB)
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
