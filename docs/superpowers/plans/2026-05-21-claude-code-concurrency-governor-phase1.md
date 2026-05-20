# Claude Code Concurrency Governor Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first Kiro-Go Claude Code Governor slice: classify Claude Code requests, log governor metadata, and prevent same-session subagents from starving the foreground Opus 4.7 turn.

**Architecture:** Keep the existing Opus 4.7 `modelAdmissionGate` as the global model safety cap. Add a separate Claude Code session governor that runs before the model admission gate and outside account retry loops, so queued subagents do not occupy global Opus 4.7 capacity. Classification and logging are implemented in small focused files and wired into the existing Claude/OpenAI request entry points after request parsing.

**Tech Stack:** Go, `net/http`, existing Kiro-Go config/request-log patterns, existing `go test` package tests, Docker/sub2api UAT in later phases.

---

## Scope

This plan implements only Phase 1 from `docs/superpowers/specs/2026-05-21-claude-code-concurrency-governor-design.md`:

- request classification
- request log fields
- per-session main/subagent concurrency gate
- handler wiring before existing Opus 4.7 admission
- focused Go tests

This plan does not implement the later account health state machine, first-token EWMA scheduler, priority model queue, background quiet mode, or Admin UI panel. Those should be separate plans after this phase passes.

## File Map

- Create `proxy/request_classifier.go`: request lane types, classification input/output, metadata parsing helpers.
- Create `proxy/request_classifier_test.go`: classifier unit tests.
- Create `proxy/claude_code_concurrency_governor.go`: session/subagent gate and handler acquisition wrapper.
- Create `proxy/claude_code_concurrency_governor_test.go`: pure governor concurrency tests.
- Modify `config/config.go`: add `ClaudeCodeGovernorConfig`, defaults, normalization, validation, getter.
- Modify `config/config_test.go`: config default and validation tests.
- Modify `proxy/request_log.go`: add request log fields and updater helpers.
- Modify `proxy/request_log_test.go`: request log updater tests.
- Modify `proxy/anthropic_envelope.go`: parse `metadata.user_id` without leaking full metadata.
- Modify `proxy/anthropic_envelope_test.go`: metadata user id extraction test.
- Modify `proxy/handler.go`: add governor field, initialize it, classify requests in handlers, acquire session governor before Opus admission.
- Modify `proxy/handler_test.go`: handler-level wiring tests.

---

### Task 1: Add Governor Config

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add these tests to `config/config_test.go` near the model admission tests:

```go
func TestClaudeCodeGovernorConfigDefaultsDisabled(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}

	got := GetClaudeCodeGovernorConfig()
	if got.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if len(got.Models) != 1 || got.Models[0] != "claude-opus-4.7" {
		t.Fatalf("Models = %#v, want claude-opus-4.7", got.Models)
	}
	if got.InteractiveReservedPerSession != 1 {
		t.Fatalf("InteractiveReservedPerSession = %d, want 1", got.InteractiveReservedPerSession)
	}
	if got.SubagentMaxConcurrentPerSession != 2 {
		t.Fatalf("SubagentMaxConcurrentPerSession = %d, want 2", got.SubagentMaxConcurrentPerSession)
	}
	if got.QueueMaxDepth != 300 {
		t.Fatalf("QueueMaxDepth = %d, want 300", got.QueueMaxDepth)
	}
}

func TestValidateClaudeCodeGovernorConfig(t *testing.T) {
	valid := ClaudeCodeGovernorConfig{
		Enabled:                         true,
		Models:                          []string{"claude-opus-4.7"},
		InteractiveReservedPerSession:   1,
		SubagentMaxConcurrentPerSession: 2,
		BackgroundMaxConcurrent:         1,
		QueueMaxDepth:                   300,
		InteractiveMaxWaitSeconds:       120,
		SubagentMaxWaitSeconds:          90,
		BackgroundMaxWaitSeconds:        15,
	}
	if err := ValidateClaudeCodeGovernorConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	invalid := valid
	invalid.SubagentMaxConcurrentPerSession = -1
	if err := ValidateClaudeCodeGovernorConfig(invalid); err == nil {
		t.Fatalf("expected invalid subagent concurrency to be rejected")
	}
	invalid = valid
	invalid.QueueMaxDepth = -1
	if err := ValidateClaudeCodeGovernorConfig(invalid); err == nil {
		t.Fatalf("expected invalid queue depth to be rejected")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./config -run 'TestClaudeCodeGovernorConfig|TestValidateClaudeCodeGovernorConfig' -count=1
```

Expected: FAIL with `undefined: GetClaudeCodeGovernorConfig` or `undefined: ClaudeCodeGovernorConfig`.

- [ ] **Step 3: Implement config type, defaults, validation, and getter**

In `config/config.go`, add this type near `ContentContinuityConfig`:

```go
type ClaudeCodeGovernorConfig struct {
	Enabled                         bool     `json:"enabled"`
	Models                          []string `json:"models"`
	InteractiveReservedPerSession   int      `json:"interactiveReservedPerSession"`
	SubagentMaxConcurrentPerSession int      `json:"subagentMaxConcurrentPerSession"`
	BackgroundMaxConcurrent         int      `json:"backgroundMaxConcurrent"`
	QueueMaxDepth                   int      `json:"queueMaxDepth"`
	InteractiveMaxWaitSeconds       int      `json:"interactiveMaxWaitSeconds"`
	SubagentMaxWaitSeconds          int      `json:"subagentMaxWaitSeconds"`
	BackgroundMaxWaitSeconds        int      `json:"backgroundMaxWaitSeconds"`
}
```

Add a field to `Config` after `ContentContinuity`:

```go
ClaudeCodeGovernor ClaudeCodeGovernorConfig `json:"claudeCodeGovernor,omitempty"`
```

Add these helper functions near the other default/validate helpers:

```go
func defaultClaudeCodeGovernorConfig() ClaudeCodeGovernorConfig {
	return ClaudeCodeGovernorConfig{
		Enabled:                         false,
		Models:                          []string{"claude-opus-4.7"},
		InteractiveReservedPerSession:   1,
		SubagentMaxConcurrentPerSession: 2,
		BackgroundMaxConcurrent:         1,
		QueueMaxDepth:                   300,
		InteractiveMaxWaitSeconds:       120,
		SubagentMaxWaitSeconds:          90,
		BackgroundMaxWaitSeconds:        15,
	}
}

func normalizeClaudeCodeGovernorConfig(in ClaudeCodeGovernorConfig) ClaudeCodeGovernorConfig {
	defaults := defaultClaudeCodeGovernorConfig()
	if len(in.Models) == 0 {
		in.Models = append([]string(nil), defaults.Models...)
	}
	if in.InteractiveReservedPerSession == 0 {
		in.InteractiveReservedPerSession = defaults.InteractiveReservedPerSession
	}
	if in.SubagentMaxConcurrentPerSession == 0 {
		in.SubagentMaxConcurrentPerSession = defaults.SubagentMaxConcurrentPerSession
	}
	if in.BackgroundMaxConcurrent == 0 {
		in.BackgroundMaxConcurrent = defaults.BackgroundMaxConcurrent
	}
	if in.QueueMaxDepth == 0 {
		in.QueueMaxDepth = defaults.QueueMaxDepth
	}
	if in.InteractiveMaxWaitSeconds == 0 {
		in.InteractiveMaxWaitSeconds = defaults.InteractiveMaxWaitSeconds
	}
	if in.SubagentMaxWaitSeconds == 0 {
		in.SubagentMaxWaitSeconds = defaults.SubagentMaxWaitSeconds
	}
	if in.BackgroundMaxWaitSeconds == 0 {
		in.BackgroundMaxWaitSeconds = defaults.BackgroundMaxWaitSeconds
	}
	return in
}

func ValidateClaudeCodeGovernorConfig(in ClaudeCodeGovernorConfig) error {
	if in.InteractiveReservedPerSession < 0 {
		return fmt.Errorf("interactiveReservedPerSession must be greater than or equal to 0")
	}
	if in.SubagentMaxConcurrentPerSession < 0 {
		return fmt.Errorf("subagentMaxConcurrentPerSession must be greater than or equal to 0")
	}
	if in.BackgroundMaxConcurrent < 0 {
		return fmt.Errorf("backgroundMaxConcurrent must be greater than or equal to 0")
	}
	if in.QueueMaxDepth < 0 {
		return fmt.Errorf("queueMaxDepth must be greater than or equal to 0")
	}
	if in.InteractiveMaxWaitSeconds < 0 || in.SubagentMaxWaitSeconds < 0 || in.BackgroundMaxWaitSeconds < 0 {
		return fmt.Errorf("governor wait seconds must be greater than or equal to 0")
	}
	for _, model := range in.Models {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("models must not contain empty values")
		}
	}
	return nil
}
```

In `defaultConfig()`, add:

```go
ClaudeCodeGovernor: defaultClaudeCodeGovernorConfig(),
```

In `Load()`, after content continuity normalization, add:

```go
c.ClaudeCodeGovernor = normalizeClaudeCodeGovernorConfig(c.ClaudeCodeGovernor)
```

Add this getter near `GetModelAdmissionConfig()`:

```go
func GetClaudeCodeGovernorConfig() ClaudeCodeGovernorConfig {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	if cfg == nil {
		return defaultClaudeCodeGovernorConfig()
	}
	return normalizeClaudeCodeGovernorConfig(cfg.ClaudeCodeGovernor)
}
```

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
go test ./config -run 'TestClaudeCodeGovernorConfig|TestValidateClaudeCodeGovernorConfig' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): add claude code governor settings"
```

---

### Task 2: Add Request Classification

**Files:**
- Create: `proxy/request_classifier.go`
- Create: `proxy/request_classifier_test.go`
- Modify: `proxy/anthropic_envelope.go`
- Modify: `proxy/anthropic_envelope_test.go`

- [ ] **Step 1: Write failing classifier tests**

Create `proxy/request_classifier_test.go`:

```go
package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyGenerationRequestInteractiveClaudeCodeSession(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	r.Header.Set("X-Claude-Code-Session-Id", "session-main")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  r,
		Endpoint: "/v1/messages",
		Model:    "claude-opus-4.7",
		Stream:   true,
	})

	if got.Lane != RequestLaneInteractive {
		t.Fatalf("Lane = %q, want interactive: %#v", got.Lane, got)
	}
	if got.SessionID != "session-main" {
		t.Fatalf("SessionID = %q, want session-main", got.SessionID)
	}
	if !got.ClaudeCode {
		t.Fatalf("ClaudeCode = false, want true")
	}
}

func TestClassifyGenerationRequestSubagentFromParentHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("X-Claude-Code-Session-Id", "session-1")
	r.Header.Set("X-Claude-Code-Agent-Id", "agent-1")
	r.Header.Set("X-Claude-Code-Parent-Agent-Id", "parent-1")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  r,
		Endpoint: "/v1/messages",
		Model:    "claude-opus-4.7",
		Stream:   true,
	})

	if got.Lane != RequestLaneSubagent {
		t.Fatalf("Lane = %q, want subagent: %#v", got.Lane, got)
	}
	if got.AgentID != "agent-1" || got.ParentAgentID != "parent-1" {
		t.Fatalf("agent metadata not preserved: %#v", got)
	}
}

func TestClassifyGenerationRequestBackgroundForCountTokens(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	r.Header.Set("X-Claude-Code-Session-Id", "session-1")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  r,
		Endpoint: "/v1/messages/count_tokens",
		Model:    "claude-opus-4.7",
	})

	if got.Lane != RequestLaneBackground {
		t.Fatalf("Lane = %q, want background: %#v", got.Lane, got)
	}
}

func TestClassifyGenerationRequestConservativeOpenAIDefaultInteractive(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("User-Agent", "sub2api/1.0")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  r,
		Endpoint: "/v1/chat/completions",
		Model:    "claude-opus-4.7",
		Stream:   true,
	})

	if got.Lane != RequestLaneInteractive {
		t.Fatalf("Lane = %q, want conservative interactive: %#v", got.Lane, got)
	}
}
```

Add this test to `proxy/anthropic_envelope_test.go`:

```go
func TestParseAnthropicEnvelopeExtractsMetadataUserID(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4.7",
		"max_tokens":16,
		"metadata":{"user_id":"{\"session_id\":\"session-from-metadata\"}"},
		"messages":[{"role":"user","content":"hello"}]
	}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	env, err := parseAnthropicEnvelope(r, body)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.MetadataUserID != `{"session_id":"session-from-metadata"}` {
		t.Fatalf("MetadataUserID = %q", env.MetadataUserID)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./proxy -run 'TestClassifyGenerationRequest|TestParseAnthropicEnvelopeExtractsMetadataUserID' -count=1
```

Expected: FAIL with undefined classifier types/functions and missing `MetadataUserID`.

- [ ] **Step 3: Implement metadata extraction**

In `proxy/anthropic_envelope.go`, add a field to `anthropicEnvelope`:

```go
MetadataUserID string
```

In `parseAnthropicEnvelope`, set it in the returned struct:

```go
MetadataUserID: metadataUserIDFromRaw(raw["metadata"]),
```

Add this helper near `officialAnthropicExtraKeys`:

```go
func metadataUserIDFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var meta struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	return strings.TrimSpace(meta.UserID)
}
```

- [ ] **Step 4: Implement request classifier**

Create `proxy/request_classifier.go`:

```go
package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
)

type RequestPriorityLane string

const (
	RequestLaneInteractive RequestPriorityLane = "interactive"
	RequestLaneSubagent    RequestPriorityLane = "subagent"
	RequestLaneBackground  RequestPriorityLane = "background"
)

type RequestClassification struct {
	Lane           RequestPriorityLane
	Reason         string
	SessionID      string
	AgentID        string
	ParentAgentID  string
	MetadataUserID string
	ClaudeCode     bool
	Endpoint       string
	Model          string
	Stream         bool
}

type RequestClassificationInput struct {
	Request            *http.Request
	Endpoint           string
	Model              string
	Stream             bool
	Anthropic          *anthropicEnvelope
	Claude             *ClaudeRequest
	OpenAI             *OpenAIRequest
	RawOpenAIResponses map[string]interface{}
}

func classifyGenerationRequest(input RequestClassificationInput) RequestClassification {
	r := input.Request
	out := RequestClassification{
		Lane:     RequestLaneInteractive,
		Reason:   "default_interactive",
		Endpoint: strings.TrimSpace(input.Endpoint),
		Model:    strings.TrimSpace(input.Model),
		Stream:   input.Stream,
	}
	if r != nil && out.Endpoint == "" {
		out.Endpoint = r.URL.Path
	}
	if isBackgroundEndpoint(out.Endpoint) {
		out.Lane = RequestLaneBackground
		out.Reason = "background_endpoint"
	}
	if input.Anthropic != nil {
		out.SessionID = strings.TrimSpace(input.Anthropic.SessionID)
		out.AgentID = strings.TrimSpace(input.Anthropic.AgentID)
		out.ParentAgentID = strings.TrimSpace(input.Anthropic.ParentAgentID)
		out.MetadataUserID = strings.TrimSpace(input.Anthropic.MetadataUserID)
	}
	if r != nil {
		if out.SessionID == "" {
			out.SessionID = firstNonEmptyHeader(r, "x-claude-code-session-id", "x-claude-session-id", "claude-code-session-id")
		}
		if out.AgentID == "" {
			out.AgentID = firstNonEmptyHeader(r, "x-claude-code-agent-id", "x-claude-agent-id")
		}
		if out.ParentAgentID == "" {
			out.ParentAgentID = firstNonEmptyHeader(r, "x-claude-code-parent-agent-id", "x-claude-parent-agent-id")
		}
	}
	if out.SessionID == "" {
		out.SessionID = sessionIDFromMetadataUserID(out.MetadataUserID)
	}
	out.ClaudeCode = looksLikeClaudeCodeRequest(r, out)
	if out.Lane != RequestLaneBackground && (out.AgentID != "" || out.ParentAgentID != "") {
		out.Lane = RequestLaneSubagent
		out.Reason = "agent_metadata"
	}
	if out.Lane == RequestLaneInteractive && out.ClaudeCode && out.Reason == "default_interactive" {
		out.Reason = "claude_code_foreground"
	}
	return out
}

func isBackgroundEndpoint(endpoint string) bool {
	switch strings.TrimSpace(endpoint) {
	case "/v1/messages/count_tokens", "/messages/count_tokens", "/v1/models", "/models", "/health":
		return true
	default:
		return false
	}
}

func looksLikeClaudeCodeRequest(r *http.Request, c RequestClassification) bool {
	if c.SessionID != "" || c.AgentID != "" || c.ParentAgentID != "" {
		return true
	}
	if r == nil {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	return strings.Contains(ua, "claude-cli") || strings.Contains(ua, "claude-code")
}

func sessionIDFromMetadataUserID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return ""
	}
	for _, key := range []string{"session_id", "sessionId", "claude_code_session_id"} {
		if value, ok := data[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
```

- [ ] **Step 5: Run tests and verify they pass**

Run:

```bash
go test ./proxy -run 'TestClassifyGenerationRequest|TestParseAnthropicEnvelopeExtractsMetadataUserID' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add proxy/request_classifier.go proxy/request_classifier_test.go proxy/anthropic_envelope.go proxy/anthropic_envelope_test.go
git commit -m "feat(proxy): classify claude code request lanes"
```

---

### Task 3: Add Request Log Governor Fields

**Files:**
- Modify: `proxy/request_log.go`
- Modify: `proxy/request_log_test.go`

- [ ] **Step 1: Write failing request log test**

Add this test to `proxy/request_log_test.go`:

```go
func TestRequestLogRecordsGovernorClassificationAndWaits(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx, loggedReq, recorder, _ := h.beginRequestLog(httptest.NewRecorder(), req)

	updateRequestLogClassification(loggedReq, RequestClassification{
		Lane:          RequestLaneSubagent,
		Reason:        "agent_metadata",
		SessionID:     "session-1",
		AgentID:       "agent-1",
		ParentAgentID: "parent-1",
		ClaudeCode:    true,
		Model:         "claude-opus-4.7",
		Stream:        true,
	})
	updateRequestLogGovernor(loggedReq, GovernorLogUpdate{
		QueuePosition:                   3,
		SessionConcurrencyWait:          25 * time.Millisecond,
		AccountConcurrencyWait:          5 * time.Millisecond,
		ModelConcurrencyWait:            10 * time.Millisecond,
		SelectedAccountHealthState:      "healthy",
		SelectedAccountFirstTokenEwmaMs: 750,
		GovernorDecision:                "admitted_subagent",
		GovernorWaitReason:              "subagent_slots_full",
		BackgroundQuietModeSkipped:      true,
	})
	h.finishRequestLog(ctx, recorder)

	entry := h.requestLogs.List(1)[0]
	if entry.PriorityLane != "subagent" {
		t.Fatalf("PriorityLane = %q, want subagent: %#v", entry.PriorityLane, entry)
	}
	if entry.ClaudeCodeSessionID != "session-1" || entry.ClaudeCodeAgentID != "agent-1" || entry.ClaudeCodeParentAgentID != "parent-1" {
		t.Fatalf("Claude Code metadata not recorded: %#v", entry)
	}
	if entry.QueuePosition != 3 || entry.SessionConcurrencyWaitMs != 25 || entry.AccountConcurrencyWaitMs != 5 || entry.ModelConcurrencyWaitMs != 10 {
		t.Fatalf("governor waits not recorded: %#v", entry)
	}
	if entry.SelectedAccountHealthState != "healthy" || entry.SelectedAccountFirstTokenEwmaMs != 750 {
		t.Fatalf("selected account governor fields not recorded: %#v", entry)
	}
	if entry.GovernorDecision != "admitted_subagent" || entry.GovernorWaitReason != "subagent_slots_full" || !entry.BackgroundQuietModeSkipped {
		t.Fatalf("governor decision fields not recorded: %#v", entry)
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
go test ./proxy -run TestRequestLogRecordsGovernorClassificationAndWaits -count=1
```

Expected: FAIL with undefined `updateRequestLogClassification`, `GovernorLogUpdate`, or missing fields.

- [ ] **Step 3: Add fields and update helpers**

In `proxy/request_log.go`, add these fields to `RequestLogEntry` after `RoutingPressure`:

```go
PriorityLane                     string `json:"priorityLane,omitempty"`
QueuePosition                    int    `json:"queuePosition,omitempty"`
SessionConcurrencyWaitMs         int64  `json:"sessionConcurrencyWaitMs,omitempty"`
AccountConcurrencyWaitMs         int64  `json:"accountConcurrencyWaitMs,omitempty"`
ModelConcurrencyWaitMs           int64  `json:"modelConcurrencyWaitMs,omitempty"`
SelectedAccountHealthState       string `json:"selectedAccountHealthState,omitempty"`
SelectedAccountFirstTokenEwmaMs  int64  `json:"selectedAccountFirstTokenEwmaMs,omitempty"`
GovernorDecision                 string `json:"governorDecision,omitempty"`
GovernorWaitReason               string `json:"governorWaitReason,omitempty"`
BackgroundQuietModeSkipped       bool   `json:"backgroundQuietModeSkipped,omitempty"`
```

Add these helpers near `updateRequestLogMetadata`:

```go
type GovernorLogUpdate struct {
	QueuePosition                   int
	SessionConcurrencyWait          time.Duration
	AccountConcurrencyWait          time.Duration
	ModelConcurrencyWait            time.Duration
	SelectedAccountHealthState      string
	SelectedAccountFirstTokenEwmaMs int64
	GovernorDecision                string
	GovernorWaitReason              string
	BackgroundQuietModeSkipped      bool
}

func updateRequestLogClassification(r *http.Request, c RequestClassification) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.entry.PriorityLane = string(c.Lane)
	if strings.TrimSpace(c.SessionID) != "" {
		ctx.entry.ClaudeCodeSessionID = strings.TrimSpace(c.SessionID)
	}
	if strings.TrimSpace(c.AgentID) != "" {
		ctx.entry.ClaudeCodeAgentID = strings.TrimSpace(c.AgentID)
	}
	if strings.TrimSpace(c.ParentAgentID) != "" {
		ctx.entry.ClaudeCodeParentAgentID = strings.TrimSpace(c.ParentAgentID)
	}
	if strings.TrimSpace(c.Model) != "" {
		ctx.entry.Model = strings.TrimSpace(c.Model)
	}
	ctx.entry.Stream = c.Stream
}

func updateRequestLogGovernor(r *http.Request, u GovernorLogUpdate) {
	ctx, _ := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if ctx == nil {
		return
	}
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if u.QueuePosition > 0 {
		ctx.entry.QueuePosition = u.QueuePosition
	}
	if u.SessionConcurrencyWait >= 0 {
		ctx.entry.SessionConcurrencyWaitMs = u.SessionConcurrencyWait.Milliseconds()
	}
	if u.AccountConcurrencyWait >= 0 {
		ctx.entry.AccountConcurrencyWaitMs = u.AccountConcurrencyWait.Milliseconds()
	}
	if u.ModelConcurrencyWait >= 0 {
		ctx.entry.ModelConcurrencyWaitMs = u.ModelConcurrencyWait.Milliseconds()
	}
	if strings.TrimSpace(u.SelectedAccountHealthState) != "" {
		ctx.entry.SelectedAccountHealthState = strings.TrimSpace(u.SelectedAccountHealthState)
	}
	if u.SelectedAccountFirstTokenEwmaMs > 0 {
		ctx.entry.SelectedAccountFirstTokenEwmaMs = u.SelectedAccountFirstTokenEwmaMs
	}
	if strings.TrimSpace(u.GovernorDecision) != "" {
		ctx.entry.GovernorDecision = strings.TrimSpace(u.GovernorDecision)
	}
	if strings.TrimSpace(u.GovernorWaitReason) != "" {
		ctx.entry.GovernorWaitReason = strings.TrimSpace(u.GovernorWaitReason)
	}
	if u.BackgroundQuietModeSkipped {
		ctx.entry.BackgroundQuietModeSkipped = true
	}
}
```

- [ ] **Step 4: Run test and verify it passes**

Run:

```bash
go test ./proxy -run TestRequestLogRecordsGovernorClassificationAndWaits -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/request_log.go proxy/request_log_test.go
git commit -m "feat(proxy): log claude code governor metadata"
```

---

### Task 4: Add Session/Subagent Governor

**Files:**
- Create: `proxy/claude_code_concurrency_governor.go`
- Create: `proxy/claude_code_concurrency_governor_test.go`

- [ ] **Step 1: Write failing pure governor tests**

Create `proxy/claude_code_concurrency_governor_test.go`:

```go
package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"kiro-go/config"
)

func testGovernorConfig() config.ClaudeCodeGovernorConfig {
	return config.ClaudeCodeGovernorConfig{
		Enabled:                         true,
		Models:                          []string{"claude-opus-4.7"},
		InteractiveReservedPerSession:   1,
		SubagentMaxConcurrentPerSession: 2,
		BackgroundMaxConcurrent:         1,
		QueueMaxDepth:                   10,
		InteractiveMaxWaitSeconds:       120,
		SubagentMaxWaitSeconds:          90,
		BackgroundMaxWaitSeconds:        15,
	}
}

func TestClaudeCodeGovernorAllowsMainWhenSubagentsFull(t *testing.T) {
	g := newClaudeCodeConcurrencyGovernor(testGovernorConfig())
	ctx := context.Background()
	sub1, err := g.Acquire(ctx, claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "s1", AgentID: "a1"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub1 acquire: %v", err)
	}
	defer sub1.Release()
	sub2, err := g.Acquire(ctx, claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "s1", AgentID: "a2"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub2 acquire: %v", err)
	}
	defer sub2.Release()

	main, err := g.Acquire(ctx, claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "s1"}, time.Millisecond)
	if err != nil {
		t.Fatalf("main acquire should use reserved foreground lane: %v", err)
	}
	defer main.Release()
	if main.Role != "interactive" {
		t.Fatalf("Role = %q, want interactive", main.Role)
	}
}

func TestClaudeCodeGovernorQueuesExtraSubagentsPerSession(t *testing.T) {
	g := newClaudeCodeConcurrencyGovernor(testGovernorConfig())
	ctx := context.Background()
	sub1, err := g.Acquire(ctx, claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "s1", AgentID: "a1"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub1 acquire: %v", err)
	}
	defer sub1.Release()
	sub2, err := g.Acquire(ctx, claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "s1", AgentID: "a2"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub2 acquire: %v", err)
	}
	defer sub2.Release()

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = g.Acquire(waitCtx, claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "s1", AgentID: "a3"}, 200*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
}

func TestClaudeCodeGovernorDoesNotApplyWithoutSession(t *testing.T) {
	g := newClaudeCodeConcurrencyGovernor(testGovernorConfig())
	decision, err := g.Acquire(context.Background(), claudeCodeAdmissionRequest{Model: "claude-opus-4.7"}, time.Millisecond)
	if err != nil {
		t.Fatalf("acquire without session: %v", err)
	}
	defer decision.Release()
	if decision.Applied {
		t.Fatalf("Applied = true, want false for missing session")
	}
}

func TestClaudeCodeGovernorDoesNotApplyToNonOpusModel(t *testing.T) {
	g := newClaudeCodeConcurrencyGovernor(testGovernorConfig())
	decision, err := g.Acquire(context.Background(), claudeCodeAdmissionRequest{Model: "claude-sonnet-4.5", SessionID: "s1"}, time.Millisecond)
	if err != nil {
		t.Fatalf("acquire non-opus: %v", err)
	}
	defer decision.Release()
	if decision.Applied {
		t.Fatalf("Applied = true, want false for non-Opus")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./proxy -run TestClaudeCodeGovernor -count=1
```

Expected: FAIL with undefined governor types/functions.

- [ ] **Step 3: Implement governor**

Create `proxy/claude_code_concurrency_governor.go`:

```go
package proxy

import (
	"context"
	"strings"
	"sync"
	"time"

	"kiro-go/config"
)

type claudeCodeConcurrencyGovernor struct {
	mu       sync.Mutex
	cfg      config.ClaudeCodeGovernorConfig
	sessions map[string]*claudeCodeSessionGate
}

type claudeCodeSessionGate struct {
	activeInteractive int
	activeSubagents   int
}

type claudeCodeAdmissionRequest struct {
	Model         string
	SessionID     string
	AgentID       string
	ParentAgentID string
	Stream        bool
	ClaudeFormat  bool
}

type claudeCodeAdmissionDecision struct {
	Release func()
	Applied bool
	Role    string
	Wait    time.Duration
}

func newClaudeCodeConcurrencyGovernor(cfg config.ClaudeCodeGovernorConfig) *claudeCodeConcurrencyGovernor {
	cfg = normalizeRuntimeClaudeCodeGovernorConfig(cfg)
	return &claudeCodeConcurrencyGovernor{
		cfg:      cfg,
		sessions: make(map[string]*claudeCodeSessionGate),
	}
}

func normalizeRuntimeClaudeCodeGovernorConfig(cfg config.ClaudeCodeGovernorConfig) config.ClaudeCodeGovernorConfig {
	if cfg.Models == nil {
		cfg.Models = []string{"claude-opus-4.7"}
	}
	if cfg.InteractiveReservedPerSession == 0 {
		cfg.InteractiveReservedPerSession = 1
	}
	if cfg.SubagentMaxConcurrentPerSession == 0 {
		cfg.SubagentMaxConcurrentPerSession = 2
	}
	if cfg.QueueMaxDepth == 0 {
		cfg.QueueMaxDepth = 300
	}
	return cfg
}

func (g *claudeCodeConcurrencyGovernor) Acquire(ctx context.Context, req claudeCodeAdmissionRequest, timeout time.Duration) (claudeCodeAdmissionDecision, error) {
	noop := claudeCodeAdmissionDecision{Release: func() {}}
	if g == nil || !g.cfg.Enabled || !g.supportsModel(req.Model) || strings.TrimSpace(req.SessionID) == "" {
		return noop, nil
	}
	role := claudeCodeRequestRole(req)
	start := time.Now()
	deadline := time.Now().Add(timeout)
	for {
		if release, ok := g.tryAcquire(req.SessionID, role); ok {
			return claudeCodeAdmissionDecision{
				Release: release,
				Applied: true,
				Role:    role,
				Wait:    time.Since(start),
			}, nil
		}
		if timeout <= 0 {
			return noop, context.DeadlineExceeded
		}
		wait := 10 * time.Millisecond
		if remaining := time.Until(deadline); remaining <= 0 {
			return noop, context.DeadlineExceeded
		} else if remaining < wait {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return noop, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (g *claudeCodeConcurrencyGovernor) supportsModel(model string) bool {
	model = normalizeAdmissionModel(model)
	if model == "" {
		return false
	}
	for _, candidate := range g.cfg.Models {
		if normalizeAdmissionModel(candidate) == model {
			return true
		}
	}
	return false
}

func claudeCodeRequestRole(req claudeCodeAdmissionRequest) string {
	if strings.TrimSpace(req.AgentID) != "" || strings.TrimSpace(req.ParentAgentID) != "" {
		return "subagent"
	}
	return "interactive"
}

func (g *claudeCodeConcurrencyGovernor) tryAcquire(sessionID, role string) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gate := g.sessions[sessionID]
	if gate == nil {
		gate = &claudeCodeSessionGate{}
		g.sessions[sessionID] = gate
	}
	switch role {
	case "subagent":
		limit := g.cfg.SubagentMaxConcurrentPerSession
		if limit <= 0 {
			return nil, false
		}
		if gate.activeSubagents >= limit {
			return nil, false
		}
		gate.activeSubagents++
		return func() { g.release(sessionID, role) }, true
	default:
		limit := g.cfg.InteractiveReservedPerSession
		if limit <= 0 {
			limit = 1
		}
		if gate.activeInteractive >= limit {
			return nil, false
		}
		gate.activeInteractive++
		return func() { g.release(sessionID, "interactive") }, true
	}
}

func (g *claudeCodeConcurrencyGovernor) release(sessionID, role string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gate := g.sessions[sessionID]
	if gate == nil {
		return
	}
	if role == "subagent" {
		if gate.activeSubagents > 0 {
			gate.activeSubagents--
		}
	} else if gate.activeInteractive > 0 {
		gate.activeInteractive--
	}
	if gate.activeInteractive == 0 && gate.activeSubagents == 0 {
		delete(g.sessions, sessionID)
	}
}
```

- [ ] **Step 4: Run governor tests and verify they pass**

Run:

```bash
go test ./proxy -run TestClaudeCodeGovernor -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy/claude_code_concurrency_governor.go proxy/claude_code_concurrency_governor_test.go
git commit -m "feat(proxy): add claude code session governor"
```

---

### Task 5: Wire Classification Into Handlers

**Files:**
- Modify: `proxy/handler.go`
- Test: `proxy/request_log_test.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Add a failing handler classification test**

Add this test to `proxy/request_log_test.go`:

```go
func TestClaudeHandlerRecordsSubagentPriorityLane(t *testing.T) {
	h := &Handler{requestLogs: newRequestLogStore(5), promptCache: newPromptCacheTracker(defaultPromptCacheTTL)}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"model":"claude-opus-4.7",
		"max_tokens":0,
		"stream":false,
		"messages":[{"role":"user","content":"warm cache"}]
	}`))
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-1")
	req.Header.Set("X-Claude-Code-Parent-Agent-Id", "parent-1")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	logs := h.requestLogs.List(1)
	if len(logs) != 1 {
		t.Fatalf("expected request log, got %#v", logs)
	}
	if logs[0].PriorityLane != "subagent" {
		t.Fatalf("PriorityLane = %q, want subagent: %#v", logs[0].PriorityLane, logs[0])
	}
	if logs[0].ClaudeCodeSessionID != "session-1" || logs[0].ClaudeCodeAgentID != "agent-1" || logs[0].ClaudeCodeParentAgentID != "parent-1" {
		t.Fatalf("Claude Code metadata missing: %#v", logs[0])
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
go test ./proxy -run TestClaudeHandlerRecordsSubagentPriorityLane -count=1
```

Expected: FAIL because handlers do not call `updateRequestLogClassification`.

- [ ] **Step 3: Wire classifier in request handlers**

In `handleCountTokens`, after `updateRequestLogMetadata(r, req.Model, false)`, add:

```go
updateRequestLogClassification(r, classifyGenerationRequest(RequestClassificationInput{
	Request:  r,
	Endpoint: r.URL.Path,
	Model:    req.Model,
	Stream:   false,
	Claude:   &req,
}))
```

In `handleClaudeMessagesInternal`, after `updateRequestLogAnthropic(r, env)` and after `updateRequestLogMetadata(r, req.Model, req.Stream)`, add:

```go
updateRequestLogClassification(r, classifyGenerationRequest(RequestClassificationInput{
	Request:   r,
	Endpoint:  r.URL.Path,
	Model:     req.Model,
	Stream:    req.Stream,
	Anthropic: env,
	Claude:    &req,
}))
```

In `handleOpenAIChat`, after `updateRequestLogMetadata(r, req.Model, req.Stream)`, add:

```go
updateRequestLogClassification(r, classifyGenerationRequest(RequestClassificationInput{
	Request:  r,
	Endpoint: r.URL.Path,
	Model:    req.Model,
	Stream:   req.Stream,
	OpenAI:   &req,
}))
```

In `handleOpenAIResponses`, after `updateRequestLogMetadata(r, req.Model, req.Stream)`, add:

```go
updateRequestLogClassification(r, classifyGenerationRequest(RequestClassificationInput{
	Request:            r,
	Endpoint:           r.URL.Path,
	Model:              req.Model,
	Stream:             req.Stream,
	OpenAI:             req,
	RawOpenAIResponses: payload,
}))
```

- [ ] **Step 4: Run classification wiring test**

Run:

```bash
go test ./proxy -run TestClaudeHandlerRecordsSubagentPriorityLane -count=1
```

Expected: PASS.

- [ ] **Step 5: Run classifier and request log focused tests**

Run:

```bash
go test ./proxy -run 'TestClassifyGenerationRequest|TestRequestLogRecordsGovernor|TestClaudeHandlerRecordsSubagentPriorityLane' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add proxy/handler.go proxy/request_log_test.go
git commit -m "feat(proxy): record claude code request lanes"
```

---

### Task 6: Wire Session Governor Before Opus Admission

**Files:**
- Modify: `proxy/handler.go`
- Modify: `proxy/claude_code_concurrency_governor.go`
- Test: `proxy/handler_test.go`

- [ ] **Step 1: Write failing handler-level governor tests**

Add this test to `proxy/handler_test.go`:

```go
func TestHandlerClaudeSessionGovernorRunsBeforeModelAdmission(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("init config: %v", err)
	}
	govCfg := config.GetClaudeCodeGovernorConfig()
	govCfg.Enabled = true
	h := &Handler{
		governor:    newClaudeCodeConcurrencyGovernor(govCfg),
		requestLogs: newRequestLogStore(10),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
	}

	sub1, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "session-1", AgentID: "agent-1"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub1 acquire: %v", err)
	}
	defer sub1.Release()
	sub2, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "session-1", AgentID: "agent-2"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub2 acquire: %v", err)
	}
	defer sub2.Release()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-3")
	req = req.WithContext(context.Background())
	ctx, loggedReq, recorder, _ := h.beginRequestLog(httptest.NewRecorder(), req)
	defer h.finishRequestLog(ctx, recorder)

	_, ok := h.acquireClaudeCodeSessionAdmissionForRequest(loggedReq, "claude-opus-4.7", true, true, time.Now().Add(20*time.Millisecond))
	if ok {
		t.Fatalf("expected third subagent to be queued/rejected while subagent slots are full")
	}
	logs := h.requestLogs.List(1)
	if len(logs) == 0 {
		h.finishRequestLog(ctx, recorder)
		logs = h.requestLogs.List(1)
	}
}

func TestHandlerClaudeSessionGovernorAllowsInteractiveWhenSubagentsFull(t *testing.T) {
	govCfg := config.ClaudeCodeGovernorConfig{
		Enabled:                         true,
		Models:                          []string{"claude-opus-4.7"},
		InteractiveReservedPerSession:   1,
		SubagentMaxConcurrentPerSession: 2,
		QueueMaxDepth:                   10,
	}
	h := &Handler{governor: newClaudeCodeConcurrencyGovernor(govCfg)}
	sub1, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "session-1", AgentID: "agent-1"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub1 acquire: %v", err)
	}
	defer sub1.Release()
	sub2, err := h.governor.Acquire(context.Background(), claudeCodeAdmissionRequest{Model: "claude-opus-4.7", SessionID: "session-1", AgentID: "agent-2"}, time.Millisecond)
	if err != nil {
		t.Fatalf("sub2 acquire: %v", err)
	}
	defer sub2.Release()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Claude-Code-Session-Id", "session-1")
	release, ok := h.acquireClaudeCodeSessionAdmissionForRequest(req, "claude-opus-4.7", true, true, time.Now().Add(time.Second))
	if !ok {
		t.Fatalf("interactive request should acquire reserved slot")
	}
	defer release()
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./proxy -run 'TestHandlerClaudeSessionGovernor' -count=1
```

Expected: FAIL with missing `Handler.governor` and `acquireClaudeCodeSessionAdmissionForRequest`.

- [ ] **Step 3: Add Handler field and initialize governor**

In `proxy/handler.go`, add this field to `Handler`:

```go
governor *claudeCodeConcurrencyGovernor
```

In `NewHandler()`, add:

```go
governor: newClaudeCodeConcurrencyGovernor(config.GetClaudeCodeGovernorConfig()),
```

- [ ] **Step 4: Add HTTP admission request helper and handler wrapper**

Add this code to `proxy/claude_code_concurrency_governor.go`:

```go
func claudeCodeAdmissionRequestFromHTTP(r *http.Request, model string, stream bool, claudeFormat bool) claudeCodeAdmissionRequest {
	req := claudeCodeAdmissionRequest{
		Model:        strings.TrimSpace(model),
		Stream:       stream,
		ClaudeFormat: claudeFormat,
	}
	if r == nil {
		return req
	}
	req.SessionID = firstNonEmptyHeader(r, "x-claude-code-session-id", "x-claude-session-id", "claude-code-session-id")
	req.AgentID = firstNonEmptyHeader(r, "x-claude-code-agent-id", "x-claude-agent-id")
	req.ParentAgentID = firstNonEmptyHeader(r, "x-claude-code-parent-agent-id", "x-claude-parent-agent-id")
	return req
}

func (h *Handler) acquireClaudeCodeSessionAdmissionForRequest(r *http.Request, model string, stream bool, claudeFormat bool, deadline time.Time) (func(), bool) {
	if h == nil || h.governor == nil {
		return func() {}, true
	}
	timeout := time.Until(deadline)
	if timeout <= 0 {
		timeout = 0
	}
	ctx := context.Background()
	if r != nil && r.Context() != nil {
		ctx = r.Context()
	}
	decision, err := h.governor.Acquire(ctx, claudeCodeAdmissionRequestFromHTTP(r, model, stream, claudeFormat), timeout)
	if err != nil {
		updateRequestLogGovernor(r, GovernorLogUpdate{
			SessionConcurrencyWait: decision.Wait,
			GovernorDecision:       "session_governor_rejected",
			GovernorWaitReason:     err.Error(),
		})
		return nil, false
	}
	updateRequestLogGovernor(r, GovernorLogUpdate{
		SessionConcurrencyWait: decision.Wait,
		GovernorDecision:       "session_governor_admitted_" + decision.Role,
	})
	return decision.Release, true
}
```

- [ ] **Step 5: Wire wrapper before model admission**

In each retry entry, add the session governor before `acquireOpus47AdmissionForRequest`.

In `handleClaudeWithAccountRetry`, after `updateRequestLogOpusGovernor(...)` and before `releaseGate, ok := h.acquireOpus47AdmissionForRequest(...)`, add:

```go
releaseSession, ok := h.acquireClaudeCodeSessionAdmissionForRequest(r, model, stream, true, deadline)
if !ok {
	return
}
defer releaseSession()
```

In `handleOpenAIWithAccountRetry`, add the same block with `claudeFormat=false`.

In `handleOpenAIResponsesWithAccountRetry`, add the same block with `claudeFormat=false`.

- [ ] **Step 6: Run handler governor tests**

Run:

```bash
go test ./proxy -run 'TestHandlerClaudeSessionGovernor' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run focused regression tests**

Run:

```bash
go test ./proxy -run 'TestClaudeCodeGovernor|TestHandlerClaudeSessionGovernor|TestOpus47Gate|TestStableDownstream|TestStableClaude' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add proxy/handler.go proxy/handler_test.go proxy/claude_code_concurrency_governor.go
git commit -m "feat(proxy): gate claude code subagent concurrency"
```

---

### Task 7: Final Verification

**Files:**
- All files changed by previous tasks.

- [ ] **Step 1: Run focused config and proxy tests**

Run:

```bash
go test ./config ./proxy -run 'ClaudeCodeGovernor|ClassifyGenerationRequest|GovernorClassification|SessionGovernor|StableDownstream|Opus47' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full touched package tests**

Run:

```bash
go test ./config ./proxy -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full Go test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Build Docker service**

Run:

```bash
docker compose up -d --build kiro-go
```

Expected: the `kiro-go-kiro-go-1` container rebuilds and starts.

- [ ] **Step 5: Check container health**

Run:

```bash
curl -fsS http://127.0.0.1:8080/health
docker ps --format '{{.Names}}\t{{.Status}}' | grep 'kiro-go-kiro-go-1'
```

Expected: `/health` returns success and the container status is `healthy` or running without restart loops.

- [ ] **Step 6: Check recent logs for regressions**

Run:

```bash
docker logs --since 5m kiro-go-kiro-go-1 2>&1 | grep -Ei 'panic|fatal|superfluous WriteHeader|kiro_go_stable_fallback|data race' || true
```

Expected: no matching regression lines.

- [ ] **Step 7: Confirm no uncommitted adjustments remain**

Run:

```bash
git status --short
```

Expected: no output. If there is output, inspect it and commit only intentional files with a specific message for those files.

---

## Self-Review Checklist

- Spec coverage: this plan covers classification, request logs, and session/subagent governor. It intentionally defers priority model queue, account health states, EWMA scheduler, quiet mode, Admin UI, and full Docker UAT harness.
- Handler placement: governor acquisition is before `acquireOpus47AdmissionForRequest`, so queued subagents do not hold global Opus 4.7 admission.
- Protocol safety: this plan does not change SSE writers or fallback bodies.
- Compatibility: default config disables the governor, and non-session/non-Opus requests bypass the session gate.
- Tests: every new helper has focused tests, and final verification includes full package and full repo tests.
