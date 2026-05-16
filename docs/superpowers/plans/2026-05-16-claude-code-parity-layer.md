# Claude Code Parity Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Claude Code parity layer described in `docs/superpowers/specs/2026-05-16-claude-code-parity-layer-design.md`.

**Architecture:** Add a tolerant Anthropic ingress envelope, a dedicated Claude SSE writer, tool-reference acceptance, per-model account breaker/stickiness, payload guards, and compatibility fixtures. Keep the existing Kiro conversion and handler flow, but move correctness-sensitive behavior into focused helpers with golden tests.

**Tech Stack:** Go standard library, existing `proxy` and `pool` packages, `httptest`, existing fake Kiro event helpers, existing request-log and account-pool patterns.

---

## File Structure

- Create: `proxy/anthropic_envelope.go`  
  Parses Claude/Anthropic headers and unknown request fields, exposes beta helpers, and installs request IDs on responses.
- Create: `proxy/anthropic_envelope_test.go`  
  Tests beta parsing, unknown-field preservation, `tool_reference` acceptance, and request ID headers.
- Create: `proxy/claude_sse_writer.go`  
  Owns Anthropic SSE event order, block lifecycle, heartbeat events, stream errors, and chunked tool JSON deltas.
- Create: `proxy/claude_sse_writer_test.go`  
  Golden event-order tests for text, thinking, tool use, heartbeat, and error events.
- Create: `pool/breaker.go`  
  Adds per-account, per-model breaker and sticky-session state with deterministic time injection for tests.
- Create: `pool/breaker_test.go`  
  Tests closed/open/half-open transitions, `Retry-After`, backoff, and sticky escape.
- Create: `proxy/payload_guard.go`  
  Performs serialized Kiro payload preflight, pairwise history trimming, and truncation recovery markers.
- Create: `proxy/payload_guard_test.go`  
  Tests byte-limit trimming, orphan repair, account-health neutrality, and recovery note injection.
- Modify: `proxy/translator.go`  
  Add request fields for `tool_reference`, preserve outward tool names, and expose helper conversions.
- Modify: `proxy/handler.go`  
  Wire the envelope, SSE writer, breaker-aware selection, request IDs, model endpoint hardening, and payload guard into `/v1/messages`.
- Modify: `proxy/request_log.go`  
  Store beta flags, tool reference counts, breaker state, payload byte counts, truncation status, and Anthropic request IDs.
- Modify: `pool/account.go`  
  Use breaker and sticky routing inside account selection without weakening the existing active-connection reservation.
- Modify: `proxy/handler_test.go`, `proxy/translator_test.go`, `pool/account_test.go`, `proxy/request_log_test.go`  
  Extend current tests rather than replacing them.

## Task 1: Anthropic Envelope And Request IDs

**Files:**
- Create: `proxy/anthropic_envelope.go`
- Create: `proxy/anthropic_envelope_test.go`
- Modify: `proxy/translator.go`
- Modify: `proxy/handler.go`
- Modify: `proxy/request_log.go`
- Test: `proxy/anthropic_envelope_test.go`
- Test: `proxy/handler_test.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Write failing envelope tests**

Add `proxy/anthropic_envelope_test.go`:

```go
package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseAnthropicEnvelopeCapturesBetaAndUnknownFields(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4.5",
		"max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"tool_reference":[{"type":"tool_reference","name":"mcp__fs__read_file","id":"toolref_1"}],
		"unknown_feature":{"enabled":true}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("anthropic-beta", "fine-grained-tool-streaming-2025-05-14,tool-search-2025-10-19")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-request-id", "client-req-1")

	env, err := parseAnthropicEnvelope(req, body)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Request.Model != "claude-sonnet-4.5" {
		t.Fatalf("unexpected model %q", env.Request.Model)
	}
	if !env.HasBeta("fine-grained-tool-streaming-2025-05-14") {
		t.Fatalf("expected fine-grained beta")
	}
	if !env.HasBetaPrefix("tool-search") {
		t.Fatalf("expected tool-search beta prefix")
	}
	if env.AnthropicVersion != "2023-06-01" {
		t.Fatalf("unexpected version %q", env.AnthropicVersion)
	}
	if len(env.Request.ToolReferences) != 1 {
		t.Fatalf("expected one tool_reference, got %#v", env.Request.ToolReferences)
	}
	if _, ok := env.Extra["unknown_feature"]; !ok {
		t.Fatalf("expected unknown_feature in Extra")
	}
	if env.ClientRequestID != "client-req-1" {
		t.Fatalf("unexpected request id %q", env.ClientRequestID)
	}
}

func TestWriteAnthropicRequestIDHeadersSetsBothNames(t *testing.T) {
	w := httptest.NewRecorder()
	env := &anthropicEnvelope{AnthropicRequestID: "req_test_123"}
	writeAnthropicRequestIDHeaders(w, env)
	if got := w.Header().Get("request-id"); got != "req_test_123" {
		t.Fatalf("request-id = %q", got)
	}
	if got := w.Header().Get("x-request-id"); got != "req_test_123" {
		t.Fatalf("x-request-id = %q", got)
	}
}

func TestClaudeRequestAcceptsToolReferences(t *testing.T) {
	var req ClaudeRequest
	if err := json.Unmarshal([]byte(`{
		"model":"claude-sonnet-4.5",
		"max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"tool_reference":[{"type":"tool_reference","name":"mcp__fs__read_file","id":"toolref_1","defer_loading":true}]
	}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.ToolReferences) != 1 {
		t.Fatalf("expected one tool reference, got %#v", req.ToolReferences)
	}
	if req.ToolReferences[0].Name != "mcp__fs__read_file" {
		t.Fatalf("unexpected tool reference name %q", req.ToolReferences[0].Name)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./proxy -run 'TestParseAnthropicEnvelope|TestWriteAnthropicRequestIDHeaders|TestClaudeRequestAcceptsToolReferences' -v
```

Expected: FAIL with undefined `parseAnthropicEnvelope`, `anthropicEnvelope`, `writeAnthropicRequestIDHeaders`, or missing `ToolReferences`.

- [ ] **Step 3: Add Claude tool-reference types**

Modify `proxy/translator.go` near `ClaudeRequest` and `ClaudeTool`:

```go
type ClaudeRequest struct {
	Model          string                 `json:"model"`
	Messages       []ClaudeMessage        `json:"messages"`
	MaxTokens      int                    `json:"max_tokens"`
	Temperature    float64                `json:"temperature,omitempty"`
	TopP           float64                `json:"top_p,omitempty"`
	Stream         bool                   `json:"stream,omitempty"`
	System         interface{}            `json:"system,omitempty"`
	Thinking       *ClaudeThinkingConfig  `json:"thinking,omitempty"`
	Tools          []ClaudeTool           `json:"tools,omitempty"`
	ToolReferences []ClaudeToolReference  `json:"tool_reference,omitempty"`
	ToolChoice     interface{}            `json:"tool_choice,omitempty"`
	Extra          map[string]interface{} `json:"-"`
}

type ClaudeToolReference struct {
	Type         string          `json:"type,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  interface{}     `json:"input_schema,omitempty"`
	DeferLoading bool            `json:"defer_loading,omitempty"`
	Raw          json.RawMessage `json:"-"`
}
```

Add `encoding/json` to the import list if `translator.go` does not already import it. Preserve existing struct fields and comments around these declarations.

- [ ] **Step 4: Add the envelope implementation**

Create `proxy/anthropic_envelope.go`:

```go
package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type anthropicEnvelope struct {
	Request            ClaudeRequest
	Extra              map[string]json.RawMessage
	AnthropicVersion   string
	BetaHeader         string
	Betas              map[string]bool
	ClientRequestID    string
	AnthropicRequestID string
	UserAgent          string
	SessionID          string
	AgentID            string
}

func parseAnthropicEnvelope(r *http.Request, body []byte) (*anthropicEnvelope, error) {
	var req ClaudeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	for _, key := range []string{
		"model", "messages", "max_tokens", "temperature", "top_p", "stream", "system",
		"thinking", "tools", "tool_reference", "tool_choice",
	} {
		delete(raw, key)
	}
	req.Extra = make(map[string]interface{}, len(raw))
	for key, value := range raw {
		var decoded interface{}
		if err := json.Unmarshal(value, &decoded); err == nil {
			req.Extra[key] = decoded
		}
	}
	requestID := strings.TrimSpace(r.Header.Get("request-id"))
	if requestID == "" {
		requestID = strings.TrimSpace(r.Header.Get("x-request-id"))
	}
	if requestID == "" {
		requestID = "req_" + uuid.New().String()
	}
	betaHeader := strings.TrimSpace(r.Header.Get("anthropic-beta"))
	return &anthropicEnvelope{
		Request:            req,
		Extra:              raw,
		AnthropicVersion:   strings.TrimSpace(r.Header.Get("anthropic-version")),
		BetaHeader:         betaHeader,
		Betas:              parseAnthropicBetas(betaHeader),
		ClientRequestID:    strings.TrimSpace(r.Header.Get("x-request-id")),
		AnthropicRequestID: requestID,
		UserAgent:          strings.TrimSpace(r.UserAgent()),
		SessionID:          firstNonEmptyHeader(r, "x-claude-code-session-id", "x-claude-session-id", "claude-code-session-id"),
		AgentID:            firstNonEmptyHeader(r, "x-claude-code-agent-id", "x-claude-agent-id"),
	}, nil
}

func parseAnthropicBetas(header string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[strings.ToLower(part)] = true
		}
	}
	return out
}

func (e *anthropicEnvelope) HasBeta(name string) bool {
	if e == nil {
		return false
	}
	return e.Betas[strings.ToLower(strings.TrimSpace(name))]
}

func (e *anthropicEnvelope) HasBetaPrefix(prefix string) bool {
	if e == nil {
		return false
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for beta := range e.Betas {
		if strings.HasPrefix(beta, prefix) {
			return true
		}
	}
	return false
}

func writeAnthropicRequestIDHeaders(w http.ResponseWriter, env *anthropicEnvelope) {
	requestID := ""
	if env != nil {
		requestID = strings.TrimSpace(env.AnthropicRequestID)
	}
	if requestID == "" {
		requestID = "req_" + uuid.New().String()
	}
	w.Header().Set("request-id", requestID)
	w.Header().Set("x-request-id", requestID)
}

func firstNonEmptyHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
```

- [ ] **Step 5: Wire the envelope into Claude handlers**

Modify `proxy/handler.go` in `handleClaudeMessagesInternal`:

```go
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", "Failed to read request body")
		return
	}

	env, err := parseAnthropicEnvelope(r, body)
	if err != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", "Invalid JSON: "+err.Error())
		return
	}
	writeAnthropicRequestIDHeaders(w, env)
	req := env.Request
	updateRequestLogAnthropic(r, env)
	updateRequestLogMetadata(r, req.Model, req.Stream)
```

Remove the old direct `json.Unmarshal(body, &req)` block from this function. Keep the rest of the function flow unchanged.

- [ ] **Step 6: Store envelope metadata in request logs**

Modify `proxy/request_log.go`:

```go
type RequestLogEntry struct {
	// keep existing fields
	AnthropicRequestID string   `json:"anthropicRequestId,omitempty"`
	AnthropicVersion   string   `json:"anthropicVersion,omitempty"`
	AnthropicBetas     []string `json:"anthropicBetas,omitempty"`
	ToolReferenceCount int      `json:"toolReferenceCount,omitempty"`
}
```

Add:

```go
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
}

func sortedAnthropicBetas(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for beta := range in {
		out = append(out, beta)
	}
	sort.Strings(out)
	return out
}
```

Add `sort` to imports. If `RequestLogEntry` field ordering differs, add these fields near the existing request ID/session fields.

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestParseAnthropicEnvelope|TestWriteAnthropicRequestIDHeaders|TestClaudeRequestAcceptsToolReferences|TestRequestLog' -v
```

Expected: PASS.

- [ ] **Step 8: Run package tests and commit**

Run:

```bash
go test ./proxy ./pool ./config
```

Expected: PASS.

Commit:

```bash
git add proxy/anthropic_envelope.go proxy/anthropic_envelope_test.go proxy/translator.go proxy/handler.go proxy/request_log.go proxy/request_log_test.go proxy/handler_test.go
git commit -m "feat: add anthropic compatibility envelope"
```

## Task 2: Claude SSE Conformance Writer

**Files:**
- Create: `proxy/claude_sse_writer.go`
- Create: `proxy/claude_sse_writer_test.go`
- Modify: `proxy/handler.go`
- Test: `proxy/claude_sse_writer_test.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing SSE writer tests**

Create `proxy/claude_sse_writer_test.go`:

```go
package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeSSEWriterOrdersTextEvents(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 4096)
	writer.TextDelta("hello")
	writer.Stop("end_turn", buildClaudeUsageMap(10, 1, promptCacheUsage{}, false))

	body := w.Body.String()
	mustContainInOrder(t, body,
		"event: message_start",
		"event: content_block_start",
		`"type":"text"`,
		"event: content_block_delta",
		`"text":"hello"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		"event: message_stop",
	)
}

func TestClaudeSSEWriterChunksToolInput(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 8)
	writer.ToolUse(KiroToolUse{ToolUseID: "toolu_1", Name: "readFile", Input: map[string]interface{}{"path": strings.Repeat("a", 24)}})
	writer.Stop("tool_use", buildClaudeUsageMap(10, 2, promptCacheUsage{}, false))

	if got := strings.Count(w.Body.String(), `"input_json_delta"`); got < 2 {
		t.Fatalf("expected chunked input_json_delta events, got %d body=%s", got, w.Body.String())
	}
	mustContainInOrder(t, w.Body.String(),
		`"type":"tool_use"`,
		`"id":"toolu_1"`,
		`"name":"readFile"`,
		`"stop_reason":"tool_use"`,
	)
}

func TestClaudeSSEWriterPingAndError(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 4096)
	writer.Ping()
	writer.Error("overloaded_error", "upstream reset")
	body := w.Body.String()
	mustContainInOrder(t, body, "event: ping", `"type":"ping"`, "event: error", `"type":"overloaded_error"`)
}

func mustContainInOrder(t *testing.T, body string, parts ...string) {
	t.Helper()
	pos := 0
	for _, part := range parts {
		idx := strings.Index(body[pos:], part)
		if idx < 0 {
			t.Fatalf("missing %q after offset %d in body:\n%s", part, pos, body)
		}
		pos += idx + len(part)
	}
}

func TestClaudeSSEWriterEventsAreJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(1, 0, promptCacheUsage{}, false), 4096)
	writer.TextDelta("ok")
	writer.Stop("end_turn", buildClaudeUsageMap(1, 1, promptCacheUsage{}, false))
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var v interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
			t.Fatalf("invalid json line %q: %v", line, err)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./proxy -run 'TestClaudeSSEWriter' -v
```

Expected: FAIL with undefined `newClaudeSSEWriter`.

- [ ] **Step 3: Implement the SSE writer**

Create `proxy/claude_sse_writer.go`:

```go
package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type claudeSSEWriter struct {
	w              http.ResponseWriter
	flusher        http.Flusher
	messageID      string
	model          string
	startUsage     map[string]interface{}
	toolChunkBytes int
	started        bool
	stopped        bool
	nextIndex      int
	activeIndex    int
	activeType     string
}

func newClaudeSSEWriter(w http.ResponseWriter, messageID, model string, startUsage map[string]interface{}, toolChunkBytes int) *claudeSSEWriter {
	if toolChunkBytes <= 0 {
		toolChunkBytes = 4096
	}
	flusher, _ := w.(http.Flusher)
	return &claudeSSEWriter{
		w:              w,
		flusher:        flusher,
		messageID:      messageID,
		model:          model,
		startUsage:     startUsage,
		toolChunkBytes: toolChunkBytes,
		activeIndex:    -1,
	}
}

func (s *claudeSSEWriter) Start() {
	if s.started {
		return
	}
	s.started = true
	s.write("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         s.startUsage,
		},
	})
}

func (s *claudeSSEWriter) TextDelta(text string) {
	if text == "" || s.stopped {
		return
	}
	s.startBlock("text", map[string]string{"type": "text", "text": ""})
	s.write("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.activeIndex,
		"delta": map[string]string{"type": "text_delta", "text": text},
	})
}

func (s *claudeSSEWriter) ThinkingDelta(text string) {
	if text == "" || s.stopped {
		return
	}
	s.startBlock("thinking", map[string]string{"type": "thinking", "thinking": ""})
	s.write("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.activeIndex,
		"delta": map[string]string{"type": "thinking_delta", "thinking": text},
	})
}

func (s *claudeSSEWriter) ToolUse(tu KiroToolUse) {
	if s.stopped {
		return
	}
	s.closeBlock()
	s.Start()
	idx := s.nextIndex
	s.nextIndex++
	s.write("content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": idx,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    tu.ToolUseID,
			"name":  tu.Name,
			"input": map[string]interface{}{},
		},
	})
	inputJSON, _ := json.Marshal(tu.Input)
	for _, chunk := range chunkStringForSSE(string(inputJSON), s.toolChunkBytes) {
		s.write("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]interface{}{
				"type":         "input_json_delta",
				"partial_json": chunk,
			},
		})
	}
	s.write("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": idx})
}

func (s *claudeSSEWriter) Ping() {
	if s.stopped {
		return
	}
	s.write("ping", map[string]string{"type": "ping"})
}

func (s *claudeSSEWriter) Error(errType, message string) {
	if s.stopped {
		return
	}
	s.write("error", map[string]interface{}{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
	s.stopped = true
}

func (s *claudeSSEWriter) Stop(stopReason string, usage map[string]interface{}) {
	if s.stopped {
		return
	}
	s.closeBlock()
	s.Start()
	s.write("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason},
		"usage": usage,
	})
	s.write("message_stop", map[string]string{"type": "message_stop"})
	s.stopped = true
}

func (s *claudeSSEWriter) startBlock(blockType string, block interface{}) {
	if s.activeType == blockType {
		return
	}
	s.closeBlock()
	s.Start()
	idx := s.nextIndex
	s.nextIndex++
	s.write("content_block_start", map[string]interface{}{
		"type":          "content_block_start",
		"index":         idx,
		"content_block": block,
	})
	s.activeIndex = idx
	s.activeType = blockType
}

func (s *claudeSSEWriter) closeBlock() {
	if s.activeIndex < 0 {
		return
	}
	s.write("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": s.activeIndex})
	s.activeIndex = -1
	s.activeType = ""
}

func (s *claudeSSEWriter) write(event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func chunkStringForSSE(value string, maxBytes int) []string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return []string{value}
	}
	chunks := make([]string, 0, len(value)/maxBytes+1)
	for len(value) > maxBytes {
		chunks = append(chunks, value[:maxBytes])
		value = value[maxBytes:]
	}
	if value != "" {
		chunks = append(chunks, value)
	}
	return chunks
}
```

- [ ] **Step 4: Replace direct Claude stream writes**

Modify `proxy/handler.go` inside `handleClaudeStreamAttempt`:

- Create the writer after `msgID` is created:

```go
sse := newClaudeSSEWriter(w, msgID, model, buildClaudeUsageMap(startInputTokens, 0, cacheUsage, cacheProfile != nil), 4096)
```

- Replace `startMessage`, `closeActiveBlock`, and `startContentBlock` direct `h.sendSSE` paths with calls to:

```go
sse.Start()
sse.TextDelta(text)
sse.ThinkingDelta(text)
sse.ToolUse(tu)
sse.Stop(stopReason, buildClaudeUsageMap(inputTokens, outputTokens, cacheUsage, cacheProfile != nil))
```

- Replace stream error after start:

```go
_, errType := claudeUpstreamErrorStatusAndType(err)
sse.Error(errType, err.Error())
```

Keep the thinking-tag parsing logic, raw content builders, latency tracking, and accounting code unchanged. The only intended behavior change in this task is event emission.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestClaudeSSEWriter|TestHandleClaude' -v
```

Expected: PASS.

- [ ] **Step 6: Run package tests and commit**

Run:

```bash
go test ./proxy
```

Expected: PASS.

Commit:

```bash
git add proxy/claude_sse_writer.go proxy/claude_sse_writer_test.go proxy/handler.go proxy/handler_test.go
git commit -m "feat: add claude sse conformance writer"
```

## Task 3: Tool Reference Acceptance And Tool Name Fidelity

**Files:**
- Modify: `proxy/translator.go`
- Modify: `proxy/translator_test.go`
- Modify: `proxy/handler_test.go`
- Test: `proxy/translator_test.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing translator tests**

Add to `proxy/translator_test.go`:

```go
func TestClaudeToKiroExpandsToolReferencesWithSchema(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 64,
		Messages: []ClaudeMessage{{Role: "user", Content: "read the file"}},
		ToolReferences: []ClaudeToolReference{{
			Type:        "tool_reference",
			ID:          "toolref_1",
			Name:        "mcp__filesystem__read_file",
			Description: "Read a file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"path"},
			},
		}},
	}
	payload := ClaudeToKiro(req, false)
	if len(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools) != 1 {
		t.Fatalf("expected one Kiro tool, got %#v", payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)
	}
	tool := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools[0]
	if tool.ToolSpecification.Name == "mcp__filesystem__read_file" {
		t.Fatalf("expected Kiro-safe sanitized name")
	}
	if got := payload.ToolNameMap[tool.ToolSpecification.Name]; got != "mcp__filesystem__read_file" {
		t.Fatalf("expected outward name mapping, got %q", got)
	}
}

func TestClaudeToKiroIgnoresDeferredToolReferenceWithoutSchema(t *testing.T) {
	req := &ClaudeRequest{
		Model:          "claude-sonnet-4.5",
		MaxTokens:      64,
		Messages:       []ClaudeMessage{{Role: "user", Content: "hi"}},
		ToolReferences: []ClaudeToolReference{{Type: "tool_reference", ID: "toolref_1", Name: "mcp__late__tool", DeferLoading: true}},
	}
	payload := ClaudeToKiro(req, false)
	if payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext != nil &&
		len(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools) != 0 {
		t.Fatalf("expected deferred unresolved reference to be accepted but not converted")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroExpandsToolReferences|TestClaudeToKiroIgnoresDeferredToolReference' -v
```

Expected: FAIL because `ToolReferences` are not converted.

- [ ] **Step 3: Implement tool-reference conversion**

Modify `proxy/translator.go`. Add:

```go
func mergeClaudeToolsAndReferences(tools []ClaudeTool, refs []ClaudeToolReference) []ClaudeTool {
	out := append([]ClaudeTool(nil), tools...)
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		if name == "" || ref.InputSchema == nil {
			continue
		}
		desc := strings.TrimSpace(ref.Description)
		if desc == "" {
			desc = strings.TrimSpace(ref.Title)
		}
		if desc == "" {
			desc = "Claude Code tool reference " + name
		}
		out = append(out, ClaudeTool{
			Type:        ref.Type,
			Name:        name,
			Description: desc,
			InputSchema: ref.InputSchema,
		})
	}
	return out
}
```

Change in `ClaudeToKiro`:

```go
kiroTools, toolNameMap := convertClaudeTools(mergeClaudeToolsAndReferences(req.Tools, req.ToolReferences))
```

Keep unresolved deferred references accepted and ignored. Do not emit an error in `ClaudeToKiro`, because validation belongs in the handler if a later task has enough real Claude Code fixture evidence to reject a shape.

- [ ] **Step 4: Ensure outward names survive stream tool-use callbacks**

Verify `proxy/kiro.go` already rewrites Kiro tool names through `payload.ToolNameMap` before invoking `OnToolUse`. If missing, add this in the callback setup around tool-use handling:

```go
if payload != nil && len(payload.ToolNameMap) > 0 {
	if original, ok := payload.ToolNameMap[toolUse.Name]; ok && original != "" {
		toolUse.Name = original
	}
}
```

Add a targeted test in `proxy/kiro_test.go` only if the mapping is missing.

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroExpandsToolReferences|TestClaudeToKiroIgnoresDeferredToolReference|TestHandleClaude' -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
go test ./proxy
```

Expected: PASS.

Commit:

```bash
git add proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/kiro_test.go proxy/handler_test.go
git commit -m "feat: accept claude tool references"
```

## Task 4: Per-Model Breaker And Sticky Account Routing

**Files:**
- Create: `pool/breaker.go`
- Create: `pool/breaker_test.go`
- Modify: `pool/account.go`
- Modify: `pool/account_test.go`
- Modify: `proxy/handler.go`
- Modify: `proxy/request_log.go`
- Test: `pool/breaker_test.go`
- Test: `pool/account_test.go`

- [ ] **Step 1: Write failing breaker tests**

Create `pool/breaker_test.go`:

```go
package pool

import (
	"testing"
	"time"
)

func TestModelBreakerOpenHalfOpenClose(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newModelBreakerState()
	b.open("acct-1", "claude-opus-4.7", FailureReasonUpstream5xx, now, 30*time.Second)
	if b.canUse("acct-1", "claude-opus-4.7", now.Add(10*time.Second)) {
		t.Fatalf("expected account blocked while breaker is open")
	}
	if !b.canProbe("acct-1", "claude-opus-4.7", now.Add(31*time.Second)) {
		t.Fatalf("expected half-open probe after backoff")
	}
	b.success("acct-1", "claude-opus-4.7")
	if !b.canUse("acct-1", "claude-opus-4.7", now.Add(32*time.Second)) {
		t.Fatalf("expected account usable after success")
	}
}

func TestStickyAccountEscapesWhenBreakerOpen(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newModelBreakerState()
	b.rememberSticky("session-1", "claude-sonnet-4.5", "acct-1", now)
	b.open("acct-1", "claude-sonnet-4.5", FailureReasonRateLimited, now, time.Minute)
	if got := b.stickyAccount("session-1", "claude-sonnet-4.5", now.Add(5*time.Second)); got != "" {
		t.Fatalf("expected sticky account escaped while open, got %q", got)
	}
	b.rememberSticky("session-1", "claude-sonnet-4.5", "acct-2", now.Add(6*time.Second))
	if got := b.stickyAccount("session-1", "claude-sonnet-4.5", now.Add(7*time.Second)); got != "acct-2" {
		t.Fatalf("expected acct-2 sticky account, got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./pool -run 'TestModelBreaker' -v
```

Expected: FAIL with undefined breaker state.

- [ ] **Step 3: Implement breaker state**

Create `pool/breaker.go`:

```go
package pool

import (
	"strings"
	"time"
)

type breakerStatus string

const (
	breakerClosed   breakerStatus = "closed"
	breakerOpen     breakerStatus = "open"
	breakerHalfOpen breakerStatus = "half_open"
)

type modelBreakerState struct {
	entries map[string]*breakerEntry
	sticky  map[string]stickyEntry
}

type breakerEntry struct {
	Status  breakerStatus
	Reason  FailureReason
	OpenAt  time.Time
	RetryAt time.Time
	Probing bool
}

type stickyEntry struct {
	AccountID string
	UpdatedAt time.Time
}

func newModelBreakerState() *modelBreakerState {
	return &modelBreakerState{
		entries: map[string]*breakerEntry{},
		sticky:  map[string]stickyEntry{},
	}
}

func breakerKey(accountID, model string) string {
	return strings.TrimSpace(accountID) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func stickyKey(sessionKey, model string) string {
	return strings.TrimSpace(sessionKey) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func (b *modelBreakerState) canUse(accountID, model string, now time.Time) bool {
	e := b.entries[breakerKey(accountID, model)]
	if e == nil || e.Status == breakerClosed {
		return true
	}
	return now.After(e.RetryAt) && !e.Probing
}

func (b *modelBreakerState) canProbe(accountID, model string, now time.Time) bool {
	e := b.entries[breakerKey(accountID, model)]
	return e != nil && e.Status == breakerOpen && now.After(e.RetryAt) && !e.Probing
}

func (b *modelBreakerState) markProbe(accountID, model string, now time.Time) {
	key := breakerKey(accountID, model)
	e := b.entries[key]
	if e == nil {
		return
	}
	e.Status = breakerHalfOpen
	e.Probing = true
}

func (b *modelBreakerState) open(accountID, model string, reason FailureReason, now time.Time, delay time.Duration) {
	if delay <= 0 {
		delay = 30 * time.Second
	}
	b.entries[breakerKey(accountID, model)] = &breakerEntry{
		Status:  breakerOpen,
		Reason:  reason,
		OpenAt:  now,
		RetryAt: now.Add(delay),
	}
}

func (b *modelBreakerState) success(accountID, model string) {
	delete(b.entries, breakerKey(accountID, model))
}

func (b *modelBreakerState) rememberSticky(sessionKeyValue, model, accountID string, now time.Time) {
	if strings.TrimSpace(sessionKeyValue) == "" || strings.TrimSpace(accountID) == "" {
		return
	}
	b.sticky[stickyKey(sessionKeyValue, model)] = stickyEntry{AccountID: accountID, UpdatedAt: now}
}

func (b *modelBreakerState) stickyAccount(sessionKeyValue, model string, now time.Time) string {
	e, ok := b.sticky[stickyKey(sessionKeyValue, model)]
	if !ok || now.Sub(e.UpdatedAt) > 30*time.Minute {
		return ""
	}
	if !b.canUse(e.AccountID, model, now) {
		return ""
	}
	return e.AccountID
}
```

- [ ] **Step 4: Wire breaker into account pool state**

Modify `pool/account.go`:

```go
type AccountPool struct {
	// keep existing fields
	breakers *modelBreakerState
}
```

In `ensureStateLocked` add:

```go
if p.breakers == nil {
	p.breakers = newModelBreakerState()
}
```

Add methods:

```go
func (p *AccountPool) RememberSticky(sessionKey, model, accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.breakers.rememberSticky(sessionKey, model, accountID, time.Now())
}

func (p *AccountPool) RecordModelSuccess(accountID, model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.breakers.success(accountID, model)
}

func (p *AccountPool) RecordModelFailure(accountID, model string, reason FailureReason, retryAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	delay := 30 * time.Second
	now := time.Now()
	if retryAt.After(now) {
		delay = retryAt.Sub(now)
	}
	switch reason {
	case FailureReasonRateLimited:
		if !retryAt.After(now) {
			delay = time.Minute
		}
	case FailureReasonAuthExpired:
		delay = 10 * time.Minute
	case FailureReasonQuotaExhausted, FailureReasonSuspended:
		delay = time.Hour
	case FailureReasonTransientNetwork, FailureReasonUpstream5xx:
		delay = 30 * time.Second
	}
	p.breakers.open(accountID, model, reason, now, delay)
}
```

Add a selector variant:

```go
func (p *AccountPool) BeginNextForModelSessionExcept(model, sessionKey string, excluded map[string]bool) (*config.Account, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	now := time.Now()
	if sticky := p.breakers.stickyAccount(sessionKey, model, now); sticky != "" {
		for i := range p.accounts {
			if p.accounts[i].ID == sticky && (excluded == nil || !excluded[sticky]) && p.accountHasModel(sticky, model) {
				acc := &p.accounts[i]
				health := p.runtimeHealthForLocked(acc.ID)
				health.activeConnections++
				health.lastUpdatedAt = now.Unix()
				return acc, p.releaseAccountRequestFunc(acc.ID)
			}
		}
	}
	acc := p.getNextForModelExceptLocked(model, excluded)
	if acc == nil {
		return nil, func() {}
	}
	health := p.runtimeHealthForLocked(acc.ID)
	health.activeConnections++
	health.lastUpdatedAt = now.Unix()
	p.breakers.rememberSticky(sessionKey, model, acc.ID, now)
	return acc, p.releaseAccountRequestFunc(acc.ID)
}

func (p *AccountPool) releaseAccountRequestFunc(accountID string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.ensureStateLocked()
			health := p.runtimeHealthForLocked(accountID)
			if health.activeConnections > 0 {
				health.activeConnections--
			}
			health.lastUpdatedAt = time.Now().Unix()
		})
	}
}
```

Refactor `BeginNextForModelExcept` to call `BeginNextForModelSessionExcept(model, "", excluded)`.

- [ ] **Step 5: Skip open breaker candidates in selection**

Inside `getNextForModelExceptLocked`, before assigning `best`, add:

```go
if p.breakers != nil && !p.breakers.canUse(acc.ID, model, now) {
	seen[acc.ID] = true
	continue
}
```

Apply this in both the primary and fallback loops.

- [ ] **Step 6: Wire session key from handler**

In `proxy/handler.go`, derive a sticky key in `handleClaudeWithAccountRetry`:

```go
sessionKey := requestStickyKey(r, effectiveReq)
account, releaseRequest := h.pool.BeginNextForModelSessionExcept(model, sessionKey, used)
```

Add helper in `proxy/handler.go` or a new small file:

```go
func requestStickyKey(r *http.Request, req *ClaudeRequest) string {
	if r != nil {
		for _, name := range []string{"x-claude-code-session-id", "x-claude-session-id", "x-request-id", "request-id"} {
			if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
				return value
			}
		}
	}
	if req != nil {
		model := MapModel(req.Model)
		return buildConversationID(model, buildClaudeSystemPrompt(req.System, false), firstClaudeConversationAnchor(req.Messages))
	}
	return ""
}
```

On success:

```go
h.pool.RecordModelSuccess(account.ID, model)
```

On account failure:

```go
h.pool.RecordModelFailure(account.ID, model, classifyFailureReason(err), rateLimitResetFromError(err))
```

Implement `rateLimitResetFromError(err error) time.Time` by checking the existing Kiro error type used by `RecordFailureUntil`; if unavailable, return `time.Time{}`.

- [ ] **Step 7: Run tests**

Run:

```bash
go test ./pool -run 'TestModelBreaker|TestBeginNextForModel' -v
go test ./proxy -run 'TestHandleClaude' -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
go test ./pool ./proxy
```

Expected: PASS.

Commit:

```bash
git add pool/breaker.go pool/breaker_test.go pool/account.go pool/account_test.go proxy/handler.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: add model breaker and sticky routing"
```

## Task 5: Payload Guard And Truncation Recovery

**Files:**
- Create: `proxy/payload_guard.go`
- Create: `proxy/payload_guard_test.go`
- Modify: `proxy/translator.go`
- Modify: `proxy/handler.go`
- Modify: `proxy/request_log.go`
- Test: `proxy/payload_guard_test.go`
- Test: `proxy/translator_test.go`

- [ ] **Step 1: Write failing payload guard tests**

Create `proxy/payload_guard_test.go`:

```go
package proxy

import (
	"strings"
	"testing"
)

func TestGuardKiroPayloadTrimsPairwiseWithoutOrphans(t *testing.T) {
	payload := ClaudeToKiro(&ClaudeRequest{
		Model: "claude-sonnet-4.5",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "start"},
			{Role: "assistant", Content: []ClaudeContentBlock{{Type: "tool_use", ID: "toolu_old", Name: "readFile", Input: map[string]interface{}{"path": "old"}}}},
			{Role: "user", Content: []ClaudeContentBlock{{Type: "tool_result", ToolUseID: "toolu_old", Content: strings.Repeat("x", 4096)}}},
			{Role: "user", Content: "current"},
		},
		MaxTokens: 64,
	}, false)
	result, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 512, HardLimitBytes: 2048})
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected trimming")
	}
	if hasOrphanedKiroToolMessages(payload.ConversationState.History) {
		t.Fatalf("expected no orphan tool messages: %#v", payload.ConversationState.History)
	}
	if result.FinalBytes > 2048 {
		t.Fatalf("payload remains over hard limit: %d", result.FinalBytes)
	}
}

func TestGuardKiroPayloadRejectsOversizedCurrentToolResult(t *testing.T) {
	payload := ClaudeToKiro(&ClaudeRequest{
		Model: "claude-sonnet-4.5",
		Messages: []ClaudeMessage{
			{Role: "user", Content: []ClaudeContentBlock{{Type: "tool_result", ToolUseID: "toolu_now", Content: strings.Repeat("x", 8192)}}},
		},
		MaxTokens: 64,
	}, false)
	_, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 512, HardLimitBytes: 2048})
	if err == nil {
		t.Fatalf("expected invalid payload error")
	}
	if !strings.Contains(err.Error(), "current tool_result") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyTruncationRecoveryNote(t *testing.T) {
	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		Messages:  []ClaudeMessage{{Role: "user", Content: "continue"}},
		MaxTokens: 64,
	}, false)
	applyTruncationRecoveryNote(payload, "previous history was trimmed")
	if !strings.Contains(payload.ConversationState.CurrentMessage.UserInputMessage.Content, "previous history was trimmed") {
		t.Fatalf("expected recovery note in current content")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./proxy -run 'TestGuardKiroPayload|TestApplyTruncationRecoveryNote' -v
```

Expected: FAIL with undefined guard functions.

- [ ] **Step 3: Implement payload guard**

Create `proxy/payload_guard.go`:

```go
package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
)

type payloadGuardOptions struct {
	SoftLimitBytes int
	HardLimitBytes int
}

type payloadGuardResult struct {
	OriginalBytes int
	FinalBytes    int
	Trimmed       bool
	TrimmedCount  int
	RecoveryNote  string
}

func defaultPayloadGuardOptions() payloadGuardOptions {
	return payloadGuardOptions{SoftLimitBytes: maxKiroHistoryPayloadBytes, HardLimitBytes: maxKiroHistoryPayloadBytes + 256*1024}
}

func guardKiroPayload(payload *KiroPayload, opts payloadGuardOptions) (payloadGuardResult, error) {
	if opts.SoftLimitBytes <= 0 {
		opts.SoftLimitBytes = maxKiroHistoryPayloadBytes
	}
	if opts.HardLimitBytes <= opts.SoftLimitBytes {
		opts.HardLimitBytes = opts.SoftLimitBytes + 256*1024
	}
	result := payloadGuardResult{OriginalBytes: kiroPayloadJSONSize(payload)}
	if result.OriginalBytes <= opts.SoftLimitBytes {
		result.FinalBytes = result.OriginalBytes
		return result, nil
	}
	if currentToolResultsSize(payload) > opts.HardLimitBytes/2 {
		return result, fmt.Errorf("current tool_result content is too large for Kiro payload")
	}
	for len(payload.ConversationState.History) > 0 && kiroPayloadJSONSize(payload) > opts.SoftLimitBytes {
		before := len(payload.ConversationState.History)
		payload.ConversationState.History = trimOldestKiroHistoryPair(payload.ConversationState.History)
		payload.ConversationState.History = dropOrphanedKiroToolMessages(payload.ConversationState.History)
		if len(payload.ConversationState.History) == before {
			break
		}
		result.Trimmed = true
		result.TrimmedCount += before - len(payload.ConversationState.History)
	}
	result.FinalBytes = kiroPayloadJSONSize(payload)
	if result.FinalBytes > opts.HardLimitBytes {
		return result, fmt.Errorf("Kiro payload remains too large after trimming: %d bytes", result.FinalBytes)
	}
	if result.Trimmed {
		result.RecoveryNote = "Some earlier conversation history was trimmed before sending this turn to the upstream model."
	}
	return result, nil
}

func kiroPayloadJSONSize(payload *KiroPayload) int {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	return len(data)
}

func currentToolResultsSize(payload *KiroPayload) int {
	if payload == nil ||
		payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return 0
	}
	total := 0
	for _, result := range payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults {
		data, _ := json.Marshal(result)
		total += len(data)
	}
	return total
}

func trimOldestKiroHistoryPair(history []KiroHistoryMessage) []KiroHistoryMessage {
	if len(history) == 0 {
		return history
	}
	removeCount := 1
	if historyMessageHasToolUses(history[0]) && len(history) > 1 && historyMessageHasToolResults(history[1]) {
		removeCount = 2
	}
	if removeCount > len(history) {
		removeCount = len(history)
	}
	return append([]KiroHistoryMessage(nil), history[removeCount:]...)
}

func hasOrphanedKiroToolMessages(history []KiroHistoryMessage) bool {
	cleaned := dropOrphanedKiroToolMessages(append([]KiroHistoryMessage(nil), history...))
	return len(cleaned) != len(history)
}

func applyTruncationRecoveryNote(payload *KiroPayload, note string) {
	note = strings.TrimSpace(note)
	if payload == nil || note == "" {
		return
	}
	current := &payload.ConversationState.CurrentMessage.UserInputMessage
	current.Content = "--- CONTEXT NOTICE ---\n" + note + "\n--- END CONTEXT NOTICE ---\n\n" + current.Content
}
```

- [ ] **Step 4: Wire guard into Claude handler before account selection**

In `proxy/handler.go`, after `kiroPayload := ClaudeToKiro(&req, thinking)` and before `handleClaudeWithAccountRetry`, add:

```go
guardResult, guardErr := guardKiroPayload(kiroPayload, defaultPayloadGuardOptions())
updateRequestLogPayload(r, guardResult)
if guardErr != nil {
	h.sendClaudeError(w, 400, "invalid_request_error", guardErr.Error())
	return
}
if guardResult.RecoveryNote != "" {
	applyTruncationRecoveryNote(kiroPayload, guardResult.RecoveryNote)
}
```

Apply equivalent guard wiring to OpenAI Chat and Responses payload paths only if they route through Kiro payloads and can reuse the same helper without changing response semantics. Otherwise keep this task scoped to Claude and add a follow-up comment in the plan execution notes.

- [ ] **Step 5: Add request-log payload fields**

Modify `proxy/request_log.go`:

```go
type RequestLogEntry struct {
	// keep existing fields
	PayloadOriginalBytes int  `json:"payloadOriginalBytes,omitempty"`
	PayloadFinalBytes    int  `json:"payloadFinalBytes,omitempty"`
	PayloadTrimmed       bool `json:"payloadTrimmed,omitempty"`
	PayloadTrimmedCount  int  `json:"payloadTrimmedCount,omitempty"`
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
}
```

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./proxy -run 'TestGuardKiroPayload|TestApplyTruncationRecoveryNote|TestClaudeToKiro' -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
go test ./proxy
```

Expected: PASS.

Commit:

```bash
git add proxy/payload_guard.go proxy/payload_guard_test.go proxy/handler.go proxy/request_log.go proxy/request_log_test.go proxy/translator.go proxy/translator_test.go
git commit -m "feat: guard kiro payload size"
```

## Task 6: `/v1/models`, Error Headers, And Compatibility Fixtures

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`
- Modify: `proxy/kiro.go`
- Modify: `proxy/request_log.go`
- Create: `proxy/testdata/claude_code_basic_message.json`
- Create: `proxy/testdata/claude_code_tool_reference_message.json`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing compatibility tests**

Add to `proxy/handler_test.go`:

```go
func TestAnthropicModelsResponseIncludesAliasesWithoutExtraFields(t *testing.T) {
	models := buildAnthropicModelsResponse([]ModelInfo{{ModelId: "claude-sonnet-4.5", ModelName: "Claude Sonnet", InputTypes: []string{"text"}}}, "-thinking")
	if len(models) == 0 {
		t.Fatalf("expected models")
	}
	for _, model := range models {
		if _, ok := model["id"]; !ok {
			t.Fatalf("missing id in %#v", model)
		}
		if _, ok := model["object"]; !ok {
			t.Fatalf("missing object in %#v", model)
		}
		if _, ok := model["owned_by"]; !ok {
			t.Fatalf("missing owned_by in %#v", model)
		}
		if _, ok := model["supports_image"]; ok {
			t.Fatalf("public Anthropic model response should not include non-Anthropic supports_image: %#v", model)
		}
	}
}

func TestClaudeErrorSetsRequestIDAndRetryAfter(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"bad":`))
	env := &anthropicEnvelope{AnthropicRequestID: "req_test_123"}
	writeAnthropicRequestIDHeaders(w, env)
	h.sendClaudeErrorWithHeaders(w, 429, "rate_limit_error", "rate limited", map[string]string{"Retry-After": "2"})
	if got := w.Header().Get("request-id"); got != "req_test_123" {
		t.Fatalf("request-id = %q", got)
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q", got)
	}
	_ = req
}
```

Create fixture `proxy/testdata/claude_code_tool_reference_message.json`:

```json
{
  "model": "claude-sonnet-4.5",
  "max_tokens": 64,
  "stream": true,
  "messages": [
    {
      "role": "user",
      "content": "Use the filesystem tool if needed."
    }
  ],
  "tool_reference": [
    {
      "type": "tool_reference",
      "id": "toolref_fs_read",
      "name": "mcp__filesystem__read_file",
      "description": "Read a file from disk",
      "input_schema": {
        "type": "object",
        "properties": {
          "path": {
            "type": "string"
          }
        },
        "required": ["path"]
      }
    }
  ]
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./proxy -run 'TestAnthropicModelsResponseIncludesAliasesWithoutExtraFields|TestClaudeErrorSetsRequestIDAndRetryAfter' -v
```

Expected: FAIL for non-Anthropic model fields or missing `sendClaudeErrorWithHeaders`.

- [ ] **Step 3: Harden model response**

Modify `buildAnthropicModelsResponse` in `proxy/handler.go` to return Anthropic-shaped public fields only:

```go
item := map[string]interface{}{
	"id":          modelID,
	"type":        "model",
	"object":      "model",
	"display_name": displayName,
	"created_at":  0,
	"owned_by":    "anthropic",
}
```

Do not include `supports_image` or local-only capability fields in `/v1/models`. Keep those fields only in admin APIs if they currently exist there.

- [ ] **Step 4: Add error helper with extra headers**

Modify `proxy/handler.go`:

```go
func (h *Handler) sendClaudeErrorWithHeaders(w http.ResponseWriter, status int, errType, message string, headers map[string]string) {
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			w.Header().Set(key, value)
		}
	}
	h.sendClaudeError(w, status, errType, message)
}
```

Use this helper in paths that already know `Retry-After`, especially rate-limit and overload paths. Preserve existing `sendClaudeError` for simple validation failures.

- [ ] **Step 5: Add fixture loader test**

Add to `proxy/handler_test.go`:

```go
func TestClaudeCodeToolReferenceFixtureParses(t *testing.T) {
	body, err := os.ReadFile("testdata/claude_code_tool_reference_message.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("anthropic-beta", "tool-search-2025-10-19")
	env, err := parseAnthropicEnvelope(req, body)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(env.Request.ToolReferences) != 1 {
		t.Fatalf("expected tool_reference fixture to parse")
	}
}
```

Add `os` to the test imports if needed.

- [ ] **Step 6: Run tests and commit**

Run:

```bash
go test ./proxy -run 'TestAnthropicModelsResponse|TestClaudeErrorSetsRequestIDAndRetryAfter|TestClaudeCodeToolReferenceFixtureParses' -v
go test ./proxy
```

Expected: PASS.

Commit:

```bash
git add proxy/handler.go proxy/handler_test.go proxy/testdata/claude_code_tool_reference_message.json proxy/request_log.go proxy/kiro.go
git commit -m "feat: harden anthropic model and error compatibility"
```

## Task 7: End-To-End Verification And Production UAT Notes

**Files:**
- Modify: `docs/superpowers/uat/2026-05-16-claude-code-parity-layer-uat.md`
- Test: all touched packages

- [ ] **Step 1: Run full automated verification**

Run:

```bash
go test ./...
go build ./...
```

Expected: both commands complete with exit code 0.

- [ ] **Step 2: Run targeted compatibility suites**

Run:

```bash
go test ./proxy -run 'TestParseAnthropicEnvelope|TestClaudeSSEWriter|TestClaudeToKiroExpandsToolReferences|TestGuardKiroPayload|TestAnthropicModelsResponse|TestClaudeCodeToolReferenceFixtureParses' -v
go test ./pool -run 'TestModelBreaker|TestBeginNextForModel' -v
```

Expected: PASS.

- [ ] **Step 3: Create UAT record**

Create or update `docs/superpowers/uat/2026-05-16-claude-code-parity-layer-uat.md`:

```markdown
# Claude Code Parity Layer UAT

Date: 2026-05-16

## Automated Verification

- `go test ./...`: PASS
- `go build ./...`: PASS
- Targeted Claude compatibility tests: PASS
- Targeted account breaker tests: PASS

## Manual Claude Code Smoke Plan

1. Run Kiro-Go locally on `http://localhost:8080`.
2. Run Claude Code with `ANTHROPIC_BASE_URL=http://localhost:8080`.
3. Send a normal prompt and confirm stream text appears.
4. Run again with `ENABLE_TOOL_SEARCH=true`.
5. Confirm request logs show beta flags, request IDs, selected account, first-token latency, and tool-reference count.

## Production Load Plan

- Non-stream: 100 requests at concurrency 10 through sub2api.
- Stream: 100 requests at concurrency 10 through sub2api.
- Success criteria: no protocol-invalid responses, no orphan tool messages, errors are classified as rate limit, overload, timeout, auth, billing, or invalid request.

## Results

- Automated verification completed before deployment.
- Manual and production load results should be appended after deployment.
```

- [ ] **Step 4: Commit UAT notes**

Run:

```bash
git add docs/superpowers/uat/2026-05-16-claude-code-parity-layer-uat.md
git commit -m "docs: add claude code parity uat plan"
```

## Self-Review Checklist

- Spec coverage:
  - Anthropic envelope and headers: Task 1 and Task 6.
  - `tool_reference` and Tool Search request compatibility: Task 1 and Task 3.
  - SSE ordering, heartbeat, stream errors, chunked `input_json_delta`: Task 2.
  - Per-model breaker and sticky routing: Task 4.
  - Payload guard and truncation recovery: Task 5.
  - `/v1/models`, error headers, request IDs: Task 6.
  - Fake/fixture compatibility and UAT: Task 6 and Task 7.
- Placeholder scan:
  - This plan intentionally avoids vague implementation instructions. Each code-bearing task includes concrete test and implementation snippets.
- Type consistency:
  - `ClaudeToolReference`, `anthropicEnvelope`, `claudeSSEWriter`, `modelBreakerState`, `payloadGuardResult`, and request-log helper names are defined before later tasks reference them.
