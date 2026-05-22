package proxy

import (
	"encoding/json"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
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

func TestSchedulerPreviewAndFleetReadinessShareEligibilityReasonCodes(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	now := time.Now()
	accounts := []config.Account{
		{ID: "disabled", Email: "disabled@example.com", Enabled: false, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "cooldown", Email: "cooldown@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1", LastFailureReason: string(pool.FailureReasonRateLimited), CooldownUntil: now.Add(time.Minute).Unix()},
		{ID: "breaker", Email: "breaker@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "token", Email: "token@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1", ExpiresAt: now.Add(time.Minute).Unix()},
		{ID: "usage", Email: "usage@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1", UsageCurrent: 10, UsageLimit: 10},
		{ID: "not-listed", Email: "not-listed@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "eligible", Email: "eligible@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	for _, account := range accounts {
		p.SetModelList(account.ID, []string{"claude-opus-4.7"})
	}
	p.SetModelList("not-listed", []string{"claude-sonnet-4.5"})
	p.RecordModelFailure("breaker", "claude-opus-4.7", pool.FailureReasonModelCapacity, now.Add(time.Minute))
	p.RecordModelContentSuccess("eligible", "claude-opus-4.7", now.Add(-time.Minute))
	h := &Handler{pool: p, requestLogs: newRequestLogStore(10)}

	previewW := httptest.NewRecorder()
	h.apiGetSchedulerPreview(previewW, httptest.NewRequest(http.MethodGet, "/admin/api/scheduler/preview?model=claude-opus-4.7", nil))
	var preview map[string]interface{}
	if err := json.Unmarshal(previewW.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}

	fleetW := httptest.NewRecorder()
	h.apiGetFleetReadiness(fleetW, httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4.7", nil))
	var fleet map[string]interface{}
	if err := json.Unmarshal(fleetW.Body.Bytes(), &fleet); err != nil {
		t.Fatalf("decode fleet: %v", err)
	}

	previewRows := rowsByID(preview["accounts"].([]interface{}))
	fleetRows := rowsByID(fleet["accounts"].([]interface{}))
	wantCodes := map[string][]string{
		"disabled":   {"disabled"},
		"cooldown":   {"cooling_down"},
		"breaker":    {"model_breaker_open"},
		"token":      {"token_expired"},
		"usage":      {"usage_limit_reached"},
		"not-listed": {"model_not_listed"},
		"eligible":   {"eligible"},
	}
	for id, want := range wantCodes {
		previewRow := previewRows[id]
		fleetRow := fleetRows[id]
		if previewRow == nil || fleetRow == nil {
			t.Fatalf("missing row %s preview=%#v fleet=%#v", id, previewRows, fleetRows)
		}
		if previewRow["eligible"] != fleetRow["eligible"] {
			t.Fatalf("%s eligible mismatch preview=%#v fleet=%#v", id, previewRow["eligible"], fleetRow["eligible"])
		}
		if !reflect.DeepEqual(stringSliceFromInterface(previewRow["reasonCodes"]), stringSliceFromInterface(fleetRow["reasonCodes"])) {
			t.Fatalf("%s reasonCodes mismatch preview=%#v fleet=%#v", id, previewRow["reasonCodes"], fleetRow["reasonCodes"])
		}
		if got := stringSliceFromInterface(previewRow["reasonCodes"]); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s reasonCodes = %#v, want %#v", id, got, want)
		}
	}
	if fleetRows["eligible"]["latestContentSuccessModel"] != "claude-opus-4.7" {
		t.Fatalf("expected account content success evidence on eligible row: %#v", fleetRows["eligible"])
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
	if body["lastPressureReason"] == "" {
		t.Fatalf("lastPressureReason missing: %#v", body)
	}
	if body["contractVersion"] != "opus-4.7-readiness.1" {
		t.Fatalf("contractVersion = %#v", body["contractVersion"])
	}
	if body["recommendedAction"] != "retry_after_or_wait_for_recovery" {
		t.Fatalf("recommendedAction = %#v; body=%#v", body["recommendedAction"], body)
	}
	if _, ok := body["safeConcurrency"].(float64); !ok {
		t.Fatalf("safeConcurrency missing: %#v", body)
	}
	if got := body["safeConcurrency"]; got != float64(0) {
		t.Fatalf("safeConcurrency = %#v, want blocked zero; body=%#v", got, body)
	}
	if _, ok := body["locallySchedulableAccounts"].(float64); !ok {
		t.Fatalf("locallySchedulableAccounts missing: %#v", body)
	}
	pressure := h.admissionPressureForModel("claude-opus-4-7")
	if pressure["circuitState"] != "open" {
		t.Fatalf("expected model readiness admission pressure circuit state, got %#v", pressure)
	}
	if retryAfter, ok := pressure["retryAfterSeconds"].(int); !ok || retryAfter <= 0 {
		t.Fatalf("expected model readiness retry-after, got %#v", pressure)
	}
	if pressure["lastPressureReason"] == "" {
		t.Fatalf("expected model readiness pressure reason, got %#v", pressure)
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

func TestFleetReadinessSafeConcurrencyFormulaForHealthyAndDegraded(t *testing.T) {
	tests := []struct {
		name       string
		pressure   bool
		wantStatus string
	}{
		{name: "healthy", wantStatus: "healthy"},
		{name: "degraded", pressure: true, wantStatus: "degraded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
				t.Fatalf("init config: %v", err)
			}
			oldGate := modelAdmissionGate
			now := time.Unix(2000, 0)
			modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
				Models: map[string]config.ModelAdmissionRule{
					"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 8},
				},
			})
			modelAdmissionGate.now = func() time.Time { return now }
			if tt.pressure {
				modelAdmissionGate.recordPressureUntil("claude-opus-4.7", http.StatusTooManyRequests, time.Second, time.Time{})
			}
			t.Cleanup(func() { modelAdmissionGate = oldGate })

			p := &pool.AccountPool{}
			h := &Handler{pool: p, requestLogs: newRequestLogStore(10)}
			for i := 1; i <= 3; i++ {
				id := "acct-" + string(rune('0'+i))
				if err := config.AddAccount(config.Account{ID: id, Email: id + "@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"}); err != nil {
					t.Fatalf("add account: %v", err)
				}
			}
			p.Reload()
			for _, account := range config.GetAccounts() {
				p.SetModelList(account.ID, []string{"claude-opus-4.7"})
			}

			w := httptest.NewRecorder()
			h.apiGetFleetReadiness(w, httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4.7", nil))
			var body map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode readiness: %v", err)
			}
			if body["status"] != tt.wantStatus {
				t.Fatalf("status = %#v, want %s; body=%#v", body["status"], tt.wantStatus, body)
			}
			local := int(body["locallySchedulableAccounts"].(float64))
			admission := int(body["admissionEffectiveConcurrency"].(float64))
			wantSafe := local
			if admission < wantSafe {
				wantSafe = admission
			}
			if got := int(body["safeConcurrency"].(float64)); got != wantSafe {
				t.Fatalf("safeConcurrency = %d, want min(%d,%d); body=%#v", got, local, admission, body)
			}
		})
	}
}

func TestFleetReadinessConfiguredLimitIsHealthyWithoutPressure(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	oldGate := modelAdmissionGate
	now := time.Now()
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 2, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	t.Cleanup(func() { modelAdmissionGate = oldGate })

	p := &pool.AccountPool{}
	h := &Handler{pool: p, requestLogs: newRequestLogStore(10)}
	for i := 1; i <= 3; i++ {
		id := "acct-" + string(rune('0'+i))
		if err := config.AddAccount(config.Account{ID: id, Email: id + "@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"}); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p.Reload()
	for _, account := range config.GetAccounts() {
		p.SetModelList(account.ID, []string{"claude-opus-4.7"})
	}

	w := httptest.NewRecorder()
	h.apiGetFleetReadiness(w, httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4.7", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if body["status"] != "healthy" {
		t.Fatalf("status = %#v, want healthy; body=%#v", body["status"], body)
	}
	if got := body["safeConcurrency"]; got != float64(2) {
		t.Fatalf("safeConcurrency = %#v, want configured safe limit 2; body=%#v", got, body)
	}
	if got := stringSliceFromInterface(body["reasonCodes"]); !reflect.DeepEqual(got, []string{"healthy"}) {
		t.Fatalf("reasonCodes = %#v, want healthy; body=%#v", got, body)
	}
}

func TestFleetReadinessDoesNotSurfaceCoolingDownWhenCapacityRemainsHealthy(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	oldGate := modelAdmissionGate
	now := time.Now()
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 2, MaxWaiting: 8},
		},
	})
	modelAdmissionGate.now = func() time.Time { return now }
	t.Cleanup(func() { modelAdmissionGate = oldGate })

	accounts := []config.Account{
		{ID: "cooling", Email: "cooling@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1", LastFailureReason: string(pool.FailureReasonTemporaryLimited), CooldownUntil: now.Add(time.Hour).Unix()},
		{ID: "healthy-1", Email: "healthy1@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "healthy-2", Email: "healthy2@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
		{ID: "healthy-3", Email: "healthy3@example.com", Enabled: true, AccessToken: "token", RefreshToken: "refresh", ProfileArn: "arn:profile", Region: "us-east-1"},
	}
	for _, account := range accounts {
		if err := config.AddAccount(account); err != nil {
			t.Fatalf("add account: %v", err)
		}
	}
	p := &pool.AccountPool{}
	p.Reload()
	for _, account := range accounts {
		p.SetModelList(account.ID, []string{"claude-opus-4.7"})
	}
	h := &Handler{pool: p, requestLogs: newRequestLogStore(10)}

	w := httptest.NewRecorder()
	h.apiGetFleetReadiness(w, httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4.7", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if body["status"] != "healthy" {
		t.Fatalf("status = %#v, want healthy; body=%#v", body["status"], body)
	}
	if got := body["coolingDownAccounts"]; got != float64(1) {
		t.Fatalf("coolingDownAccounts = %#v, want 1; body=%#v", got, body)
	}
	if got := body["retryAfterSeconds"]; got != float64(0) {
		t.Fatalf("retryAfterSeconds = %#v, want 0 while healthy capacity remains; body=%#v", got, body)
	}
	if got := stringSliceFromInterface(body["reasonCodes"]); !reflect.DeepEqual(got, []string{"healthy"}) {
		t.Fatalf("reasonCodes = %#v, want healthy; body=%#v", got, body)
	}
}

func TestFleetReadinessReportsRecentContentFailures(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	t.Cleanup(h.Close)
	h.ensureRequestLogStore().Add(RequestLogEntry{
		Timestamp:                  time.Now(),
		Model:                      "claude-opus-4.7",
		StatusCode:                 http.StatusOK,
		Outcome:                    "success",
		StableDownstreamFallback:   true,
		StableFallbackReason:       "admission_pressure",
		ContentFailureReason:       "admission_pressure",
		SuppressedDownstreamStatus: http.StatusServiceUnavailable,
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/fleet/readiness?model=claude-opus-4-7", nil)
	w := httptest.NewRecorder()

	h.apiGetFleetReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["recentContentRequests"] != float64(1) {
		t.Fatalf("recentContentRequests = %#v, want 1; body=%#v", body["recentContentRequests"], body)
	}
	if body["contentSuccessRate"] != float64(0) {
		t.Fatalf("contentSuccessRate = %#v, want 0; body=%#v", body["contentSuccessRate"], body)
	}
	if body["recentStableFallbacks"] != float64(1) {
		t.Fatalf("recentStableFallbacks = %#v, want 1; body=%#v", body["recentStableFallbacks"], body)
	}
	if body["recentEmptyCompletions"] != float64(1) {
		t.Fatalf("recentEmptyCompletions = %#v, want 1; body=%#v", body["recentEmptyCompletions"], body)
	}
	if body["recommendedQueueWaitSeconds"] != float64(config.Get().ContentContinuity.MaxQueueWaitSeconds) {
		t.Fatalf("recommendedQueueWaitSeconds = %#v, body=%#v", body["recommendedQueueWaitSeconds"], body)
	}
	h.ensureRequestLogStore().Add(RequestLogEntry{
		Timestamp:             time.Now(),
		Model:                 "claude-opus-4.7",
		StatusCode:            http.StatusOK,
		Outcome:               "success",
		ContentSuccess:        true,
		UpstreamContentTokens: 3,
	})
	w = httptest.NewRecorder()
	h.apiGetFleetReadiness(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode second body: %v", err)
	}
	if body["recentContentRequests"] != float64(2) || body["recentStableFallbacks"] != float64(1) || body["recentEmptyCompletions"] != float64(1) || body["contentSuccessRate"] != 0.5 {
		t.Fatalf("expected separate success/fallback/empty counters, got %#v", body)
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

func rowsByID(rows []interface{}) map[string]map[string]interface{} {
	out := make(map[string]map[string]interface{}, len(rows))
	for _, raw := range rows {
		row, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		if id != "" {
			out[id] = row
		}
	}
	return out
}

func stringSliceFromInterface(raw interface{}) []string {
	values, _ := raw.([]interface{})
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.(string))
	}
	return out
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
	if got, ok := body["retryAfterSeconds"].(float64); !ok || got <= 0 {
		t.Fatalf("retryAfterSeconds = %#v, want model breaker recovery hint; body=%#v", body["retryAfterSeconds"], body)
	}
}

func TestWebSearchDiagnosticsReturnsToolEvidenceFields(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                     time.Date(2026, 5, 22, 5, 29, 0, 0, time.UTC),
		RequestID:                     "req-websearch",
		AccountID:                     "acct-1",
		WebSearchQuery:                "node test",
		WebSearchMCPStatus:            "ok",
		WebSearchResultCount:          3,
		WebSearchInjectedPayloadBytes: 512,
		WebSearchLatencyMs:            42,
		PayloadKeptTools:              []string{"mcp__browser__search", "bash"},
		PayloadMaterializedToolRefs:   []string{"mcp__fs__read"},
		PayloadTrimmedTools:           []string{"mcp__old__tool"},
		PayloadDeferredTools:          []string{"mcp__late__tool"},
		PayloadCurrentTools:           4,
		PayloadCurrentToolSchemaBytes: 2048,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/websearch/diagnostics?query=node+test", nil)
	w := httptest.NewRecorder()
	h.apiGetWebSearchDiagnostics(w, req)

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if body["status"] != "ready" || body["supported"] != true {
		t.Fatalf("expected ready supported diagnostics, got %#v", body)
	}
	recent := body["recent"].([]interface{})
	if len(recent) != 1 {
		t.Fatalf("expected one recent diagnostic row, got %#v", recent)
	}
	row := recent[0].(map[string]interface{})
	for key, want := range map[string]interface{}{
		"requestId":                     "req-websearch",
		"accountId":                     "acct-1",
		"query":                         "node test",
		"mcpStatus":                     "ok",
		"resultCount":                   float64(3),
		"latencyMs":                     float64(42),
		"injectedPayloadBytes":          float64(512),
		"payloadCurrentTools":           float64(4),
		"payloadCurrentToolSchemaBytes": float64(2048),
		"mcpToolPresent":                true,
		"webSearchToolPresent":          false,
		"toolEvidence":                  "mcp,websearch_run",
	} {
		if got := row[key]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %#v, want %#v; row=%#v", key, got, want, row)
		}
	}
	if got := stringSliceFromInterface(row["payloadKeptTools"]); !reflect.DeepEqual(got, []string{"mcp__browser__search", "bash"}) {
		t.Fatalf("payloadKeptTools = %#v", got)
	}
	if got := stringSliceFromInterface(row["payloadMaterializedToolRefs"]); !reflect.DeepEqual(got, []string{"mcp__fs__read"}) {
		t.Fatalf("payloadMaterializedToolRefs = %#v", got)
	}
	if got := stringSliceFromInterface(row["payloadTrimmedTools"]); !reflect.DeepEqual(got, []string{"mcp__old__tool"}) {
		t.Fatalf("payloadTrimmedTools = %#v", got)
	}
	if got := stringSliceFromInterface(row["payloadDeferredTools"]); !reflect.DeepEqual(got, []string{"mcp__late__tool"}) {
		t.Fatalf("payloadDeferredTools = %#v", got)
	}
}

func TestWebSearchDiagnosticsIncludesClaudeCodeWebSearchToolEvidence(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:           time.Date(2026, 5, 22, 5, 42, 0, 0, time.UTC),
		RequestID:           "req-tool-evidence",
		PayloadKeptTools:    []string{"agent", "bash", "webSearch", "read"},
		PayloadCurrentTools: 4,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/websearch/diagnostics", nil)
	w := httptest.NewRecorder()
	h.apiGetWebSearchDiagnostics(w, req)

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	recent := body["recent"].([]interface{})
	if len(recent) != 1 {
		t.Fatalf("expected webSearch tool evidence row, got %#v", recent)
	}
	row := recent[0].(map[string]interface{})
	if got := row["webSearchToolPresent"]; got != true {
		t.Fatalf("webSearchToolPresent = %#v, want true; row=%#v", got, row)
	}
	if got := row["toolEvidence"]; got != "websearch" {
		t.Fatalf("toolEvidence = %#v, want websearch; row=%#v", got, row)
	}
	if got := row["query"]; got != "" {
		t.Fatalf("query = %#v, want empty tool capability evidence; row=%#v", got, row)
	}
}
