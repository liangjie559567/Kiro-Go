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
	rows := h.readinessAccountRows(model, time.Now())
	out := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.asMap())
	}
	sortReadinessRows(out)
	return out
}

type readinessAccountRow struct {
	ID                          string
	Email                       string
	Eligible                    bool
	Reason                      string
	ReasonCodes                 []string
	Weight                      int
	RuntimeHealth               pool.RuntimeHealth
	ModelsCached                []string
	ModelsListed                bool
	ModelBreakerState           string
	ModelBreakerReason          string
	ModelBreakerRetryAt         time.Time
	CoolingDown                 bool
	CooldownRetryAt             time.Time
	CooldownRemainingSeconds    int64
	LastFailureReason           string
	LatestContentSuccessAt      time.Time
	LatestContentSuccessModel   string
	LatestContentSuccessPresent bool
}

func (row readinessAccountRow) asMap() map[string]interface{} {
	out := map[string]interface{}{
		"id":                row.ID,
		"email":             row.Email,
		"eligible":          row.Eligible,
		"reason":            row.Reason,
		"reasonCodes":       row.ReasonCodes,
		"weight":            row.Weight,
		"runtimeHealth":     row.RuntimeHealth,
		"modelsCached":      row.ModelsCached,
		"modelListed":       row.ModelsListed,
		"modelBreakerState": row.ModelBreakerState,
		"coolingDown":       row.CoolingDown,
	}
	if row.ModelBreakerReason != "" {
		out["modelBreakerReason"] = row.ModelBreakerReason
	}
	if !row.ModelBreakerRetryAt.IsZero() {
		out["modelBreakerRetryAt"] = row.ModelBreakerRetryAt
	}
	if !row.CooldownRetryAt.IsZero() {
		out["cooldownRetryAt"] = row.CooldownRetryAt
	}
	if row.CooldownRemainingSeconds > 0 {
		out["cooldownRemainingSeconds"] = row.CooldownRemainingSeconds
	}
	if row.LastFailureReason != "" {
		out["lastFailureReason"] = row.LastFailureReason
	}
	if row.LatestContentSuccessPresent {
		out["latestContentSuccessAt"] = row.LatestContentSuccessAt
		out["latestContentSuccessModel"] = row.LatestContentSuccessModel
	}
	return out
}

func (h *Handler) readinessAccountRows(model string, now time.Time) []readinessAccountRow {
	accounts := config.GetAccounts()
	rows := make([]readinessAccountRow, 0, len(accounts))
	nowUnix := now.Unix()
	for _, account := range accounts {
		eligible := true
		reasonCodes := []string{}
		models := modelListForAccount(h, account.ID)
		modelListed := len(models) == 0 || stringSliceEqualFoldContains(models, model)
		cooldownState := pool.CooldownState{}
		modelBlockState := pool.ModelAccountBlockState{CircuitState: "closed"}
		var contentSuccessAt time.Time
		contentSuccessOK := false
		if h != nil && h.pool != nil {
			cooldownState = h.pool.CooldownState(account.ID, now)
			modelBlockState = h.pool.ModelAccountBlockState(account.ID, model, now)
			contentSuccessAt, contentSuccessOK = h.pool.ModelContentSuccess(account.ID, model)
		}
		if !account.Enabled {
			eligible = false
			reasonCodes = append(reasonCodes, "disabled")
		}
		if cooldownState.CoolingDown || account.CooldownUntil > nowUnix {
			eligible = false
			reasonCodes = append(reasonCodes, "cooling_down")
		}
		if modelBlockState.Blocked {
			eligible = false
			reasonCodes = append(reasonCodes, "model_breaker_open")
		}
		if account.ExpiresAt > 0 && nowUnix > account.ExpiresAt-tokenRefreshSkewSeconds {
			eligible = false
			reasonCodes = append(reasonCodes, "token_expired")
		}
		if readinessAccountUsageBlocked(account) {
			eligible = false
			reasonCodes = append(reasonCodes, "usage_limit_reached")
		}
		if !modelListed {
			eligible = false
			reasonCodes = append(reasonCodes, "model_not_listed")
		}
		if len(reasonCodes) == 0 {
			reasonCodes = append(reasonCodes, "eligible")
		}
		cooldownRetryAt := cooldownState.RetryAt
		if account.CooldownUntil > nowUnix && time.Unix(account.CooldownUntil, 0).After(cooldownRetryAt) {
			cooldownRetryAt = time.Unix(account.CooldownUntil, 0)
		}
		cooldownRemaining := int64(0)
		if !cooldownRetryAt.IsZero() && cooldownRetryAt.After(now) {
			cooldownRemaining = int64((cooldownRetryAt.Sub(now) + time.Second - 1) / time.Second)
		}
		rows = append(rows, readinessAccountRow{
			ID:                          account.ID,
			Email:                       maskReadinessEmail(account.Email),
			Eligible:                    eligible,
			Reason:                      reasonCodes[0],
			ReasonCodes:                 reasonCodes,
			Weight:                      account.Weight,
			RuntimeHealth:               runtimeHealthForAccount(h, account.ID),
			ModelsCached:                models,
			ModelsListed:                modelListed,
			ModelBreakerState:           firstNonEmpty(modelBlockState.CircuitState, "closed"),
			ModelBreakerReason:          string(modelBlockState.Reason),
			ModelBreakerRetryAt:         modelBlockState.RetryAt,
			CoolingDown:                 cooldownState.CoolingDown || account.CooldownUntil > nowUnix,
			CooldownRetryAt:             cooldownRetryAt,
			CooldownRemainingSeconds:    cooldownRemaining,
			LastFailureReason:           firstNonEmpty(string(cooldownState.Reason), account.LastFailureReason),
			LatestContentSuccessAt:      contentSuccessAt,
			LatestContentSuccessModel:   model,
			LatestContentSuccessPresent: contentSuccessOK,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ih := rows[i].RuntimeHealth.Score
		jh := rows[j].RuntimeHealth.Score
		if ih == jh {
			return rows[i].ID < rows[j].ID
		}
		return ih > jh
	})
	return rows
}

func sortReadinessRows(rows []map[string]interface{}) {
	sort.SliceStable(rows, func(i, j int) bool {
		ih := rowHealthScore(rows[i])
		jh := rowHealthScore(rows[j])
		if ih == jh {
			return fmt.Sprint(rows[i]["id"]) < fmt.Sprint(rows[j]["id"])
		}
		return ih > jh
	})
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
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		model = "claude-sonnet-4.5"
	}
	json.NewEncoder(w).Encode(h.fleetReadinessEvidence(model))
}

func (h *Handler) fleetReadinessEvidence(model string) map[string]interface{} {
	mapped, _ := resolveClaudeThinkingMode(model, nil, config.GetThinkingConfig().Suffix)
	rowModels := h.readinessAccountRows(mapped, time.Now())
	rows := make([]map[string]interface{}, 0, len(rowModels))
	for _, row := range rowModels {
		rows = append(rows, row.asMap())
	}
	sortReadinessRows(rows)
	summary := readinessSummary(rowModels)
	snap := AdmissionPressureSnapshot{}
	if modelAdmissionGate != nil {
		snap = modelAdmissionGate.modelSnapshot(mapped)
	}
	locallySchedulable := summary["eligible"]
	admissionEffectiveConcurrency := snap.EffectiveMaxConcurrent
	if admissionEffectiveConcurrency <= 0 {
		admissionEffectiveConcurrency = locallySchedulable
	}
	retryAfterSeconds := fleetReadinessRetryAfterSeconds(rowModels, snap, locallySchedulable, time.Now())
	reasonCodes := fleetReadinessReasonCodes(rowModels, snap, locallySchedulable)
	status := "healthy"
	if snap.CircuitState == "open" || locallySchedulable == 0 || admissionEffectiveConcurrency <= 0 {
		status = "blocked"
	} else if snap.CircuitState == "degraded" || snap.CircuitState == "half_open" || snap.Score >= 2 {
		status = "degraded"
	}
	safeConcurrency := 0
	if status != "blocked" {
		safeConcurrency = minInt(locallySchedulable, admissionEffectiveConcurrency)
	}
	continuity := contentContinuityReadinessStats(h.ensureRequestLogStore().List(maxRequestLogLimit), mapped, time.Now())
	recommendedQueueWaitSeconds := 0
	if cfg := config.Get(); cfg != nil {
		recommendedQueueWaitSeconds = cfg.ContentContinuity.MaxQueueWaitSeconds
	}
	return map[string]interface{}{
		"contractVersion":               "opus-4.7-readiness.1",
		"model":                         model,
		"requestedModel":                model,
		"mappedModel":                   mapped,
		"status":                        status,
		"circuitState":                  firstNonEmpty(snap.CircuitState, "closed"),
		"retryAfterSeconds":             retryAfterSeconds,
		"safeConcurrency":               safeConcurrency,
		"admissionEffectiveConcurrency": admissionEffectiveConcurrency,
		"currentInFlight":               snap.ActiveRequests,
		"enabledAccounts":               summary["enabled"],
		"modelListedAccounts":           summary["total"] - summary["modelNotListed"],
		"locallySchedulableAccounts":    locallySchedulable,
		"coolingDownAccounts":           summary["coolingDown"],
		"temporaryLimitedAccounts":      countFleetRowsByReason(rows, string(pool.FailureReasonTemporaryLimited)),
		"quotaBlockedAccounts":          summary["quotaBlocked"],
		"authBlockedAccounts":           summary["authBlocked"],
		"modelBreakerBlockedAccounts":   summary["modelBreakerOpen"],
		"admissionPressureScore":        snap.Score,
		"lastPressureReason":            firstNonEmpty(snap.LastPressureReason, status),
		"lastPressureAt":                snap.LastPressureAt,
		"reasonCodes":                   reasonCodes,
		"recommendedAction":             fleetReadinessRecommendedAction(status, reasonCodes),
		"notes":                         fleetReadinessNotes(status, snap, summary),
		"strategy":                      config.GetLoadBalanceConfig().Strategy,
		"summary":                       summary,
		"accounts":                      rows,
		"autoRefresh":                   h.getAutoRefreshStatus(),
		"healthCheck":                   h.getHealthCheckStatus(),
		"recentContentRequests":         continuity["recentContentRequests"],
		"contentSuccessRate":            continuity["contentSuccessRate"],
		"recentStableFallbacks":         continuity["recentStableFallbacks"],
		"recentEmptyCompletions":        continuity["recentEmptyCompletions"],
		"recommendedQueueWaitSeconds":   recommendedQueueWaitSeconds,
	}
}

func fleetReadinessRetryAfterSeconds(rows []readinessAccountRow, snap AdmissionPressureSnapshot, locallySchedulable int, now time.Time) int {
	retryAfter := snap.RetryAfterSeconds
	if locallySchedulable > 0 {
		return retryAfter
	}
	for _, row := range rows {
		if row.Eligible {
			continue
		}
		if row.CoolingDown && int(row.CooldownRemainingSeconds) > retryAfter {
			retryAfter = int(row.CooldownRemainingSeconds)
		}
		if !row.ModelBreakerRetryAt.IsZero() && row.ModelBreakerRetryAt.After(now) {
			seconds := int(time.Until(row.ModelBreakerRetryAt).Seconds())
			if seconds < 1 {
				seconds = 1
			}
			if seconds > retryAfter {
				retryAfter = seconds
			}
		}
	}
	return retryAfter
}

func readinessSummary(rows []readinessAccountRow) map[string]int {
	summary := map[string]int{"total": len(rows), "enabled": 0, "eligible": 0, "disabled": 0, "coolingDown": 0, "quotaBlocked": 0, "modelNotListed": 0, "authBlocked": 0, "modelBreakerOpen": 0}
	for _, row := range rows {
		if row.Eligible {
			summary["eligible"]++
		}
		for _, code := range row.ReasonCodes {
			switch code {
			case "disabled":
				summary["disabled"]++
			case "cooling_down":
				summary["coolingDown"]++
			case "usage_limit_reached":
				summary["quotaBlocked"]++
			case "model_not_listed":
				summary["modelNotListed"]++
			case "token_expired":
				summary["authBlocked"]++
			case "model_breaker_open":
				summary["modelBreakerOpen"]++
			}
		}
		if !hasReasonCode(row.ReasonCodes, "disabled") {
			summary["enabled"]++
		}
	}
	return summary
}

func hasReasonCode(codes []string, target string) bool {
	for _, code := range codes {
		if code == target {
			return true
		}
	}
	return false
}

func fleetReadinessReasonCodes(rows []readinessAccountRow, snap AdmissionPressureSnapshot, locallySchedulable int) []string {
	set := map[string]bool{}
	if locallySchedulable == 0 {
		set["no_schedulable_accounts"] = true
		for _, row := range rows {
			for _, code := range row.ReasonCodes {
				if code != "eligible" {
					set[code] = true
				}
			}
		}
	}
	if snap.CircuitState == "open" {
		set["admission_circuit_open"] = true
	} else if snap.Score >= 2 || snap.CircuitState == "degraded" || snap.CircuitState == "half_open" {
		set["admission_pressure"] = true
	}
	if len(set) == 0 {
		return []string{"healthy"}
	}
	out := make([]string, 0, len(set))
	for code := range set {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

func fleetReadinessRecommendedAction(status string, reasonCodes []string) string {
	if status == "healthy" {
		return "send_with_safe_concurrency"
	}
	if hasReasonCode(reasonCodes, "no_schedulable_accounts") || hasReasonCode(reasonCodes, "admission_circuit_open") {
		return "retry_after_or_wait_for_recovery"
	}
	return "limit_to_safe_concurrency"
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
