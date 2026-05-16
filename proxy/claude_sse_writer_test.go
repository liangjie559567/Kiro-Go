package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeSSEWriterOrdersTextEvents(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 4096)
	writer.TextDelta("hello")
	writer.Stop("end_turn", buildClaudeUsageMap(10, 1, promptCacheUsage{}, false))

	body := w.Body.String()
	mustContainInOrder(t, body,
		"event: message_start",
		"event: content_block_start",
		`"type":"text"`,
		"event: content_block_delta",
		`"text":"hello"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		"event: message_stop",
	)
}

func TestClaudeSSEWriterChunksToolInput(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 8)
	writer.ToolUse(KiroToolUse{ToolUseID: "toolu_1", Name: "readFile", Input: map[string]interface{}{"path": strings.Repeat("a", 24)}})
	writer.Stop("tool_use", buildClaudeUsageMap(10, 2, promptCacheUsage{}, false))

	if got := strings.Count(w.Body.String(), `"input_json_delta"`); got < 2 {
		t.Fatalf("expected chunked input_json_delta events, got %d body=%s", got, w.Body.String())
	}
	mustContainInOrder(t, w.Body.String(),
		`"type":"tool_use"`,
		`"id":"toolu_1"`,
		`"name":"readFile"`,
		`"stop_reason":"tool_use"`,
	)
}

func TestClaudeSSEWriterPingAndError(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 4096)
	writer.Ping()
	writer.Error("overloaded_error", "upstream reset")
	body := w.Body.String()
	mustContainInOrder(t, body, "event: ping", `"type":"ping"`, "event: error", `"type":"overloaded_error"`)
}

func TestClaudeSSEWriterEventsAreJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(1, 0, promptCacheUsage{}, false), 4096)
	writer.TextDelta("ok")
	writer.Stop("end_turn", buildClaudeUsageMap(1, 1, promptCacheUsage{}, false))
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var v interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
			t.Fatalf("invalid json line %q: %v", line, err)
		}
	}
}

func mustContainInOrder(t *testing.T, body string, parts ...string) {
	t.Helper()
	pos := 0
	for _, part := range parts {
		idx := strings.Index(body[pos:], part)
		if idx < 0 {
			t.Fatalf("missing %q after offset %d in body:\n%s", part, pos, body)
		}
		pos += idx + len(part)
	}
}
