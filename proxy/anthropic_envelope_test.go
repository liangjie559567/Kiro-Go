package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestClaudeRequestPreservesToolCacheControlAndEagerStreaming(t *testing.T) {
	var req ClaudeRequest
	if err := json.Unmarshal([]byte(`{
		"model":"claude-sonnet-4.5",
		"max_tokens":64,
		"tools":[{
			"name":"write_file",
			"description":"Write a file",
			"input_schema":{"type":"object"},
			"cache_control":{"type":"ephemeral","ttl":"1h"},
			"eager_input_streaming":true
		}],
		"messages":[{"role":"user","content":"hi"}]
	}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected one tool, got %#v", req.Tools)
	}
	if req.Tools[0].CacheControl == nil || req.Tools[0].CacheControl["ttl"] != "1h" {
		t.Fatalf("expected tool cache_control to be preserved, got %#v", req.Tools[0].CacheControl)
	}
	if !req.Tools[0].EagerInputStreaming {
		t.Fatalf("expected eager_input_streaming to be preserved")
	}
}

func TestClaudeCode2143WireFixtureParsesAndPreservesCompatibilityFields(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude_code_2_1_143_wire_request.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Headers map[string]string      `json:"headers"`
		Body    map[string]interface{} `json:"body"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	body, err := json.Marshal(fixture.Body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	for key, value := range fixture.Headers {
		httpReq.Header.Set(key, value)
	}

	env, err := parseAnthropicEnvelope(httpReq, body)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if !env.HasBeta("fine-grained-tool-streaming-2025-05-14") || !env.HasBetaPrefix("tool-search") {
		t.Fatalf("expected Claude Code beta flags, got %#v", env.Betas)
	}
	if env.ClientRequestID != "client_req_test_123" || env.AnthropicRequestID != "client_req_test_123" {
		t.Fatalf("expected request IDs from fixture headers, got client=%q anthropic=%q", env.ClientRequestID, env.AnthropicRequestID)
	}
	if env.AnthropicVersion != "2023-06-01" {
		t.Fatalf("expected Anthropic version from fixture header, got %q", env.AnthropicVersion)
	}
	if env.SessionID != "session_test_123" || env.AgentID != "agent_test_123" {
		t.Fatalf("expected Claude Code session metadata, got session=%q agent=%q", env.SessionID, env.AgentID)
	}
	if len(env.Request.Tools) != 2 || !env.Request.Tools[0].EagerInputStreaming {
		t.Fatalf("expected tools and eager_input_streaming, got %#v", env.Request.Tools)
	}
	if env.Request.Tools[0].CacheControl == nil || env.Request.Tools[0].CacheControl["ttl"] != "1h" {
		t.Fatalf("expected tool cache_control, got %#v", env.Request.Tools[0].CacheControl)
	}
	if env.Request.Tools[1].Type != "web_search_20260209" {
		t.Fatalf("expected latest web search tool type, got %#v", env.Request.Tools[1])
	}
	if len(env.Request.ToolReferences) != 1 || env.Request.ToolReferences[0].Name != "mcp__filesystem__read_file" {
		t.Fatalf("expected tool reference, got %#v", env.Request.ToolReferences)
	}
}
