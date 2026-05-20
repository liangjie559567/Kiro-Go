package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyGenerationRequestInteractiveClaudeCodeSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-cli/2.1")
	req.Header.Set("X-Claude-Code-Session-Id", "session-main")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
		Model:    "claude-sonnet-4.5",
		Stream:   true,
	})

	if got.Lane != RequestLaneInteractive {
		t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneInteractive)
	}
	if got.SessionID != "session-main" {
		t.Fatalf("SessionID = %q, want session-main", got.SessionID)
	}
	if !got.ClaudeCode {
		t.Fatalf("expected ClaudeCode true")
	}
}

func TestClassifyGenerationRequestSubagentFromParentHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Claude-Code-Session-Id", "session-main")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-1")
	req.Header.Set("X-Claude-Code-Parent-Agent-Id", "parent-1")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
	})

	if got.Lane != RequestLaneSubagent {
		t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneSubagent)
	}
	if got.AgentID != "agent-1" || got.ParentAgentID != "parent-1" {
		t.Fatalf("expected agent/parent preserved, got agent=%q parent=%q", got.AgentID, got.ParentAgentID)
	}
}

func TestClassifyGenerationRequestBackgroundForCountTokens(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages/count_tokens",
	})

	if got.Lane != RequestLaneBackground {
		t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneBackground)
	}
}

func TestClassifyGenerationRequestConservativeOpenAIDefaultInteractive(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("User-Agent", "sub2api/1.0")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/chat/completions",
		OpenAI:   &OpenAIRequest{Model: "gpt-4o", Stream: true},
	})

	if got.Lane != RequestLaneInteractive {
		t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneInteractive)
	}
}
