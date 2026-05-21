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

func TestAcceptanceEvidenceAPIReturnsSafePhase7Contract(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{
		ID:           "acct-1",
		Email:        "user@example.com",
		Enabled:      true,
		AccessToken:  "secret-access-token",
		RefreshToken: "secret-refresh-token",
		ProfileArn:   "arn:aws:codewhisperer:profile/secret",
		Region:       "us-east-1",
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	p.SetModelList("acct-1", []string{"claude-opus-4.7"})
	h := &Handler{pool: p, requestLogs: newRequestLogStore(5)}
	h.recordRequestLog(RequestLogEntry{
		Timestamp:                  time.Now(),
		RequestID:                  "req-1",
		Endpoint:                   "/v1/messages",
		Model:                      "claude-opus-4.7",
		RequestedModel:             "claude-opus-4-7",
		EffectiveModel:             "claude-opus-4.7",
		AccountID:                  "acct-1",
		Region:                     "us-east-1",
		AdmissionReadinessStatus:   "healthy",
		AdmissionSafeConcurrency:   1,
		AdmissionRetryAfterSeconds: 0,
		AdmissionCircuitState:      "closed",
		AdmissionPressureReason:    "healthy",
		StatusCode:                 http.StatusOK,
		Outcome:                    "success",
		DurationMs:                 42,
		ContentSuccess:             true,
		ContentSuccessEvidence:     "output_tokens",
		UpstreamContentTokens:      3,
		AttemptTrace: []RequestLogAttempt{{
			Attempt:   1,
			AccountID: "acct-1",
			Model:     "claude-opus-4.7",
			Region:    "us-east-1",
			Event:     "selected",
		}},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/acceptance/evidence?model=claude-opus-4-7", nil)
	h.apiGetAcceptanceEvidence(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, secret := range []string{"secret-access-token", "secret-refresh-token", "profile/secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("acceptance evidence leaked secret %q in %s", secret, body)
		}
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["contractVersion"] != "phase7-acceptance.1" {
		t.Fatalf("unexpected contract version: %#v", resp)
	}
	logEvidence := resp["requestLogEvidence"].(map[string]interface{})
	if logEvidence["verdict"] != "latest_real_content_success" {
		t.Fatalf("unexpected request log verdict: %#v", logEvidence)
	}
	coverage := logEvidence["latestRequiredCoverage"].(map[string]interface{})
	for field, ok := range coverage {
		if ok != true {
			t.Fatalf("expected %s coverage true, got %#v", field, coverage)
		}
	}
	if len(resp["sub2apiEvidenceRequired"].([]interface{})) == 0 || len(resp["uatBundleRequired"].([]interface{})) == 0 {
		t.Fatalf("expected evidence contract arrays, got %#v", resp)
	}
}
