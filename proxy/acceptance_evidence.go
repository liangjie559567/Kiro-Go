package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) apiGetAcceptanceEvidence(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		model = "claude-opus-4.7"
	}
	fleet := h.fleetReadinessEvidence(model)
	mapped, _ := fleet["mappedModel"].(string)
	if mapped == "" {
		mapped = model
	}
	resp := map[string]interface{}{
		"contractVersion":          "phase7-acceptance.1",
		"generatedAt":              time.Now().UTC(),
		"requestedModel":           model,
		"mappedModel":              mapped,
		"fleetReadiness":           fleet,
		"requestLogEvidence":       h.acceptanceRequestLogEvidence(mapped),
		"safeDiagnosticHeaders":    phase7SafeDiagnosticHeaders(),
		"sub2apiEvidenceRequired":  phase7Sub2APIEvidenceRequired(),
		"uatBundleRequired":        phase7UATBundleRequired(),
		"verdictRules":             phase7VerdictRules(),
		"secretRedactionBoundary":  phase7SecretRedactionBoundary(),
		"safeForDownstreamSharing": true,
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) acceptanceRequestLogEvidence(model string) map[string]interface{} {
	logs := h.ensureRequestLogStore().List(maxRequestLogLimit)
	normalized := normalizeAdmissionModel(model)
	cutoff := time.Now().Add(-30 * time.Minute)
	total := 0
	contentSuccess := 0
	stableFallbacks := 0
	var latest *RequestLogEntry
	for _, entry := range logs {
		entryModel := firstNonEmpty(entry.EffectiveModel, entry.Model)
		if normalizeAdmissionModel(entryModel) != normalized {
			continue
		}
		total++
		if latest == nil || entry.Timestamp.After(latest.Timestamp) {
			copy := entry
			latest = &copy
		}
		if entry.ContentSuccess {
			contentSuccess++
		}
		if entry.StableDownstreamFallback {
			stableFallbacks++
		}
	}
	recent := 0
	for _, entry := range logs {
		entryModel := firstNonEmpty(entry.EffectiveModel, entry.Model)
		if normalizeAdmissionModel(entryModel) == normalized && !entry.Timestamp.Before(cutoff) {
			recent++
		}
	}
	out := map[string]interface{}{
		"model":                  model,
		"totalOpus47Logs":        total,
		"recentWindowSeconds":    1800,
		"recentOpus47Logs":       recent,
		"contentSuccessCount":    contentSuccess,
		"stableFallbackCount":    stableFallbacks,
		"requiredFields":         phase7RequestLogRequiredFields(),
		"latestRequiredCoverage": map[string]bool{},
	}
	if latest == nil {
		out["latest"] = nil
		out["latestRequiredCoverage"] = requestLogEvidenceCoverage(nil)
		out["verdict"] = "no_recent_generation_evidence"
		return out
	}
	out["latest"] = safeAcceptanceLogSummary(*latest)
	out["latestRequiredCoverage"] = requestLogEvidenceCoverage(latest)
	if latest.ContentSuccess && !latest.StableDownstreamFallback {
		out["verdict"] = "latest_real_content_success"
	} else if latest.StableDownstreamFallback {
		out["verdict"] = "latest_stable_fallback_not_generation_pass"
	} else {
		out["verdict"] = "latest_not_real_content_success"
	}
	return out
}

func safeAcceptanceLogSummary(entry RequestLogEntry) map[string]interface{} {
	return map[string]interface{}{
		"timestamp":                  entry.Timestamp,
		"requestId":                  entry.RequestID,
		"endpoint":                   entry.Endpoint,
		"requestedModel":             firstNonEmpty(entry.RequestedModel, entry.Model),
		"effectiveModel":             firstNonEmpty(entry.EffectiveModel, entry.Model),
		"stream":                     entry.Stream,
		"statusCode":                 entry.StatusCode,
		"outcome":                    entry.Outcome,
		"accountId":                  entry.AccountID,
		"region":                     entry.Region,
		"admissionReadinessStatus":   entry.AdmissionReadinessStatus,
		"admissionSafeConcurrency":   entry.AdmissionSafeConcurrency,
		"admissionRetryAfterSeconds": entry.AdmissionRetryAfterSeconds,
		"admissionCircuitState":      entry.AdmissionCircuitState,
		"admissionPressureReason":    entry.AdmissionPressureReason,
		"effectiveConcurrentLimit":   entry.EffectiveConcurrentLimit,
		"admissionPressureScore":     entry.AdmissionPressureScore,
		"attempts":                   entry.Attempts,
		"attemptTrace":               entry.AttemptTrace,
		"stableDownstreamFallback":   entry.StableDownstreamFallback,
		"stableFallbackReason":       entry.StableFallbackReason,
		"contentSuccess":             entry.ContentSuccess,
		"contentSuccessEvidence":     entry.ContentSuccessEvidence,
		"contentFailureReason":       entry.ContentFailureReason,
		"upstreamContentTokens":      entry.UpstreamContentTokens,
		"durationMs":                 entry.DurationMs,
		"errorType":                  entry.ErrorType,
		"error":                      entry.Error,
	}
}

func requestLogEvidenceCoverage(entry *RequestLogEntry) map[string]bool {
	coverage := map[string]bool{}
	for _, field := range phase7RequestLogRequiredFields() {
		coverage[field] = false
	}
	if entry == nil {
		return coverage
	}
	coverage["requestedModel"] = firstNonEmpty(entry.RequestedModel, entry.Model) != ""
	coverage["effectiveModel"] = firstNonEmpty(entry.EffectiveModel, entry.Model) != ""
	coverage["admissionReadinessStatus"] = entry.AdmissionReadinessStatus != ""
	coverage["admissionSafeConcurrency"] = entry.AdmissionSafeConcurrency >= 0
	coverage["admissionRetryAfterSeconds"] = entry.AdmissionRetryAfterSeconds >= 0
	coverage["admissionCircuitState"] = entry.AdmissionCircuitState != ""
	coverage["admissionPressureReason"] = entry.AdmissionPressureReason != ""
	coverage["selectedAccount"] = entry.AccountID != ""
	coverage["attemptTrace"] = len(entry.AttemptTrace) > 0
	coverage["fallbackState"] = true
	coverage["contentSuccessEvidence"] = entry.ContentSuccessEvidence != "" || entry.ContentFailureReason != "" || entry.StableDownstreamFallback
	coverage["latencyStatusError"] = entry.DurationMs >= 0 && entry.StatusCode > 0 && entry.Outcome != ""
	return coverage
}

func phase7RequestLogRequiredFields() []string {
	return []string{
		"requestedModel",
		"effectiveModel",
		"admissionReadinessStatus",
		"admissionSafeConcurrency",
		"admissionRetryAfterSeconds",
		"admissionCircuitState",
		"admissionPressureReason",
		"selectedAccount",
		"attemptTrace",
		"fallbackState",
		"contentSuccessEvidence",
		"latencyStatusError",
	}
}

func phase7SafeDiagnosticHeaders() []string {
	return []string{
		"Retry-After",
		"X-Kiro-Go-Error-Reason",
		"X-Kiro-Go-Retryable",
		"X-Kiro-Go-Circuit-State",
		"X-Kiro-Go-Safe-Concurrency",
		"X-Kiro-Go-Stable-Fallback",
		"X-Kiro-Go-Internal-Reason",
		"x-request-id",
	}
}

func phase7Sub2APIEvidenceRequired() []string {
	return []string{
		"kiro_go_readiness_decision log with account_id/status/cache_hit/ttl_ms/retry_after_seconds/safe_concurrency/requested_model/effective_model/provider/contract/reasons/error",
		"usage_logs rows with request_id/account_id/model/requested_model/upstream_model/channel_id/stream/duration_ms/first_token_ms",
		"ops_error_logs rows distinguishing upstream_status_code 429/529 from readiness-blocked scheduling",
		"accounts scheduling state with schedulable/rate_limit_reset_at/overload_until/temp_unschedulable_until/temp_unschedulable_reason",
	}
}

func phase7UATBundleRequired() []string {
	return []string{
		"evidence-manifest.json",
		"UAT-RESULT.md",
		"api/fleet-readiness.json",
		"api/acceptance-evidence.json",
		"api/request-logs.json",
		"request-results/non-stream.jsonl",
		"request-results/stream.jsonl",
		"headers/*.txt",
		"sub2api/readiness-decisions.log",
		"sub2api/usage-logs.json",
		"sub2api/ops-errors.json",
		"sub2api/account-scheduling-state.json",
		"screenshots/*.png",
		"console-summary.json",
		"redaction-report.json",
	}
}

func phase7VerdictRules() []string {
	return []string{
		"Full generation PASS requires healthy or degraded readiness with safeConcurrency > 0 before running 100 non-stream and 100 stream requests.",
		"Full generation PASS requires 100/100 HTTP completions, 100/100 real content successes, zero stable fallback successes, and no replay after started stream content.",
		"Blocked readiness or safeConcurrency=0 can only produce blocked-capacity PASS or generation blocked by upstream capacity.",
		"Stable fallback, empty completion, and transport-only HTTP 200 never count as real Opus 4.7 generation success.",
	}
}

func phase7SecretRedactionBoundary() []string {
	return []string{
		"Do not read data/config.json, .env, token stores, keychains, browser sessions, or recovery snapshots.",
		"Do not include API keys, access tokens, refresh tokens, cookies, client secrets, private upstream headers, profile ARNs, machine IDs, or raw account secrets in evidence.",
		"UAT harnesses must receive URLs/passwords/API keys only through explicit environment variables and must redact them before writing artifacts.",
	}
}
