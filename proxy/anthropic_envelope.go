package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type anthropicEnvelope struct {
	Request            ClaudeRequest
	Extra              map[string]json.RawMessage
	AnthropicVersion   string
	BetaHeader         string
	Betas              map[string]bool
	ClientRequestID    string
	AnthropicRequestID string
	UserAgent          string
	SessionID          string
	AgentID            string
}

func parseAnthropicEnvelope(r *http.Request, body []byte) (*anthropicEnvelope, error) {
	var req ClaudeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	for _, key := range []string{
		"model", "messages", "max_tokens", "temperature", "top_p", "stream", "system",
		"thinking", "tools", "tool_reference", "tool_choice",
	} {
		delete(raw, key)
	}
	req.Extra = make(map[string]interface{}, len(raw))
	for key, value := range raw {
		var decoded interface{}
		if err := json.Unmarshal(value, &decoded); err == nil {
			req.Extra[key] = decoded
		}
	}
	requestID := strings.TrimSpace(r.Header.Get("request-id"))
	if requestID == "" {
		requestID = strings.TrimSpace(r.Header.Get("x-request-id"))
	}
	if requestID == "" {
		requestID = "req_" + uuid.New().String()
	}
	betaHeader := strings.TrimSpace(r.Header.Get("anthropic-beta"))
	return &anthropicEnvelope{
		Request:            req,
		Extra:              raw,
		AnthropicVersion:   strings.TrimSpace(r.Header.Get("anthropic-version")),
		BetaHeader:         betaHeader,
		Betas:              parseAnthropicBetas(betaHeader),
		ClientRequestID:    strings.TrimSpace(r.Header.Get("x-request-id")),
		AnthropicRequestID: requestID,
		UserAgent:          strings.TrimSpace(r.UserAgent()),
		SessionID:          firstNonEmptyHeader(r, "x-claude-code-session-id", "x-claude-session-id", "claude-code-session-id"),
		AgentID:            firstNonEmptyHeader(r, "x-claude-code-agent-id", "x-claude-agent-id"),
	}, nil
}

func parseAnthropicBetas(header string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[strings.ToLower(part)] = true
		}
	}
	return out
}

func (e *anthropicEnvelope) HasBeta(name string) bool {
	if e == nil {
		return false
	}
	return e.Betas[strings.ToLower(strings.TrimSpace(name))]
}

func (e *anthropicEnvelope) HasBetaPrefix(prefix string) bool {
	if e == nil {
		return false
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for beta := range e.Betas {
		if strings.HasPrefix(beta, prefix) {
			return true
		}
	}
	return false
}

func writeAnthropicRequestIDHeaders(w http.ResponseWriter, env *anthropicEnvelope) {
	requestID := ""
	if env != nil {
		requestID = strings.TrimSpace(env.AnthropicRequestID)
	}
	if requestID == "" {
		requestID = "req_" + uuid.New().String()
	}
	w.Header().Set("request-id", requestID)
	w.Header().Set("x-request-id", requestID)
}

func firstNonEmptyHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
