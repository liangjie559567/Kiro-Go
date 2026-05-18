# Full Claude Code Parity And Real UAT Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Kiro-Go's full Claude Code/Anthropic parity optimization and prove `/www/sub2api` still works through the latest Docker-built Kiro-Go with API, database, and Playwright-MCP evidence.

**Architecture:** Keep compatibility behavior at the Anthropic envelope, translator, Kiro callback, request-log, and admin-observability boundaries. Do not rewrite account routing or modify `/www/sub2api`; use sub2api only as a real downstream verification target. Treat upstream-impossible official features as explicit `PARTIAL` capabilities with request-log/admin evidence.

**Tech Stack:** Go 1.21, standard `testing`, static `web/index.html`, Docker Compose, curl, PostgreSQL via Docker exec, Playwright-MCP/browser screenshots, existing `docs/superpowers/uat/` evidence format.

---

## File Structure

- `proxy/handler.go`: request validation, Anthropic `/v1/messages`, count tokens, stream stop reasons, admin API routing, readiness/model-readiness response builders.
- `proxy/handler_test.go`: endpoint behavior, stream behavior, admin API tests, assistant prefill and `max_tokens=0` tests.
- `proxy/kiro.go`: Kiro stream parsing, model-emitted tool-use validation/repair/drop, JSON Schema validation helpers.
- `proxy/kiro_test.go`: low-level stream/tool validation tests.
- `proxy/request_log.go`: request-log entry fields and update helpers.
- `proxy/request_log_test.go`: request-log metadata tests.
- `proxy/translator.go`: Claude message normalization, unsupported block conversion, tool/reference conversion, Kiro payload metadata.
- `proxy/translator_test.go`: protocol normalization, unsupported content, tool-result image, relocated tool docs tests.
- `proxy/token_estimator.go`: count-token estimator coverage for official content/tool fields.
- `proxy/token_estimator_test.go`: estimator regression tests.
- `web/index.html`: admin readiness, request logs, model-readiness rendering.
- `docs/superpowers/uat/<timestamp>/`: generated UAT scripts, screenshots, API responses, database evidence, and final report.

## Ground Rules

- Modify only `/www/Kiro-Go`.
- Do not edit `/www/sub2api` source files.
- Do not run `docker compose down -v`.
- Before touching any source file, run `git diff -- <path>` and preserve unrelated changes.
- Commit after each task.
- Never include credentials or tokens in committed artifacts.

---

### Task 1: Stabilize Existing Invalid Tool Parameter Fix

**Files:**
- Modify: `proxy/kiro.go`
- Modify: `proxy/kiro_test.go`
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Inspect current diffs**

Run:

```bash
git diff -- proxy/kiro.go proxy/kiro_test.go proxy/handler.go proxy/handler_test.go
```

Expected: current diff contains `KiroStreamCallback.OnValidatedToolUse`, JSON Schema validation helpers, and tests for `request_user_input` over `maxItems`.

- [ ] **Step 2: Verify the low-level regression**

Run:

```bash
go test ./proxy -run TestWrapToolUseDropsArrayAboveMaxItems -count=1 -v
```

Expected: PASS. Output should include a warning that invalid `request_user_input` tool use was dropped.

- [ ] **Step 3: Verify the stream regression**

Run:

```bash
go test ./proxy -run TestHandleClaudeStreamInvalidToolUseFallsBackToEndTurn -count=1 -v
```

Expected: PASS. The test must prove the response does not contain `"type":"tool_use"` and does contain `"stop_reason":"end_turn"`.

- [ ] **Step 4: Verify all Go tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS for all packages.

- [ ] **Step 5: Commit**

Run:

```bash
git add proxy/kiro.go proxy/kiro_test.go proxy/handler.go proxy/handler_test.go
git commit -m "fix: suppress invalid claude code tool calls"
```

Expected: commit succeeds and contains only these four source/test files.

---

### Task 2: Add Tool-Use Suppression Request-Log Metadata

**Files:**
- Modify: `proxy/kiro.go`
- Modify: `proxy/handler.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/kiro.go proxy/handler.go proxy/request_log.go proxy/request_log_test.go proxy/handler_test.go
```

Expected: no unrelated local changes after Task 1 commit.

- [ ] **Step 2: Write failing request-log unit test**

Add to `proxy/request_log_test.go`:

```go
func TestRequestLogCapturesSuppressedToolUses(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(10)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()

	ctx, loggedReq, recorder, _ := h.beginRequestLog(rr, req)
	if ctx == nil || recorder == nil {
		t.Fatalf("expected request log context")
	}
	updateRequestLogSuppressedToolUse(loggedReq, "request_user_input", "input does not satisfy client tool schema")
	h.finishRequestLog(ctx, recorder)

	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one log entry")
	}
	entry := entries[0]
	if entry.SuppressedToolUseCount != 1 {
		t.Fatalf("expected suppressed tool count, got %#v", entry)
	}
	if strings.Join(entry.SuppressedToolUseNames, ",") != "request_user_input" {
		t.Fatalf("expected suppressed tool name, got %#v", entry)
	}
	if !strings.Contains(strings.Join(entry.SuppressedToolUseReasons, ","), "schema") {
		t.Fatalf("expected suppressed tool reason, got %#v", entry)
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestRequestLogCapturesSuppressedToolUses -count=1 -v
```

Expected: FAIL because `SuppressedToolUseCount`, `SuppressedToolUseNames`, `SuppressedToolUseReasons`, and `updateRequestLogSuppressedToolUse` do not exist.

- [ ] **Step 4: Add request-log fields and helper**

In `proxy/request_log.go`, add fields to `RequestLogEntry`:

```go
	SuppressedToolUseCount   int      `json:"suppressedToolUseCount,omitempty"`
	SuppressedToolUseNames   []string `json:"suppressedToolUseNames,omitempty"`
	SuppressedToolUseReasons []string `json:"suppressedToolUseReasons,omitempty"`
```

Add helper near other `updateRequestLog...` helpers:

```go
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

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Wire stream suppression logging**

In `proxy/handler.go`, inside the stream `KiroStreamCallback` setup, add an `OnSuppressedToolUse`-style hook if Task 1 callback design has such a field. If Task 1 only has `OnValidatedToolUse`, extend `KiroStreamCallback` in `proxy/kiro.go`:

```go
	OnSuppressedToolUse func(toolUse KiroToolUse, reason string)
```

In `wrapKiroToolUseCallback`, replace each invalid-drop block with:

```go
reason := "input does not satisfy client tool schema"
logger.Warnf("[ToolUse] Dropping invalid tool_use id=%s name=%s: %s", tu.ToolUseID, tu.Name, reason)
if wrapped.OnSuppressedToolUse != nil {
	wrapped.OnSuppressedToolUse(tu, reason)
}
return false
```

For the legacy `OnToolUse` callback branch, use `return` instead of `return false`.

In `proxy/handler.go`, set:

```go
OnSuppressedToolUse: func(tu KiroToolUse, reason string) {
	updateRequestLogSuppressedToolUse(r, tu.Name, reason)
},
```

- [ ] **Step 6: Run request-log test**

Run:

```bash
go test ./proxy -run TestRequestLogCapturesSuppressedToolUses -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Add stream log assertion**

Extend `TestHandleClaudeStreamInvalidToolUseFallsBackToEndTurn` in `proxy/handler_test.go` by constructing handler with request logs:

```go
h := &Handler{pool: p, promptCache: newPromptCacheTracker(defaultPromptCacheTTL), requestLogs: newRequestLogStore(5)}
```

After `waitForAccountRequestCount(t, 1)`, add:

```go
entries := h.requestLogs.List(1)
if len(entries) != 1 {
	t.Fatalf("expected one request log entry")
}
if entries[0].SuppressedToolUseCount != 1 {
	t.Fatalf("expected one suppressed tool use, got %#v", entries[0])
}
if strings.Join(entries[0].SuppressedToolUseNames, ",") != "request_user_input" {
	t.Fatalf("expected request_user_input suppression, got %#v", entries[0])
}
```

- [ ] **Step 8: Run targeted tests**

Run:

```bash
go test ./proxy -run 'TestRequestLogCapturesSuppressedToolUses|TestHandleClaudeStreamInvalidToolUseFallsBackToEndTurn' -count=1 -v
```

Expected: PASS.

- [ ] **Step 9: Commit**

Run:

```bash
gofmt -w proxy/kiro.go proxy/handler.go proxy/request_log.go proxy/request_log_test.go proxy/handler_test.go
go test ./proxy -run 'TestRequestLogCapturesSuppressedToolUses|TestHandleClaudeStreamInvalidToolUseFallsBackToEndTurn' -count=1
git add proxy/kiro.go proxy/handler.go proxy/request_log.go proxy/request_log_test.go proxy/handler_test.go
git commit -m "feat: log suppressed claude tool calls"
```

Expected: commit succeeds.

---

### Task 3: Support Safe Assistant Prefill Compatibility

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`
- Modify: `proxy/translator.go`
- Modify: `proxy/translator_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/handler.go proxy/handler_test.go proxy/translator.go proxy/translator_test.go
```

Expected: no unrelated local changes after previous commit.

- [ ] **Step 2: Write failing validation test**

Replace the existing assistant-prefill rejection expectation in `proxy/handler_test.go` with this test:

```go
func TestValidateClaudeRequestShapeAllowsFinalAssistantTextPrefill(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 64,
		Messages: []ClaudeMessage{
			{Role: "user", Content: "Write one sentence."},
			{Role: "assistant", Content: "The answer is"},
		},
	}
	if msg := validateClaudeRequestShape(req); msg != "" {
		t.Fatalf("expected final assistant text prefill to be accepted, got %q", msg)
	}
}
```

Keep a rejection test for final assistant tool-use-only content:

```go
func TestValidateClaudeRequestShapeRejectsFinalAssistantToolUsePrefill(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 64,
		Messages: []ClaudeMessage{
			{Role: "user", Content: "Use a tool."},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "bash", "input": map[string]interface{}{"command": "pwd"}},
			}},
		},
	}
	if msg := validateClaudeRequestShape(req); !strings.Contains(msg, "assistant-prefill tool_use") {
		t.Fatalf("expected tool-use prefill rejection, got %q", msg)
	}
}
```

- [ ] **Step 3: Run failing validation tests**

Run:

```bash
go test ./proxy -run 'TestValidateClaudeRequestShapeAllowsFinalAssistantTextPrefill|TestValidateClaudeRequestShapeRejectsFinalAssistantToolUsePrefill' -count=1 -v
```

Expected: first test FAILS with current assistant-prefill rejection.

- [ ] **Step 4: Add prefill helpers**

In `proxy/translator.go`, add:

```go
func normalizeAssistantPrefillForKiro(messages []ClaudeMessage) []ClaudeMessage {
	if len(messages) == 0 {
		return messages
	}
	last := messages[len(messages)-1]
	if strings.TrimSpace(last.Role) != "assistant" {
		return messages
	}
	text, toolUses := extractClaudeAssistantContent(last.Content)
	if len(toolUses) > 0 || strings.TrimSpace(text) == "" {
		return messages
	}
	out := append([]ClaudeMessage(nil), messages[:len(messages)-1]...)
	instruction := "Continue the assistant response starting exactly with this prefill:\n\n" + strings.TrimSpace(text)
	out = append(out, ClaudeMessage{Role: "user", Content: instruction})
	return out
}

func finalAssistantMessageHasToolUse(content interface{}) bool {
	_, toolUses := extractClaudeAssistantContent(content)
	return len(toolUses) > 0
}
```

At the start of `ClaudeToKiro`, before `normalizeClaudeMessagesForKiro`, change:

```go
messages := normalizeClaudeMessagesForKiro(req.Messages)
```

to:

```go
messages := normalizeClaudeMessagesForKiro(normalizeAssistantPrefillForKiro(req.Messages))
```

- [ ] **Step 5: Update request shape validation**

In `proxy/handler.go`, replace:

```go
if lastRole == "assistant" {
	return "assistant-prefill final message is not supported; last message must be user"
}
```

with:

```go
if lastRole == "assistant" {
	last := req.Messages[len(req.Messages)-1]
	if finalAssistantMessageHasToolUse(last.Content) {
		return "assistant-prefill tool_use final message is not supported; last message must be user or assistant text prefill"
	}
	text, _ := extractClaudeAssistantContent(last.Content)
	if strings.TrimSpace(text) == "" {
		return "assistant-prefill final message must contain text"
	}
}
```

- [ ] **Step 6: Add translator test**

Add to `proxy/translator_test.go`:

```go
func TestClaudeToKiroConvertsFinalAssistantTextPrefillToCurrentUserInstruction(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 64,
		Messages: []ClaudeMessage{
			{Role: "user", Content: "Complete the sentence."},
			{Role: "assistant", Content: "The result is"},
		},
	}

	payload := ClaudeToKiro(req, false)
	current := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !strings.Contains(current, "Continue the assistant response starting exactly with this prefill") {
		t.Fatalf("expected prefill continuation instruction, got %q", current)
	}
	if !strings.Contains(current, "The result is") {
		t.Fatalf("expected prefill text in current content, got %q", current)
	}
}
```

- [ ] **Step 7: Run targeted tests**

Run:

```bash
go test ./proxy -run 'TestValidateClaudeRequestShapeAllowsFinalAssistantTextPrefill|TestValidateClaudeRequestShapeRejectsFinalAssistantToolUsePrefill|TestClaudeToKiroConvertsFinalAssistantTextPrefillToCurrentUserInstruction' -count=1 -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
gofmt -w proxy/handler.go proxy/handler_test.go proxy/translator.go proxy/translator_test.go
go test ./proxy -run 'TestValidateClaudeRequestShapeAllowsFinalAssistantTextPrefill|TestValidateClaudeRequestShapeRejectsFinalAssistantToolUsePrefill|TestClaudeToKiroConvertsFinalAssistantTextPrefillToCurrentUserInstruction' -count=1
git add proxy/handler.go proxy/handler_test.go proxy/translator.go proxy/translator_test.go
git commit -m "feat: tolerate assistant text prefill"
```

Expected: commit succeeds.

---

### Task 4: Define `max_tokens=0` Messages Behavior

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/handler.go proxy/handler_test.go proxy/request_log.go proxy/request_log_test.go
```

Expected: clean after previous commit.

- [ ] **Step 2: Write failing endpoint test**

Add to `proxy/handler_test.go`:

```go
func TestHandleClaudeMessagesMaxTokensZeroReturnsCompatibleNoOutputResponse(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	h := &Handler{requestLogs: newRequestLogStore(5), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":0,"messages":[{"role":"user","content":"warm cache"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()

	h.handleClaudeMessagesInternal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp ClaudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StopReason != "max_tokens" {
		t.Fatalf("expected max_tokens stop reason, got %#v", resp)
	}
	if len(resp.Content) != 0 || resp.Usage.OutputTokens != 0 {
		t.Fatalf("expected zero-output response, got %#v", resp)
	}
	entries := h.requestLogs.List(1)
	if len(entries) != 1 || entries[0].MaxTokensZeroMode != "local_zero_output" {
		t.Fatalf("expected max_tokens=0 request log mode, got %#v", entries)
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestHandleClaudeMessagesMaxTokensZeroReturnsCompatibleNoOutputResponse -count=1 -v
```

Expected: FAIL because `MaxTokensZeroMode` does not exist and `/v1/messages` currently continues to normal Kiro routing.

- [ ] **Step 4: Add request-log field and helper**

In `proxy/request_log.go`, add:

```go
	MaxTokensZeroMode string `json:"maxTokensZeroMode,omitempty"`
```

Add helper:

```go
func updateRequestLogMaxTokensZeroMode(r *http.Request, mode string) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.MaxTokensZeroMode = strings.TrimSpace(mode)
}
```

- [ ] **Step 5: Implement local zero-output response**

In `proxy/handler.go`, after `estimatedInputTokens := estimateClaudeRequestInputTokens(effectiveReq)` and before web search/routing, add:

```go
if req.MaxTokens == 0 {
	updateRequestLogMaxTokensZeroMode(r, "local_zero_output")
	updateRequestLogUsage(r, estimatedInputTokens, 0, 0, 0)
	h.recordSuccess(estimatedInputTokens, 0, 0)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(ClaudeResponse{
		ID:           "msg_" + uuid.New().String(),
		Type:         "message",
		Role:         "assistant",
		Content:      []ClaudeContentBlock{},
		Model:        req.Model,
		StopReason:   "max_tokens",
		StopSequence: nil,
		Usage: ClaudeUsage{
			InputTokens:  estimatedInputTokens,
			OutputTokens: 0,
		},
	})
	return
}
```

This is a compatibility response, not a true upstream cache warmup. Admin readiness must label cache warmup as `PARTIAL` in a later task.

- [ ] **Step 6: Add request-log helper test**

Add to `proxy/request_log_test.go`:

```go
func TestRequestLogCapturesMaxTokensZeroMode(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(10)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	ctx, loggedReq, recorder, _ := h.beginRequestLog(rr, req)
	updateRequestLogMaxTokensZeroMode(loggedReq, "local_zero_output")
	h.finishRequestLog(ctx, recorder)

	entries := h.requestLogs.List(1)
	if len(entries) != 1 || entries[0].MaxTokensZeroMode != "local_zero_output" {
		t.Fatalf("expected max tokens zero mode, got %#v", entries)
	}
}
```

- [ ] **Step 7: Run targeted tests**

Run:

```bash
go test ./proxy -run 'TestHandleClaudeMessagesMaxTokensZeroReturnsCompatibleNoOutputResponse|TestRequestLogCapturesMaxTokensZeroMode' -count=1 -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
gofmt -w proxy/handler.go proxy/handler_test.go proxy/request_log.go proxy/request_log_test.go
go test ./proxy -run 'TestHandleClaudeMessagesMaxTokensZeroReturnsCompatibleNoOutputResponse|TestRequestLogCapturesMaxTokensZeroMode' -count=1
git add proxy/handler.go proxy/handler_test.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: handle claude max tokens zero"
```

Expected: commit succeeds.

---

### Task 5: Expand Count Token Disclosure And Tests

**Files:**
- Modify: `proxy/token_estimator.go`
- Modify: `proxy/token_estimator_test.go`
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/token_estimator.go proxy/token_estimator_test.go proxy/handler.go proxy/handler_test.go
```

Expected: clean after previous commit.

- [ ] **Step 2: Add estimator regression test**

Add to `proxy/token_estimator_test.go`:

```go
func TestEstimateClaudeRequestInputTokensIncludesOfficialParityFields(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 128,
		System: []interface{}{
			map[string]interface{}{"type": "text", "text": "System instructions", "cache_control": map[string]interface{}{"type": "ephemeral"}},
		},
		Tools: []ClaudeTool{{
			Name:        "browser",
			Description: "Capture screenshots",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{"type": "string", "maxLength": 256},
				},
				"required": []interface{}{"url"},
			},
			CacheControl: map[string]interface{}{"type": "ephemeral"},
		}},
		ToolReferences: []ClaudeToolReference{{
			Type:        "tool_reference",
			ID:          "toolref_1",
			Name:        "mcp__browser__screenshot",
			Description: "Browser screenshot",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}},
		}},
		Thinking: &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 1024},
		Messages: []ClaudeMessage{{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "text", "text": "Read the document and image."},
			map[string]interface{}{"type": "document", "title": "spec.pdf", "source": map[string]interface{}{"type": "base64", "media_type": "application/pdf", "data": strings.Repeat("a", 100)}},
			map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": strings.Repeat("b", 100)}},
			map[string]interface{}{"type": "search_result", "title": "Docs", "url": "https://example.test", "content": "result body"},
		}}},
	}

	got := estimateClaudeRequestInputTokens(req)
	if got < 100 {
		t.Fatalf("expected official parity fields to affect estimate, got %d", got)
	}
}
```

- [ ] **Step 3: Run estimator test**

Run:

```bash
go test ./proxy -run TestEstimateClaudeRequestInputTokensIncludesOfficialParityFields -count=1 -v
```

Expected: PASS if existing estimator already covers these fields. If it fails, update `proxy/token_estimator.go` so `estimateClaudeContentTokens`, `estimateSystemTokens`, and tool/reference token logic include the fields in the test.

- [ ] **Step 4: Add count-token endpoint disclosure test**

Add to `proxy/handler_test.go`:

```go
func TestHandleCountTokensMarksEstimateInHeader(t *testing.T) {
	h := &Handler{}
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", body)
	w := httptest.NewRecorder()

	h.handleCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Kiro-Go-Token-Count-Mode"); got != "estimated" {
		t.Fatalf("expected estimated token count header, got %q", got)
	}
}
```

- [ ] **Step 5: Run failing header test**

Run:

```bash
go test ./proxy -run TestHandleCountTokensMarksEstimateInHeader -count=1 -v
```

Expected: FAIL because the header is not set.

- [ ] **Step 6: Implement header**

In `proxy/handler.go`, in `handleCountTokens`, before encoding response, add:

```go
w.Header().Set("X-Kiro-Go-Token-Count-Mode", "estimated")
```

- [ ] **Step 7: Run targeted tests**

Run:

```bash
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludesOfficialParityFields|TestHandleCountTokensMarksEstimateInHeader' -count=1 -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
gofmt -w proxy/token_estimator.go proxy/token_estimator_test.go proxy/handler.go proxy/handler_test.go
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludesOfficialParityFields|TestHandleCountTokensMarksEstimateInHeader' -count=1
git add proxy/token_estimator.go proxy/token_estimator_test.go proxy/handler.go proxy/handler_test.go
git commit -m "feat: disclose estimated count tokens"
```

Expected: commit succeeds.

---

### Task 6: Improve Claude Code Readiness And Admin UI

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`
- Modify: `web/index.html`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/handler.go proxy/handler_test.go web/index.html
```

Expected: clean after previous commit.

- [ ] **Step 2: Write readiness API test**

Add to `proxy/handler_test.go`:

```go
func TestClaudeCodeReadinessReportsPartialCapabilities(t *testing.T) {
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5)}
	h.requestLogs.Add(RequestLogEntry{
		RequestID:                           "req-partial",
		Endpoint:                            "/v1/messages",
		Model:                               "claude-sonnet-4.5",
		StatusCode:                          200,
		Outcome:                             "success",
		FineGrainedToolStreamingRequested:   true,
		FineGrainedToolStreamingMode:        "requested_partial",
		MaxTokensZeroMode:                   "local_zero_output",
		SuppressedToolUseCount:              1,
		SuppressedToolUseNames:              []string{"request_user_input"},
		PayloadUnsupportedContentBlocks:     []string{"document"},
		PayloadUnknownOfficialFields:        []string{"container"},
		PayloadRelocatedToolDescriptions:    2,
		PayloadOrphanedToolResultsConverted: 1,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	capabilities, ok := resp["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", resp)
	}
	for _, key := range []string{"fineGrainedToolStreaming", "maxTokensZero", "countTokens", "assistantPrefill"} {
		if _, ok := capabilities[key]; !ok {
			t.Fatalf("expected capability %s in %#v", key, capabilities)
		}
	}
}
```

- [ ] **Step 3: Run failing readiness test**

Run:

```bash
go test ./proxy -run TestClaudeCodeReadinessReportsPartialCapabilities -count=1 -v
```

Expected: FAIL if readiness lacks one or more requested capability keys.

- [ ] **Step 4: Implement readiness capability map**

In `proxy/handler.go`, update `apiGetClaudeCodeReadiness` response to include:

```go
"capabilities": map[string]interface{}{
	"messages": map[string]interface{}{"status": "PASS", "detail": "/v1/messages is implemented"},
	"countTokens": map[string]interface{}{"status": "PARTIAL", "detail": "Token counts are estimated by Kiro-Go"},
	"maxTokensZero": map[string]interface{}{"status": "PARTIAL", "detail": "Returns local zero-output compatibility response; not a proven upstream cache warmup"},
	"assistantPrefill": map[string]interface{}{"status": "PARTIAL", "detail": "Text prefill is converted into continuation instruction; tool-use prefill is rejected"},
	"fineGrainedToolStreaming": map[string]interface{}{"status": "PARTIAL", "detail": "Anthropic SSE input_json_delta is emitted from complete Kiro tool input; true upstream partial JSON parity depends on Kiro stream shape"},
	"toolSchemaValidation": map[string]interface{}{"status": "PASS", "detail": "Invalid model-emitted tool_use inputs are repaired or suppressed before Claude Code receives them"},
	"toolReference": map[string]interface{}{"status": "PASS", "detail": "tool_reference is accepted and materialized when relevant"},
}
```

Preserve existing fields and examples.

- [ ] **Step 5: Update admin UI rendering**

In `web/index.html`, update `renderClaudeCodeReadiness(data)` so it renders `data.capabilities`. Add this helper near existing readiness rendering helpers:

```javascript
function renderCapabilityBadge(value) {
    if (!value || !value.status) return '';
    const status = String(value.status);
    const color = status === 'PASS' ? '#16a34a' : status === 'PARTIAL' ? '#d97706' : '#dc2626';
    return '<span style="display:inline-block;padding:2px 7px;border-radius:999px;background:' + color + ';color:white;font-size:11px">' + escapeHtml(status) + '</span>';
}
```

In the readiness card HTML, include:

```javascript
const capabilities = data.capabilities || {};
const capabilityHtml = Object.keys(capabilities).sort().map(function(key) {
    const cap = capabilities[key] || {};
    return '<div style="padding:8px 0;border-top:1px solid #e5e7eb">' +
        '<strong>' + escapeHtml(key) + '</strong> ' + renderCapabilityBadge(cap) +
        '<br><small style="color:#64748b">' + escapeHtml(cap.detail || '') + '</small>' +
        '</div>';
}).join('');
```

Then append `capabilityHtml` into the existing readiness panel.

- [ ] **Step 6: Run tests and static smoke**

Run:

```bash
go test ./proxy -run TestClaudeCodeReadinessReportsPartialCapabilities -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go web/index.html
git commit -m "feat: expose claude code parity readiness"
```

Expected: commit succeeds.

---

### Task 7: Complete Model Readiness Reasons

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`
- Modify: `web/index.html`

- [ ] **Step 1: Inspect existing model readiness implementation**

Run:

```bash
sed -n '4471,4565p' proxy/handler.go
sed -n '3000,3038p' web/index.html
```

Expected: see current `/admin/api/claude-code/model-readiness` builder and UI renderer.

- [ ] **Step 2: Add model readiness reason test**

Add to `proxy/handler_test.go`:

```go
func TestClaudeCodeModelReadinessIncludesAccountReasons(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	if err := config.AddAccount(config.Account{ID: "disabled-account", Email: "disabled@example.com", Enabled: false}); err != nil {
		t.Fatalf("add disabled account: %v", err)
	}
	p := &pool.AccountPool{}
	p.Reload()
	h := &Handler{pool: p, startTime: time.Now().Unix()}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if strings.TrimSpace(fmt.Sprint(resp["routingReason"])) == "" {
		t.Fatalf("expected routingReason, got %#v", resp)
	}
	if _, ok := resp["accounts"].([]interface{}); !ok {
		t.Fatalf("expected account readiness list, got %#v", resp)
	}
}
```

- [ ] **Step 3: Run test**

Run:

```bash
go test ./proxy -run TestClaudeCodeModelReadinessIncludesAccountReasons -count=1 -v
```

Expected: FAIL if `routingReason` or `accounts` is missing.

- [ ] **Step 4: Implement response fields**

In `apiGetClaudeCodeModelReadiness`, ensure response contains:

```go
"requestedModel": requestedModel,
"mappedModel": mappedModel,
"thinking": thinking,
"routingReason": routingReason,
"accounts": accountRows,
```

Build `routingReason` with deterministic values:

```go
routingReason := "no enabled accounts"
if len(accountRows) > 0 {
	routingReason = "accounts evaluated"
}
if schedulableCount > 0 {
	routingReason = "schedulable accounts available"
}
```

Each account row should include:

```go
map[string]interface{}{
	"id": account.ID,
	"email": maskEmail(account.Email),
	"enabled": account.Enabled,
	"healthy": healthy,
	"listsModel": listsModel,
	"schedulable": schedulable,
	"reason": reason,
}
```

Use existing masking/helper functions if present; otherwise add a small local `maskReadinessEmail` helper that preserves the domain and first two local characters.

- [ ] **Step 5: Update web renderer**

In `web/index.html`, update `renderClaudeCodeModelReadiness(data)` to show `routingReason` and account rows. Use a compact table:

```javascript
const accounts = data.accounts || [];
const rows = accounts.map(function(account) {
    return '<tr>' +
        '<td style="padding:6px">' + escapeHtml(account.email || account.id || '') + '</td>' +
        '<td style="padding:6px">' + escapeHtml(String(account.enabled)) + '</td>' +
        '<td style="padding:6px">' + escapeHtml(String(account.listsModel)) + '</td>' +
        '<td style="padding:6px">' + escapeHtml(String(account.schedulable)) + '</td>' +
        '<td style="padding:6px">' + escapeHtml(account.reason || '') + '</td>' +
        '</tr>';
}).join('');
```

Include it below the current model mapping summary.

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./proxy -run TestClaudeCodeModelReadinessIncludesAccountReasons -count=1
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go web/index.html
git commit -m "feat: explain model readiness routing"
```

Expected: commit succeeds.

---

### Task 8: Full Go Regression

**Files:**
- No source changes expected.

- [ ] **Step 1: Run full test suite**

Run:

```bash
go test ./... -count=1
```

Expected: PASS for `kiro-go`, `auth`, `config`, `pool`, and `proxy`.

- [ ] **Step 2: Check worktree**

Run:

```bash
git status --short
```

Expected: no modified source files except expected untracked UAT artifacts from prior sessions.

---

### Task 9: Docker Rebuild And Health Verification

**Files:**
- Create: `docs/superpowers/uat/<timestamp>/docker-health.json`
- Create: `docs/superpowers/uat/<timestamp>/kiro-health.json`
- Create: `docs/superpowers/uat/<timestamp>/sub2api-health.json`

- [ ] **Step 1: Create UAT directory**

Run:

```bash
UAT_DIR="docs/superpowers/uat/full-parity-$(date +%Y%m%d%H%M%S)"
mkdir -p "$UAT_DIR"
printf '%s\n' "$UAT_DIR" > /tmp/kiro-go-full-parity-uat-dir
```

Expected: directory exists and path saved.

- [ ] **Step 2: Capture pre-deploy container status**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
docker ps --format '{{json .}}' > "$UAT_DIR/docker-before.jsonl"
```

Expected: file contains current container list.

- [ ] **Step 3: Rebuild and restart Kiro-Go**

Run:

```bash
docker compose -f /www/Kiro-Go/docker-compose.yml up -d --build
```

Expected: Kiro-Go image builds and container starts. Do not use `down -v`.

- [ ] **Step 4: Capture post-deploy container status**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
docker ps --format '{{json .}}' > "$UAT_DIR/docker-after.jsonl"
docker compose -f /www/Kiro-Go/docker-compose.yml ps --format json > "$UAT_DIR/docker-health.json"
```

Expected: `kiro-go` service is running.

- [ ] **Step 5: Verify Kiro-Go health**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
curl -sS -D "$UAT_DIR/kiro-health.headers" http://localhost:8080/health -o "$UAT_DIR/kiro-health.json"
cat "$UAT_DIR/kiro-health.json"
```

Expected: JSON includes `"status":"ok"`.

- [ ] **Step 6: Verify sub2api health**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
curl -sS -D "$UAT_DIR/sub2api-health.headers" http://localhost:18080/health -o "$UAT_DIR/sub2api-health.json" || curl -sS -D "$UAT_DIR/sub2api-health.headers" http://localhost:18080/ -o "$UAT_DIR/sub2api-health.json"
head -c 1000 "$UAT_DIR/sub2api-health.json"
```

Expected: HTTP response succeeds. If `/health` is not implemented, root page or API response is acceptable and must be analyzed in the UAT report.

---

### Task 10: Real API And Database UAT Through sub2api

**Files:**
- Create: `docs/superpowers/uat/<timestamp>/sub2api-db-before.txt`
- Create: `docs/superpowers/uat/<timestamp>/sub2api-message-nonstream.json`
- Create: `docs/superpowers/uat/<timestamp>/sub2api-message-stream.sse`
- Create: `docs/superpowers/uat/<timestamp>/sub2api-db-after.txt`
- Create: `docs/superpowers/uat/<timestamp>/api-summary.json`

- [ ] **Step 1: Capture database counts before**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
docker ps --format '{{.Names}}' | rg 'postgres|sub2api' > "$UAT_DIR/docker-names.txt"
POSTGRES_CONTAINER="$(docker ps --format '{{.Names}}' | rg 'postgres' | head -n 1)"
docker exec "$POSTGRES_CONTAINER" sh -lc 'psql -U ${POSTGRES_USER:-postgres} -d ${POSTGRES_DB:-sub2api} -c "\dt" -c "select now();"' > "$UAT_DIR/sub2api-db-before.txt" 2>&1
```

Expected: file contains table list or a clear database connection error. Connection errors must be reported as UAT blockers.

- [ ] **Step 2: Discover sub2api API credential source without printing secrets**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
grep -R "API_KEY\|AUTH_TOKEN\|ADMIN" -n /www/sub2api/deploy/.env /www/sub2api/deploy/docker-compose*.yml | sed -E 's/(=).+/\1REDACTED/' > "$UAT_DIR/sub2api-secret-shape.txt"
```

Expected: file shows which env variables exist with redacted values. Do not commit unredacted secrets.

- [ ] **Step 3: Send non-stream message through sub2api**

Use the known sub2api client key from the local environment/config without printing it. If no client key is required, omit `Authorization`.

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
SUB2API_KEY="$(grep -E '^API_KEY=' /www/sub2api/deploy/.env 2>/dev/null | head -n1 | cut -d= -f2-)"
AUTH_ARGS=()
if [ -n "$SUB2API_KEY" ]; then AUTH_ARGS=(-H "Authorization: Bearer $SUB2API_KEY"); fi
curl -sS "${AUTH_ARGS[@]}" \
  -H 'Content-Type: application/json' \
  -D "$UAT_DIR/sub2api-message-nonstream.headers" \
  http://localhost:18080/v1/messages \
  -d '{"model":"claude-sonnet-4.5","max_tokens":64,"messages":[{"role":"user","content":"Reply with exactly: KIro-Go sub2api nonstream ok"}]}' \
  -o "$UAT_DIR/sub2api-message-nonstream.json"
jq '{id,type,role,model,stop_reason,usage,content}' "$UAT_DIR/sub2api-message-nonstream.json" > "$UAT_DIR/sub2api-message-nonstream.summary.json" 2>/dev/null || head -c 2000 "$UAT_DIR/sub2api-message-nonstream.json" > "$UAT_DIR/sub2api-message-nonstream.preview.txt"
```

Expected: HTTP 2xx and a Claude-compatible response body. If the model refuses exact marker text, response can still pass if status, schema, and routing evidence are correct.

- [ ] **Step 4: Send stream message through sub2api**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
SUB2API_KEY="$(grep -E '^API_KEY=' /www/sub2api/deploy/.env 2>/dev/null | head -n1 | cut -d= -f2-)"
AUTH_ARGS=()
if [ -n "$SUB2API_KEY" ]; then AUTH_ARGS=(-H "Authorization: Bearer $SUB2API_KEY"); fi
curl -sS -N "${AUTH_ARGS[@]}" \
  -H 'Content-Type: application/json' \
  -D "$UAT_DIR/sub2api-message-stream.headers" \
  http://localhost:18080/v1/messages \
  -d '{"model":"claude-sonnet-4.5","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"Say stream ok in a short sentence."}]}' \
  -o "$UAT_DIR/sub2api-message-stream.sse"
head -c 3000 "$UAT_DIR/sub2api-message-stream.sse" > "$UAT_DIR/sub2api-message-stream.preview.txt"
```

Expected: SSE file includes `event: message_start` and `event: message_stop`, or sub2api's documented stream envelope if it transforms upstream streams.

- [ ] **Step 5: Capture database counts after**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
POSTGRES_CONTAINER="$(docker ps --format '{{.Names}}' | rg 'postgres' | head -n 1)"
docker exec "$POSTGRES_CONTAINER" sh -lc 'psql -U ${POSTGRES_USER:-postgres} -d ${POSTGRES_DB:-sub2api} -c "select now();" -c "\dt"' > "$UAT_DIR/sub2api-db-after.txt" 2>&1
```

Expected: after evidence captured. If exact usage tables are known, add targeted read-only `select count(*)` queries and save output.

- [ ] **Step 6: Capture Kiro-Go readiness APIs**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
curl -sS http://localhost:8080/admin/api/claude-code/readiness -o "$UAT_DIR/kiro-claude-readiness.json"
curl -sS 'http://localhost:8080/admin/api/claude-code/model-readiness?model=claude-sonnet-4.5' -o "$UAT_DIR/kiro-model-readiness.json"
curl -sS 'http://localhost:8080/admin/api/request-logs?limit=20' -o "$UAT_DIR/kiro-request-logs.json"
```

Expected: JSON files are created. If admin password is required, rerun with `X-Admin-Password` header from local config without printing the password.

---

### Task 11: Playwright-MCP Browser UAT And Screenshot Analysis

**Files:**
- Create: `docs/superpowers/uat/<timestamp>/*.png`
- Create: `docs/superpowers/uat/<timestamp>/playwright-summary.json`
- Create: `docs/superpowers/uat/<timestamp>/screenshot-analysis.md`

- [ ] **Step 1: Open Kiro-Go admin in Playwright-MCP**

Use Playwright-MCP browser to visit:

```text
http://localhost:8080/admin
```

Expected: Kiro-Go admin page loads.

- [ ] **Step 2: Capture Kiro-Go screenshots**

Using Playwright-MCP, capture screenshots into the UAT directory:

```text
kiro-admin-dashboard.png
kiro-admin-claude-readiness.png
kiro-admin-model-readiness.png
kiro-admin-request-logs.png
```

Expected visual facts:

- dashboard/admin shell renders;
- Claude Code readiness panel is visible;
- model readiness panel is visible;
- request logs show recent API calls or empty state plus controls.

- [ ] **Step 3: Open sub2api admin in Playwright-MCP**

Use Playwright-MCP browser to visit:

```text
http://localhost:18080
```

If sub2api admin lives under a different path, follow visible navigation without editing sub2api source.

- [ ] **Step 4: Capture sub2api screenshots**

Using Playwright-MCP, capture screenshots into the UAT directory:

```text
sub2api-dashboard.png
sub2api-accounts.png
sub2api-usage.png
sub2api-groups-or-channels.png
```

Expected visual facts:

- sub2api frontend renders without browser console fatal errors;
- accounts or channel page is reachable;
- usage/logs page is reachable;
- recent request activity is visible when the app exposes it.

- [ ] **Step 5: Save browser diagnostics**

Use Playwright-MCP to collect page title, URL, console errors, and screenshot paths. Save to:

```text
docs/superpowers/uat/<timestamp>/playwright-summary.json
```

Expected: JSON contains visited URLs, screenshot filenames, and console/page error summary.

- [ ] **Step 6: Analyze screenshots**

Create `docs/superpowers/uat/<timestamp>/screenshot-analysis.md`. For every screenshot, write a concrete verdict word (`PASS`, `PARTIAL`, or `FAIL`) followed by what is actually visible. Do not leave empty evidence text.

```markdown
# Screenshot Analysis

## Kiro-Go Admin

- `kiro-admin-dashboard.png`: write verdict and visible evidence from the screenshot.
- `kiro-admin-claude-readiness.png`: write verdict and visible evidence from the screenshot.
- `kiro-admin-model-readiness.png`: write verdict and visible evidence from the screenshot.
- `kiro-admin-request-logs.png`: write verdict and visible evidence from the screenshot.

## sub2api Admin

- `sub2api-dashboard.png`: write verdict and visible evidence from the screenshot.
- `sub2api-accounts.png`: write verdict and visible evidence from the screenshot.
- `sub2api-usage.png`: write verdict and visible evidence from the screenshot.
- `sub2api-groups-or-channels.png`: write verdict and visible evidence from the screenshot.

## Console And Page Errors

- Write verdict and evidence from `playwright-summary.json`.
```

Expected: analysis references what is actually visible in each screenshot. Do not mark PASS for screenshots that do not show the claimed page.

---

### Task 12: Final UAT Report

**Files:**
- Create: `docs/superpowers/uat/<timestamp>/REPORT.md`
- Create: `docs/superpowers/uat/<timestamp>/summary.json`

- [ ] **Step 1: Generate summary JSON from collected evidence**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
GO_TESTS="FAIL"
go test ./... -count=1 >/tmp/kiro-go-full-parity-tests.log 2>&1 && GO_TESTS="PASS"
cp /tmp/kiro-go-full-parity-tests.log "$UAT_DIR/go-test-final.log"

DOCKER_STATUS="FAIL"
grep -q 'kiro-go' "$UAT_DIR/docker-after.jsonl" && DOCKER_STATUS="PASS"

KIRO_HEALTH="FAIL"
grep -q '"status"[[:space:]]*:[[:space:]]*"ok"' "$UAT_DIR/kiro-health.json" && KIRO_HEALTH="PASS"

SUB2API_HEALTH="FAIL"
test -s "$UAT_DIR/sub2api-health.json" && SUB2API_HEALTH="PASS"

SUB2API_NONSTREAM="FAIL"
if grep -q '"type"[[:space:]]*:[[:space:]]*"message"' "$UAT_DIR/sub2api-message-nonstream.json"; then
  SUB2API_NONSTREAM="PASS"
elif test -s "$UAT_DIR/sub2api-message-nonstream.json"; then
  SUB2API_NONSTREAM="PARTIAL"
fi

SUB2API_STREAM="FAIL"
if grep -q 'message_stop' "$UAT_DIR/sub2api-message-stream.sse"; then
  SUB2API_STREAM="PASS"
elif test -s "$UAT_DIR/sub2api-message-stream.sse"; then
  SUB2API_STREAM="PARTIAL"
fi

DATABASE_EVIDENCE="FAIL"
if test -s "$UAT_DIR/sub2api-db-before.txt" && test -s "$UAT_DIR/sub2api-db-after.txt"; then
  DATABASE_EVIDENCE="PASS"
fi

PLAYWRIGHT_SCREENSHOTS="FAIL"
if test -s "$UAT_DIR/playwright-summary.json" && test -s "$UAT_DIR/screenshot-analysis.md"; then
  if rg -q 'FAIL' "$UAT_DIR/screenshot-analysis.md"; then
    PLAYWRIGHT_SCREENSHOTS="FAIL"
  elif rg -q 'PARTIAL' "$UAT_DIR/screenshot-analysis.md"; then
    PLAYWRIGHT_SCREENSHOTS="PARTIAL"
  else
    PLAYWRIGHT_SCREENSHOTS="PASS"
  fi
fi

OVERALL="PASS"
for value in "$GO_TESTS" "$DOCKER_STATUS" "$KIRO_HEALTH" "$SUB2API_HEALTH" "$SUB2API_NONSTREAM" "$SUB2API_STREAM" "$DATABASE_EVIDENCE" "$PLAYWRIGHT_SCREENSHOTS"; do
  if [ "$value" = "FAIL" ]; then OVERALL="FAIL"; fi
  if [ "$value" = "PARTIAL" ] && [ "$OVERALL" = "PASS" ]; then OVERALL="PARTIAL"; fi
done

jq -n \
  --arg status "$OVERALL" \
  --arg timestamp "$(date -Iseconds)" \
  --arg go_tests "$GO_TESTS" \
  --arg docker "$DOCKER_STATUS" \
  --arg kiro_health "$KIRO_HEALTH" \
  --arg sub2api_health "$SUB2API_HEALTH" \
  --arg sub2api_nonstream "$SUB2API_NONSTREAM" \
  --arg sub2api_stream "$SUB2API_STREAM" \
  --arg database_evidence "$DATABASE_EVIDENCE" \
  --arg playwright_screenshots "$PLAYWRIGHT_SCREENSHOTS" \
  '{
    status: $status,
    timestamp: $timestamp,
    go_tests: $go_tests,
    docker: $docker,
    kiro_health: $kiro_health,
    sub2api_health: $sub2api_health,
    sub2api_nonstream: $sub2api_nonstream,
    sub2api_stream: $sub2api_stream,
    database_evidence: $database_evidence,
    playwright_screenshots: $playwright_screenshots,
    notes: []
  }' > "$UAT_DIR/summary.json"
```

Expected: `summary.json` contains only `PASS`, `PARTIAL`, or `FAIL` values derived from collected evidence.

- [ ] **Step 2: Generate report with evidence-linked verdicts**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
STATUS="$(jq -r '.status' "$UAT_DIR/summary.json")"
GO_TESTS="$(jq -r '.go_tests' "$UAT_DIR/summary.json")"
DOCKER_STATUS="$(jq -r '.docker' "$UAT_DIR/summary.json")"
KIRO_HEALTH="$(jq -r '.kiro_health' "$UAT_DIR/summary.json")"
SUB2API_HEALTH="$(jq -r '.sub2api_health' "$UAT_DIR/summary.json")"
SUB2API_NONSTREAM="$(jq -r '.sub2api_nonstream' "$UAT_DIR/summary.json")"
SUB2API_STREAM="$(jq -r '.sub2api_stream' "$UAT_DIR/summary.json")"
DATABASE_EVIDENCE="$(jq -r '.database_evidence' "$UAT_DIR/summary.json")"
PLAYWRIGHT_SCREENSHOTS="$(jq -r '.playwright_screenshots' "$UAT_DIR/summary.json")"

TOOL_SCHEMA_VALIDATION="FAIL"
if [ "$GO_TESTS" = "PASS" ] && rg -q 'suppressedToolUse|request_user_input|toolSchemaValidation' "$UAT_DIR/kiro-request-logs.json" "$UAT_DIR/kiro-claude-readiness.json"; then
  TOOL_SCHEMA_VALIDATION="PASS"
elif [ "$GO_TESTS" = "PASS" ]; then
  TOOL_SCHEMA_VALIDATION="PARTIAL"
fi

MODEL_READINESS="FAIL"
if test -s "$UAT_DIR/kiro-model-readiness.json"; then
  MODEL_READINESS="PASS"
fi

TOOL_REFERENCE="FAIL"
if rg -q 'toolReference|tool_reference|tool refs|tool refs' "$UAT_DIR/kiro-claude-readiness.json" "$UAT_DIR/kiro-request-logs.json"; then
  TOOL_REFERENCE="PASS"
elif test -s "$UAT_DIR/kiro-claude-readiness.json"; then
  TOOL_REFERENCE="PARTIAL"
fi

cat > "$UAT_DIR/REPORT.md" <<EOF_REPORT
# Full Claude Code Parity And Real UAT Report

## Verdict

$STATUS

## Code Verification

- \`go test ./... -count=1\`: $GO_TESTS. See \`go-test-final.log\`.

## Docker Health

- Docker service state: $DOCKER_STATUS. See \`docker-after.jsonl\` and \`docker-health.json\`.
- Kiro-Go health: $KIRO_HEALTH. See \`kiro-health.json\`.
- sub2api health: $SUB2API_HEALTH. See \`sub2api-health.json\`.

## API Evidence

- sub2api non-stream: $SUB2API_NONSTREAM. See \`sub2api-message-nonstream.json\`.
- sub2api stream: $SUB2API_STREAM. See \`sub2api-message-stream.sse\`.
- Kiro-Go readiness: see \`kiro-claude-readiness.json\`.
- Kiro-Go model readiness: see \`kiro-model-readiness.json\`.
- Kiro-Go request logs: see \`kiro-request-logs.json\`.

## Database Evidence

- Database evidence: $DATABASE_EVIDENCE.
- Before: \`sub2api-db-before.txt\`.
- After: \`sub2api-db-after.txt\`.
- Interpretation: compare before/after files for reachable database state and recorded request/usage tables where present.

## Browser Evidence

- Playwright screenshots: $PLAYWRIGHT_SCREENSHOTS.
- Kiro-Go screenshots: \`kiro-admin-dashboard.png\`, \`kiro-admin-claude-readiness.png\`, \`kiro-admin-model-readiness.png\`, \`kiro-admin-request-logs.png\`.
- sub2api screenshots: \`sub2api-dashboard.png\`, \`sub2api-accounts.png\`, \`sub2api-usage.png\`, \`sub2api-groups-or-channels.png\`.
- Console/page errors: see \`playwright-summary.json\`.

## Capability Matrix

| Capability | Verdict | Evidence |
| --- | --- | --- |
| Messages API | $SUB2API_NONSTREAM | \`sub2api-message-nonstream.json\`, \`kiro-request-logs.json\` |
| Count tokens | PARTIAL | \`go-test-final.log\`, \`kiro-claude-readiness.json\`; Kiro-Go discloses estimated counts |
| max_tokens=0 | PARTIAL | \`go-test-final.log\`, \`kiro-claude-readiness.json\`; local zero-output compatibility is not proven upstream cache warmup |
| Assistant text prefill | PARTIAL | \`go-test-final.log\`, \`kiro-claude-readiness.json\`; text prefill is converted, tool-use prefill remains unsupported |
| Tool schema validation | $TOOL_SCHEMA_VALIDATION | \`go-test-final.log\`, \`kiro-request-logs.json\`, \`kiro-claude-readiness.json\` |
| Fine-grained streaming truthfulness | PARTIAL | \`kiro-claude-readiness.json\`; accepted headers do not prove true upstream partial JSON parity |
| Tool reference | $TOOL_REFERENCE | \`kiro-claude-readiness.json\`, \`kiro-request-logs.json\` |
| Model readiness | $MODEL_READINESS | \`kiro-model-readiness.json\`, \`kiro-admin-model-readiness.png\` |
| sub2api downstream non-stream | $SUB2API_NONSTREAM | \`sub2api-message-nonstream.json\`, \`sub2api-db-after.txt\` |
| sub2api downstream stream | $SUB2API_STREAM | \`sub2api-message-stream.sse\`, \`sub2api-db-after.txt\` |

## Screenshot Analysis Link

- \`screenshot-analysis.md\`

## Residual Risks

EOF_REPORT

if rg -q 'PARTIAL|FAIL' "$UAT_DIR/REPORT.md"; then
  cat >> "$UAT_DIR/REPORT.md" <<'EOF_REPORT'
- Review the capability matrix above. Each PARTIAL or FAIL row cites the artifact proving the limitation.
EOF_REPORT
else
  cat >> "$UAT_DIR/REPORT.md" <<'EOF_REPORT'
- No residual PARTIAL or FAIL items were detected by the generated summary.
EOF_REPORT
fi
```

Expected: `REPORT.md` contains no empty evidence cells, no placeholder values, and no claims unsupported by screenshots/API/database artifacts.

- [ ] **Step 3: Final verification**

Run:

```bash
go test ./... -count=1
git status --short
```

Expected: tests PASS. Worktree shows only intentional source commits and the new UAT directory.

- [ ] **Step 4: Commit UAT artifacts**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
git add "$UAT_DIR"
git commit -m "test: add full parity docker uat evidence"
```

Expected: commit succeeds and includes only sanitized UAT artifacts.
