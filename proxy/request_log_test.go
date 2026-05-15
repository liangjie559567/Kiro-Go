package proxy

import (
	"encoding/json"
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestLogStoreKeepsNewestEntriesFirst(t *testing.T) {
	store := newRequestLogStore(2)
	store.Add(RequestLogEntry{RequestID: "old", Timestamp: time.Unix(1, 0), Endpoint: "/v1/messages"})
	store.Add(RequestLogEntry{RequestID: "middle", Timestamp: time.Unix(2, 0), Endpoint: "/v1/messages"})
	store.Add(RequestLogEntry{RequestID: "new", Timestamp: time.Unix(3, 0), Endpoint: "/v1/chat/completions"})

	got := store.List(10)
	if len(got) != 2 {
		t.Fatalf("expected two retained entries, got %d", len(got))
	}
	if got[0].RequestID != "new" || got[1].RequestID != "middle" {
		t.Fatalf("expected newest retained entries first, got %#v", got)
	}

	limited := store.List(1)
	if len(limited) != 1 || limited[0].RequestID != "new" {
		t.Fatalf("expected limit to return newest entry, got %#v", limited)
	}
}

func TestAdminRequestLogsEndpointReturnsRecentEntries(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	h.recordRequestLog(RequestLogEntry{RequestID: "req-1", Endpoint: "/v1/messages", Model: "claude-opus-4.7", StatusCode: 200, Outcome: "success", DurationMs: 12})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/request-logs?limit=3", nil)
	w := httptest.NewRecorder()

	h.apiGetRequestLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Logs []RequestLogEntry `json:"logs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Logs) != 1 {
		t.Fatalf("expected one log entry, got %#v", resp.Logs)
	}
	entry := resp.Logs[0]
	if entry.RequestID != "req-1" || entry.Endpoint != "/v1/messages" || entry.Model != "claude-opus-4.7" || entry.StatusCode != 200 || entry.Outcome != "success" || entry.DurationMs != 12 {
		t.Fatalf("unexpected log entry: %#v", entry)
	}
}

func TestAdminRequestStatsEndpointAggregatesRecentEntries(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	h.recordRequestLog(RequestLogEntry{RequestID: "ok-1", Endpoint: "/v1/messages", Model: "claude-opus-4.7", StatusCode: 200, Outcome: "success", DurationMs: 100})
	h.recordRequestLog(RequestLogEntry{RequestID: "err-1", Endpoint: "/v1/messages", Model: "claude-opus-4.7", StatusCode: 503, Outcome: "error", DurationMs: 300})
	h.recordRequestLog(RequestLogEntry{RequestID: "ok-2", Endpoint: "/v1/chat/completions", Model: "claude-sonnet-4.6", StatusCode: 200, Outcome: "success", DurationMs: 200})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/request-stats", nil)
	w := httptest.NewRecorder()

	h.apiGetRequestStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Total             int                         `json:"total"`
		Success           int                         `json:"success"`
		Failed            int                         `json:"failed"`
		AverageDurationMs int64                       `json:"averageDurationMs"`
		ByModel           map[string]RequestLogBucket `json:"byModel"`
		ByEndpoint        map[string]RequestLogBucket `json:"byEndpoint"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 3 || resp.Success != 2 || resp.Failed != 1 || resp.AverageDurationMs != 200 {
		t.Fatalf("unexpected aggregate stats: %#v", resp)
	}
	if got := resp.ByModel["claude-opus-4.7"]; got.Total != 2 || got.Failed != 1 || got.AverageDurationMs != 200 {
		t.Fatalf("unexpected model stats: %#v", got)
	}
	if got := resp.ByEndpoint["/v1/messages"]; got.Total != 2 || got.Failed != 1 || got.AverageDurationMs != 200 {
		t.Fatalf("unexpected endpoint stats: %#v", got)
	}
}

func TestExtractResponseErrorSummaryOnlyUsesStructuredErrors(t *testing.T) {
	successBody := `{"content":[{"type":"text","text":"the word error is part of a normal answer"}]}`
	if got := extractResponseErrorSummary(successBody); got != "" {
		t.Fatalf("expected normal content not to be treated as an error, got %q", got)
	}

	jsonError := `{"error":{"message":"upstream unavailable","type":"api_error"},"type":"error"}`
	if got := extractResponseErrorSummary(jsonError); got != "upstream unavailable" {
		t.Fatalf("expected JSON error message, got %q", got)
	}

	sseError := "data: {\"error\":{\"message\":\"stream failed\",\"type\":\"api_error\"}}\n\ndata: [DONE]\n\n"
	if got := extractResponseErrorSummary(sseError); got != "stream failed" {
		t.Fatalf("expected SSE error message, got %q", got)
	}
}

func TestServeHTTPRecordsClaudeValidationError(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	h := &Handler{requestLogs: newRequestLogStore(5)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","messages":[]}`))
	req.Header.Set("x-request-id", "client-req-1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	logs := h.requestLogs.List(10)
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %#v", logs)
	}
	entry := logs[0]
	if entry.RequestID != "client-req-1" {
		t.Fatalf("expected provided request id, got %q", entry.RequestID)
	}
	if entry.Endpoint != "/v1/messages" || entry.Model != "claude-opus-4.7" || entry.Stream || entry.StatusCode != 400 || entry.Outcome != "error" {
		t.Fatalf("unexpected request log: %#v", entry)
	}
	if entry.DurationMs < 0 {
		t.Fatalf("expected non-negative duration, got %d", entry.DurationMs)
	}
}
