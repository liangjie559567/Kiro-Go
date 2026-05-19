# Claude Code Partial Parity Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add layered Claude Code compatibility vs official Anthropic parity readiness for the current `PARTIAL` capabilities without breaking existing Kiro-Go or sub2api callers.

**Architecture:** Keep `/admin/api/claude-code/readiness` backward compatible by preserving each capability's top-level `status` and `detail`, then add nested `claudeCodeCompatibility`, `officialAnthropicParity`, and `evidence` objects. Derive modes from existing request logs first, adding small log fields only where needed.

**Tech Stack:** Go HTTP handler/tests, existing Kiro-Go request log store, browser admin UI in `web/index.html`, existing Go test suite, Docker/UAT scripts.

---

## File Structure

- Modify `proxy/request_log.go`: add `CountTokensMode` and optional `AssistantPrefillMode` fields plus update helpers.
- Modify `proxy/handler.go`: record count-token mode, record assistant-prefill emulation, construct layered readiness capability objects.
- Modify `proxy/handler_test.go`: add focused tests for layered readiness JSON and evidence derivation.
- Modify `web/index.html`: render nested compatibility/parity statuses and evidence under each Claude Code capability.
- Optionally modify `proxy/request_log_test.go`: add a focused test if request-log helper coverage is clearer there.
- Create or update UAT evidence under `docs/superpowers/uat/` after implementation.

## Task 1: Add Request-Log Mode Fields

**Files:**
- Modify: `proxy/request_log.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Write the failing request-log test**

Add this test near `TestRequestLogCapturesMaxTokensZeroMode` in `proxy/request_log_test.go`:

```go
func TestRequestLogCapturesClaudeParityModes(t *testing.T) {
	store := newRequestLogStore(10)
	loggedReq := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	loggedReq = beginRequestLog(loggedReq, store)

	updateRequestLogCountTokensMode(loggedReq, "estimated")
	updateRequestLogAssistantPrefillMode(loggedReq, "emulated_text_prefill")

	completeRequestLog(loggedReq, http.StatusOK, "success", "")
	entries := store.List(10)
	if len(entries) != 1 {
		t.Fatalf("expected one request log entry, got %d", len(entries))
	}
	if entries[0].CountTokensMode != "estimated" {
		t.Fatalf("expected count token mode, got %#v", entries[0])
	}
	if entries[0].AssistantPrefillMode != "emulated_text_prefill" {
		t.Fatalf("expected assistant prefill mode, got %#v", entries[0])
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
go test ./proxy -run TestRequestLogCapturesClaudeParityModes -count=1
```

Expected: fail because `CountTokensMode`, `AssistantPrefillMode`, `updateRequestLogCountTokensMode`, and `updateRequestLogAssistantPrefillMode` do not exist yet.

- [ ] **Step 3: Add the log fields and helpers**

In `RequestLogEntry` in `proxy/request_log.go`, add fields near `MaxTokensZeroMode`:

```go
	CountTokensMode                     string                    `json:"countTokensMode,omitempty"`
	AssistantPrefillMode                string                    `json:"assistantPrefillMode,omitempty"`
```

Add helpers near `updateRequestLogMaxTokensZeroMode`:

```go
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
```

- [ ] **Step 4: Verify request-log test passes**

Run:

```bash
go test ./proxy -run TestRequestLogCapturesClaudeParityModes -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: record claude parity readiness modes"
```

## Task 2: Record Modes During Claude Requests

**Files:**
- Modify: `proxy/handler.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing tests for mode recording**

Add these tests near existing `count_tokens`, `max_tokens=0`, and prefill tests in `proxy/handler_test.go`:

```go
func TestClaudeCountTokensRecordsEstimatedMode(t *testing.T) {
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5)}
	body := strings.NewReader(`{"model":"claude-sonnet-4.5","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", body)
	req = beginRequestLog(req, h.ensureRequestLogStore())
	w := httptest.NewRecorder()

	h.handleClaudeCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	entries := h.ensureRequestLogStore().List(5)
	if len(entries) != 1 || entries[0].CountTokensMode != "estimated" {
		t.Fatalf("expected estimated count-token mode, got %#v", entries)
	}
}

func TestClaudeAssistantTextPrefillRecordsEmulatedMode(t *testing.T) {
	req := ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 64,
		Messages: []ClaudeMessage{
			{Role: "user", Content: "Return JSON"},
			{Role: "assistant", Content: "{\"ok\":"},
		},
	}
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(5)}
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	httpReq = beginRequestLog(httpReq, h.ensureRequestLogStore())

	normalized := normalizeAssistantPrefillForKiro(req.Messages)
	if len(normalized) != 2 || normalized[1].Role != "user" {
		t.Fatalf("expected text prefill to be converted to user instruction, got %#v", normalized)
	}
	updateRequestLogAssistantPrefillMode(httpReq, "emulated_text_prefill")
	completeRequestLog(httpReq, http.StatusOK, "success", "")
	entries := h.ensureRequestLogStore().List(5)
	if len(entries) != 1 || entries[0].AssistantPrefillMode != "emulated_text_prefill" {
		t.Fatalf("expected assistant prefill mode, got %#v", entries)
	}
}
```

- [ ] **Step 2: Run the focused tests**

Run:

```bash
go test ./proxy -run 'TestClaudeCountTokensRecordsEstimatedMode|TestClaudeAssistantTextPrefillRecordsEmulatedMode' -count=1
```

Expected: count-token test fails until the handler records the mode. The assistant prefill test may pass after Task 1 because it exercises the helper directly; keep it as regression coverage.

- [ ] **Step 3: Record count_tokens mode**

In the `/v1/messages/count_tokens` handler function in `proxy/handler.go`, add this before writing the successful response:

```go
	updateRequestLogCountTokensMode(r, "estimated")
```

If the function already computes `estimatedInputTokens`, place the call near `updateRequestLogUsage`.

- [ ] **Step 4: Record assistant prefill mode**

In `handleClaudeMessagesInternal`, immediately before or after `normalizeAssistantPrefillForKiro` is applied, detect a final assistant text prefill:

```go
	if len(effectiveReq.Messages) > 0 {
		last := effectiveReq.Messages[len(effectiveReq.Messages)-1]
		if strings.TrimSpace(last.Role) == "assistant" && !finalAssistantMessageHasToolUse(last.Content) {
			text, _ := extractClaudeAssistantContent(last.Content)
			if strings.TrimSpace(text) != "" {
				updateRequestLogAssistantPrefillMode(r, "emulated_text_prefill")
			}
		}
	}
```

Do not record mode for rejected final assistant `tool_use`, because those requests stop before conversion.

- [ ] **Step 5: Verify focused tests pass**

Run:

```bash
go test ./proxy -run 'TestClaudeCountTokensRecordsEstimatedMode|TestClaudeAssistantTextPrefillRecordsEmulatedMode' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat: capture claude readiness evidence modes"
```

## Task 3: Build Layered Readiness Capabilities

**Files:**
- Modify: `proxy/handler.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing layered readiness test**

Add this test near `TestClaudeCodeReadinessReportsPartialCapabilities`:

```go
func TestClaudeCodeReadinessReportsLayeredPartialCapabilities(t *testing.T) {
	h := &Handler{pool: &pool.AccountPool{}, startTime: time.Now().Unix(), requestLogs: newRequestLogStore(10)}
	now := time.Now().UTC()
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                     now,
		RequestID:                     "req-count",
		Endpoint:                      "/v1/messages/count_tokens",
		Model:                         "claude-sonnet-4.5",
		StatusCode:                    200,
		Outcome:                       "success",
		CountTokensMode:               "estimated",
	})
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                     now,
		RequestID:                     "req-zero",
		Endpoint:                      "/v1/messages",
		Model:                         "claude-sonnet-4.5",
		StatusCode:                    200,
		Outcome:                       "success",
		MaxTokensZeroMode:             "local_zero_output",
	})
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                           now,
		RequestID:                           "req-fg",
		Endpoint:                            "/v1/messages",
		Model:                               "claude-sonnet-4.5",
		StatusCode:                          200,
		Outcome:                             "success",
		FineGrainedToolStreamingRequested:   true,
		FineGrainedToolStreamingMode:        "kiro_go_chunked_complete_input",
	})
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:              now,
		RequestID:              "req-prefill",
		Endpoint:               "/v1/messages",
		Model:                  "claude-opus-4.7",
		StatusCode:             200,
		Outcome:                "success",
		AssistantPrefillMode:   "emulated_text_prefill",
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	w := httptest.NewRecorder()

	h.apiGetClaudeCodeReadiness(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	capabilities := resp["capabilities"].(map[string]interface{})
	for _, name := range []string{"countTokens", "maxTokensZero", "fineGrainedToolStreaming", "assistantPrefill"} {
		capability := capabilities[name].(map[string]interface{})
		if capability["status"] != "PARTIAL" {
			t.Fatalf("expected %s top-level PARTIAL, got %#v", name, capability)
		}
		if _, ok := capability["claudeCodeCompatibility"].(map[string]interface{}); !ok {
			t.Fatalf("expected %s claudeCodeCompatibility object, got %#v", name, capability)
		}
		if _, ok := capability["officialAnthropicParity"].(map[string]interface{}); !ok {
			t.Fatalf("expected %s officialAnthropicParity object, got %#v", name, capability)
		}
		if _, ok := capability["evidence"].(map[string]interface{}); !ok {
			t.Fatalf("expected %s evidence object, got %#v", name, capability)
		}
	}
	count := capabilities["countTokens"].(map[string]interface{})
	countCompat := count["claudeCodeCompatibility"].(map[string]interface{})
	if countCompat["status"] != "PASS" || countCompat["mode"] != "estimated" {
		t.Fatalf("expected countTokens compatibility PASS estimated, got %#v", countCompat)
	}
	prefill := capabilities["assistantPrefill"].(map[string]interface{})
	prefillCompat := prefill["claudeCodeCompatibility"].(map[string]interface{})
	if prefillCompat["status"] != "EMULATED_PASS" || prefillCompat["mode"] != "emulated_text_prefill" {
		t.Fatalf("expected assistant prefill emulated compatibility, got %#v", prefillCompat)
	}
	prefillOfficial := prefill["officialAnthropicParity"].(map[string]interface{})
	if prefillOfficial["status"] != "UNSUPPORTED_BY_MODEL" {
		t.Fatalf("expected opus 4.7 prefill unsupported by model, got %#v", prefillOfficial)
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
go test ./proxy -run TestClaudeCodeReadinessReportsLayeredPartialCapabilities -count=1
```

Expected: fail because current capability objects are `map[string]string` and do not include nested layers.

- [ ] **Step 3: Add helper constructors in `proxy/handler.go`**

Add these helper functions near `apiGetClaudeCodeReadiness`:

```go
func layeredCapability(status, detail string, compat map[string]string, official map[string]string, evidence map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"status":                    status,
		"detail":                    detail,
		"claudeCodeCompatibility":   compat,
		"officialAnthropicParity":   official,
	}
	if evidence == nil {
		evidence = map[string]interface{}{}
	}
	out["evidence"] = evidence
	return out
}

func basicCapability(status, detail string) map[string]interface{} {
	return map[string]interface{}{
		"status": status,
		"detail": detail,
	}
}

func readinessEvidence(entry *RequestLogEntry, mode string, proof string) map[string]interface{} {
	if entry == nil {
		return map[string]interface{}{
			"mode":  mode,
			"proof": proof,
		}
	}
	return map[string]interface{}{
		"lastSeenAt":    entry.Timestamp.Format(time.RFC3339),
		"lastRequestId": entry.RequestID,
		"model":         entry.Model,
		"mode":          mode,
		"proof":         proof,
	}
}

func modelDisallowsAssistantPrefill(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "opus-4.7") ||
		strings.Contains(model, "opus-4-7") ||
		strings.Contains(model, "opus-4.6") ||
		strings.Contains(model, "opus-4-6") ||
		strings.Contains(model, "sonnet-4.6") ||
		strings.Contains(model, "sonnet-4-6")
}
```

- [ ] **Step 4: Change readiness capability map type**

In `apiGetClaudeCodeReadiness`, change:

```go
"capabilities": map[string]map[string]string{
```

to:

```go
"capabilities": map[string]interface{}{
```

Replace static entries:

```go
"messages": basicCapability("PASS", "/v1/messages is implemented"),
"toolSchemaValidation": basicCapability("PASS", "Invalid model-emitted tool_use inputs are repaired or suppressed before Claude Code receives them"),
"toolReference": basicCapability("PASS", "tool_reference is accepted and materialized when relevant"),
```

For the four nuanced entries, initialize with layered fallback values:

```go
"countTokens": layeredCapability(
	"PARTIAL",
	"Claude Code compatible estimated token counting; official exact count is not proven",
	map[string]string{"status": "PASS", "mode": "estimated", "proof": "count_tokens endpoint is implemented"},
	map[string]string{"status": "PARTIAL", "mode": "estimated", "proof": "no upstream exact count_tokens evidence"},
	readinessEvidence(nil, "estimated", "no recent count_tokens request in readiness window"),
),
"maxTokensZero": layeredCapability(
	"PARTIAL",
	"Claude Code compatible zero-output response; official cache warmup is not proven",
	map[string]string{"status": "PASS", "mode": "local_zero_output", "proof": "local zero-output response shape is implemented"},
	map[string]string{"status": "BLOCKED_BY_UPSTREAM", "mode": "local_zero_output", "proof": "no upstream cache warmup evidence"},
	readinessEvidence(nil, "local_zero_output", "no recent max_tokens=0 request in readiness window"),
),
"assistantPrefill": layeredCapability(
	"PARTIAL",
	"Text prefill is emulated as a continuation instruction; tool-use prefill is rejected",
	map[string]string{"status": "EMULATED_PASS", "mode": "emulated_text_prefill", "proof": "text prefill conversion is implemented"},
	map[string]string{"status": "PARTIAL", "mode": "emulated_text_prefill", "proof": "native upstream prefill is not proven"},
	readinessEvidence(nil, "emulated_text_prefill", "no recent assistant text prefill request in readiness window"),
),
"fineGrainedToolStreaming": layeredCapability(
	"PARTIAL",
	"Claude Code compatible input_json_delta events are emitted; true upstream partial JSON parity depends on Kiro stream shape",
	map[string]string{"status": "PASS", "mode": "kiro_go_chunked_complete_input", "proof": "Anthropic SSE input_json_delta writer is implemented"},
	map[string]string{"status": "PARTIAL", "mode": "kiro_go_chunked_complete_input", "proof": "upstream partial tool input deltas are not proven"},
	readinessEvidence(nil, "kiro_go_chunked_complete_input", "no recent fine-grained tool stream in readiness window"),
),
```

- [ ] **Step 5: Track recent evidence while scanning logs**

Before the loop over logs, add:

```go
var recentCountTokens, recentMaxTokensZero, recentFineGrained, recentAssistantPrefill *RequestLogEntry
```

Inside the loop, after existing signal checks, add:

```go
entryCopy := entry
if entry.CountTokensMode != "" && recentCountTokens == nil {
	recentCountTokens = &entryCopy
}
if entry.MaxTokensZeroMode != "" && recentMaxTokensZero == nil {
	recentMaxTokensZero = &entryCopy
}
if entry.FineGrainedToolStreamingMode != "" && recentFineGrained == nil {
	recentFineGrained = &entryCopy
}
if entry.AssistantPrefillMode != "" && recentAssistantPrefill == nil {
	recentAssistantPrefill = &entryCopy
}
```

After the loop and before encoding response, update the four capability objects from evidence:

```go
capabilities := resp["capabilities"].(map[string]interface{})
if recentCountTokens != nil {
	capabilities["countTokens"] = layeredCapability(
		"PARTIAL",
		"Claude Code compatible estimated token counting; official exact count is not proven",
		map[string]string{"status": "PASS", "mode": recentCountTokens.CountTokensMode, "proof": "count_tokens endpoint returned input_tokens"},
		map[string]string{"status": "PARTIAL", "mode": recentCountTokens.CountTokensMode, "proof": "no upstream exact count_tokens evidence"},
		readinessEvidence(recentCountTokens, recentCountTokens.CountTokensMode, "recent count_tokens request completed"),
	)
}
if recentMaxTokensZero != nil {
	officialStatus := "BLOCKED_BY_UPSTREAM"
	officialProof := "no upstream cache warmup evidence"
	if recentMaxTokensZero.CacheCreationInputTokens > 0 || recentMaxTokensZero.CacheReadInputTokens > 0 {
		officialStatus = "PASS"
		officialProof = "upstream cache usage tokens were observed"
	}
	capabilities["maxTokensZero"] = layeredCapability(
		"PARTIAL",
		"Claude Code compatible zero-output response; official cache warmup is not proven",
		map[string]string{"status": "PASS", "mode": recentMaxTokensZero.MaxTokensZeroMode, "proof": "zero-output response shape completed"},
		map[string]string{"status": officialStatus, "mode": recentMaxTokensZero.MaxTokensZeroMode, "proof": officialProof},
		readinessEvidence(recentMaxTokensZero, recentMaxTokensZero.MaxTokensZeroMode, "recent max_tokens=0 request completed"),
	)
}
if recentFineGrained != nil {
	capabilities["fineGrainedToolStreaming"] = layeredCapability(
		"PARTIAL",
		"Claude Code compatible input_json_delta events are emitted; true upstream partial JSON parity depends on Kiro stream shape",
		map[string]string{"status": "PASS", "mode": recentFineGrained.FineGrainedToolStreamingMode, "proof": "recent request asked for fine-grained tool streaming"},
		map[string]string{"status": "PARTIAL", "mode": recentFineGrained.FineGrainedToolStreamingMode, "proof": "upstream partial tool input deltas are not proven"},
		readinessEvidence(recentFineGrained, recentFineGrained.FineGrainedToolStreamingMode, "recent fine-grained tool-stream request observed"),
	)
}
if recentAssistantPrefill != nil {
	officialStatus := "PARTIAL"
	officialProof := "native upstream prefill is not proven"
	if modelDisallowsAssistantPrefill(recentAssistantPrefill.Model) {
		officialStatus = "UNSUPPORTED_BY_MODEL"
		officialProof = "official model family does not support assistant prefill"
	}
	capabilities["assistantPrefill"] = layeredCapability(
		"PARTIAL",
		"Text prefill is emulated as a continuation instruction; tool-use prefill is rejected",
		map[string]string{"status": "EMULATED_PASS", "mode": recentAssistantPrefill.AssistantPrefillMode, "proof": "recent assistant text prefill was converted"},
		map[string]string{"status": officialStatus, "mode": recentAssistantPrefill.AssistantPrefillMode, "proof": officialProof},
		readinessEvidence(recentAssistantPrefill, recentAssistantPrefill.AssistantPrefillMode, "recent assistant text prefill request observed"),
	)
}
```

- [ ] **Step 6: Verify layered readiness test passes**

Run:

```bash
go test ./proxy -run 'TestClaudeCodeReadinessReportsLayeredPartialCapabilities|TestClaudeCodeReadinessReportsPartialCapabilities|TestClaudeCodeReadinessIncludesNewParitySignals' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat: expose layered claude code readiness"
```

## Task 4: Render Layered Readiness In Admin UI

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Add UI rendering support for nested statuses**

In `renderClaudeCodeReadiness`, replace the `capabilityHtml` block mapping with:

```javascript
            const capabilityHtml = Object.keys(capabilities).sort().map(key => {
                const capability = capabilities[key] || {};
                const compat = capability.claudeCodeCompatibility || null;
                const official = capability.officialAnthropicParity || null;
                const evidence = capability.evidence || null;
                const layer = (label, value) => {
                    if (!value) return '';
                    const mode = value.mode ? ' · ' + escapeHtml(value.mode) : '';
                    const proof = value.proof ? '<div style="font-size:11px;color:#64748b;margin-top:2px">' + escapeHtml(value.proof) + '</div>' : '';
                    return '<div style="display:flex;gap:8px;align-items:flex-start;justify-content:space-between;margin-top:6px">' +
                        '<div style="font-size:12px;color:#475569;min-width:0"><strong>' + escapeHtml(label) + '</strong>' + mode + proof + '</div>' +
                        renderCapabilityBadge(value.status) +
                        '</div>';
                };
                const evidenceText = evidence && (evidence.lastRequestId || evidence.mode || evidence.proof)
                    ? '<div style="font-size:11px;color:#64748b;margin-top:6px">Evidence: ' +
                        escapeHtml([evidence.lastRequestId, evidence.model, evidence.mode].filter(Boolean).join(' · ') || evidence.proof || '-') +
                      '</div>'
                    : '';
                return '<div style="padding:8px 0;border-top:1px solid #e2e8f0">' +
                    '<div style="display:flex;gap:8px;align-items:center;justify-content:space-between;flex-wrap:wrap">' +
                    '<span style="font-size:13px;font-weight:600;color:#334155">' + escapeHtml(key) + '</span>' +
                    renderCapabilityBadge(capability.status) +
                    '</div>' +
                    '<div style="font-size:12px;color:#64748b;margin-top:4px;line-height:1.4">' + escapeHtml(capability.detail || '-') + '</div>' +
                    layer('Claude Code', compat) +
                    layer('Official API', official) +
                    evidenceText +
                    '</div>';
            }).join('');
```

- [ ] **Step 2: Add badge styling for new statuses**

In `renderCapabilityBadge`, update status classes:

```javascript
            if (status === 'PASS' || status === 'EMULATED_PASS') cls = 'badge-success';
            else if (status === 'PARTIAL' || status === 'UNSUPPORTED_BY_MODEL' || status === 'BLOCKED_BY_UPSTREAM') cls = 'badge-warning';
```

- [ ] **Step 3: Syntax check the HTML**

Run:

```bash
node --check <(awk '/<script>/{flag=1;next}/<\/script>/{flag=0}flag' web/index.html)
```

Expected: no syntax errors.

- [ ] **Step 4: Commit**

```bash
git add web/index.html
git commit -m "feat: show layered claude readiness in admin ui"
```

## Task 5: Run Focused Regression Tests

**Files:**
- No source file changes expected.

- [ ] **Step 1: Run proxy focused tests**

Run:

```bash
go test ./proxy -run 'ClaudeCodeReadiness|CountTokens|MaxTokensZero|FineGrained|AssistantTextPrefill|AssistantPrefill|RequestLogCapturesClaudeParityModes' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run pool tests touched by current worktree**

Run:

```bash
go test ./pool -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full Go tests if focused tests pass**

Run:

```bash
go test ./...
```

Expected: PASS. If unrelated existing tests fail, capture the failing package, test name, and error in UAT notes without reverting unrelated work.

## Task 6: Docker and Real Integration UAT

**Files:**
- Create or update: `docs/superpowers/uat/kiro-layered-readiness-20260519/UAT-RESULT.md`

- [ ] **Step 1: Rebuild and start Kiro-Go Docker service**

Use the repository's existing Docker deployment command from current project scripts or compose files. If the service is already running, rebuild the Kiro-Go image and restart only Kiro-Go.

Expected: Kiro-Go health endpoint returns HTTP 200.

- [ ] **Step 2: Verify direct readiness JSON**

Run a curl request against:

```bash
curl -sS -H "X-Admin-Password: $KIRO_ADMIN_PASSWORD" "$KIRO_BASE_URL/admin/api/claude-code/readiness"
```

Expected:

- each of `assistantPrefill`, `countTokens`, `fineGrainedToolStreaming`, `maxTokensZero` still has top-level `status`.
- each has `claudeCodeCompatibility`.
- each has `officialAnthropicParity`.
- each has `evidence`.

- [ ] **Step 3: Verify direct Kiro-Go API paths**

Run direct:

- non-stream `/v1/messages`.
- stream `/v1/messages`.
- `/v1/messages/count_tokens`.
- `max_tokens=0` `/v1/messages`.

Expected: all return valid Anthropic-shaped responses. The count-token request updates `countTokensMode=estimated`; the zero-output request updates `maxTokensZeroMode=local_zero_output`.

- [ ] **Step 4: Verify sub2api paths**

Run through `/www/sub2api/` using its existing configured upstream:

- non-stream request.
- stream request.
- count-token request if sub2api exposes the endpoint.

Expected: requests complete successfully and Kiro-Go request logs show traffic from the downstream path.

- [ ] **Step 5: Browser verification**

Use available Playwright tooling to open the Kiro-Go admin UI, refresh Claude Code readiness, and capture a screenshot.

Expected screenshot analysis:

- each nuanced capability row shows the top-level status.
- each row shows `Claude Code` and `Official API` sub-statuses.
- `PARTIAL` reasons remain visible.
- no text overlap or clipped status labels.

- [ ] **Step 6: Write UAT result**

Create `docs/superpowers/uat/kiro-layered-readiness-20260519/UAT-RESULT.md` with:

- commit under test.
- Docker health evidence.
- direct API evidence.
- sub2api evidence.
- readiness JSON excerpts.
- browser screenshot path and analysis.
- final verdict.

Expected final verdict: PASS only if API, sub2api, and browser evidence all pass. If upstream Kiro returns `INSUFFICIENT_MODEL_CAPACITY`, record it separately as upstream capacity and do not claim full live model-response PASS for that run.

## Self-Review

- Spec coverage: Tasks cover layered readiness, request evidence, UI rendering, tests, direct API UAT, sub2api UAT, and browser screenshot analysis.
- Placeholder scan: No task uses TBD/TODO or asks the implementer to invent unspecified behavior.
- Type consistency: New fields are `CountTokensMode` and `AssistantPrefillMode`; JSON fields are `countTokensMode` and `assistantPrefillMode`; readiness fields are `claudeCodeCompatibility`, `officialAnthropicParity`, and `evidence`.
