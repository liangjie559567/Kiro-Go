package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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
	Timestamp                time.Time `json:"timestamp"`
	RequestID                string    `json:"requestId"`
	Method                   string    `json:"method"`
	Endpoint                 string    `json:"endpoint"`
	Model                    string    `json:"model,omitempty"`
	AccountID                string    `json:"accountId,omitempty"`
	Region                   string    `json:"region,omitempty"`
	Stream                   bool      `json:"stream"`
	StatusCode               int       `json:"statusCode"`
	Outcome                  string    `json:"outcome"`
	DurationMs               int64     `json:"durationMs"`
	InputTokens              int       `json:"inputTokens,omitempty"`
	OutputTokens             int       `json:"outputTokens,omitempty"`
	CacheReadInputTokens     int       `json:"cacheReadInputTokens,omitempty"`
	CacheCreationInputTokens int       `json:"cacheCreationInputTokens,omitempty"`
	ErrorType                string    `json:"errorType,omitempty"`
	Error                    string    `json:"error,omitempty"`
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
			Timestamp: time.Now().UTC(),
			RequestID: requestID,
			Method:    r.Method,
			Endpoint:  r.URL.Path,
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
	ctx.entry.Model = model
	ctx.entry.Stream = stream
}

func updateRequestLogUpstream(r *http.Request, accountID, region string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.entry.AccountID = strings.TrimSpace(accountID)
	ctx.entry.Region = strings.TrimSpace(region)
}

func updateRequestLogUsage(r *http.Request, inputTokens, outputTokens, cacheReadInputTokens, cacheCreationInputTokens int) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.entry.InputTokens = inputTokens
	ctx.entry.OutputTokens = outputTokens
	ctx.entry.CacheReadInputTokens = cacheReadInputTokens
	ctx.entry.CacheCreationInputTokens = cacheCreationInputTokens
}

func (h *Handler) finishRequestLog(ctx *requestLogContext, rw *responseLogWriter) {
	if ctx == nil || rw == nil {
		return
	}
	status := rw.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	entry := ctx.entry
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
