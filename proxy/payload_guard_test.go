package proxy

import (
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

	_, err := guardKiroPayload(payload, payloadGuardOptions{SoftLimitBytes: 512, HardLimitBytes: 2048})
	if err == nil {
		t.Fatalf("expected invalid payload error")
	}
	if !strings.Contains(err.Error(), "current tool_result") {
		t.Fatalf("unexpected error: %v", err)
	}
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
