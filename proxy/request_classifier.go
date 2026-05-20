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
	out := RequestClassification{
		Lane:     RequestLaneInteractive,
		Reason:   "default_interactive",
		Endpoint: strings.TrimSpace(input.Endpoint),
		Model:    strings.TrimSpace(input.Model),
		Stream:   input.Stream,
	}
	if out.Endpoint == "" && input.Request != nil {
		out.Endpoint = input.Request.URL.Path
	}
	if out.Model == "" {
		out.Model = requestModel(input)
	}
	if !out.Stream {
		out.Stream = requestStream(input)
	}

	if input.Anthropic != nil {
		out.SessionID = strings.TrimSpace(input.Anthropic.SessionID)
		out.AgentID = strings.TrimSpace(input.Anthropic.AgentID)
		out.ParentAgentID = strings.TrimSpace(input.Anthropic.ParentAgentID)
		out.MetadataUserID = strings.TrimSpace(input.Anthropic.MetadataUserID)
	}
	if input.Request != nil {
		if out.SessionID == "" {
			out.SessionID = firstNonEmptyHeader(input.Request, "x-claude-code-session-id", "x-claude-session-id", "claude-code-session-id")
		}
		if out.AgentID == "" {
			out.AgentID = firstNonEmptyHeader(input.Request, "x-claude-code-agent-id", "x-claude-agent-id")
		}
		if out.ParentAgentID == "" {
			out.ParentAgentID = firstNonEmptyHeader(input.Request, "x-claude-code-parent-agent-id", "x-claude-parent-agent-id")
		}
	}
	if out.SessionID == "" {
		out.SessionID = sessionIDFromMetadataUserID(out.MetadataUserID)
	}

	out.ClaudeCode = out.SessionID != "" || out.AgentID != "" || out.ParentAgentID != "" || requestUserAgentLooksClaudeCode(input.Request)
	if isBackgroundEndpoint(out.Endpoint) {
		out.Lane = RequestLaneBackground
		out.Reason = "background_endpoint"
		return out
	}
	if out.AgentID != "" || out.ParentAgentID != "" {
		out.Lane = RequestLaneSubagent
		out.Reason = "agent_metadata"
		return out
	}
	if out.ClaudeCode {
		out.Reason = "claude_code_foreground"
	}
	return out
}

func requestModel(input RequestClassificationInput) string {
	if input.Anthropic != nil {
		return strings.TrimSpace(input.Anthropic.Request.Model)
	}
	if input.Claude != nil {
		return strings.TrimSpace(input.Claude.Model)
	}
	if input.OpenAI != nil {
		return strings.TrimSpace(input.OpenAI.Model)
	}
	if model, ok := input.RawOpenAIResponses["model"].(string); ok {
		return strings.TrimSpace(model)
	}
	return ""
}

func requestStream(input RequestClassificationInput) bool {
	if input.Anthropic != nil {
		return input.Anthropic.Request.Stream
	}
	if input.Claude != nil {
		return input.Claude.Stream
	}
	if input.OpenAI != nil {
		return input.OpenAI.Stream
	}
	if stream, ok := input.RawOpenAIResponses["stream"].(bool); ok {
		return stream
	}
	return false
}

func isBackgroundEndpoint(endpoint string) bool {
	switch strings.TrimSpace(endpoint) {
	case "/v1/messages/count_tokens", "/messages/count_tokens", "/v1/models", "/models", "/health":
		return true
	default:
		return false
	}
}

func requestUserAgentLooksClaudeCode(r *http.Request) bool {
	if r == nil {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	return strings.Contains(ua, "claude-cli") || strings.Contains(ua, "claude-code")
}

func sessionIDFromMetadataUserID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var meta struct {
		SessionID           string `json:"session_id"`
		SessionIDCamel      string `json:"sessionId"`
		ClaudeCodeSessionID string `json:"claude_code_session_id"`
	}
	if err := json.Unmarshal([]byte(value), &meta); err != nil {
		return ""
	}
	return firstNonEmptyString(meta.SessionID, meta.SessionIDCamel, meta.ClaudeCodeSessionID)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
