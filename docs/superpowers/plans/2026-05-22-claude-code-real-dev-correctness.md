# Claude Code Real Development Correctness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent real Claude Code development workflows from being falsely completed by Opus 4.7 capacity fallback assistant text.

**Architecture:** Add a Claude Code request class on top of the existing priority lane classifier, then route Anthropic Claude Code development failures to retryable errors or stream closure without assistant content. Keep OpenAI-compatible and simple sub2api stable fallback behavior intact, and lock the boundary with unit/integration tests.

**Tech Stack:** Go, `net/http/httptest`, existing Kiro-Go proxy/request log/config test helpers, `go test`.

---

## File Structure

- Modify `proxy/request_classifier.go`: add `RequestWorkloadClass`, classification fields, and conservative dev-flow heuristics.
- Modify `proxy/request_classifier_test.go`: cover Claude Code dev/simple/background/openai classification boundaries.
- Modify `proxy/request_log.go`: persist request workload class and classification reason in request logs.
- Modify `proxy/handler.go`: add development-aware stable fallback helpers and use them from admission, no-account, attempt-budget, and session governor paths.
- Modify `proxy/handler_test.go`: replace old Claude dev fallback expectations with retryable/no-assistant expectations; preserve simple stable fallback tests.
- Optional docs update in `README_CN.md` only if implementation changes user-visible operator behavior not already documented.

## Task 1: Classify Claude Code Development Requests

**Files:**
- Modify: `proxy/request_classifier.go`
- Modify: `proxy/request_classifier_test.go`
- Modify: `proxy/request_log.go`

- [ ] **Step 1: Write failing classifier tests**

Add these tests to `proxy/request_classifier_test.go`:

```go
func TestClassifyGenerationRequestClaudeCodeDevFromTools(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	req.Header.Set("X-Claude-Code-Session-Id", "session-main")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
		Anthropic: &anthropicEnvelope{
			SessionID: "session-main",
			Request: ClaudeRequest{
				Model: "claude-opus-4.7",
				Tools: []ClaudeTool{{
					Name:        "bash",
					Description: "Run a shell command",
					InputSchema: map[string]interface{}{"type": "object"},
				}},
			},
		},
	})

	if got.WorkloadClass != RequestWorkloadClaudeCodeDev {
		t.Fatalf("WorkloadClass = %q, want %q", got.WorkloadClass, RequestWorkloadClaudeCodeDev)
	}
	if got.Reason != "claude_code_dev_tools" {
		t.Fatalf("Reason = %q, want claude_code_dev_tools", got.Reason)
	}
}

func TestClassifyGenerationRequestClaudeCodeDevFromToolResultMessage(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/2.1")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
		Claude: &ClaudeRequest{
			Model: "claude-opus-4.7",
			Messages: []ClaudeMessage{{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok"},
				},
			}},
		},
	})

	if got.WorkloadClass != RequestWorkloadClaudeCodeDev {
		t.Fatalf("WorkloadClass = %q, want %q", got.WorkloadClass, RequestWorkloadClaudeCodeDev)
	}
	if got.Reason != "claude_code_dev_tool_result" {
		t.Fatalf("Reason = %q, want claude_code_dev_tool_result", got.Reason)
	}
}

func TestClassifyGenerationRequestClaudeCodeSimpleFromUserAgentOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
		Claude: &ClaudeRequest{
			Model:    "claude-opus-4.7",
			Messages: []ClaudeMessage{{Role: "user", Content: "say ok"}},
		},
	})

	if got.WorkloadClass != RequestWorkloadClaudeCodeSimple {
		t.Fatalf("WorkloadClass = %q, want %q", got.WorkloadClass, RequestWorkloadClaudeCodeSimple)
	}
	if got.Reason != "claude_code_simple" {
		t.Fatalf("Reason = %q, want claude_code_simple", got.Reason)
	}
}

func TestClassifyGenerationRequestBackgroundWorkloadForCountTokens(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages/count_tokens",
	})

	if got.WorkloadClass != RequestWorkloadBackground {
		t.Fatalf("WorkloadClass = %q, want %q", got.WorkloadClass, RequestWorkloadBackground)
	}
}
```

- [ ] **Step 2: Run classifier tests and verify failure**

Run:

```bash
go test ./proxy -run 'TestClassifyGenerationRequestClaudeCode(Dev|Simple)|TestClassifyGenerationRequestBackgroundWorkload' -count=1
```

Expected: FAIL because `RequestWorkloadClaudeCodeDev`, `RequestWorkloadClaudeCodeSimple`, and `WorkloadClass` do not exist yet.

- [ ] **Step 3: Implement workload classification**

In `proxy/request_classifier.go`, add this enum near `RequestPriorityLane`:

```go
type RequestWorkloadClass string

const (
	RequestWorkloadUnknown          RequestWorkloadClass = ""
	RequestWorkloadClaudeCodeDev    RequestWorkloadClass = "claude_code_dev"
	RequestWorkloadClaudeCodeSimple RequestWorkloadClass = "claude_code_simple"
	RequestWorkloadOpenAICompatible RequestWorkloadClass = "openai_compatible"
	RequestWorkloadBackground       RequestWorkloadClass = "background"
)
```

Add fields to `RequestClassification`:

```go
WorkloadClass RequestWorkloadClass
```

Update `classifyGenerationRequest` so it sets the workload class after model/stream/session extraction and before returning. Use this exact helper shape:

```go
func classifyGenerationRequest(input RequestClassificationInput) RequestClassification {
	out := RequestClassification{
		Lane:          RequestLaneInteractive,
		WorkloadClass: RequestWorkloadOpenAICompatible,
		Reason:        "default_interactive",
		Endpoint:      strings.TrimSpace(input.Endpoint),
		Model:         strings.TrimSpace(input.Model),
		Stream:        input.Stream,
	}
	// keep existing endpoint/model/stream/session extraction unchanged

	out.ClaudeCode = out.SessionID != "" || out.AgentID != "" || out.ParentAgentID != "" || requestUserAgentLooksClaudeCode(input.Request)
	if isBackgroundEndpoint(out.Endpoint) {
		out.Lane = RequestLaneBackground
		out.WorkloadClass = RequestWorkloadBackground
		out.Reason = "background_endpoint"
		return out
	}
	if out.AgentID != "" || out.ParentAgentID != "" {
		out.Lane = RequestLaneSubagent
	}
	if out.ClaudeCode {
		out.WorkloadClass, out.Reason = classifyClaudeCodeWorkload(input)
		if out.Lane == RequestLaneSubagent {
			if out.WorkloadClass == RequestWorkloadClaudeCodeSimple {
				out.WorkloadClass = RequestWorkloadClaudeCodeDev
			}
			out.Reason = "agent_metadata"
		}
		return out
	}
	return out
}
```

Add these helpers below `requestUserAgentLooksClaudeCode`:

```go
func classifyClaudeCodeWorkload(input RequestClassificationInput) (RequestWorkloadClass, string) {
	req := input.Claude
	if req == nil && input.Anthropic != nil {
		req = &input.Anthropic.Request
	}
	if req == nil {
		return RequestWorkloadClaudeCodeSimple, "claude_code_simple"
	}
	if len(req.Tools) > 0 || len(req.ToolReferences) > 0 {
		return RequestWorkloadClaudeCodeDev, "claude_code_dev_tools"
	}
	if claudeMessagesContainBlockType(req.Messages, "tool_use") {
		return RequestWorkloadClaudeCodeDev, "claude_code_dev_tool_use"
	}
	if claudeMessagesContainBlockType(req.Messages, "tool_result") {
		return RequestWorkloadClaudeCodeDev, "claude_code_dev_tool_result"
	}
	return RequestWorkloadClaudeCodeSimple, "claude_code_simple"
}

func claudeMessagesContainBlockType(messages []ClaudeMessage, blockType string) bool {
	for _, message := range messages {
		if claudeContentContainsBlockType(message.Content, blockType) {
			return true
		}
	}
	return false
}

func claudeContentContainsBlockType(content interface{}, blockType string) bool {
	raw, err := json.Marshal(content)
	if err != nil {
		return false
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	return decodedContentContainsBlockType(decoded, blockType)
}

func decodedContentContainsBlockType(value interface{}, blockType string) bool {
	switch v := value.(type) {
	case []interface{}:
		for _, item := range v {
			if decodedContentContainsBlockType(item, blockType) {
				return true
			}
		}
	case map[string]interface{}:
		if typ, _ := v["type"].(string); strings.EqualFold(strings.TrimSpace(typ), blockType) {
			return true
		}
		for _, nested := range v {
			if decodedContentContainsBlockType(nested, blockType) {
				return true
			}
		}
	}
	return false
}
```

Keep the existing lane behavior for old tests: subagent still returns `Reason == "agent_metadata"`.

- [ ] **Step 4: Log workload classification**

In `proxy/request_log.go`, add fields to `RequestLogEntry` near `PriorityLane`:

```go
RequestWorkloadClass string `json:"requestWorkloadClass,omitempty"`
ClassificationReason string `json:"classificationReason,omitempty"`
```

Update `updateRequestLogClassification`:

```go
ctx.entry.PriorityLane = string(c.Lane)
ctx.entry.RequestWorkloadClass = string(c.WorkloadClass)
ctx.entry.ClassificationReason = strings.TrimSpace(c.Reason)
```

- [ ] **Step 5: Run classifier tests and commit**

Run:

```bash
go test ./proxy -run 'TestClassifyGenerationRequest' -count=1
```

Expected: PASS.

Commit:

```bash
git add proxy/request_classifier.go proxy/request_classifier_test.go proxy/request_log.go
git commit -m "feat: classify Claude Code dev requests"
```

## Task 2: Route Claude Code Dev Fallbacks To Retryable Errors

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`
- Modify: `proxy/request_log.go`

- [ ] **Step 1: Add failing helper tests**

In `proxy/handler_test.go`, add:

```go
func TestStableClaudeDevFallbackReturnsRetryableError(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	t.Cleanup(h.Close)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":64,"messages":[{"role":"user","content":"edit file"}],"tools":[{"name":"bash","description":"Run command","input_schema":{"type":"object"}}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")

	h.sendStableClaudeFallback(w, req, "claude-opus-4.7", "admission_pressure", errors.New("queue timeout"))

	if w.Code != anthropicOverloadedStatus {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, anthropicOverloadedStatus, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, `"overloaded_error"`) {
		t.Fatalf("expected retryable error body, got %s", body)
	}
	if strings.Contains(body, `"role":"assistant"`) || strings.Contains(body, "This turn has been closed by the gateway") {
		t.Fatalf("dev fallback must not close as assistant content: %s", body)
	}
}

func TestStableClaudeSimpleFallbackKeepsAssistantCompatibility(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	t.Cleanup(h.Close)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","max_tokens":64,"messages":[{"role":"user","content":"say ok"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")

	h.sendStableClaudeFallback(w, req, "claude-opus-4.7", "no_available_accounts", errors.New("No available accounts"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"role":"assistant"`) {
		t.Fatalf("simple fallback should keep assistant compatibility: %s", w.Body.String())
	}
}
```

- [ ] **Step 2: Run helper tests and verify failure**

Run:

```bash
go test ./proxy -run 'TestStableClaude(Dev|Simple)Fallback' -count=1
```

Expected: `TestStableClaudeDevFallbackReturnsRetryableError` FAILS because current `sendStableClaudeFallback` returns HTTP 200 assistant content for dev requests.

- [ ] **Step 3: Implement development-aware fallback helper**

In `proxy/request_log.go`, add this helper near `updateRequestLogClassification`:

```go
func requestLogWorkloadClass(r *http.Request) RequestWorkloadClass {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return RequestWorkloadUnknown
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return RequestWorkloadClass(strings.TrimSpace(ctx.entry.RequestWorkloadClass))
}
```

In `proxy/handler.go`, add:

func isClaudeCodeDevRequest(r *http.Request, model string, stream bool) bool {
	if requestLogWorkloadClass(r) == RequestWorkloadClaudeCodeDev {
		return true
	}
	if r != nil && requestUserAgentLooksClaudeCode(r) && firstNonEmptyHeader(r, "x-claude-code-agent-id", "x-claude-code-parent-agent-id") != "" {
		return true
	}
	return false
}
```

Then update `sendStableClaudeFallback`:

```go
func (h *Handler) sendStableClaudeFallback(w http.ResponseWriter, r *http.Request, model, reason string, err error) {
	if isClaudeCodeDevRequest(r, model, false) {
		h.sendStableClaudeRetryableError(w, r, model, reason, err)
		return
	}
	h.sendStableClaudeAssistantFallback(w, r, model, reason, err)
}
```

Keep `sendStableClaudeAssistantFallback` unchanged for simple compatibility calls.

- [ ] **Step 4: Run helper tests and commit**

Run:

```bash
go test ./proxy -run 'TestStableClaude(Dev|Simple)Fallback' -count=1
```

Expected: PASS.

Commit:

```bash
git add proxy/handler.go proxy/handler_test.go proxy/request_log.go
git commit -m "fix: avoid assistant fallback for Claude Code dev"
```

## Task 3: Preserve Stream No-Assistant Behavior For Dev Fallbacks

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Add failing stream test**

In `proxy/handler_test.go`, add:

```go
func TestStableClaudeDevStreamFallbackDoesNotEmitAssistantContent(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := NewHandler()
	t.Cleanup(h.Close)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-opus-4.7","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"edit file"}],"tools":[{"name":"bash","description":"Run command","input_schema":{"type":"object"}}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")

	h.sendStableClaudeStreamFallback(w, req, "claude-opus-4.7", "admission_pressure", errors.New("queue timeout"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 SSE; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "message_start") || strings.Contains(body, "content_block_delta") || strings.Contains(body, "message_stop") {
		t.Fatalf("dev stream fallback must not emit assistant message events: %s", body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, `"overloaded_error"`) {
		t.Fatalf("expected retryable SSE error, got %s", body)
	}
}
```

- [ ] **Step 2: Run stream test**

Run:

```bash
go test ./proxy -run 'TestStableClaudeDevStreamFallbackDoesNotEmitAssistantContent|TestStableDownstreamClaudeStreamFallbackReturnsRetryableSSEError' -count=1
```

Expected: PASS if current `sendStableClaudeStreamFallback` already uses SSE error only. If it fails because helper classification changes stream behavior, continue to Step 3.

- [ ] **Step 3: Make stream helper explicitly no-assistant**

If needed, update `sendStableClaudeStreamFallback` in `proxy/handler.go` to keep this behavior:

```go
func (h *Handler) sendStableClaudeStreamFallback(w http.ResponseWriter, r *http.Request, model, reason string, err error) {
	h.sendStableClaudeStreamRetryableError(w, r, model, reason, err)
}
```

Do not route stream fallback to `sendStableClaudeAssistantFallback`.

- [ ] **Step 4: Run stream tests and commit**

Run:

```bash
go test ./proxy -run 'TestStableClaudeDevStreamFallbackDoesNotEmitAssistantContent|TestStableDownstreamClaudeStreamFallbackReturnsRetryableSSEError' -count=1
```

Expected: PASS.

Commit only if files changed:

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "test: cover Claude Code dev stream fallback"
```

## Task 4: Update Existing Regression Expectations

**Files:**
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Find old tests that require assistant fallback for Claude Code dev**

Run:

```bash
rg -n 'This turn has been closed by the gateway|stable fallback must close the turn|want 200 stable assistant fallback' proxy/handler_test.go
```

Expected: several tests still assert assistant fallback for `/v1/messages` with `claude-cli`.

- [ ] **Step 2: Split simple compatibility from dev workflow tests**

For tests that use only this simple body:

```json
{"model":"claude-opus-4.7","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}
```

keep assistant fallback expectations.

For tests that represent governor rejection or development flow, add a tool and session header:

```go
body := `{"model":"claude-opus-4.7","max_tokens":64,"stream":false,"messages":[{"role":"user","content":"edit file"}],"tools":[{"name":"bash","description":"Run command","input_schema":{"type":"object"}}]}`
req.Header.Set("X-Claude-Code-Session-Id", "session-1")
```

Change expectations to:

```go
if w.Code != anthropicOverloadedStatus {
	t.Fatalf("status = %d, want %d; body=%s", w.Code, anthropicOverloadedStatus, w.Body.String())
}
if !strings.Contains(w.Body.String(), `"overloaded_error"`) {
	t.Fatalf("expected retryable error: %s", w.Body.String())
}
if strings.Contains(w.Body.String(), `"role":"assistant"`) || strings.Contains(w.Body.String(), "This turn has been closed by the gateway") {
	t.Fatalf("dev path must not return assistant fallback: %s", w.Body.String())
}
```

Do this at minimum for:

- `TestHandlerClaudeSessionGovernorRejectionWritesStableResponse`
- any no-account or attempt-budget test that adds tools/session after Task 2

- [ ] **Step 3: Run targeted old/new tests**

Run:

```bash
go test ./proxy -run 'TestStableClaude|TestHandlerClaudeSessionGovernor|TestStableClaudeNoAccounts|TestStableClaudeAttemptBudget' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit regression updates**

Commit:

```bash
git add proxy/handler_test.go
git commit -m "test: update Claude Code dev fallback expectations"
```

## Task 5: Add End-To-End Handler Coverage For No Accounts Dev Failure

**Files:**
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Add failing no-account dev test**

Add this test to `proxy/handler_test.go`:

```go
func TestClaudeCodeDevNoAccountsReturnsRetryableErrorWithoutAssistantFallback(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	cfg := config.Get()
	cfg.ContentContinuity.MaxQueueWaitSeconds = 1
	cfg.ContentContinuity.MaxQueueDepth = 10
	if err := config.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	oldGate := modelAdmissionGate
	oldContinuity := contentContinuityGateGlobal
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 1},
		},
	})
	contentContinuityGateGlobal = newContentContinuityGate()
	t.Cleanup(func() {
		modelAdmissionGate = oldGate
		contentContinuityGateGlobal = oldContinuity
	})

	h := &Handler{
		pool:        &pool.AccountPool{},
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
		requestLogs: newRequestLogStore(defaultRequestLogCapacity),
	}
	body := `{"model":"claude-opus-4.7","max_tokens":64,"messages":[{"role":"user","content":"edit src/task.js"}],"tools":[{"name":"bash","description":"Run command","input_schema":{"type":"object"}}]}`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != anthropicOverloadedStatus {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, anthropicOverloadedStatus, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"overloaded_error"`) {
		t.Fatalf("expected retryable error: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"role":"assistant"`) || strings.Contains(w.Body.String(), "This turn has been closed by the gateway") {
		t.Fatalf("dev no-account path must not return assistant fallback: %s", w.Body.String())
	}

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	if logs[0].ContentSuccess {
		t.Fatalf("fallback must not be content success: %#v", logs[0])
	}
	if logs[0].ContentFailureReason == "" {
		t.Fatalf("expected content failure reason: %#v", logs[0])
	}
	if logs[0].RequestWorkloadClass != string(RequestWorkloadClaudeCodeDev) {
		t.Fatalf("RequestWorkloadClass = %q, want %q", logs[0].RequestWorkloadClass, RequestWorkloadClaudeCodeDev)
	}
}
```

Add `kiro-go/config`, `kiro-go/pool`, `context`, and `time` imports only if this test file does not already import them.

- [ ] **Step 2: Run no-account dev test**

Run:

```bash
go test ./proxy -run 'TestClaudeCodeDevNoAccountsReturnsRetryableErrorWithoutAssistantFallback' -count=1
```

Expected: PASS after Tasks 1-4. If it fails with HTTP 200 assistant fallback, route the no-account branch in `handleClaudeWithAccountRetry` through `sendStableClaudeFallback`, which is now dev-aware, rather than directly calling assistant fallback.

- [ ] **Step 3: Commit**

Commit:

```bash
git add proxy/handler_test.go proxy/handler.go
git commit -m "test: cover Claude Code dev no-account capacity failure"
```

## Task 6: Full Verification And Documentation Touch-Up

**Files:**
- Modify: `README_CN.md` only if current text contradicts final behavior.

- [ ] **Step 1: Run full Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Check stable fallback text is no longer required in dev tests**

Run:

```bash
rg -n 'Claude Code dev|claude_code_dev|This turn has been closed by the gateway|StableDownstreamFallback' proxy/handler_test.go proxy/request_classifier_test.go
```

Expected: any `This turn has been closed by the gateway` expectations apply only to simple compatibility fallback tests, not dev workflow tests.

- [ ] **Step 3: Update README if needed**

If `README_CN.md` already says Claude/Anthropic requests wait for real upstream content and do not send fallback text, do not edit it.

If it still says Claude Code gets assistant fallback text, replace that sentence with:

```markdown
- 对真实 Claude Code 开发型请求，容量压力不会以 assistant fallback 文本结束回合；网关会等待真实上游内容，或在预算耗尽时返回可重试错误并记录 `contentSuccess=false`。
```

- [ ] **Step 4: Commit documentation only if changed**

Run:

```bash
git diff -- README_CN.md
```

If there is a diff:

```bash
git add README_CN.md
git commit -m "docs: clarify Claude Code dev fallback behavior"
```

- [ ] **Step 5: Final status check**

Run:

```bash
git status --short --branch
```

Expected: only unrelated pre-existing untracked `.playwright-mcp/console-2026-05-21T17-28-09-796Z.log` remains, unless the user asked to include it.

## Task 7: Real Environment Validation Plan

**Files:**
- Create after implementation: `docs/superpowers/uat/kiro-go-claude-code-real-dev-correctness-<timestamp>/UAT-RESULT.md`
- Create after implementation: evidence files under the same UAT directory.

- [ ] **Step 1: Rebuild and start Docker services**

Run:

```bash
docker compose up -d --build
```

Expected: Kiro-Go on `http://127.0.0.1:8080`; sub2api on `http://127.0.0.1:18080`.

- [ ] **Step 2: Check health and readiness**

Run:

```bash
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:18080/health
ADMIN_PASSWORD=$(node -e "process.stdout.write(require('/www/Kiro-Go/data/config.json').password || '')")
curl -sS -H "X-Admin-Password: $ADMIN_PASSWORD" 'http://127.0.0.1:8080/admin/api/fleet/readiness?model=claude-opus-4-7'
```

Expected: health endpoints respond; readiness may be `healthy` or `degraded`, but reason codes must match real account/admission state.

- [ ] **Step 3: Run real Claude Code single dev task**

Use the existing harness if still present:

```bash
cd /root/gsd-workspaces/claude-code-dev-pressure-20260522
DEV_PRESSURE_AGENTS=1 node run-dev-pressure.mjs
```

Expected: one task edits its target file and selected test passes. Output must not contain `Opus 4.7 upstream capacity is temporarily unavailable` as assistant completion.

- [ ] **Step 4: Run bounded concurrent dev task**

Run only when readiness safe concurrency is at least 1:

```bash
cd /root/gsd-workspaces/claude-code-dev-pressure-20260522
DEV_PRESSURE_AGENTS=2 node run-dev-pressure.mjs
```

Expected: tasks either queue and complete with real content, or one fails explicitly with retryable capacity pressure. No task may be marked PASS without file edits and tests.

- [ ] **Step 5: Capture Playwright-MCP evidence**

Use Playwright-MCP to capture:

- Kiro-Go admin readiness page.
- Kiro-Go request log view.
- sub2api usage/admin view if available.

Expected: screenshots agree with API evidence; fallback entries show `contentSuccess=false`.

- [ ] **Step 6: Write UAT result**

Create `docs/superpowers/uat/kiro-go-claude-code-real-dev-correctness-<timestamp>/UAT-RESULT.md` with:

```markdown
# Kiro-Go Claude Code Real Dev Correctness UAT

Date: <timestamp>
Verdict: PASS or FAIL

## Environment

- Kiro-Go: http://127.0.0.1:8080
- sub2api: http://127.0.0.1:18080
- Claude Code: real local CLI

## Evidence

- Health: PASS or FAIL
- Readiness: status=<status>, reasonCodes=<codes>, safeConcurrency=<n>
- Single dev task: PASS or FAIL, editedFiles=<paths>, tests=<result>
- Concurrent dev task: PASS or FAIL, fallbackTextSeen=<true|false>
- Request logs: contentSuccess for fallback=<true|false>
- Screenshots: <paths>

## Conclusion

PASS requires real file edits, passing tests, no assistant fallback fake success, and request-log/API/screenshot agreement.
```

- [ ] **Step 7: Commit UAT evidence if generated**

Commit:

```bash
git add docs/superpowers/uat/kiro-go-claude-code-real-dev-correctness-*/UAT-RESULT.md docs/superpowers/uat/kiro-go-claude-code-real-dev-correctness-*
git commit -m "test: add Claude Code real dev correctness UAT evidence"
```

## Self-Review

- Spec coverage: request classification is Task 1; no assistant fallback for dev is Tasks 2-5; stream behavior is Task 3; logging/content success is Tasks 1 and 5; real UAT is Task 7.
- Placeholder scan: no implementation step uses unresolved markers or vague edge-case instructions; each code-changing task has concrete snippets and commands.
- Type consistency: `RequestWorkloadClass`, `RequestWorkloadClaudeCodeDev`, `RequestWorkloadClaudeCodeSimple`, `RequestWorkloadOpenAICompatible`, and `RequestWorkloadBackground` are introduced before use.
