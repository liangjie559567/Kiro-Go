package proxy

import (
	"encoding/json"
	"fmt"
	"kiro-go/config"
	"kiro-go/logger"
	"strings"
)

type payloadGuardOptions struct {
	SoftLimitBytes int
	HardLimitBytes int
}

type payloadGuardResult struct {
	OriginalBytes int
	FinalBytes    int
	Trimmed       bool
	TrimmedCount  int
	RecoveryNote  string
}

func defaultPayloadGuardOptions() payloadGuardOptions {
	return payloadGuardOptions{
		SoftLimitBytes: maxKiroHistoryPayloadBytes,
		HardLimitBytes: maxKiroHistoryPayloadBytes + 256*1024,
	}
}

func guardKiroPayload(payload *KiroPayload, opts payloadGuardOptions) (payloadGuardResult, error) {
	opts = normalizePayloadGuardOptions(opts)
	result := payloadGuardResult{OriginalBytes: kiroPayloadJSONSize(payload)}
	result.FinalBytes = result.OriginalBytes
	if payload != nil {
		before := len(payload.ConversationState.History)
		payload.ConversationState.History = dropOrphanedKiroToolMessagesForPayload(payload.ConversationState.History, currentToolResultIDs(payload))
		if after := len(payload.ConversationState.History); after < before {
			result.Trimmed = true
			result.TrimmedCount += before - after
			result.FinalBytes = kiroPayloadJSONSize(payload)
		}
	}
	if result.OriginalBytes <= opts.SoftLimitBytes {
		if result.Trimmed {
			result.RecoveryNote = payloadTruncationRecoveryNote()
		}
		return result, nil
	}

	if currentToolResultsSize(payload) > opts.HardLimitBytes/2 {
		return result, fmt.Errorf("current tool_result content is too large for Kiro payload")
	}

	for payload != nil && len(payload.ConversationState.History) > 0 && kiroPayloadJSONSize(payload) > opts.SoftLimitBytes {
		before := len(payload.ConversationState.History)
		currentResultIDs := currentToolResultIDs(payload)
		payload.ConversationState.History = trimOldestKiroHistoryPair(payload.ConversationState.History, currentResultIDs)
		payload.ConversationState.History = dropOrphanedKiroToolMessagesForPayload(payload.ConversationState.History, currentResultIDs)
		after := len(payload.ConversationState.History)
		if after >= before {
			break
		}
		result.Trimmed = true
		result.TrimmedCount += before - after
	}

	result.FinalBytes = kiroPayloadJSONSize(payload)
	if result.FinalBytes > opts.HardLimitBytes {
		return result, fmt.Errorf("Kiro payload remains too large after trimming: %d bytes", result.FinalBytes)
	}
	if result.Trimmed {
		result.RecoveryNote = payloadTruncationRecoveryNote()
	}
	return result, nil
}

func prepareGuardedKiroPayload(payload *KiroPayload, opts payloadGuardOptions) (payloadGuardResult, error) {
	result, err := guardKiroPayload(payload, opts)
	if err != nil {
		return result, err
	}
	return applyTruncationRecoveryNoteWithLimit(payload, result, opts)
}

func applyTruncationRecoveryNoteWithLimit(payload *KiroPayload, result payloadGuardResult, opts payloadGuardOptions) (payloadGuardResult, error) {
	opts = normalizePayloadGuardOptions(opts)
	if result.RecoveryNote == "" {
		result.FinalBytes = kiroPayloadJSONSize(payload)
		return result, nil
	}
	applyTruncationRecoveryNote(payload, result.RecoveryNote)
	result.FinalBytes = kiroPayloadJSONSize(payload)
	if result.FinalBytes > opts.HardLimitBytes {
		return result, fmt.Errorf("Kiro payload exceeds hard limit after recovery note: %d bytes", result.FinalBytes)
	}
	return result, nil
}

func finalizeKiroPayloadForAccount(payload *KiroPayload, account *config.Account, opts payloadGuardOptions) (payloadGuardResult, error) {
	opts = normalizePayloadGuardOptions(opts)
	if payload != nil && strings.TrimSpace(payload.ProfileArn) == "" {
		finalizeKiroPayloadProfileArn(payload, account)
	}
	result := payloadGuardResult{FinalBytes: kiroPayloadJSONSize(payload)}
	if result.FinalBytes > opts.HardLimitBytes {
		return result, fmt.Errorf("Kiro payload exceeds hard limit after ProfileArn finalization: %d bytes", result.FinalBytes)
	}
	return result, nil
}

func finalizeKiroPayloadProfileArn(payload *KiroPayload, account *config.Account) {
	if payload == nil || strings.TrimSpace(payload.ProfileArn) != "" {
		return
	}
	if profileArn, err := ResolveProfileArn(account); err == nil {
		payload.ProfileArn = profileArn
	} else {
		accountEmail := "<nil>"
		if account != nil {
			accountEmail = account.Email
		}
		logger.Warnf("[ProfileArn] Failed to resolve profile ARN for %s: %v", accountEmail, err)
	}
}

func normalizePayloadGuardOptions(opts payloadGuardOptions) payloadGuardOptions {
	if opts.SoftLimitBytes <= 0 {
		opts.SoftLimitBytes = maxKiroHistoryPayloadBytes
	}
	if opts.HardLimitBytes <= opts.SoftLimitBytes {
		opts.HardLimitBytes = opts.SoftLimitBytes + 256*1024
	}
	return opts
}

func kiroPayloadJSONSize(payload *KiroPayload) int {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	return len(data)
}

func currentToolResultsSize(payload *KiroPayload) int {
	if payload == nil || payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return 0
	}
	total := 0
	for _, result := range payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults {
		data, err := json.Marshal(result)
		if err == nil {
			total += len(data)
		}
	}
	return total
}

func trimOldestKiroHistoryPair(history []KiroHistoryMessage, currentResultIDs map[string]bool) []KiroHistoryMessage {
	if len(history) == 0 {
		return history
	}
	if historyMessageHasCurrentToolUse(history[0], currentResultIDs) {
		return history
	}
	removeCount := 1
	if historyMessageHasToolUses(history[0]) && len(history) > 1 && historyMessageHasToolResults(history[1]) {
		removeCount = 2
	}
	if removeCount > len(history) {
		removeCount = len(history)
	}
	out := append([]KiroHistoryMessage(nil), history[removeCount:]...)
	return out
}

func currentToolResultIDs(payload *KiroPayload) map[string]bool {
	ids := make(map[string]bool)
	if payload == nil || payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return ids
	}
	for _, result := range payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults {
		ids[result.ToolUseID] = true
	}
	return ids
}

func historyMessageHasCurrentToolUse(message KiroHistoryMessage, currentResultIDs map[string]bool) bool {
	if message.AssistantResponseMessage == nil || len(currentResultIDs) == 0 {
		return false
	}
	for _, toolUse := range message.AssistantResponseMessage.ToolUses {
		if currentResultIDs[toolUse.ToolUseID] {
			return true
		}
	}
	return false
}

func dropOrphanedKiroToolMessagesForPayload(history []KiroHistoryMessage, currentResultIDs map[string]bool) []KiroHistoryMessage {
	if len(history) == 0 {
		return history
	}
	toolUses := make(map[string]bool)
	toolResults := make(map[string]bool)
	for _, message := range history {
		if message.AssistantResponseMessage != nil {
			for _, toolUse := range message.AssistantResponseMessage.ToolUses {
				toolUses[toolUse.ToolUseID] = true
			}
		}
		if message.UserInputMessage != nil && message.UserInputMessage.UserInputMessageContext != nil {
			for _, result := range message.UserInputMessage.UserInputMessageContext.ToolResults {
				toolResults[result.ToolUseID] = true
			}
		}
	}
	for id := range currentResultIDs {
		toolResults[id] = true
	}

	out := make([]KiroHistoryMessage, 0, len(history))
	for _, message := range history {
		if message.AssistantResponseMessage != nil && len(message.AssistantResponseMessage.ToolUses) > 0 {
			kept := message.AssistantResponseMessage.ToolUses[:0]
			for _, toolUse := range message.AssistantResponseMessage.ToolUses {
				if toolResults[toolUse.ToolUseID] {
					kept = append(kept, toolUse)
				}
			}
			message.AssistantResponseMessage.ToolUses = kept
			if message.AssistantResponseMessage.Content == "" && len(kept) == 0 {
				continue
			}
		}
		if message.UserInputMessage != nil && message.UserInputMessage.UserInputMessageContext != nil && len(message.UserInputMessage.UserInputMessageContext.ToolResults) > 0 {
			kept := message.UserInputMessage.UserInputMessageContext.ToolResults[:0]
			for _, result := range message.UserInputMessage.UserInputMessageContext.ToolResults {
				if toolUses[result.ToolUseID] {
					kept = append(kept, result)
				}
			}
			message.UserInputMessage.UserInputMessageContext.ToolResults = kept
			if len(message.UserInputMessage.UserInputMessageContext.Tools) == 0 && len(kept) == 0 {
				message.UserInputMessage.UserInputMessageContext = nil
			}
			if message.UserInputMessage.Content == "" && message.UserInputMessage.UserInputMessageContext == nil {
				continue
			}
		}
		out = append(out, message)
	}
	return out
}

func hasOrphanedKiroToolMessages(history []KiroHistoryMessage) bool {
	toolUses := make(map[string]bool)
	toolResults := make(map[string]bool)
	for _, message := range history {
		if message.AssistantResponseMessage != nil {
			for _, toolUse := range message.AssistantResponseMessage.ToolUses {
				toolUses[toolUse.ToolUseID] = true
			}
		}
		if message.UserInputMessage != nil && message.UserInputMessage.UserInputMessageContext != nil {
			for _, result := range message.UserInputMessage.UserInputMessageContext.ToolResults {
				toolResults[result.ToolUseID] = true
			}
		}
	}
	for id := range toolUses {
		if !toolResults[id] {
			return true
		}
	}
	for id := range toolResults {
		if !toolUses[id] {
			return true
		}
	}
	return false
}

func cloneKiroHistoryMessages(history []KiroHistoryMessage) []KiroHistoryMessage {
	out := make([]KiroHistoryMessage, len(history))
	for i, message := range history {
		if message.UserInputMessage != nil {
			user := *message.UserInputMessage
			if message.UserInputMessage.Images != nil {
				user.Images = append([]KiroImage(nil), message.UserInputMessage.Images...)
			}
			if message.UserInputMessage.UserInputMessageContext != nil {
				ctx := *message.UserInputMessage.UserInputMessageContext
				if ctx.Tools != nil {
					ctx.Tools = append([]KiroToolWrapper(nil), ctx.Tools...)
				}
				if ctx.ToolResults != nil {
					ctx.ToolResults = append([]KiroToolResult(nil), ctx.ToolResults...)
				}
				user.UserInputMessageContext = &ctx
			}
			out[i].UserInputMessage = &user
		}
		if message.AssistantResponseMessage != nil {
			assistant := *message.AssistantResponseMessage
			if assistant.ToolUses != nil {
				assistant.ToolUses = append([]KiroToolUse(nil), assistant.ToolUses...)
			}
			out[i].AssistantResponseMessage = &assistant
		}
	}
	return out
}

func applyTruncationRecoveryNote(payload *KiroPayload, note string) {
	note = strings.TrimSpace(note)
	if payload == nil || note == "" {
		return
	}
	current := &payload.ConversationState.CurrentMessage.UserInputMessage
	current.Content = "--- CONTEXT NOTICE ---\n" + note + "\n--- END CONTEXT NOTICE ---\n\n" + current.Content
}

func payloadTruncationRecoveryNote() string {
	return "Some earlier conversation history was trimmed before sending this turn to the upstream model."
}
