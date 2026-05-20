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
	if got.Reason != "claude_code_foreground" {
		t.Fatalf("Reason = %q, want claude_code_foreground", got.Reason)
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
	if got.Reason != "agent_metadata" {
		t.Fatalf("Reason = %q, want agent_metadata", got.Reason)
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

func TestClassifyGenerationRequestBackgroundEndpoints(t *testing.T) {
	for _, endpoint := range []string{"/messages/count_tokens", "/v1/models", "/models", "/health"} {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, endpoint, nil)

			got := classifyGenerationRequest(RequestClassificationInput{
				Request:  req,
				Endpoint: endpoint,
			})

			if got.Lane != RequestLaneBackground {
				t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneBackground)
			}
			if got.Reason != "background_endpoint" {
				t.Fatalf("Reason = %q, want background_endpoint", got.Reason)
			}
		})
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

func TestClassifyGenerationRequestSessionFromMetadataUserIDKeys(t *testing.T) {
	for _, tc := range []struct {
		name           string
		metadataUserID string
		wantSessionID  string
	}{
		{
			name:           "session_id",
			metadataUserID: `{"session_id":"session-from-snake"}`,
			wantSessionID:  "session-from-snake",
		},
		{
			name:           "sessionId",
			metadataUserID: `{"sessionId":"session-from-camel"}`,
			wantSessionID:  "session-from-camel",
		},
		{
			name:           "claude_code_session_id",
			metadataUserID: `{"claude_code_session_id":"session-from-claude-code"}`,
			wantSessionID:  "session-from-claude-code",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

			got := classifyGenerationRequest(RequestClassificationInput{
				Request:  req,
				Endpoint: "/v1/messages",
				Anthropic: &anthropicEnvelope{
					MetadataUserID: tc.metadataUserID,
				},
			})

			if got.SessionID != tc.wantSessionID {
				t.Fatalf("SessionID = %q, want %q", got.SessionID, tc.wantSessionID)
			}
			if !got.ClaudeCode {
				t.Fatalf("expected ClaudeCode true")
			}
		})
	}
}

func TestClassifyGenerationRequestInvalidMetadataUserIDJSONTolerated(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
		Anthropic: &anthropicEnvelope{
			MetadataUserID: `{"session_id":`,
		},
	})

	if got.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty", got.SessionID)
	}
	if got.ClaudeCode {
		t.Fatalf("ClaudeCode = true, want false")
	}
	if got.Lane != RequestLaneInteractive {
		t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneInteractive)
	}
}

func TestClassifyGenerationRequestClaudeCodeUserAgentOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("User-Agent", "sub2api/1.0 claude-code/2.1")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
	})

	if got.Lane != RequestLaneInteractive {
		t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneInteractive)
	}
	if !got.ClaudeCode {
		t.Fatalf("expected ClaudeCode true")
	}
	if got.SessionID != "" || got.AgentID != "" || got.ParentAgentID != "" {
		t.Fatalf("expected no ids, got session=%q agent=%q parent=%q", got.SessionID, got.AgentID, got.ParentAgentID)
	}
	if got.Reason != "claude_code_foreground" {
		t.Fatalf("Reason = %q, want claude_code_foreground", got.Reason)
	}
}

func TestClassifyGenerationRequestAnthropicMetadataPrecedesHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Claude-Code-Session-Id", "session-from-header")
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-from-header")
	req.Header.Set("X-Claude-Code-Parent-Agent-Id", "parent-from-header")

	got := classifyGenerationRequest(RequestClassificationInput{
		Request:  req,
		Endpoint: "/v1/messages",
		Anthropic: &anthropicEnvelope{
			SessionID:     "session-from-env",
			AgentID:       "agent-from-env",
			ParentAgentID: "parent-from-env",
		},
	})

	if got.SessionID != "session-from-env" || got.AgentID != "agent-from-env" || got.ParentAgentID != "parent-from-env" {
		t.Fatalf("expected env ids before headers, got session=%q agent=%q parent=%q", got.SessionID, got.AgentID, got.ParentAgentID)
	}
	if got.Lane != RequestLaneSubagent {
		t.Fatalf("Lane = %q, want %q", got.Lane, RequestLaneSubagent)
	}
}
