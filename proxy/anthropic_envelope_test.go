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
