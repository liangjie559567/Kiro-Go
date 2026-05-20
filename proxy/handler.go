package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kiro-go/auth"
	"kiro-go/config"
	"kiro-go/logger"
	"kiro-go/pool"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	ensureValidTokenForHealthCheck    = defaultEnsureValidTokenForHealthCheck
	listAvailableModelsForHealthCheck = ListAvailableModels
	opusCapacityRetryBudget           = 90 * time.Second
	sleepForOpusCapacityRetry         = time.Sleep
	adminAccountTestMinSpacing        = 3 * time.Second
	opus47AdmissionGate               = newOpus47Gate(2, 200)
	modelAdmissionGate                = newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 2, MaxWaiting: 200},
		},
	})
)

const tokenRefreshSkewSeconds int64 = 120

const poolTemporaryLimitStatus = http.StatusTooManyRequests

func defaultEnsureValidTokenForHealthCheck(h *Handler, account *config.Account) error {
	return h.ensureValidToken(account)
}

// Handler HTTP 处理器
type Handler struct {
	pool *pool.AccountPool
	// 运行时统计 (使用原子操作)
	totalRequests      int64
	successRequests    int64
	failedRequests     int64
	totalTokens        int64
	totalCredits       float64 // float64 需要用锁保护
	creditsMu          sync.RWMutex
	startTime          int64
	stopRefresh        chan struct{}
	stopStatsSaver     chan struct{}
	autoRefreshUpdated chan struct{}
	autoRefreshMu      sync.RWMutex
	autoRefreshStatus  autoRefreshStatus
	healthCheckUpdated chan struct{}
	healthCheckMu      sync.RWMutex
	healthCheckStatus  healthCheckStatus
	// 模型缓存
	cachedModels    []ModelInfo
	modelsCacheMu   sync.RWMutex
	modelsCacheTime int64
	promptCache     *promptCacheTracker
	tokenRefreshMu  sync.Mutex
	tokenRefreshes  map[string]*tokenRefreshCall
	requestLogsMu   sync.Mutex
	requestLogs     *requestLogStore
	accountTestMu   sync.Mutex
	accountTestLast map[string]time.Time
	responsesMu     sync.Mutex
	responses       map[string]responsesSession
}

type tokenRefreshCall struct {
	done chan struct{}
	err  error
}

const (
	maxOpenAIResponsesSessions = 128
	openAIResponsesSessionTTL  = time.Hour
)

type responsesSession struct {
	PreviousResponseID string
	Messages           []OpenAIMessage
	Tools              []OpenAITool
	ToolChoice         interface{}
	UpdatedAt          time.Time
}

type thinkingStreamSource int

const (
	thinkingSourceUnknown thinkingStreamSource = iota
	thinkingSourceReasoningEvent
	thinkingSourceTagBlock
)

func allowReasoningSource(source *thinkingStreamSource) bool {
	if *source == thinkingSourceTagBlock {
		return false
	}
	*source = thinkingSourceReasoningEvent
	return true
}

func allowTagSource(source *thinkingStreamSource) bool {
	if *source == thinkingSourceReasoningEvent {
		return false
	}
	if *source == thinkingSourceUnknown {
		*source = thinkingSourceTagBlock
	}
	return *source == thinkingSourceTagBlock
}

func validateClaudeRequestShape(req *ClaudeRequest) string {
	return validateClaudeRequestShapeWithOptions(req, true)
}

func validateClaudeRequestShapeWithOptions(req *ClaudeRequest, maxTokensPresent bool) string {
	if len(req.Messages) == 0 {
		return "messages must not be empty"
	}
	if msg := validateClaudeThinkingConfigWithOptions(req.Thinking, req.MaxTokens, maxTokensPresent); msg != "" {
		return msg
	}

	hasUserContext := false
	lastRole := ""
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			continue
		}
		lastRole = role
		if role != "user" {
			continue
		}

		text, images, toolResults, _, _ := extractClaudeUserContent(msg.Content)
		if normalizeUserContent(text, len(images) > 0) != "" || len(toolResults) > 0 {
			hasUserContext = true
		}
	}

	if lastRole == "assistant" {
		last := req.Messages[len(req.Messages)-1]
		if finalAssistantMessageHasToolUse(last.Content) {
			return "assistant-prefill tool_use final message is not supported; last message must be user or assistant text prefill"
		}
		text, _ := extractClaudeAssistantContent(last.Content)
		if strings.TrimSpace(text) == "" {
			return "assistant-prefill final message must contain text"
		}
	}
	if !hasUserContext {
		return "at least one non-empty user message is required"
	}
	return ""
}

func validateClaudeThinkingConfig(thinking *ClaudeThinkingConfig, maxTokens int) string {
	return validateClaudeThinkingConfigWithOptions(thinking, maxTokens, true)
}

func validateClaudeThinkingConfigWithOptions(thinking *ClaudeThinkingConfig, maxTokens int, maxTokensPresent bool) string {
	if thinking == nil {
		return ""
	}

	kind := strings.ToLower(strings.TrimSpace(thinking.Type))
	switch kind {
	case "enabled":
		if maxTokensPresent && maxTokens == 0 {
			return "thinking.type enabled cannot be used with max_tokens=0"
		}
		if thinking.BudgetTokens <= 0 {
			return "thinking.budget_tokens is required when thinking.type is enabled"
		}
		if thinking.BudgetTokens < 1024 {
			return "thinking.budget_tokens must be at least 1024"
		}
		if maxTokens > 0 && thinking.BudgetTokens >= maxTokens {
			return "thinking.budget_tokens must be less than max_tokens"
		}
	case "adaptive":
		if thinking.BudgetTokens != 0 {
			return "thinking.budget_tokens is not supported when thinking.type is adaptive"
		}
	case "disabled":
		if thinking.BudgetTokens != 0 {
			return "thinking.budget_tokens is not supported when thinking.type is disabled"
		}
	default:
		return "thinking.type must be one of: enabled, adaptive, disabled"
	}

	display := strings.ToLower(strings.TrimSpace(thinking.Display))
	if display != "" && display != "summarized" && display != "omitted" {
		return "thinking.display must be one of: summarized, omitted"
	}
	if kind == "disabled" && display != "" {
		return "thinking.display is not supported when thinking.type is disabled"
	}

	return ""
}

type claudeThinkingResponseOptions struct {
	Format      string
	OmitDisplay bool
}

func resolveClaudeThinkingResponseOptions(thinking *ClaudeThinkingConfig, defaultFormat string) claudeThinkingResponseOptions {
	opts := claudeThinkingResponseOptions{Format: defaultFormat}
	if opts.Format == "" {
		opts.Format = "thinking"
	}
	if thinking == nil {
		return opts
	}

	display := strings.ToLower(strings.TrimSpace(thinking.Display))
	switch display {
	case "summarized":
		opts.Format = "thinking"
	case "omitted":
		opts.Format = "thinking"
		opts.OmitDisplay = true
	}

	return opts
}

type opus47NormalizationMetadata struct {
	Opus47             bool
	ThinkingNormalized bool
	SamplingDropped    bool
}

func normalizeOpus47ClaudeRequest(req *ClaudeRequest, claudeCodeCompatible bool) opus47NormalizationMetadata {
	meta := opus47NormalizationMetadata{}
	if req == nil {
		return meta
	}
	mapped, suffixThinking := ParseModelAndThinking(req.Model, configuredThinkingSuffix())
	if !isOpus47RequestModel(mapped) {
		return meta
	}
	req.Model = mapped
	meta.Opus47 = true
	if req.Temperature != 0 {
		req.Temperature = 0
		meta.SamplingDropped = true
	}
	if req.TopP != 0 {
		req.TopP = 0
		meta.SamplingDropped = true
	}
	if claudeCodeCompatible || suffixThinking || isClaudeThinkingRequested(req.Thinking) {
		display := ""
		if req.Thinking != nil {
			display = req.Thinking.Display
		}
		if req.Thinking == nil ||
			strings.ToLower(strings.TrimSpace(req.Thinking.Type)) != "adaptive" ||
			req.Thinking.BudgetTokens != 0 {
			req.Thinking = &ClaudeThinkingConfig{Type: "adaptive", Display: display}
			meta.ThinkingNormalized = true
		}
	}
	return meta
}

func configuredThinkingSuffix() (suffix string) {
	suffix = "-thinking"
	defer func() {
		if recover() != nil || strings.TrimSpace(suffix) == "" {
			suffix = "-thinking"
		}
	}()
	return config.GetThinkingConfig().Suffix
}

func safeThinkingConfig() (cfg config.ThinkingConfig) {
	cfg = config.ThinkingConfig{
		Suffix:       "-thinking",
		OpenAIFormat: "reasoning_content",
		ClaudeFormat: "thinking",
	}
	defer func() {
		if recover() != nil {
			cfg = config.ThinkingConfig{
				Suffix:       "-thinking",
				OpenAIFormat: "reasoning_content",
				ClaudeFormat: "thinking",
			}
			return
		}
		if strings.TrimSpace(cfg.Suffix) == "" {
			cfg.Suffix = "-thinking"
		}
		if strings.TrimSpace(cfg.OpenAIFormat) == "" {
			cfg.OpenAIFormat = "reasoning_content"
		}
		if strings.TrimSpace(cfg.ClaudeFormat) == "" {
			cfg.ClaudeFormat = "thinking"
		}
	}()
	return config.GetThinkingConfig()
}

func validateOpenAIRequestShape(req *OpenAIRequest) string {
	if len(req.Messages) == 0 {
		return "messages must not be empty"
	}

	hasNonSystem := false
	hasUserContext := false
	hasToolContext := false
	lastRole := ""
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			continue
		}
		if role != "system" {
			hasNonSystem = true
			lastRole = role
		}

		if role != "user" {
			if role == "tool" && strings.TrimSpace(extractOpenAIMessageText(msg.Content)) != "" && strings.TrimSpace(msg.ToolCallID) != "" {
				hasToolContext = true
			}
			continue
		}
		text, images := extractOpenAIUserContent(msg.Content)
		if normalizeUserContent(text, len(images) > 0) != "" {
			hasUserContext = true
		}
	}

	if !hasNonSystem {
		return "at least one non-system message is required"
	}
	if lastRole == "assistant" {
		return "assistant-prefill final message is not supported; last message must be user or tool"
	}
	if !hasUserContext && !hasToolContext {
		return "at least one non-empty user or tool message is required"
	}
	return ""
}

func classifyFailureReason(err error) pool.FailureReason {
	return pool.ClassifyFailureReason(err)
}

type rateLimitResetError interface {
	error
	RateLimitResetAt() time.Time
}

func (h *Handler) recordAccountFailure(accountID string, err error) pool.FailureReason {
	reason := classifyFailureReason(err)
	if h.pool == nil {
		return reason
	}
	if reason == pool.FailureReasonRateLimited || reason == pool.FailureReasonTemporaryLimited {
		if resetErr, ok := err.(rateLimitResetError); ok {
			h.pool.RecordFailureUntil(accountID, reason, resetErr.RateLimitResetAt())
			return reason
		}
	}
	h.pool.RecordFailure(accountID, reason)
	return reason
}

func (h *Handler) recordAccountModelFailure(accountID, model string, err error) pool.FailureReason {
	reason := h.recordAccountFailure(accountID, err)
	if h.pool != nil {
		h.pool.RecordModelFailure(accountID, model, reason, rateLimitResetFromError(err))
	}
	return reason
}

func (h *Handler) updateRequestLogUpstreamAccount(r *http.Request, accountID, region string) {
	if h == nil || h.pool == nil {
		updateRequestLogUpstream(r, accountID, region)
		return
	}
	health := h.pool.GetRuntimeHealth(accountID)
	updateRequestLogUpstream(r, accountID, region, AccountRequestHealthSnapshot{
		ActiveConnections: health.ActiveConnections,
		RecentFailures:    health.RecentFailures,
		RecentSuccesses:   health.RecentSuccesses,
		AvgLatencyMS:      health.AvgLatencyMS,
		Score:             health.Score,
	})
}

func (h *Handler) updateRequestLogRoutingDecision(r *http.Request, model string, attempt int, account *config.Account) {
	if account == nil {
		return
	}
	strategy := config.GetLoadBalanceConfig().Strategy
	if strings.TrimSpace(strategy) == "" {
		strategy = string(pool.StrategyHealth)
	}
	pressure := modelAdmissionGate.hasPressure(model)
	health := pool.RuntimeHealth{}
	if h != nil && h.pool != nil {
		health = h.pool.GetRuntimeHealth(account.ID)
	}
	decision := fmt.Sprintf("selected account=%s model=%s region=%s attempt=%d strategy=%s active=%d score=%d pressure=%t",
		account.ID,
		model,
		resolveAccountKiroRegion(account),
		attempt+1,
		strategy,
		health.ActiveConnections,
		health.Score,
		pressure,
	)
	updateRequestLogRouting(r, decision, strategy, pressure)
}

func rateLimitResetFromError(err error) time.Time {
	if resetErr, ok := err.(rateLimitResetError); ok {
		return resetErr.RateLimitResetAt()
	}
	return time.Time{}
}

func rateLimitRetryAfterHeaders(reason pool.FailureReason, resetAt time.Time) http.Header {
	headers := http.Header{}
	var delay time.Duration
	if resetAt.After(time.Now()) {
		delay = time.Until(resetAt)
	}
	if reason == pool.FailureReasonTemporaryLimited {
		if floor := pool.TemporaryLimitRetryAfterFloor(); delay < floor {
			delay = floor
		}
	}
	if delay <= 0 {
		return headers
	}
	seconds := int((delay + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	headers.Set("Retry-After", strconv.Itoa(seconds))
	return headers
}

type poolTemporaryLimitError struct {
	model   string
	resetAt time.Time
}

func (e *poolTemporaryLimitError) Error() string {
	return fmt.Sprintf("No available accounts for %s: upstream temporary limits are cooling down (TEMPORARY_LIMITED)", e.model)
}

func (e *poolTemporaryLimitError) RateLimitResetAt() time.Time {
	return e.resetAt
}

func (h *Handler) sendNoAvailableAccountsError(w http.ResponseWriter, model string, lastErr error, claude bool) {
	if lastErr == nil && h != nil && h.pool != nil {
		if model != "" {
			if err := h.modelBlockedError(model); err != nil {
				lastErr = err
			}
		}
	}
	if claude {
		if lastErr != nil {
			status, errType := claudeUpstreamErrorStatusAndType(lastErr)
			h.sendClaudeUpstreamError(w, status, errType, lastErr.Error(), lastErr)
			return
		}
		h.sendClaudeError(w, http.StatusServiceUnavailable, "api_error", "No available accounts")
		return
	}

	if lastErr != nil {
		status, errType := openAIUpstreamErrorStatusAndType(lastErr)
		h.sendOpenAIError(w, status, errType, lastErr.Error())
		return
	}
	h.sendOpenAIError(w, http.StatusServiceUnavailable, "server_error", "No available accounts")
}

func (h *Handler) modelBlockedError(model string) error {
	state := h.pool.ModelBlockState(model, time.Now())
	if state.AccountsEvaluated == 0 || state.Blocked == 0 || !state.AllBlocked {
		return nil
	}
	reason := state.LastReason
	switch reason {
	case pool.FailureReasonTemporaryLimited:
		return &poolTemporaryLimitError{model: model, resetAt: state.RetryAt}
	case pool.FailureReasonRateLimited:
		return &rateLimitError{
			endpoint: "Kiro account pool",
			body:     fmt.Sprintf(`{"message":"No available accounts for %s: upstream rate limits are cooling down","reason":"RATE_LIMITED"}`, model),
			resetAt:  state.RetryAt,
		}
	case pool.FailureReasonQuotaExhausted:
		return fmt.Errorf("No available accounts for %s: all evaluated accounts are quota exhausted", model)
	case pool.FailureReasonSuspended:
		return fmt.Errorf("No available accounts for %s: all evaluated accounts are suspended", model)
	case pool.FailureReasonAuthExpired:
		return fmt.Errorf("No available accounts for %s: all evaluated accounts require token refresh", model)
	default:
		return fmt.Errorf("No available accounts for %s: all evaluated accounts are cooling down (%s)", model, reason)
	}
}

func (h *Handler) waitForRecoverablePoolBlock(model string, deadline time.Time) bool {
	if h == nil || h.pool == nil || model == "" {
		return false
	}
	state := h.pool.ModelBlockState(model, time.Now())
	if state.AccountsEvaluated == 0 || state.Blocked == 0 || !state.AllBlocked || state.RetryAt.IsZero() {
		return false
	}
	switch state.LastReason {
	case pool.FailureReasonRateLimited, pool.FailureReasonModelCapacity, pool.FailureReasonTransientNetwork, pool.FailureReasonUpstream5xx:
	default:
		return false
	}
	delay := time.Until(state.RetryAt)
	if delay < 0 {
		delay = 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || delay > remaining {
		return false
	}
	sleepForOpusCapacityRetry(delay)
	return true
}

func shouldRetryAccount(reason pool.FailureReason, attempt int) bool {
	switch reason {
	case pool.FailureReasonQuotaExhausted, pool.FailureReasonRateLimited, pool.FailureReasonTemporaryLimited, pool.FailureReasonTransientNetwork, pool.FailureReasonUpstream5xx:
		return true
	default:
		return false
	}
}

func isOpus47Model(model string) bool {
	normalized := strings.TrimSpace(model)
	if suffix := strings.TrimSpace(configuredThinkingSuffix()); suffix != "" {
		normalized = strings.TrimSuffix(normalized, suffix)
	}
	return isOpus47RequestModel(normalized)
}

func requestStickyKey(r *http.Request, req *ClaudeRequest) string {
	if r != nil {
		sessionID := firstNonEmptyHeader(r, "x-claude-code-session-id", "x-claude-session-id", "claude-code-session-id")
		if sessionID != "" {
			agentID := firstNonEmptyHeader(r, "x-claude-code-agent-id", "x-claude-agent-id")
			if agentID != "" {
				return sessionID + "/" + agentID
			}
			return sessionID
		}
		for _, name := range []string{
			"x-request-id",
			"request-id",
		} {
			if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
				return value
			}
		}
	}
	return ""
}

func isOpus47CapacityLimit(err error, model string) bool {
	if err == nil || !isOpus47Model(model) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "insufficient_model_capacity") ||
		strings.Contains(msg, "experiencing high traffic") ||
		strings.Contains(msg, "model capacity")
}

func shouldWaitAndRetryOpus47(err error, model string) bool {
	if err == nil || !isOpus47Model(model) {
		return false
	}
	reason := classifyFailureReason(err)
	switch reason {
	case pool.FailureReasonQuotaExhausted, pool.FailureReasonAuthExpired, pool.FailureReasonSuspended, pool.FailureReasonTemporaryLimited:
		return false
	case pool.FailureReasonRateLimited, pool.FailureReasonModelCapacity:
		return true
	default:
		return isOpus47CapacityLimit(err, model)
	}
}

func opusCapacityRetryDelay(err error, deadline time.Time) (time.Duration, bool) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, false
	}

	minDelay := time.Duration(defaultRateLimitFallbackSeconds) * time.Second
	delay := minDelay
	if resetErr, ok := err.(rateLimitResetError); ok {
		resetDelay := time.Until(resetErr.RateLimitResetAt())
		if resetDelay < 0 {
			resetDelay = 0
		}
		delay = resetDelay
	}
	if delay < minDelay {
		delay = minDelay
	}
	if delay > remaining {
		delay = remaining
	}
	return delay, true
}

func NewHandler() *Handler {
	// 启动时应用代理配置
	applyProxyConfig(config.GetProxyURL())
	applyModelAdmissionConfig()

	totalReq, successReq, failedReq, totalTokens, totalCredits := config.GetStats()
	h := &Handler{
		pool:               pool.GetPool(),
		totalRequests:      int64(totalReq),
		successRequests:    int64(successReq),
		failedRequests:     int64(failedReq),
		totalTokens:        int64(totalTokens),
		totalCredits:       totalCredits,
		startTime:          time.Now().Unix(),
		stopRefresh:        make(chan struct{}),
		stopStatsSaver:     make(chan struct{}),
		autoRefreshUpdated: make(chan struct{}, 1),
		healthCheckUpdated: make(chan struct{}, 1),
		promptCache:        newPromptCacheTracker(defaultPromptCacheTTL),
		requestLogs:        newRequestLogStore(defaultRequestLogCapacity),
		responses:          make(map[string]responsesSession),
	}
	// 启动后台刷新
	go h.backgroundRefresh()
	go h.backgroundHealthCheck()
	// 启动后台统计保存 (每30秒保存一次)
	go h.backgroundStatsSaver()
	return h
}

func applyOpus47AdmissionConfig() {
	applyModelAdmissionConfig()
}

func applyModelAdmissionConfig() {
	admission := config.GetOpus47AdmissionConfig()
	opus47AdmissionGate = newOpus47Gate(admission.MaxConcurrent, admission.MaxWaiting)
	modelAdmission := config.GetModelAdmissionConfig()
	modelAdmissionGate = newModelAdmissionGateSet(modelAdmission)
	logger.Infof("[ModelAdmission] default=%d/%d models=%d legacyOpus47=%d/%d",
		modelAdmission.Default.MaxConcurrent,
		modelAdmission.Default.MaxWaiting,
		len(modelAdmission.Models),
		admission.MaxConcurrent,
		admission.MaxWaiting,
	)
	pool.GetPool().SetStrategy(pool.Strategy(config.GetLoadBalanceConfig().Strategy))
}

// backgroundRefresh 后台定时刷新账户信息
func (h *Handler) backgroundRefresh() {
	modelTicker := time.NewTicker(30 * time.Minute)
	defer modelTicker.Stop()

	h.refreshModelsCache()
	h.scheduleNextAutoRefresh()

refreshLoop:
	for {
		settings := config.GetAutoRefreshConfig()
		if !settings.Enabled {
			timer := time.NewTimer(time.Minute)
			select {
			case <-timer.C:
				continue
			case <-modelTicker.C:
				stopTimer(timer)
				h.refreshModelsCache()
			case <-h.autoRefreshUpdated:
				stopTimer(timer)
				h.scheduleNextAutoRefresh()
				continue
			case <-h.stopRefresh:
				stopTimer(timer)
				return
			}
			continue
		}

		interval := time.Duration(settings.IntervalMinutes) * time.Minute
		timer := time.NewTimer(interval)
		for {
			select {
			case <-timer.C:
				h.runAutoRefresh()
				h.refreshModelsCache()
				continue refreshLoop
			case <-modelTicker.C:
				h.refreshModelsCache()
			case <-h.autoRefreshUpdated:
				stopTimer(timer)
				h.scheduleNextAutoRefresh()
				continue refreshLoop
			case <-h.stopRefresh:
				stopTimer(timer)
				return
			}
		}
	}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (h *Handler) scheduleNextAutoRefresh() {
	h.setNextAutoRefreshRun(computeNextRunAt(time.Now(), config.GetAutoRefreshConfig()))
}

func (h *Handler) notifyAutoRefreshUpdated() {
	if h.autoRefreshUpdated == nil {
		return
	}
	select {
	case h.autoRefreshUpdated <- struct{}{}:
	default:
	}
}

func (h *Handler) runAutoRefresh() {
	settings := config.GetAutoRefreshConfig()
	now := time.Now()
	if !settings.Enabled {
		h.setNextAutoRefreshRun(0)
		return
	}
	if !h.tryBeginAutoRefresh(now.Unix()) {
		h.scheduleNextAutoRefresh()
		return
	}

	accounts := selectAutoRefreshAccounts(config.GetAccounts(), settings.Scope)
	result := runRefreshBatch(accounts, func(account *config.Account) error {
		_, err := refreshAccountData(account)
		if err != nil {
			logger.Warnf("[AutoRefresh] Failed to refresh %s: %v", account.Email, err)
		}
		return err
	})
	h.pool.Reload()

	finishedAt := time.Now()
	nextSettings := config.GetAutoRefreshConfig()
	h.finishAutoRefresh(result, finishedAt.Unix(), computeNextRunAt(finishedAt, nextSettings))
	logger.Infof("[AutoRefresh] Completed: success=%d failed=%d", result.Success, result.Failed)
}

func (h *Handler) backgroundHealthCheck() {
	h.scheduleNextHealthCheck()

healthLoop:
	for {
		settings := config.GetHealthCheckConfig()
		if !settings.Enabled {
			timer := time.NewTimer(time.Minute)
			select {
			case <-timer.C:
				continue
			case <-h.healthCheckUpdated:
				stopTimer(timer)
				h.scheduleNextHealthCheck()
				continue
			case <-h.stopRefresh:
				stopTimer(timer)
				return
			}
			continue
		}

		interval := time.Duration(settings.IntervalMinutes) * time.Minute
		timer := time.NewTimer(interval)
		for {
			select {
			case <-timer.C:
				h.runHealthCheck()
				continue healthLoop
			case <-h.healthCheckUpdated:
				stopTimer(timer)
				h.scheduleNextHealthCheck()
				continue healthLoop
			case <-h.stopRefresh:
				stopTimer(timer)
				return
			}
		}
	}
}

func (h *Handler) scheduleNextHealthCheck() {
	h.setNextHealthCheckRun(computeNextHealthCheckRunAt(time.Now(), config.GetHealthCheckConfig()))
}

func (h *Handler) notifyHealthCheckUpdated() {
	if h.healthCheckUpdated == nil {
		return
	}
	select {
	case h.healthCheckUpdated <- struct{}{}:
	default:
	}
}

func (h *Handler) runHealthCheck() {
	settings := config.GetHealthCheckConfig()
	now := time.Now()
	if !settings.Enabled {
		h.setNextHealthCheckRun(0)
		return
	}
	if !h.tryBeginHealthCheck(now.Unix()) {
		h.scheduleNextHealthCheck()
		return
	}

	accounts := selectHealthCheckAccounts(config.GetAccounts())
	result := runHealthCheckBatch(accounts, settings.AutoDisableUnhealthy, func(account *config.Account) error {
		err := h.checkAccountHealth(account)
		if err != nil {
			logger.Warnf("[HealthCheck] Account %s unhealthy: %v", account.Email, err)
		}
		return err
	}, disableUnhealthyAccount, now.Unix())

	if result.Disabled > 0 {
		h.pool.Reload()
	}

	finishedAt := time.Now()
	nextSettings := config.GetHealthCheckConfig()
	h.finishHealthCheck(result, finishedAt.Unix(), computeNextHealthCheckRunAt(finishedAt, nextSettings))
	logger.Infof("[HealthCheck] Completed: success=%d failed=%d disabled=%d", result.Success, result.Failed, result.Disabled)
}

func (h *Handler) checkAccountHealth(account *config.Account) error {
	startedAt := time.Now()
	if err := ensureValidTokenForHealthCheck(h, account); err != nil {
		if h.pool != nil {
			h.recordAccountFailure(account.ID, err)
		}
		return err
	}
	if _, err := listAvailableModelsForHealthCheck(account); err != nil {
		if h.pool != nil {
			h.recordAccountFailure(account.ID, err)
		}
		return err
	}
	if h.pool != nil {
		h.pool.RecordSuccessWithLatency(account.ID, time.Since(startedAt))
	}
	return nil
}

// validateApiKey 验证 API Key
func (h *Handler) validateApiKey(r *http.Request) bool {
	if !config.IsApiKeyRequired() {
		return true
	}

	allowedKeys := config.GetClientApiKeys()
	if len(allowedKeys) == 0 {
		return true
	}

	// 从 Authorization 头或 X-Api-Key 头获取
	authHeader := r.Header.Get("Authorization")
	apiKeyHeader := r.Header.Get("X-Api-Key")

	var providedKey string
	if strings.HasPrefix(authHeader, "Bearer ") {
		providedKey = strings.TrimPrefix(authHeader, "Bearer ")
	} else if apiKeyHeader != "" {
		providedKey = apiKeyHeader
	}

	for _, allowedKey := range allowedKeys {
		if providedKey == allowedKey {
			return true
		}
	}
	return false
}

func (h *Handler) validateClientAccess(r *http.Request) bool {
	return config.IsClientIPAllowed(r.RemoteAddr) && h.validateApiKey(r)
}

// ServeHTTP 路由分发
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	logCtx, loggedReq, responseRecorder, loggedWriter := h.beginRequestLog(w, r)
	if responseRecorder != nil {
		defer h.finishRequestLog(logCtx, responseRecorder)
	}
	r = loggedReq
	w = loggedWriter

	// Debug-level request trace for fine-grained visibility
	logger.Debugf("[HTTP] %s %s from %s", r.Method, path, r.RemoteAddr)

	// CORS - 完整的头部支持
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key, anthropic-version, anthropic-beta, x-api-key, x-stainless-os, x-stainless-lang, x-stainless-package-version, x-stainless-runtime, x-stainless-runtime-version, x-stainless-arch")
	w.Header().Set("Access-Control-Expose-Headers", "x-request-id, x-ratelimit-limit-requests, x-ratelimit-limit-tokens, x-ratelimit-remaining-requests, x-ratelimit-remaining-tokens, x-ratelimit-reset-requests, x-ratelimit-reset-tokens")

	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}

	// 路由
	switch {
	// API 端点（需要验证 API Key）
	case path == "/v1/messages" || path == "/messages" || path == "/anthropic/v1/messages":
		if !h.validateClientAccess(r) {
			h.sendClaudeError(w, 401, "authentication_error", "Invalid or missing API key")
			return
		}
		h.handleClaudeMessages(w, r)
	case path == "/v1/messages/count_tokens" || path == "/messages/count_tokens":
		if !h.validateClientAccess(r) {
			h.sendClaudeError(w, 401, "authentication_error", "Invalid or missing API key")
			return
		}
		h.handleCountTokens(w, r)
	case path == "/v1/chat/completions" || path == "/chat/completions":
		if !h.validateClientAccess(r) {
			h.sendOpenAIError(w, 401, "authentication_error", "Invalid or missing API key")
			return
		}
		h.handleOpenAIChat(w, r)
	case path == "/v1/responses" || path == "/responses":
		if !h.validateClientAccess(r) {
			h.sendOpenAIError(w, 401, "authentication_error", "Invalid or missing API key")
			return
		}
		h.handleOpenAIResponses(w, r)
	case path == "/v1/models" || path == "/models":
		h.handleModels(w, r)
	case path == "/api/event_logging/batch":
		// Claude Code 遥测端点 - 直接返回 200 OK
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(`{"status":"ok"}`))
	case path == "/favicon.ico":
		h.serveFavicon(w, r)

	// 管理端点
	case path == "/admin" || path == "/admin/":
		h.serveAdminPage(w, r)
	case strings.HasPrefix(path, "/admin/api/"):
		h.handleAdminAPI(w, r)
	case strings.HasPrefix(path, "/admin/"):
		h.serveStaticFile(w, r)

	// 健康检查
	case path == "/health" || path == "/":
		h.handleHealth(w, r)

	// 统计端点（需要 API Key 鉴权）
	case path == "/v1/stats":
		if !h.validateClientAccess(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid or missing API key"})
			return
		}
		h.handleStats(w, r)

	default:
		http.Error(w, "Not Found", 404)
	}
}

// handleHealth 健康检查（不暴露统计数据）
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"version": config.Version,
		"uptime":  time.Now().Unix() - h.startTime,
	})
}

// handleStats 统计数据（需要 API Key 鉴权）
func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "ok",
		"version":         config.Version,
		"accounts":        h.pool.Count(),
		"available":       h.pool.AvailableCount(),
		"totalRequests":   atomic.LoadInt64(&h.totalRequests),
		"successRequests": atomic.LoadInt64(&h.successRequests),
		"failedRequests":  atomic.LoadInt64(&h.failedRequests),
		"totalTokens":     atomic.LoadInt64(&h.totalTokens),
		"totalCredits":    h.getCredits(),
		"uptime":          time.Now().Unix() - h.startTime,
	})
}

// handleModels 模型列表
func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	// 尝试用缓存的真实模型列表
	h.modelsCacheMu.RLock()
	cached := h.cachedModels
	h.modelsCacheMu.RUnlock()
	if len(cached) == 0 {
		h.refreshModelsCache()
		h.modelsCacheMu.RLock()
		cached = h.cachedModels
		h.modelsCacheMu.RUnlock()
	}

	thinkingSuffix := safeThinkingConfig().Suffix

	models := buildAnthropicModelsResponse(cached, thinkingSuffix)
	if len(models) == 0 {
		models = fallbackAnthropicModels(thinkingSuffix)
	}

	// 添加别名模型
	models = append(models,
		buildModelInfo("auto", "kiro-proxy", true),
		buildModelInfo("gpt-4o", "kiro-proxy", true),
		buildModelInfo("gpt-4", "kiro-proxy", true),
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
	return
}

func buildAnthropicModelsResponse(cached []ModelInfo, thinkingSuffix string) []map[string]interface{} {
	if len(cached) == 0 {
		return nil
	}

	models := make([]map[string]interface{}, 0, len(cached)*2)
	if len(cached) > 0 {
		for _, m := range cached {
			supportsImage := modelSupportsImage(m.InputTypes)
			models = append(models, buildModelInfo(m.ModelId, "anthropic", supportsImage))
			// 自动生成 thinking 变体
			if thinkingSuffix != "" {
				models = append(models, buildModelInfo(m.ModelId+thinkingSuffix, "anthropic", supportsImage))
			}
		}
	}
	return appendOfficialModelAliases(models, thinkingSuffix)
}

func fallbackAnthropicModels(thinkingSuffix string) []map[string]interface{} {
	models := []map[string]interface{}{
		buildModelInfo("claude-sonnet-4.6", "anthropic", true),
		buildModelInfo("claude-sonnet-4.6"+thinkingSuffix, "anthropic", true),
		buildModelInfo("claude-opus-4.6", "anthropic", true),
		buildModelInfo("claude-opus-4.6"+thinkingSuffix, "anthropic", true),
		buildModelInfo("claude-opus-4.7", "anthropic", true),
		buildModelInfo("claude-opus-4.7"+thinkingSuffix, "anthropic", true),
		buildModelInfo("claude-sonnet-4.5", "anthropic", true),
		buildModelInfo("claude-sonnet-4.5"+thinkingSuffix, "anthropic", true),
		buildModelInfo("claude-sonnet-4", "anthropic", true),
		buildModelInfo("claude-sonnet-4"+thinkingSuffix, "anthropic", true),
		buildModelInfo("claude-haiku-4.5", "anthropic", true),
		buildModelInfo("claude-haiku-4.5"+thinkingSuffix, "anthropic", true),
		buildModelInfo("claude-opus-4.5", "anthropic", true),
		buildModelInfo("claude-opus-4.5"+thinkingSuffix, "anthropic", true),
	}
	return appendOfficialModelAliases(models, thinkingSuffix)
}

func appendOfficialModelAliases(models []map[string]interface{}, thinkingSuffix string) []map[string]interface{} {
	ids := make(map[string]bool, len(models)+2)
	for _, model := range models {
		id, _ := model["id"].(string)
		if id != "" {
			ids[id] = true
		}
	}

	appendAlias := func(sourceID, aliasID string) {
		if !ids[sourceID] || ids[aliasID] {
			return
		}
		models = append(models, buildModelInfo(aliasID, "anthropic", true))
		ids[aliasID] = true
	}

	appendAlias("claude-opus-4.7", "claude-opus-4-7")
	if thinkingSuffix != "" {
		appendAlias("claude-opus-4.7"+thinkingSuffix, "claude-opus-4-7"+thinkingSuffix)
	}
	return models
}

func modelSupportsImage(inputTypes []string) bool {
	for _, t := range inputTypes {
		lt := strings.ToLower(t)
		if strings.Contains(lt, "image") || strings.Contains(lt, "vision") {
			return true
		}
	}
	return false
}

func buildModelInfo(id, ownedBy string, supportsImage bool) map[string]interface{} {
	return map[string]interface{}{
		"id":           id,
		"type":         "model",
		"object":       "model",
		"display_name": id,
		"created_at":   int64(0),
		"owned_by":     ownedBy,
	}
}

// refreshModelsCache 从 Kiro API 拉取模型列表并缓存
func (h *Handler) refreshModelsCache() {
	accounts := config.GetEnabledAccounts()
	if len(accounts) == 0 {
		return
	}

	aggregated := make([]ModelInfo, 0)
	for i := range accounts {
		account := &accounts[i]
		if err := h.ensureValidToken(account); err != nil {
			logger.Warnf("[ModelsCache] Skip %s token refresh failed: %v", account.Email, err)
			continue
		}

		models, err := ListAvailableModels(account)
		if err != nil {
			logger.Warnf("[ModelsCache] Failed to refresh for %s: %v", account.Email, err)
			continue
		}
		// 缓存每账号可用模型，用于路由时过滤
		modelIDs := make([]string, 0, len(models))
		for _, m := range models {
			modelIDs = append(modelIDs, m.ModelId)
		}
		h.pool.SetModelList(account.ID, modelIDs)
		aggregated = mergeUniqueModels(aggregated, models)
	}

	if len(aggregated) > 0 {
		h.modelsCacheMu.Lock()
		h.cachedModels = aggregated
		h.modelsCacheTime = time.Now().Unix()
		h.modelsCacheMu.Unlock()
		logger.Infof("[ModelsCache] Cached %d models", len(aggregated))
	}
}

// fetchAndCacheAccountModels 为单个账号拉取并写入模型缓存。
// 同时更新 pool 的路由缓存与全局聚合模型列表。
func (h *Handler) fetchAndCacheAccountModels(account *config.Account) error {
	if err := h.ensureValidToken(account); err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}
	models, err := ListAvailableModels(account)
	if err != nil {
		return err
	}
	modelIDs := make([]string, 0, len(models))
	for _, m := range models {
		modelIDs = append(modelIDs, m.ModelId)
	}
	h.pool.SetModelList(account.ID, modelIDs)

	// 合并到聚合缓存
	h.modelsCacheMu.Lock()
	h.cachedModels = mergeUniqueModels(h.cachedModels, models)
	h.modelsCacheTime = time.Now().Unix()
	h.modelsCacheMu.Unlock()

	logger.Infof("[ModelsCache] Refreshed %d models for account %s", len(models), account.Email)
	return nil
}

// apiRefreshAccountModels POST /admin/api/accounts/{id}/models/refresh
// 立即为指定账号拉取并更新模型路由缓存。
func (h *Handler) apiRefreshAccountModels(w http.ResponseWriter, r *http.Request, id string) {
	accounts := config.GetAccounts()
	var account *config.Account
	for i := range accounts {
		if accounts[i].ID == id {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}
	// 从 pool 取运行时最新 token（与 refreshModelsCache 逻辑一致）
	if latest := h.pool.GetByID(id); latest != nil {
		account.AccessToken = latest.AccessToken
		account.RefreshToken = latest.RefreshToken
		account.ExpiresAt = latest.ExpiresAt
		account.ProfileArn = latest.ProfileArn
	}
	if err := h.fetchAndCacheAccountModels(account); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   len(h.pool.GetModelList(id)),
	})
}

// apiRefreshAllAccountsModels POST /admin/api/accounts/models/refresh
// 直接复用 refreshModelsCache，为所有已启用账号刷新模型路由缓存。
func (h *Handler) apiRefreshAllAccountsModels(w http.ResponseWriter, r *http.Request) {
	h.refreshModelsCache()
	h.modelsCacheMu.RLock()
	cachedLen := len(h.cachedModels)
	h.modelsCacheMu.RUnlock()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"refreshed": cachedLen,
		"failed":    0,
	})
}

func mergeUniqueModels(existing []ModelInfo, incoming []ModelInfo) []ModelInfo {
	if len(incoming) == 0 {
		return existing
	}

	indexByID := make(map[string]int, len(existing))
	merged := make([]ModelInfo, len(existing))
	copy(merged, existing)
	for i, model := range merged {
		indexByID[strings.ToLower(strings.TrimSpace(model.ModelId))] = i
	}

	for _, model := range incoming {
		key := strings.ToLower(strings.TrimSpace(model.ModelId))
		if key == "" {
			continue
		}
		if idx, ok := indexByID[key]; ok {
			merged[idx] = mergeModelInfo(merged[idx], model)
			continue
		}
		indexByID[key] = len(merged)
		merged = append(merged, model)
	}

	return merged
}

func mergeModelInfo(base ModelInfo, extra ModelInfo) ModelInfo {
	if base.ModelName == "" {
		base.ModelName = extra.ModelName
	}
	if base.Description == "" {
		base.Description = extra.Description
	}
	if base.RateMultiplier == 0 {
		base.RateMultiplier = extra.RateMultiplier
	}
	if base.TokenLimits == nil {
		base.TokenLimits = extra.TokenLimits
	}
	base.InputTypes = mergeStringLists(base.InputTypes, extra.InputTypes)
	return base
}

func mergeStringLists(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base)+len(extra))
	merged := make([]string, 0, len(base)+len(extra))
	for _, item := range base {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, item)
	}
	for _, item := range extra {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, item)
	}
	return merged
}

// handleCountTokens Token 计数（Claude Code 会调用）
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method Not Allowed", 405)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", "Failed to read request body")
		return
	}

	var req ClaudeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", "Invalid JSON")
		return
	}
	updateRequestLogMetadata(r, req.Model, false)
	if msg := validateClaudeThinkingConfigWithOptions(req.Thinking, req.MaxTokens, claudeRequestHasMaxTokens(body)); msg != "" {
		h.sendClaudeError(w, 400, "invalid_request_error", msg)
		return
	}

	thinkingCfg := config.GetThinkingConfig()
	actualModel, thinking := resolveClaudeThinkingMode(req.Model, req.Thinking, thinkingCfg.Suffix)
	req.Model = actualModel
	effectiveReq := cloneClaudeRequestForThinking(&req, thinking)

	estimatedTokens := estimateClaudeRequestInputTokens(effectiveReq)
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}

	updateRequestLogCountTokensMode(r, "estimated")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Kiro-Go-Token-Count-Mode", "estimated")
	json.NewEncoder(w).Encode(map[string]int{"input_tokens": estimatedTokens})
}

// handleClaudeMessages Claude API 处理
func (h *Handler) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	h.handleClaudeMessagesInternal(w, r)
}

func (h *Handler) handleClaudeMessagesInternal(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method Not Allowed", 405)
		return
	}

	// 读取请求
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", "Failed to read request body")
		return
	}

	env, err := parseAnthropicEnvelope(r, body)
	if err != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", "Invalid JSON: "+err.Error())
		return
	}
	writeAnthropicRequestIDHeaders(w, env)
	req := env.Request
	updateRequestLogAnthropic(r, env)
	updateRequestLogMetadata(r, req.Model, req.Stream)
	opusMeta := normalizeOpus47ClaudeRequest(&req, env.HasBetaPrefix("claude-code") || env.SessionID != "" || env.AgentID != "" || env.ProjectDirPresent || env.Version != "")
	updateRequestLogOpus47Normalization(r, opusMeta)
	updateRequestLogMetadata(r, req.Model, req.Stream)
	maxTokensPresent := claudeRequestHasMaxTokens(body)
	if msg := validateClaudeRequestShapeWithOptions(&req, maxTokensPresent); msg != "" {
		h.sendClaudeError(w, 400, "invalid_request_error", msg)
		return
	}
	if msg := validateClaudeToolNames(req.Tools, req.ToolReferences); msg != "" {
		h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", msg)
		return
	}

	// 解析模型和 thinking 模式
	thinkingCfg := safeThinkingConfig()
	actualModel, thinking := resolveClaudeThinkingMode(req.Model, req.Thinking, thinkingCfg.Suffix)
	req.Model = actualModel
	effectiveReq := cloneClaudeRequestForThinking(&req, thinking)
	thinkingResponseOpts := resolveClaudeThinkingResponseOptions(req.Thinking, thinkingCfg.ClaudeFormat)
	estimatedInputTokens := estimateClaudeRequestInputTokens(effectiveReq)
	if len(effectiveReq.Messages) > 0 {
		last := effectiveReq.Messages[len(effectiveReq.Messages)-1]
		if strings.TrimSpace(last.Role) == "assistant" && !finalAssistantMessageHasToolUse(last.Content) {
			text, _ := extractClaudeAssistantContent(last.Content)
			if strings.TrimSpace(text) != "" {
				updateRequestLogAssistantPrefillMode(r, "emulated_text_prefill")
			}
		}
	}

	if req.MaxTokens == 0 && maxTokensPresent {
		mode := "local_zero_output"
		cacheCreationInputTokens := 0
		if claudeRequestHasCacheControl(effectiveReq) {
			mode = "cache_prewarm"
			cacheCreationInputTokens = estimatedInputTokens
		}
		updateRequestLogMaxTokensZeroMode(r, mode)
		updateRequestLogUsage(r, estimatedInputTokens, 0, 0, cacheCreationInputTokens)
		h.recordSuccess(estimatedInputTokens, 0, 0)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(ClaudeResponse{
			ID:           "msg_" + uuid.New().String(),
			Type:         "message",
			Role:         "assistant",
			Content:      []ClaudeContentBlock{},
			Model:        req.Model,
			StopReason:   "max_tokens",
			StopSequence: nil,
			Usage: ClaudeUsage{
				InputTokens:              estimatedInputTokens,
				OutputTokens:             0,
				CacheCreationInputTokens: cacheCreationInputTokens,
			},
		})
		return
	}

	if hasNativeClaudeWebSearch(req.Tools) {
		h.handleClaudeNativeWebSearch(w, r, &req, estimatedInputTokens)
		return
	}

	// 转换请求
	kiroPayload := ClaudeToKiro(&req, thinking)
	guardResult, guardErr := prepareGuardedKiroPayload(kiroPayload, defaultPayloadGuardOptions())
	updateRequestLogPayload(r, guardResult)
	if guardErr != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", guardErr.Error())
		return
	}
	h.handleClaudeWithAccountRetry(w, r, req.Stream, kiroPayload, req.Model, thinking, thinkingResponseOpts, effectiveReq, estimatedInputTokens)
}

func claudeRequestHasCacheControl(req *ClaudeRequest) bool {
	if req == nil {
		return false
	}
	if contentBlocksHaveCacheControl(req.System) {
		return true
	}
	for _, tool := range req.Tools {
		if len(tool.CacheControl) > 0 {
			return true
		}
	}
	for _, msg := range req.Messages {
		if contentBlocksHaveCacheControl(msg.Content) {
			return true
		}
	}
	return false
}

func contentBlocksHaveCacheControl(value interface{}) bool {
	if value == nil {
		return false
	}
	var decoded interface{}
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	switch v := decoded.(type) {
	case map[string]interface{}:
		return blockHasCacheControl(v)
	case []interface{}:
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if blockHasCacheControl(block) {
				return true
			}
		}
	}
	return false
}

func blockHasCacheControl(block map[string]interface{}) bool {
	_, ok := block["cache_control"]
	return ok
}

func claudeRequestHasMaxTokens(body []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	_, ok := raw["max_tokens"]
	return ok
}

func hasNativeClaudeWebSearch(tools []ClaudeTool) bool {
	for _, tool := range tools {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(tool.Type)), "web_search") {
			return true
		}
	}
	return false
}

func (h *Handler) handleClaudeNativeWebSearch(w http.ResponseWriter, r *http.Request, req *ClaudeRequest, estimatedInputTokens int) {
	query := extractClaudeWebSearchQuery(req.Messages)
	if query == "" {
		h.sendClaudeError(w, 400, "invalid_request_error", "Cannot extract search query from messages")
		return
	}

	used := make(map[string]bool)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		account := h.pool.GetNextForModelExcept(req.Model, used)
		if account == nil {
			if lastErr != nil {
				h.recordFailure()
				h.sendClaudeError(w, 503, "api_error", lastErr.Error())
			} else {
				h.recordFailure()
				h.sendClaudeError(w, 503, "api_error", "No available accounts")
			}
			return
		}
		used[account.ID] = true
		h.updateRequestLogUpstreamAccount(r, account.ID, resolveAccountKiroRegion(account))
		h.updateRequestLogRoutingDecision(r, req.Model, attempt, account)

		if err := h.ensureValidToken(account); err != nil {
			lastErr = err
			h.recordAccountFailure(account.ID, err)
			continue
		}

		releaseRequest := h.pool.BeginRequest(account.ID)
		startedAt := time.Now()
		toolUseID, results, err := callKiroMCPWebSearch(account, query)
		releaseRequest()
		if err != nil {
			lastErr = err
			h.recordAccountFailure(account.ID, err)
			if shouldRetryAccount(classifyFailureReason(err), attempt) {
				continue
			}
			h.recordFailure()
			h.sendClaudeError(w, 500, "api_error", err.Error())
			return
		}

		outputTokens := estimateClaudeOutputTokens(summarizeKiroWebSearchResults(query, results), "", nil)
		updateRequestLogUsage(r, estimatedInputTokens, outputTokens, 0, 0)
		h.recordSuccess(estimatedInputTokens, outputTokens, 0)
		h.pool.RecordSuccessWithLatency(account.ID, time.Since(startedAt))
		h.pool.UpdateStats(account.ID, estimatedInputTokens+outputTokens, 0)

		if req.Stream {
			h.sendClaudeNativeWebSearchStream(w, req.Model, query, toolUseID, results, estimatedInputTokens, outputTokens)
			return
		}
		h.sendClaudeNativeWebSearchResponse(w, req.Model, query, toolUseID, results, estimatedInputTokens, outputTokens)
		return
	}

	h.recordFailure()
	if lastErr != nil {
		h.sendClaudeError(w, 503, "api_error", lastErr.Error())
		return
	}
	h.sendClaudeError(w, 503, "api_error", "No available accounts")
}

func extractClaudeWebSearchQuery(messages []ClaudeMessage) string {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		text, _ := extractClaudeMessageText(msg.Content)
		if query := normalizeClaudeWebSearchQuery(text); query != "" {
			return query
		}
	}
	return ""
}

func extractClaudeMessageText(content interface{}) (string, bool) {
	switch v := content.(type) {
	case string:
		return v, true
	case []interface{}:
		var b strings.Builder
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if block["type"] == "text" {
				if text, ok := block["text"].(string); ok {
					if b.Len() > 0 {
						b.WriteString("\n")
					}
					b.WriteString(text)
				}
			}
		}
		return b.String(), b.Len() > 0
	default:
		return "", false
	}
}

func normalizeClaudeWebSearchQuery(text string) string {
	query := strings.TrimSpace(text)
	if query == "" {
		return ""
	}
	prefixes := []string{
		"Perform a web search for the query:",
		"Search the web for:",
		"Web search query:",
		"web_search:",
	}
	lower := strings.ToLower(query)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return strings.TrimSpace(query[len(prefix):])
		}
	}
	return query
}

type kiroMCPResponse struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type kiroWebSearchResults struct {
	Results []kiroWebSearchResult `json:"results"`
	Query   string                `json:"query,omitempty"`
}

type kiroWebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func callKiroMCPWebSearch(account *config.Account, query string) (string, *kiroWebSearchResults, error) {
	toolUseID := fmt.Sprintf("web_search_tooluse_%s_%d", uuid.New().String(), time.Now().UnixMilli())
	body := map[string]interface{}{
		"id":      toolUseID,
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "web_search",
			"arguments": map[string]interface{}{"query": query},
		},
	}
	reqBody, _ := json.Marshal(body)

	region := resolveAccountKiroRegion(account)
	endpoints := []string{
		fmt.Sprintf("https://q.%s.amazonaws.com/mcp", region),
		fmt.Sprintf("https://codewhisperer.%s.amazonaws.com/mcp", region),
	}
	var lastErr error
	for _, endpoint := range endpoints {
		httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if err != nil {
			lastErr = err
			continue
		}
		host := ""
		if parsed, err := url.Parse(endpoint); err == nil {
			host = parsed.Host
		}
		applyKiroBaseHeaders(httpReq, account, buildStreamingHeaderValues(account, host))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-amzn-codewhisperer-optout", "false")

		resp, err := GetClientForProxy(ResolveAccountProxyURL(account)).Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}
		respBody := readResponseBody(resp)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d from Kiro MCP: %s", resp.StatusCode, respBody)
			continue
		}

		var mcpResp kiroMCPResponse
		if err := json.Unmarshal([]byte(respBody), &mcpResp); err != nil {
			return "", nil, err
		}
		if mcpResp.Error != nil {
			return "", nil, fmt.Errorf("Kiro MCP error: %s", mcpResp.Error.Message)
		}
		if mcpResp.Result.IsError {
			return "", nil, fmt.Errorf("Kiro MCP web_search returned an error")
		}
		if len(mcpResp.Result.Content) == 0 || strings.TrimSpace(mcpResp.Result.Content[0].Text) == "" {
			return "", nil, fmt.Errorf("Kiro MCP web_search returned empty results")
		}
		var results kiroWebSearchResults
		if err := json.Unmarshal([]byte(mcpResp.Result.Content[0].Text), &results); err != nil {
			return "", nil, err
		}
		return toolUseID, &results, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("Kiro MCP web_search failed")
	}
	return "", nil, lastErr
}

func sendClaudeNativeWebSearchContent(model, query, toolUseID string, results *kiroWebSearchResults, inputTokens, outputTokens int) *ClaudeResponse {
	summary := summarizeKiroWebSearchResults(query, results)
	resp := &ClaudeResponse{
		ID:           "msg_" + uuid.New().String(),
		Type:         "message",
		Role:         "assistant",
		Model:        model,
		StopReason:   "end_turn",
		StopSequence: nil,
		Usage: ClaudeUsage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
	}
	resp.Content = []ClaudeContentBlock{
		{
			Type:  "server_tool_use",
			ID:    toolUseID,
			Name:  "web_search",
			Input: map[string]interface{}{"query": query},
		},
		{
			Type:      "web_search_tool_result",
			ToolUseID: toolUseID,
			Content:   buildClaudeWebSearchResultContent(results),
		},
		{Type: "text", Text: summary},
	}
	return resp
}

func buildClaudeWebSearchResultContent(results *kiroWebSearchResults) []map[string]interface{} {
	if results == nil {
		return nil
	}
	content := make([]map[string]interface{}, 0, len(results.Results))
	for _, result := range results.Results {
		content = append(content, map[string]interface{}{
			"type":              "web_search_result",
			"title":             result.Title,
			"url":               result.URL,
			"encrypted_content": result.Snippet,
			"page_age":          nil,
		})
	}
	return content
}

func summarizeKiroWebSearchResults(query string, results *kiroWebSearchResults) string {
	if results == nil || len(results.Results) == 0 {
		return fmt.Sprintf("<web_search>\nNo results found for %q.\n</web_search>", query)
	}
	var b strings.Builder
	b.WriteString("<web_search>\n")
	for i, result := range results.Results {
		if i >= 10 {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n%s\n%s\n", i+1, result.Title, result.URL, result.Snippet)
	}
	b.WriteString("</web_search>")
	return b.String()
}

func (h *Handler) sendClaudeNativeWebSearchResponse(w http.ResponseWriter, model, query, toolUseID string, results *kiroWebSearchResults, inputTokens, outputTokens int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(sendClaudeNativeWebSearchContent(model, query, toolUseID, results, inputTokens, outputTokens))
}

func (h *Handler) sendClaudeNativeWebSearchStream(w http.ResponseWriter, model, query, toolUseID string, results *kiroWebSearchResults, inputTokens, outputTokens int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.sendClaudeError(w, 500, "api_error", "Streaming not supported")
		return
	}
	resp := sendClaudeNativeWebSearchContent(model, query, toolUseID, results, inputTokens, outputTokens)
	h.sendSSE(w, flusher, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            resp.ID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]int{"input_tokens": inputTokens, "output_tokens": 0},
		},
	})
	for idx, block := range resp.Content {
		h.sendSSE(w, flusher, "content_block_start", map[string]interface{}{
			"type":          "content_block_start",
			"index":         idx,
			"content_block": block,
		})
		h.sendSSE(w, flusher, "content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": idx,
		})
	}
	h.sendSSE(w, flusher, "message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]int{"input_tokens": inputTokens, "output_tokens": outputTokens},
	})
	h.sendSSE(w, flusher, "message_stop", map[string]interface{}{"type": "message_stop"})
}

func (h *Handler) handleClaudeWithAccountRetry(w http.ResponseWriter, r *http.Request, stream bool, payload *KiroPayload, model string, thinking bool, thinkingResponseOpts claudeThinkingResponseOptions, effectiveReq *ClaudeRequest, estimatedInputTokens int) {
	deadline := time.Now().Add(opusCapacityRetryBudget)
	releaseGate, ok := h.acquireOpus47AdmissionForRequest(w, r, model, stream, true, deadline)
	if !ok {
		return
	}
	defer releaseGate()

	cacheProfile := h.promptCache.BuildClaudeProfile(effectiveReq, estimatedInputTokens)
	var lastErr error
	used := make(map[string]bool)
	attempt := 0
	capacityRetryCount := 0
	sessionKey := requestStickyKey(r, effectiveReq)

	for {
		updateRequestLogReliability(r, -1, attempt+1, 0, -1)
		account, releaseRequest := h.pool.BeginNextForModelSessionExcept(model, sessionKey, used)
		if account == nil {
			if h.waitForRecoverablePoolBlock(model, deadline) {
				capacityRetryCount++
				updateRequestLogCapacityRetryCount(r, capacityRetryCount)
				used = make(map[string]bool)
				attempt++
				continue
			}
			if shouldWaitAndRetryOpus47(lastErr, model) {
				if delay, ok := opusCapacityRetryDelay(lastErr, deadline); ok {
					capacityRetryCount++
					updateRequestLogCapacityRetryCount(r, capacityRetryCount)
					sleepForOpusCapacityRetry(delay)
					used = make(map[string]bool)
					continue
				}
				h.recordFailure()
				status, errType := claudeUpstreamErrorStatusAndType(lastErr)
				h.sendClaudeUpstreamError(w, status, errType, "Opus 4.7 upstream capacity did not recover within 90s: "+lastErr.Error(), lastErr)
				return
			}
			h.recordFailure()
			h.sendNoAvailableAccountsError(w, model, lastErr, true)
			return
		}
		used[account.ID] = true
		h.updateRequestLogUpstreamAccount(r, account.ID, resolveAccountKiroRegion(account))
		h.updateRequestLogRoutingDecision(r, model, attempt, account)

		attemptPayload := cloneKiroPayload(payload)
		if !h.finalizePayloadForClaudeAccount(w, r, account, attemptPayload) {
			releaseRequest()
			return
		}

		if err := h.ensureValidToken(account); err != nil {
			h.recordAccountModelFailure(account.ID, model, err)
			if attempt == 0 {
				releaseRequest()
				continue
			}
			releaseRequest()
			h.sendClaudeError(w, 503, "api_error", "Token refresh failed: "+err.Error())
			return
		}

		cacheUsage := h.promptCache.Compute(account.ID, cacheProfile)
		var err error
		var done bool
		if stream {
			done, err = h.handleClaudeStreamAttempt(w, r, account, attemptPayload, model, thinking, thinkingResponseOpts, estimatedInputTokens, cacheUsage, cacheProfile, used, attempt)
		} else {
			done, err = h.handleClaudeNonStreamAttempt(w, r, account, attemptPayload, model, thinking, thinkingResponseOpts, estimatedInputTokens, cacheUsage, cacheProfile, used, attempt)
		}
		releaseRequest()
		if done {
			return
		}
		lastErr = err
		if shouldWaitAndRetryOpus47(err, model) {
			attempt++
			continue
		}
		if err != nil && shouldRetryAccount(classifyFailureReason(err), attempt) {
			attempt++
			continue
		}
		if stream {
			if err != nil {
				h.recordFailure()
				status, errType := claudeUpstreamErrorStatusAndType(err)
				h.sendClaudeUpstreamError(w, status, errType, err.Error(), err)
			}
			return
		}
		attempt++
	}
}

func (h *Handler) handleOpenAIWithAccountRetry(w http.ResponseWriter, r *http.Request, stream bool, payload *KiroPayload, model string, thinking bool, estimatedInputTokens int) {
	deadline := time.Now().Add(opusCapacityRetryBudget)
	releaseGate, ok := h.acquireOpus47AdmissionForRequest(w, r, model, stream, false, deadline)
	if !ok {
		return
	}
	defer releaseGate()

	used := make(map[string]bool)
	var lastErr error
	attempt := 0
	capacityRetryCount := 0

	for {
		updateRequestLogReliability(r, -1, attempt+1, 0, -1)
		account, releaseRequest := h.pool.BeginNextForModelExcept(model, used)
		if account == nil {
			if h.waitForRecoverablePoolBlock(model, deadline) {
				capacityRetryCount++
				updateRequestLogCapacityRetryCount(r, capacityRetryCount)
				used = make(map[string]bool)
				attempt++
				continue
			}
			if shouldWaitAndRetryOpus47(lastErr, model) {
				if delay, ok := opusCapacityRetryDelay(lastErr, deadline); ok {
					capacityRetryCount++
					updateRequestLogCapacityRetryCount(r, capacityRetryCount)
					sleepForOpusCapacityRetry(delay)
					used = make(map[string]bool)
					continue
				}
				h.recordFailure()
				status, errType := openAIUpstreamErrorStatusAndType(lastErr)
				h.sendOpenAIError(w, status, errType, "Opus 4.7 upstream capacity did not recover within 90s: "+lastErr.Error())
				return
			}
			h.recordFailure()
			h.sendNoAvailableAccountsError(w, model, lastErr, false)
			return
		}
		used[account.ID] = true
		h.updateRequestLogUpstreamAccount(r, account.ID, resolveAccountKiroRegion(account))
		h.updateRequestLogRoutingDecision(r, model, attempt, account)

		attemptPayload := cloneKiroPayload(payload)
		if !h.finalizePayloadForOpenAIAccount(w, r, account, attemptPayload) {
			releaseRequest()
			return
		}

		if err := h.ensureValidToken(account); err != nil {
			h.recordAccountFailure(account.ID, err)
			if attempt == 0 {
				releaseRequest()
				continue
			}
			releaseRequest()
			h.sendOpenAIError(w, 503, "server_error", "Token refresh failed")
			return
		}

		var err error
		var done bool
		if stream {
			done, err = h.handleOpenAIStreamAttempt(w, r, account, attemptPayload, model, thinking, estimatedInputTokens, used, attempt)
		} else {
			done, err = h.handleOpenAINonStreamAttempt(w, r, account, attemptPayload, model, thinking, estimatedInputTokens, used, attempt)
		}
		releaseRequest()
		if done {
			return
		}
		lastErr = err
		if shouldWaitAndRetryOpus47(err, model) {
			attempt++
			continue
		}
		if err != nil && shouldRetryAccount(classifyFailureReason(err), attempt) {
			attempt++
			continue
		}
		if stream {
			if err != nil {
				h.recordFailure()
				status, errType := openAIUpstreamErrorStatusAndType(err)
				h.sendOpenAIError(w, status, errType, err.Error())
			}
			return
		}
		attempt++
	}
}

func (h *Handler) acquireOpus47Admission(w http.ResponseWriter, model string, stream bool, claudeFormat bool, deadline time.Time) (func(), bool) {
	if stream && modelAdmissionGate.shouldBypassStream(model) {
		return func() {}, true
	}
	timeout := time.Until(deadline)
	if timeout <= 0 {
		timeout = 0
	}
	release, gated, err := modelAdmissionGate.acquire(model, timeout)
	if !gated {
		return func() {}, true
	}
	if err == nil {
		return release, true
	}
	h.recordFailure()
	modelLabel := strings.TrimSpace(model)
	if modelLabel == "" {
		modelLabel = "model"
	}
	if claudeFormat {
		h.sendClaudeError(w, 503, "api_error", modelLabel+" concurrency queue timeout")
	} else {
		h.sendOpenAIError(w, 503, "server_error", modelLabel+" concurrency queue timeout")
	}
	return nil, false
}

func (h *Handler) acquireOpus47AdmissionForRequest(w http.ResponseWriter, r *http.Request, model string, stream bool, claudeFormat bool, deadline time.Time) (func(), bool) {
	startedAt := time.Now()
	release, ok := h.acquireOpus47Admission(w, model, stream, claudeFormat, deadline)
	wait := time.Since(startedAt)
	effectiveLimit, pressureScore := modelAdmissionGate.admissionMetrics(model)
	updateRequestLogAdmission(r, wait, effectiveLimit, pressureScore)
	if ok {
		updateRequestLogReliability(r, wait.Milliseconds(), 0, 0, -1)
	}
	return release, ok
}

func claudeUpstreamErrorStatusAndType(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "api_error"
	}
	var poolTempErr *poolTemporaryLimitError
	if errors.As(err, &poolTempErr) {
		return poolTemporaryLimitStatus, "rate_limit_error"
	}
	reason := classifyFailureReason(err)
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "http 400") || strings.Contains(msg, "bad request") || strings.Contains(msg, "improperly formed request") || strings.Contains(msg, "invalid request") {
		return http.StatusBadRequest, "invalid_request_error"
	}
	switch reason {
	case pool.FailureReasonTemporaryLimited:
		return poolTemporaryLimitStatus, "rate_limit_error"
	case pool.FailureReasonRateLimited, pool.FailureReasonModelCapacity:
		return http.StatusTooManyRequests, "rate_limit_error"
	case pool.FailureReasonTransientNetwork, pool.FailureReasonUpstream5xx:
		if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
			return http.StatusGatewayTimeout, "timeout_error"
		}
		return http.StatusServiceUnavailable, "overloaded_error"
	case pool.FailureReasonAuthExpired:
		return http.StatusUnauthorized, "authentication_error"
	case pool.FailureReasonQuotaExhausted:
		return http.StatusPaymentRequired, "billing_error"
	default:
		return http.StatusInternalServerError, "api_error"
	}
}

func openAIUpstreamErrorStatusAndType(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "server_error"
	}
	var poolTempErr *poolTemporaryLimitError
	if errors.As(err, &poolTempErr) {
		return poolTemporaryLimitStatus, "rate_limit_error"
	}
	reason := classifyFailureReason(err)
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "http 400") || strings.Contains(msg, "bad request") || strings.Contains(msg, "improperly formed request") || strings.Contains(msg, "invalid request") {
		return http.StatusBadRequest, "invalid_request_error"
	}
	switch reason {
	case pool.FailureReasonTemporaryLimited:
		return poolTemporaryLimitStatus, "rate_limit_error"
	case pool.FailureReasonRateLimited, pool.FailureReasonModelCapacity:
		return http.StatusTooManyRequests, "rate_limit_error"
	case pool.FailureReasonTransientNetwork, pool.FailureReasonUpstream5xx:
		if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
			return http.StatusGatewayTimeout, "server_error"
		}
		return http.StatusServiceUnavailable, "server_error"
	case pool.FailureReasonAuthExpired:
		return http.StatusUnauthorized, "authentication_error"
	case pool.FailureReasonQuotaExhausted:
		return http.StatusPaymentRequired, "billing_error"
	default:
		return http.StatusInternalServerError, "server_error"
	}
}

// handleClaudeStream Claude 流式响应
func (h *Handler) handleClaudeStreamAttempt(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload, model string, thinking bool, thinkingOpts claudeThinkingResponseOptions, estimatedInputTokens int, cacheUsage promptCacheUsage, cacheProfile *promptCacheProfile, used map[string]bool, attempt int) (bool, error) {
	if _, ok := w.(http.Flusher); !ok {
		h.sendClaudeError(w, 500, "api_error", "Streaming not supported")
		return true, fmt.Errorf("streaming not supported")
	}

	// 获取 thinking 输出格式配置
	thinkingFormat := thinkingOpts.Format

	msgID := "msg_" + uuid.New().String()
	var inputTokens, outputTokens int
	var credits float64
	var realInputTokens int
	var toolUses []KiroToolUse
	var rawContentBuilder strings.Builder
	var rawThinkingBuilder strings.Builder
	startInputTokens := estimatedInputTokens
	sse := newClaudeSSEWriter(w, msgID, model, buildClaudeUsageMap(startInputTokens, 0, cacheUsage, cacheProfile != nil), 4096)
	streamStarted := false
	upstreamStartedAt := time.Now()
	firstTokenRecorded := false

	recordFirstToken := func() {
		if firstTokenRecorded {
			return
		}
		firstTokenRecorded = true
		updateRequestLogReliability(r, -1, 0, time.Since(upstreamStartedAt).Milliseconds(), -1)
	}

	startMessage := func() {
		if streamStarted {
			return
		}
		streamStarted = true
		sse.Start()
	}

	closeActiveBlock := func() {
		sse.closeBlock()
	}

	startContentBlock := func(blockType string) {
		startMessage()
		if blockType == "thinking" {
			signature := generateClaudeThinkingSignature()
			sse.startBlock("thinking", map[string]string{"type": "thinking", "thinking": "", "signature": signature})
			sse.activeSignature = signature
		} else {
			sse.startBlock("text", map[string]string{"type": "text", "text": ""})
		}
	}

	sendTextDelta := func(text string) {
		if text == "" {
			return
		}
		startMessage()
		sse.TextDelta(text)
	}

	sendThinkingDelta := func(text string) {
		if text == "" {
			return
		}
		startMessage()
		sse.ThinkingDelta(text)
	}

	// Thinking 标签解析状态
	var textBuffer string
	var inThinkingBlock bool
	var dropTagThinking bool
	var thinkingSource thinkingStreamSource

	// 发送文本的辅助函数
	// thinkingState: 0=普通内容, 1=thinking开始, 2=thinking中间, 3=thinking结束
	sendText := func(text string, thinkingState int) {
		if thinkingState == 0 {
			// 普通内容
			if text == "" {
				return
			}
			sendTextDelta(text)
			return
		}

		if !thinking {
			return
		}

		switch thinkingFormat {
		case "think":
			var outputText string
			switch thinkingState {
			case 1:
				outputText = "<think>" + text
			case 2:
				outputText = text
			case 3:
				outputText = text + "</think>"
			}
			if outputText == "" {
				return
			}
			sendTextDelta(outputText)
		case "reasoning_content":
			if text == "" {
				return
			}
			sendTextDelta(text)
		default:
			if thinkingOpts.OmitDisplay {
				if thinkingState == 1 {
					startContentBlock("thinking")
					return
				}
				if thinkingState == 3 {
					if sse.activeType != "thinking" {
						startContentBlock("thinking")
					}
					closeActiveBlock()
				}
				return
			}
			if thinkingState == 3 && text == "" {
				if sse.activeType == "thinking" {
					closeActiveBlock()
				}
				return
			}
			if text != "" {
				sendThinkingDelta(text)
			}
			if thinkingState == 3 && sse.activeType == "thinking" {
				closeActiveBlock()
			}
		}
	}

	// 处理文本，解析 <thinking> 标签
	var thinkingStarted bool
	var eventThinkingOpen bool

	processClaudeText := func(text string, isThinking bool, forceFlush bool) {
		if isThinking && !thinking {
			return
		}

		// 如果是 reasoningContentEvent，直接输出
		if isThinking {
			if !allowReasoningSource(&thinkingSource) {
				return
			}
			if !thinkingStarted {
				sendText(text, 1)
				thinkingStarted = true
				eventThinkingOpen = true
			} else {
				sendText(text, 2)
			}
			return
		}

		if eventThinkingOpen {
			sendText("", 3)
			eventThinkingOpen = false
			thinkingStarted = false
		}

		textBuffer += text

		for {
			if !inThinkingBlock {
				thinkingStart := strings.Index(textBuffer, "<thinking>")
				if thinkingStart != -1 {
					if thinkingStart > 0 {
						sendText(textBuffer[:thinkingStart], 0)
					}
					textBuffer = textBuffer[thinkingStart+10:]
					inThinkingBlock = true
					dropTagThinking = !allowTagSource(&thinkingSource)
					thinkingStarted = false
				} else if forceFlush || len([]rune(textBuffer)) > 50 {
					// 使用 rune 切片来正确处理 Unicode 字符
					runes := []rune(textBuffer)
					safeLen := len(runes)
					if !forceFlush {
						safeLen = max(0, len(runes)-15)
					}
					if safeLen > 0 {
						sendText(string(runes[:safeLen]), 0)
						textBuffer = string(runes[safeLen:])
					}
					break
				} else {
					break
				}
			} else {
				thinkingEnd := strings.Index(textBuffer, "</thinking>")
				if thinkingEnd != -1 {
					content := textBuffer[:thinkingEnd]
					if !dropTagThinking {
						if !thinkingStarted {
							sendText(content, 1)
							sendText("", 3)
						} else {
							sendText(content, 3)
						}
					}
					textBuffer = textBuffer[thinkingEnd+11:]
					inThinkingBlock = false
					dropTagThinking = false
					thinkingStarted = false
				} else if forceFlush {
					if textBuffer != "" {
						if !dropTagThinking {
							if !thinkingStarted {
								sendText(textBuffer, 1)
								sendText("", 3)
							} else {
								sendText(textBuffer, 3)
							}
						}
						textBuffer = ""
					}
					inThinkingBlock = false
					dropTagThinking = false
					thinkingStarted = false
					break
				} else {
					// 流式输出 thinking 块内的内容
					runes := []rune(textBuffer)
					if len(runes) > 20 {
						safeLen := len(runes) - 15
						if safeLen > 0 {
							if !dropTagThinking {
								if !thinkingStarted {
									sendText(string(runes[:safeLen]), 1)
									thinkingStarted = true
								} else {
									sendText(string(runes[:safeLen]), 2)
								}
							}
							textBuffer = string(runes[safeLen:])
						}
					}
					break
				}
			}
		}
	}

	callback := &KiroStreamCallback{
		OnText: func(text string, isThinking bool) {
			if text == "" {
				return
			}
			recordFirstToken()
			if isThinking {
				rawThinkingBuilder.WriteString(text)
			} else {
				rawContentBuilder.WriteString(text)
			}
			processClaudeText(text, isThinking, false)
		},
		OnValidatedToolUse: func(tu KiroToolUse) bool {
			recordFirstToken()
			// 先刷新缓冲区
			processClaudeText("", false, true)
			rawContentBuilder.WriteString(tu.Name)
			if b, err := json.Marshal(tu.Input); err == nil {
				rawContentBuilder.Write(b)
			}

			toolUses = append(toolUses, tu)
			updateRequestLogReliability(r, -1, 0, 0, len(toolUses))
			closeActiveBlock()
			startMessage()
			sse.ToolUse(tu)
			return true
		},
		OnSuppressedToolUse: func(tu KiroToolUse, reason string) {
			updateRequestLogSuppressedToolUse(r, tu.Name, reason)
			updateRequestLogSuppressedToolUseDetail(r, tu, reason)
		},
		OnComplete: func(inTok, outTok int) {
			inputTokens = inTok
			outputTokens = outTok
		},
		OnError: func(err error) {
			h.recordAccountModelFailure(account.ID, model, err)
		},
		OnCredits: func(c float64) {
			credits = c
		},
		OnContextUsage: func(pct float64) {
			realInputTokens = int(pct * float64(getContextWindowSize(model)) / 100.0)
		},
	}

	upstreamStartedAt = time.Now()
	startedAt := upstreamStartedAt
	err := CallKiroAPI(account, payload, callback)
	latency := time.Since(startedAt)
	if err != nil {
		h.recordFailure()
		reason := classifyFailureReason(err)
		status, _ := claudeUpstreamErrorStatusAndType(err)
		modelAdmissionGate.recordPressureUntil(model, status, latency, rateLimitResetFromError(err))
		h.recordAccountModelFailure(account.ID, model, err)
		h.checkOverageError(err, account.ID)
		if (shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model)) && !streamStarted {
			return false, err
		}
		if !streamStarted {
			status, errType := claudeUpstreamErrorStatusAndType(err)
			h.sendClaudeUpstreamError(w, status, errType, err.Error(), err)
			return true, err
		}
		startMessage()
		_, errType := claudeUpstreamErrorStatusAndType(err)
		sse.Error(errType, err.Error())
		return true, err
	}
	modelAdmissionGate.recordSuccess(model, latency)

	// 刷新剩余缓冲区
	processClaudeText("", false, true)
	if eventThinkingOpen {
		sendText("", 3)
		eventThinkingOpen = false
	}
	closeActiveBlock()

	if realInputTokens > 0 {
		inputTokens = realInputTokens
	} else if inputTokens <= 0 {
		inputTokens = estimatedInputTokens
	}
	outputContent, extractedReasoning := extractThinkingFromContent(rawContentBuilder.String())
	thinkingOutput := rawThinkingBuilder.String()
	if thinking && thinkingOutput == "" && extractedReasoning != "" {
		thinkingOutput = extractedReasoning
	}
	if !thinking {
		thinkingOutput = ""
	}
	outputTokens = estimateClaudeOutputTokens(outputContent, thinkingOutput, toolUses)

	updateRequestLogUsage(r, billedClaudeInputTokens(inputTokens, cacheUsage), outputTokens, cacheUsage.CacheReadInputTokens, cacheUsage.CacheCreationInputTokens)
	h.recordSuccess(inputTokens, outputTokens, credits)
	h.pool.RecordSuccessWithLatency(account.ID, latency)
	h.pool.RecordModelSuccess(account.ID, model)
	h.pool.UpdateStats(account.ID, inputTokens+outputTokens, credits)
	h.promptCache.Update(account.ID, cacheProfile)

	// 发送 message_delta
	stopReason := "end_turn"
	if len(toolUses) > 0 {
		stopReason = "tool_use"
	}

	startMessage()
	sse.Stop(stopReason, buildClaudeUsageMap(inputTokens, outputTokens, cacheUsage, cacheProfile != nil))
	return true, nil
}

func (h *Handler) sendSSE(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	flusher.Flush()
}

func (h *Handler) sendClaudeStreamError(w http.ResponseWriter, flusher http.Flusher, err error) {
	_, errType := claudeUpstreamErrorStatusAndType(err)
	message := "unknown upstream error"
	if err != nil {
		message = err.Error()
	}
	h.sendSSE(w, flusher, "error", map[string]interface{}{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
}

// backgroundStatsSaver 后台定时保存统计数据
func (h *Handler) backgroundStatsSaver() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.saveStats()
		case <-h.stopStatsSaver:
			h.saveStats() // 退出前保存一次
			return
		}
	}
}

// saveStats 保存统计到配置文件
func (h *Handler) saveStats() {
	config.UpdateStats(
		int(atomic.LoadInt64(&h.totalRequests)),
		int(atomic.LoadInt64(&h.successRequests)),
		int(atomic.LoadInt64(&h.failedRequests)),
		int(atomic.LoadInt64(&h.totalTokens)),
		h.getCredits(),
	)
}

// getCredits 线程安全获取 credits
func (h *Handler) getCredits() float64 {
	h.creditsMu.RLock()
	defer h.creditsMu.RUnlock()
	return h.totalCredits
}

// addCredits 线程安全增加 credits
func (h *Handler) addCredits(credits float64) {
	h.creditsMu.Lock()
	h.totalCredits += credits
	h.creditsMu.Unlock()
}

// 统计记录 (使用原子操作)
func (h *Handler) recordSuccess(inputTokens, outputTokens int, credits float64) {
	atomic.AddInt64(&h.totalRequests, 1)
	atomic.AddInt64(&h.successRequests, 1)
	atomic.AddInt64(&h.totalTokens, int64(inputTokens+outputTokens))
	h.addCredits(credits)
}

func (h *Handler) recordFailure() {
	atomic.AddInt64(&h.totalRequests, 1)
	atomic.AddInt64(&h.failedRequests, 1)
}

// checkOverageError 检测 402 超额错误，自动关闭对应账号的超额使用
func (h *Handler) checkOverageError(err error, accountID string) {
	if err == nil {
		return
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "402") && strings.Contains(errMsg, "OVERAGE") {
		logger.Warnf("[Overage] Detected overage limit error for account %s, disabling AllowOverage", accountID)
		config.DisableAccountOverage(accountID)
	}
}

// handleClaudeNonStream Claude 非流式响应
func (h *Handler) handleClaudeNonStreamAttempt(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload, model string, thinking bool, thinkingOpts claudeThinkingResponseOptions, estimatedInputTokens int, cacheUsage promptCacheUsage, cacheProfile *promptCacheProfile, used map[string]bool, attempt int) (bool, error) {
	var content string
	var thinkingContent string
	var toolUses []KiroToolUse
	var inputTokens, outputTokens int
	var credits float64
	var realInputTokens int
	var upstreamStartedAt time.Time
	var firstTokenRecorded bool
	recordFirstToken := func() {
		if firstTokenRecorded || upstreamStartedAt.IsZero() {
			return
		}
		firstTokenRecorded = true
		updateRequestLogReliability(r, -1, 0, time.Since(upstreamStartedAt).Milliseconds(), -1)
	}

	callback := &KiroStreamCallback{
		OnText: func(text string, isThinking bool) {
			if text != "" {
				recordFirstToken()
			}
			if isThinking {
				thinkingContent += text
			} else {
				content += text
			}
		},
		OnToolUse: func(tu KiroToolUse) {
			recordFirstToken()
			toolUses = append(toolUses, tu)
			updateRequestLogReliability(r, -1, 0, 0, len(toolUses))
		},
		OnSuppressedToolUse: func(tu KiroToolUse, reason string) {
			updateRequestLogSuppressedToolUse(r, tu.Name, reason)
			updateRequestLogSuppressedToolUseDetail(r, tu, reason)
		},
		OnComplete: func(inTok, outTok int) {
			inputTokens = inTok
			outputTokens = outTok
		},
		OnError: func(err error) {
			h.recordAccountModelFailure(account.ID, model, err)
		},
		OnCredits: func(c float64) {
			credits = c
		},
		OnContextUsage: func(pct float64) {
			realInputTokens = int(pct * float64(getContextWindowSize(model)) / 100.0)
		},
	}

	upstreamStartedAt = time.Now()
	startedAt := upstreamStartedAt
	err := CallKiroAPI(account, payload, callback)
	latency := time.Since(startedAt)
	if err != nil {
		reason := classifyFailureReason(err)
		status, _ := claudeUpstreamErrorStatusAndType(err)
		modelAdmissionGate.recordPressureUntil(model, status, latency, rateLimitResetFromError(err))
		h.recordAccountModelFailure(account.ID, model, err)
		if shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model) {
			return false, err
		}
		h.recordFailure()
		h.checkOverageError(err, account.ID)
		status, errType := claudeUpstreamErrorStatusAndType(err)
		h.sendClaudeUpstreamError(w, status, errType, err.Error(), err)
		return true, err
	}
	modelAdmissionGate.recordSuccess(model, latency)

	// 合并 thinking 内容（如果有 reasoningContentEvent 的内容）
	thinkingFormat := thinkingOpts.Format
	finalContent, extractedReasoning := extractThinkingFromContent(content)
	rawThinkingContent := thinkingContent
	if thinking && rawThinkingContent == "" && extractedReasoning != "" {
		rawThinkingContent = extractedReasoning
	}
	if !thinking {
		rawThinkingContent = ""
	}

	if realInputTokens > 0 {
		inputTokens = realInputTokens
	} else if inputTokens <= 0 {
		inputTokens = estimatedInputTokens
	}
	outputTokens = estimateClaudeOutputTokens(finalContent, rawThinkingContent, toolUses)

	updateRequestLogUsage(r, billedClaudeInputTokens(inputTokens, cacheUsage), outputTokens, cacheUsage.CacheReadInputTokens, cacheUsage.CacheCreationInputTokens)
	h.recordSuccess(inputTokens, outputTokens, credits)
	h.pool.RecordSuccessWithLatency(account.ID, latency)
	h.pool.RecordModelSuccess(account.ID, model)
	h.pool.UpdateStats(account.ID, inputTokens+outputTokens, credits)
	h.promptCache.Update(account.ID, cacheProfile)

	responseThinkingContent := rawThinkingContent
	includeEmptyThinkingBlock := thinking && thinkingOpts.OmitDisplay && rawThinkingContent != ""
	if includeEmptyThinkingBlock {
		responseThinkingContent = ""
	}

	if thinking && responseThinkingContent != "" {
		switch thinkingFormat {
		case "think":
			finalContent = "<think>" + responseThinkingContent + "</think>" + finalContent
			responseThinkingContent = ""
		case "reasoning_content":
			finalContent = responseThinkingContent + finalContent // Claude 格式不支持 reasoning_content，直接拼接
			responseThinkingContent = ""
		default:
		}
	}

	resp := KiroToClaudeResponse(finalContent, responseThinkingContent, includeEmptyThinkingBlock, toolUses, inputTokens, outputTokens, model)
	resp.Usage.InputTokens = billedClaudeInputTokens(inputTokens, cacheUsage)
	resp.Usage.CacheCreationInputTokens = cacheUsage.CacheCreationInputTokens
	resp.Usage.CacheReadInputTokens = cacheUsage.CacheReadInputTokens
	if cacheProfile != nil {
		resp.Usage.CacheCreation = &ClaudeCacheCreationUsage{
			Ephemeral5mInputTokens: cacheUsage.CacheCreation5mInputTokens,
			Ephemeral1hInputTokens: cacheUsage.CacheCreation1hInputTokens,
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
	return true, nil
}

func (h *Handler) sendClaudeError(w http.ResponseWriter, status int, errType, message string) {
	h.sendClaudeErrorWithHeaders(w, status, errType, message, nil)
}

func (h *Handler) sendClaudeUpstreamError(w http.ResponseWriter, status int, errType, message string, err error) {
	h.sendClaudeErrorWithHeaders(w, status, errType, message, claudeErrorHeadersForUpstreamError(err))
}

func claudeErrorHeadersForUpstreamError(err error) http.Header {
	headers := http.Header{}
	if err == nil {
		return headers
	}
	var poolTempErr *poolTemporaryLimitError
	if errors.As(err, &poolTempErr) {
		headers.Set("Retry-After", retryAfterSeconds(pool.FailureReasonTemporaryLimited, poolTempErr.RateLimitResetAt()))
		headers.Set("X-Kiro-Go-Error-Reason", "TEMPORARY_LIMITED")
		return headers
	}
	reason := classifyFailureReason(err)
	if reason == pool.FailureReasonTemporaryLimited {
		headers.Set("Retry-After", retryAfterSeconds(reason, rateLimitResetFromError(err)))
		headers.Set("X-Kiro-Go-Error-Reason", "TEMPORARY_LIMITED")
		return headers
	}
	status, errType := claudeUpstreamErrorStatusAndType(err)
	if status != http.StatusTooManyRequests || errType != "rate_limit_error" {
		return headers
	}
	resetAt := rateLimitResetFromError(err)
	if resetAt.IsZero() {
		return headers
	}
	headers.Set("Retry-After", retryAfterSeconds(reason, resetAt))
	return headers
}

func retryAfterSeconds(reason pool.FailureReason, resetAt time.Time) string {
	var delay time.Duration
	if resetAt.After(time.Now()) {
		delay = time.Until(resetAt)
	}
	if reason == pool.FailureReasonTemporaryLimited {
		if floor := pool.TemporaryLimitRetryAfterFloor(); delay < floor {
			delay = floor
		}
	}
	if delay <= 0 {
		delay = time.Second
	}
	seconds := int((delay + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func (h *Handler) sendClaudeErrorWithHeaders(w http.ResponseWriter, status int, errType, message string, headers http.Header) {
	for key, values := range headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				w.Header().Set(key, value)
				break
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"type":    errType,
			"message": message,
		},
	})
}

// handleOpenAIChat OpenAI API 处理
func (h *Handler) handleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method Not Allowed", 405)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.sendOpenAIError(w, 400, "invalid_request_error", "Failed to read request body")
		return
	}

	var req OpenAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.sendOpenAIError(w, 400, "invalid_request_error", "Invalid JSON")
		return
	}
	updateRequestLogMetadata(r, req.Model, req.Stream)
	if msg := validateOpenAIRequestShape(&req); msg != "" {
		h.sendOpenAIError(w, 400, "invalid_request_error", msg)
		return
	}

	// 解析模型和 thinking 模式
	thinkingCfg := config.GetThinkingConfig()
	actualModel, thinking := ParseModelAndThinking(req.Model, thinkingCfg.Suffix)
	req.Model = actualModel
	estimatedInputTokens := estimateOpenAIRequestInputTokens(&req)

	kiroPayload := OpenAIToKiro(&req, thinking)
	guardResult, guardErr := prepareGuardedKiroPayload(kiroPayload, defaultPayloadGuardOptions())
	updateRequestLogPayload(r, guardResult)
	if guardErr != nil {
		h.sendOpenAIError(w, 400, "invalid_request_error", guardErr.Error())
		return
	}
	h.handleOpenAIWithAccountRetry(w, r, req.Stream, kiroPayload, req.Model, thinking, estimatedInputTokens)
}

func (h *Handler) handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method Not Allowed", 405)
		return
	}

	var payload map[string]interface{}
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		h.sendOpenAIError(w, 400, "invalid_request_error", "Invalid JSON")
		return
	}

	req, err := OpenAIResponsesToChatRequest(payload)
	if err != nil {
		h.sendOpenAIError(w, 400, "invalid_request_error", err.Error())
		return
	}
	previousResponseID, _ := payload["previous_response_id"].(string)
	h.restoreOpenAIResponsesSession(payload, req)
	updateRequestLogMetadata(r, req.Model, req.Stream)
	if msg := validateOpenAIRequestShape(req); msg != "" {
		h.sendOpenAIError(w, 400, "invalid_request_error", msg)
		return
	}

	thinkingCfg := config.GetThinkingConfig()
	actualModel, thinking := ParseModelAndThinking(req.Model, thinkingCfg.Suffix)
	req.Model = actualModel
	estimatedInputTokens := estimateOpenAIRequestInputTokens(req)

	kiroPayload := OpenAIToKiro(req, thinking)
	guardResult, guardErr := prepareGuardedKiroPayload(kiroPayload, defaultPayloadGuardOptions())
	updateRequestLogPayload(r, guardResult)
	if guardErr != nil {
		h.sendOpenAIError(w, 400, "invalid_request_error", guardErr.Error())
		return
	}
	h.handleOpenAIResponsesWithAccountRetry(w, r, req.Stream, kiroPayload, req.Model, thinking, estimatedInputTokens, req, previousResponseID)
}

func (h *Handler) handleOpenAIResponsesWithAccountRetry(w http.ResponseWriter, r *http.Request, stream bool, payload *KiroPayload, model string, thinking bool, estimatedInputTokens int, req *OpenAIRequest, previousResponseID string) {
	deadline := time.Now().Add(opusCapacityRetryBudget)
	releaseGate, ok := h.acquireOpus47AdmissionForRequest(w, r, model, stream, false, deadline)
	if !ok {
		return
	}
	defer releaseGate()

	used := make(map[string]bool)
	var lastErr error
	attempt := 0
	capacityRetryCount := 0

	for {
		updateRequestLogReliability(r, -1, attempt+1, 0, -1)
		account, releaseRequest := h.pool.BeginNextForModelExcept(model, used)
		if account == nil {
			if h.waitForRecoverablePoolBlock(model, deadline) {
				capacityRetryCount++
				updateRequestLogCapacityRetryCount(r, capacityRetryCount)
				used = make(map[string]bool)
				attempt++
				continue
			}
			if shouldWaitAndRetryOpus47(lastErr, model) {
				if delay, ok := opusCapacityRetryDelay(lastErr, deadline); ok {
					capacityRetryCount++
					updateRequestLogCapacityRetryCount(r, capacityRetryCount)
					sleepForOpusCapacityRetry(delay)
					used = make(map[string]bool)
					continue
				}
				h.recordFailure()
				status, errType := openAIUpstreamErrorStatusAndType(lastErr)
				h.sendOpenAIError(w, status, errType, "Opus 4.7 upstream capacity did not recover within 90s: "+lastErr.Error())
				return
			}
			if lastErr != nil {
				h.recordFailure()
				status, errType := openAIUpstreamErrorStatusAndType(lastErr)
				h.sendOpenAIError(w, status, errType, lastErr.Error())
			} else {
				h.recordFailure()
				h.sendOpenAIError(w, 503, "server_error", "No available accounts")
			}
			return
		}
		used[account.ID] = true
		h.updateRequestLogUpstreamAccount(r, account.ID, resolveAccountKiroRegion(account))
		h.updateRequestLogRoutingDecision(r, model, attempt, account)

		attemptPayload := cloneKiroPayload(payload)
		if !h.finalizePayloadForOpenAIAccount(w, r, account, attemptPayload) {
			releaseRequest()
			return
		}

		if err := h.ensureValidToken(account); err != nil {
			h.recordAccountFailure(account.ID, err)
			if attempt == 0 {
				releaseRequest()
				continue
			}
			releaseRequest()
			h.sendOpenAIError(w, 503, "server_error", "Token refresh failed")
			return
		}

		var err error
		var done bool
		if stream {
			done, err = h.handleOpenAIResponsesStreamAttempt(w, r, account, attemptPayload, model, thinking, estimatedInputTokens, attempt, req, previousResponseID)
		} else {
			done, err = h.handleOpenAIResponsesNonStreamAttempt(w, r, account, attemptPayload, model, thinking, estimatedInputTokens, attempt, req, previousResponseID)
		}
		releaseRequest()
		if done {
			return
		}
		lastErr = err
		if shouldWaitAndRetryOpus47(err, model) {
			attempt++
			continue
		}
		if err != nil && shouldRetryAccount(classifyFailureReason(err), attempt) {
			attempt++
			continue
		}
		if err != nil {
			h.recordFailure()
			status, errType := openAIUpstreamErrorStatusAndType(err)
			h.sendOpenAIError(w, status, errType, err.Error())
			return
		}
		attempt++
	}
}

func (h *Handler) finalizePayloadForClaudeAccount(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload) bool {
	result, err := finalizeKiroPayloadForAccount(payload, account, defaultPayloadGuardOptions())
	updateRequestLogPayloadFinalBytes(r, result.FinalBytes)
	if err != nil {
		h.sendClaudeError(w, 400, "invalid_request_error", err.Error())
		return false
	}
	return true
}

func (h *Handler) finalizePayloadForOpenAIAccount(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload) bool {
	result, err := finalizeKiroPayloadForAccount(payload, account, defaultPayloadGuardOptions())
	updateRequestLogPayloadFinalBytes(r, result.FinalBytes)
	if err != nil {
		h.sendOpenAIError(w, 400, "invalid_request_error", err.Error())
		return false
	}
	return true
}

func (h *Handler) restoreOpenAIResponsesSession(payload map[string]interface{}, req *OpenAIRequest) {
	if h == nil || req == nil || payload == nil {
		return
	}
	previousID, _ := payload["previous_response_id"].(string)
	chain := h.collectOpenAIResponsesSessionChain(previousID)
	if len(chain) == 0 {
		return
	}
	currentMessages := req.Messages
	toolOutputIDs := currentOpenAIToolOutputIDs(currentMessages)
	restored := make([]OpenAIMessage, 0)
	for i, session := range chain {
		messages := session.Messages
		if i == len(chain)-1 {
			messages = filterLatestAssistantToolCalls(messages, toolOutputIDs)
		}
		restored = append(restored, messages...)
	}
	req.Messages = append(restored, currentMessages...)
	for i := len(chain) - 1; i >= 0; i-- {
		session := chain[i]
		if len(req.Tools) == 0 && len(session.Tools) > 0 {
			req.Tools = append([]OpenAITool(nil), session.Tools...)
		}
		if req.ToolChoice == nil && session.ToolChoice != nil {
			req.ToolChoice = session.ToolChoice
		}
	}
}

func (h *Handler) collectOpenAIResponsesSessionChain(previousID string) []responsesSession {
	previousID = strings.TrimSpace(previousID)
	if previousID == "" {
		return nil
	}
	seen := make(map[string]bool)
	var chain []responsesSession
	for previousID != "" && !seen[previousID] {
		seen[previousID] = true
		session, ok := h.getOpenAIResponsesSession(previousID)
		if !ok {
			break
		}
		chain = append(chain, session)
		previousID = strings.TrimSpace(session.PreviousResponseID)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func currentOpenAIToolOutputIDs(messages []OpenAIMessage) map[string]bool {
	ids := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == "tool" && strings.TrimSpace(msg.ToolCallID) != "" {
			ids[msg.ToolCallID] = true
		}
	}
	return ids
}

func filterLatestAssistantToolCalls(messages []OpenAIMessage, keep map[string]bool) []OpenAIMessage {
	if len(keep) == 0 {
		return messages
	}
	filtered := append([]OpenAIMessage(nil), messages...)
	for i := len(filtered) - 1; i >= 0; i-- {
		msg := filtered[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		toolCalls := make([]ToolCall, 0, len(msg.ToolCalls))
		for _, toolCall := range msg.ToolCalls {
			if keep[toolCall.ID] {
				toolCalls = append(toolCalls, toolCall)
			}
		}
		filtered[i].ToolCalls = toolCalls
		return filtered
	}
	return filtered
}

func (h *Handler) getOpenAIResponsesSession(id string) (responsesSession, bool) {
	if h == nil {
		return responsesSession{}, false
	}
	h.responsesMu.Lock()
	defer h.responsesMu.Unlock()
	if h.responses == nil {
		return responsesSession{}, false
	}
	session, ok := h.responses[id]
	if !ok {
		return responsesSession{}, false
	}
	session.Messages = append([]OpenAIMessage(nil), session.Messages...)
	session.Tools = append([]OpenAITool(nil), session.Tools...)
	return session, true
}

func (h *Handler) saveOpenAIResponsesSession(id, previousResponseID string, req *OpenAIRequest, content string, toolUses []KiroToolUse) {
	if h == nil || req == nil || strings.TrimSpace(id) == "" {
		return
	}
	messages := append([]OpenAIMessage(nil), req.Messages...)
	if len(toolUses) > 0 {
		assistant := OpenAIMessage{Role: "assistant"}
		for _, tu := range toolUses {
			tc := ToolCall{ID: tu.ToolUseID, Type: "function"}
			tc.Function.Name = tu.Name
			args, _ := json.Marshal(tu.Input)
			tc.Function.Arguments = string(args)
			assistant.ToolCalls = append(assistant.ToolCalls, tc)
		}
		messages = append(messages, assistant)
	} else if strings.TrimSpace(content) != "" {
		messages = append(messages, OpenAIMessage{Role: "assistant", Content: content})
	}
	session := responsesSession{
		PreviousResponseID: strings.TrimSpace(previousResponseID),
		Messages:           messages,
		Tools:              append([]OpenAITool(nil), req.Tools...),
		ToolChoice:         req.ToolChoice,
		UpdatedAt:          time.Now(),
	}
	h.responsesMu.Lock()
	defer h.responsesMu.Unlock()
	if h.responses == nil {
		h.responses = make(map[string]responsesSession)
	}
	h.pruneOpenAIResponsesSessionsLocked(time.Now())
	h.responses[id] = session
	h.pruneOpenAIResponsesSessionsLocked(time.Now())
}

func (h *Handler) pruneOpenAIResponsesSessionsLocked(now time.Time) {
	if h.responses == nil {
		return
	}
	for id, session := range h.responses {
		if now.Sub(session.UpdatedAt) > openAIResponsesSessionTTL {
			delete(h.responses, id)
		}
	}
	for len(h.responses) > maxOpenAIResponsesSessions {
		h.trimOpenAIResponsesSessionsLocked()
	}
}

func (h *Handler) trimOpenAIResponsesSessionsLocked() {
	var oldestID string
	var oldest time.Time
	for id, session := range h.responses {
		if oldestID == "" || session.UpdatedAt.Before(oldest) {
			oldestID = id
			oldest = session.UpdatedAt
		}
	}
	if oldestID != "" {
		delete(h.responses, oldestID)
	}
}

// handleOpenAIStream OpenAI 流式响应
func (h *Handler) handleOpenAIStreamAttempt(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload, model string, thinking bool, estimatedInputTokens int, used map[string]bool, attempt int) (bool, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		h.sendOpenAIError(w, 500, "server_error", "Streaming not supported")
		return true, fmt.Errorf("streaming not supported")
	}

	// 获取 thinking 输出格式配置
	thinkingFormat := config.GetThinkingConfig().OpenAIFormat

	chatID := "chatcmpl-" + uuid.New().String()
	var toolCalls []ToolCall
	var toolCallIndex int
	var inputTokens, outputTokens int
	var credits float64
	var realInputTokens int
	var rawContentBuilder strings.Builder
	var rawReasoningBuilder strings.Builder
	streamStarted := false

	// Thinking 标签解析状态
	var textBuffer string
	var inThinkingBlock bool
	var dropTagThinking bool
	var thinkingSource thinkingStreamSource

	// 发送 chunk 的辅助函数
	// thinkingState: 0=普通内容, 1=thinking开始, 2=thinking中间, 3=thinking结束
	sendChunk := func(content string, thinkingState int) {
		if content == "" && thinkingState == 2 {
			return
		}

		var chunk map[string]interface{}

		if thinkingState > 0 {
			if !thinking {
				return
			}
			// thinking 内容
			switch thinkingFormat {
			case "thinking":
				// 流式输出标签
				var text string
				switch thinkingState {
				case 1: // 开始
					text = "<thinking>" + content
				case 2: // 中间
					text = content
				case 3: // 结束
					text = content + "</thinking>"
				}
				if text == "" {
					return
				}
				chunk = map[string]interface{}{
					"id":      chatID,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   model,
					"choices": []map[string]interface{}{{
						"index":         0,
						"delta":         map[string]string{"content": text},
						"finish_reason": nil,
					}},
				}
			case "think":
				var text string
				switch thinkingState {
				case 1:
					text = "<think>" + content
				case 2:
					text = content
				case 3:
					text = content + "</think>"
				}
				if text == "" {
					return
				}
				chunk = map[string]interface{}{
					"id":      chatID,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   model,
					"choices": []map[string]interface{}{{
						"index":         0,
						"delta":         map[string]string{"content": text},
						"finish_reason": nil,
					}},
				}
			default: // "reasoning_content"
				if content == "" {
					return
				}
				chunk = map[string]interface{}{
					"id":      chatID,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   model,
					"choices": []map[string]interface{}{{
						"index":         0,
						"delta":         map[string]string{"reasoning_content": content},
						"finish_reason": nil,
					}},
				}
			}
		} else {
			// 普通内容
			if content == "" {
				return
			}
			chunk = map[string]interface{}{
				"id":      chatID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index":         0,
					"delta":         map[string]string{"content": content},
					"finish_reason": nil,
				}},
			}
		}
		data, _ := json.Marshal(chunk)
		streamStarted = true
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		flusher.Flush()
	}

	// 处理文本，解析 <thinking> 标签
	// thinkingStarted 用于跟踪是否已发送开始标签
	var thinkingStarted bool
	var eventThinkingOpen bool

	processText := func(text string, isThinking bool, forceFlush bool) {
		if isThinking && !thinking {
			return
		}

		// 如果是 reasoningContentEvent，直接输出
		if isThinking {
			if !allowReasoningSource(&thinkingSource) {
				return
			}
			if !thinkingStarted {
				sendChunk(text, 1) // 开始
				thinkingStarted = true
				eventThinkingOpen = true
			} else {
				sendChunk(text, 2) // 中间
			}
			return
		}

		if eventThinkingOpen {
			sendChunk("", 3)
			eventThinkingOpen = false
			thinkingStarted = false
		}

		textBuffer += text

		for {
			if !inThinkingBlock {
				// 查找 <thinking> 开始标签
				thinkingStart := strings.Index(textBuffer, "<thinking>")
				if thinkingStart != -1 {
					// 输出 thinking 标签之前的内容
					if thinkingStart > 0 {
						sendChunk(textBuffer[:thinkingStart], 0)
					}
					textBuffer = textBuffer[thinkingStart+10:] // 移除 <thinking>
					inThinkingBlock = true
					dropTagThinking = !allowTagSource(&thinkingSource)
					thinkingStarted = false // 重置，准备发送新的开始标签
				} else if forceFlush || len([]rune(textBuffer)) > 50 {
					// 没有找到标签，安全输出（保留可能的部分标签）
					runes := []rune(textBuffer)
					safeLen := len(runes)
					if !forceFlush {
						safeLen = max(0, len(runes)-15)
					}
					if safeLen > 0 {
						sendChunk(string(runes[:safeLen]), 0)
						textBuffer = string(runes[safeLen:])
					}
					break
				} else {
					break
				}
			} else {
				// 在 thinking 块内，查找 </thinking> 结束标签
				thinkingEnd := strings.Index(textBuffer, "</thinking>")
				if thinkingEnd != -1 {
					// 输出 thinking 内容
					content := textBuffer[:thinkingEnd]
					if !dropTagThinking {
						if !thinkingStarted {
							// 一次性输出完整内容（开始+内容+结束）
							sendChunk(content, 1) // 开始
							sendChunk("", 3)      // 结束（空内容，只发结束标签）
						} else {
							// 已经开始了，发送剩余内容和结束
							sendChunk(content, 3) // 结束
						}
					}
					textBuffer = textBuffer[thinkingEnd+11:] // 移除 </thinking>
					inThinkingBlock = false
					dropTagThinking = false
					thinkingStarted = false
				} else if forceFlush {
					// 强制刷新：输出剩余内容
					if textBuffer != "" {
						if !dropTagThinking {
							if !thinkingStarted {
								sendChunk(textBuffer, 1) // 开始
								sendChunk("", 3)         // 结束
							} else {
								sendChunk(textBuffer, 3) // 结束
							}
						}
						textBuffer = ""
					}
					inThinkingBlock = false
					dropTagThinking = false
					thinkingStarted = false
					break
				} else {
					// 流式输出 thinking 块内的内容
					runes := []rune(textBuffer)
					if len(runes) > 20 {
						safeLen := len(runes) - 15 // 保留可能的 </thinking> 部分
						if safeLen > 0 {
							if !dropTagThinking {
								if !thinkingStarted {
									sendChunk(string(runes[:safeLen]), 1) // 开始
									thinkingStarted = true
								} else {
									sendChunk(string(runes[:safeLen]), 2) // 中间
								}
							}
							textBuffer = string(runes[safeLen:])
						}
					}
					break
				}
			}
		}
	}

	callback := &KiroStreamCallback{
		OnText: func(text string, isThinking bool) {
			if text == "" {
				return
			}
			if isThinking {
				rawReasoningBuilder.WriteString(text)
			} else {
				rawContentBuilder.WriteString(text)
			}
			processText(text, isThinking, false)
		},
		OnToolUse: func(tu KiroToolUse) {
			// 先刷新缓冲区
			processText("", false, true)

			args, _ := json.Marshal(tu.Input)
			rawContentBuilder.WriteString(tu.Name)
			rawContentBuilder.Write(args)
			tc := ToolCall{ID: tu.ToolUseID, Type: "function"}
			tc.Function.Name = tu.Name
			tc.Function.Arguments = string(args)
			toolCalls = append(toolCalls, tc)

			chunk := map[string]interface{}{
				"id":      chatID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": toolCallIndex,
							"id":    tu.ToolUseID,
							"type":  "function",
							"function": map[string]string{
								"name":      tu.Name,
								"arguments": string(args),
							},
						}},
					},
					"finish_reason": nil,
				}},
			}
			toolCallIndex++
			data, _ := json.Marshal(chunk)
			streamStarted = true
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		},
		OnSuppressedToolUse: func(tu KiroToolUse, reason string) {
			updateRequestLogSuppressedToolUse(r, tu.Name, reason)
			updateRequestLogSuppressedToolUseDetail(r, tu, reason)
		},
		OnComplete: func(inTok, outTok int) {
			inputTokens = inTok
			outputTokens = outTok
		},
		OnError: func(err error) {
			h.recordAccountFailure(account.ID, err)
		},
		OnCredits: func(c float64) {
			credits = c
		},
		OnContextUsage: func(pct float64) {
			realInputTokens = int(pct * float64(getContextWindowSize(model)) / 100.0)
		},
	}

	releaseRequest := h.pool.BeginRequest(account.ID)
	startedAt := time.Now()
	err := CallKiroAPI(account, payload, callback)
	latency := time.Since(startedAt)
	releaseRequest()
	if err != nil {
		h.recordFailure()
		reason := classifyFailureReason(err)
		status, _ := openAIUpstreamErrorStatusAndType(err)
		modelAdmissionGate.recordPressureUntil(model, status, latency, rateLimitResetFromError(err))
		h.recordAccountFailure(account.ID, err)
		h.checkOverageError(err, account.ID)
		if (shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model)) && !streamStarted {
			return false, err
		}
		if streamStarted {
			chunk := map[string]interface{}{
				"error": map[string]string{
					"type":    "server_error",
					"message": err.Error(),
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		}
		return true, err
	}

	// 刷新剩余缓冲区
	processText("", false, true)
	if eventThinkingOpen {
		sendChunk("", 3)
		eventThinkingOpen = false
	}

	if realInputTokens > 0 {
		inputTokens = realInputTokens
	} else if inputTokens <= 0 {
		inputTokens = estimatedInputTokens
	}
	outputContent, extractedReasoning := extractThinkingFromContent(rawContentBuilder.String())
	reasoningOutput := rawReasoningBuilder.String()
	if thinking && reasoningOutput == "" && extractedReasoning != "" {
		reasoningOutput = extractedReasoning
	}
	if !thinking {
		reasoningOutput = ""
	}
	outputTokens = estimateApproxTokens(outputContent) + estimateApproxTokens(reasoningOutput)
	for _, tc := range toolCalls {
		outputTokens += estimateApproxTokens(tc.Function.Name)
		outputTokens += estimateApproxTokens(tc.Function.Arguments)
	}

	updateRequestLogUsage(r, inputTokens, outputTokens, 0, 0)
	modelAdmissionGate.recordSuccess(model, latency)
	h.recordSuccess(inputTokens, outputTokens, credits)
	h.pool.RecordSuccessWithLatency(account.ID, latency)
	h.pool.UpdateStats(account.ID, inputTokens+outputTokens, credits)

	// 发送结束
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	chunk := map[string]interface{}{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{{
			"index":         0,
			"delta":         map[string]interface{}{},
			"finish_reason": finishReason,
		}},
		"usage": map[string]int{
			"prompt_tokens":     inputTokens,
			"completion_tokens": outputTokens,
			"total_tokens":      inputTokens + outputTokens,
		},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", string(data))
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	return true, nil
}

func (h *Handler) handleOpenAIResponsesStreamAttempt(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload, model string, thinking bool, estimatedInputTokens int, attempt int, req *OpenAIRequest, previousResponseID string) (bool, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		h.sendOpenAIError(w, 500, "server_error", "Streaming not supported")
		return true, fmt.Errorf("streaming not supported")
	}

	responseID := "resp_" + uuid.New().String()
	var content strings.Builder
	var reasoning strings.Builder
	var toolUses []KiroToolUse
	var inputTokens, outputTokens int
	var credits float64
	var realInputTokens int
	streamStarted := false
	createdSent := false

	sendEvent := func(event string, payload interface{}) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\n", event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		streamStarted = true
		flusher.Flush()
	}

	ensureCreated := func() {
		if createdSent {
			return
		}
		createdSent = true
		sendEvent("response.created", map[string]interface{}{
			"type":     "response.created",
			"response": buildOpenAIResponsesObject(responseID, model, "", 0, 0, false),
		})
	}

	callback := &KiroStreamCallback{
		OnText: func(text string, isThinking bool) {
			if isThinking {
				reasoning.WriteString(text)
				return
			}
			content.WriteString(text)
			ensureCreated()
			sendEvent("response.output_text.delta", map[string]interface{}{
				"type":  "response.output_text.delta",
				"delta": text,
			})
		},
		OnToolUse: func(tu KiroToolUse) {
			toolUses = append(toolUses, tu)
			ensureCreated()
			args, _ := json.Marshal(tu.Input)
			sendEvent("response.output_item.done", map[string]interface{}{
				"type": "response.output_item.done",
				"item": map[string]interface{}{
					"id":        "item_" + uuid.New().String(),
					"type":      "function_call",
					"call_id":   tu.ToolUseID,
					"name":      tu.Name,
					"arguments": string(args),
					"status":    "completed",
				},
			})
		},
		OnSuppressedToolUse: func(tu KiroToolUse, reason string) {
			updateRequestLogSuppressedToolUse(r, tu.Name, reason)
			updateRequestLogSuppressedToolUseDetail(r, tu, reason)
		},
		OnComplete: func(inTok, outTok int) { inputTokens = inTok; outputTokens = outTok },
		OnError:    func(err error) { h.recordAccountFailure(account.ID, err) },
		OnCredits:  func(c float64) { credits = c },
		OnContextUsage: func(pct float64) {
			realInputTokens = int(pct * float64(getContextWindowSize(model)) / 100.0)
		},
	}

	releaseRequest := h.pool.BeginRequest(account.ID)
	startedAt := time.Now()
	err := CallKiroAPI(account, payload, callback)
	latency := time.Since(startedAt)
	releaseRequest()
	if err != nil {
		reason := classifyFailureReason(err)
		status, _ := openAIUpstreamErrorStatusAndType(err)
		modelAdmissionGate.recordPressureUntil(model, status, latency, rateLimitResetFromError(err))
		h.recordAccountFailure(account.ID, err)
		if (shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model)) && !streamStarted {
			return false, err
		}
		h.recordFailure()
		h.checkOverageError(err, account.ID)
		sendEvent("response.failed", map[string]interface{}{
			"type":  "response.failed",
			"error": map[string]string{"type": "server_error", "message": err.Error()},
		})
		return true, err
	}

	finalContent, extractedReasoning := extractThinkingFromContent(content.String())
	reasoningContent := reasoning.String()
	if thinking && reasoningContent == "" && extractedReasoning != "" {
		reasoningContent = extractedReasoning
	}
	if !thinking {
		reasoningContent = ""
	}
	if realInputTokens > 0 {
		inputTokens = realInputTokens
	} else if inputTokens <= 0 {
		inputTokens = estimatedInputTokens
	}
	outputTokens = estimateOpenAIOutputTokens(finalContent, reasoningContent, toolUses)

	updateRequestLogUsage(r, inputTokens, outputTokens, 0, 0)
	modelAdmissionGate.recordSuccess(model, latency)
	h.recordSuccess(inputTokens, outputTokens, credits)
	h.pool.RecordSuccessWithLatency(account.ID, latency)
	h.pool.UpdateStats(account.ID, inputTokens+outputTokens, credits)
	h.saveOpenAIResponsesSession(responseID, previousResponseID, req, finalContent, toolUses)

	ensureCreated()
	sendEvent("response.completed", map[string]interface{}{
		"type":     "response.completed",
		"response": buildOpenAIResponsesObjectWithToolUses(responseID, model, finalContent, toolUses, inputTokens, outputTokens, true),
	})
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	return true, nil
}

// handleOpenAINonStream OpenAI 非流式响应
func (h *Handler) handleOpenAINonStreamAttempt(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload, model string, thinking bool, estimatedInputTokens int, used map[string]bool, attempt int) (bool, error) {
	var content string
	var reasoningContent string
	var toolUses []KiroToolUse
	var inputTokens, outputTokens int
	var credits float64
	var realInputTokens int

	callback := &KiroStreamCallback{
		OnText: func(text string, isThinking bool) {
			if isThinking {
				reasoningContent += text
			} else {
				content += text
			}
		},
		OnToolUse: func(tu KiroToolUse) { toolUses = append(toolUses, tu) },
		OnSuppressedToolUse: func(tu KiroToolUse, reason string) {
			updateRequestLogSuppressedToolUse(r, tu.Name, reason)
			updateRequestLogSuppressedToolUseDetail(r, tu, reason)
		},
		OnComplete: func(inTok, outTok int) { inputTokens = inTok; outputTokens = outTok },
		OnError:    func(err error) { h.recordAccountFailure(account.ID, err) },
		OnCredits:  func(c float64) { credits = c },
		OnContextUsage: func(pct float64) {
			realInputTokens = int(pct * float64(getContextWindowSize(model)) / 100.0)
		},
	}

	releaseRequest := h.pool.BeginRequest(account.ID)
	startedAt := time.Now()
	err := CallKiroAPI(account, payload, callback)
	latency := time.Since(startedAt)
	releaseRequest()
	if err != nil {
		reason := classifyFailureReason(err)
		status, _ := openAIUpstreamErrorStatusAndType(err)
		modelAdmissionGate.recordPressureUntil(model, status, latency, rateLimitResetFromError(err))
		h.recordAccountFailure(account.ID, err)
		if shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model) {
			return false, err
		}
		h.recordFailure()
		h.checkOverageError(err, account.ID)
		h.sendOpenAIError(w, 500, "server_error", err.Error())
		return true, err
	}

	// 解析 content 中的 <thinking> 标签
	finalContent, extractedReasoning := extractThinkingFromContent(content)
	if thinking && reasoningContent == "" && extractedReasoning != "" {
		reasoningContent = extractedReasoning
	} else if !thinking {
		reasoningContent = ""
	}

	if realInputTokens > 0 {
		inputTokens = realInputTokens
	} else if inputTokens <= 0 {
		inputTokens = estimatedInputTokens
	}
	outputTokens = estimateOpenAIOutputTokens(finalContent, reasoningContent, toolUses)

	updateRequestLogUsage(r, inputTokens, outputTokens, 0, 0)
	modelAdmissionGate.recordSuccess(model, latency)
	h.recordSuccess(inputTokens, outputTokens, credits)
	h.pool.RecordSuccessWithLatency(account.ID, latency)
	h.pool.UpdateStats(account.ID, inputTokens+outputTokens, credits)

	thinkingFormat := config.GetThinkingConfig().OpenAIFormat
	resp := KiroToOpenAIResponseWithReasoning(finalContent, reasoningContent, toolUses, inputTokens, outputTokens, model, thinkingFormat)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
	return true, nil
}

func (h *Handler) handleOpenAIResponsesNonStreamAttempt(w http.ResponseWriter, r *http.Request, account *config.Account, payload *KiroPayload, model string, thinking bool, estimatedInputTokens int, attempt int, req *OpenAIRequest, previousResponseID string) (bool, error) {
	var content string
	var reasoningContent string
	var toolUses []KiroToolUse
	var inputTokens, outputTokens int
	var credits float64
	var realInputTokens int

	callback := &KiroStreamCallback{
		OnText: func(text string, isThinking bool) {
			if isThinking {
				reasoningContent += text
			} else {
				content += text
			}
		},
		OnToolUse: func(tu KiroToolUse) { toolUses = append(toolUses, tu) },
		OnSuppressedToolUse: func(tu KiroToolUse, reason string) {
			updateRequestLogSuppressedToolUse(r, tu.Name, reason)
			updateRequestLogSuppressedToolUseDetail(r, tu, reason)
		},
		OnComplete: func(inTok, outTok int) { inputTokens = inTok; outputTokens = outTok },
		OnError:    func(err error) { h.recordAccountFailure(account.ID, err) },
		OnCredits:  func(c float64) { credits = c },
		OnContextUsage: func(pct float64) {
			realInputTokens = int(pct * float64(getContextWindowSize(model)) / 100.0)
		},
	}

	releaseRequest := h.pool.BeginRequest(account.ID)
	startedAt := time.Now()
	err := CallKiroAPI(account, payload, callback)
	latency := time.Since(startedAt)
	releaseRequest()
	if err != nil {
		reason := classifyFailureReason(err)
		status, _ := openAIUpstreamErrorStatusAndType(err)
		modelAdmissionGate.recordPressureUntil(model, status, latency, rateLimitResetFromError(err))
		h.recordAccountFailure(account.ID, err)
		if shouldRetryAccount(reason, attempt) || shouldWaitAndRetryOpus47(err, model) {
			return false, err
		}
		h.recordFailure()
		h.checkOverageError(err, account.ID)
		h.sendOpenAIError(w, 500, "server_error", err.Error())
		return true, err
	}

	finalContent, extractedReasoning := extractThinkingFromContent(content)
	if thinking && reasoningContent == "" && extractedReasoning != "" {
		reasoningContent = extractedReasoning
	} else if !thinking {
		reasoningContent = ""
	}
	if realInputTokens > 0 {
		inputTokens = realInputTokens
	} else if inputTokens <= 0 {
		inputTokens = estimatedInputTokens
	}
	outputTokens = estimateOpenAIOutputTokens(finalContent, reasoningContent, toolUses)

	updateRequestLogUsage(r, inputTokens, outputTokens, 0, 0)
	modelAdmissionGate.recordSuccess(model, latency)
	h.recordSuccess(inputTokens, outputTokens, credits)
	h.pool.RecordSuccessWithLatency(account.ID, latency)
	h.pool.UpdateStats(account.ID, inputTokens+outputTokens, credits)

	responseID := "resp_" + uuid.New().String()
	h.saveOpenAIResponsesSession(responseID, previousResponseID, req, finalContent, toolUses)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(buildOpenAIResponsesObjectWithToolUses(responseID, model, finalContent, toolUses, inputTokens, outputTokens, true))
	return true, nil
}

func buildOpenAIResponsesObject(id, model, text string, inputTokens, outputTokens int, completed bool) map[string]interface{} {
	return buildOpenAIResponsesObjectWithToolUses(id, model, text, nil, inputTokens, outputTokens, completed)
}

func buildOpenAIResponsesObjectWithToolUses(id, model, text string, toolUses []KiroToolUse, inputTokens, outputTokens int, completed bool) map[string]interface{} {
	status := "in_progress"
	if completed {
		status = "completed"
	}
	output := []map[string]interface{}{}
	outputText := text
	if len(toolUses) > 0 {
		outputText = ""
		for _, tu := range toolUses {
			args, _ := json.Marshal(tu.Input)
			output = append(output, map[string]interface{}{
				"id":        "item_" + uuid.New().String(),
				"type":      "function_call",
				"call_id":   tu.ToolUseID,
				"name":      tu.Name,
				"arguments": string(args),
				"status":    "completed",
			})
		}
	} else {
		output = append(output, map[string]interface{}{
			"id":      "msg_" + uuid.New().String(),
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]interface{}{{"type": "output_text", "text": text}},
			"status":  status,
		})
	}
	return map[string]interface{}{
		"id":          id,
		"object":      "response",
		"created_at":  time.Now().Unix(),
		"status":      status,
		"model":       model,
		"output":      output,
		"output_text": outputText,
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  inputTokens + outputTokens,
		},
	}
}

func (h *Handler) sendOpenAIError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"type":    errType,
			"message": message,
		},
	})
}

// ensureValidToken 确保 token 有效
func (h *Handler) ensureValidToken(account *config.Account) error {
	if account.ExpiresAt == 0 || time.Now().Unix() < account.ExpiresAt-tokenRefreshSkewSeconds {
		return nil
	}

	h.tokenRefreshMu.Lock()
	if h.tokenRefreshes == nil {
		h.tokenRefreshes = make(map[string]*tokenRefreshCall)
	}
	if call := h.tokenRefreshes[account.ID]; call != nil {
		h.tokenRefreshMu.Unlock()
		<-call.done
		if call.err == nil {
			h.syncLatestAccountToken(account)
		}
		return call.err
	}
	call := &tokenRefreshCall{done: make(chan struct{})}
	h.tokenRefreshes[account.ID] = call
	h.tokenRefreshMu.Unlock()

	call.err = h.refreshExpiredToken(account)

	h.tokenRefreshMu.Lock()
	delete(h.tokenRefreshes, account.ID)
	h.tokenRefreshMu.Unlock()
	close(call.done)

	return call.err
}

func (h *Handler) syncLatestAccountToken(account *config.Account) bool {
	if latest := h.pool.GetByID(account.ID); latest != nil {
		account.AccessToken = latest.AccessToken
		account.RefreshToken = latest.RefreshToken
		account.ExpiresAt = latest.ExpiresAt
		account.ProfileArn = latest.ProfileArn
		return account.ExpiresAt == 0 || time.Now().Unix() < account.ExpiresAt-tokenRefreshSkewSeconds
	}
	return false
}

func (h *Handler) refreshExpiredToken(account *config.Account) error {
	// Another concurrent request for the same account may have refreshed it
	// before this call became the owner.
	if h.syncLatestAccountToken(account) {
		return nil
	}

	accessToken, refreshToken, expiresAt, profileArn, err := auth.RefreshToken(account)
	if err != nil {
		return err
	}

	// 更新内存
	h.pool.UpdateToken(account.ID, accessToken, refreshToken, expiresAt)
	account.AccessToken = accessToken
	if refreshToken != "" {
		account.RefreshToken = refreshToken
	}
	account.ExpiresAt = expiresAt
	if profileArn != "" {
		account.ProfileArn = profileArn
		config.UpdateAccountProfileArn(account.ID, profileArn)
	}

	// 持久化
	config.UpdateAccountToken(account.ID, accessToken, refreshToken, expiresAt)

	return nil
}

// ==================== 管理 API ====================

func (h *Handler) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	// 验证密码
	password := r.Header.Get("X-Admin-Password")
	if password == "" {
		cookie, _ := r.Cookie("admin_password")
		if cookie != nil {
			password = cookie.Value
		}
	}

	if password != config.GetPassword() {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/admin/api")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch {
	case path == "/accounts" && r.Method == "GET":
		h.apiGetAccounts(w, r)
	case path == "/accounts" && r.Method == "POST":
		h.apiAddAccount(w, r)
	case path == "/accounts/batch" && r.Method == "POST":
		h.apiBatchAccounts(w, r)
	// models/refresh 必须在通用 /refresh 前匹配，否则会被误拦截
	case path == "/accounts/models/refresh" && r.Method == "POST":
		h.apiRefreshAllAccountsModels(w, r)
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/models/refresh") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/models/refresh")
		h.apiRefreshAccountModels(w, r, id)
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/refresh") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/refresh")
		h.apiRefreshAccount(w, r, id)
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/test") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/test")
		h.apiTestAccount(w, r, id)
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/models/cached") && r.Method == "GET":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/models/cached")
		h.apiGetAccountModelsCached(w, r, id)
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/models") && r.Method == "GET":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/models")
		h.apiGetAccountModels(w, r, id)

	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/full") && r.Method == "GET":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/full")
		h.apiGetAccountFull(w, r, id)
	case strings.HasPrefix(path, "/accounts/") && r.Method == "DELETE":
		h.apiDeleteAccount(w, r, strings.TrimPrefix(path, "/accounts/"))
	case strings.HasPrefix(path, "/accounts/") && r.Method == "PUT":
		h.apiUpdateAccount(w, r, strings.TrimPrefix(path, "/accounts/"))
	case path == "/auth/iam-sso/start" && r.Method == "POST":
		h.apiStartIamSso(w, r)
	case path == "/auth/iam-sso/complete" && r.Method == "POST":
		h.apiCompleteIamSso(w, r)
	case path == "/auth/builderid/start" && r.Method == "POST":
		h.apiStartBuilderIdLogin(w, r)
	case path == "/auth/builderid/poll" && r.Method == "POST":
		h.apiPollBuilderIdAuth(w, r)
	case path == "/auth/sso-token" && r.Method == "POST":
		h.apiImportSsoToken(w, r)
	case path == "/auth/credentials" && r.Method == "POST":
		h.apiImportCredentials(w, r)
	case path == "/status" && r.Method == "GET":
		h.apiGetStatus(w, r)
	case path == "/settings" && r.Method == "GET":
		h.apiGetSettings(w, r)
	case path == "/settings" && r.Method == "POST":
		h.apiUpdateSettings(w, r)
	case path == "/claude-code/compat" && r.Method == "GET":
		h.apiGetClaudeCodeCompatibility(w, r)
	case path == "/claude-code/model-readiness" && r.Method == "GET":
		h.apiGetClaudeCodeModelReadiness(w, r)
	case path == "/auto-refresh" && r.Method == "GET":
		h.apiGetAutoRefresh(w, r)
	case path == "/auto-refresh" && r.Method == "POST":
		h.apiUpdateAutoRefresh(w, r)
	case path == "/health-check" && r.Method == "GET":
		h.apiGetHealthCheck(w, r)
	case path == "/health-check" && r.Method == "POST":
		h.apiUpdateHealthCheck(w, r)
	case path == "/load-balance" && r.Method == "GET":
		h.apiGetLoadBalance(w, r)
	case path == "/load-balance" && r.Method == "POST":
		h.apiUpdateLoadBalance(w, r)
	case path == "/stats" && r.Method == "GET":
		h.apiGetStats(w, r)
	case path == "/stats/reset" && r.Method == "POST":
		h.apiResetStats(w, r)
	case path == "/request-logs" && r.Method == "GET":
		h.apiGetRequestLogs(w, r)
	case path == "/request-stats" && r.Method == "GET":
		h.apiGetRequestStats(w, r)
	case path == "/claude-code/readiness" && r.Method == "GET":
		h.apiGetClaudeCodeReadiness(w, r)
	case path == "/admission-pressure" && r.Method == "GET":
		h.apiGetAdmissionPressure(w, r)
	case path == "/request-logs/clear" && r.Method == "POST":
		h.apiClearRequestLogs(w, r)
	case path == "/generate-machine-id" && r.Method == "GET":
		h.apiGenerateMachineId(w, r)
	case path == "/thinking" && r.Method == "GET":
		h.apiGetThinkingConfig(w, r)
	case path == "/thinking" && r.Method == "POST":
		h.apiUpdateThinkingConfig(w, r)
	case path == "/endpoint" && r.Method == "GET":
		h.apiGetEndpointConfig(w, r)
	case path == "/endpoint" && r.Method == "POST":
		h.apiUpdateEndpointConfig(w, r)
	case path == "/proxy" && r.Method == "GET":
		h.apiGetProxy(w, r)
	case path == "/proxy" && r.Method == "POST":
		h.apiUpdateProxy(w, r)
	case path == "/prompt-filter" && r.Method == "GET":
		h.apiGetPromptFilter(w, r)
	case path == "/prompt-filter" && r.Method == "POST":
		h.apiUpdatePromptFilter(w, r)
	case path == "/version" && r.Method == "GET":
		h.apiGetVersion(w, r)
	case path == "/export" && r.Method == "POST":
		h.apiExportAccounts(w, r)
	default:
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "Not Found"})
	}
}

func (h *Handler) apiGetAccounts(w http.ResponseWriter, r *http.Request) {
	accounts := config.GetAccounts()
	poolAccounts := h.pool.GetAllAccounts()
	now := time.Now().Unix()

	// 合并运行时统计
	statsMap := make(map[string]config.Account)
	for _, a := range poolAccounts {
		statsMap[a.ID] = a
	}
	riskGroupSizes := make(map[string]int)
	riskGroupKeys := make(map[string]string, len(accounts))
	for _, a := range accounts {
		key := pool.AccountRiskGroupKey(a)
		riskGroupKeys[a.ID] = key
		if key != "" {
			riskGroupSizes[key]++
		}
	}

	// 隐藏敏感信息
	result := make([]map[string]interface{}, len(accounts))
	for i, a := range accounts {
		// 获取运行时统计
		stats := statsMap[a.ID]
		cooldownState := h.pool.CooldownState(a.ID, time.Now())
		coolingDown := cooldownState.CoolingDown || a.CooldownUntil > now
		cooldownRemaining := int64(0)
		if !cooldownState.RetryAt.IsZero() && cooldownState.RetryAt.Unix() > now {
			cooldownRemaining = cooldownState.RetryAt.Unix() - now
		} else if coolingDown {
			cooldownRemaining = a.CooldownUntil - now
		}
		riskGroupKey := riskGroupKeys[a.ID]
		lastFailureReason := a.LastFailureReason
		if cooldownState.RiskGroup && cooldownState.Reason != "" && cooldownState.Reason != pool.FailureReasonUnknown {
			lastFailureReason = string(cooldownState.Reason)
		}
		cooldownUntil := a.CooldownUntil
		if cooldownState.RiskGroup && !cooldownState.RetryAt.IsZero() && cooldownState.RetryAt.Unix() > cooldownUntil {
			cooldownUntil = cooldownState.RetryAt.Unix()
		}

		result[i] = map[string]interface{}{
			"id":                       a.ID,
			"email":                    a.Email,
			"userId":                   a.UserId,
			"nickname":                 a.Nickname,
			"authMethod":               a.AuthMethod,
			"provider":                 a.Provider,
			"region":                   a.Region,
			"enabled":                  a.Enabled,
			"banStatus":                a.BanStatus,
			"banReason":                a.BanReason,
			"banTime":                  a.BanTime,
			"lastFailureReason":        lastFailureReason,
			"lastFailureAt":            a.LastFailureAt,
			"cooldownUntil":            cooldownUntil,
			"coolingDown":              coolingDown,
			"cooldownRemainingSeconds": cooldownRemaining,
			"cooldownSource":           map[bool]string{true: "risk_group", false: "account"}[cooldownState.RiskGroup],
			"failureCount":             a.FailureCount,
			"riskGroupKey":             riskGroupKey,
			"riskGroupSize":            riskGroupSizes[riskGroupKey],
			"expiresAt":                a.ExpiresAt,
			"hasToken":                 a.AccessToken != "",
			"machineId":                a.MachineId,
			"weight":                   a.Weight,
			"allowOverage":             a.AllowOverage,
			"overageWeight":            a.OverageWeight,
			"proxyURL":                 a.ProxyURL,
			"subscriptionType":         a.SubscriptionType,
			"subscriptionTitle":        a.SubscriptionTitle,
			"daysRemaining":            a.DaysRemaining,
			"usageCurrent":             a.UsageCurrent,
			"usageLimit":               a.UsageLimit,
			"usagePercent":             a.UsagePercent,
			"nextResetDate":            a.NextResetDate,
			"lastRefresh":              a.LastRefresh,
			"trialUsageCurrent":        a.TrialUsageCurrent,
			"trialUsageLimit":          a.TrialUsageLimit,
			"trialUsagePercent":        a.TrialUsagePercent,
			"trialStatus":              a.TrialStatus,
			"trialExpiresAt":           a.TrialExpiresAt,
			"requestCount":             stats.RequestCount,
			"errorCount":               stats.ErrorCount,
			"totalTokens":              stats.TotalTokens,
			"totalCredits":             stats.TotalCredits,
			"lastUsed":                 stats.LastUsed,
			"runtimeHealth":            h.pool.GetRuntimeHealth(a.ID),
		}
	}
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) apiAddAccount(w http.ResponseWriter, r *http.Request) {
	var account config.Account
	if err := json.NewDecoder(r.Body).Decode(&account); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	if account.ID == "" {
		account.ID = auth.GenerateAccountID()
	}
	if account.Region == "" {
		account.Region = "us-east-1"
	}

	if err := config.AddAccount(account); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.pool.Reload()
	// 新账号若已启用且有 token，立即拉取并缓存模型列表
	if account.Enabled && account.AccessToken != "" {
		go func(acc config.Account) {
			if err := h.fetchAndCacheAccountModels(&acc); err != nil {
				logger.Warnf("[ModelsCache] Auto-refresh failed for new account %s: %v", acc.Email, err)
			}
		}(account)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "id": account.ID})
}

func (h *Handler) apiDeleteAccount(w http.ResponseWriter, r *http.Request, id string) {
	if err := config.DeleteAccount(id); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	h.pool.Reload()
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) apiUpdateAccount(w http.ResponseWriter, r *http.Request, id string) {
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	// 获取现有账号
	accounts := config.GetAccounts()
	var existing *config.Account
	for i := range accounts {
		if accounts[i].ID == id {
			existing = &accounts[i]
			break
		}
	}
	if existing == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}

	// 只更新传入的字段
	oldEnabled := existing.Enabled
	if v, ok := updates["enabled"].(bool); ok {
		existing.Enabled = v
	}
	if v, ok := updates["nickname"].(string); ok {
		existing.Nickname = v
	}
	if v, ok := updates["machineId"].(string); ok {
		existing.MachineId = v
	}
	if v, ok := updates["weight"].(float64); ok {
		existing.Weight = int(v)
	}
	if v, ok := updates["allowOverage"].(bool); ok {
		existing.AllowOverage = v
	}
	if v, ok := updates["overageWeight"].(float64); ok {
		existing.OverageWeight = clampInt(int(v), 1, 10)
	}
	if v, ok := updates["proxyURL"].(string); ok {
		existing.ProxyURL = v
	}

	if err := config.UpdateAccount(id, *existing); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.pool.Reload()
	// 账号从禁用→启用时，自动拉取并缓存模型列表
	if !oldEnabled && existing.Enabled && existing.AccessToken != "" {
		go func(acc config.Account) {
			if err := h.fetchAndCacheAccountModels(&acc); err != nil {
				logger.Warnf("[ModelsCache] Auto-refresh failed for re-enabled account %s: %v", acc.Email, err)
			}
		}(*existing)
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// apiBatchAccounts 批量操作账号（启用/禁用/刷新）
func (h *Handler) apiBatchAccounts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"` // "enable", "disable", "refresh"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	if len(req.IDs) == 0 {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "No account IDs provided"})
		return
	}

	switch req.Action {
	case "enable", "disable":
		enabled := req.Action == "enable"
		accounts := config.GetAccounts()
		idSet := make(map[string]bool)
		for _, id := range req.IDs {
			idSet[id] = true
		}
		var toRefreshModels []config.Account
		for _, a := range accounts {
			if idSet[a.ID] {
				// 记录本次从禁用→启用、且有 token 的账号
				if enabled && !a.Enabled && a.AccessToken != "" {
					toRefreshModels = append(toRefreshModels, a)
				}
				a.Enabled = enabled
				if enabled && a.BanStatus != "" && a.BanStatus != "ACTIVE" {
					a.BanStatus = "ACTIVE"
					a.BanReason = ""
					a.BanTime = 0
				}
				config.UpdateAccount(a.ID, a)
			}
		}
		h.pool.Reload()
		// 为本次新启用的账号异步拉取模型缓存
		for _, acc := range toRefreshModels {
			go func(a config.Account) {
				a.Enabled = true
				if err := h.fetchAndCacheAccountModels(&a); err != nil {
					logger.Warnf("[ModelsCache] Auto-refresh failed for batch-enabled account %s: %v", a.Email, err)
				}
			}(acc)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "count": len(req.IDs)})

	case "refresh":
		successCount := 0
		failCount := 0
		for _, id := range req.IDs {
			accounts := config.GetAccounts()
			var account *config.Account
			for i := range accounts {
				if accounts[i].ID == id {
					account = &accounts[i]
					break
				}
			}
			if account == nil {
				failCount++
				continue
			}
			if _, err := refreshAccountData(account); err != nil {
				logger.Warnf("[AdminAPI] Failed to refresh %s: %v", account.Email, err)
				failCount++
				continue
			}
			successCount++
		}
		h.pool.Reload()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"refreshed": successCount,
			"failed":    failCount,
		})

	default:
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid action: " + req.Action})
	}
}

func (h *Handler) apiStartIamSso(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StartUrl string `json:"startUrl"`
		Region   string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	if req.StartUrl == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "startUrl is required"})
		return
	}

	sessionID, authorizeUrl, expiresIn, err := auth.StartIamSsoLogin(req.StartUrl, req.Region)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessionId":    sessionID,
		"authorizeUrl": authorizeUrl,
		"expiresIn":    expiresIn,
	})
}

func (h *Handler) apiCompleteIamSso(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string `json:"sessionId"`
		CallbackUrl string `json:"callbackUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	accessToken, refreshToken, clientID, clientSecret, region, expiresIn, err := auth.CompleteIamSsoLogin(req.SessionID, req.CallbackUrl)
	if err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// 获取用户信息
	email, _, _ := auth.GetUserInfo(accessToken)

	// 创建账号
	account := config.Account{
		ID:           auth.GenerateAccountID(),
		Email:        email,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthMethod:   "idc",
		Region:       region,
		ExpiresAt:    time.Now().Unix() + int64(expiresIn),
		Enabled:      true,
		MachineId:    config.GenerateMachineId(),
	}

	if err := config.AddAccount(account); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.pool.Reload()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"account": map[string]interface{}{
			"id":    account.ID,
			"email": account.Email,
		},
	})
}

func (h *Handler) apiStartBuilderIdLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Region string `json:"region"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	session, err := auth.StartBuilderIdLogin(req.Region)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessionId":       session.ID,
		"userCode":        session.UserCode,
		"verificationUri": session.VerificationUri,
		"interval":        session.Interval,
	})
}

func (h *Handler) apiPollBuilderIdAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	accessToken, refreshToken, clientID, clientSecret, region, expiresIn, status, err := auth.PollBuilderIdAuth(req.SessionID)
	if err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if status == "pending" || status == "slow_down" {
		// 获取当前间隔
		interval := 5
		if session := auth.GetBuilderIdSession(req.SessionID); session != nil {
			interval = session.Interval
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"completed": false,
			"status":    status,
			"interval":  interval,
		})
		return
	}

	// 授权完成，获取用户信息
	email, _, _ := auth.GetUserInfo(accessToken)

	// 创建账号
	account := config.Account{
		ID:           auth.GenerateAccountID(),
		Email:        email,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthMethod:   "idc",
		Provider:     "BuilderId",
		Region:       region,
		ExpiresAt:    time.Now().Unix() + int64(expiresIn),
		Enabled:      true,
		MachineId:    config.GenerateMachineId(),
	}

	if err := config.AddAccount(account); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.pool.Reload()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"completed": true,
		"account": map[string]interface{}{
			"id":    account.ID,
			"email": account.Email,
		},
	})
}

func (h *Handler) apiImportSsoToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BearerToken string `json:"bearerToken"`
		Region      string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	if req.BearerToken == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "bearerToken is required"})
		return
	}

	// 支持批量导入，按行分割
	tokens := strings.Split(strings.TrimSpace(req.BearerToken), "\n")
	var imported []map[string]interface{}
	var errors []string

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}

		accessToken, refreshToken, clientID, clientSecret, expiresIn, err := auth.ImportFromSsoToken(token, req.Region)
		if err != nil {
			errors = append(errors, err.Error())
			continue
		}

		// 获取用户信息
		email, _, _ := auth.GetUserInfo(accessToken)

		// 创建账号
		account := config.Account{
			ID:           auth.GenerateAccountID(),
			Email:        email,
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			AuthMethod:   "idc",
			Region:       req.Region,
			ExpiresAt:    time.Now().Unix() + int64(expiresIn),
			Enabled:      true,
			MachineId:    config.GenerateMachineId(),
		}

		if err := config.AddAccount(account); err != nil {
			errors = append(errors, err.Error())
			continue
		}

		imported = append(imported, map[string]interface{}{
			"id":    account.ID,
			"email": account.Email,
		})
	}

	h.pool.Reload()

	if len(imported) == 0 && len(errors) > 0 {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   strings.Join(errors, "; "),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"accounts": imported,
		"errors":   errors,
	})
}

func (h *Handler) apiImportCredentials(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		AuthMethod   string `json:"authMethod"`
		Provider     string `json:"provider"`
		Region       string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	if req.RefreshToken == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "refreshToken is required"})
		return
	}

	// 设置默认值
	if req.Region == "" {
		req.Region = "us-east-1"
	}
	if req.AuthMethod == "" {
		if req.ClientID != "" {
			req.AuthMethod = "idc"
		} else {
			req.AuthMethod = "social"
		}
	}
	// 标准化 authMethod
	switch strings.ToLower(req.AuthMethod) {
	case "idc", "builderid", "enterprise":
		req.AuthMethod = "idc"
	case "social", "google", "github":
		req.AuthMethod = "social"
	default:
		if req.ClientID != "" && req.ClientSecret != "" {
			req.AuthMethod = "idc"
		} else {
			req.AuthMethod = "social"
		}
	}

	// 始终尝试用 refreshToken 刷新获取新的 accessToken
	var accessToken string
	var expiresAt int64
	tempAccount := &config.Account{
		RefreshToken: req.RefreshToken,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		AuthMethod:   req.AuthMethod,
		Region:       req.Region,
	}
	newAccessToken, newRefreshToken, newExpiresAt, newProfileArn, err := auth.RefreshToken(tempAccount)
	if err != nil {
		// 刷新失败，如果有传入的 accessToken 则尝试使用
		if req.AccessToken != "" {
			accessToken = req.AccessToken
			expiresAt = time.Now().Unix() + 300 // 可能已过期，设短一点
		} else {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "Token refresh failed: " + err.Error()})
			return
		}
	} else {
		accessToken = newAccessToken
		if newRefreshToken != "" {
			req.RefreshToken = newRefreshToken
		}
		expiresAt = newExpiresAt
	}

	// 获取用户信息
	email, _, _ := auth.GetUserInfo(accessToken)

	// 创建账号
	account := config.Account{
		ID:           auth.GenerateAccountID(),
		Email:        email,
		AccessToken:  accessToken,
		RefreshToken: req.RefreshToken,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		AuthMethod:   req.AuthMethod,
		Provider:     req.Provider,
		Region:       req.Region,
		ExpiresAt:    expiresAt,
		Enabled:      true,
		MachineId:    config.GenerateMachineId(),
		ProfileArn:   newProfileArn,
	}

	if err := config.AddAccount(account); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	h.pool.Reload()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"account": map[string]interface{}{
			"id":    account.ID,
			"email": account.Email,
		},
	})
}

func (h *Handler) apiGetStatus(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"accounts":        h.pool.Count(),
		"available":       h.pool.AvailableCount(),
		"totalRequests":   h.totalRequests,
		"successRequests": h.successRequests,
		"failedRequests":  h.failedRequests,
		"totalTokens":     h.totalTokens,
		"totalCredits":    h.totalCredits,
		"uptime":          time.Now().Unix() - h.startTime,
	})
}

func (h *Handler) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"apiKey":            config.GetApiKey(),
		"requireApiKey":     config.IsApiKeyRequired(),
		"clientApiKeys":     config.GetClientAccessConfig().ClientApiKeys,
		"clientIPAllowlist": config.GetClientAccessConfig().ClientIPAllowlist,
		"modelMappings":     config.GetClientAccessConfig().ModelMappings,
		"modelAdmission":    config.GetModelAdmissionConfig(),
		"port":              config.GetPort(),
		"host":              config.GetHost(),
		"allowOverUsage":    config.GetAllowOverUsage(),
	})
}

func (h *Handler) apiGetClaudeCodeCompatibility(w http.ResponseWriter, r *http.Request) {
	baseURL := buildClaudeCodeBaseURL(r)
	apiKey := config.GetApiKey()
	clientKeys := config.GetClientApiKeys()
	if len(clientKeys) > 0 {
		apiKey = clientKeys[0]
	}
	if !config.IsApiKeyRequired() {
		apiKey = "any"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"baseUrl": baseURL,
		"environment": map[string]string{
			"ANTHROPIC_BASE_URL":                             baseURL,
			"ANTHROPIC_AUTH_TOKEN":                           apiKey,
			"ANTHROPIC_API_KEY":                              apiKey,
			"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY":     "1",
			"CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING": "1",
			"ENABLE_TOOL_SEARCH":                             "true",
		},
		"capabilities": map[string]bool{
			"anthropicMessages":        true,
			"countTokens":              true,
			"openAIChat":               true,
			"openAIResponses":          true,
			"models":                   true,
			"streaming":                true,
			"toolUse":                  true,
			"toolReferences":           true,
			"vision":                   true,
			"webSearch":                true,
			"webSearch20260209":        true,
			"thinking":                 true,
			"promptCaching":            true,
			"promptCacheControl":       true,
			"requestLogs":              true,
			"modelAdmission":           true,
			"adaptiveModelAdmission":   true,
			"fineGrainedToolStreaming": true,
			"toolSearch":               true,
		},
		"endpoints": []string{
			"/v1/messages",
			"/v1/messages/count_tokens",
			"/v1/models",
			"/v1/chat/completions",
			"/v1/responses",
			"/v1/stats",
		},
		"modelAdmission": config.GetModelAdmissionConfig(),
		"sub2apiNotes": []string{
			"Keep sub2api user/account concurrency above expected Claude Code parallelism so requests are not rejected before reaching Kiro-Go.",
			"For local production validation, preserve the existing sub2api upstream base_url and API key; this endpoint is informational only.",
			"Use Kiro-Go request logs to separate downstream admission failures from upstream Kiro errors.",
		},
	})
}

func (h *Handler) apiGetClaudeCodeModelReadiness(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimSpace(r.URL.Query().Get("model"))
	if requested == "" {
		requested = "claude-sonnet-4.5"
	}
	thinkingCfg := config.GetThinkingConfig()
	mapped, thinking := resolveClaudeThinkingMode(requested, nil, thinkingCfg.Suffix)
	h.modelsCacheMu.RLock()
	cached := append([]ModelInfo(nil), h.cachedModels...)
	h.modelsCacheMu.RUnlock()
	listed, supportsImage := modelListedAndVision(cached, mapped)
	accountRows, enabledCount, schedulableCount := h.claudeCodeModelReadinessAccountRows(mapped)
	routingReason := "no enabled accounts"
	if enabledCount > 0 {
		routingReason = "accounts evaluated"
	}
	if schedulableCount > 0 {
		routingReason = "schedulable accounts available"
	}
	summary := claudeCodeModelReadinessSummary(listed, accountRows)
	resp := map[string]interface{}{
		"requestedModel":    requested,
		"mappedModel":       mapped,
		"thinking":          thinking,
		"thinkingVariant":   thinking || strings.HasSuffix(strings.ToLower(requested), strings.ToLower(thinkingCfg.Suffix)),
		"listedByGateway":   listed,
		"routingReason":     routingReason,
		"accounts":          accountRows,
		"admissionPressure": h.admissionPressureForModel(mapped),
		"summary":           summary,
		"capabilities": map[string]interface{}{
			"vision":    supportsImage,
			"toolUse":   true,
			"thinking":  true,
			"webSearch": true,
		},
		"reason": modelReadinessReason(listed),
	}
	json.NewEncoder(w).Encode(resp)
}

func claudeCodeModelReadinessSummary(modelListed bool, rows []map[string]interface{}) map[string]interface{} {
	summary := map[string]interface{}{
		"modelListed":          modelListed,
		"accountsEvaluated":    len(rows),
		"locallySchedulable":   0,
		"riskGroupCoolingDown": 0,
		"generationBlocked":    0,
	}
	for _, row := range rows {
		if schedulable, _ := row["schedulable"].(bool); schedulable {
			summary["locallySchedulable"] = summary["locallySchedulable"].(int) + 1
		}
		if coolingDown, _ := row["coolingDown"].(bool); coolingDown {
			summary["generationBlocked"] = summary["generationBlocked"].(int) + 1
		}
		if source, _ := row["cooldownSource"].(string); source == "risk_group" {
			if coolingDown, _ := row["coolingDown"].(bool); coolingDown {
				summary["riskGroupCoolingDown"] = summary["riskGroupCoolingDown"].(int) + 1
			}
		}
	}
	return summary
}

func (h *Handler) admissionPressureForModel(model string) map[string]interface{} {
	if modelAdmissionGate == nil {
		return map[string]interface{}{"active": false}
	}
	normalizedModel := normalizeAdmissionModel(model)
	for _, snap := range modelAdmissionGate.snapshot() {
		if normalizeAdmissionModel(snap.Model) == normalizedModel {
			return map[string]interface{}{
				"model":                  snap.Model,
				"active":                 snap.Active,
				"score":                  snap.Score,
				"reducedConcurrency":     snap.ReducedConcurrency,
				"maxConcurrent":          snap.MaxConcurrent,
				"effectiveMaxConcurrent": snap.EffectiveMaxConcurrent,
				"queueDepth":             snap.QueueDepth,
				"activeRequests":         snap.ActiveRequests,
				"recentCapacityErrors":   snap.RecentCapacityErrors,
				"recentQueueTimeouts":    snap.RecentQueueTimeouts,
				"recentSuccesses":        snap.RecentSuccesses,
				"expiresInMs":            snap.ExpiresInMs,
			}
		}
	}
	return map[string]interface{}{"active": false, "model": normalizedModel}
}

func (h *Handler) claudeCodeModelReadinessAccountRows(model string) ([]map[string]interface{}, int, int) {
	accounts := config.GetAccounts()
	rows := make([]map[string]interface{}, 0, len(accounts))
	enabledCount := 0
	schedulableCount := 0
	now := time.Now().Unix()

	for _, account := range accounts {
		if account.Enabled {
			enabledCount++
		}
		healthy := account.Enabled
		cooldownState := pool.CooldownState{}
		if h.pool != nil {
			cooldownState = h.pool.CooldownState(account.ID, time.Now())
		}
		if cooldownState.CoolingDown || (account.CooldownUntil > 0 && now < account.CooldownUntil) {
			healthy = false
		}
		if account.ExpiresAt > 0 && now > account.ExpiresAt-tokenRefreshSkewSeconds {
			healthy = false
		}
		usageBlocked := readinessAccountUsageBlocked(account)

		listsModel := true
		if h.pool != nil {
			models := h.pool.GetModelList(account.ID)
			if len(models) > 0 {
				listsModel = false
				for _, candidate := range models {
					if strings.EqualFold(strings.TrimSpace(candidate), model) {
						listsModel = true
						break
					}
				}
			}
		}

		schedulable := account.Enabled && healthy && listsModel && !usageBlocked
		if schedulable {
			schedulableCount++
		}
		reason := "schedulable"
		switch {
		case !account.Enabled:
			reason = "disabled account"
		case cooldownState.Reason == pool.FailureReasonTemporaryLimited && cooldownState.RiskGroup:
			reason = "temporary limited risk group cooling down"
		case cooldownState.Reason == pool.FailureReasonTemporaryLimited:
			reason = "temporary limited account cooling down"
		case !healthy:
			reason = "unhealthy account"
		case usageBlocked:
			reason = "usage limit reached"
		case !listsModel:
			reason = "model not listed"
		}

		cooldownRemaining := int64(0)
		if !cooldownState.RetryAt.IsZero() && cooldownState.RetryAt.Unix() > now {
			cooldownRemaining = cooldownState.RetryAt.Unix() - now
		} else if account.CooldownUntil > now {
			cooldownRemaining = account.CooldownUntil - now
		}
		rows = append(rows, map[string]interface{}{
			"id":          account.ID,
			"email":       maskReadinessEmail(account.Email),
			"enabled":     account.Enabled,
			"healthy":     healthy,
			"listsModel":  listsModel,
			"schedulable": schedulable,
			"reason":      reason,
			"coolingDown": cooldownState.CoolingDown,
			"cooldownSource": map[bool]string{
				true:  "risk_group",
				false: "account",
			}[cooldownState.RiskGroup],
			"cooldownRemainingSeconds": cooldownRemaining,
			"lastFailureReason":        string(cooldownState.Reason),
		})
	}

	return rows, enabledCount, schedulableCount
}

func readinessAccountUsageBlocked(account config.Account) bool {
	if account.UsageLimit <= 0 || account.UsageCurrent < account.UsageLimit {
		return false
	}
	return !account.AllowOverage
}

func maskReadinessEmail(email string) string {
	email = strings.TrimSpace(email)
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return email
	}
	local := parts[0]
	switch {
	case len(local) <= 2:
		local = "***"
	default:
		local = local[:1] + "***"
	}
	return local + "@" + parts[1]
}

func modelListedAndVision(models []ModelInfo, model string) (bool, bool) {
	for _, m := range models {
		if strings.EqualFold(m.ModelId, model) {
			return true, modelSupportsImage(m.InputTypes)
		}
	}
	return false, false
}

func modelReadinessReason(listed bool) string {
	if listed {
		return "model listed by Kiro-Go model cache"
	}
	return "model not found in current Kiro-Go model cache"
}

func layeredCapability(status, detail string, compat map[string]string, official map[string]string, evidence map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"status":                  status,
		"detail":                  detail,
		"claudeCodeCompatibility": compat,
		"officialAnthropicParity": official,
	}
	if evidence == nil {
		evidence = map[string]interface{}{}
	}
	out["evidence"] = evidence
	return out
}

func basicCapability(status, detail string) map[string]interface{} {
	return map[string]interface{}{
		"status": status,
		"detail": detail,
	}
}

func readinessEvidence(entry *RequestLogEntry, mode string, proof string) map[string]interface{} {
	if entry == nil {
		return map[string]interface{}{
			"mode":  mode,
			"proof": proof,
		}
	}
	return map[string]interface{}{
		"lastSeenAt":    entry.Timestamp.Format(time.RFC3339),
		"lastRequestId": entry.RequestID,
		"model":         entry.Model,
		"mode":          mode,
		"proof":         proof,
	}
}

func modelDisallowsAssistantPrefill(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "opus-4.7") ||
		strings.Contains(model, "opus-4-7") ||
		strings.Contains(model, "opus-4.6") ||
		strings.Contains(model, "opus-4-6") ||
		strings.Contains(model, "sonnet-4.6") ||
		strings.Contains(model, "sonnet-4-6")
}

func (h *Handler) apiGetClaudeCodeReadiness(w http.ResponseWriter, r *http.Request) {
	logs := h.ensureRequestLogStore().List(maxRequestLogLimit)
	cutoff := time.Now().Add(-30 * time.Minute)
	resp := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"messages": basicCapability("PASS", "/v1/messages is implemented"),
			"countTokens": layeredCapability(
				"PARTIAL",
				"Claude Code compatible estimated token counting; official exact count is not proven",
				map[string]string{"status": "PASS", "mode": "estimated", "proof": "count_tokens endpoint is implemented"},
				map[string]string{"status": "PARTIAL", "mode": "estimated", "proof": "no upstream exact count_tokens evidence"},
				readinessEvidence(nil, "estimated", "no recent count_tokens request in readiness window"),
			),
			"maxTokensZero": layeredCapability(
				"PARTIAL",
				"Claude Code compatible zero-output response; official cache warmup is not proven",
				map[string]string{"status": "PASS", "mode": "local_zero_output", "proof": "local zero-output response shape is implemented"},
				map[string]string{"status": "BLOCKED_BY_UPSTREAM", "mode": "local_zero_output", "proof": "no upstream cache warmup evidence"},
				readinessEvidence(nil, "local_zero_output", "no recent max_tokens=0 request in readiness window"),
			),
			"assistantPrefill": layeredCapability(
				"PARTIAL",
				"Text prefill is emulated as a continuation instruction; tool-use prefill is rejected",
				map[string]string{"status": "EMULATED_PASS", "mode": "emulated_text_prefill", "proof": "text prefill conversion is implemented"},
				map[string]string{"status": "PARTIAL", "mode": "emulated_text_prefill", "proof": "native upstream prefill is not proven"},
				readinessEvidence(nil, "emulated_text_prefill", "no recent assistant text prefill request in readiness window"),
			),
			"fineGrainedToolStreaming": layeredCapability(
				"PARTIAL",
				"Claude Code compatible input_json_delta events are emitted; true upstream partial JSON parity depends on Kiro stream shape",
				map[string]string{"status": "PASS", "mode": "kiro_go_chunked_complete_input", "proof": "Anthropic SSE input_json_delta writer is implemented"},
				map[string]string{"status": "PARTIAL", "mode": "kiro_go_chunked_complete_input", "proof": "upstream partial tool input deltas are not proven"},
				readinessEvidence(nil, "kiro_go_chunked_complete_input", "no recent fine-grained tool stream in readiness window"),
			),
			"toolSchemaValidation": basicCapability("PASS", "Invalid model-emitted tool_use inputs are repaired or suppressed before Claude Code receives them"),
			"toolReference":        basicCapability("PASS", "tool_reference is accepted and materialized when relevant"),
			"opus47AdaptiveAdmission": basicCapability(
				"PASS",
				"Opus 4.7 model-capacity pressure is tracked separately from account health and can reduce effective concurrency",
			),
		},
		"recentClaudeCode":               false,
		"recentToolReferences":           false,
		"recentMCPTools":                 false,
		"recentToolTrimming":             false,
		"recentResponsesRestore":         false,
		"recentToolResultTurns":          false,
		"recentParentAgents":             false,
		"recentToolResultImages":         false,
		"recentOrphanedToolResults":      false,
		"recentUnsupportedBlocks":        false,
		"recentFineGrainedToolStreaming": false,
		"recentSuppressedToolUses":       false,
		"recentAdmissionPressure":        false,
		"recentContextReminders":         []string{},
		"lastSeen":                       "",
		"examples":                       []map[string]interface{}{},
	}
	examples := make([]map[string]interface{}, 0, 5)
	reminderSet := make(map[string]bool)
	var recentCountTokens, recentMaxTokensZero, recentFineGrained, recentAssistantPrefill *RequestLogEntry
	for _, entry := range logs {
		if entry.Timestamp.Before(cutoff) {
			continue
		}
		betas := strings.ToLower(strings.Join(entry.AnthropicBetas, ","))
		if entry.ClaudeCodeSessionID != "" || strings.Contains(betas, "tool") || strings.Contains(betas, "claude-code") {
			resp["recentClaudeCode"] = true
			if resp["lastSeen"] == "" {
				resp["lastSeen"] = entry.Timestamp.Format(time.RFC3339)
			}
		}
		if entry.ToolReferenceCount > 0 || len(entry.PayloadMaterializedToolRefs) > 0 || len(entry.PayloadDeferredTools) > 0 {
			resp["recentToolReferences"] = true
		}
		if containsMCPToolName(entry.PayloadKeptTools) || containsMCPToolName(entry.PayloadTrimmedTools) || containsMCPToolName(entry.PayloadMaterializedToolRefs) || containsMCPToolName(entry.PayloadDeferredTools) {
			resp["recentMCPTools"] = true
		}
		if entry.PayloadTrimmed || len(entry.PayloadTrimmedTools) > 0 {
			resp["recentToolTrimming"] = true
		}
		if strings.Contains(entry.PayloadCurrentMessageShape, "tool_result") || entry.PayloadCompactedToolResults > 0 {
			resp["recentToolResultTurns"] = true
		}
		if entry.ClaudeCodeParentAgentID != "" {
			resp["recentParentAgents"] = true
		}
		if entry.PayloadToolResultImages > 0 {
			resp["recentToolResultImages"] = true
		}
		if entry.PayloadOrphanedToolResultsConverted > 0 {
			resp["recentOrphanedToolResults"] = true
		}
		if len(entry.PayloadUnsupportedContentBlocks) > 0 {
			resp["recentUnsupportedBlocks"] = true
		}
		if entry.FineGrainedToolStreamingRequested {
			resp["recentFineGrainedToolStreaming"] = true
		}
		if entry.SuppressedToolUseCount > 0 {
			resp["recentSuppressedToolUses"] = true
		}
		if entry.AdmissionPressureScore > 0 || entry.EffectiveConcurrentLimit > 0 {
			resp["recentAdmissionPressure"] = true
		}
		entryCopy := entry
		if entry.CountTokensMode != "" && recentCountTokens == nil {
			recentCountTokens = &entryCopy
		}
		if entry.MaxTokensZeroMode != "" && recentMaxTokensZero == nil {
			recentMaxTokensZero = &entryCopy
		}
		if entry.FineGrainedToolStreamingMode != "" && recentFineGrained == nil {
			recentFineGrained = &entryCopy
		}
		if entry.AssistantPrefillMode != "" && recentAssistantPrefill == nil {
			recentAssistantPrefill = &entryCopy
		}
		for _, kind := range entry.PayloadContextReminderKinds {
			kind = strings.TrimSpace(kind)
			if kind != "" {
				reminderSet[kind] = true
			}
		}
		if len(examples) < 5 {
			examples = append(examples, map[string]interface{}{
				"timestamp":                    entry.Timestamp,
				"endpoint":                     entry.Endpoint,
				"model":                        entry.Model,
				"currentMessageShape":          entry.PayloadCurrentMessageShape,
				"contextReminderKinds":         append([]string(nil), entry.PayloadContextReminderKinds...),
				"parentAgentId":                entry.ClaudeCodeParentAgentID,
				"unsupportedContentBlocks":     append([]string(nil), entry.PayloadUnsupportedContentBlocks...),
				"fineGrainedToolStreamingMode": entry.FineGrainedToolStreamingMode,
				"suppressedToolUseCount":       entry.SuppressedToolUseCount,
				"suppressedToolUseNames":       append([]string(nil), entry.SuppressedToolUseNames...),
			})
		}
	}
	reminders := make([]string, 0, len(reminderSet))
	for kind := range reminderSet {
		reminders = append(reminders, kind)
	}
	sort.Strings(reminders)
	resp["recentContextReminders"] = reminders
	resp["examples"] = examples
	capabilities := resp["capabilities"].(map[string]interface{})
	if recentCountTokens != nil {
		capabilities["countTokens"] = layeredCapability(
			"PARTIAL",
			"Claude Code compatible estimated token counting; official exact count is not proven",
			map[string]string{"status": "PASS", "mode": recentCountTokens.CountTokensMode, "proof": "count_tokens endpoint returned input_tokens"},
			map[string]string{"status": "PARTIAL", "mode": recentCountTokens.CountTokensMode, "proof": "no upstream exact count_tokens evidence"},
			readinessEvidence(recentCountTokens, recentCountTokens.CountTokensMode, "recent count_tokens request completed"),
		)
	}
	if recentMaxTokensZero != nil {
		officialStatus := "BLOCKED_BY_UPSTREAM"
		officialProof := "no upstream cache warmup evidence"
		if recentMaxTokensZero.CacheCreationInputTokens > 0 || recentMaxTokensZero.CacheReadInputTokens > 0 {
			officialStatus = "PASS"
			officialProof = "upstream cache usage tokens were observed"
		}
		capabilities["maxTokensZero"] = layeredCapability(
			"PARTIAL",
			"Claude Code compatible zero-output response; official cache warmup is not proven",
			map[string]string{"status": "PASS", "mode": recentMaxTokensZero.MaxTokensZeroMode, "proof": "zero-output response shape completed"},
			map[string]string{"status": officialStatus, "mode": recentMaxTokensZero.MaxTokensZeroMode, "proof": officialProof},
			readinessEvidence(recentMaxTokensZero, recentMaxTokensZero.MaxTokensZeroMode, "recent max_tokens=0 request completed"),
		)
	}
	if recentFineGrained != nil {
		capabilities["fineGrainedToolStreaming"] = layeredCapability(
			"PARTIAL",
			"Claude Code compatible input_json_delta events are emitted; true upstream partial JSON parity depends on Kiro stream shape",
			map[string]string{"status": "PASS", "mode": recentFineGrained.FineGrainedToolStreamingMode, "proof": "recent request asked for fine-grained tool streaming"},
			map[string]string{"status": "PARTIAL", "mode": recentFineGrained.FineGrainedToolStreamingMode, "proof": "upstream partial tool input deltas are not proven"},
			readinessEvidence(recentFineGrained, recentFineGrained.FineGrainedToolStreamingMode, "recent fine-grained tool-stream request observed"),
		)
	}
	if recentAssistantPrefill != nil {
		officialStatus := "PARTIAL"
		officialProof := "native upstream prefill is not proven"
		if modelDisallowsAssistantPrefill(recentAssistantPrefill.Model) {
			officialStatus = "UNSUPPORTED_BY_MODEL"
			officialProof = "official model family does not support assistant prefill"
		}
		capabilities["assistantPrefill"] = layeredCapability(
			"PARTIAL",
			"Text prefill is emulated as a continuation instruction; tool-use prefill is rejected",
			map[string]string{"status": "EMULATED_PASS", "mode": recentAssistantPrefill.AssistantPrefillMode, "proof": "recent assistant text prefill was converted"},
			map[string]string{"status": officialStatus, "mode": recentAssistantPrefill.AssistantPrefillMode, "proof": officialProof},
			readinessEvidence(recentAssistantPrefill, recentAssistantPrefill.AssistantPrefillMode, "recent assistant text prefill request observed"),
		)
	}
	json.NewEncoder(w).Encode(resp)
}

func containsMCPToolName(names []string) bool {
	for _, name := range names {
		lower := strings.ToLower(strings.TrimSpace(name))
		if strings.Contains(lower, "mcp__") || strings.HasPrefix(lower, "mcp") {
			return true
		}
	}
	return false
}

func buildClaudeCodeBaseURL(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = fmt.Sprintf("%s:%d", config.GetHost(), config.GetPort())
	}
	if strings.HasPrefix(host, "0.0.0.0:") || strings.HasPrefix(host, "[::]:") {
		_, port, err := net.SplitHostPort(host)
		if err == nil {
			host = "127.0.0.1:" + port
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + host
}

func (h *Handler) apiGetPromptFilter(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(config.GetPromptFilterConfig())
}

func (h *Handler) apiUpdatePromptFilter(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FilterClaudeCode      *bool                      `json:"filterClaudeCode,omitempty"`
		FilterEnvNoise        *bool                      `json:"filterEnvNoise,omitempty"`
		FilterStripBoundaries *bool                      `json:"filterStripBoundaries,omitempty"`
		Rules                 *[]config.PromptFilterRule `json:"rules,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	// Read current config to fill in any fields not provided in the request.
	current := config.GetPromptFilterConfig()
	fcc := current.FilterClaudeCode
	fen := current.FilterEnvNoise
	fsb := current.FilterStripBoundaries
	rules := current.Rules
	if req.FilterClaudeCode != nil {
		fcc = *req.FilterClaudeCode
	}
	if req.FilterEnvNoise != nil {
		fen = *req.FilterEnvNoise
	}
	if req.FilterStripBoundaries != nil {
		fsb = *req.FilterStripBoundaries
	}
	if req.Rules != nil {
		rules = *req.Rules
	}
	if err := config.UpdatePromptFilterConfig(fcc, fen, fsb, rules); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) apiUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApiKey            *string                      `json:"apiKey,omitempty"`
		RequireApiKey     *bool                        `json:"requireApiKey,omitempty"`
		ClientApiKeys     []string                     `json:"clientApiKeys,omitempty"`
		ClientIPAllowlist []string                     `json:"clientIPAllowlist,omitempty"`
		ModelMappings     []config.ModelMappingRule    `json:"modelMappings,omitempty"`
		ModelAdmission    *config.ModelAdmissionConfig `json:"modelAdmission,omitempty"`
		Password          string                       `json:"password"`
		AllowOverUsage    *bool                        `json:"allowOverUsage,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	currentAccess := config.GetClientAccessConfig()
	apiKey := currentAccess.ApiKey
	requireApiKey := currentAccess.RequireApiKey
	if req.ApiKey != nil {
		apiKey = *req.ApiKey
	}
	if req.RequireApiKey != nil {
		requireApiKey = *req.RequireApiKey
	}
	if req.ClientApiKeys == nil {
		req.ClientApiKeys = currentAccess.ClientApiKeys
	}
	if req.ClientIPAllowlist == nil {
		req.ClientIPAllowlist = currentAccess.ClientIPAllowlist
	}
	if req.ModelMappings == nil {
		req.ModelMappings = currentAccess.ModelMappings
	}
	if err := config.UpdateClientAccessConfig(config.ClientAccessConfig{
		ApiKey:            apiKey,
		RequireApiKey:     requireApiKey,
		ClientApiKeys:     req.ClientApiKeys,
		ClientIPAllowlist: req.ClientIPAllowlist,
		ModelMappings:     req.ModelMappings,
	}); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if req.ModelAdmission != nil {
		if err := config.UpdateModelAdmissionConfig(*req.ModelAdmission); err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		applyModelAdmissionConfig()
	}
	if req.Password != "" {
		if err := config.UpdateSettings(apiKey, requireApiKey, req.Password); err != nil {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
	}

	// 更新超额使用设置
	if req.AllowOverUsage != nil {
		if err := config.UpdateAllowOverUsage(*req.AllowOverUsage); err != nil {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) apiGetAutoRefresh(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"settings": config.GetAutoRefreshConfig(),
		"status":   h.getAutoRefreshStatus(),
	})
}

func (h *Handler) apiUpdateAutoRefresh(w http.ResponseWriter, r *http.Request) {
	var req config.AutoRefreshConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	if err := config.UpdateAutoRefreshConfig(req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if req.Enabled {
		go h.runAutoRefresh()
	} else {
		h.scheduleNextAutoRefresh()
	}
	h.notifyAutoRefreshUpdated()
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) apiGetHealthCheck(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"settings": config.GetHealthCheckConfig(),
		"status":   h.getHealthCheckStatus(),
	})
}

func (h *Handler) apiUpdateHealthCheck(w http.ResponseWriter, r *http.Request) {
	var req config.HealthCheckConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	if err := config.UpdateHealthCheckConfig(req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if req.Enabled {
		go h.runHealthCheck()
	} else {
		h.scheduleNextHealthCheck()
	}
	h.notifyHealthCheckUpdated()
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) apiGetStats(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"totalRequests":   atomic.LoadInt64(&h.totalRequests),
		"successRequests": atomic.LoadInt64(&h.successRequests),
		"failedRequests":  atomic.LoadInt64(&h.failedRequests),
		"totalTokens":     atomic.LoadInt64(&h.totalTokens),
		"totalCredits":    h.getCredits(),
		"uptime":          time.Now().Unix() - h.startTime,
	})
}

func (h *Handler) apiResetStats(w http.ResponseWriter, r *http.Request) {
	atomic.StoreInt64(&h.totalRequests, 0)
	atomic.StoreInt64(&h.successRequests, 0)
	atomic.StoreInt64(&h.failedRequests, 0)
	atomic.StoreInt64(&h.totalTokens, 0)
	h.creditsMu.Lock()
	h.totalCredits = 0
	h.creditsMu.Unlock()
	config.UpdateStats(0, 0, 0, 0, 0)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// apiGenerateMachineId 生成新的机器码
func (h *Handler) apiGenerateMachineId(w http.ResponseWriter, r *http.Request) {
	machineId := config.GenerateMachineId()
	json.NewEncoder(w).Encode(map[string]string{"machineId": machineId})
}

// apiTestAccount tests a specific account by sending a real model request through its proxy.
func (h *Handler) apiTestAccount(w http.ResponseWriter, r *http.Request, id string) {
	accounts := config.GetAccounts()
	var account *config.Account
	for i := range accounts {
		if accounts[i].ID == id {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}
	if retryAfter, throttled := h.reserveAccountTest(account.ID); throttled {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":             true,
			"status":              "test_throttled",
			"reason":              "admin_test_rate_limited",
			"message":             "Account generation test skipped to avoid triggering upstream limits",
			"retry_after_seconds": retryAfter,
		})
		return
	}
	if h.pool != nil {
		cooldownState := h.pool.CooldownState(account.ID, time.Now())
		if cooldownState.CoolingDown {
			retryAfter := 0
			reason := cooldownState.Reason
			if latest := h.pool.GetByID(account.ID); latest != nil {
				if reason == "" || reason == pool.FailureReasonUnknown {
					reason = pool.FailureReason(latest.LastFailureReason)
				}
				if cooldownState.RetryAt.IsZero() && latest.CooldownUntil > 0 {
					retryAfter = int(time.Until(time.Unix(latest.CooldownUntil, 0)).Seconds())
					if retryAfter < 1 {
						retryAfter = 1
					}
				}
			}
			if !cooldownState.RetryAt.IsZero() {
				retryAfter = int(time.Until(cooldownState.RetryAt).Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
			}
			if reason == "" {
				reason = pool.FailureReasonRateLimited
			}
			status := "cooling_down"
			if reason == pool.FailureReasonTemporaryLimited {
				status = "temporary_limited"
				if retryAfter < int(pool.TemporaryLimitRetryAfterFloor().Seconds()) {
					retryAfter = int(pool.TemporaryLimitRetryAfterFloor().Seconds())
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":             true,
				"status":              status,
				"reason":              string(reason),
				"message":             "Account is cooling down; test skipped to avoid triggering upstream limits",
				"retry_after_seconds": retryAfter,
				"cooldown_source":     map[bool]string{true: "risk_group", false: "account"}[cooldownState.RiskGroup],
			})
			return
		}
	}

	if err := h.ensureValidToken(account); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "Token refresh failed: " + err.Error()})
		return
	}

	// Parse test model from request body (optional)
	var req struct {
		Model string `json:"model"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Model == "" {
		req.Model = "claude-sonnet-4"
	}

	// Build a minimal chat payload
	thinkingCfg := config.GetThinkingConfig()
	actualModel, thinking := ParseModelAndThinking(req.Model, thinkingCfg.Suffix)

	openaiReq := &OpenAIRequest{
		Model:     actualModel,
		Messages:  []OpenAIMessage{{Role: "user", Content: "say ok"}},
		MaxTokens: 5,
		Stream:    false,
	}
	kiroPayload := OpenAIToKiro(openaiReq, thinking)

	var content string
	callback := &KiroStreamCallback{
		OnText:         func(text string, isThinking bool) { content += text },
		OnToolUse:      func(tu KiroToolUse) {},
		OnComplete:     func(inTok, outTok int) {},
		OnError:        func(err error) {},
		OnCredits:      func(c float64) {},
		OnContextUsage: func(pct float64) {},
	}

	err := CallKiroAPI(account, kiroPayload, callback)
	if err != nil {
		reason := h.recordAccountFailure(account.ID, err)
		if reason == pool.FailureReasonModelCapacity {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"status":  "capacity_busy",
				"reason":  string(reason),
				"message": err.Error(),
				"model":   req.Model,
			})
			return
		}
		if reason == pool.FailureReasonTemporaryLimited {
			headers := claudeErrorHeadersForUpstreamError(err)
			retryAfter := 0
			if rawRetryAfter := strings.TrimSpace(headers.Get("Retry-After")); rawRetryAfter != "" {
				if seconds, parseErr := strconv.Atoi(rawRetryAfter); parseErr == nil {
					retryAfter = seconds
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":             true,
				"status":              "temporary_limited",
				"reason":              string(reason),
				"message":             err.Error(),
				"model":               req.Model,
				"retry_after_seconds": retryAfter,
			})
			return
		}
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"status":  "failed",
			"reason":  string(reason),
			"error":   err.Error(),
			"model":   req.Model,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"status":  "ok",
		"reply":   content,
		"model":   req.Model,
	})
}

func (h *Handler) reserveAccountTest(accountID string) (int, bool) {
	if adminAccountTestMinSpacing <= 0 {
		return 0, false
	}
	h.accountTestMu.Lock()
	defer h.accountTestMu.Unlock()
	if h.accountTestLast == nil {
		h.accountTestLast = make(map[string]time.Time)
	}
	now := time.Now()
	if last := h.accountTestLast[accountID]; !last.IsZero() {
		next := last.Add(adminAccountTestMinSpacing)
		if now.Before(next) {
			retryAfter := int(time.Until(next).Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
			return retryAfter, true
		}
	}
	h.accountTestLast[accountID] = now
	return 0, false
}

// apiRefreshAccount 刷新账户信息（使用量、订阅等）
func (h *Handler) apiRefreshAccount(w http.ResponseWriter, r *http.Request, id string) {
	accounts := config.GetAccounts()
	var account *config.Account
	for i := range accounts {
		if accounts[i].ID == id {
			account = &accounts[i]
			break
		}
	}

	if account == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}

	result, err := refreshAccountData(account)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	resp := map[string]interface{}{"success": true}
	if result != nil {
		if result.Info != nil {
			resp["info"] = result.Info
		}
		if result.Message != "" {
			resp["message"] = result.Message
		}
	}
	json.NewEncoder(w).Encode(resp)
}

// apiGetAccountFull 获取单个账号的完整信息（包含敏感字段）
func (h *Handler) apiGetAccountFull(w http.ResponseWriter, r *http.Request, id string) {
	accounts := config.GetAccounts()
	poolAccounts := h.pool.GetAllAccounts()

	// 查找指定账号
	var account *config.Account
	for i := range accounts {
		if accounts[i].ID == id {
			account = &accounts[i]
			break
		}
	}

	if account == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}

	// 获取运行时统计
	var stats config.Account
	for _, a := range poolAccounts {
		if a.ID == id {
			stats = a
			break
		}
	}

	// 返回完整账号信息（包含敏感字段）
	result := map[string]interface{}{
		"id":                account.ID,
		"email":             account.Email,
		"userId":            account.UserId,
		"nickname":          account.Nickname,
		"accessToken":       account.AccessToken,
		"refreshToken":      account.RefreshToken,
		"clientId":          account.ClientID,
		"clientSecret":      account.ClientSecret,
		"authMethod":        account.AuthMethod,
		"provider":          account.Provider,
		"region":            account.Region,
		"expiresAt":         account.ExpiresAt,
		"machineId":         account.MachineId,
		"weight":            account.Weight,
		"allowOverage":      account.AllowOverage,
		"overageWeight":     account.OverageWeight,
		"proxyURL":          account.ProxyURL,
		"enabled":           account.Enabled,
		"banStatus":         account.BanStatus,
		"banReason":         account.BanReason,
		"banTime":           account.BanTime,
		"lastFailureReason": account.LastFailureReason,
		"lastFailureAt":     account.LastFailureAt,
		"cooldownUntil":     account.CooldownUntil,
		"failureCount":      account.FailureCount,
		"subscriptionType":  account.SubscriptionType,
		"subscriptionTitle": account.SubscriptionTitle,
		"daysRemaining":     account.DaysRemaining,
		"usageCurrent":      account.UsageCurrent,
		"usageLimit":        account.UsageLimit,
		"usagePercent":      account.UsagePercent,
		"nextResetDate":     account.NextResetDate,
		"lastRefresh":       account.LastRefresh,
		"trialUsageCurrent": account.TrialUsageCurrent,
		"trialUsageLimit":   account.TrialUsageLimit,
		"trialUsagePercent": account.TrialUsagePercent,
		"trialStatus":       account.TrialStatus,
		"trialExpiresAt":    account.TrialExpiresAt,
		"requestCount":      stats.RequestCount,
		"errorCount":        stats.ErrorCount,
		"totalTokens":       stats.TotalTokens,
		"totalCredits":      stats.TotalCredits,
		"lastUsed":          stats.LastUsed,
		"runtimeHealth":     h.pool.GetRuntimeHealth(account.ID),
	}

	json.NewEncoder(w).Encode(result)
}

// apiGetAccountModels 获取账户可用模型
func (h *Handler) apiGetAccountModels(w http.ResponseWriter, r *http.Request, id string) {
	accounts := config.GetAccounts()
	var account *config.Account
	for i := range accounts {
		if accounts[i].ID == id {
			account = &accounts[i]
			break
		}
	}

	if account == nil {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "Account not found"})
		return
	}

	models, err := ListAvailableModels(account)
	if err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// 同步更新路由缓存
	modelIDs := make([]string, 0, len(models))
	for _, m := range models {
		modelIDs = append(modelIDs, m.ModelId)
	}
	h.pool.SetModelList(id, modelIDs)
	h.modelsCacheMu.Lock()
	h.cachedModels = mergeUniqueModels(h.cachedModels, models)
	h.modelsCacheTime = time.Now().Unix()
	h.modelsCacheMu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"models":  models,
	})
}

// apiGetAccountModelsCached 返回账号已缓存的模型列表（不实时拉取）
func (h *Handler) apiGetAccountModelsCached(w http.ResponseWriter, r *http.Request, id string) {
	models := h.pool.GetModelList(id)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"models":  models,
	})
}

// ==================== 静态文件服务 ====================

func (h *Handler) serveAdminPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	http.ServeFile(w, r, "web/index.html")
}

func (h *Handler) serveStaticFile(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/")
	http.ServeFile(w, r, "web/"+path)
}

func (h *Handler) serveFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="12" fill="#0f172a"/><path d="M18 16h8v14l14-14h10L34 32l17 16H40L26 34v14h-8z" fill="#38bdf8"/></svg>`))
}

// apiGetThinkingConfig 获取 thinking 配置
func (h *Handler) apiGetThinkingConfig(w http.ResponseWriter, r *http.Request) {
	cfg := config.GetThinkingConfig()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"suffix":       cfg.Suffix,
		"openaiFormat": cfg.OpenAIFormat,
		"claudeFormat": cfg.ClaudeFormat,
	})
}

// apiUpdateThinkingConfig 更新 thinking 配置
func (h *Handler) apiUpdateThinkingConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Suffix       string `json:"suffix"`
		OpenAIFormat string `json:"openaiFormat"`
		ClaudeFormat string `json:"claudeFormat"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	// 验证格式
	validFormats := map[string]bool{"reasoning_content": true, "thinking": true, "think": true}
	if req.OpenAIFormat != "" && !validFormats[req.OpenAIFormat] {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid openaiFormat, must be: reasoning_content, thinking, or think"})
		return
	}
	if req.ClaudeFormat != "" && !validFormats[req.ClaudeFormat] {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid claudeFormat, must be: reasoning_content, thinking, or think"})
		return
	}

	if err := config.UpdateThinkingConfig(req.Suffix, req.OpenAIFormat, req.ClaudeFormat); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// apiGetEndpointConfig 获取端点配置
func (h *Handler) apiGetEndpointConfig(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"preferredEndpoint": config.GetPreferredEndpoint(),
		"endpointFallback":  config.GetEndpointFallback(),
	})
}

// apiUpdateEndpointConfig 更新端点配置
func (h *Handler) apiUpdateEndpointConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PreferredEndpoint string `json:"preferredEndpoint"`
		EndpointFallback  *bool  `json:"endpointFallback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	valid := map[string]bool{"auto": true, "kiro": true, "codewhisperer": true, "amazonq": true}
	if !valid[req.PreferredEndpoint] {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid endpoint, must be: auto, kiro, codewhisperer, or amazonq"})
		return
	}

	if err := config.UpdatePreferredEndpoint(req.PreferredEndpoint); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.EndpointFallback != nil {
		config.UpdateEndpointFallback(*req.EndpointFallback)
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *Handler) apiGetLoadBalance(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(config.GetLoadBalanceConfig())
}

func (h *Handler) apiUpdateLoadBalance(w http.ResponseWriter, r *http.Request) {
	var req config.LoadBalanceConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}
	if err := config.UpdateLoadBalanceConfig(req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if h.pool != nil {
		h.pool.SetStrategy(pool.Strategy(config.GetLoadBalanceConfig().Strategy))
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// applyProxyConfig 将代理配置应用到所有出站 HTTP 客户端（Kiro API + auth 模块）
func applyProxyConfig(proxyURL string) {
	InitKiroHttpClient(proxyURL)
	auth.InitHttpClient(proxyURL)
}

// apiGetProxy 获取当前代理配置
func (h *Handler) apiGetProxy(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"proxyURL": config.GetProxyURL(),
	})
}

// apiUpdateProxy 更新代理配置并立即生效
func (h *Handler) apiUpdateProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProxyURL string `json:"proxyURL"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	// 验证代理 URL 格式（非空时）
	if req.ProxyURL != "" {
		if !strings.HasPrefix(req.ProxyURL, "http://") &&
			!strings.HasPrefix(req.ProxyURL, "https://") &&
			!strings.HasPrefix(req.ProxyURL, "socks5://") &&
			!strings.HasPrefix(req.ProxyURL, "socks5h://") {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "proxyURL must start with http://, https://, socks5://, or socks5h://"})
			return
		}
	}

	if err := config.UpdateProxySettings(req.ProxyURL); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// 立即应用新的代理配置
	applyProxyConfig(req.ProxyURL)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// apiGetVersion 获取版本信息
func (h *Handler) apiGetVersion(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{
		"version": config.Version,
	})
}

// apiExportAccounts 导出账号凭证
func (h *Handler) apiExportAccounts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"` // 为空则导出全部
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 如果 body 为空或解析失败，导出全部
		req.IDs = nil
	}

	accounts := config.GetAccounts()

	// 如果指定了 ID，只导出指定的
	if len(req.IDs) > 0 {
		idSet := make(map[string]bool)
		for _, id := range req.IDs {
			idSet[id] = true
		}
		var filtered []config.Account
		for _, a := range accounts {
			if idSet[a.ID] {
				filtered = append(filtered, a)
			}
		}
		accounts = filtered
	}

	// 构建兼容 Kiro Account Manager 的导出格式
	type ExportCredentials struct {
		AccessToken  string `json:"accessToken"`
		CsrfToken    string `json:"csrfToken"`
		RefreshToken string `json:"refreshToken"`
		ClientID     string `json:"clientId,omitempty"`
		ClientSecret string `json:"clientSecret,omitempty"`
		Region       string `json:"region,omitempty"`
		ExpiresAt    int64  `json:"expiresAt"`
		AuthMethod   string `json:"authMethod,omitempty"`
		Provider     string `json:"provider,omitempty"`
	}

	type ExportSubscription struct {
		Type  string `json:"type"`
		Title string `json:"title,omitempty"`
	}

	type ExportUsage struct {
		Current     float64 `json:"current"`
		Limit       float64 `json:"limit"`
		PercentUsed float64 `json:"percentUsed"`
		LastUpdated int64   `json:"lastUpdated"`
	}

	type ExportAccount struct {
		ID           string             `json:"id"`
		Email        string             `json:"email"`
		Nickname     string             `json:"nickname,omitempty"`
		Idp          string             `json:"idp"`
		UserId       string             `json:"userId,omitempty"`
		MachineId    string             `json:"machineId,omitempty"`
		Credentials  ExportCredentials  `json:"credentials"`
		Subscription ExportSubscription `json:"subscription"`
		Usage        ExportUsage        `json:"usage"`
		Tags         []string           `json:"tags"`
		Status       string             `json:"status"`
		CreatedAt    int64              `json:"createdAt"`
		LastUsedAt   int64              `json:"lastUsedAt"`
	}

	type ExportData struct {
		Version    string          `json:"version"`
		ExportedAt int64           `json:"exportedAt"`
		Accounts   []ExportAccount `json:"accounts"`
		Groups     []interface{}   `json:"groups"`
		Tags       []interface{}   `json:"tags"`
	}

	exportAccounts := make([]ExportAccount, 0, len(accounts))
	for _, a := range accounts {
		// 映射 provider 到 idp
		idp := a.Provider
		if idp == "" {
			if a.AuthMethod == "social" {
				idp = "Google"
			} else {
				idp = "BuilderId"
			}
		}

		// 映射 authMethod
		authMethod := a.AuthMethod
		if authMethod == "idc" {
			authMethod = "IdC"
		}

		// 映射订阅类型
		subType := "Free"
		rawType := strings.ToUpper(a.SubscriptionType)
		if strings.Contains(rawType, "PRO_PLUS") || strings.Contains(rawType, "PROPLUS") {
			subType = "Pro_Plus"
		} else if strings.Contains(rawType, "PRO") {
			subType = "Pro"
		} else if strings.Contains(rawType, "POWER") {
			subType = "Pro_Plus"
		}

		exportAccounts = append(exportAccounts, ExportAccount{
			ID:        a.ID,
			Email:     a.Email,
			Nickname:  a.Nickname,
			Idp:       idp,
			UserId:    a.UserId,
			MachineId: a.MachineId,
			Credentials: ExportCredentials{
				AccessToken:  a.AccessToken,
				CsrfToken:    "",
				RefreshToken: a.RefreshToken,
				ClientID:     a.ClientID,
				ClientSecret: a.ClientSecret,
				Region:       a.Region,
				ExpiresAt:    a.ExpiresAt * 1000, // 转为毫秒时间戳
				AuthMethod:   authMethod,
				Provider:     a.Provider,
			},
			Subscription: ExportSubscription{
				Type:  subType,
				Title: a.SubscriptionTitle,
			},
			Usage: ExportUsage{
				Current:     a.UsageCurrent,
				Limit:       a.UsageLimit,
				PercentUsed: a.UsagePercent,
				LastUpdated: time.Now().UnixMilli(),
			},
			Tags:       []string{},
			Status:     "active",
			CreatedAt:  time.Now().UnixMilli(),
			LastUsedAt: time.Now().UnixMilli(),
		})
	}

	data := ExportData{
		Version:    config.Version,
		ExportedAt: time.Now().UnixMilli(),
		Accounts:   exportAccounts,
		Groups:     []interface{}{},
		Tags:       []interface{}{},
	}

	json.NewEncoder(w).Encode(data)
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
