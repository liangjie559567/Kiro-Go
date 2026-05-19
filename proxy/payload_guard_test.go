package proxy

import (
	"encoding/json"
	"fmt"
	"kiro-go/config"
	"strings"
	"testing"
)

func TestGuardKiroPayloadTrimsPairwiseWithoutOrphans(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "current",
		ModelID: "claude-sonnet-4.5",
		Origin:  "AI_EDITOR",
	}
	payload.ConversationState.History = []KiroHistoryMessage{
		{AssistantResponseMessage: &KiroAssistantResponseMessage{
			ToolUses: []KiroToolUse{{ToolUseID: "toolu_old", Name: "readFile", Input: map[string]interface{}{"path": "old"}}},
		}},
		{UserInputMessage: &KiroUserInputMessage{
			Content: strings.Repeat("x", 4096),
			UserInputMessageContext: &UserInputMessageContext{
				ToolResults: []KiroToolResult{{ToolUseID: "toolu_old", Content: []KiroResultContent{{Text: strings.Repeat("x", 4096)}}, Status: "success"}},
			},
		}},
	}

	result, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 512, HardLimitBytes: 2048})
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected trimming")
	}
	if result.TrimmedCount != 2 {
		t.Fatalf("expected tool pair to be trimmed, got count %d", result.TrimmedCount)
	}
	if result.CompactedPairs != 1 {
		t.Fatalf("expected one compacted tool pair, got %d", result.CompactedPairs)
	}
	if result.RecoveryNote == "" {
		t.Fatalf("expected recovery note")
	}
	if hasOrphanedKiroToolMessages(payload.ConversationState.History) {
		t.Fatalf("expected no orphan tool messages: %#v", payload.ConversationState.History)
	}
	if result.FinalBytes > 2048 {
		t.Fatalf("payload remains over hard limit: %d", result.FinalBytes)
	}
}

func TestGuardKiroPayloadReportsToolPairCompaction(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "current",
		ModelID: "claude-sonnet-4.5",
		Origin:  "AI_EDITOR",
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("toolu_%d", i)
		payload.ConversationState.History = append(payload.ConversationState.History,
			KiroHistoryMessage{AssistantResponseMessage: &KiroAssistantResponseMessage{
				Content:  "use tool",
				ToolUses: []KiroToolUse{{ToolUseID: id, Name: "Read", Input: map[string]interface{}{"path": fmt.Sprintf("file-%d", i)}}},
			}},
			KiroHistoryMessage{UserInputMessage: &KiroUserInputMessage{
				Content: "tool result",
				UserInputMessageContext: &UserInputMessageContext{
					ToolResults: []KiroToolResult{{ToolUseID: id, Content: []KiroResultContent{{Text: strings.Repeat("x", 1024)}}, Status: "success"}},
				},
			}},
		)
	}

	result, err := guardKiroPayload(payload, payloadGuardOptions{
		SoftLimitBytes:     100 * 1024,
		HardLimitBytes:     200 * 1024,
		MaxHistoryMessages: 4,
		MaxHistoryToolUses: 2,
	})
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if result.CompactedPairs < 2 {
		t.Fatalf("expected at least two compacted tool pairs, got %#v", result)
	}
	if hasOrphanedKiroToolMessages(payload.ConversationState.History) {
		t.Fatalf("expected no orphan tool messages: %#v", payload.ConversationState.History)
	}
}

func TestGuardKiroPayloadRejectsOversizedCurrentToolResult(t *testing.T) {
	payload := ClaudeToKiro(&ClaudeRequest{
		Model: "claude-sonnet-4.5",
		Messages: []ClaudeMessage{
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_now", "content": strings.Repeat("x", 8192)},
			}},
		},
		MaxTokens: 64,
	}, false)

	_, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 128, HardLimitBytes: 512})
	if err == nil {
		t.Fatalf("expected invalid payload error")
	}
}

func TestGuardKiroPayloadRejectsOversizedCurrentUserContent(t *testing.T) {
	payload := ClaudeToKiro(&ClaudeRequest{
		Model: "claude-sonnet-4.5",
		Messages: []ClaudeMessage{
			{Role: "user", Content: strings.Repeat("x", maxKiroCurrentContentBytes+1)},
		},
		MaxTokens: 64,
	}, false)

	_, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err == nil {
		t.Fatalf("expected invalid payload error")
	}
	if !strings.Contains(err.Error(), "payload remains too large") {
		t.Fatalf("expected payload-too-large error, got %v", err)
	}
}

func TestGuardKiroPayloadTruncatesLargeCurrentToolResult(t *testing.T) {
	payload := ClaudeToKiro(&ClaudeRequest{
		Model: "claude-opus-4.7",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "read a large file"},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "toolu_now", "name": "readFile", "input": map[string]interface{}{"path": "large.txt"}},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_now", "content": strings.Repeat("x", 4096)},
			}},
		},
		MaxTokens: 64,
	}, false)

	result, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 1024, HardLimitBytes: 5500})
	if err != nil {
		t.Fatalf("expected guard to truncate current tool_result, got error: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected current tool_result trimming")
	}
	if result.FinalBytes > 5500 {
		t.Fatalf("expected payload under hard limit, got %d", result.FinalBytes)
	}
	got := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults[0].Content[0].Text
	if len(got) >= 4096 {
		t.Fatalf("expected tool_result text to be truncated, got len=%d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation note in tool_result text, got %q", got)
	}
}

func TestGuardKiroPayloadKeepsRelocatedDocsAheadOfToolResultContinuation(t *testing.T) {
	longDescription := strings.Repeat("Detailed usage guidance for the browser tool. ", 30)
	payload := ClaudeToKiro(&ClaudeRequest{
		Model: "claude-opus-4.7",
		Tools: []ClaudeTool{{
			Name:        "mcp__browser__screenshot",
			Description: longDescription,
			InputSchema: map[string]interface{}{"type": "object"},
		}},
		Messages: []ClaudeMessage{
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "toolu_now", "name": "mcp__browser__screenshot", "input": map[string]interface{}{}},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_now", "content": strings.Repeat("x", 4096)},
			}},
		},
		MaxTokens: 64,
	}, false)

	result, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 1024, HardLimitBytes: 7500})
	if err != nil {
		t.Fatalf("expected guard to truncate current tool_result, got error: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected current tool_result trimming")
	}
	content := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	docIndex := strings.Index(content, "Operator tool documentation for this session")
	continuationIndex := strings.Index(content, toolResultsContinuationPrefix)
	if docIndex < 0 {
		t.Fatalf("expected relocated docs after guard, got %q", content)
	}
	if continuationIndex < 0 {
		t.Fatalf("expected tool-result continuation after guard, got %q", content)
	}
	if docIndex > continuationIndex {
		t.Fatalf("expected relocated docs before tool-result continuation after guard, got %q", content)
	}
}

func TestGuardKiroPayloadDoesNotTruncateUserTextMentioningToolResults(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "User note: Tool results: should not be treated as generated continuation.\n\n" +
			"Operator tool documentation for this session:\n\nTool: browser\n" +
			strings.Repeat("docs ", 40) + "\n\n" +
			buildToolResultsContinuation([]KiroToolResult{{
				ToolUseID: "toolu_now",
				Content:   []KiroResultContent{{Text: strings.Repeat("x", 4096)}},
				Status:    "success",
			}}),
		ModelID: "claude-opus-4.7",
		Origin:  "AI_EDITOR",
		UserInputMessageContext: &UserInputMessageContext{
			ToolResults: []KiroToolResult{{ToolUseID: "toolu_now", Content: []KiroResultContent{{Text: strings.Repeat("x", 4096)}}, Status: "success"}},
		},
	}

	if !truncateCurrentToolResultContinuationForPayload(payload, 1024) {
		t.Fatalf("expected generated tool-result continuation to be truncated")
	}
	content := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !strings.Contains(content, "User note: Tool results: should not be treated as generated continuation.") {
		t.Fatalf("expected user-authored marker text to survive, got %q", content)
	}
	if !strings.Contains(content, "Operator tool documentation for this session") {
		t.Fatalf("expected relocated docs to survive, got %q", content)
	}
}

func TestDefaultGuardPreservesLargeHistoryToolResultsBelowSoftLimit(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "current",
		ModelID: "claude-opus-4.7",
		Origin:  "AI_EDITOR",
	}
	for i := 0; i < 42; i++ {
		toolUseID := fmt.Sprintf("toolu_%02d", i)
		payload.ConversationState.History = append(payload.ConversationState.History,
			KiroHistoryMessage{AssistantResponseMessage: &KiroAssistantResponseMessage{
				Content:  "use tool",
				ToolUses: []KiroToolUse{{ToolUseID: toolUseID, Name: "readFile", Input: map[string]interface{}{"path": fmt.Sprintf("file-%02d.txt", i)}}},
			}},
			KiroHistoryMessage{UserInputMessage: &KiroUserInputMessage{
				Content: "tool result",
				UserInputMessageContext: &UserInputMessageContext{
					ToolResults: []KiroToolResult{{ToolUseID: toolUseID, Content: []KiroResultContent{{Text: strings.Repeat("x", 6200)}}, Status: "success"}},
				},
			}},
		)
	}
	originalSummary := summarizeKiroPayload(payload)
	if originalSummary.TotalBytes >= defaultPayloadGuardOptions().SoftLimitBytes {
		t.Fatalf("test setup should stay below soft limit, got %d >= %d", originalSummary.TotalBytes, defaultPayloadGuardOptions().SoftLimitBytes)
	}
	if originalSummary.HistoryToolResultBytes < 250*1024 {
		t.Fatalf("test setup should resemble large historical tool output, got %d", originalSummary.HistoryToolResultBytes)
	}

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if result.Trimmed {
		t.Fatalf("expected default guard to preserve historical tool_result content below soft limit, got %#v", result)
	}
	finalSummary := summarizeKiroPayload(payload)
	if finalSummary.HistoryToolResultBytes != originalSummary.HistoryToolResultBytes {
		t.Fatalf("expected historical tool result bytes preserved, before=%d after=%d", originalSummary.HistoryToolResultBytes, finalSummary.HistoryToolResultBytes)
	}
}

func TestConservativeGuardTruncatesLargeHistoryToolResults(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "current",
		ModelID: "claude-opus-4.7",
		Origin:  "AI_EDITOR",
	}
	for i := 0; i < 42; i++ {
		toolUseID := fmt.Sprintf("toolu_%02d", i)
		payload.ConversationState.History = append(payload.ConversationState.History,
			KiroHistoryMessage{AssistantResponseMessage: &KiroAssistantResponseMessage{
				Content:  "use tool",
				ToolUses: []KiroToolUse{{ToolUseID: toolUseID, Name: "readFile", Input: map[string]interface{}{"path": fmt.Sprintf("file-%02d.txt", i)}}},
			}},
			KiroHistoryMessage{UserInputMessage: &KiroUserInputMessage{
				Content: "tool result",
				UserInputMessageContext: &UserInputMessageContext{
					ToolResults: []KiroToolResult{{ToolUseID: toolUseID, Content: []KiroResultContent{{Text: strings.Repeat("x", 6200)}}, Status: "success"}},
				},
			}},
		)
	}
	originalSummary := summarizeKiroPayload(payload)

	result, err := guardKiroPayload(payload, conservativePayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected conservative historical tool_result trimming")
	}
	finalSummary := summarizeKiroPayload(payload)
	if finalSummary.HistoryToolResultBytes >= originalSummary.HistoryToolResultBytes {
		t.Fatalf("expected historical tool result bytes to shrink, before=%d after=%d", originalSummary.HistoryToolResultBytes, finalSummary.HistoryToolResultBytes)
	}
}

func TestDefaultGuardPreservesClaudeCodeSizedToolHistory(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-claude-code"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "continue using the file contents already read",
		ModelID: "claude-opus-4.7",
		Origin:  "AI_EDITOR",
	}
	for i := 0; i < 60; i++ {
		toolUseID := fmt.Sprintf("toolu_read_%02d", i)
		payload.ConversationState.History = append(payload.ConversationState.History,
			KiroHistoryMessage{AssistantResponseMessage: &KiroAssistantResponseMessage{
				Content:  "read core file",
				ToolUses: []KiroToolUse{{ToolUseID: toolUseID, Name: "Read", Input: map[string]interface{}{"file_path": fmt.Sprintf("/repo/core-%02d.go", i)}}},
			}},
			KiroHistoryMessage{UserInputMessage: &KiroUserInputMessage{
				Content: "file content",
				UserInputMessageContext: &UserInputMessageContext{
					ToolResults: []KiroToolResult{{ToolUseID: toolUseID, Content: []KiroResultContent{{Text: strings.Repeat("important file content ", 320)}}, Status: "success"}},
				},
			}},
		)
	}
	originalSummary := summarizeKiroPayload(payload)
	if originalSummary.TotalBytes < 350*1024 || originalSummary.TotalBytes > 700*1024 {
		t.Fatalf("test setup should resemble observed Claude Code payload size, got %#v", originalSummary)
	}

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if result.Trimmed {
		t.Fatalf("expected default guard to preserve Claude Code sized file history, got %#v", result)
	}
	finalSummary := summarizeKiroPayload(payload)
	if finalSummary.HistoryToolUses != originalSummary.HistoryToolUses || finalSummary.HistoryMessages != originalSummary.HistoryMessages {
		t.Fatalf("expected history shape preserved, before=%#v after=%#v", originalSummary, finalSummary)
	}
	if finalSummary.HistoryToolResultBytes != originalSummary.HistoryToolResultBytes {
		t.Fatalf("expected file tool_result bytes preserved, before=%d after=%d", originalSummary.HistoryToolResultBytes, finalSummary.HistoryToolResultBytes)
	}
}

func TestGuardKiroPayloadTrimsLargeToolHistoryWindowBelowSoftLimit(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(54, 26)
	originalSummary := summarizeKiroPayload(payload)
	if originalSummary.TotalBytes >= defaultPayloadGuardOptions().SoftLimitBytes {
		t.Fatalf("test setup should stay below soft limit, got %d >= %d", originalSummary.TotalBytes, defaultPayloadGuardOptions().SoftLimitBytes)
	}
	if originalSummary.HistoryMessages < 96 || originalSummary.HistoryToolUses < 54 || originalSummary.Tools != 26 {
		t.Fatalf("test setup should resemble production malformed shape, got %#v", originalSummary)
	}

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected structural trimming for large tool history window")
	}
	finalSummary := summarizeKiroPayload(payload)
	if finalSummary.HistoryMessages > maxKiroHistoryMessages || finalSummary.HistoryToolUses > maxKiroHistoryToolUses {
		t.Fatalf("expected history structure under limits, got %#v", finalSummary)
	}
	if hasOrphanedKiroToolMessages(payload.ConversationState.History) {
		t.Fatalf("expected no orphan tool messages after structural trimming")
	}
}

func TestGuardKiroPayloadTrimsObservedKiroMalformedToolWindow(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(24, 16)
	originalSummary := summarizeKiroPayload(payload)
	if originalSummary.HistoryToolUses != 24 || originalSummary.CurrentTools != 16 {
		t.Fatalf("test setup should match observed malformed tool window, got %#v", originalSummary)
	}

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected observed malformed tool window to be trimmed")
	}
	finalSummary := summarizeKiroPayload(payload)
	if finalSummary.HistoryToolUses > maxKiroHistoryToolUses {
		t.Fatalf("expected history tool uses under %d, got %#v", maxKiroHistoryToolUses, finalSummary)
	}
	if finalSummary.CurrentTools != 16 {
		t.Fatalf("expected current tool registry to remain available, got %#v", finalSummary)
	}
	if hasOrphanedKiroToolMessages(payload.ConversationState.History) {
		t.Fatalf("expected no orphan tool messages after observed malformed trimming")
	}
}

func TestGuardKiroPayloadSanitizesLargeToolSchemas(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(8, 26)
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if ctx == nil || len(ctx.Tools) != 26 {
		t.Fatalf("test setup expected 26 tools, got %#v", ctx)
	}
	originalSchemaBytes := currentToolsJSONSize(payload)
	if originalSchemaBytes <= maxKiroToolSchemaBytes {
		t.Fatalf("test setup expected oversized tool schema bytes, got %d", originalSchemaBytes)
	}

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected tool schema sanitization")
	}
	if got := len(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools); got > maxKiroTools {
		t.Fatalf("expected tools capped to %d, got %d", maxKiroTools, got)
	}
	if got := currentToolsJSONSize(payload); got > maxKiroToolSchemaBytes {
		t.Fatalf("expected tool schemas under %d bytes, got %d", maxKiroToolSchemaBytes, got)
	}
}

func TestGuardKiroPayloadRemovesUnsupportedAdditionalProperties(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "read",
		ModelID: "claude-sonnet-4.5",
		Origin:  "AI_EDITOR",
		UserInputMessageContext: &UserInputMessageContext{
			Tools: []KiroToolWrapper{{}},
		},
	}
	spec := &payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools[0].ToolSpecification
	spec.Name = "readFile"
	spec.Description = "Read a file"
	spec.InputSchema = InputSchema{JSON: map[string]interface{}{
		"type":                 "object",
		"required":             []interface{}{},
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":                 "string",
				"additionalProperties": false,
			},
		},
	}}

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected schema sanitization to be reported")
	}
	data, err := json.Marshal(spec.InputSchema.JSON)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if strings.Contains(string(data), "additionalProperties") {
		t.Fatalf("expected additionalProperties removed, got %s", string(data))
	}
	if strings.Contains(string(data), `"required":[]`) {
		t.Fatalf("expected empty required removed, got %s", string(data))
	}
}

func TestGuardKiroPayloadPrioritizesToolsMentionedInCurrentPrompt(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(2, 24)
	payload.ConversationState.CurrentMessage.UserInputMessage.Content = "Please call mcp__fs__tool_20 for this operation."

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected tool trimming")
	}
	tools := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	if len(tools) > maxKiroTools {
		t.Fatalf("expected tools capped to %d, got %d", maxKiroTools, len(tools))
	}
	found := false
	for _, tool := range tools {
		if tool.ToolSpecification.Name == "mcp__fs__tool_20" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected prompt-mentioned tool to survive trimming, got %#v", toolNamesForTest(tools))
	}
}

func TestGuardKiroPayloadPreservesClaudeCodeCoreTools(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(2, 24)
	tools := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	coreNames := []string{"agent", "task", "todoWrite", "bash", "read"}
	for i, name := range coreNames {
		tools[len(tools)-1-i].ToolSpecification.Name = name
	}
	payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = tools

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected tool trimming")
	}
	got := toolNamesForTest(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)
	for _, name := range coreNames {
		if !containsString(got, name) {
			t.Fatalf("expected core Claude Code tool %q to survive trimming, got %#v", name, got)
		}
	}
}

func TestGuardKiroPayloadPreservesClaudeCodeCoreToolsWithoutPromptText(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(2, 24)
	payload.ConversationState.CurrentMessage.UserInputMessage.Content = ""
	tools := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	coreNames := []string{"agent", "task", "taskCreate", "taskUpdate", "taskOutput", "taskGet", "taskList", "taskStop", "todoWrite", "bash", "read", "write", "edit", "multiEdit"}
	for i, name := range coreNames {
		tools[len(tools)-1-i].ToolSpecification.Name = name
	}
	payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = tools

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected tool trimming")
	}
	got := toolNamesForTest(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)
	for _, name := range coreNames {
		if !containsString(got, name) {
			t.Fatalf("expected core Claude Code tool %q to survive trimming without prompt text, got %#v", name, got)
		}
	}
}

func TestGuardKiroPayloadPrioritizesTaskLifecycleToolsFromClaudeCodeLogShape(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(2, 26)
	payload.ConversationState.CurrentMessage.UserInputMessage.Content = ""
	tools := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	realLogOrder := []string{
		"agent", "write", "bash", "edit", "read", "webFetch", "webSearch", "notebookEdit",
		"taskGet", "taskList", "taskStop", "cronDelete", "askUserQuestion", "cronCreate",
		"enterPlanMode", "enterWorktree", "exitPlanMode", "exitWorktree", "monitor",
		"pushNotification", "scheduleWakeup", "skill", "taskCreate", "taskOutput",
		"taskUpdate", "cronList",
	}
	for i, name := range realLogOrder {
		tools[i].ToolSpecification.Name = name
	}
	payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = tools

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected tool trimming")
	}
	got := toolNamesForTest(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)
	for _, name := range []string{"taskCreate", "taskUpdate", "taskOutput", "taskGet", "taskList", "taskStop"} {
		if !containsString(got, name) {
			t.Fatalf("expected task lifecycle tool %q to survive trimming, got %#v", name, got)
		}
	}
}

func TestGuardKiroPayloadReportsKeptAndTrimmedToolNames(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(2, 24)
	tools := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	tools[23].ToolSpecification.Name = "agent"
	tools[22].ToolSpecification.Name = "task"
	payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = tools

	result, err := guardKiroPayload(payload, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !containsString(result.KeptToolNames, "agent") || !containsString(result.KeptToolNames, "task") {
		t.Fatalf("expected kept tool names to include core tools, got %#v", result.KeptToolNames)
	}
	if len(result.TrimmedToolNames) == 0 {
		t.Fatalf("expected trimmed tool names")
	}
	if containsString(result.TrimmedToolNames, "agent") || containsString(result.TrimmedToolNames, "task") {
		t.Fatalf("core tools should not be reported as trimmed, got %#v", result.TrimmedToolNames)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestGuardKiroPayloadPreservesCurrentToolResultPair(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "Tool results:",
		ModelID: "claude-sonnet-4.5",
		Origin:  "AI_EDITOR",
		UserInputMessageContext: &UserInputMessageContext{
			ToolResults: []KiroToolResult{{ToolUseID: "toolu_now", Content: []KiroResultContent{{Text: "current result"}}, Status: "success"}},
		},
	}
	payload.ConversationState.History = []KiroHistoryMessage{
		{AssistantResponseMessage: &KiroAssistantResponseMessage{
			Content:  "use tool",
			ToolUses: []KiroToolUse{{ToolUseID: "toolu_now", Name: "readFile", Input: map[string]interface{}{"path": "now"}}},
		}},
	}

	result, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 512, HardLimitBytes: 2048})
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if result.Trimmed {
		t.Fatalf("did not expect current tool_result pair to be trimmed: %#v", result)
	}
	if len(payload.ConversationState.History) != 1 || len(payload.ConversationState.History[0].AssistantResponseMessage.ToolUses) != 1 {
		t.Fatalf("expected current tool_result matching tool_use to be preserved, got %#v", payload.ConversationState.History)
	}
}

func TestGuardKiroPayloadPreservesContextBeforeCurrentToolUsePair(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "Tool results:",
		ModelID: "claude-opus-4.7",
		Origin:  "AI_EDITOR",
		UserInputMessageContext: &UserInputMessageContext{
			ToolResults: []KiroToolResult{{ToolUseID: "toolu_now", Content: []KiroResultContent{{Text: strings.Repeat("result ", 64)}}, Status: "success"}},
		},
	}
	payload.ConversationState.History = []KiroHistoryMessage{
		{UserInputMessage: &KiroUserInputMessage{Content: "Operator instructions for this session:\n\n说中文", ModelID: "claude-opus-4.7", Origin: "AI_EDITOR"}},
		{AssistantResponseMessage: &KiroAssistantResponseMessage{Content: kiroSystemAcknowledgement}},
		{UserInputMessage: &KiroUserInputMessage{Content: "old user", ModelID: "claude-opus-4.7", Origin: "AI_EDITOR"}},
		{AssistantResponseMessage: &KiroAssistantResponseMessage{
			Content:  "use tool",
			ToolUses: []KiroToolUse{{ToolUseID: "toolu_now", Name: "Read", Input: map[string]interface{}{"file_path": "README.md"}}},
		}},
		{UserInputMessage: &KiroUserInputMessage{Content: "intervening gsd-sdk output", ModelID: "claude-opus-4.7", Origin: "AI_EDITOR"}},
	}

	result, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 100 * 1024, HardLimitBytes: 200 * 1024})
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if !result.Trimmed {
		t.Fatalf("expected intervening history before current tool_result to be trimmed")
	}
	if got := len(payload.ConversationState.History); got != 4 {
		t.Fatalf("expected context before matching tool_use to be preserved, got %d: %#v", got, payload.ConversationState.History)
	}
	if got := payload.ConversationState.History[0].UserInputMessage.Content; !strings.Contains(got, "说中文") {
		t.Fatalf("expected system/user context to survive adjacency enforcement, got %q", got)
	}
	if got := payload.ConversationState.History[2].UserInputMessage.Content; got != "old user" {
		t.Fatalf("expected prior user task context to survive, got %q", got)
	}
	tail := payload.ConversationState.History[len(payload.ConversationState.History)-1]
	if tail.AssistantResponseMessage == nil || len(tail.AssistantResponseMessage.ToolUses) != 1 || tail.AssistantResponseMessage.ToolUses[0].ToolUseID != "toolu_now" {
		t.Fatalf("expected matching current tool_use at history tail, got %#v", tail)
	}
}

func TestGuardKiroPayloadPreservesUnmatchedCurrentToolResult(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "Tool results:",
		ModelID: "claude-opus-4.7",
		Origin:  "AI_EDITOR",
		UserInputMessageContext: &UserInputMessageContext{
			ToolResults: []KiroToolResult{{ToolUseID: "toolu_missing", Content: []KiroResultContent{{Text: "orphan current result"}}, Status: "success"}},
		},
	}
	payload.ConversationState.History = []KiroHistoryMessage{
		{UserInputMessage: &KiroUserInputMessage{Content: "old user", ModelID: "claude-opus-4.7", Origin: "AI_EDITOR"}},
	}

	result, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 100 * 1024, HardLimitBytes: 200 * 1024})
	if err != nil {
		t.Fatalf("guard payload: %v", err)
	}
	if result.Trimmed {
		t.Fatalf("did not expect unmatched current tool_result to be trimmed: %#v", result)
	}
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if ctx == nil || len(ctx.ToolResults) != 1 {
		t.Fatalf("expected unmatched current tool_result to be preserved, got %#v", ctx)
	}
}

func toolNamesForTest(tools []KiroToolWrapper) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.ToolSpecification.Name)
	}
	return names
}

func TestKiroPayloadSummaryReportsSizeDriversWithoutContent(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "secret current content",
		UserInputMessageContext: &UserInputMessageContext{
			Tools:       []KiroToolWrapper{{}},
			ToolResults: []KiroToolResult{{ToolUseID: "toolu_1", Content: []KiroResultContent{{Text: "secret tool output"}}, Status: "success"}},
		},
	}
	payload.ConversationState.History = []KiroHistoryMessage{
		{UserInputMessage: &KiroUserInputMessage{Content: "secret history"}},
		{AssistantResponseMessage: &KiroAssistantResponseMessage{Content: "secret assistant", ToolUses: []KiroToolUse{{ToolUseID: "toolu_1", Name: "read", Input: map[string]interface{}{"path": "secret"}}}}},
	}

	summary := summarizeKiroPayload(payload)

	if summary.CurrentContentBytes <= 0 || summary.CurrentToolResultBytes <= 0 || summary.HistoryMessages != 2 || summary.Tools != 1 || summary.HistoryToolUses != 1 {
		t.Fatalf("unexpected payload summary: %#v", summary)
	}
	encoded := fmt.Sprintf("%#v", summary)
	for _, secret := range []string{"secret current content", "secret tool output", "secret history", "secret assistant"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("payload summary leaked content %q: %s", secret, encoded)
		}
	}
}

func TestKiroPayloadSummaryReportsCurrentToolSchemaBudget(t *testing.T) {
	payload := malformedRiskToolHistoryPayload(2, 3)

	summary := summarizeKiroPayload(payload)

	if summary.CurrentTools != 3 {
		t.Fatalf("expected current tool count, got summary %#v", summary)
	}
	if summary.CurrentToolSchemaBytes != currentToolsJSONSize(payload) {
		t.Fatalf("expected current tool schema bytes to match serialized tools, got summary %#v size=%d", summary, currentToolsJSONSize(payload))
	}
	if summary.CurrentToolSchemaBytes <= 0 {
		t.Fatalf("expected non-zero current tool schema bytes")
	}
}

func TestApplyTruncationRecoveryNote(t *testing.T) {
	payload := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		Messages:  []ClaudeMessage{{Role: "user", Content: "continue"}},
		MaxTokens: 64,
	}, false)

	applyTruncationRecoveryNote(payload, "previous history was trimmed")

	content := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !strings.Contains(content, "previous history was trimmed") {
		t.Fatalf("expected recovery note in current content")
	}
	if !strings.Contains(content, "continue") {
		t.Fatalf("expected original current content to remain, got %q", content)
	}
}

func TestApplyTruncationRecoveryNoteRejectsOverHardLimit(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: strings.Repeat("x", 900),
		ModelID: "claude-sonnet-4.5",
		Origin:  "AI_EDITOR",
	}

	result := payloadGuardResult{FinalBytes: kiroPayloadJSONSize(payload), RecoveryNote: strings.Repeat("n", 200)}
	_, err := applyTruncationRecoveryNoteWithLimit(payload, result, payloadGuardOptions{SoftLimitBytes: 128, HardLimitBytes: result.FinalBytes + 32})
	if err == nil {
		t.Fatalf("expected hard-limit error after recovery note")
	}
	if !strings.Contains(err.Error(), "recovery note") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFinalizeKiroPayloadForAccountRejectsProfileArnHardLimitGrowth(t *testing.T) {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-1"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: strings.Repeat("x", 900),
		ModelID: "claude-sonnet-4.5",
		Origin:  "AI_EDITOR",
	}
	account := &config.Account{ProfileArn: "arn:aws:codewhisperer:profile/" + strings.Repeat("p", 256)}
	opts := payloadGuardOptions{SoftLimitBytes: 128, HardLimitBytes: kiroPayloadJSONSize(payload) + 32}

	result, err := finalizeKiroPayloadForAccount(payload, account, opts)
	if err == nil {
		t.Fatalf("expected hard-limit error after profile ARN")
	}
	if !strings.Contains(err.Error(), "ProfileArn") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalBytes <= opts.HardLimitBytes {
		t.Fatalf("expected final bytes over hard limit, got %d <= %d", result.FinalBytes, opts.HardLimitBytes)
	}
}

func TestCloneKiroPayloadIsolatesProfileArnAndPreservesToolNameMap(t *testing.T) {
	base := ClaudeToKiro(&ClaudeRequest{
		Model:     "claude-sonnet-4.5",
		Messages:  []ClaudeMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 64,
		Tools: []ClaudeTool{{
			Name:        "mcp__fs__read_file",
			Description: "read",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
			},
		}},
	}, false)
	if len(base.ToolNameMap) == 0 {
		t.Fatalf("expected base tool name map")
	}

	attempt := cloneKiroPayload(base)
	_, err := finalizeKiroPayloadForAccount(attempt, &config.Account{ProfileArn: "arn:aws:codewhisperer:profile/account-a"}, defaultPayloadGuardOptions())
	if err != nil {
		t.Fatalf("finalize attempt payload: %v", err)
	}

	if base.ProfileArn != "" || base.ProfileArnFinalized {
		t.Fatalf("expected base payload to remain unfinalized, got arn=%q finalized=%v", base.ProfileArn, base.ProfileArnFinalized)
	}
	if attempt.ProfileArn != "arn:aws:codewhisperer:profile/account-a" || !attempt.ProfileArnFinalized {
		t.Fatalf("expected attempt payload to be finalized for account A, got arn=%q finalized=%v", attempt.ProfileArn, attempt.ProfileArnFinalized)
	}
	for key, value := range base.ToolNameMap {
		if got := attempt.ToolNameMap[key]; got != value {
			t.Fatalf("expected tool name map entry %q=%q to survive clone, got %#v", key, value, attempt.ToolNameMap)
		}
	}
}

func TestHasOrphanedKiroToolMessagesDetectsToolUseAndToolResultOrphans(t *testing.T) {
	history := []KiroHistoryMessage{
		{AssistantResponseMessage: &KiroAssistantResponseMessage{
			ToolUses: []KiroToolUse{{ToolUseID: "toolu_missing_result", Name: "read", Input: map[string]interface{}{"path": "a"}}},
		}},
		{UserInputMessage: &KiroUserInputMessage{
			UserInputMessageContext: &UserInputMessageContext{
				ToolResults: []KiroToolResult{{ToolUseID: "toolu_missing_use", Content: []KiroResultContent{{Text: "orphan"}}}},
			},
		}},
	}

	if !hasOrphanedKiroToolMessages(history) {
		t.Fatalf("expected orphaned tool messages")
	}

	cleaned := dropOrphanedKiroToolMessages(history)
	if hasOrphanedKiroToolMessages(cleaned) {
		t.Fatalf("expected cleaned history to have no orphans: %#v", cleaned)
	}
}

func malformedRiskToolHistoryPayload(toolPairs int, toolCount int) *KiroPayload {
	payload := &KiroPayload{}
	payload.ConversationState.ChatTriggerType = "MANUAL"
	payload.ConversationState.ConversationID = "conv-structural-risk"
	payload.ConversationState.CurrentMessage.UserInputMessage = KiroUserInputMessage{
		Content: "continue",
		ModelID: "claude-opus-4.7",
		Origin:  "AI_EDITOR",
		UserInputMessageContext: &UserInputMessageContext{
			Tools: make([]KiroToolWrapper, 0, toolCount),
		},
	}
	for i := 0; i < toolCount; i++ {
		var tool KiroToolWrapper
		tool.ToolSpecification.Name = fmt.Sprintf("mcp__fs__tool_%02d", i)
		tool.ToolSpecification.Description = strings.Repeat("Detailed MCP tool description with nested schema guidance. ", 120)
		tool.ToolSpecification.InputSchema = InputSchema{JSON: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": strings.Repeat("path description ", 80)},
				"options": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"mode": map[string]interface{}{"anyOf": []interface{}{
							map[string]interface{}{"type": "string"},
							map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						}},
					},
				},
			},
		}}
		payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = append(
			payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools,
			tool,
		)
	}
	for i := 0; i < toolPairs; i++ {
		toolUseID := fmt.Sprintf("toolu_history_%02d", i)
		payload.ConversationState.History = append(payload.ConversationState.History,
			KiroHistoryMessage{UserInputMessage: &KiroUserInputMessage{
				Content: strings.Repeat("history user context ", 20),
				ModelID: "claude-opus-4.7",
				Origin:  "AI_EDITOR",
			}},
			KiroHistoryMessage{AssistantResponseMessage: &KiroAssistantResponseMessage{
				Content:  "use tool",
				ToolUses: []KiroToolUse{{ToolUseID: toolUseID, Name: "readFile", Input: map[string]interface{}{"path": fmt.Sprintf("file-%02d.txt", i)}}},
			}},
			KiroHistoryMessage{UserInputMessage: &KiroUserInputMessage{
				Content: strings.Repeat("tool result summary ", 8),
				ModelID: "claude-opus-4.7",
				Origin:  "AI_EDITOR",
				UserInputMessageContext: &UserInputMessageContext{
					ToolResults: []KiroToolResult{{ToolUseID: toolUseID, Content: []KiroResultContent{{Text: strings.Repeat("x", 1100)}}, Status: "success"}},
				},
			}},
		)
	}
	return payload
}
