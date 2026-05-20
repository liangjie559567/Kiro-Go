package proxy

import (
	"encoding/json"
	"fmt"
	"kiro-go/config"
	"kiro-go/pool"
	"net/http"
	"sort"
	"strings"
	"time"
)

type credentialValidationRequest struct {
	SourceType string          `json:"sourceType"`
	DryRun     *bool           `json:"dryRun,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type credentialValidationAccount struct {
	Index           int      `json:"index"`
	Status          string   `json:"status"`
	Action          string   `json:"action,omitempty"`
	Email           string   `json:"email,omitempty"`
	Region          string   `json:"region,omitempty"`
	AuthMethod      string   `json:"authMethod,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	HasAccessToken  bool     `json:"hasAccessToken"`
	HasRefreshToken bool     `json:"hasRefreshToken"`
	MissingFields   []string `json:"missingFields,omitempty"`
	Message         string   `json:"message"`
}

func (h *Handler) apiValidateCredentials(w http.ResponseWriter, r *http.Request) {
	var req credentialValidationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	resp := validateCredentialSource(req)
	json.NewEncoder(w).Encode(resp)
}

func validateCredentialSource(req credentialValidationRequest) map[string]interface{} {
	sourceType := strings.ToLower(strings.TrimSpace(req.SourceType))
	if sourceType == "" {
		sourceType = "kiro_account_manager_json"
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	resp := map[string]interface{}{
		"sourceType": sourceType,
		"dryRun":     dryRun,
		"mutated":    false,
		"accounts":   []credentialValidationAccount{},
		"summary": map[string]int{
			"valid":       0,
			"invalid":     0,
			"unsupported": 0,
		},
	}
	switch sourceType {
	case "kiro_account_manager_json", "kiro-account-manager", "credentials_json":
		accounts, err := parseCredentialAccounts(req.Data)
		if err != nil {
			resp["accounts"] = []credentialValidationAccount{{
				Index:   0,
				Status:  "invalid",
				Message: "credential JSON parse failed: " + err.Error(),
			}}
			resp["summary"] = map[string]int{"valid": 0, "invalid": 1, "unsupported": 0}
			return resp
		}
		existing := existingAccountIdentitySet()
		out := make([]credentialValidationAccount, 0, len(accounts))
		summary := map[string]int{"valid": 0, "invalid": 0, "unsupported": 0}
		for i, account := range accounts {
			row := validateCredentialAccount(i, account, existing)
			out = append(out, row)
			summary[row.Status]++
		}
		resp["accounts"] = out
		resp["summary"] = summary
		return resp
	case "kiro_cli", "amazon_q_cli", "amazonq_cli":
		resp["accounts"] = []credentialValidationAccount{{
			Index:   0,
			Status:  "unsupported",
			Message: "local CLI credential discovery is intentionally unsupported in this API unless credentials are supplied as JSON fixtures",
		}}
		resp["summary"] = map[string]int{"valid": 0, "invalid": 0, "unsupported": 1}
		return resp
	default:
		resp["accounts"] = []credentialValidationAccount{{
			Index:   0,
			Status:  "unsupported",
			Message: "unsupported credential source type",
		}}
		resp["summary"] = map[string]int{"valid": 0, "invalid": 0, "unsupported": 1}
		return resp
	}
}

func parseCredentialAccounts(raw json.RawMessage) ([]config.Account, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("data is required")
	}
	var wrapped struct {
		Accounts []config.Account `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Accounts) > 0 {
		return wrapped.Accounts, nil
	}
	var accounts []config.Account
	if err := json.Unmarshal(raw, &accounts); err == nil {
		return accounts, nil
	}
	var account config.Account
	if err := json.Unmarshal(raw, &account); err != nil {
		return nil, err
	}
	return []config.Account{account}, nil
}

func existingAccountIdentitySet() map[string]bool {
	out := make(map[string]bool)
	for _, account := range config.GetAccounts() {
		for _, key := range accountIdentityKeys(account) {
			out[key] = true
		}
	}
	return out
}

func validateCredentialAccount(index int, account config.Account, existing map[string]bool) credentialValidationAccount {
	missing := make([]string, 0, 2)
	if strings.TrimSpace(account.RefreshToken) == "" {
		missing = append(missing, "refreshToken")
	}
	if strings.TrimSpace(account.Region) == "" {
		account.Region = "us-east-1"
	}
	status := "valid"
	message := "credential fixture is importable"
	if len(missing) > 0 {
		status = "invalid"
		message = "missing required fields: " + strings.Join(missing, ", ")
	}
	action := "create"
	for _, key := range accountIdentityKeys(account) {
		if existing[key] {
			action = "update"
			break
		}
	}
	return credentialValidationAccount{
		Index:           index,
		Status:          status,
		Action:          action,
		Email:           account.Email,
		Region:          account.Region,
		AuthMethod:      normalizeDiagnosticAuthMethod(account.AuthMethod, account.ClientID),
		Provider:        account.Provider,
		HasAccessToken:  strings.TrimSpace(account.AccessToken) != "",
		HasRefreshToken: strings.TrimSpace(account.RefreshToken) != "",
		MissingFields:   missing,
		Message:         message,
	}
}

func accountIdentityKeys(account config.Account) []string {
	keys := make([]string, 0, 3)
	if account.ID != "" {
		keys = append(keys, "id:"+account.ID)
	}
	if account.Email != "" {
		keys = append(keys, "email:"+strings.ToLower(account.Email))
	}
	if account.RefreshToken != "" {
		keys = append(keys, "refresh:"+account.RefreshToken)
	}
	return keys
}

func (h *Handler) apiGetAccountDiagnostics(w http.ResponseWriter, r *http.Request, id string) {
	account, ok := findConfigAccount(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}
	json.NewEncoder(w).Encode(h.accountDiagnostics(account, "claude-sonnet-4.5"))
}

func (h *Handler) accountDiagnostics(account config.Account, model string) map[string]interface{} {
	now := time.Now()
	nowUnix := now.Unix()
	status := "ready"
	reason := "ready"
	message := "Account is enabled and locally ready for generation traffic."
	if !account.Enabled {
		status, reason, message = "blocked", "disabled", "Enable the account before routing generation traffic."
	} else if strings.TrimSpace(account.AccessToken) == "" && strings.TrimSpace(account.RefreshToken) == "" {
		status, reason, message = "blocked", "missing_token", "Add an access token or refresh token."
	} else if account.ExpiresAt > 0 && nowUnix >= account.ExpiresAt-tokenRefreshSkewSeconds && strings.TrimSpace(account.RefreshToken) == "" {
		status, reason, message = "blocked", "token_expired", "Refresh token is missing and the access token is expired or near expiry."
	} else if strings.TrimSpace(account.ProfileArn) == "" {
		status, reason, message = "attention", "missing_profile_arn", "Refresh or test the account so Kiro profile ARN can be discovered."
	} else if account.CooldownUntil > nowUnix {
		status, reason, message = "blocked", nonEmpty(account.LastFailureReason, "cooling_down"), "Account is cooling down; wait before testing or routing traffic."
	} else if readinessAccountUsageBlocked(account) {
		status, reason, message = "blocked", "usage_limit_reached", "Usage limit is reached; enable overage or wait for reset."
	}
	models := []string{}
	modelListed := true
	if h != nil && h.pool != nil {
		models = h.pool.GetModelList(account.ID)
		if len(models) > 0 {
			modelListed = stringSliceEqualFoldContains(models, model)
			if !modelListed && status == "ready" {
				status, reason, message = "attention", "model_not_listed", "Refresh model cache or choose a model listed by this account."
			}
		}
	}
	cooldownRemaining := int64(0)
	if account.CooldownUntil > nowUnix {
		cooldownRemaining = account.CooldownUntil - nowUnix
	}
	return map[string]interface{}{
		"id":      account.ID,
		"email":   maskReadinessEmail(account.Email),
		"status":  status,
		"reason":  reason,
		"message": message,
		"checks": map[string]interface{}{
			"enabled":           account.Enabled,
			"authMethod":        normalizeDiagnosticAuthMethod(account.AuthMethod, account.ClientID),
			"tokenExpiresAt":    account.ExpiresAt,
			"tokenExpired":      account.ExpiresAt > 0 && nowUnix >= account.ExpiresAt,
			"refreshViable":     strings.TrimSpace(account.RefreshToken) != "",
			"profileArnPresent": strings.TrimSpace(account.ProfileArn) != "",
			"modelListCached":   len(models) > 0,
			"modelListed":       modelListed,
			"quotaBlocked":      readinessAccountUsageBlocked(account),
			"proxyConfigured":   strings.TrimSpace(account.ProxyURL) != "",
			"coolingDown":       account.CooldownUntil > nowUnix,
			"cooldownRemaining": cooldownRemaining,
			"lastFailureReason": account.LastFailureReason,
		},
		"runtimeHealth": runtimeHealthForAccount(h, account.ID),
	}
}

func normalizeDiagnosticAuthMethod(authMethod, clientID string) string {
	authMethod = strings.ToLower(strings.TrimSpace(authMethod))
	if authMethod == "" {
		if strings.TrimSpace(clientID) != "" {
			return "idc"
		}
		return "social"
	}
	return authMethod
}

func (h *Handler) apiGetSchedulerPreview(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		model = "claude-sonnet-4.5"
	}
	mapped, _ := resolveClaudeThinkingMode(model, nil, config.GetThinkingConfig().Suffix)
	rows := h.schedulerPreviewRows(mapped)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"requestedModel": model,
		"mappedModel":    mapped,
		"strategy":       config.GetLoadBalanceConfig().Strategy,
		"accounts":       rows,
		"preferred":      preferredSchedulerPreviewRows(rows),
		"readOnly":       true,
	})
}

func (h *Handler) schedulerPreviewRows(model string) []map[string]interface{} {
	accounts := config.GetAccounts()
	rows := make([]map[string]interface{}, 0, len(accounts))
	now := time.Now()
	nowUnix := now.Unix()
	for _, account := range accounts {
		reason := "eligible"
		eligible := true
		if !account.Enabled {
			eligible, reason = false, "disabled"
		} else if account.CooldownUntil > nowUnix {
			eligible, reason = false, nonEmpty(account.LastFailureReason, "cooling_down")
		} else if account.ExpiresAt > 0 && nowUnix > account.ExpiresAt-tokenRefreshSkewSeconds {
			eligible, reason = false, "token_expired"
		} else if readinessAccountUsageBlocked(account) {
			eligible, reason = false, "usage_limit_reached"
		} else if h != nil && h.pool != nil {
			models := h.pool.GetModelList(account.ID)
			if len(models) > 0 && !stringSliceEqualFoldContains(models, model) {
				eligible, reason = false, "model_not_listed"
			}
		}
		rows = append(rows, map[string]interface{}{
			"id":            account.ID,
			"email":         maskReadinessEmail(account.Email),
			"eligible":      eligible,
			"reason":        reason,
			"weight":        account.Weight,
			"runtimeHealth": runtimeHealthForAccount(h, account.ID),
			"modelsCached":  modelListForAccount(h, account.ID),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ih := rowHealthScore(rows[i])
		jh := rowHealthScore(rows[j])
		if ih == jh {
			return fmt.Sprint(rows[i]["id"]) < fmt.Sprint(rows[j]["id"])
		}
		return ih > jh
	})
	return rows
}

func preferredSchedulerPreviewRows(rows []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, 3)
	for _, row := range rows {
		if eligible, _ := row["eligible"].(bool); !eligible {
			continue
		}
		out = append(out, row)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func (h *Handler) apiGetFleetReadiness(w http.ResponseWriter, r *http.Request) {
	accounts := config.GetAccounts()
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		model = "claude-sonnet-4.5"
	}
	mapped, _ := resolveClaudeThinkingMode(model, nil, config.GetThinkingConfig().Suffix)
	rows := h.schedulerPreviewRows(mapped)
	summary := map[string]int{"total": len(accounts), "enabled": 0, "eligible": 0, "disabled": 0, "coolingDown": 0, "quotaBlocked": 0, "modelNotListed": 0}
	for _, row := range rows {
		if eligible, _ := row["eligible"].(bool); eligible {
			summary["eligible"]++
		}
		switch row["reason"] {
		case "disabled":
			summary["disabled"]++
		case string(pool.FailureReasonTemporaryLimited), string(pool.FailureReasonRateLimited), "cooling_down":
			summary["coolingDown"]++
		case "usage_limit_reached":
			summary["quotaBlocked"]++
		case "model_not_listed":
			summary["modelNotListed"]++
		}
	}
	for _, account := range accounts {
		if account.Enabled {
			summary["enabled"]++
		}
	}
	snap := AdmissionPressureSnapshot{}
	if modelAdmissionGate != nil {
		snap = modelAdmissionGate.modelSnapshot(mapped)
	}
	blockState := pool.ModelBlockState{}
	if h != nil && h.pool != nil {
		blockState = h.pool.ModelBlockState(mapped, time.Now())
	}
	locallySchedulable := summary["eligible"]
	if blockState.AccountsEvaluated > 0 && blockState.Blocked > 0 {
		if blockState.AllBlocked {
			locallySchedulable = 0
		} else if blockState.Blocked < locallySchedulable {
			locallySchedulable -= blockState.Blocked
		}
	}
	safeConcurrency := snap.EffectiveMaxConcurrent
	if safeConcurrency <= 0 {
		safeConcurrency = locallySchedulable
	}
	if locallySchedulable >= 0 && safeConcurrency > locallySchedulable {
		safeConcurrency = locallySchedulable
	}
	status := "healthy"
	if snap.CircuitState == "open" || safeConcurrency <= 0 || locallySchedulable == 0 {
		status = "blocked"
	} else if snap.CircuitState == "degraded" || snap.CircuitState == "half_open" || snap.Score >= 2 || safeConcurrency < summary["eligible"] {
		status = "degraded"
	}
	continuity := contentContinuityReadinessStats(h.ensureRequestLogStore().List(maxRequestLogLimit), mapped, time.Now())
	recommendedQueueWaitSeconds := 0
	if cfg := config.Get(); cfg != nil {
		recommendedQueueWaitSeconds = cfg.ContentContinuity.MaxQueueWaitSeconds
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"model":                       model,
		"requestedModel":              model,
		"mappedModel":                 mapped,
		"status":                      status,
		"circuitState":                firstNonEmpty(snap.CircuitState, "closed"),
		"retryAfterSeconds":           snap.RetryAfterSeconds,
		"safeConcurrency":             safeConcurrency,
		"currentInFlight":             snap.ActiveRequests,
		"enabledAccounts":             summary["enabled"],
		"modelListedAccounts":         summary["total"] - summary["modelNotListed"],
		"locallySchedulableAccounts":  locallySchedulable,
		"coolingDownAccounts":         summary["coolingDown"],
		"temporaryLimitedAccounts":    countFleetRowsByReason(rows, string(pool.FailureReasonTemporaryLimited)),
		"quotaBlockedAccounts":        summary["quotaBlocked"],
		"authBlockedAccounts":         countFleetRowsByReason(rows, string(pool.FailureReasonAuthExpired)),
		"admissionPressureScore":      snap.Score,
		"lastPressureReason":          snap.LastPressureReason,
		"lastPressureAt":              snap.LastPressureAt,
		"notes":                       fleetReadinessNotes(status, snap, summary),
		"strategy":                    config.GetLoadBalanceConfig().Strategy,
		"summary":                     summary,
		"accounts":                    rows,
		"autoRefresh":                 h.getAutoRefreshStatus(),
		"healthCheck":                 h.getHealthCheckStatus(),
		"recentContentRequests":       continuity["recentContentRequests"],
		"contentSuccessRate":          continuity["contentSuccessRate"],
		"recentStableFallbacks":       continuity["recentStableFallbacks"],
		"recentEmptyCompletions":      continuity["recentEmptyCompletions"],
		"recommendedQueueWaitSeconds": recommendedQueueWaitSeconds,
	})
}

func contentContinuityReadinessStats(logs []RequestLogEntry, model string, now time.Time) map[string]interface{} {
	model = normalizeAdmissionModel(model)
	recent := 0
	contentSuccess := 0
	stableFallbacks := 0
	emptyCompletions := 0
	for _, entry := range logs {
		if now.Sub(entry.Timestamp) > 10*time.Minute {
			continue
		}
		if normalizeAdmissionModel(entry.Model) != model {
			continue
		}
		recent++
		if entry.ContentSuccess {
			contentSuccess++
		}
		if entry.StableDownstreamFallback {
			stableFallbacks++
		}
		if !entry.ContentSuccess && (entry.StableDownstreamFallback || strings.TrimSpace(entry.ContentFailureReason) != "") {
			emptyCompletions++
		}
	}
	rate := 1.0
	if recent > 0 {
		rate = float64(contentSuccess) / float64(recent)
	}
	return map[string]interface{}{
		"recentContentRequests":  recent,
		"contentSuccessRate":     rate,
		"recentStableFallbacks":  stableFallbacks,
		"recentEmptyCompletions": emptyCompletions,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func countFleetRowsByReason(rows []map[string]interface{}, reason string) int {
	count := 0
	for _, row := range rows {
		if fmt.Sprint(row["reason"]) == reason || fmt.Sprint(row["lastFailureReason"]) == reason {
			count++
		}
	}
	return count
}

func fleetReadinessNotes(status string, snap AdmissionPressureSnapshot, summary map[string]int) []string {
	notes := []string{}
	if status == "blocked" {
		notes = append(notes, "sub2api should not send new Opus 4.7 calls until retryAfterSeconds or schedulable capacity recovers")
	}
	if status == "degraded" {
		notes = append(notes, "sub2api should queue or limit Opus 4.7 calls to safeConcurrency")
	}
	if snap.CircuitState == "open" {
		notes = append(notes, "model circuit is open due to recent upstream pressure")
	}
	if summary["coolingDown"] > 0 {
		notes = append(notes, "some accounts are cooling down and must not be probed aggressively")
	}
	return notes
}

func (h *Handler) apiGetWebSearchDiagnostics(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	logs := h.ensureRequestLogStore().List(maxRequestLogLimit)
	recent := make([]map[string]interface{}, 0, 10)
	for _, entry := range logs {
		if entry.MCPServerCount == 0 && !containsMCPToolName(entry.PayloadKeptTools) && !containsMCPToolName(entry.PayloadMaterializedToolRefs) {
			continue
		}
		recent = append(recent, map[string]interface{}{
			"requestId":      entry.RequestID,
			"timestamp":      entry.Timestamp,
			"accountId":      entry.AccountID,
			"mcpServerCount": entry.MCPServerCount,
		})
		if len(recent) == 10 {
			break
		}
	}
	status := "ready"
	reason := ""
	if query == "" {
		status = "query_required_for_live_test"
		reason = "query_extraction_required"
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         status,
		"reason":         reason,
		"query":          query,
		"supported":      true,
		"localMCPHost":   false,
		"recent":         recent,
		"failureClasses": []string{"query_extraction_failed", "no_account_available", "kiro_mcp_http_error", "empty_results", "payload_injection_failed"},
	})
}

func findConfigAccount(id string) (config.Account, bool) {
	for _, account := range config.GetAccounts() {
		if account.ID == id {
			return account, true
		}
	}
	return config.Account{}, false
}

func runtimeHealthForAccount(h *Handler, id string) pool.RuntimeHealth {
	if h == nil || h.pool == nil {
		return pool.RuntimeHealth{}
	}
	return h.pool.GetRuntimeHealth(id)
}

func modelListForAccount(h *Handler, id string) []string {
	if h == nil || h.pool == nil {
		return nil
	}
	return h.pool.GetModelList(id)
}

func rowHealthScore(row map[string]interface{}) int {
	health, _ := row["runtimeHealth"].(pool.RuntimeHealth)
	if health.Score != 0 {
		return health.Score
	}
	if healthMap, ok := row["runtimeHealth"].(map[string]interface{}); ok {
		switch value := healthMap["score"].(type) {
		case int:
			return value
		case float64:
			return int(value)
		}
	}
	return 0
}

func stringSliceEqualFoldContains(values []string, needle string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func summarizeBatchResults(results []map[string]interface{}) map[string]int {
	summary := map[string]int{"success": 0, "failed": 0, "skipped": 0}
	for _, result := range results {
		switch result["status"] {
		case "success":
			summary["success"]++
		case "skipped":
			summary["skipped"]++
		default:
			summary["failed"]++
		}
	}
	return summary
}
