package proxy

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

func TestValidateCredentialSourceDryRunDoesNotMutateAccounts(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "existing", Email: "old@example.com", RefreshToken: "old-token", Region: "us-east-1"}); err != nil {
		t.Fatalf("add account: %v", err)
	}

	req := credentialValidationRequest{
		SourceType: "kiro_account_manager_json",
		Data:       json.RawMessage(`{"accounts":[{"email":"new@example.com","refreshToken":"new-token","region":"us-east-1"},{"email":"bad@example.com"}]}`),
	}
	resp := validateCredentialSource(req)
	accounts := resp["accounts"].([]credentialValidationAccount)
	if len(accounts) != 2 {
		t.Fatalf("expected two validation rows, got %#v", accounts)
	}
	if accounts[0].Status != "valid" || accounts[0].Action != "create" {
		t.Fatalf("expected valid create row, got %#v", accounts[0])
	}
	if accounts[1].Status != "invalid" || !strings.Contains(accounts[1].Message, "refreshToken") {
		t.Fatalf("expected invalid refreshToken row, got %#v", accounts[1])
	}
	if got := config.GetAccounts(); len(got) != 1 || got[0].ID != "existing" {
		t.Fatalf("dry-run validation must not mutate accounts, got %#v", got)
	}
}

func TestAccountDiagnosticsReportsActionableBlockedState(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:                "acct-1",
		Email:             "user@example.com",
		Enabled:           true,
		AccessToken:       "token",
		RefreshToken:      "refresh",
		Region:            "us-east-1",
		ProfileArn:        "arn:aws:codewhisperer:profile/test",
		LastFailureReason: string(pool.FailureReasonTemporaryLimited),
		CooldownUntil:     time.Now().Add(time.Minute).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/accounts/acct-1/diagnostics", nil)
	h.apiGetAccountDiagnostics(w, req, "acct-1")

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if resp["status"] != "blocked" || resp["reason"] != string(pool.FailureReasonTemporaryLimited) {
		t.Fatalf("expected temporary-limit blocked diagnostics, got %#v", resp)
	}
	checks := resp["checks"].(map[string]interface{})
	if checks["refreshViable"] != true || checks["profileArnPresent"] != true || checks["coolingDown"] != true {
		t.Fatalf("expected actionable diagnostic checks, got %#v", checks)
	}
}

func TestSchedulerPreviewAndFleetReadinessAreReadOnly(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	accounts := []config.Account{
		{ID: "healthy", Email: "healthy@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "disabled", Email: "disabled@example.com", Enabled: false, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "cooling", Email: "cooling@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1", LastFailureReason: string(pool.FailureReasonRateLimited), CooldownUntil: time.Now().Add(time.Minute).Unix()},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.SetModelList("healthy", []string{"claude-opus-4.7"})
	p.SetModelList("disabled", []string{"claude-opus-4.7"})
	p.SetModelList("cooling", []string{"claude-opus-4.7"})
	h := &Handler{pool: p}

	before := config.GetAccounts()
	previewW := httptest.NewRecorder()
	previewReq := httptest.NewRequest(http.MethodGet, "/admin/api/scheduler/preview?model=claude-opus-4.7", nil)
	h.apiGetSchedulerPreview(previewW, previewReq)
	if previewW.Code != http.StatusOK {
		t.Fatalf("preview status %d body=%s", previewW.Code, previewW.Body.String())
	}
	var preview map[string]interface{}
	if err := json.Unmarshal(previewW.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview["readOnly"] != true {
		t.Fatalf("expected readOnly preview, got %#v", preview)
	}
	preferred := preview["preferred"].([]interface{})
	if len(preferred) != 1 || preferred[0].(map[string]interface{})["id"] != "healthy" {
		t.Fatalf("expected healthy as only preferred account, got %#v", preferred)
	}

	fleetW := httptest.NewRecorder()
	fleetReq := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4.7", nil)
	h.apiGetFleetReadiness(fleetW, fleetReq)
	var fleet map[string]interface{}
	if err := json.Unmarshal(fleetW.Body.Bytes(), &fleet); err != nil {
		t.Fatalf("decode fleet: %v", err)
	}
	summary := fleet["summary"].(map[string]interface{})
	if summary["total"] != float64(3) || summary["eligible"] != float64(1) || summary["disabled"] != float64(1) || summary["coolingDown"] != float64(1) {
		t.Fatalf("unexpected fleet summary: %#v", summary)
	}
	after := config.GetAccounts()
	if before[0].RequestCount != after[0].RequestCount || before[0].CooldownUntil != after[0].CooldownUntil {
		t.Fatalf("preview/readiness should not mutate accounts, before=%#v after=%#v", before, after)
	}
}

func TestFleetReadinessIncludesOpusGovernorContract(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.UpdateSettings("admin-key", true, "secret"); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	oldGate := modelAdmissionGate
	now := time.Unix(2000, 0)
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(30*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(30*time.Second))
	modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, now.Add(30*time.Second))
	t.Cleanup(func() { modelAdmissionGate = oldGate })

	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{
		pool:               p,
		autoRefreshUpdated: make(chan struct{}, 1),
		healthCheckUpdated: make(chan struct{}, 1),
		requestLogs:        newRequestLogStore(defaultRequestLogCapacity),
	}

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
		"model",
		"status",
		"circuitState",
		"retryAfterSeconds",
		"safeConcurrency",
		"currentInFlight",
		"enabledAccounts",
		"modelListedAccounts",
		"locallySchedulableAccounts",
		"coolingDownAccounts",
		"temporaryLimitedAccounts",
		"quotaBlockedAccounts",
		"authBlockedAccounts",
		"admissionPressureScore",
		"lastPressureReason",
		"lastPressureAt",
		"notes",
	}
	for _, field := range wantFields {
		if _, ok := body[field]; !ok {
			t.Fatalf("%s missing: %#v", field, body)
		}
	}
	if body["model"] != "claude-opus-4-7" {
		t.Fatalf("model = %#v", body["model"])
	}
	if body["status"] != "blocked" {
		t.Fatalf("status = %#v, want blocked; body=%#v", body["status"], body)
	}
	if body["circuitState"] != "open" {
		t.Fatalf("circuitState = %#v, want open; body=%#v", body["circuitState"], body)
	}
	if got, ok := body["retryAfterSeconds"].(float64); !ok || got <= 0 {
		t.Fatalf("retryAfterSeconds = %#v, want >0", body["retryAfterSeconds"])
	}
	if _, ok := body["safeConcurrency"].(float64); !ok {
		t.Fatalf("safeConcurrency missing: %#v", body)
	}
	if _, ok := body["locallySchedulableAccounts"].(float64); !ok {
		t.Fatalf("locallySchedulableAccounts missing: %#v", body)
	}

	routeReq := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4-7", nil)
	routeReq.Header.Set("X-Admin-Password", "secret")
	routeW := httptest.NewRecorder()
	h.handleAdminAPI(routeW, routeReq)
	if routeW.Code != http.StatusOK {
		t.Fatalf("route status = %d body=%s", routeW.Code, routeW.Body.String())
	}
	var routed map[string]interface{}
	if err := json.Unmarshal(routeW.Body.Bytes(), &routed); err != nil {
		t.Fatalf("decode routed readiness: %v", err)
	}
	if routed["status"] != "blocked" || routed["circuitState"] != "open" {
		t.Fatalf("unexpected routed readiness: %#v", routed)
	}
}

func TestFleetReadinessDoesNotTreatNearExpiryAccountAsSchedulable(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:           "near-expiry",
		Email:        "near@example.com",
		Enabled:      true,
		AccessToken:  "token",
		RefreshToken: "refresh",
		ProfileArn:   "arn:profile",
		Region:       "us-east-1",
		ExpiresAt:    time.Now().Add(time.Minute).Unix(),
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.SetModelList("near-expiry", []string{"claude-opus-4.7"})
	h := &Handler{pool: p}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4-7", nil)
	w := httptest.NewRecorder()
	h.apiGetFleetReadiness(w, req)

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if got := body["locallySchedulableAccounts"]; got != float64(0) {
		t.Fatalf("locallySchedulableAccounts = %#v, want 0; body=%#v", got, body)
	}
	if body["status"] != "blocked" {
		t.Fatalf("status = %#v, want blocked; body=%#v", body["status"], body)
	}
}

func TestFleetReadinessAccountsForModelBreakerBlocks(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:           "breaker-blocked",
		Email:        "blocked@example.com",
		Enabled:      true,
		AccessToken:  "token",
		RefreshToken: "refresh",
		ProfileArn:   "arn:profile",
		Region:       "us-east-1",
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.SetModelList("breaker-blocked", []string{"claude-opus-4.7"})
	p.RecordModelFailure("breaker-blocked", "claude-opus-4.7", pool.FailureReasonModelCapacity, time.Now().Add(time.Minute))
	h := &Handler{pool: p}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4-7", nil)
	w := httptest.NewRecorder()
	h.apiGetFleetReadiness(w, req)

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if got := body["locallySchedulableAccounts"]; got != float64(0) {
		t.Fatalf("locallySchedulableAccounts = %#v, want 0; body=%#v", got, body)
	}
	if got := body["safeConcurrency"]; got != float64(0) {
		t.Fatalf("safeConcurrency = %#v, want 0; body=%#v", got, body)
	}
	if body["status"] != "blocked" {
		t.Fatalf("status = %#v, want blocked; body=%#v", body["status"], body)
	}
}
