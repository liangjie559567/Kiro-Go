# Claude Code Official Experience Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first verification-first slice for Claude Code official API experience while preserving the local `/www/sub2api -> Kiro-Go -> Kiro` real downstream route.

**Architecture:** This phase adds tests, fixture coverage, token-estimator improvements, and sub2api regression tooling before larger scheduler or endpoint rewrites. It keeps behavior changes narrowly scoped to count-token estimation and observability, using existing `anthropicEnvelope`, `ClaudeSSEWriter`, request logging, and sub2api load scripts.

**Tech Stack:** Go 1.21, standard `testing`, existing Kiro-Go proxy package, Node.js UAT scripts for sub2api, Docker production services for real downstream verification.

---

## Scope

This plan implements Phase 1 and Phase 2 from [the design spec](/www/Kiro-Go/docs/superpowers/specs/2026-05-16-claude-code-official-experience-reliability-design.md). It deliberately does not implement runtime endpoint routing or scheduler/admission rewrites. Those need a separate plan after the verification harness and count-token safety net are in place.

## File Structure

- Modify `proxy/token_estimator.go`
  - Responsibility: estimate Claude/OpenAI input and output tokens for count_tokens, request logs, and fallback usage.
  - Add structured coverage for `tool_reference`, `cache_control`, images, tool metadata, and thinking budget.

- Modify `proxy/token_estimator_test.go`
  - New focused tests for token estimator behavior. If this file does not exist, create it.

- Modify `proxy/claude_sse_writer_test.go`
  - Add golden-style tests for mixed thinking/text/tool sequences and post-start error behavior.

- Modify `proxy/anthropic_envelope_test.go`
  - Add a fixture-backed Claude Code request parsing test that asserts beta, session, request ID, unknown fields, tools, and tool references are all preserved.

- Modify `proxy/request_log_test.go`
  - Add request-log assertions for Claude Code/session metadata, beta flags, tool references, first-token attempts, and payload metadata.

- Create `docs/superpowers/uat/claude-code-sub2api-smoke.js`
  - Responsibility: run a small real downstream smoke against local sub2api for non-stream, stream, count_tokens, and models.

- Create `docs/superpowers/uat/2026-05-16-claude-code-official-experience-phase1-uat.md`
  - Responsibility: record commands, expected outputs, and real sub2api verification evidence for this phase.

## Task 1: Token Estimator Coverage Tests

**Files:**
- Create or modify: `proxy/token_estimator_test.go`
- Read: `proxy/token_estimator.go`, `proxy/translator.go`

- [ ] **Step 1: Write failing tests for Claude tool_reference, image, cache_control, and thinking budget**

Add these tests to `proxy/token_estimator_test.go`:

```go
package proxy

import "testing"

func TestEstimateClaudeRequestInputTokensIncludesToolReferencesAndToolCacheControl(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 4096,
		System: []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "system rules",
				"cache_control": map[string]interface{}{"type": "ephemeral", "ttl": "1h"},
			},
		},
		Messages: []ClaudeMessage{{
			Role: "user",
			Content: []interface{}{
				map[string]interface{}{"type": "text", "text": "read a file"},
				map[string]interface{}{
					"type": "image",
					"source": map[string]interface{}{
						"type": "base64",
						"media_type": "image/png",
						"data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
					},
				},
			},
		}},
		Tools: []ClaudeTool{{
			Name:        "mcp__filesystem__read_file",
			Description: "Read a file from disk",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Absolute path"},
				},
				"required": []interface{}{"path"},
			},
			CacheControl: map[string]interface{}{"type": "ephemeral"},
		}},
		ToolReferences: []ClaudeToolReference{{
			Type:        "tool_reference",
			ID:          "toolref_1",
			Name:        "mcp__git__status",
			Title:       "Git status",
			Description: "Show repository status",
			InputSchema: map[string]interface{}{"type": "object"},
		}},
	}

	withoutTools := &ClaudeRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  req.Messages,
	}

	base := estimateClaudeRequestInputTokens(withoutTools)
	withTools := estimateClaudeRequestInputTokens(req)

	if withTools <= base {
		t.Fatalf("expected tools and tool_reference to increase estimate: base=%d withTools=%d", base, withTools)
	}
	if withTools-base < 20 {
		t.Fatalf("expected meaningful tool/reference overhead, base=%d withTools=%d", base, withTools)
	}
}

func TestEstimateClaudeRequestInputTokensIncludesThinkingBudget(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 4096,
		Thinking: &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
		Messages: []ClaudeMessage{{Role: "user", Content: "solve"}},
	}

	withoutThinking := *req
	withoutThinking.Thinking = nil

	base := estimateClaudeRequestInputTokens(&withoutThinking)
	withThinking := estimateClaudeRequestInputTokens(req)

	if withThinking <= base {
		t.Fatalf("expected thinking config to increase estimate: base=%d thinking=%d", base, withThinking)
	}
}
```

- [ ] **Step 2: Run the token estimator tests and verify they fail**

Run:

```bash
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludesToolReferencesAndToolCacheControl|TestEstimateClaudeRequestInputTokensIncludesThinkingBudget' -count=1 -v
```

Expected: at least the tool_reference/cache_control test fails because `estimateClaudeRequestInputTokens` does not currently count `ToolReferences`, `CacheControl`, or image source metadata explicitly.

- [ ] **Step 3: Implement token-estimator coverage**

Modify `proxy/token_estimator.go`:

```go
func estimateClaudeRequestInputTokens(req *ClaudeRequest) int {
	if req == nil {
		return 0
	}

	total := estimateClaudeValueTokens(req.System)
	total += estimateClaudeThinkingConfigTokens(req.Thinking)

	for _, msg := range req.Messages {
		total += estimateApproxTokens(msg.Role)
		total += estimateClaudeValueTokens(msg.Content)
	}

	for _, tool := range req.Tools {
		total += estimateClaudeToolTokens(tool)
	}
	for _, ref := range req.ToolReferences {
		total += estimateClaudeToolReferenceTokens(ref)
	}

	return total
}

func estimateClaudeThinkingConfigTokens(thinking *ClaudeThinkingConfig) int {
	if thinking == nil {
		return 0
	}
	total := estimateApproxTokens(thinking.Type)
	total += estimateApproxTokens(thinking.Display)
	if thinking.BudgetTokens > 0 {
		total += estimateApproxTokens("thinking_budget")
		total += max(1, thinking.BudgetTokens/256)
	}
	return total
}

func estimateClaudeToolTokens(tool ClaudeTool) int {
	total := estimateApproxTokens(tool.Type)
	total += estimateApproxTokens(tool.Name)
	total += estimateApproxTokens(tool.Description)
	total += estimateJSONTokens(tool.InputSchema)
	total += estimateJSONTokens(tool.CacheControl)
	if tool.MaxUses > 0 {
		total += estimateApproxTokens("max_uses")
		total += 1
	}
	if tool.EagerInputStreaming {
		total += estimateApproxTokens("eager_input_streaming")
	}
	return total
}

func estimateClaudeToolReferenceTokens(ref ClaudeToolReference) int {
	total := estimateApproxTokens(ref.Type)
	total += estimateApproxTokens(ref.ID)
	total += estimateApproxTokens(ref.Name)
	total += estimateApproxTokens(ref.Title)
	total += estimateApproxTokens(ref.Description)
	total += estimateJSONTokens(ref.InputSchema)
	if ref.DeferLoading {
		total += estimateApproxTokens("defer_loading")
	}
	return total
}
```

Update `estimateClaudeValueTokens` image handling by adding cases inside the `map[string]interface{}` switch:

```go
case "image":
	total := estimateApproxTokens("image")
	if source, ok := value["source"]; ok {
		total += estimateImageSourceTokens(source)
	}
	return total
case "document":
	total := estimateApproxTokens("document")
	if source, ok := value["source"]; ok {
		total += estimateImageSourceTokens(source)
	}
	if title, ok := value["title"].(string); ok {
		total += estimateApproxTokens(title)
	}
	return total
```

Add helper:

```go
func estimateImageSourceTokens(source interface{}) int {
	switch s := source.(type) {
	case nil:
		return 0
	case *ImageSource:
		total := estimateApproxTokens(s.Type)
		total += estimateApproxTokens(s.MediaType)
		total += max(1, len(s.Data)/1024)
		return total
	case map[string]interface{}:
		total := 0
		if t, ok := s["type"].(string); ok {
			total += estimateApproxTokens(t)
		}
		if mt, ok := s["media_type"].(string); ok {
			total += estimateApproxTokens(mt)
		}
		if data, ok := s["data"].(string); ok {
			total += max(1, len(data)/1024)
		}
		if url, ok := s["url"].(string); ok {
			total += estimateApproxTokens(url)
		}
		if total > 0 {
			return total
		}
	}
	return estimateJSONTokens(source)
}
```

In the generic map fallback, include `cache_control`:

```go
if cacheControl, ok := value["cache_control"]; ok {
	total += estimateJSONTokens(cacheControl)
}
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludesToolReferencesAndToolCacheControl|TestEstimateClaudeRequestInputTokensIncludesThinkingBudget|TestThinkingPromptAffectsClaudeTokenEstimate' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/token_estimator.go proxy/token_estimator_test.go
git commit -m "test: cover claude token estimator inputs"
```

## Task 2: Golden SSE Compatibility Tests

**Files:**
- Modify: `proxy/claude_sse_writer_test.go`
- Read: `proxy/claude_sse_writer.go`

- [ ] **Step 1: Add golden tests for mixed thinking/text/tool and post-start error**

Append to `proxy/claude_sse_writer_test.go`:

```go
func TestClaudeSSEWriterMixedThinkingTextToolOrder(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(20, 0, promptCacheUsage{}, false), 16)

	writer.ThinkingDelta("reason")
	writer.TextDelta("answer")
	writer.ToolUse(KiroToolUse{
		ToolUseID: "toolu_1",
		Name:      "readFile",
		Input:     map[string]interface{}{"path": "/tmp/example.txt"},
	})
	writer.Stop("tool_use", buildClaudeUsageMap(20, 4, promptCacheUsage{}, false))

	body := w.Body.String()
	mustContainInOrder(t, body,
		"event: message_start",
		`"type":"thinking"`,
		`"type":"thinking_delta"`,
		"event: content_block_stop",
		`"type":"text"`,
		`"type":"text_delta"`,
		"event: content_block_stop",
		`"type":"tool_use"`,
		`"type":"input_json_delta"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"tool_use"`,
		"event: message_stop",
	)
}

func TestClaudeSSEWriterPostStartErrorDoesNotEmitMessageStop(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 4096)

	writer.TextDelta("partial")
	writer.Error("overloaded_error", "upstream reset")

	body := w.Body.String()
	mustContainInOrder(t, body,
		"event: message_start",
		`"type":"text_delta"`,
		"event: content_block_stop",
		"event: error",
		`"type":"overloaded_error"`,
	)
	if strings.Contains(body, "event: message_stop") {
		t.Fatalf("stream error after start must not emit message_stop body=%s", body)
	}
}
```

- [ ] **Step 2: Run SSE tests**

Run:

```bash
go test ./proxy -run 'TestClaudeSSEWriter' -count=1 -v
```

Expected: PASS. If any ordering assertion fails, fix `ClaudeSSEWriter` rather than weakening the test.

- [ ] **Step 3: Commit**

```bash
git add proxy/claude_sse_writer_test.go proxy/claude_sse_writer.go
git commit -m "test: add claude sse golden coverage"
```

## Task 3: Claude Code Fixture And Request Log Regression Tests

**Files:**
- Modify: `proxy/anthropic_envelope_test.go`
- Modify: `proxy/request_log_test.go`
- Read: `proxy/testdata/claude_code_2_1_143_wire_request.json`, `proxy/testdata/claude_code_tool_reference_message.json`
- Read: `proxy/anthropic_envelope.go`, `proxy/request_log.go`

- [ ] **Step 1: Add fixture-backed envelope assertions**

Append to `proxy/anthropic_envelope_test.go`:

```go
func TestClaudeCodeWireFixturePreservesHeadersBetasAndUnknownFields(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "claude_code_2_1_143_wire_request.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14")
	req.Header.Set("x-request-id", "client-fixture-1")
	req.Header.Set("x-claude-code-session-id", "session-fixture-1")
	req.Header.Set("x-claude-code-agent-id", "agent-fixture-1")
	req.Header.Set("user-agent", "claude-code/2.1.143")

	env, err := parseAnthropicEnvelope(req, body)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	if env.AnthropicRequestID != "client-fixture-1" {
		t.Fatalf("request id = %q", env.AnthropicRequestID)
	}
	if !env.HasBeta("claude-code-20250219") || !env.HasBetaPrefix("interleaved-thinking") {
		t.Fatalf("expected beta flags, got %#v", env.Betas)
	}
	if env.SessionID != "session-fixture-1" || env.AgentID != "agent-fixture-1" {
		t.Fatalf("missing Claude Code metadata session=%q agent=%q", env.SessionID, env.AgentID)
	}
	if env.UserAgent != "claude-code/2.1.143" {
		t.Fatalf("user agent = %q", env.UserAgent)
	}
	if env.Request.Model == "" || len(env.Request.Messages) == 0 {
		t.Fatalf("fixture did not populate model/messages: %#v", env.Request)
	}
}
```

If imports are missing, add:

```go
import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)
```

Merge with existing imports rather than creating duplicates.

- [ ] **Step 2: Add request-log metadata test**

Append to `proxy/request_log_test.go`:

```go
func TestRequestLogCapturesClaudeCodeCompatibilityMetadata(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("x-request-id", "client-req-1")
	req.Header.Set("x-claude-code-session-id", "session-1")
	req.Header.Set("x-claude-code-agent-id", "agent-1")
	w := httptest.NewRecorder()

	logCtx, loggedReq, recorder, _ := h.beginRequestLog(w, req)
	updateRequestLogAnthropic(loggedReq, &anthropicEnvelope{
		AnthropicRequestID: "client-req-1",
		AnthropicVersion:   "2023-06-01",
		Betas:              parseAnthropicBetas("claude-code-20250219,interleaved-thinking-2025-05-14"),
		Request: ClaudeRequest{
			ToolReferences: []ClaudeToolReference{{Name: "mcp__fs__read_file"}},
		},
	})
	updateRequestLogMetadata(loggedReq, "claude-sonnet-4.5", true)
	updateRequestLogReliability(loggedReq, 12, 2, 345, 1)
	recorder.WriteHeader(http.StatusOK)
	h.finishRequestLog(logCtx, recorder)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one log, got %d", len(logs))
	}
	got := logs[0]
	if got.RequestID != "client-req-1" || got.AnthropicRequestID != "client-req-1" {
		t.Fatalf("request ids not captured: %#v", got)
	}
	if got.ClaudeCodeSessionID != "session-1" || got.ClaudeCodeAgentID != "agent-1" {
		t.Fatalf("Claude Code metadata not captured: %#v", got)
	}
	if got.ToolReferenceCount != 1 || got.FirstTokenMs != 345 || got.Attempts != 2 {
		t.Fatalf("compatibility metrics not captured: %#v", got)
	}
	if len(got.AnthropicBetas) != 2 {
		t.Fatalf("expected beta flags, got %#v", got.AnthropicBetas)
	}
}
```

- [ ] **Step 3: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestClaudeCodeWireFixturePreservesHeadersBetasAndUnknownFields|TestClaudeCodeToolReferenceFixtureParses|TestRequestLogCapturesClaudeCodeCompatibilityMetadata' -count=1 -v
```

Expected: PASS. If the first fixture file is unavailable in a clean checkout, create a minimal but real-shaped fixture in `proxy/testdata/claude_code_2_1_143_wire_request.json` and include it in the commit.

- [ ] **Step 4: Commit**

```bash
git add proxy/anthropic_envelope_test.go proxy/request_log_test.go proxy/testdata/claude_code_2_1_143_wire_request.json
git commit -m "test: lock claude code request metadata"
```

## Task 4: sub2api Smoke Script

**Files:**
- Create: `docs/superpowers/uat/claude-code-sub2api-smoke.js`
- Read: `docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js`

- [ ] **Step 1: Create the smoke script**

Create `docs/superpowers/uat/claude-code-sub2api-smoke.js`:

```javascript
const fs = require('fs');
const path = require('path');

const base = process.env.SUB2API_BASE || 'http://127.0.0.1:18080';
const model = process.env.SUB2API_MODEL || 'claude-sonnet-4.5';
const keyPath = process.env.SUB2API_KEY_FILE || '/tmp/sub2api_claude_key';
const outDir = process.env.SUB2API_SMOKE_OUT || '/www/Kiro-Go/docs/superpowers/uat/sub2api-smoke';
const apiKey = fs.readFileSync(keyPath, 'utf8').trim();
const runId = process.env.SUB2API_SMOKE_RUN_ID || `sub2api-smoke-${Date.now()}`;

fs.mkdirSync(outDir, {recursive: true});

function extractClaudeText(body) {
  if (!body || !Array.isArray(body.content)) return '';
  return body.content.map((block) => block && typeof block.text === 'string' ? block.text : '').join('');
}

function parseSSE(raw) {
  const events = [];
  const text = [];
  for (const block of raw.split(/\n\n+/)) {
    const eventLine = block.split(/\r?\n/).find((line) => line.startsWith('event: '));
    const dataLine = block.split(/\r?\n/).find((line) => line.startsWith('data: '));
    if (!dataLine) continue;
    const event = eventLine ? eventLine.slice(7).trim() : '';
    const data = dataLine.slice(6).trim();
    events.push(event);
    if (!data || data === '[DONE]') continue;
    const parsed = JSON.parse(data);
    if (parsed.type === 'content_block_delta' && parsed.delta && typeof parsed.delta.text === 'string') {
      text.push(parsed.delta.text);
    }
  }
  return {events, text: text.join('')};
}

async function requestJSON(endpoint, payload) {
  const started = Date.now();
  const res = await fetch(`${base}${endpoint}`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
      'anthropic-version': '2023-06-01',
      'x-request-id': `${runId}-${endpoint.replace(/\W+/g, '-')}`,
      'x-claude-code-session-id': runId,
    },
    body: JSON.stringify(payload),
  });
  const raw = await res.text();
  return {status: res.status, ok: res.ok, durationMs: Date.now() - started, raw};
}

async function main() {
  const marker = `${runId}-marker`;
  const messages = [{role: 'user', content: `Return exactly ${marker}`}];
  const results = {runId, base, model, startedAt: new Date().toISOString()};

  const modelsStarted = Date.now();
  const modelsRes = await fetch(`${base}/v1/models`, {headers: {Authorization: `Bearer ${apiKey}`}});
  results.models = {status: modelsRes.status, ok: modelsRes.ok, durationMs: Date.now() - modelsStarted};

  const count = await requestJSON('/v1/messages/count_tokens', {model, max_tokens: 64, messages});
  results.countTokens = {
    status: count.status,
    ok: count.ok,
    durationMs: count.durationMs,
    body: JSON.parse(count.raw),
  };

  const sync = await requestJSON('/v1/messages', {model, max_tokens: 64, stream: false, messages});
  const syncBody = JSON.parse(sync.raw);
  const syncText = extractClaudeText(syncBody).trim();
  results.sync = {
    status: sync.status,
    ok: sync.ok,
    durationMs: sync.durationMs,
    text: syncText,
    correct: sync.ok && syncText === marker,
    usage: syncBody.usage,
  };

  const stream = await requestJSON('/v1/messages', {model, max_tokens: 64, stream: true, messages});
  const parsedStream = parseSSE(stream.raw);
  results.stream = {
    status: stream.status,
    ok: stream.ok,
    durationMs: stream.durationMs,
    text: parsedStream.text.trim(),
    correct: stream.ok && parsedStream.text.trim() === marker,
    hasMessageStart: parsedStream.events.includes('message_start'),
    hasMessageStop: parsedStream.events.includes('message_stop'),
    eventCount: parsedStream.events.length,
  };

  const out = path.join(outDir, `${runId}.json`);
  fs.writeFileSync(out, JSON.stringify(results, null, 2));
  console.log(JSON.stringify({out, results}, null, 2));

  if (!results.models.ok || !results.countTokens.ok || !results.sync.correct || !results.stream.correct || !results.stream.hasMessageStop) {
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(err && err.stack || err);
  process.exit(1);
});
```

- [ ] **Step 2: Run syntax check**

Run:

```bash
node --check docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Expected: `Syntax check passed` or no output with exit code 0.

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/uat/claude-code-sub2api-smoke.js
git commit -m "test: add sub2api downstream smoke"
```

## Task 5: UAT Documentation Template And Commands

**Files:**
- Create: `docs/superpowers/uat/2026-05-16-claude-code-official-experience-phase1-uat.md`

- [ ] **Step 1: Create UAT document**

Create `docs/superpowers/uat/2026-05-16-claude-code-official-experience-phase1-uat.md`:

```markdown
# Claude Code Official Experience Phase 1 UAT

Date: 2026-05-16

## Scope

- Kiro-Go verification-first phase for Claude Code official API experience.
- Local downstream preservation: `/www/sub2api -> Kiro-Go -> Kiro`.
- Focus areas: token estimator coverage, Claude SSE compatibility, Claude Code envelope/log metadata, sub2api smoke.

## Unit Verification

Run:

```bash
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludes|TestClaudeSSEWriter|TestClaudeCodeWireFixture|TestClaudeCodeToolReferenceFixtureParses|TestRequestLogCapturesClaudeCodeCompatibilityMetadata' -count=1 -v
```

Expected:

- Token estimator tests pass.
- Claude SSE writer golden tests pass.
- Claude Code fixture parsing tests pass.
- Request log compatibility metadata test passes.

## Full Go Verification

Run:

```bash
go test ./...
```

Expected: all packages pass.

## sub2api Real Downstream Smoke

Preconditions:

- Kiro-Go is reachable at `http://127.0.0.1:8080`.
- sub2api is reachable at `http://127.0.0.1:18080`.
- `/tmp/sub2api_claude_key` contains the local sub2api Claude API key.

Run:

```bash
node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Expected:

- `/v1/models` returns HTTP 200 through sub2api.
- `/v1/messages/count_tokens` returns HTTP 200 with positive `input_tokens`.
- Non-stream `/v1/messages` returns the exact marker.
- Stream `/v1/messages` returns the exact marker and includes `message_start` and `message_stop`.

## Optional sub2api 100x10 Regression

Run this for scheduler, error-mapping, or stream behavior changes:

```bash
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js sync 100 10 claude-sonnet-4.5 phase1-sync-$(date +%Y%m%d%H%M%S)
node docs/superpowers/uat/2026-05-16-sub2api-100x10-content-latency-test.js stream 100 10 claude-sonnet-4.5 phase1-stream-$(date +%Y%m%d%H%M%S)
```

Expected:

- No protocol errors.
- Content correctness is preserved.
- Any 429/529/concurrency failure is attributable to explicit sub2api or Kiro-Go admission limits, not malformed responses.

## Results

Record command outputs and artifact paths here after execution.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/uat/2026-05-16-claude-code-official-experience-phase1-uat.md
git commit -m "docs: add claude code phase1 uat checklist"
```

## Task 6: Final Verification And Phase Closeout

**Files:**
- Verify only unless a previous task exposed a real bug.

- [ ] **Step 1: Run focused proxy tests**

Run:

```bash
go test ./proxy -run 'TestEstimateClaudeRequestInputTokensIncludes|TestClaudeSSEWriter|TestClaudeCodeWireFixture|TestClaudeCodeToolReferenceFixtureParses|TestRequestLogCapturesClaudeCodeCompatibilityMetadata' -count=1 -v
```

Expected: PASS.

- [ ] **Step 2: Run all Go tests**

Run:

```bash
go test ./...
```

Expected: PASS. If failures occur in unrelated dirty worktree changes, record them explicitly and do not revert unrelated user changes.

- [ ] **Step 3: Run sub2api smoke if services are available**

Run:

```bash
test -f /tmp/sub2api_claude_key && curl -fsS http://127.0.0.1:18080/health && node docs/superpowers/uat/claude-code-sub2api-smoke.js
```

Expected:

- If sub2api is running, the smoke script exits 0 and writes an artifact under `docs/superpowers/uat/sub2api-smoke/`.
- If sub2api is not running, record that the smoke was not run; do not claim real downstream verification.

- [ ] **Step 4: Commit UAT artifact only if generated intentionally**

If the smoke script generated a new artifact and it is worth preserving:

```bash
git add docs/superpowers/uat/sub2api-smoke/<artifact>.json
git commit -m "test: record sub2api phase1 smoke"
```

If the artifact is just local noise, leave it untracked and mention it in the final report.

- [ ] **Step 5: Final status**

Report:

- commits created;
- focused tests status;
- full `go test ./...` status;
- sub2api smoke status;
- any skipped verification and why.

## Self-Review

Spec coverage:

- Official compatibility harness: Tasks 2 and 3.
- sub2api downstream gate: Tasks 4, 5, and 6.
- Count tokens calibration first slice: Task 1.
- Streaming conformance: Task 2.
- Observability metadata: Task 3.
- Larger scheduler/admission, runtime endpoint strategy, and prompt-cache diagnostics remain intentionally out of scope for this phase and need follow-up plans.

Placeholder scan: no placeholder implementation steps remain. Optional verification is explicitly conditional on local services being available.

Type consistency: all referenced types already exist in `proxy`: `ClaudeRequest`, `ClaudeTool`, `ClaudeToolReference`, `ClaudeThinkingConfig`, `KiroToolUse`, `promptCacheUsage`, and `anthropicEnvelope`.
