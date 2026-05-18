# Claude Code Full Official Parity Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve Kiro-Go's Claude Code/Anthropic API parity across P0, P1, and P2 while preserving the real `/www/sub2api` downstream integration.

**Architecture:** Add compatibility behavior at the Claude envelope/normalization/conversion boundary, then expose all degradation and readiness decisions through request logs and the existing admin UI. Keep routing and account scheduling intact; use `/www/sub2api` only as a real verification target.

**Tech Stack:** Go 1.21, standard `testing`, Docker Compose, static admin UI in `web/index.html`, Playwright browser UAT, PostgreSQL/Redis-backed `/www/sub2api` deployment.

---

## Ground Rules

- Modify only `/www/Kiro-Go`.
- Do not edit `/www/sub2api` source files.
- Do not run `docker compose down -v`.
- Before touching any file with existing local changes, run:

```bash
git diff -- <path>
```

- Preserve unrelated user changes.
- Use TDD for behavior changes.
- Commit each task separately.
- Do not print API keys, admin passwords, tokens, or account secrets in logs or UAT artifacts.

## File Structure

- `proxy/anthropic_envelope.go`: parse Claude request body, Claude Code headers, beta/version metadata, and unknown official field keys.
- `proxy/request_log.go`: store request metadata and payload compatibility diagnostics.
- `proxy/translator.go`: normalize Claude messages and convert them into Kiro payloads.
- `proxy/payload_guard.go`: retain final safety trimming and summary accounting.
- `proxy/handler.go`: route metadata into logs, validate client-facing errors, expose admin readiness/model APIs.
- `proxy/*_test.go`: regression tests for envelope, translator, guard, handler, logs, and token estimator.
- `web/index.html`: show readiness/log/model diagnostics in the existing admin surface.
- `docs/superpowers/uat/`: store real UAT scripts, screenshots, JSON, and summary evidence.

---

### Task 1: Capture Claude Code Parent Agent And Official Field Metadata

**Files:**
- Modify: `proxy/anthropic_envelope.go`
- Modify: `proxy/anthropic_envelope_test.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`

- [ ] **Step 1: Inspect current diffs**

Run:

```bash
git diff -- proxy/anthropic_envelope.go proxy/anthropic_envelope_test.go proxy/request_log.go proxy/request_log_test.go
```

Expected: review existing local changes and preserve them.

- [ ] **Step 2: Write failing envelope test**

Add this test to `proxy/anthropic_envelope_test.go`:

```go
func TestParseAnthropicEnvelopeCapturesParentAgentAndOfficialExtras(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4.5",
		"max_tokens":64,
		"messages":[{"role":"user","content":"hello"}],
		"container":{"id":"container_1"},
		"context_management":{"clear_function_results":true},
		"mcp_servers":[{"name":"repo"}],
		"service_tier":"standard_only",
		"metadata":{"user_id":"u1"},
		"stop_sequences":["END"]
	}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	r.Header.Set("X-Claude-Code-Session-Id", "session_1")
	r.Header.Set("X-Claude-Code-Agent-Id", "agent_1")
	r.Header.Set("X-Claude-Code-Parent-Agent-Id", "parent_1")
	r.Header.Set("anthropic-beta", "fine-grained-tool-streaming-2025-05-14,context-management-2025-06-27")

	env, err := parseAnthropicEnvelope(r, body)
	if err != nil {
		t.Fatalf("parseAnthropicEnvelope returned error: %v", err)
	}
	if env.ParentAgentID != "parent_1" {
		t.Fatalf("expected parent agent id, got %q", env.ParentAgentID)
	}
	want := "container,context_management,mcp_servers,metadata,service_tier,stop_sequences"
	if got := strings.Join(env.OfficialExtraKeys, ","); got != want {
		t.Fatalf("expected official extra keys %q, got %q", want, got)
	}
	if !env.HasBeta("fine-grained-tool-streaming-2025-05-14") {
		t.Fatalf("expected fine-grained beta to be parsed")
	}
}
```

Ensure imports include:

```go
import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)
```

- [ ] **Step 3: Run failing envelope test**

Run:

```bash
go test ./proxy -run TestParseAnthropicEnvelopeCapturesParentAgentAndOfficialExtras -count=1 -v
```

Expected: FAIL because `ParentAgentID` and `OfficialExtraKeys` do not exist.

- [ ] **Step 4: Implement envelope metadata**

In `proxy/anthropic_envelope.go`, extend `anthropicEnvelope`:

```go
	ParentAgentID    string
	OfficialExtraKeys []string
```

Add helper:

```go
func officialAnthropicExtraKeys(raw map[string]json.RawMessage) []string {
	official := map[string]bool{
		"container":          true,
		"context_management": true,
		"mcp_servers":        true,
		"service_tier":       true,
		"metadata":           true,
		"stop_sequences":     true,
		"cache_control":      true,
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		if official[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
```

Add `sort` to imports. In `parseAnthropicEnvelope`, before returning, set:

```go
officialExtraKeys := officialAnthropicExtraKeys(raw)
```

and in the returned struct:

```go
ParentAgentID:     firstNonEmptyHeader(r, "x-claude-code-parent-agent-id", "x-claude-parent-agent-id"),
OfficialExtraKeys: officialExtraKeys,
```

- [ ] **Step 5: Run envelope test**

Run:

```bash
go test ./proxy -run TestParseAnthropicEnvelopeCapturesParentAgentAndOfficialExtras -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Write failing request-log test**

Add this test to `proxy/request_log_test.go`:

```go
func TestRequestLogCapturesParentAgentAndOfficialExtras(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(10)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("X-Claude-Code-Session-Id", "session_1")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent_1")
	req.Header.Set("X-Claude-Code-Parent-Agent-Id", "parent_1")
	rr := httptest.NewRecorder()

	ctx, loggedReq, recorder, _ := h.beginRequestLog(rr, req)
	if ctx == nil || recorder == nil {
		t.Fatalf("expected request log context")
	}
	env := &anthropicEnvelope{
		Request:           ClaudeRequest{Model: "claude-sonnet-4.5"},
		SessionID:         "session_1",
		AgentID:           "agent_1",
		ParentAgentID:     "parent_1",
		AnthropicVersion:  "2023-06-01",
		BetaHeader:        "fine-grained-tool-streaming-2025-05-14",
		Betas:             parseAnthropicBetas("fine-grained-tool-streaming-2025-05-14"),
		OfficialExtraKeys: []string{"container", "mcp_servers"},
	}
	updateRequestLogAnthropic(loggedReq, env)
	h.finishRequestLog(loggedReq, recorder)

	entries := h.requestLogs.List(1)
	if len(entries) != 1 {
		t.Fatalf("expected one log entry")
	}
	entry := entries[0]
	if entry.ClaudeCodeParentAgentID != "parent_1" {
		t.Fatalf("expected parent agent id, got %#v", entry)
	}
	if got := strings.Join(entry.PayloadUnknownOfficialFields, ","); got != "container,mcp_servers" {
		t.Fatalf("expected official extra fields, got %#v", entry.PayloadUnknownOfficialFields)
	}
	if !entry.FineGrainedToolStreamingRequested || entry.FineGrainedToolStreamingMode != "requested_partial" {
		t.Fatalf("expected fine-grained telemetry, got %#v", entry)
	}
}
```

- [ ] **Step 7: Run failing request-log test**

Run:

```bash
go test ./proxy -run TestRequestLogCapturesParentAgentAndOfficialExtras -count=1 -v
```

Expected: FAIL because request log fields do not exist.

- [ ] **Step 8: Implement request-log fields**

In `proxy/request_log.go`, add fields to `RequestLogEntry`:

```go
	ClaudeCodeParentAgentID          string   `json:"claudeCodeParentAgentId,omitempty"`
	PayloadUnknownOfficialFields     []string `json:"payloadUnknownOfficialFields,omitempty"`
	FineGrainedToolStreamingRequested bool    `json:"fineGrainedToolStreamingRequested,omitempty"`
	FineGrainedToolStreamingMode      string   `json:"fineGrainedToolStreamingMode,omitempty"`
```

In `beginRequestLog`, set:

```go
ClaudeCodeParentAgentID: firstNonEmptyHeader(r, "X-Claude-Code-Parent-Agent-Id", "X-Claude-Parent-Agent-Id"),
```

In `updateRequestLogAnthropic`, set:

```go
ctx.entry.ClaudeCodeParentAgentID = env.ParentAgentID
ctx.entry.PayloadUnknownOfficialFields = append([]string(nil), env.OfficialExtraKeys...)
if env.HasBeta("fine-grained-tool-streaming-2025-05-14") {
	ctx.entry.FineGrainedToolStreamingRequested = true
	ctx.entry.FineGrainedToolStreamingMode = "requested_partial"
}
```

If `firstNonEmptyHeader` is not available in this file, add a local helper or reuse the existing one if package-visible.

- [ ] **Step 9: Run request-log tests**

Run:

```bash
go test ./proxy -run 'TestParseAnthropicEnvelopeCapturesParentAgentAndOfficialExtras|TestRequestLogCapturesParentAgentAndOfficialExtras' -count=1 -v
```

Expected: PASS.

- [ ] **Step 10: Commit**

Run:

```bash
git add proxy/anthropic_envelope.go proxy/anthropic_envelope_test.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: capture claude code official metadata"
```

---

### Task 2: Normalize Same-Role Claude Messages Before Kiro Conversion

**Files:**
- Modify: `proxy/translator.go`
- Modify: `proxy/translator_test.go`

- [ ] **Step 1: Inspect current diffs**

Run:

```bash
git diff -- proxy/translator.go proxy/translator_test.go
```

Expected: review existing local changes and preserve them.

- [ ] **Step 2: Write failing same-role merge test**

Add this test to `proxy/translator_test.go`:

```go
func TestClaudeToKiroMergesAdjacentSameRoleMessages(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 128,
		Tools: []ClaudeTool{{
			Name:        "bash",
			Description: "Run shell commands",
			InputSchema: map[string]interface{}{"type": "object"},
		}},
		Messages: []ClaudeMessage{
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "text", "text": "first user"},
				map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_1", "content": "first result"},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "text", "text": "second user"},
				map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_2", "content": "second result"},
			}},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "text", "text": "assistant text"},
				map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "bash", "input": map[string]interface{}{"command": "pwd"}},
			}},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "toolu_2", "name": "bash", "input": map[string]interface{}{"command": "ls"}},
			}},
			{Role: "user", Content: "final question"},
		},
	}

	payload := ClaudeToKiro(req, false)
	if len(payload.ConversationState.History) < 2 {
		t.Fatalf("expected merged history, got %#v", payload.ConversationState.History)
	}
	firstUser := payload.ConversationState.History[0].UserInputMessage
	if firstUser == nil || !strings.Contains(firstUser.Content, "first user") || !strings.Contains(firstUser.Content, "second user") {
		t.Fatalf("expected adjacent user text to merge, got %#v", firstUser)
	}
	ctx := firstUser.UserInputMessageContext
	if ctx == nil || len(ctx.ToolResults) != 2 {
		t.Fatalf("expected merged tool results, got %#v", ctx)
	}
	assistant := payload.ConversationState.History[1].AssistantResponseMessage
	if assistant == nil || len(assistant.ToolUses) != 2 {
		t.Fatalf("expected merged assistant tool uses, got %#v", assistant)
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestClaudeToKiroMergesAdjacentSameRoleMessages -count=1 -v
```

Expected: FAIL because adjacent same-role turns are not explicitly merged with all tool content preserved.

- [ ] **Step 4: Implement normalization helper**

In `proxy/translator.go`, add:

```go
func normalizeClaudeMessagesForKiro(messages []ClaudeMessage) []ClaudeMessage {
	if len(messages) == 0 {
		return nil
	}
	normalized := make([]ClaudeMessage, 0, len(messages))
	for _, msg := range messages {
		if len(normalized) == 0 || normalized[len(normalized)-1].Role != msg.Role {
			normalized = append(normalized, msg)
			continue
		}
		last := &normalized[len(normalized)-1]
		last.Content = mergeClaudeContent(last.Content, msg.Content)
	}
	return normalized
}

func mergeClaudeContent(a, b interface{}) interface{} {
	aBlocks := claudeContentAsBlocks(a)
	bBlocks := claudeContentAsBlocks(b)
	if len(aBlocks) == 0 {
		return b
	}
	if len(bBlocks) == 0 {
		return a
	}
	return append(aBlocks, bBlocks...)
}

func claudeContentAsBlocks(content interface{}) []interface{} {
	switch v := content.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []interface{}{map[string]interface{}{"type": "text", "text": v}}
	case []interface{}:
		return append([]interface{}(nil), v...)
	default:
		return []interface{}{map[string]interface{}{"type": "text", "text": fmt.Sprint(v)}}
	}
}
```

In `ClaudeToKiro`, iterate over:

```go
messages := normalizeClaudeMessagesForKiro(req.Messages)
for i, msg := range messages {
```

and update `firstClaudeConversationAnchor(req.Messages)` to use `messages`.

- [ ] **Step 5: Run focused and existing translator tests**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroMergesAdjacentSameRoleMessages|TestClaudeToKiro' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add proxy/translator.go proxy/translator_test.go
git commit -m "feat: merge same-role claude messages"
```

---

### Task 3: Preserve Orphaned Tool Results As Text

**Files:**
- Modify: `proxy/translator.go`
- Modify: `proxy/translator_test.go`
- Modify: `proxy/kiro.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/request_log.go proxy/request_log_test.go
```

- [ ] **Step 2: Write failing translator test**

Add to `proxy/translator_test.go`:

```go
func TestClaudeToKiroConvertsOrphanedToolResultToText(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 128,
		Messages: []ClaudeMessage{
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "missing_tool", "content": "important orphan output"},
			}},
		},
	}

	payload := ClaudeToKiro(req, false)
	current := payload.ConversationState.CurrentMessage.UserInputMessage
	if !strings.Contains(current.Content, "[Tool Result (missing_tool)]") || !strings.Contains(current.Content, "important orphan output") {
		t.Fatalf("expected orphaned tool result text in current content, got %q", current.Content)
	}
	if current.UserInputMessageContext != nil && len(current.UserInputMessageContext.ToolResults) > 0 {
		t.Fatalf("expected orphaned tool result not to be sent as Kiro toolResult, got %#v", current.UserInputMessageContext)
	}
	if payload.OrphanedToolResultsConverted != 1 {
		t.Fatalf("expected orphan conversion metric, got %d", payload.OrphanedToolResultsConverted)
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestClaudeToKiroConvertsOrphanedToolResultToText -count=1 -v
```

Expected: FAIL because the metric and conversion do not exist.

- [ ] **Step 4: Add Kiro payload metric**

In `proxy/kiro.go`, add non-JSON internal fields to `KiroPayload`:

```go
	OrphanedToolResultsConverted int `json:"-"`
```

- [ ] **Step 5: Implement orphan conversion**

In `proxy/translator.go`, add:

```go
func knownClaudeToolUseIDs(messages []ClaudeMessage, beforeIndex int) map[string]bool {
	ids := map[string]bool{}
	for i := 0; i < beforeIndex && i < len(messages); i++ {
		if messages[i].Role != "assistant" {
			continue
		}
		_, toolUses := extractClaudeAssistantContent(messages[i].Content)
		for _, tu := range toolUses {
			if strings.TrimSpace(tu.ToolUseID) != "" {
				ids[tu.ToolUseID] = true
			}
		}
	}
	return ids
}

func splitOrphanedToolResults(content string, toolResults []KiroToolResult, known map[string]bool) (string, []KiroToolResult, int) {
	if len(toolResults) == 0 {
		return content, toolResults, 0
	}
	kept := make([]KiroToolResult, 0, len(toolResults))
	parts := []string{}
	if strings.TrimSpace(content) != "" {
		parts = append(parts, strings.TrimSpace(content))
	}
	converted := 0
	for _, result := range toolResults {
		id := strings.TrimSpace(result.ToolUseID)
		if id != "" && known[id] {
			kept = append(kept, result)
			continue
		}
		parts = append(parts, formatToolResultAsText(result))
		converted++
	}
	return strings.Join(parts, "\n\n"), kept, converted
}

func formatToolResultAsText(result KiroToolResult) string {
	id := strings.TrimSpace(result.ToolUseID)
	textParts := make([]string, 0, len(result.Content))
	for _, c := range result.Content {
		if strings.TrimSpace(c.Text) != "" {
			textParts = append(textParts, c.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(textParts, "\n"))
	if text == "" {
		text = "(empty result)"
	}
	if id == "" {
		return "[Tool Result]\n" + text
	}
	return "[Tool Result (" + id + ")]\n" + text
}
```

In `ClaudeToKiro`, after `content, images, toolResults := extractClaudeUserContent(msg.Content)`, call:

```go
known := knownClaudeToolUseIDs(messages, i)
var converted int
content, toolResults, converted = splitOrphanedToolResults(content, toolResults, known)
orphanedToolResultsConverted += converted
```

Declare `orphanedToolResultsConverted := 0` before the loop, and set:

```go
payload.OrphanedToolResultsConverted = orphanedToolResultsConverted
```

- [ ] **Step 6: Run focused test**

Run:

```bash
go test ./proxy -run TestClaudeToKiroConvertsOrphanedToolResultToText -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Write failing request-log metric test**

Add to `proxy/request_log_test.go`:

```go
func TestRequestLogCapturesOrphanedToolResultConversions(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	h := &Handler{requestLogs: newRequestLogStore(10)}
	_, loggedReq, recorder, _ := h.beginRequestLog(rr, req)

	updateRequestLogPayload(loggedReq, payloadGuardResult{
		Summary: kiroPayloadSummary{},
		OrphanedToolResultsConverted: 2,
	})
	h.finishRequestLog(loggedReq, recorder)

	entry := h.requestLogs.List(1)[0]
	if entry.PayloadOrphanedToolResultsConverted != 2 {
		t.Fatalf("expected orphaned tool result conversion metric, got %#v", entry)
	}
}
```

- [ ] **Step 8: Add metric to guard result and request log**

In `payloadGuardResult`, add:

```go
	OrphanedToolResultsConverted int
```

In `prepareGuardedKiroPayload` or before `updateRequestLogPayload`, copy:

```go
result.OrphanedToolResultsConverted = payload.OrphanedToolResultsConverted
```

In `RequestLogEntry`, add:

```go
	PayloadOrphanedToolResultsConverted int `json:"payloadOrphanedToolResultsConverted,omitempty"`
```

In `updateRequestLogPayload`, set:

```go
ctx.entry.PayloadOrphanedToolResultsConverted = result.OrphanedToolResultsConverted
```

- [ ] **Step 9: Run tests**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroConvertsOrphanedToolResultToText|TestRequestLogCapturesOrphanedToolResultConversions' -count=1 -v
```

Expected: PASS.

- [ ] **Step 10: Commit**

Run:

```bash
git add proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/payload_guard.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: preserve orphaned tool results"
```

---

### Task 4: Extract Images Nested In Tool Results

**Files:**
- Modify: `proxy/translator.go`
- Modify: `proxy/translator_test.go`
- Modify: `proxy/kiro.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/request_log.go proxy/request_log_test.go
```

- [ ] **Step 2: Write failing image extraction test**

Add to `proxy/translator_test.go`:

```go
func TestClaudeToKiroExtractsImagesInsideToolResultContent(t *testing.T) {
	imageData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB"
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 128,
		Messages: []ClaudeMessage{
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "toolu_screen", "name": "mcp__browser__screenshot", "input": map[string]interface{}{}},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": "toolu_screen",
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "screenshot captured"},
						map[string]interface{}{
							"type": "image",
							"source": map[string]interface{}{
								"type":       "base64",
								"media_type": "image/png",
								"data":       imageData,
							},
						},
					},
				},
			}},
		},
	}

	payload := ClaudeToKiro(req, false)
	images := payload.ConversationState.CurrentMessage.UserInputMessage.Images
	if len(images) != 1 {
		t.Fatalf("expected one extracted tool-result image, got %#v", images)
	}
	if images[0].Format != "png" || images[0].Source.Bytes != imageData {
		t.Fatalf("unexpected image: %#v", images[0])
	}
	if payload.ToolResultImages != 1 {
		t.Fatalf("expected image metric, got %d", payload.ToolResultImages)
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestClaudeToKiroExtractsImagesInsideToolResultContent -count=1 -v
```

Expected: FAIL because nested tool-result images are not promoted.

- [ ] **Step 4: Add Kiro payload metric**

In `proxy/kiro.go`, add to `KiroPayload`:

```go
	ToolResultImages int `json:"-"`
```

- [ ] **Step 5: Implement nested image extraction**

In `proxy/translator.go`, change `extractClaudeUserContent` so `tool_result` handles both text and images:

```go
case "tool_result":
	toolUseID, _ := block["tool_use_id"].(string)
	resultContent := extractToolResultContent(block["content"])
	if nestedImages := extractImagesFromToolResultContent(block["content"]); len(nestedImages) > 0 {
		images = append(images, nestedImages...)
	}
	toolResults = append(toolResults, KiroToolResult{
		ToolUseID: toolUseID,
		Content:   []KiroResultContent{{Text: resultContent}},
		Status:    "success",
	})
```

Add:

```go
func extractImagesFromToolResultContent(content interface{}) []KiroImage {
	blocks, ok := content.([]interface{})
	if !ok {
		return nil
	}
	images := make([]KiroImage, 0)
	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "image", "image_url", "input_image":
			if img := extractImageFromClaudeBlock(block); img != nil {
				images = append(images, *img)
			}
		}
	}
	return images
}
```

In `ClaudeToKiro`, track the difference in image count caused by tool results. A simple implementation is to have `extractClaudeUserContent` return a fourth value:

```go
func extractClaudeUserContent(content interface{}) (string, []KiroImage, []KiroToolResult, int)
```

and increment `toolResultImageCount` in the `tool_result` branch. Update all call sites.

Set:

```go
payload.ToolResultImages = toolResultImageCount
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroExtractsImagesInsideToolResultContent|TestClaudeToKiro.*Image|TestEstimateClaudeRequestInputTokens' -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Add request-log metric**

In `RequestLogEntry`, add:

```go
	PayloadToolResultImages int `json:"payloadToolResultImages,omitempty"`
```

In `payloadGuardResult`, add:

```go
	ToolResultImages int
```

Copy from payload into guard result and into request log:

```go
ctx.entry.PayloadToolResultImages = result.ToolResultImages
```

Add a request-log test similar to Task 3, asserting `PayloadToolResultImages == 1`.

- [ ] **Step 8: Run tests**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroExtractsImagesInsideToolResultContent|TestRequestLogCaptures.*ToolResultImages' -count=1 -v
```

Expected: PASS.

- [ ] **Step 9: Commit**

Run:

```bash
git add proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/payload_guard.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: extract tool result images"
```

---

### Task 5: Relocate Long Tool Descriptions Before Truncation

**Files:**
- Modify: `proxy/translator.go`
- Modify: `proxy/translator_test.go`
- Modify: `proxy/kiro.go`
- Modify: `proxy/payload_guard.go`
- Modify: `proxy/payload_guard_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/payload_guard.go proxy/payload_guard_test.go
```

- [ ] **Step 2: Write failing relocation test**

Add to `proxy/translator_test.go`:

```go
func TestClaudeToKiroRelocatesLongToolDescriptionsToContext(t *testing.T) {
	longDescription := strings.Repeat("Detailed usage guidance for the browser tool. ", 80)
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 128,
		Tools: []ClaudeTool{{
			Name:        "mcp__browser__screenshot",
			Description: longDescription,
			InputSchema: map[string]interface{}{"type": "object"},
		}},
		Messages: []ClaudeMessage{{Role: "user", Content: "use screenshot tool"}},
	}

	payload := ClaudeToKiro(req, false)
	current := payload.ConversationState.CurrentMessage.UserInputMessage
	if !strings.Contains(current.Content, "Operator tool documentation for this session") {
		t.Fatalf("expected relocated tool docs in current context, got %q", current.Content)
	}
	if !strings.Contains(current.Content, longDescription[:80]) {
		t.Fatalf("expected full long description in context, got %q", current.Content)
	}
	ctx := current.UserInputMessageContext
	if ctx == nil || len(ctx.Tools) != 1 {
		t.Fatalf("expected tool in current context, got %#v", ctx)
	}
	desc := ctx.Tools[0].ToolSpecification.Description
	if len(desc) > 220 || !strings.Contains(desc, "Full documentation") {
		t.Fatalf("expected short reference description, got %q", desc)
	}
	if payload.RelocatedToolDescriptions != 1 {
		t.Fatalf("expected relocation metric, got %d", payload.RelocatedToolDescriptions)
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestClaudeToKiroRelocatesLongToolDescriptionsToContext -count=1 -v
```

Expected: FAIL.

- [ ] **Step 4: Add metric and relocation helper**

In `KiroPayload`, add:

```go
	RelocatedToolDescriptions int `json:"-"`
```

In `proxy/translator.go`, add:

```go
const toolDescriptionRelocationThreshold = 1024

type toolDescriptionRelocation struct {
	Name        string
	Description string
}

func relocateLongClaudeToolDescriptions(tools []ClaudeTool) ([]ClaudeTool, []toolDescriptionRelocation) {
	if len(tools) == 0 {
		return tools, nil
	}
	out := make([]ClaudeTool, len(tools))
	copy(out, tools)
	relocated := make([]toolDescriptionRelocation, 0)
	for i := range out {
		desc := strings.TrimSpace(out[i].Description)
		if len(desc) <= toolDescriptionRelocationThreshold {
			continue
		}
		relocated = append(relocated, toolDescriptionRelocation{Name: out[i].Name, Description: desc})
		out[i].Description = "[Full documentation provided in session context under Tool: " + out[i].Name + "]"
	}
	return out, relocated
}

func buildRelocatedToolDocumentation(relocated []toolDescriptionRelocation) string {
	if len(relocated) == 0 {
		return ""
	}
	parts := []string{"Operator tool documentation for this session:"}
	for _, item := range relocated {
		parts = append(parts, "Tool: "+item.Name+"\n"+item.Description)
	}
	return strings.Join(parts, "\n\n")
}
```

In `ClaudeToKiro`, before merging tools:

```go
toolsForKiro, relocatedDocs := relocateLongClaudeToolDescriptions(req.Tools)
reqForTools := *req
reqForTools.Tools = toolsForKiro
toolSelection := mergeClaudeToolsAndReferences(reqForTools.Tools, reqForTools.ToolReferences, finalContent)
if docs := buildRelocatedToolDocumentation(relocatedDocs); docs != "" {
	finalContent = strings.TrimSpace(finalContent + "\n\n" + docs)
}
```

Set:

```go
payload.RelocatedToolDescriptions = len(relocatedDocs)
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroRelocatesLongToolDescriptionsToContext|TestGuardKiroPayloadTruncatesOversizedToolDescriptions' -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Add request-log metric**

Add `PayloadRelocatedToolDescriptions int` to `RequestLogEntry` and `payloadGuardResult`, copy from `KiroPayload`, and add one request-log test.

- [ ] **Step 7: Run package tests**

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroRelocatesLongToolDescriptionsToContext|TestRequestLogCaptures.*RelocatedTool' -count=1 -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
git add proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/payload_guard.go proxy/payload_guard_test.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: preserve long tool descriptions"
```

---

### Task 6: Validate Tool Names And Sanitized Collisions

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

- [ ] **Step 2: Write failing validation test**

Add to `proxy/handler_test.go`:

```go
func TestHandleClaudeMessagesRejectsTooLongRawToolName(t *testing.T) {
	h := NewHandler()
	body := `{
		"model":"claude-sonnet-4.5",
		"max_tokens":64,
		"tools":[{"name":"` + strings.Repeat("a", 65) + `","description":"too long","input_schema":{"type":"object"}}],
		"messages":[{"role":"user","content":"hello"}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.handleClaudeMessages(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Tool name") || !strings.Contains(rr.Body.String(), "64") {
		t.Fatalf("expected actionable tool-name error, got %s", rr.Body.String())
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestHandleClaudeMessagesRejectsTooLongRawToolName -count=1 -v
```

Expected: FAIL because request reaches later logic or gives non-actionable error.

- [ ] **Step 4: Implement tool validation**

In `proxy/translator.go`, add:

```go
func validateClaudeToolNames(tools []ClaudeTool, refs []ClaudeToolReference) string {
	type namedTool struct {
		Kind string
		Name string
	}
	items := make([]namedTool, 0, len(tools)+len(refs))
	for _, tool := range tools {
		items = append(items, namedTool{Kind: "tool", Name: tool.Name})
	}
	for _, ref := range refs {
		items = append(items, namedTool{Kind: "tool_reference", Name: ref.Name})
	}
	seen := map[string]string{}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if len(name) > 64 {
			return fmt.Sprintf("Tool name %q exceeds Kiro API limit of 64 characters; shorten the tool name or use Claude Code Tool Search with a shorter alias", name)
		}
		sanitized := sanitizeToolName(name)
		if previous, ok := seen[sanitized]; ok && previous != name {
			return fmt.Sprintf("Tool names %q and %q collide after Kiro-safe sanitization as %q; rename one tool", previous, name, sanitized)
		}
		seen[sanitized] = name
	}
	return ""
}
```

In `handleClaudeMessagesInternal`, after request shape validation and before model conversion:

```go
if msg := validateClaudeToolNames(req.Tools, req.ToolReferences); msg != "" {
	h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", msg)
	return
}
```

- [ ] **Step 5: Run validation test**

Run:

```bash
go test ./proxy -run TestHandleClaudeMessagesRejectsTooLongRawToolName -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Add collision test**

Add to `proxy/translator_test.go`:

```go
func TestValidateClaudeToolNamesDetectsSanitizedCollision(t *testing.T) {
	msg := validateClaudeToolNames(
		[]ClaudeTool{{Name: "mcp.fs.read", Description: "a", InputSchema: map[string]interface{}{"type": "object"}}},
		[]ClaudeToolReference{{Name: "mcp-fs-read", Description: "b", InputSchema: map[string]interface{}{"type": "object"}}},
	)
	if !strings.Contains(msg, "collide") {
		t.Fatalf("expected collision message, got %q", msg)
	}
}
```

Run:

```bash
go test ./proxy -run 'TestHandleClaudeMessagesRejectsTooLongRawToolName|TestValidateClaudeToolNamesDetectsSanitizedCollision' -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go proxy/translator.go proxy/translator_test.go
git commit -m "feat: validate claude tool names"
```

---

### Task 7: Tolerate Official Content Blocks And Log Degradation

**Files:**
- Modify: `proxy/translator.go`
- Modify: `proxy/translator_test.go`
- Modify: `proxy/kiro.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`
- Modify: `proxy/token_estimator.go`
- Modify: `proxy/token_estimator_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/request_log.go proxy/request_log_test.go proxy/token_estimator.go proxy/token_estimator_test.go
```

- [ ] **Step 2: Write failing unsupported content test**

Add to `proxy/translator_test.go`:

```go
func TestClaudeToKiroToleratesOfficialUnsupportedContentBlocks(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 128,
		Messages: []ClaudeMessage{{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "document", "title": "spec.pdf", "source": map[string]interface{}{"type": "base64", "media_type": "application/pdf", "data": "abc"}},
			map[string]interface{}{"type": "search_result", "title": "Result", "url": "https://example.com", "content": "search body"},
			map[string]interface{}{"type": "server_tool_result", "content": "server output"},
		}}},
	}

	payload := ClaudeToKiro(req, false)
	content := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	for _, want := range []string{"Unsupported content block: document", "Result", "search body", "server output"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected %q in converted content, got %q", want, content)
		}
	}
	if len(payload.UnsupportedContentBlocks) == 0 {
		t.Fatalf("expected unsupported content block metadata")
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestClaudeToKiroToleratesOfficialUnsupportedContentBlocks -count=1 -v
```

Expected: FAIL.

- [ ] **Step 4: Add unsupported block conversion**

In `KiroPayload`, add:

```go
	UnsupportedContentBlocks []string `json:"-"`
```

In `extractClaudeUserContent`, add default handling:

```go
default:
	if converted := convertUnsupportedClaudeBlockToText(block); converted != "" {
		if text != "" {
			text += "\n\n"
		}
		text += converted
		unsupportedBlocks = append(unsupportedBlocks, blockType)
	}
```

Change signature to return unsupported blocks:

```go
func extractClaudeUserContent(content interface{}) (string, []KiroImage, []KiroToolResult, int, []string)
```

Add helper:

```go
func convertUnsupportedClaudeBlockToText(block map[string]interface{}) string {
	blockType, _ := block["type"].(string)
	switch blockType {
	case "search_result":
		title, _ := block["title"].(string)
		url, _ := block["url"].(string)
		body := extractClaudeValueText(block["content"])
		return strings.TrimSpace("Search result: " + title + "\n" + url + "\n" + body)
	case "server_tool_result":
		return "Server tool result:\n" + extractClaudeValueText(block["content"])
	case "document":
		title, _ := block["title"].(string)
		if title == "" {
			title = "untitled"
		}
		return "Unsupported content block: document (" + title + ")"
	default:
		if txt := extractClaudeValueText(block["text"]); txt != "" {
			return txt
		}
		if txt := extractClaudeValueText(block["content"]); txt != "" {
			return "Unsupported content block: " + blockType + "\n" + txt
		}
		if blockType != "" {
			return "Unsupported content block: " + blockType
		}
	}
	return ""
}

func extractClaudeValueText(v interface{}) string {
	switch value := v.(type) {
	case string:
		return value
	case []interface{}:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if m, ok := item.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}
```

Deduplicate block names before assigning:

```go
payload.UnsupportedContentBlocks = cappedUniqueStrings(unsupportedContentBlocks, 16)
```

Add `cappedUniqueStrings` if not present.

- [ ] **Step 5: Run focused test**

Run:

```bash
go test ./proxy -run TestClaudeToKiroToleratesOfficialUnsupportedContentBlocks -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Log unsupported block metadata**

Add to `RequestLogEntry`:

```go
	PayloadUnsupportedContentBlocks []string `json:"payloadUnsupportedContentBlocks,omitempty"`
```

Add to `payloadGuardResult` and copy from payload. Add request-log test asserting the field is stored.

- [ ] **Step 7: Extend token estimator test**

Add to `proxy/token_estimator_test.go`:

```go
func TestEstimateClaudeRequestInputTokensIncludesOfficialUnsupportedBlocks(t *testing.T) {
	req := &ClaudeRequest{
		Model: "claude-sonnet-4.5",
		Messages: []ClaudeMessage{{Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "document", "title": "spec.pdf", "source": map[string]interface{}{"type": "base64", "media_type": "application/pdf", "data": strings.Repeat("a", 100)}},
			map[string]interface{}{"type": "search_result", "title": "Result", "content": "search body"},
		}}},
	}
	if got := estimateClaudeRequestInputTokens(req); got <= 0 {
		t.Fatalf("expected positive token estimate, got %d", got)
	}
}
```

Run:

```bash
go test ./proxy -run 'TestClaudeToKiroToleratesOfficialUnsupportedContentBlocks|TestEstimateClaudeRequestInputTokensIncludesOfficialUnsupportedBlocks|TestRequestLogCaptures.*UnsupportedContent' -count=1 -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
git add proxy/translator.go proxy/translator_test.go proxy/kiro.go proxy/payload_guard.go proxy/request_log.go proxy/request_log_test.go proxy/token_estimator.go proxy/token_estimator_test.go
git commit -m "feat: tolerate official claude content blocks"
```

---

### Task 8: Add Model Capability Matrix API

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/handler.go proxy/handler_test.go
```

- [ ] **Step 2: Write failing API test**

Add to `proxy/handler_test.go`:

```go
func TestAdminClaudeCodeModelReadinessReturnsCapabilityMatrix(t *testing.T) {
	h := &Handler{
		cachedModels: []ModelInfo{
			{ModelId: "claude-sonnet-4.5", InputTypes: []string{"text", "image"}},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-sonnet-4-5", nil)
	rr := httptest.NewRecorder()

	h.apiGetClaudeCodeModelReadiness(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body["requestedModel"] != "claude-sonnet-4-5" || body["mappedModel"] != "claude-sonnet-4.5" {
		t.Fatalf("unexpected model mapping: %#v", body)
	}
	if body["listedByGateway"] != true {
		t.Fatalf("expected listedByGateway=true: %#v", body)
	}
	caps, ok := body["capabilities"].(map[string]interface{})
	if !ok || caps["vision"] != true || caps["toolUse"] != true {
		t.Fatalf("expected capabilities, got %#v", body["capabilities"])
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestAdminClaudeCodeModelReadinessReturnsCapabilityMatrix -count=1 -v
```

Expected: FAIL because API function does not exist.

- [ ] **Step 4: Implement model readiness API**

In `proxy/handler.go`, add handler:

```go
func (h *Handler) apiGetClaudeCodeModelReadiness(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimSpace(r.URL.Query().Get("model"))
	if requested == "" {
		requested = "claude-sonnet-4.5"
	}
	thinkingCfg := config.GetThinkingConfig()
	mapped, thinking := resolveClaudeThinkingMode(requested, nil, thinkingCfg.Suffix)
	h.modelsCacheMu.RLock()
	cached := append([]ModelInfo(nil), h.cachedModels...)
	h.modelsCacheMu.RUnlock()
	listed, supportsImage := modelListedAndVision(cached, mapped)
	resp := map[string]interface{}{
		"requestedModel":  requested,
		"mappedModel":     mapped,
		"thinkingVariant": thinking || strings.HasSuffix(strings.ToLower(requested), strings.ToLower(thinkingCfg.Suffix)),
		"listedByGateway": listed,
		"capabilities": map[string]interface{}{
			"vision":    supportsImage,
			"toolUse":   true,
			"thinking":  true,
			"webSearch": true,
		},
		"reason": modelReadinessReason(listed),
	}
	json.NewEncoder(w).Encode(resp)
}

func modelListedAndVision(models []ModelInfo, model string) (bool, bool) {
	for _, m := range models {
		if strings.EqualFold(m.ModelId, model) {
			return true, modelSupportsImage(m.InputTypes)
		}
	}
	return false, false
}

func modelReadinessReason(listed bool) string {
	if listed {
		return "model listed by Kiro-Go model cache"
	}
	return "model not found in current Kiro-Go model cache"
}
```

Register route near other admin APIs:

```go
case path == "/admin/api/claude-code/model-readiness":
	h.apiGetClaudeCodeModelReadiness(w, r)
```

- [ ] **Step 5: Run focused test**

Run:

```bash
go test ./proxy -run TestAdminClaudeCodeModelReadinessReturnsCapabilityMatrix -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat: add claude model readiness matrix"
```

---

### Task 9: Surface New Metadata In Admin UI

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- web/index.html
```

- [ ] **Step 2: Add readiness labels**

In `renderClaudeCodeReadiness`, include flags for:

```javascript
flag(!!data.recentParentAgents, 'parent-agent') +
flag(!!data.recentUnsupportedBlocks, 'unsupported blocks') +
flag(!!data.recentToolResultImages, 'tool-result images') +
flag(!!data.recentOrphanedToolResults, 'orphan tool_result') +
flag(!!data.recentFineGrainedToolStreaming, 'fine-grained requested')
```

If those API fields do not yet exist, use fallback checks from examples:

```javascript
const examples = Array.isArray(data.examples) ? data.examples : [];
const hasParent = examples.some(e => e.parentAgentId);
```

- [ ] **Step 3: Add request log compact chips**

In request-log row rendering, extend the metadata chips:

```javascript
'parent:' + compactValue(log.claudeCodeParentAgentId),
'orphans:' + compactValue(log.payloadOrphanedToolResultsConverted),
'tri:' + compactValue(log.payloadToolResultImages),
'fg:' + compactValue(log.fineGrainedToolStreamingMode)
```

Filter empty values the same way current chips are filtered.

- [ ] **Step 4: Add model readiness panel**

Near the existing Claude Code readiness card, add a compact model readiness fetch:

```javascript
async function loadClaudeCodeModelReadiness() {
    if (!password) return;
    try {
        const resp = await fetch('/admin/api/claude-code/model-readiness', { headers: { 'X-Admin-Password': password } });
        const data = await resp.json();
        const el = document.getElementById('claude-code-model-readiness');
        if (!el) return;
        el.innerHTML = '<div style="margin-top:10px;color:#475569;font-size:12px">' +
            'model: ' + escapeHtml(data.requestedModel || '-') +
            ' -> ' + escapeHtml(data.mappedModel || '-') +
            ' · ' + escapeHtml(data.reason || '-') +
            '</div>';
    } catch (e) {}
}
```

Add a child container:

```html
<div id="claude-code-model-readiness"></div>
```

Call `loadClaudeCodeModelReadiness()` after `loadClaudeCodeReadiness()`.

- [ ] **Step 5: Run static grep check**

Run:

```bash
rg -n "parent-agent|tool-result images|model-readiness|payloadOrphanedToolResultsConverted|fineGrainedToolStreamingMode" web/index.html
```

Expected: all terms appear.

- [ ] **Step 6: Commit**

Run:

```bash
git add web/index.html
git commit -m "feat: show claude parity diagnostics"
```

---

### Task 10: Update Readiness API Aggregation

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/handler_test.go`

- [ ] **Step 1: Inspect diffs**

Run:

```bash
git diff -- proxy/handler.go proxy/handler_test.go
```

- [ ] **Step 2: Write failing readiness aggregation test**

Add to `proxy/handler_test.go`:

```go
func TestClaudeCodeReadinessIncludesNewParitySignals(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(10)}
	h.requestLogs.Add(RequestLogEntry{
		Timestamp:                         time.Now().UTC(),
		ClaudeCodeSessionID:               "session_1",
		ClaudeCodeAgentID:                 "agent_1",
		ClaudeCodeParentAgentID:           "parent_1",
		PayloadToolResultImages:           1,
		PayloadOrphanedToolResultsConverted: 1,
		PayloadUnsupportedContentBlocks:   []string{"document"},
		FineGrainedToolStreamingRequested: true,
		FineGrainedToolStreamingMode:      "requested_partial",
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/readiness", nil)
	rr := httptest.NewRecorder()

	h.apiGetClaudeCodeReadiness(rr, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	for _, key := range []string{"recentParentAgents", "recentToolResultImages", "recentOrphanedToolResults", "recentUnsupportedBlocks", "recentFineGrainedToolStreaming"} {
		if body[key] != true {
			t.Fatalf("expected %s=true, got %#v", key, body)
		}
	}
}
```

- [ ] **Step 3: Run failing test**

Run:

```bash
go test ./proxy -run TestClaudeCodeReadinessIncludesNewParitySignals -count=1 -v
```

Expected: FAIL until aggregation fields are implemented.

- [ ] **Step 4: Implement aggregation fields**

In `apiGetClaudeCodeReadiness`, initialize:

```go
"recentParentAgents": false,
"recentToolResultImages": false,
"recentOrphanedToolResults": false,
"recentUnsupportedBlocks": false,
"recentFineGrainedToolStreaming": false,
```

Inside the loop:

```go
if entry.ClaudeCodeParentAgentID != "" {
	resp["recentParentAgents"] = true
}
if entry.PayloadToolResultImages > 0 {
	resp["recentToolResultImages"] = true
}
if entry.PayloadOrphanedToolResultsConverted > 0 {
	resp["recentOrphanedToolResults"] = true
}
if len(entry.PayloadUnsupportedContentBlocks) > 0 {
	resp["recentUnsupportedBlocks"] = true
}
if entry.FineGrainedToolStreamingRequested {
	resp["recentFineGrainedToolStreaming"] = true
}
```

In readiness examples, include:

```go
"parentAgentId": entry.ClaudeCodeParentAgentID,
"unsupportedContentBlocks": append([]string(nil), entry.PayloadUnsupportedContentBlocks...),
"fineGrainedToolStreamingMode": entry.FineGrainedToolStreamingMode,
```

- [ ] **Step 5: Run focused test**

Run:

```bash
go test ./proxy -run TestClaudeCodeReadinessIncludesNewParitySignals -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go
git commit -m "feat: aggregate claude parity readiness"
```

---

### Task 11: Run Full Go Verification

**Files:**
- No source edits expected.

- [ ] **Step 1: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS for all packages.

- [ ] **Step 2: Fix failures with TDD discipline**

If tests fail, write or adjust focused tests first, implement minimal fixes, then rerun:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Commit only if fixes were needed**

Run:

```bash
git status --short
```

If source changes were made:

```bash
git add <changed-files>
git commit -m "fix: stabilize claude parity tests"
```

---

### Task 12: Docker Rebuild And Direct Kiro-Go Health

**Files:**
- Create UAT artifact directory under `docs/superpowers/uat/uat-full-official-parity-YYYYMMDDHHMMSS/`

- [ ] **Step 1: Create artifact directory**

Run:

```bash
UAT_DIR="docs/superpowers/uat/uat-full-official-parity-$(date +%Y%m%d%H%M%S)"
mkdir -p "$UAT_DIR"
printf '%s\n' "$UAT_DIR" > /tmp/kiro-go-full-parity-uat-dir
```

- [ ] **Step 2: Rebuild Kiro-Go Docker**

Run:

```bash
docker compose up -d --build kiro-go
```

Expected: container `kiro-go-kiro-go-1` is recreated or running.

- [ ] **Step 3: Capture health and models**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
curl -fsS http://127.0.0.1:8080/health | tee "$UAT_DIR/kiro-health.json"
curl -fsS http://127.0.0.1:8080/v1/models | tee "$UAT_DIR/kiro-models.json" >/dev/null
```

Expected: health JSON contains `"status":"ok"` and models JSON has `"object":"list"`.

- [ ] **Step 4: Direct Kiro-Go smoke with new metadata**

Run a non-secret request:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
curl -sS http://127.0.0.1:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -H 'anthropic-beta: fine-grained-tool-streaming-2025-05-14,context-management-2025-06-27' \
  -H 'x-claude-code-session-id: uat-session-full-parity' \
  -H 'x-claude-code-agent-id: uat-agent-child' \
  -H 'x-claude-code-parent-agent-id: uat-agent-parent' \
  -H 'x-request-id: uat-full-parity-direct' \
  -d '{"model":"claude-sonnet-4.5","max_tokens":64,"container":{"id":"uat"},"context_management":{"clear_function_results":true},"mcp_servers":[{"name":"repo"}],"tools":[{"name":"bash","description":"Run commands","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],"messages":[{"role":"user","content":"Return exactly: UAT_FULL_PARITY_DIRECT"}]}' \
  | tee "$UAT_DIR/kiro-direct-message.json"
```

Expected: status 200 and response text includes `UAT_FULL_PARITY_DIRECT` or a successful assistant response. If upstream capacity changes wording, record actual response and classify.

---

### Task 13: Rebuild sub2api Without Source Changes And Run Real Calls

**Files:**
- Do not modify `/www/sub2api`.
- Write artifacts only under Kiro-Go UAT directory.

- [ ] **Step 1: Record sub2api git status and DB counts before rebuild**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
git -C /www/sub2api status --short | tee "$UAT_DIR/sub2api-git-status-before.txt"
docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc "select 'users='||count(*) from users union all select 'groups='||count(*) from groups union all select 'accounts='||count(*) from accounts union all select 'api_keys='||count(*) from api_keys;" | tee "$UAT_DIR/sub2api-db-counts-before.txt"
```

Expected: counts are recorded; no secrets are printed.

- [ ] **Step 2: Rebuild only sub2api application**

Run:

```bash
cd /www/sub2api/deploy
docker compose -f docker-compose.yml -f docker-compose.current.yml up -d --build sub2api
```

Expected: `sub2api` is rebuilt/restarted; `sub2api-postgres` and `sub2api-redis` remain running and healthy.

- [ ] **Step 3: Check sub2api health**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
curl -fsS http://127.0.0.1:18080/health | tee "$UAT_DIR/sub2api-health.json"
```

Expected: `{"status":"ok"}`.

- [ ] **Step 4: Run sub2api non-stream and stream real smokes**

Use existing local sub2api API credentials from its current environment/config without printing them. If a previous smoke script exists, reuse it. Otherwise create a local script in the UAT dir that reads credentials from environment variables and redacts them in output.

Expected non-stream:

- HTTP 200.
- Response contains requested marker or a valid assistant message.
- Artifact saved as `sub2api-message-nonstream.json`.

Expected stream:

- HTTP 200.
- SSE includes `message_start` and `message_stop`.
- Artifact saved as `sub2api-message-stream.sse`.

- [ ] **Step 5: Record DB counts after calls**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc "select 'users='||count(*) from users union all select 'groups='||count(*) from groups union all select 'accounts='||count(*) from accounts union all select 'api_keys='||count(*) from api_keys;" | tee "$UAT_DIR/sub2api-db-counts-after.txt"
```

Expected: core data counts remain stable unless request-log/usage tables legitimately grow.

---

### Task 14: Playwright Browser UAT And Screenshot Analysis

**Files:**
- Create: `docs/superpowers/uat/uat-full-official-parity-YYYYMMDDHHMMSS/fullstack-uat.js`
- Create screenshots and `summary.json` in the same UAT dir.

- [ ] **Step 1: Create Playwright UAT script**

Create `fullstack-uat.js` in the UAT dir with:

```javascript
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const outDir = process.argv[2];
if (!outDir) throw new Error('usage: node fullstack-uat.js <outDir>');

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const errors = [];
  page.on('console', msg => {
    if (msg.type() === 'error') errors.push({ type: 'console', text: msg.text() });
  });
  page.on('pageerror', err => errors.push({ type: 'pageerror', text: String(err) }));

  const shots = [];
  async function shot(name) {
    const file = path.join(outDir, name + '.png');
    await page.screenshot({ path: file, fullPage: true });
    shots.push(file);
  }

  await page.goto('http://127.0.0.1:8080/admin', { waitUntil: 'networkidle' });
  await shot('kiro-admin-login-or-dashboard');

  await page.goto('http://127.0.0.1:8080/admin/api/claude-code/readiness');
  await shot('kiro-claude-readiness-json');

  await page.goto('http://127.0.0.1:8080/admin/api/claude-code/model-readiness');
  await shot('kiro-model-readiness-json');

  await page.goto('http://127.0.0.1:18080/health', { waitUntil: 'networkidle' });
  await shot('sub2api-health-json');

  await page.goto('http://127.0.0.1:18080', { waitUntil: 'networkidle' });
  await shot('sub2api-root');

  const summary = {
    ok: errors.length === 0,
    errors,
    screenshots: shots,
    checkedAt: new Date().toISOString()
  };
  fs.writeFileSync(path.join(outDir, 'playwright-summary.json'), JSON.stringify(summary, null, 2));
  await browser.close();
})().catch(err => {
  console.error(err);
  process.exit(1);
});
```

- [ ] **Step 2: Run Playwright UAT**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
node "$UAT_DIR/fullstack-uat.js" "$UAT_DIR"
```

Expected: script exits 0 and writes screenshots plus `playwright-summary.json`.

- [ ] **Step 3: Inspect screenshots**

Use local screenshot inspection or Playwright-generated images. Verify:

- pages are not blank;
- no visible fatal error page;
- JSON readiness pages contain expected keys;
- sub2api health is OK;
- sub2api root/admin page renders a real UI or login page.

Record observations in `summary.json`.

---

### Task 15: Final UAT Summary And Verification Report

**Files:**
- Create/Modify: UAT summary files under the UAT dir.
- Modify: `docs/superpowers/uat/2026-05-18-claude-code-full-official-parity-uat.md`

- [ ] **Step 1: Collect Kiro-Go request logs**

Run:

```bash
UAT_DIR="$(cat /tmp/kiro-go-full-parity-uat-dir)"
curl -fsS http://127.0.0.1:8080/admin/api/request-logs?limit=50 | tee "$UAT_DIR/kiro-request-logs.json" >/dev/null
curl -fsS http://127.0.0.1:8080/admin/api/claude-code/readiness | tee "$UAT_DIR/kiro-claude-readiness.json" >/dev/null
curl -fsS http://127.0.0.1:8080/admin/api/claude-code/model-readiness | tee "$UAT_DIR/kiro-model-readiness.json" >/dev/null
```

If admin API requires password, pass it via environment/header without printing it.

- [ ] **Step 2: Write summary.json**

Create `summary.json` with:

```json
{
  "date": "2026-05-18",
  "goTests": "PASS or FAIL with command output reference",
  "kiroDockerHealth": "PASS or FAIL",
  "sub2apiHealth": "PASS or FAIL",
  "sub2apiNonStream": "PASS or FAIL",
  "sub2apiStream": "PASS or FAIL",
  "playwright": "PASS or FAIL",
  "databaseCountsStable": "PASS or FAIL",
  "screenshotAnalysis": [
    "Kiro readiness screenshot shows ...",
    "Sub2api screenshot shows ..."
  ],
  "artifacts": []
}
```

Use actual observed values. Do not mark PASS unless evidence exists.

- [ ] **Step 3: Write UAT markdown**

Create or update `docs/superpowers/uat/2026-05-18-claude-code-full-official-parity-uat.md` with:

```markdown
# Claude Code Full Official Parity UAT

Date: 2026-05-18

## Commands

- `go test ./...`: RESULT
- `docker compose up -d --build kiro-go`: RESULT
- `curl http://127.0.0.1:8080/health`: RESULT
- `docker compose -f docker-compose.yml -f docker-compose.current.yml up -d --build sub2api`: RESULT
- `curl http://127.0.0.1:18080/health`: RESULT

## API Evidence

Summarize direct Kiro-Go, sub2api non-stream, sub2api stream, request logs, readiness, and model matrix.

## Database Evidence

Summarize pre/post counts and explain any legitimate changes.

## Browser Evidence

List screenshots and screenshot analysis. Mark PASS only if the screenshot content matches expected live data and no fatal UI/API errors are visible.

## Verdict

PASS/FAIL with reasons.
```

- [ ] **Step 4: Final verification**

Run:

```bash
go test ./...
git status --short
```

Expected: tests pass. Status shows only intentional source/UAT changes.

- [ ] **Step 5: Commit UAT**

Run:

```bash
git add docs/superpowers/uat proxy web
git commit -m "test: verify claude full parity uat"
```

Only include files intentionally changed by this plan.

---

## Self-Review Checklist

- Spec coverage:
  - Parent agent metadata: Task 1, Task 10.
  - Orphaned tool result preservation: Task 3.
  - Tool-result images: Task 4.
  - Long tool descriptions: Task 5.
  - Tool validation/collisions: Task 6.
  - Official content/field tolerance: Task 1, Task 7.
  - Model matrix: Task 8, Task 9.
  - Admin observability: Task 9, Task 10.
  - Docker/sub2api/browser/database UAT: Tasks 12-15.
- No `/www/sub2api` source edits are planned.
- No database reset commands are used.
- Each behavior task starts with a failing test.
- UAT requires screenshot analysis before PASS.

