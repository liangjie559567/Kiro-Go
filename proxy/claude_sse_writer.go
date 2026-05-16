package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type claudeSSEWriter struct {
	w              http.ResponseWriter
	flusher        http.Flusher
	messageID      string
	model          string
	startUsage     map[string]interface{}
	toolChunkBytes int
	started        bool
	stopped        bool
	nextIndex      int
	activeIndex    int
	activeType     string
}

func newClaudeSSEWriter(w http.ResponseWriter, messageID, model string, startUsage map[string]interface{}, toolChunkBytes int) *claudeSSEWriter {
	if toolChunkBytes <= 0 {
		toolChunkBytes = 4096
	}
	flusher, _ := w.(http.Flusher)
	return &claudeSSEWriter{
		w:              w,
		flusher:        flusher,
		messageID:      messageID,
		model:          model,
		startUsage:     startUsage,
		toolChunkBytes: toolChunkBytes,
		activeIndex:    -1,
	}
}

func (s *claudeSSEWriter) Start() {
	if s.started {
		return
	}
	s.started = true
	s.write("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         s.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         s.startUsage,
		},
	})
}

func (s *claudeSSEWriter) TextDelta(text string) {
	if text == "" || s.stopped {
		return
	}
	s.startBlock("text", map[string]string{"type": "text", "text": ""})
	s.write("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.activeIndex,
		"delta": map[string]string{"type": "text_delta", "text": text},
	})
}

func (s *claudeSSEWriter) ThinkingDelta(text string) {
	if text == "" || s.stopped {
		return
	}
	s.startBlock("thinking", map[string]string{"type": "thinking", "thinking": ""})
	s.write("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": s.activeIndex,
		"delta": map[string]string{"type": "thinking_delta", "thinking": text},
	})
}

func (s *claudeSSEWriter) ToolUse(tu KiroToolUse) {
	if s.stopped {
		return
	}
	s.closeBlock()
	s.Start()
	idx := s.nextIndex
	s.nextIndex++
	s.write("content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": idx,
		"content_block": claudeSSEToolUseBlock{
			Type:  "tool_use",
			ID:    tu.ToolUseID,
			Name:  tu.Name,
			Input: map[string]interface{}{},
		},
	})
	inputJSON, _ := json.Marshal(tu.Input)
	for _, chunk := range chunkStringForSSE(string(inputJSON), s.toolChunkBytes) {
		s.write("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]interface{}{
				"type":         "input_json_delta",
				"partial_json": chunk,
			},
		})
	}
	s.write("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": idx})
}

type claudeSSEToolUseBlock struct {
	Type  string                 `json:"type"`
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

func (s *claudeSSEWriter) Ping() {
	if s.stopped {
		return
	}
	s.write("ping", map[string]string{"type": "ping"})
}

func (s *claudeSSEWriter) Error(errType, message string) {
	if s.stopped {
		return
	}
	s.closeBlock()
	s.write("error", map[string]interface{}{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
	s.stopped = true
}

func (s *claudeSSEWriter) Stop(stopReason string, usage map[string]interface{}) {
	if s.stopped {
		return
	}
	s.closeBlock()
	s.Start()
	s.write("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason},
		"usage": usage,
	})
	s.write("message_stop", map[string]string{"type": "message_stop"})
	s.stopped = true
}

func (s *claudeSSEWriter) startBlock(blockType string, block interface{}) {
	if s.activeType == blockType {
		return
	}
	s.closeBlock()
	s.Start()
	idx := s.nextIndex
	s.nextIndex++
	s.write("content_block_start", map[string]interface{}{
		"type":          "content_block_start",
		"index":         idx,
		"content_block": block,
	})
	s.activeIndex = idx
	s.activeType = blockType
}

func (s *claudeSSEWriter) closeBlock() {
	if s.activeIndex < 0 {
		return
	}
	s.write("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": s.activeIndex})
	s.activeIndex = -1
	s.activeType = ""
}

func (s *claudeSSEWriter) write(event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func chunkStringForSSE(value string, maxBytes int) []string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return []string{value}
	}
	chunks := make([]string, 0, len(value)/maxBytes+1)
	start := 0
	currentBytes := 0
	for idx, r := range value {
		runeBytes := len(string(r))
		if currentBytes > 0 && currentBytes+runeBytes > maxBytes {
			chunks = append(chunks, value[start:idx])
			start = idx
			currentBytes = 0
		}
		currentBytes += runeBytes
	}
	if start < len(value) {
		chunks = append(chunks, value[start:])
	}
	return chunks
}
