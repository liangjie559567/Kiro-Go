# Kiro-Go Claude Code Official Parity and Opus 4.7 Latency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Kiro-Go-only protocol parity and Opus 4.7 latency improvements while proving live sub2api still calls Kiro-Go successfully as a black-box downstream.

**Architecture:** Keep Kiro-Go as the only codebase being modified. Extend existing request envelope, request logs, model resolution, admission gate, readiness APIs, and admin UI instead of introducing a parallel gateway path. Preserve the current risk-group cooldown model and separate model-capacity pressure from account health.

**Tech Stack:** Go 1.21+, existing Kiro-Go HTTP handlers, existing in-memory request log store, Docker Compose, browser verification with Playwright-compatible Node scripts, jq/curl for API evidence.

---

## File Structure

Modify these Kiro-Go files only:

- `proxy/translator.go`: Anthropic request types, model normalization, Opus 4.7 compatibility helpers.
- `proxy/anthropic_envelope.go`: official extra field detection and Claude Code header capture.
- `proxy/anthropic_envelope_test.go`: envelope parsing tests for official fields and redacted metadata.
- `proxy/handler.go`: request preprocessing, `/v1/models`, readiness APIs, request log updates, prewarm path, admission metric updates.
- `proxy/handler_test.go`: handler tests for Opus 4.7 normalization, max_tokens=0/cache prewarm, readiness, model list.
- `proxy/request_log.go`: new request log fields and update helpers.
- `proxy/request_log_test.go`: request log redaction and stats tests.
- `proxy/opus_gate.go`: adaptive admission metrics and effective concurrency snapshots.
- `proxy/opus_gate_test.go`: adaptive admission transitions.
- `proxy/cache_tracker.go`: cache fingerprint/prewarm metadata only if existing helpers are insufficient.
- `proxy/cache_tracker_test.go`: cache fingerprint/prewarm tests if `cache_tracker.go` changes.
- `config/config.go`: model admission config additions only if adaptive admission needs persisted knobs.
- `config/config_test.go`: config default/normalization tests if config changes.
- `web/index.html`: admin UI display for official fields, Opus 4.7 pressure, request log columns.
- `docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519/`: UAT output directory created during verification.

Do not modify `/www/sub2api`. Use it only through API, Docker, database, and browser/evidence checks.

Before executing any task, run:

```bash
git status --short
```

Expected: existing unrelated modified files may be present. Do not revert them. Commit only files touched by each task.

---

### Task 1: Official Model Name Resolution and Opus 4.7 Compatibility Helpers

**Files:**
- Modify: `proxy/translator.go`
- Modify: `proxy/handler.go`
- Test: `proxy/translator_test.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Add failing model normalization tests**

Append tests to `proxy/translator_test.go`:

```go
func TestParseModelAndThinkingNormalizesOfficialOpus47Names(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		wantModel     string
		wantThinking  bool
	}{
		{name: "official dashed", model: "claude-opus-4-7", wantModel: "claude-opus-4.7"},
		{name: "official dashed thinking", model: "claude-opus-4-7-thinking", wantModel: "claude-opus-4.7", wantThinking: true},
		{name: "kiro dotted", model: "claude-opus-4.7", wantModel: "claude-opus-4.7"},
		{name: "date suffix", model: "claude-opus-4-7-20260514", wantModel: "claude-opus-4.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotModel, gotThinking := ParseModelAndThinking(tt.model, "-thinking")
			if gotModel != tt.wantModel || gotThinking != tt.wantThinking {
				t.Fatalf("ParseModelAndThinking(%q) = %q/%v, want %q/%v", tt.model, gotModel, gotThinking, tt.wantModel, tt.wantThinking)
			}
		})
	}
}

func TestIsOpus47RequestModelRecognizesOfficialAndKiroNames(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-7",
		"claude-opus-4.7",
		"claude-opus-4-7-thinking",
		"claude-opus-4.7-thinking",
	} {
		if !isOpus47RequestModel(model) {
			t.Fatalf("expected %q to be recognized as opus 4.7", model)
		}
	}
	if isOpus47RequestModel("claude-opus-4.6") {
		t.Fatalf("did not expect opus 4.6 to be recognized as opus 4.7")
	}
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run:

```bash
go test ./proxy -run 'TestParseModelAndThinkingNormalizesOfficialOpus47Names|TestIsOpus47RequestModelRecognizesOfficialAndKiroNames' -count=1
```

Expected: FAIL because date-suffix normalization and `isOpus47RequestModel` are not complete or not exported in the expected helper form.

- [ ] **Step 3: Implement model normalization helpers**

In `proxy/translator.go`, add `regexp`-based normalization near `ParseModelAndThinking`:

```go
var claudeDateSuffixRE = regexp.MustCompile(`^(claude-(?:haiku|sonnet|opus)-\d+(?:[.-]\d+)?)-\d{8}$`)

func normalizeClaudeModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	lower := strings.ToLower(model)
	lower = strings.TrimSuffix(lower, "-latest")
	if m := claudeDateSuffixRE.FindStringSubmatch(lower); len(m) == 2 {
		lower = m[1]
	}
	for _, mapping := range modelMapOrdered {
		if lower == mapping.key {
			return mapping.value
		}
	}
	if strings.HasPrefix(lower, "claude-") {
		parts := strings.Split(lower, "-")
		if len(parts) >= 4 {
			for i := 0; i < len(parts)-1; i++ {
				if parts[i] == "4" && parts[i+1] == "7" && strings.Contains(lower, "opus") {
					return "claude-opus-4.7"
				}
			}
		}
	}
	return model
}

func isOpus47RequestModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	normalized = strings.TrimSuffix(normalized, "-thinking")
	normalized = strings.ReplaceAll(normalized, ".", "-")
	return normalized == "claude-opus-4-7"
}
```

Update `ParseModelAndThinking` to call `normalizeClaudeModelName` after model mapping and before the ordered contains loop:

```go
	normalized := normalizeClaudeModelName(model)
	if normalized != model {
		model = normalized
		lower = strings.ToLower(model)
	}
```

In `proxy/handler.go`, update existing `isOpus47Model` to call `isOpus47RequestModel` after stripping the configured thinking suffix:

```go
func isOpus47Model(model string) bool {
	normalized := strings.TrimSpace(model)
	if suffix := strings.TrimSpace(config.GetThinkingConfig().Suffix); suffix != "" {
		normalized = strings.TrimSuffix(normalized, suffix)
	}
	return isOpus47RequestModel(normalized)
}
```

- [ ] **Step 4: Run model tests**

Run:

```bash
go test ./proxy -run 'TestParseModelAndThinkingNormalizesOfficialOpus47Names|TestIsOpus47RequestModelRecognizesOfficialAndKiroNames' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add failing Opus 4.7 request normalization tests**

Append to `proxy/handler_test.go`:

```go
func TestNormalizeOpus47ClaudeRequestUsesAdaptiveThinkingAndDropsSampling(t *testing.T) {
	req := ClaudeRequest{
		Model:       "claude-opus-4-7",
		MaxTokens:   64,
		Temperature: 0.7,
		TopP:        0.9,
		Thinking:    &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048, Display: "summarized"},
		Messages:    []ClaudeMessage{{Role: "user", Content: "hello"}},
	}
	meta := normalizeOpus47ClaudeRequest(&req, true)
	if req.Model != "claude-opus-4.7" {
		t.Fatalf("model = %q, want claude-opus-4.7", req.Model)
	}
	if req.Temperature != 0 || req.TopP != 0 {
		t.Fatalf("expected sampling params dropped, got temperature=%v top_p=%v", req.Temperature, req.TopP)
	}
	if req.Thinking == nil || req.Thinking.Type != "adaptive" || req.Thinking.BudgetTokens != 0 {
		t.Fatalf("expected adaptive thinking without budget, got %#v", req.Thinking)
	}
	if !meta.Opus47 || !meta.ThinkingNormalized || !meta.SamplingDropped {
		t.Fatalf("expected metadata flags, got %#v", meta)
	}
}
```

- [ ] **Step 6: Run Opus normalization test and confirm failure**

Run:

```bash
go test ./proxy -run TestNormalizeOpus47ClaudeRequestUsesAdaptiveThinkingAndDropsSampling -count=1
```

Expected: FAIL because `normalizeOpus47ClaudeRequest` is undefined.

- [ ] **Step 7: Implement Opus 4.7 request normalizer**

Add to `proxy/handler.go` near thinking helpers:

```go
type opus47NormalizationMetadata struct {
	Opus47              bool
	ThinkingNormalized bool
	SamplingDropped     bool
}

func normalizeOpus47ClaudeRequest(req *ClaudeRequest, claudeCodeCompatible bool) opus47NormalizationMetadata {
	meta := opus47NormalizationMetadata{}
	if req == nil {
		return meta
	}
	mapped, suffixThinking := ParseModelAndThinking(req.Model, config.GetThinkingConfig().Suffix)
	req.Model = mapped
	if !isOpus47RequestModel(req.Model) {
		return meta
	}
	meta.Opus47 = true
	if req.Temperature != 0 {
		req.Temperature = 0
		meta.SamplingDropped = true
	}
	if req.TopP != 0 {
		req.TopP = 0
		meta.SamplingDropped = true
	}
	if claudeCodeCompatible || suffixThinking || isClaudeThinkingRequested(req.Thinking) {
		display := ""
		if req.Thinking != nil {
			display = req.Thinking.Display
		}
		if req.Thinking == nil || strings.ToLower(strings.TrimSpace(req.Thinking.Type)) != "adaptive" || req.Thinking.BudgetTokens != 0 {
			req.Thinking = &ClaudeThinkingConfig{Type: "adaptive", Display: display}
			meta.ThinkingNormalized = true
		}
	}
	return meta
}
```

In `handleClaudeMessagesInternal`, call it before `resolveClaudeThinkingMode`:

```go
	opusMeta := normalizeOpus47ClaudeRequest(&req, env != nil && (env.SessionID != "" || env.AgentID != "" || strings.Contains(strings.ToLower(env.UserAgent), "claude")))
	updateRequestLogOpus47Normalization(r, opusMeta)
```

Add the `updateRequestLogOpus47Normalization` helper in Task 2 before running full handler tests.

- [ ] **Step 8: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestParseModelAndThinkingNormalizesOfficialOpus47Names|TestIsOpus47RequestModelRecognizesOfficialAndKiroNames|TestNormalizeOpus47ClaudeRequestUsesAdaptiveThinkingAndDropsSampling' -count=1
```

Expected: PASS. The request log helper is implemented in Task 2 before this focused run is used as the merge gate.

- [ ] **Step 9: Commit Task 1 after Task 2 helper exists**

Commit together with Task 2 after the request-log helper exists. Do not create a broken intermediate commit.

---

### Task 2: Official Envelope Metadata and Redacted Request Logs

**Files:**
- Modify: `proxy/anthropic_envelope.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/handler.go`
- Test: `proxy/anthropic_envelope_test.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Add failing envelope test**

Append to `proxy/anthropic_envelope_test.go`:

```go
func TestParseAnthropicEnvelopeCapturesOfficialFieldsAndClaudeCodeHeaders(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-7",
		"max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"container":{"id":"container_1"},
		"context_management":{"clear_function_results":true},
		"mcp_servers":[{"name":"repo"}],
		"metadata":{"user_id":"user-secret"},
		"service_tier":"standard_only",
		"stop_sequences":["STOP"],
		"tool_choice":{"type":"auto"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("anthropic-beta", "fine-grained-tool-streaming-2025-05-14")
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-1")
	req.Header.Set("X-Claude-Code-Parent-Agent-Id", "parent-1")
	req.Header.Set("X-Claude-Code-Project-Dir", "/secret/project")
	req.Header.Set("X-Claude-Code-Version", "2.1.143")

	env, err := parseAnthropicEnvelope(req, body)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	wantKeys := []string{"container", "context_management", "mcp_servers", "metadata", "service_tier", "stop_sequences"}
	if !reflect.DeepEqual(env.OfficialExtraKeys, wantKeys) {
		t.Fatalf("official keys = %#v, want %#v", env.OfficialExtraKeys, wantKeys)
	}
	if env.SessionID != "session-1" || env.AgentID != "agent-1" || env.ParentAgentID != "parent-1" {
		t.Fatalf("claude code headers not captured: %#v", env)
	}
	if env.ProjectDirPresent != true || env.Version != "2.1.143" {
		t.Fatalf("expected project-dir presence and version, got project=%v version=%q", env.ProjectDirPresent, env.Version)
	}
	if !env.HasBetaPrefix("fine-grained-tool-streaming") {
		t.Fatalf("expected fine-grained beta")
	}
}
```

Ensure `proxy/anthropic_envelope_test.go` imports these packages exactly once:

```go
import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)
```

- [ ] **Step 2: Run test and confirm failure**

Run:

```bash
go test ./proxy -run TestParseAnthropicEnvelopeCapturesOfficialFieldsAndClaudeCodeHeaders -count=1
```

Expected: FAIL because `ProjectDirPresent`, `Version`, and some official keys are not captured yet.

- [ ] **Step 3: Extend envelope metadata**

In `proxy/anthropic_envelope.go`, extend `anthropicEnvelope`:

```go
	ProjectDirPresent bool
	Version           string
```

In `parseAnthropicEnvelope`, add:

```go
		ProjectDirPresent: firstNonEmptyHeader(r, "x-claude-code-project-dir", "claude-code-project-dir") != "",
		Version:           firstNonEmptyHeader(r, "x-claude-code-version", "claude-code-version"),
```

In `officialAnthropicExtraKeys`, include `tool_choice` only if it remains in `raw`. Since `tool_choice` is already decoded into `ClaudeRequest`, do not include it in `OfficialExtraKeys` unless it is intentionally left in raw. Keep expected keys as request-level extra fields only:

```go
		"container":          true,
		"context_management": true,
		"mcp_servers":        true,
		"service_tier":       true,
		"metadata":           true,
		"stop_sequences":     true,
		"cache_control":      true,
```

- [ ] **Step 4: Add failing request log metadata test**

Append to `proxy/request_log_test.go`:

```go
func TestUpdateRequestLogAnthropicRedactsOfficialMetadata(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	body := []byte(`{"model":"claude-opus-4-7","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"container":{"id":"secret"},"mcp_servers":[{"name":"repo"},{"name":"browser"}],"service_tier":"standard_only","metadata":{"user_id":"secret-user"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("anthropic-beta", "fine-grained-tool-streaming-2025-05-14")
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	req.Header.Set("X-Claude-Code-Project-Dir", "/secret/project")
	ctx, loggedReq, recorder, _ := h.beginRequestLog(httptest.NewRecorder(), req)
	env, err := parseAnthropicEnvelope(loggedReq, body)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	updateRequestLogAnthropic(loggedReq, env)
	h.finishRequestLog(ctx, recorder)
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one log")
	}
	entry := logs[0]
	if !entry.HasContainer || entry.MCPServerCount != 2 || !entry.HasServiceTier || !entry.AnthropicBetaPresent || !entry.ClaudeCodeProjectDirPresent {
		t.Fatalf("metadata flags not set: %#v", entry)
	}
	raw, _ := json.Marshal(entry)
	if strings.Contains(string(raw), "secret-user") || strings.Contains(string(raw), "/secret/project") || strings.Contains(string(raw), "secret\"") {
		t.Fatalf("request log leaked sensitive metadata: %s", raw)
	}
}
```

- [ ] **Step 5: Run request log test and confirm failure**

Run:

```bash
go test ./proxy -run TestUpdateRequestLogAnthropicRedactsOfficialMetadata -count=1
```

Expected: FAIL because new request log fields do not exist.

- [ ] **Step 6: Extend request log fields**

In `proxy/request_log.go`, add to `RequestLogEntry` near existing Anthropic metadata:

```go
	HasContainer                  bool     `json:"hasContainer,omitempty"`
	HasContextManagement          bool     `json:"hasContextManagement,omitempty"`
	MCPServerCount                int      `json:"mcpServerCount,omitempty"`
	HasServiceTier                bool     `json:"hasServiceTier,omitempty"`
	HasMetadata                   bool     `json:"hasMetadata,omitempty"`
	HasStopSequences              bool     `json:"hasStopSequences,omitempty"`
	ToolChoiceMode                string   `json:"toolChoiceMode,omitempty"`
	AnthropicBetaPresent          bool     `json:"anthropicBetaPresent,omitempty"`
	ClaudeCodeSessionPresent      bool     `json:"claudeCodeSessionPresent,omitempty"`
	ClaudeCodeAgentPresent        bool     `json:"claudeCodeAgentPresent,omitempty"`
	ClaudeCodeParentAgentPresent  bool     `json:"claudeCodeParentAgentPresent,omitempty"`
	ClaudeCodeProjectDirPresent   bool     `json:"claudeCodeProjectDirPresent,omitempty"`
	ClaudeCodeVersionPresent      bool     `json:"claudeCodeVersionPresent,omitempty"`
	Opus47ThinkingNormalized      bool     `json:"opus47ThinkingNormalized,omitempty"`
	Opus47SamplingDropped         bool     `json:"opus47SamplingDropped,omitempty"`
```

Update `updateRequestLogAnthropic` in `proxy/request_log.go` to populate flags:

```go
func updateRequestLogAnthropic(r *http.Request, env *anthropicEnvelope) {
	ctx := requestLogFromContext(r.Context())
	if ctx == nil || env == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.AnthropicRequestID = env.AnthropicRequestID
	ctx.entry.AnthropicVersion = env.AnthropicVersion
	ctx.entry.AnthropicBetas = sortedBetaNames(env.Betas)
	ctx.entry.AnthropicBetaPresent = strings.TrimSpace(env.BetaHeader) != ""
	ctx.entry.ClaudeCodeSessionID = env.SessionID
	ctx.entry.ClaudeCodeAgentID = env.AgentID
	ctx.entry.ClaudeCodeParentAgentID = env.ParentAgentID
	ctx.entry.ClaudeCodeSessionPresent = env.SessionID != ""
	ctx.entry.ClaudeCodeAgentPresent = env.AgentID != ""
	ctx.entry.ClaudeCodeParentAgentPresent = env.ParentAgentID != ""
	ctx.entry.ClaudeCodeProjectDirPresent = env.ProjectDirPresent
	ctx.entry.ClaudeCodeVersionPresent = env.Version != ""
	ctx.entry.PayloadUnknownOfficialFields = append([]string(nil), env.OfficialExtraKeys...)
	ctx.entry.HasContainer = rawHasKey(env.Extra, "container")
	ctx.entry.HasContextManagement = rawHasKey(env.Extra, "context_management")
	ctx.entry.HasServiceTier = rawHasKey(env.Extra, "service_tier")
	ctx.entry.HasMetadata = rawHasKey(env.Extra, "metadata")
	ctx.entry.HasStopSequences = rawHasKey(env.Extra, "stop_sequences")
	ctx.entry.MCPServerCount = rawArrayLen(env.Extra, "mcp_servers")
	ctx.entry.ToolChoiceMode = toolChoiceMode(env.Request.ToolChoice)
}

func rawHasKey(raw map[string]json.RawMessage, key string) bool {
	_, ok := raw[key]
	return ok
}

func rawArrayLen(raw map[string]json.RawMessage, key string) int {
	value, ok := raw[key]
	if !ok {
		return 0
	}
	var arr []interface{}
	if err := json.Unmarshal(value, &arr); err != nil {
		return 0
	}
	return len(arr)
}

func toolChoiceMode(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case map[string]interface{}:
		if kind, _ := v["type"].(string); strings.TrimSpace(kind) != "" {
			return strings.TrimSpace(kind)
		}
	}
	return "present"
}
```

If `updateRequestLogAnthropic` already exists, merge the assignments rather than creating a duplicate.

Add helper for Task 1:

```go
func updateRequestLogOpus47Normalization(r *http.Request, meta opus47NormalizationMetadata) {
	ctx := requestLogFromContext(r.Context())
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.Opus47ThinkingNormalized = meta.ThinkingNormalized
	ctx.entry.Opus47SamplingDropped = meta.SamplingDropped
}
```

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./proxy -run 'TestParseAnthropicEnvelopeCapturesOfficialFieldsAndClaudeCodeHeaders|TestUpdateRequestLogAnthropicRedactsOfficialMetadata|TestNormalizeOpus47ClaudeRequestUsesAdaptiveThinkingAndDropsSampling' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 2 and Task 1 helper together**

Run:

```bash
git add proxy/anthropic_envelope.go proxy/anthropic_envelope_test.go proxy/request_log.go proxy/request_log_test.go proxy/handler.go proxy/handler_test.go proxy/translator.go proxy/translator_test.go
git commit -m "feat: log claude code official metadata"
```

---

### Task 3: `/v1/models`, Count Tokens, and Prewarm Semantics

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/request_log.go`
- Test: `proxy/handler_test.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Add failing `/v1/models` official alias test**

Append to `proxy/handler_test.go`:

```go
func TestAnthropicModelsIncludesOfficialAndKiroOpus47NamesWithoutDuplicateIDs(t *testing.T) {
	models := buildAnthropicModelsResponse([]ModelInfo{{ModelId: "claude-opus-4.7", ModelName: "Claude Opus", InputTypes: []string{"text"}}}, "-thinking")
	ids := map[string]int{}
	for _, model := range models {
		id, _ := model["id"].(string)
		ids[id]++
	}
	if ids["claude-opus-4.7"] != 1 {
		t.Fatalf("expected one kiro opus id, got ids=%#v", ids)
	}
	if ids["claude-opus-4-7"] != 1 {
		t.Fatalf("expected one official opus id, got ids=%#v", ids)
	}
	for id, count := range ids {
		if count != 1 {
			t.Fatalf("duplicate model id %q count=%d", id, count)
		}
	}
}
```

- [ ] **Step 2: Run model list test and confirm failure**

Run:

```bash
go test ./proxy -run TestAnthropicModelsIncludesOfficialAndKiroOpus47NamesWithoutDuplicateIDs -count=1
```

Expected: FAIL if official alias is missing.

- [ ] **Step 3: Implement official Opus alias in model list**

In `proxy/handler.go`, add helper near model list builders:

```go
func appendOfficialModelAliases(models []map[string]interface{}, thinkingSuffix string) []map[string]interface{} {
	seen := make(map[string]bool, len(models)+2)
	for _, model := range models {
		if id, _ := model["id"].(string); id != "" {
			seen[id] = true
		}
	}
	add := func(id string, supportsImage bool) {
		if seen[id] {
			return
		}
		models = append(models, buildModelInfo(id, "anthropic", supportsImage))
		seen[id] = true
	}
	if seen["claude-opus-4.7"] {
		add("claude-opus-4-7", true)
		if thinkingSuffix != "" && seen["claude-opus-4.7"+thinkingSuffix] {
			add("claude-opus-4-7"+thinkingSuffix, true)
		}
	}
	return models
}
```

Call it before returning from `buildAnthropicModelsResponse` and `fallbackAnthropicModels`:

```go
	return appendOfficialModelAliases(models, thinkingSuffix)
```

- [ ] **Step 4: Add failing max_tokens=0 cache prewarm test**

Append to `proxy/handler_test.go`:

```go
func TestClaudeMaxTokensZeroRecordsCachePrewarmModeForCacheControl(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(10), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	body := strings.NewReader(`{
		"model":"claude-opus-4-7",
		"max_tokens":0,
		"system":[{"type":"text","text":"System prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"messages":[{"role":"user","content":"warm cache"}],
		"tools":[{"name":"read_file","description":"read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", body)
	w := httptest.NewRecorder()
	ctx, loggedReq, recorder, loggedWriter := h.beginRequestLog(w, req)
	h.handleClaudeMessages(loggedWriter, loggedReq)
	h.finishRequestLog(ctx, recorder)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected one log")
	}
	if logs[0].MaxTokensZeroMode != "cache_prewarm" {
		t.Fatalf("max_tokens zero mode = %q, want cache_prewarm", logs[0].MaxTokensZeroMode)
	}
	if logs[0].CacheCreationInputTokens <= 0 {
		t.Fatalf("expected cache creation estimate, got %#v", logs[0])
	}
}
```


- [ ] **Step 5: Run prewarm test and confirm failure**

Run:

```bash
go test ./proxy -run TestClaudeMaxTokensZeroRecordsCachePrewarmModeForCacheControl -count=1
```

Expected: FAIL because max_tokens=0 currently records `local_zero_output` even with cache control.

- [ ] **Step 6: Implement cache prewarm mode selection**

Add helper to `proxy/handler.go`:

```go
func claudeRequestHasCacheControl(req *ClaudeRequest) bool {
	if req == nil {
		return false
	}
	if systemHasCacheControl(req.System) {
		return true
	}
	for _, tool := range req.Tools {
		if len(tool.CacheControl) > 0 {
			return true
		}
	}
	for _, msg := range req.Messages {
		if contentHasCacheControl(msg.Content) {
			return true
		}
	}
	return false
}

func systemHasCacheControl(system interface{}) bool {
	return contentHasCacheControl(system)
}

func contentHasCacheControl(content interface{}) bool {
	switch v := content.(type) {
	case []interface{}:
		for _, item := range v {
			if block, ok := item.(map[string]interface{}); ok {
				if _, ok := block["cache_control"]; ok {
					return true
				}
			}
		}
	case []ClaudeContentBlock:
		return false
	case map[string]interface{}:
		_, ok := v["cache_control"]
		return ok
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return false
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	return contentHasCacheControl(decoded)
}
```

In the max_tokens=0 branch in `handleClaudeMessagesInternal`, set:

```go
		mode := "local_zero_output"
		cacheCreation := 0
		if claudeRequestHasCacheControl(effectiveReq) {
			mode = "cache_prewarm"
			cacheCreation = estimatedInputTokens
		}
		updateRequestLogMaxTokensZeroMode(r, mode)
		updateRequestLogUsage(r, estimatedInputTokens, 0, 0, cacheCreation)
```

Update response usage:

```go
				CacheCreationInputTokens: cacheCreation,
```

- [ ] **Step 7: Run Task 3 tests**

Run:

```bash
go test ./proxy -run 'TestAnthropicModelsIncludesOfficialAndKiroOpus47NamesWithoutDuplicateIDs|TestClaudeMaxTokensZeroRecordsCachePrewarmModeForCacheControl' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go proxy/request_log.go proxy/request_log_test.go
git commit -m "feat: expose opus aliases and cache prewarm mode"
```

---

### Task 4: Adaptive Admission Metrics and Effective Concurrency

**Files:**
- Modify: `proxy/opus_gate.go`
- Modify: `proxy/request_log.go`
- Modify: `proxy/handler.go`
- Test: `proxy/opus_gate_test.go`
- Test: `proxy/request_log_test.go`

- [ ] **Step 1: Add failing adaptive admission test**

Create or append to `proxy/opus_gate_test.go`:

```go
func TestModelAdmissionGateReducesAndRecoversEffectiveConcurrency(t *testing.T) {
	g := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 10},
		},
	})
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 4 {
		t.Fatalf("initial effective concurrency = %d, want 4", got)
	}
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 2 {
		t.Fatalf("after first pressure effective concurrency = %d, want 2", got)
	}
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 1 {
		t.Fatalf("after second pressure effective concurrency = %d, want 1", got)
	}
	now = now.Add(2 * time.Minute)
	g.recordSuccess("claude-opus-4.7", 2*time.Second)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 2 {
		t.Fatalf("after recovery effective concurrency = %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run test and confirm failure**

Run:

```bash
go test ./proxy -run TestModelAdmissionGateReducesAndRecoversEffectiveConcurrency -count=1
```

Expected: FAIL because `effectiveMaxConcurrent` and `recordSuccess` are missing or pressure always reduces directly to 1.

- [ ] **Step 3: Extend admission pressure state**

In `proxy/opus_gate.go`, update state structs:

```go
type admissionPressureState struct {
	score                  int
	expiresAt              time.Time
	effectiveMaxConcurrent int
	recentCapacityErrors   int
	recentQueueTimeouts    int
	recentSuccesses        int
	lastPressureAt         time.Time
	lastSuccessAt          time.Time
}
```

Extend `AdmissionPressureSnapshot`:

```go
	EffectiveMaxConcurrent int `json:"effectiveMaxConcurrent,omitempty"`
	QueueDepth             int `json:"queueDepth,omitempty"`
	ActiveRequests         int `json:"activeRequests,omitempty"`
	RecentCapacityErrors   int `json:"recentCapacityErrors,omitempty"`
	RecentQueueTimeouts    int `json:"recentQueueTimeouts,omitempty"`
	RecentSuccesses        int `json:"recentSuccesses,omitempty"`
```

Extend `opus47Gate`:

```go
	mu     sync.RWMutex
	active int
```

In `opus47Gate.acquire`, increment/decrement active around slot acquisition and release:

```go
	select {
	case g.slots <- struct{}{}:
	case <-timer.C:
		<-g.queue
		return nil, errOpus47GateTimeout
	}
	g.mu.Lock()
	g.active++
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.slots
			<-g.queue
			g.mu.Lock()
			if g.active > 0 {
				g.active--
			}
			g.mu.Unlock()
		})
	}, nil
```

Add helper:

```go
func (g *opus47Gate) snapshot() (active int, queueDepth int) {
	if g == nil {
		return 0, 0
	}
	g.mu.RLock()
	active = g.active
	g.mu.RUnlock()
	return active, len(g.queue)
}
```

- [ ] **Step 4: Implement adaptive effective concurrency**

In `proxy/opus_gate.go`, add:

```go
func (g *modelAdmissionGateSet) effectiveMaxConcurrent(model string) int {
	if g == nil {
		return 0
	}
	model = normalizeAdmissionModel(model)
	g.mu.RLock()
	defer g.mu.RUnlock()
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		return 0
	}
	state := g.pressure[model]
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	if state == nil || now.After(state.expiresAt) || state.effectiveMaxConcurrent <= 0 {
		return gate.maxConcurrent
	}
	return state.effectiveMaxConcurrent
}

func (g *modelAdmissionGateSet) pressureStateForUpdateLocked(model string, gate *adaptiveAdmissionGate, now time.Time) *admissionPressureState {
	if g.pressure == nil {
		g.pressure = make(map[string]*admissionPressureState)
	}
	state := g.pressure[model]
	if state == nil || now.After(state.expiresAt) {
		state = &admissionPressureState{effectiveMaxConcurrent: gate.maxConcurrent}
		g.pressure[model] = state
	}
	if state.effectiveMaxConcurrent <= 0 {
		state.effectiveMaxConcurrent = gate.maxConcurrent
	}
	return state
}
```

Update `recordPressureUntil` to reduce gradually:

```go
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		return
	}
	state := g.pressureStateForUpdateLocked(model, gate, now)
	state.score += score
	if state.score > 6 {
		state.score = 6
	}
	next := state.effectiveMaxConcurrent / 2
	if next < 1 {
		next = 1
	}
	state.effectiveMaxConcurrent = next
	if statusCode == http.StatusTooManyRequests {
		state.recentCapacityErrors++
	}
	if statusCode == http.StatusServiceUnavailable {
		state.recentQueueTimeouts++
	}
	state.lastPressureAt = now
	state.expiresAt = expiresAt
```

Add recovery:

```go
func (g *modelAdmissionGateSet) recordSuccess(model string, latency time.Duration) {
	if g == nil {
		return
	}
	model = normalizeAdmissionModel(model)
	g.mu.Lock()
	defer g.mu.Unlock()
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		return
	}
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	state := g.pressureStateForUpdateLocked(model, gate, now)
	state.recentSuccesses++
	state.lastSuccessAt = now
	if latency < 5*time.Second && now.After(state.expiresAt) && state.effectiveMaxConcurrent < gate.maxConcurrent {
		state.effectiveMaxConcurrent++
	}
	if state.score > 0 && latency < 5*time.Second {
		state.score--
	}
}
```

Update `acquire` to choose a gate based on `effectiveMaxConcurrent`. If the effective value is `1`, use `reduced`; if it equals base max, use `base`. For intermediate values, add a `dynamic` map to `adaptiveAdmissionGate`:

```go
type adaptiveAdmissionGate struct {
	maxConcurrent int
	base          *opus47Gate
	reduced       *opus47Gate
	dynamic       map[int]*opus47Gate
	maxWaiting    int
}

func newAdaptiveAdmissionGate(maxConcurrent, maxWaiting int) *adaptiveAdmissionGate {
	return &adaptiveAdmissionGate{
		maxConcurrent: maxConcurrent,
		maxWaiting:    maxWaiting,
		base:          newOpus47Gate(maxConcurrent, maxWaiting),
		reduced:       newOpus47Gate(1, maxWaiting),
		dynamic:       make(map[int]*opus47Gate),
	}
}

func (g *adaptiveAdmissionGate) gateForLimit(limit int) *opus47Gate {
	if g == nil {
		return nil
	}
	if limit >= g.maxConcurrent {
		return g.base
	}
	if limit <= 1 {
		return g.reduced
	}
	if existing := g.dynamic[limit]; existing != nil {
		return existing
	}
	next := newOpus47Gate(limit, g.maxWaiting)
	g.dynamic[limit] = next
	return next
}
```

- [ ] **Step 5: Wire success and timeout metrics from handler**

In `proxy/handler.go`, where `acquireOpus47AdmissionForRequest` returns timeout, update request log with admission wait and effective limit. If no helper exists, add:

```go
func updateRequestLogAdmission(r *http.Request, wait time.Duration, effectiveLimit int, pressureScore int) {
	ctx := requestLogFromContext(r.Context())
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.AdmissionWaitMs = wait.Milliseconds()
	ctx.entry.EffectiveConcurrentLimit = effectiveLimit
	ctx.entry.AdmissionPressureScore = pressureScore
}
```

In `RequestLogEntry`, add:

```go
	AdmissionWaitMs          int64 `json:"admissionWaitMs,omitempty"`
	EffectiveConcurrentLimit int   `json:"effectiveConcurrentLimit,omitempty"`
	AdmissionPressureScore   int   `json:"admissionPressureScore,omitempty"`
	CapacityRetryCount       int   `json:"capacityRetryCount,omitempty"`
```

When a Claude attempt finishes successfully in `handleClaudeStreamAttempt` and `handleClaudeNonStreamAttempt`, replace the current success pressure update:

```go
	modelAdmissionGate.recordPressure(model, http.StatusOK, latency)
```

with:

```go
	modelAdmissionGate.recordSuccess(model, latency)
```

Keep the existing `recordPressureUntil` calls for upstream errors.

- [ ] **Step 6: Update admission pressure snapshot**

In `snapshot`, set:

```go
effective := maxConcurrent
if state.effectiveMaxConcurrent > 0 && active {
	effective = state.effectiveMaxConcurrent
}
var activeRequests, queueDepth int
if gate != nil {
	activeRequests, queueDepth = gate.gateForLimit(effective).snapshot()
}
```

Populate new JSON fields:

```go
EffectiveMaxConcurrent: effective,
QueueDepth:             queueDepth,
ActiveRequests:         activeRequests,
RecentCapacityErrors:   state.recentCapacityErrors,
RecentQueueTimeouts:    state.recentQueueTimeouts,
RecentSuccesses:        state.recentSuccesses,
```

- [ ] **Step 7: Run admission tests**

Run:

```bash
go test ./proxy -run 'TestModelAdmissionGateReducesAndRecoversEffectiveConcurrency|Test.*Admission' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 4**

Run:

```bash
git add proxy/opus_gate.go proxy/opus_gate_test.go proxy/request_log.go proxy/request_log_test.go proxy/handler.go proxy/handler_test.go
git commit -m "feat: adapt model admission under opus pressure"
```

---

### Task 5: Readiness API and Admin UI Evidence

**Files:**
- Modify: `proxy/handler.go`
- Modify: `web/index.html`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Add failing readiness test**

Append to `proxy/handler_test.go`:

```go
func TestClaudeCodeModelReadinessIncludesAdmissionPressure(t *testing.T) {
	originalGate := modelAdmissionGate
	t.Cleanup(func() { modelAdmissionGate = originalGate })
	modelAdmissionGate = newModelAdmissionGateSet(config.ModelAdmissionConfig{Models: map[string]config.ModelAdmissionRule{"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 10}}})
	h := &Handler{
		pool:        &pool.AccountPool{},
		startTime:   time.Now().Unix(),
		requestLogs: newRequestLogStore(5),
	}
	modelAdmissionGate.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/claude-code/model-readiness?model=claude-opus-4-7", nil)
	w := httptest.NewRecorder()
	h.apiGetClaudeCodeModelReadiness(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pressure, ok := body["admissionPressure"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected admissionPressure object, got %#v", body)
	}
	if pressure["active"] != true {
		t.Fatalf("expected active pressure, got %#v", pressure)
	}
}
```


- [ ] **Step 2: Run test and confirm failure**

Run:

```bash
go test ./proxy -run TestClaudeCodeModelReadinessIncludesAdmissionPressure -count=1
```

Expected: FAIL because model readiness does not include `admissionPressure`.

- [ ] **Step 3: Add readiness admission pressure**

In `apiGetClaudeCodeModelReadiness`, add:

```go
	resp["admissionPressure"] = h.admissionPressureForModel(mapped)
```

Add helper:

```go
func (h *Handler) admissionPressureForModel(model string) map[string]interface{} {
	if modelAdmissionGate == nil {
		return map[string]interface{}{"active": false}
	}
	for _, snap := range modelAdmissionGate.snapshot() {
		if normalizeAdmissionModel(snap.Model) == normalizeAdmissionModel(model) {
			return map[string]interface{}{
				"model":                  snap.Model,
				"active":                 snap.Active,
				"score":                  snap.Score,
				"reducedConcurrency":     snap.ReducedConcurrency,
				"maxConcurrent":          snap.MaxConcurrent,
				"effectiveMaxConcurrent": snap.EffectiveMaxConcurrent,
				"queueDepth":             snap.QueueDepth,
				"activeRequests":         snap.ActiveRequests,
				"recentCapacityErrors":   snap.RecentCapacityErrors,
				"recentQueueTimeouts":    snap.RecentQueueTimeouts,
				"recentSuccesses":        snap.RecentSuccesses,
				"expiresInMs":            snap.ExpiresInMs,
			}
		}
	}
	return map[string]interface{}{"active": false, "model": normalizeAdmissionModel(model)}
}
```

- [ ] **Step 4: Update Claude Code readiness capability**

In `apiGetClaudeCodeReadiness`, add a capability:

```go
"opus47AdaptiveAdmission": basicCapability("PASS", "Opus 4.7 model-capacity pressure is tracked separately from account health and can reduce effective concurrency"),
```

Add recent log evidence if `AdmissionPressureScore > 0`:

```go
if entry.AdmissionPressureScore > 0 || entry.EffectiveConcurrentLimit > 0 {
	resp["recentAdmissionPressure"] = true
}
```

- [ ] **Step 5: Update admin UI rendering**

In `web/index.html`, update `loadClaudeCodeModelReadiness` / renderer around `claude-code-model-readiness` to include an admission pressure panel. Add JS helper:

```javascript
function renderAdmissionPressure(pressure) {
    if (!pressure || pressure.active === false) {
        return '<div class="muted">Admission pressure: inactive</div>';
    }
    return '<div class="meta-grid">' +
        '<span>Pressure score: ' + escapeHtml(String(pressure.score || 0)) + '</span>' +
        '<span>Effective concurrency: ' + escapeHtml(String(pressure.effectiveMaxConcurrent || 0)) + '/' + escapeHtml(String(pressure.maxConcurrent || 0)) + '</span>' +
        '<span>Queue: ' + escapeHtml(String(pressure.queueDepth || 0)) + '</span>' +
        '<span>Active: ' + escapeHtml(String(pressure.activeRequests || 0)) + '</span>' +
        '<span>Capacity errors: ' + escapeHtml(String(pressure.recentCapacityErrors || 0)) + '</span>' +
        '<span>Queue timeouts: ' + escapeHtml(String(pressure.recentQueueTimeouts || 0)) + '</span>' +
        '</div>';
}
```

Insert the returned HTML above the account readiness table.

Update request log table row rendering to include:

```javascript
'queue=' + compactValue(log.queueWaitMs || log.admissionWaitMs),
'limit=' + compactValue(log.effectiveConcurrentLimit),
'pressure=' + compactValue(log.admissionPressureScore),
```

Keep text compact so mobile does not overflow.

- [ ] **Step 6: Run readiness/UI-adjacent tests**

Run:

```bash
go test ./proxy -run 'TestClaudeCodeModelReadinessIncludesAdmissionPressure|Test.*Readiness' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 5**

Run:

```bash
git add proxy/handler.go proxy/handler_test.go web/index.html
git commit -m "feat: show opus admission readiness"
```

---

### Task 6: Full Test Run and Docker Deployment

**Files:**
- No source changes unless a test failure exposes a defect in previous tasks.
- UAT output under `docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519/`.

- [ ] **Step 1: Run full Go tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS for all packages.

- [ ] **Step 2: Rebuild Kiro-Go Docker service**

Run:

```bash
docker compose up -d --build kiro-go
```

Expected: container `kiro-go-kiro-go-1` restarts successfully.

- [ ] **Step 3: Check Kiro-Go and sub2api health**

Run:

```bash
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:18080/health
docker ps --format '{{.Names}}\t{{.Status}}' | rg 'kiro-go|sub2api'
```

Expected:

- Kiro-Go health JSON contains `"status":"ok"`.
- sub2api health JSON contains `"status":"ok"`.
- `kiro-go-kiro-go-1` is Up.
- `sub2api`, `sub2api-postgres`, and `sub2api-redis` are Up/healthy.

- [ ] **Step 4: Commit deployment-neutral code before real UAT**

Run:

```bash
git status --short
```

Expected: only intended source/test/UI files from Tasks 1-5 are committed. UAT output can remain uncommitted until Task 7.

---

### Task 7: Real API, Browser, Database, and Screenshot UAT

**Files:**
- Create: `docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519/UAT-SUMMARY.md`
- Create: `docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519/*.json`
- Create: `docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519/playwright/*`

- [ ] **Step 1: Prepare UAT directory**

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
mkdir -p "$UAT_DIR/playwright"
```

Expected: directory exists.

- [ ] **Step 2: Capture Kiro-Go readiness API evidence**

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
ADMIN_PASSWORD=$(jq -r '.password' data/config.json)
curl -sS 'http://127.0.0.1:8080/admin/api/claude-code/readiness' -H "X-Admin-Password: $ADMIN_PASSWORD" -o "$UAT_DIR/kiro-claude-readiness.json"
curl -sS 'http://127.0.0.1:8080/admin/api/claude-code/model-readiness?model=claude-opus-4-7' -H "X-Admin-Password: $ADMIN_PASSWORD" -o "$UAT_DIR/kiro-opus47-readiness.json"
curl -sS 'http://127.0.0.1:8080/admin/api/admission-pressure' -H "X-Admin-Password: $ADMIN_PASSWORD" -o "$UAT_DIR/kiro-admission-pressure.json"
curl -sS 'http://127.0.0.1:8080/admin/api/accounts' -H "X-Admin-Password: $ADMIN_PASSWORD" -o "$UAT_DIR/kiro-accounts.json"
jq '{routingReason,total:(.accounts|length),schedulable:([.accounts[]|select(.schedulable==true)]|length),admissionPressure}' "$UAT_DIR/kiro-opus47-readiness.json"
```

Expected: JSON shows Opus 4.7 readiness with `admissionPressure` object and account rows.

- [ ] **Step 3: Direct Kiro-Go smoke calls**

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
API_KEY=$(jq -r '.apiKey' data/config.json)
curl -sS http://127.0.0.1:8080/v1/messages \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -H "anthropic-beta: fine-grained-tool-streaming-2025-05-14" \
  -H "X-Claude-Code-Session-Id: uat-session-opus47" \
  -H "X-Claude-Code-Agent-Id: uat-agent" \
  -d '{"model":"claude-opus-4-7","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":2048,"display":"omitted"},"temperature":0.7,"messages":[{"role":"user","content":"Return exactly: KIro-Go opus47 parity ok"}]}' \
  -o "$UAT_DIR/direct-opus47.json"
jq '{type,model,stop_reason,content}' "$UAT_DIR/direct-opus47.json"
```

Expected: response type is `message`, content includes `KIro-Go opus47 parity ok`, and no upstream 400 occurs from incompatible Opus 4.7 params.

- [ ] **Step 4: Direct max_tokens=0 prewarm call**

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
API_KEY=$(jq -r '.apiKey' data/config.json)
curl -sS http://127.0.0.1:8080/v1/messages \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-opus-4-7","max_tokens":0,"system":[{"type":"text","text":"UAT cache prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"warm cache"}],"tools":[{"name":"read_file","description":"read files","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]}' \
  -o "$UAT_DIR/direct-opus47-prewarm.json"
jq '{type,model,stop_reason,usage}' "$UAT_DIR/direct-opus47-prewarm.json"
```

Expected: response has `stop_reason:"max_tokens"` and usage includes cache creation estimate or request logs mark `cache_prewarm`.

- [ ] **Step 5: sub2api database evidence**

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc "select 'users=' || count(*) from users; select 'groups=' || count(*) from groups; select 'accounts=' || count(*) from accounts; select 'api_keys=' || count(*) from api_keys;" > "$UAT_DIR/sub2api-db-counts.txt"
cat "$UAT_DIR/sub2api-db-counts.txt"
```

Expected: counts are nonzero for the deployed sub2api environment. If table names differ, inspect `\dt` and record the exact query used in `UAT-SUMMARY.md`.

- [ ] **Step 6: sub2api black-box real generation**

Use the existing local sub2api API key source without printing secrets. If `/tmp/sub2api_api_key` exists, use it; otherwise extract from the local database into an environment variable without writing it to UAT files.

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
if [ -f /tmp/sub2api_api_key ]; then
  SUB2API_KEY=$(cat /tmp/sub2api_api_key)
else
  SUB2API_KEY=$(docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc "select key from api_keys where disabled_at is null order by id limit 1" | tr -d '[:space:]')
fi
curl -sS http://127.0.0.1:18080/v1/messages \
  -H "Authorization: Bearer $SUB2API_KEY" \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -H "X-Claude-Code-Session-Id: sub2api-uat-session" \
  -H "X-Claude-Code-Agent-Id: sub2api-uat-agent" \
  -d '{"model":"claude-opus-4-7","max_tokens":64,"messages":[{"role":"user","content":"Return exactly: sub2api to Kiro-Go opus47 ok"}]}' \
  -o "$UAT_DIR/sub2api-opus47.json"
jq '{type,model,stop_reason,content,error}' "$UAT_DIR/sub2api-opus47.json"
```

Expected: response contains `sub2api to Kiro-Go opus47 ok`. If it fails due upstream capacity, capture Kiro-Go request logs proving the failure is capacity/admission, not account health.

- [ ] **Step 7: Capture request logs after API calls**

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
ADMIN_PASSWORD=$(jq -r '.password' data/config.json)
curl -sS 'http://127.0.0.1:8080/admin/api/request-logs?limit=100' -H "X-Admin-Password: $ADMIN_PASSWORD" -o "$UAT_DIR/kiro-request-logs.json"
jq '{count:(.logs|length), opus47:([.logs[]|select((.model//"")|test("opus-4[.-]7"))]|length), cachePrewarm:([.logs[]|select(.maxTokensZeroMode=="cache_prewarm")]|length), claudeCodeHeaders:([.logs[]|select(.claudeCodeSessionPresent==true or (.claudeCodeSessionId//"")!="")]|length), errors:([.logs[]|select((.error//"")!="")|{statusCode,error,model,accountId,admissionWaitMs,effectiveConcurrentLimit,admissionPressureScore}])}' "$UAT_DIR/kiro-request-logs.json"
```

Expected: logs show Opus 4.7 requests, cache prewarm, and Claude Code metadata presence when headers arrived.

- [ ] **Step 8: Browser Playwright admin verification**

Create `/tmp/kiro-go-admin-uat.js`:

```javascript
const fs = require('fs');
const { chromium } = require('playwright');

const outDir = '/www/Kiro-Go/docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519/playwright';
const password = process.env.ADMIN_PASSWORD;
fs.mkdirSync(outDir, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROME_PATH || undefined });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  await page.goto('http://127.0.0.1:8080/admin', { waitUntil: 'networkidle' });
  await page.fill('input[type="password"]', password);
  await page.click('button');
  await page.waitForLoadState('networkidle');
  await page.screenshot({ path: `${outDir}/admin-dashboard.png`, fullPage: true });
  const bodyText = await page.locator('body').innerText();
  fs.writeFileSync(`${outDir}/admin-body.txt`, bodyText);
  const hasRiskGroup = bodyText.includes('风险组') || bodyText.includes('Risk Group');
  const hasClaudeCode = bodyText.includes('Claude Code');
  if (!hasRiskGroup || !hasClaudeCode) {
    throw new Error(`admin dashboard missing expected text riskGroup=${hasRiskGroup} claudeCode=${hasClaudeCode}`);
  }
  await page.evaluate(() => {
    const buttons = Array.from(document.querySelectorAll('button'));
    const readiness = buttons.find((button) => /Claude Code|Readiness|刷新|Refresh/.test(button.textContent || ''));
    if (readiness) readiness.click();
  });
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${outDir}/admin-readiness.png`, fullPage: true });
  const readinessText = await page.locator('body').innerText();
  fs.writeFileSync(`${outDir}/admin-readiness-text.txt`, readinessText);
  const hasAdmission = /Admission pressure|Pressure score|admission/i.test(readinessText);
  const hasCooldown = /Cooldown|冷却/i.test(readinessText);
  fs.writeFileSync(`${outDir}/browser-result.json`, JSON.stringify({ hasRiskGroup, hasClaudeCode, hasAdmission, hasCooldown }, null, 2));
  if (!hasAdmission) {
    throw new Error('readiness page missing admission pressure evidence');
  }
  await browser.close();
})().catch(async (err) => {
  console.error(err);
  process.exit(1);
});
```

Run:

```bash
rm -rf /tmp/kiro-playwright-uat && mkdir -p /tmp/kiro-playwright-uat
cp /tmp/kiro-go-admin-uat.js /tmp/kiro-playwright-uat/uat.js
cd /tmp/kiro-playwright-uat
npm init -y >/dev/null
npm install playwright@latest >/dev/null
ADMIN_PASSWORD=$(jq -r '.password' /www/Kiro-Go/data/config.json) node uat.js
```

Expected: screenshots and `browser-result.json` are created, and script exits 0.

- [ ] **Step 9: Analyze screenshots**

Run:

```bash
UAT_DIR=docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
ls -lh "$UAT_DIR/playwright"
sed -n '1,160p' "$UAT_DIR/playwright/browser-result.json"
sed -n '1,120p' "$UAT_DIR/playwright/admin-readiness-text.txt"
```

Expected:

- `admin-dashboard.png` and `admin-readiness.png` exist and are nonzero.
- text includes Claude Code readiness and admission pressure evidence.
- If text is missing or screenshot is blank, do not mark PASS.

- [ ] **Step 10: Write UAT summary**

Create `docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519/UAT-SUMMARY.md` with this exact structure:

```markdown
# Kiro-Go Claude Code Parity and Opus 4.7 UAT

Date: 2026-05-19

## Verdict

PASS or PARTIAL with reason.

## Commands

- `go test ./... -count=1`: PASS or FAIL
- `docker compose up -d --build kiro-go`: PASS or FAIL
- Kiro-Go health: PASS or FAIL
- sub2api health: PASS or FAIL

## API Evidence

- `kiro-claude-readiness.json`
- `kiro-opus47-readiness.json`
- `kiro-admission-pressure.json`
- `kiro-accounts.json`
- `direct-opus47.json`
- `direct-opus47-prewarm.json`
- `sub2api-opus47.json`
- `kiro-request-logs.json`

## Browser Evidence

- `playwright/admin-dashboard.png`: describe visible risk group/account/Claude Code evidence.
- `playwright/admin-readiness.png`: describe visible admission pressure/readiness evidence.
- `playwright/browser-result.json`: summarize boolean checks.

## Database Evidence

Paste non-secret `sub2api-db-counts.txt` counts.

## Account Health

State whether temporary limits, risk-group cooldown, or false account failures were observed.

## Concurrency Health

State Opus 4.7 effective concurrency, pressure score, queue timeouts, and capacity errors from request logs/readiness.

## sub2api Black-Box Result

State whether live sub2api successfully called Kiro-Go and returned the expected marker.

## Screenshot Analysis

State whether screenshots were nonblank and whether visible text matches expected admin/readiness state.
```

Set verdict:

- `PASS` only if all required checks pass and screenshots are correct.
- `PARTIAL` if upstream Opus 4.7 capacity prevents success but Kiro-Go classifies it correctly and sub2api remains healthy.
- `FAIL` if Kiro-Go breaks direct calls, sub2api cannot call Kiro-Go, screenshots are blank, or account health is falsely damaged.

- [ ] **Step 11: Commit UAT evidence**

Run:

```bash
git add docs/superpowers/uat/kiro-go-claude-parity-opus47-20260519
git commit -m "test: add claude parity opus47 uat evidence"
```

---

## Final Verification

- [ ] **Step 1: Run full tests again**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Confirm Docker health again**

Run:

```bash
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:18080/health
docker ps --format '{{.Names}}\t{{.Status}}' | rg 'kiro-go|sub2api'
```

Expected: both services healthy/up.

- [ ] **Step 3: Confirm no accidental sub2api source changes**

Run:

```bash
git -C /www/sub2api status --short
```

Expected: no changes caused by this work. Existing unrelated sub2api changes must be reported as pre-existing and must not be committed from Kiro-Go.

- [ ] **Step 4: Final status summary**

Run:

```bash
git status --short
git log --oneline -6
```

Expected: only intended Kiro-Go changes and UAT evidence are present/committed. Report any remaining uncommitted pre-existing files separately.
