package proxy

import "testing"

func TestEstimateClaudeRequestInputTokensIncludesToolReferencesAndToolCacheControl(t *testing.T) {
	req := &ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		MaxTokens: 4096,
		System: []interface{}{
			map[string]interface{}{
				"type":          "text",
				"text":          "system rules",
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
						"type":       "base64",
						"media_type": "image/png",
						"data":       "iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
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
		Thinking:  &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048},
		Messages:  []ClaudeMessage{{Role: "user", Content: "solve"}},
	}

	withoutThinking := *req
	withoutThinking.Thinking = nil

	base := estimateClaudeRequestInputTokens(&withoutThinking)
	withThinking := estimateClaudeRequestInputTokens(req)

	if withThinking <= base {
		t.Fatalf("expected thinking config to increase estimate: base=%d thinking=%d", base, withThinking)
	}
}
