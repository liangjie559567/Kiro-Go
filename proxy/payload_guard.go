package proxy

import (
	"encoding/json"
	"fmt"
	"kiro-go/config"
	"kiro-go/logger"
	"sort"
	"strings"
)

type payloadGuardOptions struct {
	SoftLimitBytes       int
	HardLimitBytes       int
	MaxHistoryMessages   int
	MaxHistoryToolUses   int
	MaxCurrentTools      int
	MaxToolSchemaBytes   int
	MaxToolDescription   int
	MaxSchemaDescription int
	MaxSchemaDepth       int
	MaxSchemaProperties  int
	MaxHistoryToolBytes  int
	MaxHistoryBlockBytes int
}

type payloadGuardResult struct {
	OriginalBytes                int
	FinalBytes                   int
	Trimmed                      bool
	TrimmedCount                 int
	RecoveryNote                 string
	Summary                      kiroPayloadSummary
	KeptToolNames                []string
	TrimmedToolNames             []string
	DeferredToolNames            []string
	MaterializedToolRefNames     []string
	CompactedPairs               int
	CompactedToolResults         int
	OrphanedToolResultsConverted int
	ToolResultImages             int
	RelocatedToolDescriptions    int
	UnsupportedContentBlocks     []string
}

const minCurrentToolResultTextBytes = 256
const maxCurrentToolResultTextBytes = 64 * 1024
const maxHistoryToolResultTextBytes = 128 * 1024
const maxHistoryToolResultBlockBytes = 8 * 1024
const kiroMalformedRiskPayloadBytes = 400 * 1024
const maxKiroHistoryMessages = 48
const maxKiroHistoryToolUses = 16
const maxKiroTools = 16
const maxKiroToolSchemaBytes = 60 * 1024
const maxKiroToolDescriptionBytes = 1024
const maxKiroSchemaDescriptionBytes = 256
const maxKiroToolSchemaDepth = 3
const maxKiroSchemaProperties = 16
const conservativeKiroHistoryMessages = 32
const conservativeKiroHistoryToolUses = 8
const conservativeKiroTools = 8
const conservativeKiroToolSchemaBytes = 24 * 1024
const conservativeKiroToolDescriptionBytes = 512
const conservativeKiroSchemaDescriptionBytes = 128
const conservativeKiroToolSchemaDepth = 2
const conservativeKiroSchemaProperties = 8
const conservativeHistoryToolResultTextBytes = 64 * 1024
const conservativeHistoryToolResultBlockBytes = 4 * 1024
const maxPayloadToolNameLogEntries = 24

type kiroPayloadSummary struct {
	TotalBytes             int `json:"totalBytes"`
	HistoryMessages        int `json:"historyMessages"`
	HistoryUserBytes       int `json:"historyUserBytes"`
	HistoryAssistantBytes  int `json:"historyAssistantBytes"`
	HistoryToolUses        int `json:"historyToolUses"`
	HistoryToolResultBytes int `json:"historyToolResultBytes"`
	CurrentContentBytes    int `json:"currentContentBytes"`
	CurrentToolResultBytes int `json:"currentToolResultBytes"`
	Images                 int `json:"images"`
	Tools                  int `json:"tools"`
	CurrentTools           int `json:"currentTools"`
	CurrentToolSchemaBytes int `json:"currentToolSchemaBytes"`
	CurrentMessageShape    string
	ContextReminderKinds   []string
}

func defaultPayloadGuardOptions() payloadGuardOptions {
	return payloadGuardOptions{
		SoftLimitBytes: maxKiroHistoryPayloadBytes,
		HardLimitBytes: maxKiroHistoryPayloadBytes + 256*1024,
	}
}

func conservativePayloadGuardOptions() payloadGuardOptions {
	opts := defaultPayloadGuardOptions()
	opts.MaxHistoryMessages = conservativeKiroHistoryMessages
	opts.MaxHistoryToolUses = conservativeKiroHistoryToolUses
	opts.MaxCurrentTools = conservativeKiroTools
	opts.MaxToolSchemaBytes = conservativeKiroToolSchemaBytes
	opts.MaxToolDescription = conservativeKiroToolDescriptionBytes
	opts.MaxSchemaDescription = conservativeKiroSchemaDescriptionBytes
	opts.MaxSchemaDepth = conservativeKiroToolSchemaDepth
	opts.MaxSchemaProperties = conservativeKiroSchemaProperties
	opts.MaxHistoryToolBytes = conservativeHistoryToolResultTextBytes
	opts.MaxHistoryBlockBytes = conservativeHistoryToolResultBlockBytes
	return opts
}

func guardKiroPayload(payload *KiroPayload, opts payloadGuardOptions) (payloadGuardResult, error) {
	opts = normalizePayloadGuardOptions(opts)
	result := payloadGuardResult{OriginalBytes: kiroPayloadJSONSize(payload)}
	if payload != nil {
		result.DeferredToolNames = cappedToolNames(payload.DeferredToolReferenceNames)
		result.MaterializedToolRefNames = cappedToolNames(payload.MaterializedToolReferenceNames)
		result.ToolResultImages = payload.ToolResultImages
		result.RelocatedToolDescriptions = payload.RelocatedToolDescriptions
		result.UnsupportedContentBlocks = append([]string(nil), payload.UnsupportedContentBlocks...)
	}
	result.FinalBytes = result.OriginalBytes
	if payload != nil {
		before := len(payload.ConversationState.History)
		beforeToolResults := countKiroHistoryToolResults(payload.ConversationState.History)
		payload.ConversationState.History = dropOrphanedKiroToolMessagesForPayload(payload.ConversationState.History, currentToolResultIDs(payload))
		if after := len(payload.ConversationState.History); after < before {
			result.Trimmed = true
			result.TrimmedCount += before - after
			result.CompactedToolResults += maxInt(0, beforeToolResults-countKiroHistoryToolResults(payload.ConversationState.History))
			result.FinalBytes = kiroPayloadJSONSize(payload)
		}
	}
	if trimmedHistory, trimmedResults := enforceCurrentToolResultAdjacency(payload); trimmedHistory > 0 || trimmedResults > 0 {
		result.Trimmed = true
		result.TrimmedCount += trimmedHistory + trimmedResults
		result.CompactedToolResults += trimmedResults
		result.FinalBytes = kiroPayloadJSONSize(payload)
	}

	if trimmed, keptNames, trimmedNames := sanitizeCurrentToolsForPayload(payload, opts); trimmed > 0 || len(keptNames) > 0 || len(trimmedNames) > 0 {
		result.Trimmed = true
		result.TrimmedCount += trimmed
		result.KeptToolNames = keptNames
		result.TrimmedToolNames = trimmedNames
		result.FinalBytes = kiroPayloadJSONSize(payload)
	}

	if currentToolResultsSize(payload) > opts.HardLimitBytes/2 {
		trimmed := truncateCurrentToolResultsForPayload(payload, opts.HardLimitBytes/4)
		if trimmed > 0 {
			truncateCurrentToolResultContinuationForPayload(payload, opts.HardLimitBytes/4)
			result.Trimmed = true
			result.TrimmedCount += trimmed
			result.FinalBytes = kiroPayloadJSONSize(payload)
		}
		if currentToolResultsSize(payload) > opts.HardLimitBytes/2 {
			return result, fmt.Errorf("current tool_result content is too large for Kiro payload")
		}
	}

	if shouldTrimHistoryToolResults(payload, opts) {
		trimmed := truncateHistoryToolResultsForPayload(payload, opts.MaxHistoryToolBytes, opts.MaxHistoryBlockBytes)
		if trimmed > 0 {
			result.Trimmed = true
			result.TrimmedCount += trimmed
			result.CompactedToolResults += trimmed
			result.FinalBytes = kiroPayloadJSONSize(payload)
		}
	}

	if shouldTrimHistoryStructure(payload, opts) {
		before := len(payload.ConversationState.History)
		stats := kiroHistoryCompactionStats{}
		payload.ConversationState.History = trimKiroHistoryStructureWindowWithStats(payload.ConversationState.History, currentToolResultIDs(payload), opts, &stats)
		payload.ConversationState.History = dropOrphanedKiroToolMessagesForPayload(payload.ConversationState.History, currentToolResultIDs(payload))
		if after := len(payload.ConversationState.History); after < before {
			result.Trimmed = true
			result.TrimmedCount += before - after
			result.CompactedPairs += stats.Pairs
			result.CompactedToolResults += stats.ToolResults
			result.FinalBytes = kiroPayloadJSONSize(payload)
		}
	}

	if result.FinalBytes <= opts.SoftLimitBytes {
		result.Summary = summarizeKiroPayload(payload)
		if result.Trimmed {
			result.RecoveryNote = payloadTruncationRecoveryNote()
		}
		return result, nil
	}

	for payload != nil && len(payload.ConversationState.History) > 0 && kiroPayloadJSONSize(payload) > opts.SoftLimitBytes {
		before := len(payload.ConversationState.History)
		currentResultIDs := currentToolResultIDs(payload)
		stats := kiroHistoryCompactionStats{}
		payload.ConversationState.History = trimOldestKiroHistoryPairWithStats(payload.ConversationState.History, currentResultIDs, &stats)
		payload.ConversationState.History = dropOrphanedKiroToolMessagesForPayload(payload.ConversationState.History, currentResultIDs)
		after := len(payload.ConversationState.History)
		if after >= before {
			break
		}
		result.Trimmed = true
		result.TrimmedCount += before - after
		result.CompactedPairs += stats.Pairs
		result.CompactedToolResults += stats.ToolResults
	}

	result.FinalBytes = kiroPayloadJSONSize(payload)
	if result.FinalBytes > opts.HardLimitBytes {
		return result, fmt.Errorf("Kiro payload remains too large after trimming: %d bytes", result.FinalBytes)
	}
	result.Summary = summarizeKiroPayload(payload)
	if result.Trimmed {
		result.RecoveryNote = payloadTruncationRecoveryNote()
	}
	return result, nil
}

func shouldTrimHistoryToolResults(payload *KiroPayload, opts payloadGuardOptions) bool {
	if payload == nil {
		return false
	}
	summary := summarizeKiroPayload(payload)
	if summary.HistoryToolResultBytes > opts.MaxHistoryToolBytes {
		return true
	}
	return summary.TotalBytes > kiroMalformedRiskPayloadBytes && summary.HistoryToolResultBytes > opts.MaxHistoryToolBytes/2
}

func shouldTrimHistoryStructure(payload *KiroPayload, opts payloadGuardOptions) bool {
	if payload == nil {
		return false
	}
	summary := summarizeKiroPayload(payload)
	return summary.HistoryMessages > opts.MaxHistoryMessages || summary.HistoryToolUses > opts.MaxHistoryToolUses
}

func prepareGuardedKiroPayload(payload *KiroPayload, opts payloadGuardOptions) (payloadGuardResult, error) {
	result, err := guardKiroPayload(payload, opts)
	if payload != nil {
		result.OrphanedToolResultsConverted = payload.OrphanedToolResultsConverted
		result.ToolResultImages = payload.ToolResultImages
		result.RelocatedToolDescriptions = payload.RelocatedToolDescriptions
		result.UnsupportedContentBlocks = append([]string(nil), payload.UnsupportedContentBlocks...)
	}
	if err != nil {
		return result, err
	}
	result, err = applyTruncationRecoveryNoteWithLimit(payload, result, opts)
	if payload != nil {
		result.OrphanedToolResultsConverted = payload.OrphanedToolResultsConverted
		result.ToolResultImages = payload.ToolResultImages
		result.RelocatedToolDescriptions = payload.RelocatedToolDescriptions
		result.UnsupportedContentBlocks = append([]string(nil), payload.UnsupportedContentBlocks...)
	}
	return result, err
}

func cloneKiroPayload(payload *KiroPayload) *KiroPayload {
	if payload == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var cloned KiroPayload
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	if payload.ToolNameMap != nil {
		cloned.ToolNameMap = make(map[string]string, len(payload.ToolNameMap))
		for key, value := range payload.ToolNameMap {
			cloned.ToolNameMap[key] = value
		}
	}
	if payload.ToolSchemas != nil {
		cloned.ToolSchemas = make(map[string]toolSchemaSummary, len(payload.ToolSchemas))
		for key, value := range payload.ToolSchemas {
			cloned.ToolSchemas[key] = toolSchemaSummary{Required: append([]string(nil), value.Required...), Schema: cloneSchemaMap(value.Schema)}
		}
	}
	cloned.DeferredToolReferenceNames = append([]string(nil), payload.DeferredToolReferenceNames...)
	cloned.MaterializedToolReferenceNames = append([]string(nil), payload.MaterializedToolReferenceNames...)
	cloned.CurrentMessageShape = payload.CurrentMessageShape
	cloned.ContextReminderKinds = append([]string(nil), payload.ContextReminderKinds...)
	cloned.OrphanedToolResultsConverted = payload.OrphanedToolResultsConverted
	cloned.ToolResultImages = payload.ToolResultImages
	cloned.RelocatedToolDescriptions = payload.RelocatedToolDescriptions
	cloned.UnsupportedContentBlocks = append([]string(nil), payload.UnsupportedContentBlocks...)
	return &cloned
}

func cloneSchemaMap(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return schema
	}
	var cloned map[string]interface{}
	if err := json.Unmarshal(data, &cloned); err != nil {
		return schema
	}
	return cloned
}

func applyTruncationRecoveryNoteWithLimit(payload *KiroPayload, result payloadGuardResult, opts payloadGuardOptions) (payloadGuardResult, error) {
	opts = normalizePayloadGuardOptions(opts)
	if result.RecoveryNote == "" {
		result.FinalBytes = kiroPayloadJSONSize(payload)
		result.Summary = summarizeKiroPayload(payload)
		return result, nil
	}
	applyTruncationRecoveryNote(payload, result.RecoveryNote)
	result.FinalBytes = kiroPayloadJSONSize(payload)
	if result.FinalBytes > opts.HardLimitBytes {
		return result, fmt.Errorf("Kiro payload exceeds hard limit after recovery note: %d bytes", result.FinalBytes)
	}
	result.Summary = summarizeKiroPayload(payload)
	return result, nil
}

func finalizeKiroPayloadForAccount(payload *KiroPayload, account *config.Account, opts payloadGuardOptions) (payloadGuardResult, error) {
	opts = normalizePayloadGuardOptions(opts)
	if payload != nil && strings.TrimSpace(payload.ProfileArn) == "" {
		finalizeKiroPayloadProfileArn(payload, account)
	}
	if payload != nil {
		payload.ProfileArnFinalized = true
	}
	result := payloadGuardResult{FinalBytes: kiroPayloadJSONSize(payload)}
	if payload != nil {
		result.ToolResultImages = payload.ToolResultImages
		result.RelocatedToolDescriptions = payload.RelocatedToolDescriptions
		result.UnsupportedContentBlocks = append([]string(nil), payload.UnsupportedContentBlocks...)
	}
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
	if opts.MaxHistoryMessages <= 0 {
		opts.MaxHistoryMessages = maxKiroHistoryMessages
	}
	if opts.MaxHistoryToolUses <= 0 {
		opts.MaxHistoryToolUses = maxKiroHistoryToolUses
	}
	if opts.MaxCurrentTools <= 0 {
		opts.MaxCurrentTools = maxKiroTools
	}
	if opts.MaxToolSchemaBytes <= 0 {
		opts.MaxToolSchemaBytes = maxKiroToolSchemaBytes
	}
	if opts.MaxToolDescription <= 0 {
		opts.MaxToolDescription = maxKiroToolDescriptionBytes
	}
	if opts.MaxSchemaDescription <= 0 {
		opts.MaxSchemaDescription = maxKiroSchemaDescriptionBytes
	}
	if opts.MaxSchemaDepth <= 0 {
		opts.MaxSchemaDepth = maxKiroToolSchemaDepth
	}
	if opts.MaxSchemaProperties <= 0 {
		opts.MaxSchemaProperties = maxKiroSchemaProperties
	}
	if opts.MaxHistoryToolBytes <= 0 {
		opts.MaxHistoryToolBytes = maxHistoryToolResultTextBytes
	}
	if opts.MaxHistoryBlockBytes <= 0 {
		opts.MaxHistoryBlockBytes = maxHistoryToolResultBlockBytes
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

func summarizeKiroPayload(payload *KiroPayload) kiroPayloadSummary {
	summary := kiroPayloadSummary{TotalBytes: kiroPayloadJSONSize(payload)}
	if payload == nil {
		return summary
	}
	for _, message := range payload.ConversationState.History {
		summary.HistoryMessages++
		if message.UserInputMessage != nil {
			summary.HistoryUserBytes += len(message.UserInputMessage.Content)
			summary.Images += len(message.UserInputMessage.Images)
			if ctx := message.UserInputMessage.UserInputMessageContext; ctx != nil {
				summary.Tools += len(ctx.Tools)
				for _, result := range ctx.ToolResults {
					summary.HistoryToolResultBytes += kiroToolResultTextBytes(result)
				}
			}
		}
		if message.AssistantResponseMessage != nil {
			summary.HistoryAssistantBytes += len(message.AssistantResponseMessage.Content)
			summary.HistoryToolUses += len(message.AssistantResponseMessage.ToolUses)
		}
	}
	current := payload.ConversationState.CurrentMessage.UserInputMessage
	summary.CurrentContentBytes = len(current.Content)
	summary.Images += len(current.Images)
	summary.CurrentMessageShape = payload.CurrentMessageShape
	summary.ContextReminderKinds = append([]string(nil), payload.ContextReminderKinds...)
	if current.UserInputMessageContext != nil {
		summary.CurrentTools = len(current.UserInputMessageContext.Tools)
		summary.Tools += summary.CurrentTools
		summary.CurrentToolSchemaBytes = currentToolsJSONSize(payload)
		for _, result := range current.UserInputMessageContext.ToolResults {
			summary.CurrentToolResultBytes += kiroToolResultTextBytes(result)
		}
	}
	return summary
}

func kiroToolResultTextBytes(result KiroToolResult) int {
	total := 0
	for _, content := range result.Content {
		total += len(content.Text)
	}
	return total
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

func currentToolsJSONSize(payload *KiroPayload) int {
	if payload == nil || payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return 0
	}
	data, err := json.Marshal(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)
	if err != nil {
		return 0
	}
	return len(data)
}

func sanitizeCurrentToolsForPayload(payload *KiroPayload, opts payloadGuardOptions) (int, []string, []string) {
	if payload == nil || payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return 0, nil, nil
	}
	opts = normalizePayloadGuardOptions(opts)
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	trimmed := 0
	prioritizeCurrentToolsForPayload(payload)
	if len(ctx.Tools) > opts.MaxCurrentTools {
		trimmedNames := payloadToolNames(ctx.Tools[opts.MaxCurrentTools:])
		ctx.Tools = append([]KiroToolWrapper(nil), ctx.Tools[:opts.MaxCurrentTools]...)
		trimmed++
		return sanitizeCurrentToolsForPayloadAfterCap(payload, opts, trimmed, trimmedNames)
	}
	return sanitizeCurrentToolsForPayloadAfterCap(payload, opts, trimmed, nil)
}

func sanitizeCurrentToolsForPayloadAfterCap(payload *KiroPayload, opts payloadGuardOptions, trimmed int, trimmedNames []string) (int, []string, []string) {
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	for i := range ctx.Tools {
		spec := &ctx.Tools[i].ToolSpecification
		if len(spec.Description) > opts.MaxToolDescription {
			spec.Description = truncateTextPlain(spec.Description, opts.MaxToolDescription)
			trimmed++
		}
		sanitizedSchema := sanitizeKiroToolSchema(spec.InputSchema.JSON, 0, opts)
		if !jsonEqual(spec.InputSchema.JSON, sanitizedSchema) {
			spec.InputSchema.JSON = sanitizedSchema
			trimmed++
		}
	}
	for currentToolsJSONSize(payload) > opts.MaxToolSchemaBytes && len(ctx.Tools) > 1 {
		trimmedNames = append(trimmedNames, ctx.Tools[len(ctx.Tools)-1].ToolSpecification.Name)
		ctx.Tools = ctx.Tools[:len(ctx.Tools)-1]
		trimmed++
	}
	return trimmed, payloadToolNames(ctx.Tools), cappedToolNames(trimmedNames)
}

func payloadToolNames(tools []KiroToolWrapper) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.ToolSpecification.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
		if len(names) >= maxPayloadToolNameLogEntries {
			break
		}
	}
	return names
}

func cappedToolNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, name)
		if len(out) >= maxPayloadToolNameLogEntries {
			break
		}
	}
	return out
}

func prioritizeCurrentToolsForPayload(payload *KiroPayload) {
	if payload == nil || payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return
	}
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if len(ctx.Tools) < 2 {
		return
	}
	prompt := strings.ToLower(payload.ConversationState.CurrentMessage.UserInputMessage.Content)
	type rankedTool struct {
		tool  KiroToolWrapper
		score int
		index int
	}
	ranked := make([]rankedTool, 0, len(ctx.Tools))
	for i, tool := range ctx.Tools {
		spec := tool.ToolSpecification
		score := toolRelevanceScore(prompt, spec.Name, spec.Description)
		ranked = append(ranked, rankedTool{tool: tool, score: score, index: i})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].index < ranked[j].index
	})
	for i := range ranked {
		ctx.Tools[i] = ranked[i].tool
	}
}

func toolRelevanceScore(prompt, name, description string) int {
	score := 0
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	if normalizedName != "" {
		score += coreClaudeCodeToolPriority(normalizedName)
		if strings.Contains(prompt, normalizedName) {
			score += 100
		}
		for _, token := range splitToolNameTokens(normalizedName) {
			if len(token) >= 3 && strings.Contains(prompt, token) {
				score += 10
			}
		}
	}
	for _, token := range splitToolDescriptionTokens(description) {
		if len(token) >= 4 && strings.Contains(prompt, token) {
			score += 2
		}
	}
	return score
}

func coreClaudeCodeToolPriority(name string) int {
	switch name {
	case "agent", "task":
		return 90
	case "todowrite", "todoread":
		return 70
	case "bash", "read", "write", "edit", "multiedit", "glob", "grep", "ls":
		return 50
	case "webfetch", "websearch":
		return 40
	default:
		return 0
	}
}

func splitToolNameTokens(name string) []string {
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/' || r == ':'
	})
	return fields
}

func splitToolDescriptionTokens(description string) []string {
	description = strings.ToLower(description)
	fields := strings.FieldsFunc(description, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	if len(fields) > 32 {
		return fields[:32]
	}
	return fields
}

func sanitizeKiroToolSchema(schema interface{}, depth int, opts payloadGuardOptions) interface{} {
	opts = normalizePayloadGuardOptions(opts)
	if depth > opts.MaxSchemaDepth {
		return map[string]interface{}{"type": "object"}
	}
	m, ok := schema.(map[string]interface{})
	if !ok {
		return map[string]interface{}{"type": "object"}
	}
	out := make(map[string]interface{}, len(m))
	for key, value := range m {
		switch key {
		case "description":
			if text, ok := value.(string); ok {
				out[key] = truncateTextPlain(text, opts.MaxSchemaDescription)
			}
		case "anyOf", "oneOf", "allOf":
			out["type"] = "object"
		case "properties":
			props, ok := value.(map[string]interface{})
			if !ok {
				continue
			}
			cleanedProps := make(map[string]interface{}, len(props))
			count := 0
			for propName, propSchema := range props {
				if count >= opts.MaxSchemaProperties {
					break
				}
				cleanedProps[propName] = sanitizeKiroToolSchema(propSchema, depth+1, opts)
				count++
			}
			if len(cleanedProps) > 0 {
				out[key] = cleanedProps
			}
		case "items":
			if sub, ok := value.(map[string]interface{}); ok {
				out[key] = sanitizeKiroToolSchema(sub, depth+1, opts)
			}
		case "additionalProperties":
			continue
		case "required":
			if arr, ok := value.([]interface{}); ok && len(arr) > 0 {
				if len(arr) > opts.MaxSchemaProperties {
					arr = arr[:opts.MaxSchemaProperties]
				}
				out[key] = arr
			}
		case "type", "enum", "const", "format", "minimum", "maximum", "minLength", "maxLength":
			out[key] = value
		}
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	return out
}

func truncateTextPlain(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	notice := " [truncated]"
	if maxBytes <= len(notice) {
		return text[:maxBytes]
	}
	return text[:maxBytes-len(notice)] + notice
}

func jsonEqual(a, b interface{}) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}

func truncateCurrentToolResultsForPayload(payload *KiroPayload, budgetBytes int) int {
	if payload == nil || payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return 0
	}
	results := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults
	if len(results) == 0 {
		return 0
	}
	if budgetBytes < minCurrentToolResultTextBytes {
		budgetBytes = minCurrentToolResultTextBytes
	}
	perContentBudget := (budgetBytes / countCurrentToolResultContentBlocks(results)) - 256
	if perContentBudget < minCurrentToolResultTextBytes {
		perContentBudget = minCurrentToolResultTextBytes
	}
	if perContentBudget > maxCurrentToolResultTextBytes {
		perContentBudget = maxCurrentToolResultTextBytes
	}
	trimmed := 0
	for resultIdx := range results {
		for contentIdx := range results[resultIdx].Content {
			text := results[resultIdx].Content[contentIdx].Text
			if len(text) <= perContentBudget {
				continue
			}
			results[resultIdx].Content[contentIdx].Text = truncateTextWithNotice(text, perContentBudget)
			trimmed++
		}
	}
	payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = results
	return trimmed
}

func truncateCurrentToolResultContinuationForPayload(payload *KiroPayload, budgetBytes int) bool {
	if payload == nil {
		return false
	}
	current := &payload.ConversationState.CurrentMessage.UserInputMessage
	idx := generatedToolResultsContinuationIndex(current.Content)
	if idx < 0 {
		return false
	}
	if budgetBytes < minCurrentToolResultTextBytes {
		budgetBytes = minCurrentToolResultTextBytes
	}
	prefix := strings.TrimSpace(current.Content[:idx])
	continuation := strings.TrimSpace(current.Content[idx:])
	if len(continuation) <= budgetBytes {
		return false
	}
	truncated := truncateTextWithNotice(continuation, budgetBytes)
	if prefix == "" {
		current.Content = truncated
		return true
	}
	current.Content = prefix + "\n\n" + truncated
	return true
}

func truncateHistoryToolResultsForPayload(payload *KiroPayload, totalBudgetBytes int, maxBlockBytes int) int {
	if payload == nil || totalBudgetBytes <= 0 {
		return 0
	}
	type contentRef struct {
		messageIdx int
		resultIdx  int
		contentIdx int
		size       int
	}
	var refs []contentRef
	total := 0
	for messageIdx, message := range payload.ConversationState.History {
		if message.UserInputMessage == nil || message.UserInputMessage.UserInputMessageContext == nil {
			continue
		}
		results := message.UserInputMessage.UserInputMessageContext.ToolResults
		for resultIdx, result := range results {
			for contentIdx, content := range result.Content {
				size := len(content.Text)
				total += size
				if size > 0 {
					refs = append(refs, contentRef{messageIdx: messageIdx, resultIdx: resultIdx, contentIdx: contentIdx, size: size})
				}
			}
		}
	}
	if len(refs) == 0 || total <= totalBudgetBytes {
		return 0
	}
	perBlockBudget := totalBudgetBytes / len(refs)
	if perBlockBudget < minCurrentToolResultTextBytes {
		perBlockBudget = minCurrentToolResultTextBytes
	}
	if maxBlockBytes <= 0 {
		maxBlockBytes = maxHistoryToolResultBlockBytes
	}
	if perBlockBudget > maxBlockBytes {
		perBlockBudget = maxBlockBytes
	}

	trimmed := 0
	for _, ref := range refs {
		message := payload.ConversationState.History[ref.messageIdx].UserInputMessage
		text := message.UserInputMessageContext.ToolResults[ref.resultIdx].Content[ref.contentIdx].Text
		if len(text) <= perBlockBudget {
			continue
		}
		message.UserInputMessageContext.ToolResults[ref.resultIdx].Content[ref.contentIdx].Text = truncateTextWithNotice(text, perBlockBudget)
		trimmed++
	}
	return trimmed
}

func countCurrentToolResultContentBlocks(results []KiroToolResult) int {
	count := 0
	for _, result := range results {
		count += len(result.Content)
	}
	if count < 1 {
		return 1
	}
	return count
}

func truncateTextWithNotice(text string, maxBytes int) string {
	notice := "\n\n[tool_result truncated before upstream request]"
	if maxBytes <= len(notice) {
		return notice
	}
	limit := maxBytes - len(notice)
	if limit > len(text) {
		limit = len(text)
	}
	return text[:limit] + notice
}

type kiroHistoryCompactionStats struct {
	Pairs       int
	ToolResults int
}

func trimOldestKiroHistoryPair(history []KiroHistoryMessage, currentResultIDs map[string]bool) []KiroHistoryMessage {
	return trimOldestKiroHistoryPairWithStats(history, currentResultIDs, nil)
}

func trimOldestKiroHistoryPairWithStats(history []KiroHistoryMessage, currentResultIDs map[string]bool, stats *kiroHistoryCompactionStats) []KiroHistoryMessage {
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
	if stats != nil {
		removed := history[:removeCount]
		if removeCount == 2 && historyMessageHasToolUses(removed[0]) && historyMessageHasToolResults(removed[1]) {
			stats.Pairs++
		}
		stats.ToolResults += countKiroHistoryToolResults(removed)
	}
	out := append([]KiroHistoryMessage(nil), history[removeCount:]...)
	return out
}

func trimKiroHistoryStructureWindow(history []KiroHistoryMessage, currentResultIDs map[string]bool, opts payloadGuardOptions) []KiroHistoryMessage {
	return trimKiroHistoryStructureWindowWithStats(history, currentResultIDs, opts, nil)
}

func trimKiroHistoryStructureWindowWithStats(history []KiroHistoryMessage, currentResultIDs map[string]bool, opts payloadGuardOptions, stats *kiroHistoryCompactionStats) []KiroHistoryMessage {
	if len(history) == 0 {
		return history
	}
	opts = normalizePayloadGuardOptions(opts)
	trimmed := append([]KiroHistoryMessage(nil), history...)
	for len(trimmed) > 0 && (len(trimmed) > opts.MaxHistoryMessages || countKiroHistoryToolUses(trimmed) > opts.MaxHistoryToolUses) {
		before := len(trimmed)
		trimmed = trimOldestKiroHistoryPairWithStats(trimmed, currentResultIDs, stats)
		if len(trimmed) >= before {
			break
		}
		trimmed = dropOrphanedKiroToolMessagesForPayload(trimmed, currentResultIDs)
	}
	return trimmed
}

func countKiroHistoryToolUses(history []KiroHistoryMessage) int {
	total := 0
	for _, message := range history {
		if message.AssistantResponseMessage != nil {
			total += len(message.AssistantResponseMessage.ToolUses)
		}
	}
	return total
}

func countKiroHistoryToolResults(history []KiroHistoryMessage) int {
	total := 0
	for _, message := range history {
		if message.UserInputMessage != nil && message.UserInputMessage.UserInputMessageContext != nil {
			total += len(message.UserInputMessage.UserInputMessageContext.ToolResults)
		}
	}
	return total
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

func enforceCurrentToolResultAdjacency(payload *KiroPayload) (int, int) {
	if payload == nil || payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext == nil {
		return 0, 0
	}
	ctx := payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext
	if len(ctx.ToolResults) == 0 {
		return 0, 0
	}
	matchingIdx := -1
	matchingUses := map[string]KiroToolUse{}
	for idx := len(payload.ConversationState.History) - 1; idx >= 0; idx-- {
		message := payload.ConversationState.History[idx]
		if message.AssistantResponseMessage == nil || len(message.AssistantResponseMessage.ToolUses) == 0 {
			continue
		}
		for _, toolUse := range message.AssistantResponseMessage.ToolUses {
			for _, result := range ctx.ToolResults {
				if strings.TrimSpace(result.ToolUseID) != "" && result.ToolUseID == toolUse.ToolUseID {
					matchingUses[toolUse.ToolUseID] = toolUse
				}
			}
		}
		if len(matchingUses) > 0 {
			matchingIdx = idx
			break
		}
	}

	if matchingIdx < 0 {
		return 0, 0
	}

	trimmedResults := 0
	beforeHistory := len(payload.ConversationState.History)
	assistant := *payload.ConversationState.History[matchingIdx].AssistantResponseMessage
	filteredUses := make([]KiroToolUse, 0, len(assistant.ToolUses))
	for _, toolUse := range assistant.ToolUses {
		if _, ok := matchingUses[toolUse.ToolUseID]; ok {
			filteredUses = append(filteredUses, toolUse)
		}
	}
	assistant.ToolUses = filteredUses
	trimmedHistory := beforeHistory - (matchingIdx + 1)
	preserved := append([]KiroHistoryMessage(nil), payload.ConversationState.History[:matchingIdx]...)
	preserved = append(preserved, KiroHistoryMessage{AssistantResponseMessage: &assistant})
	payload.ConversationState.History = preserved
	return trimmedHistory, trimmedResults
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
