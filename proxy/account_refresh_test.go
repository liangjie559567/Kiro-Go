package proxy

import (
	"errors"
	"io"
	"kiro-go/auth"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	_ "unsafe"
)

//go:linkname authHttpClientStore kiro-go/auth.httpClientStore
var authHttpClientStore atomic.Pointer[http.Client]

func TestSelectAutoRefreshAccountsHonorsScope(t *testing.T) {
	now := time.Unix(2000, 0)
	accounts := []config.Account{
		{ID: "enabled-1", Enabled: true},
		{ID: "disabled-1", Enabled: false},
		{ID: "enabled-2", Enabled: true},
		{ID: "cooling-1", Enabled: true, CooldownUntil: now.Add(time.Hour).Unix(), LastFailureReason: string(pool.FailureReasonTemporaryLimited)},
	}

	enabledOnly, skipped := selectAutoRefreshAccountsForTime(accounts, config.AutoRefreshScopeEnabled, now)
	if len(enabledOnly) != 2 {
		t.Fatalf("expected 2 enabled accounts, got %d", len(enabledOnly))
	}
	if enabledOnly[0].ID != "enabled-1" || enabledOnly[1].ID != "enabled-2" {
		t.Fatalf("unexpected enabled-only order: %#v", enabledOnly)
	}
	if skipped != 1 {
		t.Fatalf("expected one cooling account skipped, got %d", skipped)
	}

	all, skipped := selectAutoRefreshAccountsForTime(accounts, config.AutoRefreshScopeAll, now)
	if len(all) != 3 {
		t.Fatalf("expected all 3 accounts, got %d", len(all))
	}
	if all[0].ID != "enabled-1" || all[1].ID != "disabled-1" || all[2].ID != "enabled-2" {
		t.Fatalf("unexpected all-scope order: %#v", all)
	}
	if skipped != 1 {
		t.Fatalf("expected one cooling account skipped for all scope, got %d", skipped)
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

func TestRunRefreshBatchQuietModeSkipsCooldownAccount(t *testing.T) {
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
	oldNow := backgroundQuietModeNow
	backgroundQuietModeNow = func() time.Time { return now }
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		backgroundQuietModeNow = oldNow
	})

	accounts := []config.Account{
		{ID: "ok", Enabled: true},
		{ID: "cooling", Enabled: true, CooldownUntil: now.Add(time.Hour).Unix()},
	}

	selected, skipped := selectAutoRefreshAccountsForTime(accounts, config.AutoRefreshScopeEnabled, now)
	if skipped != 1 {
		t.Fatalf("expected one quiet-mode selection skip, got %d", skipped)
	}
	if len(selected) != 2 {
		t.Fatalf("expected quiet-mode account to be deferred to batch skip, got %#v", selected)
	}

	var refreshed []string
	result := runRefreshBatch(selected, func(account *config.Account) error {
		refreshed = append(refreshed, account.ID)
		return nil
	})
	result.Skipped = skipped

	if result.Success != 1 || result.Failed != 0 || result.Skipped != 1 || result.QuietSkipped != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(refreshed) != 1 || refreshed[0] != "ok" {
		t.Fatalf("quiet-mode cooldown account should not be refreshed, got calls %#v", refreshed)
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

	h.finishAutoRefresh(refreshBatchResult{Success: 1, Failed: 0, Skipped: 2}, 400, 7200)
	status = h.getAutoRefreshStatus()
	if status.LastSkippedCount != 2 {
		t.Fatalf("expected last skipped count 2, got %d", status.LastSkippedCount)
	}
}

func TestAutoRefreshStatusRecordsQuietModeSkips(t *testing.T) {
	h := &Handler{}

	h.finishAutoRefresh(refreshBatchResult{Success: 1, Failed: 0, Skipped: 2, QuietSkipped: 1}, 400, 7200)

	status := h.getAutoRefreshStatus()
	if status.LastSkippedCount != 2 {
		t.Fatalf("expected last skipped count 2, got %d", status.LastSkippedCount)
	}
	if status.LastQuietSkipped != 1 {
		t.Fatalf("expected last quiet skipped 1, got %d", status.LastQuietSkipped)
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

func TestRefreshAccountDataRefreshesTokenWhenTokenIsStillValid(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	oldExpiresAt := time.Now().Add(time.Hour).Unix()
	account := config.Account{
		ID:           "acct-1",
		Email:        "user@example.com",
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AuthMethod:   "social",
		Region:       "us-east-1",
		Enabled:      true,
		ExpiresAt:    oldExpiresAt,
		ProfileArn:   "arn:aws:codewhisperer:profile/test",
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	var authCalls int32
	authHttpClientStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			atomic.AddInt32(&authCalls, 1)
			if req.URL.Host != "prod.us-east-1.auth.desktop.kiro.dev" || req.URL.Path != "/refreshToken" {
				t.Fatalf("unexpected auth request: %s %s", req.Method, req.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access-token","refreshToken":"new-refresh-token","expiresIn":7200,"profileArn":"arn:aws:codewhisperer:profile/new"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { auth.InitHttpClient("") })

	kiroRestHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Bearer new-access-token" {
				t.Fatalf("expected refreshed access token in usage request, got %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"userInfo":{"email":"user@example.com","userId":"user-1"},
					"subscriptionInfo":{"subscriptionTitle":"KIRO PRO"},
					"usageBreakdownList":[{"currentUsage":1,"usageLimit":100}],
					"nextDateReset":1800000000
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	_, err := refreshAccountData(&account)
	if err != nil {
		t.Fatalf("refresh account data: %v", err)
	}
	if authCalls != 1 {
		t.Fatalf("expected token refresh to run once, got %d", authCalls)
	}
	if account.AccessToken != "new-access-token" {
		t.Fatalf("expected in-memory account access token to refresh, got %q", account.AccessToken)
	}
	if account.ExpiresAt <= oldExpiresAt {
		t.Fatalf("expected in-memory expiresAt to move forward, old=%d new=%d", oldExpiresAt, account.ExpiresAt)
	}

	persisted := config.GetAccounts()[0]
	if persisted.AccessToken != "new-access-token" {
		t.Fatalf("expected persisted access token to refresh, got %q", persisted.AccessToken)
	}
	if persisted.ExpiresAt != account.ExpiresAt {
		t.Fatalf("expected persisted expiresAt %d, got %d", account.ExpiresAt, persisted.ExpiresAt)
	}
}

func TestRefreshAccountDataSkipsStaleTokenWriteWhenRefreshTokenChanged(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	oldExpiresAt := time.Now().Add(time.Hour).Unix()
	account := config.Account{
		ID:           "acct-stale",
		Email:        "stale@example.com",
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		AuthMethod:   "social",
		Region:       "us-east-1",
		Enabled:      true,
		ExpiresAt:    oldExpiresAt,
		ProfileArn:   "arn:aws:codewhisperer:profile/test",
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	authHttpClientStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := config.UpdateAccountToken(account.ID, "user-new-access-token", "user-new-refresh-token", oldExpiresAt+600); err != nil {
				t.Fatalf("simulate user token update: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accessToken":"stale-access-token","refreshToken":"stale-refresh-token","expiresIn":7200,"profileArn":"arn:aws:codewhisperer:profile/stale"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { auth.InitHttpClient("") })

	_, err := refreshAccountData(&account)
	if err == nil {
		t.Fatalf("expected stale refresh to fail")
	}
	if !strings.Contains(err.Error(), "stale refresh result") {
		t.Fatalf("expected stale refresh error, got %v", err)
	}
	persisted := config.GetAccounts()[0]
	if persisted.AccessToken != "user-new-access-token" || persisted.RefreshToken != "user-new-refresh-token" {
		t.Fatalf("expected user-updated token to remain, got %#v", persisted)
	}
}
