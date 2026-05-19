package proxy

import (
	"encoding/json"
	"fmt"
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

func TestClaudeSSEWriterMixedThinkingTextToolOrder(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 16)
	writer.ThinkingDelta("reason")
	writer.TextDelta("answer")
	writer.ToolUse(KiroToolUse{
		ToolUseID: "toolu_1",
		Name:      "readFile",
		Input:     map[string]interface{}{"path": "/tmp/example.txt"},
	})
	writer.Stop("tool_use", buildClaudeUsageMap(10, 2, promptCacheUsage{}, false))

	body := w.Body.String()
	frames := parseSSEFrames(t, body)

	assertFrameEvent(t, frames, 0, "message_start")

	assertFrameEvent(t, frames, 1, "content_block_start")
	assertFrameIndex(t, frames[1], 0)
	assertNestedString(t, frames[1], "content_block", "type", "thinking")
	if got := requireNestedString(t, frames[1], "content_block", "signature"); !strings.HasPrefix(got, "sig_") {
		t.Fatalf("thinking signature = %q, want sig_ prefix", got)
	}

	assertFrameEvent(t, frames, 2, "content_block_delta")
	assertFrameIndex(t, frames[2], 0)
	assertNestedString(t, frames[2], "delta", "type", "thinking_delta")
	assertNestedString(t, frames[2], "delta", "thinking", "reason")

	assertFrameEvent(t, frames, 3, "content_block_delta")
	assertFrameIndex(t, frames[3], 0)
	assertNestedString(t, frames[3], "delta", "type", "signature_delta")
	if got := requireNestedString(t, frames[3], "delta", "signature"); !strings.HasPrefix(got, "sig_") {
		t.Fatalf("signature_delta = %q, want sig_ prefix", got)
	}

	assertFrameEvent(t, frames, 4, "content_block_stop")
	assertFrameIndex(t, frames[4], 0)

	assertFrameEvent(t, frames, 5, "content_block_start")
	assertFrameIndex(t, frames[5], 1)
	assertNestedString(t, frames[5], "content_block", "type", "text")

	assertFrameEvent(t, frames, 6, "content_block_delta")
	assertFrameIndex(t, frames[6], 1)
	assertNestedString(t, frames[6], "delta", "type", "text_delta")
	assertNestedString(t, frames[6], "delta", "text", "answer")

	assertFrameEvent(t, frames, 7, "content_block_stop")
	assertFrameIndex(t, frames[7], 1)

	assertFrameEvent(t, frames, 8, "content_block_start")
	assertFrameIndex(t, frames[8], 2)
	assertNestedString(t, frames[8], "content_block", "type", "tool_use")
	assertNestedString(t, frames[8], "content_block", "id", "toolu_1")
	assertNestedString(t, frames[8], "content_block", "name", "readFile")

	var toolInputJSON strings.Builder
	next := 9
	for ; next < len(frames); next++ {
		if frames[next].event == "content_block_stop" {
			break
		}
		if frames[next].event != "content_block_delta" {
			t.Fatalf("frame %d event = %q, want tool input delta or content_block_stop", next, frames[next].event)
		}
		assertFrameIndex(t, frames[next], 2)
		assertNestedString(t, frames[next], "delta", "type", "input_json_delta")
		toolInputJSON.WriteString(requireNestedString(t, frames[next], "delta", "partial_json"))
	}
	if toolInputJSON.Len() == 0 {
		t.Fatalf("expected at least one tool input_json_delta before tool stop, frames=%v", frameEvents(frames))
	}
	var toolInput map[string]interface{}
	if err := json.Unmarshal([]byte(toolInputJSON.String()), &toolInput); err != nil {
		t.Fatalf("invalid reconstructed tool input JSON %q: %v", toolInputJSON.String(), err)
	}
	if len(toolInput) != 1 || toolInput["path"] != "/tmp/example.txt" {
		t.Fatalf("unexpected reconstructed tool input: %#v", toolInput)
	}

	assertFrameEvent(t, frames, next, "content_block_stop")
	assertFrameIndex(t, frames[next], 2)
	next++

	assertFrameEvent(t, frames, next, "message_delta")
	assertNestedString(t, frames[next], "delta", "stop_reason", "tool_use")
	assertNestedNull(t, frames[next], "delta", "stop_sequence")
	next++

	assertFrameEvent(t, frames, next, "message_stop")
	next++
	if next != len(frames) {
		t.Fatalf("unexpected frames after message_stop: events=%v", frameEvents(frames[next:]))
	}
}

func TestClaudeSSEWriterMessageDeltaIncludesStopSequence(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 4096)
	writer.TextDelta("hello")
	writer.Stop("end_turn", buildClaudeUsageMap(10, 1, promptCacheUsage{}, false))

	frames := parseSSEFrames(t, w.Body.String())
	messageDelta := frames[len(frames)-2]
	assertFrameEvent(t, frames, len(frames)-2, "message_delta")
	assertNestedString(t, messageDelta, "delta", "stop_reason", "end_turn")
	assertNestedNull(t, messageDelta, "delta", "stop_sequence")
}

func TestClaudeSSEWriterPostStartErrorDoesNotEmitMessageStop(t *testing.T) {
	w := httptest.NewRecorder()
	writer := newClaudeSSEWriter(w, "msg_test", "claude-sonnet-4.5", buildClaudeUsageMap(10, 0, promptCacheUsage{}, false), 4096)
	writer.TextDelta("partial")
	writer.Error("overloaded_error", "upstream reset")

	body := w.Body.String()
	mustContainInOrder(t, body,
		"event: message_start",
		`"type":"text_delta"`,
		"event: content_block_stop",
		"event: error",
		`"type":"overloaded_error"`,
	)
	mustContain(t, body,
		`"text":"partial"`,
		`"message":"upstream reset"`,
	)
	if strings.Contains(body, "event: message_stop") {
		t.Fatalf("expected post-start error not to emit message_stop, body:\n%s", body)
	}
	for _, frame := range parseSSEFrames(t, body) {
		if frame.event == "message_delta" {
			t.Fatalf("expected post-start error not to emit message_delta, body:\n%s", body)
		}
	}
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

func mustContain(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(body, part) {
			t.Fatalf("missing %q in body:\n%s", part, body)
		}
	}
}

type sseFrame struct {
	event string
	data  map[string]interface{}
}

func parseSSEFrames(t *testing.T, body string) []sseFrame {
	t.Helper()
	frames := []sseFrame{}
	for _, rawFrame := range strings.Split(strings.TrimSpace(body), "\n\n") {
		if strings.TrimSpace(rawFrame) == "" {
			continue
		}
		var frame sseFrame
		for _, line := range strings.Split(rawFrame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame.data); err != nil {
					t.Fatalf("invalid SSE JSON data line %q: %v", line, err)
				}
			}
		}
		if frame.event == "" {
			t.Fatalf("SSE frame missing event: %q", rawFrame)
		}
		if frame.data == nil {
			t.Fatalf("SSE frame missing data: %q", rawFrame)
		}
		frames = append(frames, frame)
	}
	return frames
}

func frameEvents(frames []sseFrame) []string {
	events := make([]string, 0, len(frames))
	for _, frame := range frames {
		events = append(events, frame.event)
	}
	return events
}

func assertFrameEvent(t *testing.T, frames []sseFrame, idx int, event string) {
	t.Helper()
	if idx >= len(frames) {
		t.Fatalf("missing frame %d, want event %q, got events=%v", idx, event, frameEvents(frames))
	}
	if frames[idx].event != event {
		t.Fatalf("frame %d event = %q, want %q", idx, frames[idx].event, event)
	}
}

func assertFrameIndex(t *testing.T, frame sseFrame, want int) {
	t.Helper()
	got, ok := frame.data["index"].(float64)
	if !ok {
		t.Fatalf("frame %q index missing or non-numeric: %#v", frame.event, frame.data["index"])
	}
	if int(got) != want {
		t.Fatalf("frame %q index = %v, want %d", frame.event, got, want)
	}
}

func assertNestedString(t *testing.T, frame sseFrame, objectKey, fieldKey, want string) {
	t.Helper()
	if got := requireNestedString(t, frame, objectKey, fieldKey); got != want {
		t.Fatalf("frame %q %s.%s = %q, want %q", frame.event, objectKey, fieldKey, got, want)
	}
}

func requireNestedString(t *testing.T, frame sseFrame, objectKey, fieldKey string) string {
	t.Helper()
	obj, ok := frame.data[objectKey].(map[string]interface{})
	if !ok {
		t.Fatalf("frame %q %s missing or non-object: %#v", frame.event, objectKey, frame.data[objectKey])
	}
	got, ok := obj[fieldKey].(string)
	if !ok {
		t.Fatalf("frame %q %s.%s missing or non-string: %#v", frame.event, objectKey, fieldKey, obj[fieldKey])
	}
	return got
}

func assertNestedNull(t *testing.T, frame sseFrame, objectKey, fieldKey string) {
	t.Helper()
	obj, ok := frame.data[objectKey].(map[string]interface{})
	if !ok {
		t.Fatalf("frame %q %s missing or non-object: %#v", frame.event, objectKey, frame.data[objectKey])
	}
	if value, ok := obj[fieldKey]; !ok || value != nil {
		t.Fatalf("frame %q %s.%s = %#v, want null", frame.event, objectKey, fieldKey, value)
	}
}

func (f sseFrame) String() string {
	return fmt.Sprintf("%s %#v", f.event, f.data)
}
