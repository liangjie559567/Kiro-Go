package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultRequestLogCapacity = 5000
	maxRequestLogLimit        = 1000
)

type RequestLogEntry struct {
	Timestamp                           time.Time                 `json:"timestamp"`
	RequestID                           string                    `json:"requestId"`
	Method                              string                    `json:"method"`
	Endpoint                            string                    `json:"endpoint"`
	Model                               string                    `json:"model,omitempty"`
	AccountID                           string                    `json:"accountId,omitempty"`
	Region                              string                    `json:"region,omitempty"`
	ClaudeCodeSessionID                 string                    `json:"claudeCodeSessionId,omitempty"`
	ClaudeCodeAgentID                   string                    `json:"claudeCodeAgentId,omitempty"`
	ClaudeCodeParentAgentID             string                    `json:"claudeCodeParentAgentId,omitempty"`
	AnthropicRequestID                  string                    `json:"anthropicRequestId,omitempty"`
	AnthropicVersion                    string                    `json:"anthropicVersion,omitempty"`
	AnthropicBetas                      []string                  `json:"anthropicBetas,omitempty"`
	ToolReferenceCount                  int                       `json:"toolReferenceCount,omitempty"`
	HasContainer                        bool                      `json:"hasContainer,omitempty"`
	HasContextManagement                bool                      `json:"hasContextManagement,omitempty"`
	MCPServerCount                      int                       `json:"mcpServerCount,omitempty"`
	HasServiceTier                      bool                      `json:"hasServiceTier,omitempty"`
	HasMetadata                         bool                      `json:"hasMetadata,omitempty"`
	HasStopSequences                    bool                      `json:"hasStopSequences,omitempty"`
	ToolChoiceMode                      string                    `json:"toolChoiceMode,omitempty"`
	AnthropicBetaPresent                bool                      `json:"anthropicBetaPresent,omitempty"`
	ClaudeCodeSessionPresent            bool                      `json:"claudeCodeSessionPresent,omitempty"`
	ClaudeCodeAgentPresent              bool                      `json:"claudeCodeAgentPresent,omitempty"`
	ClaudeCodeParentAgentPresent        bool                      `json:"claudeCodeParentAgentPresent,omitempty"`
	ClaudeCodeProjectDirPresent         bool                      `json:"claudeCodeProjectDirPresent,omitempty"`
	ClaudeCodeVersionPresent            bool                      `json:"claudeCodeVersionPresent,omitempty"`
	Opus47ThinkingNormalized            bool                      `json:"opus47ThinkingNormalized,omitempty"`
	Opus47SamplingDropped               bool                      `json:"opus47SamplingDropped,omitempty"`
	PayloadOriginalBytes                int                       `json:"payloadOriginalBytes,omitempty"`
	PayloadFinalBytes                   int                       `json:"payloadFinalBytes,omitempty"`
	PayloadTrimmed                      bool                      `json:"payloadTrimmed,omitempty"`
	PayloadTrimmedCount                 int                       `json:"payloadTrimmedCount,omitempty"`
	PayloadCurrentTools                 int                       `json:"payloadCurrentTools,omitempty"`
	PayloadCurrentToolSchemaBytes       int                       `json:"payloadCurrentToolSchemaBytes,omitempty"`
	PayloadKeptTools                    []string                  `json:"payloadKeptTools,omitempty"`
	PayloadTrimmedTools                 []string                  `json:"payloadTrimmedTools,omitempty"`
	PayloadDeferredTools                []string                  `json:"payloadDeferredTools,omitempty"`
	PayloadMaterializedToolRefs         []string                  `json:"payloadMaterializedToolRefs,omitempty"`
	PayloadCompactedPairs               int                       `json:"payloadCompactedPairs,omitempty"`
	PayloadCompactedToolResults         int                       `json:"payloadCompactedToolResults,omitempty"`
	PayloadOrphanedToolResultsConverted int                       `json:"payloadOrphanedToolResultsConverted,omitempty"`
	PayloadToolResultImages             int                       `json:"payloadToolResultImages,omitempty"`
	PayloadRelocatedToolDescriptions    int                       `json:"payloadRelocatedToolDescriptions,omitempty"`
	PayloadUnsupportedContentBlocks     []string                  `json:"payloadUnsupportedContentBlocks,omitempty"`
	PayloadCurrentMessageShape          string                    `json:"payloadCurrentMessageShape,omitempty"`
	PayloadContextReminderKinds         []string                  `json:"payloadContextReminderKinds,omitempty"`
	PayloadUnknownOfficialFields        []string                  `json:"payloadUnknownOfficialFields,omitempty"`
	FineGrainedToolStreamingRequested   bool                      `json:"fineGrainedToolStreamingRequested,omitempty"`
	FineGrainedToolStreamingMode        string                    `json:"fineGrainedToolStreamingMode,omitempty"`
	AccountActiveConnections            int                       `json:"accountActiveConnections,omitempty"`
	AccountRecentFailures               int                       `json:"accountRecentFailures,omitempty"`
	AccountRecentSuccesses              int                       `json:"accountRecentSuccesses,omitempty"`
	AccountAvgLatencyMS                 int64                     `json:"accountAvgLatencyMs,omitempty"`
	AccountHealthScore                  int                       `json:"accountHealthScore,omitempty"`
	RoutingDecision                     string                    `json:"routingDecision,omitempty"`
	RoutingStrategy                     string                    `json:"routingStrategy,omitempty"`
	RoutingPressure                     bool                      `json:"routingPressure,omitempty"`
	Stream                              bool                      `json:"stream"`
	StatusCode                          int                       `json:"statusCode"`
	Outcome                             string                    `json:"outcome"`
	DurationMs                          int64                     `json:"durationMs"`
	QueueWaitMs                         int64                     `json:"queueWaitMs,omitempty"`
	AdmissionWaitMs                     int64                     `json:"admissionWaitMs,omitempty"`
	EffectiveConcurrentLimit            int                       `json:"effectiveConcurrentLimit,omitempty"`
	AdmissionPressureScore              int                       `json:"admissionPressureScore,omitempty"`
	CapacityRetryCount                  int                       `json:"capacityRetryCount,omitempty"`
	FirstTokenMs                        int64                     `json:"firstTokenMs,omitempty"`
	Attempts                            int                       `json:"attempts,omitempty"`
	ToolUseCount                        int                       `json:"toolUseCount,omitempty"`
	SuppressedToolUseCount              int                       `json:"suppressedToolUseCount,omitempty"`
	SuppressedToolUseNames              []string                  `json:"suppressedToolUseNames,omitempty"`
	SuppressedToolUseReasons            []string                  `json:"suppressedToolUseReasons,omitempty"`
	SuppressedToolUseDetails            []SuppressedToolUseDetail `json:"suppressedToolUseDetails,omitempty"`
	InputTokens                         int                       `json:"inputTokens,omitempty"`
	OutputTokens                        int                       `json:"outputTokens,omitempty"`
	CacheReadInputTokens                int                       `json:"cacheReadInputTokens,omitempty"`
	CacheCreationInputTokens            int                       `json:"cacheCreationInputTokens,omitempty"`
	MaxTokensZeroMode                   string                    `json:"maxTokensZeroMode,omitempty"`
	CountTokensMode                     string                    `json:"countTokensMode,omitempty"`
	AssistantPrefillMode                string                    `json:"assistantPrefillMode,omitempty"`
	ErrorType                           string                    `json:"errorType,omitempty"`
	Error                               string                    `json:"error,omitempty"`
}

type SuppressedToolUseDetail struct {
	ToolUseID    string `json:"toolUseId,omitempty"`
	Name         string `json:"name,omitempty"`
	Reason       string `json:"reason,omitempty"`
	InputSummary string `json:"inputSummary,omitempty"`
}

type AccountRequestHealthSnapshot struct {
	ActiveConnections int
	RecentFailures    int
	RecentSuccesses   int
	AvgLatencyMS      int64
	Score             int
}

type RequestLogBucket struct {
	Total                    int   `json:"total"`
	Success                  int   `json:"success"`
	Failed                   int   `json:"failed"`
	AverageDurationMs        int64 `json:"averageDurationMs"`
	InputTokens              int   `json:"inputTokens"`
	OutputTokens             int   `json:"outputTokens"`
	CacheReadInputTokens     int   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int   `json:"cacheCreationInputTokens"`
	QueueWaitMs              int64 `json:"queueWaitMs"`
	MaxQueueWaitMs           int64 `json:"maxQueueWaitMs"`
	FirstTokenMs             int64 `json:"firstTokenMs"`
	MaxFirstTokenMs          int64 `json:"maxFirstTokenMs"`
	Attempts                 int   `json:"attempts"`
	ToolUseCount             int   `json:"toolUseCount"`
}

type requestLogStore struct {
	mu       sync.RWMutex
	capacity int
	entries  []RequestLogEntry
}

func newRequestLogStore(capacity int) *requestLogStore {
	if capacity <= 0 {
		capacity = defaultRequestLogCapacity
	}
	return &requestLogStore{capacity: capacity}
}

func (s *requestLogStore) Add(entry RequestLogEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) >= s.capacity {
		copy(s.entries, s.entries[1:])
		s.entries[len(s.entries)-1] = entry
		return
	}
	s.entries = append(s.entries, entry)
}

func (s *requestLogStore) List(limit int) []RequestLogEntry {
	if s == nil {
		return nil
	}
	if limit <= 0 || limit > maxRequestLogLimit {
		limit = maxRequestLogLimit
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit > len(s.entries) {
		limit = len(s.entries)
	}
	out := make([]RequestLogEntry, 0, limit)
	for i := len(s.entries) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.entries[i])
	}
	return out
}

func (s *requestLogStore) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.entries = nil
	s.mu.Unlock()
}

type requestLogContextKey struct{}

type requestLogContext struct {
	startedAt time.Time
	mu        sync.Mutex
	entry     RequestLogEntry
}

type responseLogWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func (w *responseLogWriter) WriteHeader(statusCode int) {
	if w.statusCode == 0 {
		w.statusCode = statusCode
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseLogWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	if w.body.Len() < 4096 {
		remaining := 4096 - w.body.Len()
		if len(p) > remaining {
			w.body.Write(p[:remaining])
		} else {
			w.body.Write(p)
		}
	}
	return w.ResponseWriter.Write(p)
}

type flushResponseLogWriter struct {
	*responseLogWriter
}

func (w *flushResponseLogWriter) Flush() {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

func (h *Handler) ensureRequestLogStore() *requestLogStore {
	if h.requestLogs != nil {
		return h.requestLogs
	}
	h.requestLogsMu.Lock()
	defer h.requestLogsMu.Unlock()
	if h.requestLogs == nil {
		h.requestLogs = newRequestLogStore(defaultRequestLogCapacity)
	}
	return h.requestLogs
}

func (h *Handler) beginRequestLog(w http.ResponseWriter, r *http.Request) (*requestLogContext, *http.Request, *responseLogWriter, http.ResponseWriter) {
	if !shouldLogRequestPath(r.URL.Path) {
		return nil, r, nil, w
	}

	requestID := strings.TrimSpace(r.Header.Get("x-request-id"))
	if requestID == "" {
		requestID = strings.TrimSpace(r.Header.Get("request-id"))
	}
	if requestID == "" {
		requestID = uuid.New().String()
	}
	w.Header().Set("x-request-id", requestID)

	ctx := &requestLogContext{
		startedAt: time.Now(),
		entry: RequestLogEntry{
			Timestamp:               time.Now().UTC(),
			RequestID:               requestID,
			Method:                  r.Method,
			Endpoint:                r.URL.Path,
			ClaudeCodeSessionID:     strings.TrimSpace(r.Header.Get("X-Claude-Code-Session-Id")),
			ClaudeCodeAgentID:       strings.TrimSpace(r.Header.Get("X-Claude-Code-Agent-Id")),
			ClaudeCodeParentAgentID: firstNonEmptyHeader(r, "X-Claude-Code-Parent-Agent-Id", "X-Claude-Parent-Agent-Id"),
		},
	}
	recorder := &responseLogWriter{ResponseWriter: w}
	var wrapped http.ResponseWriter = recorder
	if _, ok := w.(http.Flusher); ok {
		wrapped = &flushResponseLogWriter{responseLogWriter: recorder}
	}
	return ctx, r.WithContext(context.WithValue(r.Context(), requestLogContextKey{}, ctx)), recorder, wrapped
}

func shouldLogRequestPath(path string) bool {
	switch path {
	case "/v1/messages", "/messages", "/anthropic/v1/messages",
		"/v1/messages/count_tokens", "/messages/count_tokens",
		"/v1/chat/completions", "/chat/completions",
		"/v1/responses", "/responses",
		"/v1/models", "/models", "/v1/stats":
		return true
	default:
		return false
	}
}

func updateRequestLogMetadata(r *http.Request, model string, stream bool) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.Model = model
	ctx.entry.Stream = stream
}

func updateRequestLogAnthropic(r *http.Request, env *anthropicEnvelope) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil || env == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.AnthropicRequestID = env.AnthropicRequestID
	ctx.entry.AnthropicVersion = env.AnthropicVersion
	ctx.entry.AnthropicBetas = sortedAnthropicBetas(env.Betas)
	ctx.entry.ToolReferenceCount = len(env.Request.ToolReferences)
	ctx.entry.ClaudeCodeParentAgentID = env.ParentAgentID
	ctx.entry.PayloadUnknownOfficialFields = append([]string(nil), env.OfficialExtraKeys...)
	ctx.entry.HasContainer = rawHasKey(env.Extra, "container")
	ctx.entry.HasContextManagement = rawHasKey(env.Extra, "context_management")
	ctx.entry.MCPServerCount = rawArrayLen(env.Extra, "mcp_servers")
	ctx.entry.HasServiceTier = rawHasKey(env.Extra, "service_tier")
	ctx.entry.HasMetadata = rawHasKey(env.Extra, "metadata")
	ctx.entry.HasStopSequences = rawHasKey(env.Extra, "stop_sequences")
	ctx.entry.ToolChoiceMode = toolChoiceMode(env.Request.ToolChoice)
	ctx.entry.AnthropicBetaPresent = strings.TrimSpace(env.BetaHeader) != ""
	ctx.entry.ClaudeCodeSessionPresent = strings.TrimSpace(env.SessionID) != ""
	ctx.entry.ClaudeCodeAgentPresent = strings.TrimSpace(env.AgentID) != ""
	ctx.entry.ClaudeCodeParentAgentPresent = strings.TrimSpace(env.ParentAgentID) != ""
	ctx.entry.ClaudeCodeProjectDirPresent = env.ProjectDirPresent
	ctx.entry.ClaudeCodeVersionPresent = strings.TrimSpace(env.Version) != ""
	if env.HasBeta("fine-grained-tool-streaming-2025-05-14") {
		ctx.entry.FineGrainedToolStreamingRequested = true
		ctx.entry.FineGrainedToolStreamingMode = "requested_partial"
	}
}

func updateRequestLogOpus47Normalization(r *http.Request, meta opus47NormalizationMetadata) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.Opus47ThinkingNormalized = meta.ThinkingNormalized
	ctx.entry.Opus47SamplingDropped = meta.SamplingDropped
}

func rawHasKey(raw map[string]json.RawMessage, key string) bool {
	if raw == nil {
		return false
	}
	_, ok := raw[key]
	return ok
}

func rawArrayLen(raw map[string]json.RawMessage, key string) int {
	value, ok := raw[key]
	if !ok {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(value, &arr); err != nil {
		return 0
	}
	return len(arr)
}

func toolChoiceMode(toolChoice interface{}) string {
	switch choice := toolChoice.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(choice)
	case map[string]interface{}:
		if raw, ok := choice["type"].(string); ok {
			return strings.TrimSpace(raw)
		}
	case map[string]string:
		return strings.TrimSpace(choice["type"])
	}
	return "present"
}

func updateRequestLogPayload(r *http.Request, result payloadGuardResult) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.PayloadOriginalBytes = result.OriginalBytes
	ctx.entry.PayloadFinalBytes = result.FinalBytes
	ctx.entry.PayloadTrimmed = result.Trimmed
	ctx.entry.PayloadTrimmedCount = result.TrimmedCount
	ctx.entry.PayloadCurrentTools = result.Summary.CurrentTools
	ctx.entry.PayloadCurrentToolSchemaBytes = result.Summary.CurrentToolSchemaBytes
	ctx.entry.PayloadKeptTools = append([]string(nil), result.KeptToolNames...)
	ctx.entry.PayloadTrimmedTools = append([]string(nil), result.TrimmedToolNames...)
	ctx.entry.PayloadDeferredTools = append([]string(nil), result.DeferredToolNames...)
	ctx.entry.PayloadMaterializedToolRefs = append([]string(nil), result.MaterializedToolRefNames...)
	ctx.entry.PayloadCompactedPairs = result.CompactedPairs
	ctx.entry.PayloadCompactedToolResults = result.CompactedToolResults
	ctx.entry.PayloadOrphanedToolResultsConverted = result.OrphanedToolResultsConverted
	ctx.entry.PayloadToolResultImages = result.ToolResultImages
	ctx.entry.PayloadRelocatedToolDescriptions = result.RelocatedToolDescriptions
	ctx.entry.PayloadUnsupportedContentBlocks = append([]string(nil), result.UnsupportedContentBlocks...)
	ctx.entry.PayloadCurrentMessageShape = result.Summary.CurrentMessageShape
	ctx.entry.PayloadContextReminderKinds = append([]string(nil), result.Summary.ContextReminderKinds...)
}

func updateRequestLogPayloadFinalBytes(r *http.Request, finalBytes int) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.PayloadFinalBytes = finalBytes
}

func updateRequestLogSuppressedToolUse(r *http.Request, name, reason string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	name = strings.TrimSpace(name)
	reason = strings.TrimSpace(reason)
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.SuppressedToolUseCount++
	if name != "" && !stringSliceContains(ctx.entry.SuppressedToolUseNames, name) {
		ctx.entry.SuppressedToolUseNames = append(ctx.entry.SuppressedToolUseNames, name)
	}
	if reason != "" && !stringSliceContains(ctx.entry.SuppressedToolUseReasons, reason) {
		ctx.entry.SuppressedToolUseReasons = append(ctx.entry.SuppressedToolUseReasons, reason)
	}
}

func updateRequestLogSuppressedToolUseDetail(r *http.Request, tu KiroToolUse, reason string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	detail := SuppressedToolUseDetail{
		ToolUseID:    strings.TrimSpace(tu.ToolUseID),
		Name:         strings.TrimSpace(tu.Name),
		Reason:       strings.TrimSpace(reason),
		InputSummary: summarizeSuppressedToolInput(tu.Input),
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if len(ctx.entry.SuppressedToolUseDetails) < 20 {
		ctx.entry.SuppressedToolUseDetails = append(ctx.entry.SuppressedToolUseDetails, detail)
	}
}

func summarizeSuppressedToolInput(input map[string]interface{}) string {
	if len(input) == 0 {
		return "{}"
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "<unserializable>"
	}
	const maxSummaryBytes = 512
	if len(data) <= maxSummaryBytes {
		return string(data)
	}
	return string(data[:maxSummaryBytes]) + "...(truncated)"
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func sortedAnthropicBetas(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for beta := range in {
		out = append(out, beta)
	}
	sort.Strings(out)
	return out
}

func updateRequestLogUpstream(r *http.Request, accountID, region string, health ...AccountRequestHealthSnapshot) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.AccountID = strings.TrimSpace(accountID)
	ctx.entry.Region = strings.TrimSpace(region)
	if len(health) > 0 {
		snapshot := health[0]
		ctx.entry.AccountActiveConnections = snapshot.ActiveConnections
		ctx.entry.AccountRecentFailures = snapshot.RecentFailures
		ctx.entry.AccountRecentSuccesses = snapshot.RecentSuccesses
		ctx.entry.AccountAvgLatencyMS = snapshot.AvgLatencyMS
		ctx.entry.AccountHealthScore = snapshot.Score
	}
}

func updateRequestLogRouting(r *http.Request, decision, strategy string, pressure bool) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.RoutingDecision = strings.TrimSpace(decision)
	ctx.entry.RoutingStrategy = strings.TrimSpace(strategy)
	ctx.entry.RoutingPressure = pressure
}

func updateRequestLogUsage(r *http.Request, inputTokens, outputTokens, cacheReadInputTokens, cacheCreationInputTokens int) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.InputTokens = inputTokens
	ctx.entry.OutputTokens = outputTokens
	ctx.entry.CacheReadInputTokens = cacheReadInputTokens
	ctx.entry.CacheCreationInputTokens = cacheCreationInputTokens
}

func updateRequestLogMaxTokensZeroMode(r *http.Request, mode string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.MaxTokensZeroMode = strings.TrimSpace(mode)
}

func updateRequestLogCountTokensMode(r *http.Request, mode string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.CountTokensMode = strings.TrimSpace(mode)
}

func updateRequestLogAssistantPrefillMode(r *http.Request, mode string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.AssistantPrefillMode = strings.TrimSpace(mode)
}

func updateRequestLogReliability(r *http.Request, queueWaitMs int64, attempts int, firstTokenMs int64, toolUseCount int) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if queueWaitMs >= 0 {
		ctx.entry.QueueWaitMs = queueWaitMs
	}
	if attempts > 0 {
		ctx.entry.Attempts = attempts
	}
	if firstTokenMs > 0 {
		ctx.entry.FirstTokenMs = firstTokenMs
	}
	if toolUseCount >= 0 {
		ctx.entry.ToolUseCount = toolUseCount
	}
}

func updateRequestLogAdmission(r *http.Request, wait time.Duration, effectiveLimit int, pressureScore int) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.AdmissionWaitMs = wait.Milliseconds()
	ctx.entry.EffectiveConcurrentLimit = effectiveLimit
	ctx.entry.AdmissionPressureScore = pressureScore
}

func (h *Handler) finishRequestLog(ctx *requestLogContext, rw *responseLogWriter) {
	if ctx == nil || rw == nil {
		return
	}
	status := rw.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	ctx.mu.Lock()
	entry := ctx.entry
	ctx.mu.Unlock()
	entry.StatusCode = status
	entry.DurationMs = time.Since(ctx.startedAt).Milliseconds()
	entry.Error = extractResponseErrorSummary(rw.body.String())
	if status >= 400 || entry.Error != "" {
		entry.Outcome = "error"
	} else {
		entry.Outcome = "success"
	}
	h.recordRequestLog(entry)
}

func extractResponseErrorSummary(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}

	if strings.HasPrefix(body, "data:") {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			if msg := extractJSONErrorMessage(payload); msg != "" {
				return msg
			}
		}
		return ""
	}

	return extractJSONErrorMessage(body)
}

func extractJSONErrorMessage(body string) string {
	var generic struct {
		Error interface{} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &generic); err != nil || generic.Error == nil {
		return ""
	}

	var anthropic struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &anthropic); err == nil && anthropic.Error.Message != "" {
		return truncateRequestLogError(anthropic.Error.Message)
	}
	return ""
}

func truncateRequestLogError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 500 {
		return s
	}
	return s[:500]
}

func (h *Handler) recordRequestLog(entry RequestLogEntry) {
	h.ensureRequestLogStore().Add(entry)
}

func (h *Handler) apiGetRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": h.ensureRequestLogStore().List(limit),
	})
}

func (h *Handler) apiGetRequestStats(w http.ResponseWriter, r *http.Request) {
	logs := h.ensureRequestLogStore().List(maxRequestLogLimit)
	byModel := make(map[string]*requestLogBucketAccumulator)
	byEndpoint := make(map[string]*requestLogBucketAccumulator)
	total := requestLogBucketAccumulator{}

	for _, entry := range logs {
		addRequestLogBucket(&total, entry)
		model := strings.TrimSpace(entry.Model)
		if model == "" {
			model = "unknown"
		}
		if byModel[model] == nil {
			byModel[model] = &requestLogBucketAccumulator{}
		}
		addRequestLogBucket(byModel[model], entry)

		endpoint := strings.TrimSpace(entry.Endpoint)
		if endpoint == "" {
			endpoint = "unknown"
		}
		if byEndpoint[endpoint] == nil {
			byEndpoint[endpoint] = &requestLogBucketAccumulator{}
		}
		addRequestLogBucket(byEndpoint[endpoint], entry)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":                    total.Total,
		"success":                  total.Success,
		"failed":                   total.Failed,
		"averageDurationMs":        total.Export().AverageDurationMs,
		"inputTokens":              total.InputTokens,
		"outputTokens":             total.OutputTokens,
		"cacheReadInputTokens":     total.CacheReadInputTokens,
		"cacheCreationInputTokens": total.CacheCreationInputTokens,
		"byModel":                  exportRequestLogBuckets(byModel),
		"byEndpoint":               exportRequestLogBuckets(byEndpoint),
	})
}

func (h *Handler) apiGetAdmissionPressure(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pressure": modelAdmissionGate.snapshot(),
	})
}

func (h *Handler) apiClearRequestLogs(w http.ResponseWriter, r *http.Request) {
	h.ensureRequestLogStore().Clear()
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type requestLogBucketAccumulator struct {
	RequestLogBucket
	durationTotal int64
}

func addRequestLogBucket(bucket *requestLogBucketAccumulator, entry RequestLogEntry) {
	bucket.Total++
	if entry.Outcome == "success" && entry.StatusCode < 400 {
		bucket.Success++
	} else {
		bucket.Failed++
	}
	bucket.durationTotal += entry.DurationMs
	bucket.InputTokens += entry.InputTokens
	bucket.OutputTokens += entry.OutputTokens
	bucket.CacheReadInputTokens += entry.CacheReadInputTokens
	bucket.CacheCreationInputTokens += entry.CacheCreationInputTokens
	bucket.QueueWaitMs += entry.QueueWaitMs
	if entry.QueueWaitMs > bucket.MaxQueueWaitMs {
		bucket.MaxQueueWaitMs = entry.QueueWaitMs
	}
	bucket.FirstTokenMs += entry.FirstTokenMs
	if entry.FirstTokenMs > bucket.MaxFirstTokenMs {
		bucket.MaxFirstTokenMs = entry.FirstTokenMs
	}
	bucket.Attempts += entry.Attempts
	bucket.ToolUseCount += entry.ToolUseCount
}

func (b requestLogBucketAccumulator) Export() RequestLogBucket {
	out := b.RequestLogBucket
	if b.Total > 0 {
		out.AverageDurationMs = b.durationTotal / int64(b.Total)
	}
	return out
}

func exportRequestLogBuckets(in map[string]*requestLogBucketAccumulator) map[string]RequestLogBucket {
	out := make(map[string]RequestLogBucket, len(in))
	for key, value := range in {
		out[key] = value.Export()
	}
	return out
}
