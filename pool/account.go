// Package pool 账号池管理
// 实现轮询负载均衡、错误冷却、Token 刷新
package pool

import (
	"errors"
	"kiro-go/config"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const overageFrequencyScale = 10
const tokenRefreshSkewSeconds int64 = 120
const temporaryLimitSingleAccountBaseCooldown = time.Minute
const temporaryLimitMultiAccountBaseCooldown = time.Minute
const temporaryLimitMaxCooldown = 24 * time.Hour
const modelCapacityBaseCooldown = 3 * time.Second

type FailureReason string

const (
	FailureReasonUnknown          FailureReason = "unknown"
	FailureReasonQuotaExhausted   FailureReason = "quota_exhausted"
	FailureReasonAuthExpired      FailureReason = "auth_expired"
	FailureReasonSuspended        FailureReason = "suspended"
	FailureReasonRateLimited      FailureReason = "rate_limited"
	FailureReasonTemporaryLimited FailureReason = "temporary_limited"
	FailureReasonModelCapacity    FailureReason = "model_capacity"
	FailureReasonTransientNetwork FailureReason = "transient_network"
	FailureReasonUpstream5xx      FailureReason = "upstream_5xx"
)

type RuntimeHealth struct {
	ActiveConnections int   `json:"activeConnections"`
	RecentFailures    int   `json:"recentFailures"`
	RecentSuccesses   int   `json:"recentSuccesses"`
	AvgLatencyMS      int64 `json:"avgLatencyMs"`
	LastUpdatedAt     int64 `json:"lastUpdatedAt"`
	Score             int   `json:"score"`
}

type ModelBlockState struct {
	AccountsEvaluated int
	Blocked           int
	AllBlocked        bool
	LastReason        FailureReason
	RetryAt           time.Time
}

type ModelAccountBlockState struct {
	Blocked      bool
	CircuitState string
	Reason       FailureReason
	RetryAt      time.Time
}

type CooldownState struct {
	CoolingDown bool
	Reason      FailureReason
	RetryAt     time.Time
	RiskGroup   bool
}

type Strategy string

const (
	StrategyHealth           Strategy = "health"
	StrategyRoundRobin       Strategy = "round_robin"
	StrategyLeastConnections Strategy = "least_connections"
)

type runtimeHealthState struct {
	activeConnections int
	recentFailures    int
	recentSuccesses   int
	avgLatencyMS      int64
	lastUpdatedAt     int64
}

func (h runtimeHealthState) score() int {
	total := h.recentSuccesses + h.recentFailures
	successScore := 100
	if total > 0 {
		successScore = h.recentSuccesses * 100 / total
	}
	connectionPenalty := h.activeConnections * 15
	latencyPenalty := int(h.avgLatencyMS / 1000 * 5)
	score := successScore - connectionPenalty - latencyPenalty
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func (h runtimeHealthState) export() RuntimeHealth {
	return RuntimeHealth{
		ActiveConnections: h.activeConnections,
		RecentFailures:    h.recentFailures,
		RecentSuccesses:   h.recentSuccesses,
		AvgLatencyMS:      h.avgLatencyMS,
		LastUpdatedAt:     h.lastUpdatedAt,
		Score:             h.score(),
	}
}

func ClassifyFailureReason(err error) FailureReason {
	if err == nil {
		return FailureReasonUnknown
	}
	var structured interface {
		FailureReason() FailureReason
	}
	if errors.As(err, &structured) {
		if reason := structured.FailureReason(); reason != "" && reason != FailureReasonUnknown {
			return reason
		}
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "quota exhausted"):
		return FailureReasonQuotaExhausted
	case strings.Contains(msg, "temporarily_suspended"), strings.Contains(msg, "account suspended"), strings.Contains(msg, "suspended"):
		return FailureReasonSuspended
	case strings.Contains(msg, "suspicious activity") && strings.Contains(msg, "temporary limits"):
		return FailureReasonTemporaryLimited
	case strings.Contains(msg, "insufficient_model_capacity"), strings.Contains(msg, "experiencing high traffic"), strings.Contains(msg, "model capacity"):
		return FailureReasonModelCapacity
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"), strings.Contains(msg, "too many requests"):
		return FailureReasonRateLimited
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"), strings.Contains(msg, "expired"), strings.Contains(msg, "invalid token"):
		return FailureReasonAuthExpired
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "eof"), strings.Contains(msg, "network"),
		strings.Contains(msg, "stream error"), strings.Contains(msg, "internal_error"), strings.Contains(msg, "received from peer"), strings.Contains(msg, "http2"):
		return FailureReasonTransientNetwork
	case strings.Contains(msg, "500"), strings.Contains(msg, "502"), strings.Contains(msg, "503"), strings.Contains(msg, "504"):
		return FailureReasonUpstream5xx
	default:
		return FailureReasonUnknown
	}
}

// AccountRiskGroupKey returns a display grouping for accounts that appear to
// share an upstream subject. It is informational only; generation temporary
// limits are enforced per account because Kiro can limit one account while
// other accounts with the same profile or user-id prefix still succeed.
func AccountRiskGroupKey(account config.Account) string {
	if profileArn := strings.TrimSpace(account.ProfileArn); profileArn != "" {
		return "profile:" + profileArn
	}
	if userID := strings.TrimSpace(account.UserId); userID != "" {
		if idx := strings.Index(userID, "."); idx > 0 {
			return "user-prefix:" + userID[:idx]
		}
		return "user:" + userID
	}
	return ""
}

func (p *AccountPool) riskGroupKeyForIDLocked(id string) string {
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			return AccountRiskGroupKey(p.accounts[i])
		}
	}
	return ""
}

// AccountPool 账号池
type AccountPool struct {
	mu             sync.RWMutex
	accounts       []config.Account
	totalAccounts  int
	currentIndex   uint64
	cooldowns      map[string]time.Time       // 账号冷却时间
	errorCounts    map[string]int             // 连续错误计数
	failures       map[string]FailureReason   // Last failure classification
	groupCooldowns map[string]time.Time       // shared upstream risk-group cooldowns
	groupFailures  map[string]FailureReason   // shared upstream risk-group failure classification
	modelLists     map[string]map[string]bool // accountID -> set of modelIDs
	runtimeHealth  map[string]*runtimeHealthState
	modelSuccess   map[string]time.Time
	breakers       *modelBreakerState
	strategy       Strategy
}

var (
	pool     *AccountPool
	poolOnce sync.Once
)

// GetPool 获取全局账号池单例
func GetPool() *AccountPool {
	poolOnce.Do(func() {
		pool = &AccountPool{
			cooldowns:      make(map[string]time.Time),
			errorCounts:    make(map[string]int),
			failures:       make(map[string]FailureReason),
			groupCooldowns: make(map[string]time.Time),
			groupFailures:  make(map[string]FailureReason),
			modelLists:     make(map[string]map[string]bool),
			runtimeHealth:  make(map[string]*runtimeHealthState),
			modelSuccess:   make(map[string]time.Time),
			breakers:       newModelBreakerState(),
			strategy:       Strategy(config.GetLoadBalanceConfig().Strategy),
		}
		pool.Reload()
	})
	return pool
}

func (p *AccountPool) ensureStateLocked() {
	if p.cooldowns == nil {
		p.cooldowns = make(map[string]time.Time)
	}
	if p.errorCounts == nil {
		p.errorCounts = make(map[string]int)
	}
	if p.failures == nil {
		p.failures = make(map[string]FailureReason)
	}
	if p.groupCooldowns == nil {
		p.groupCooldowns = make(map[string]time.Time)
	}
	if p.groupFailures == nil {
		p.groupFailures = make(map[string]FailureReason)
	}
	if p.modelLists == nil {
		p.modelLists = make(map[string]map[string]bool)
	}
	if p.runtimeHealth == nil {
		p.runtimeHealth = make(map[string]*runtimeHealthState)
	}
	if p.modelSuccess == nil {
		p.modelSuccess = make(map[string]time.Time)
	}
	if p.breakers == nil {
		p.breakers = newModelBreakerState()
	}
	if p.strategy == "" {
		p.strategy = StrategyHealth
	}
}

func (p *AccountPool) SetStrategy(strategy Strategy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	switch strategy {
	case StrategyRoundRobin, StrategyLeastConnections, StrategyHealth:
		p.strategy = strategy
	default:
		p.strategy = StrategyHealth
	}
}

// Reload 从配置重新加载账号
// 构建加权列表：weight<=1 出现 1 次，weight>=2 出现 weight 次
func (p *AccountPool) Reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	enabled := config.GetEnabledAccounts()
	var weighted []config.Account
	for _, a := range enabled {
		w := effectiveWeight(a.Weight) * overageFrequencyScale
		if isOverUsageLimit(a) {
			if !a.AllowOverage {
				continue
			}
			w = effectiveOverageWeight(a.OverageWeight)
		}
		for j := 0; j < w; j++ {
			weighted = append(weighted, a)
		}
	}
	p.accounts = weighted
	p.totalAccounts = len(enabled)
}

// GetNext 获取下一个可用账号（加权轮询）
func (p *AccountPool) GetNext() *config.Account {
	return p.GetNextExcept(nil)
}

func (p *AccountPool) GetNextExcept(excluded map[string]bool) *config.Account {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.accounts) == 0 {
		return nil
	}

	allowOverUsage := config.GetAllowOverUsage()
	now := time.Now()
	p.clearExpiredCooldownsLocked(now)
	n := len(p.accounts)
	seen := make(map[string]bool)

	// 加权轮询查找可用账号
	var best *config.Account
	for i := 0; i < n; i++ {
		idx := atomic.AddUint64(&p.currentIndex, 1) % uint64(n)
		acc := &p.accounts[idx]

		if seen[acc.ID] {
			continue
		}
		if excluded != nil && excluded[acc.ID] {
			seen[acc.ID] = true
			continue
		}

		if !p.accountBaseUsableLocked(acc, now, allowOverUsage) {
			seen[acc.ID] = true
			continue
		}

		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID, "") {
			best = acc
		}
		if p.strategy == StrategyRoundRobin || p.isIdleHealthyLocked(acc.ID) {
			return acc
		}
	}
	if best != nil {
		return best
	}

	// 无可用账号时直接返回 nil。冷却账号必须等到冷却结束后再重新进入调度，
	// 否则 Opus 4.7 容量不足时会在同一冷却窗口内反复打同一个账号。
	for i := range p.accounts {
		acc := &p.accounts[i]
		if excluded != nil && excluded[acc.ID] {
			continue
		}
		if !p.accountBaseUsableLocked(acc, now, allowOverUsage) {
			continue
		}
		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID, "") {
			best = acc
		}
	}
	return best
}

// SetModelList 缓存账号支持的模型集合（由 handler 在刷新后调用）
func (p *AccountPool) SetModelList(accountID string, modelIDs []string) {
	set := make(map[string]bool, len(modelIDs))
	for _, id := range modelIDs {
		set[strings.ToLower(strings.TrimSpace(id))] = true
	}
	p.mu.Lock()
	p.ensureStateLocked()
	p.modelLists[accountID] = set
	p.mu.Unlock()
}

// GetModelList 返回该账号缓存的模型 ID 列表（供 admin API 使用）。
func (p *AccountPool) GetModelList(accountID string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	set, ok := p.modelLists[accountID]
	if !ok || len(set) == 0 {
		return []string{}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids
}

func modelContentSuccessKey(accountID, model string) string {
	return strings.TrimSpace(accountID) + "\x00" + normalizedBreakerModel(model)
}

func (p *AccountPool) RecordModelContentSuccess(accountID, model string, at time.Time) {
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimSpace(model)
	if accountID == "" || model == "" || at.IsZero() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	key := modelContentSuccessKey(accountID, model)
	if current, ok := p.modelSuccess[key]; !ok || at.After(current) {
		p.modelSuccess[key] = at
	}
}

func (p *AccountPool) ModelContentSuccess(accountID, model string) (time.Time, bool) {
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimSpace(model)
	if accountID == "" || model == "" {
		return time.Time{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.modelSuccess == nil {
		return time.Time{}, false
	}
	at, ok := p.modelSuccess[modelContentSuccessKey(accountID, model)]
	return at, ok
}

// accountHasModel 检查账号是否支持指定模型。冷启动时模型列表为空，乐观放行。
func (p *AccountPool) accountHasModel(accountID, model string) bool {
	list, ok := p.modelLists[accountID]
	if !ok || len(list) == 0 {
		return true
	}
	return list[strings.ToLower(strings.TrimSpace(model))]
}

// GetNextForModel 获取下一个支持指定模型的可用账号。
func (p *AccountPool) GetNextForModel(model string) *config.Account {
	return p.GetNextForModelExcept(model, nil)
}

// GetNextForModelExcept 获取下一个支持指定模型且不在 excluded 中的账号。
func (p *AccountPool) GetNextForModelExcept(model string, excluded map[string]bool) *config.Account {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.getNextForModelExceptLocked(model, excluded)
}

func (p *AccountPool) RememberSticky(sessionKey, model, accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.breakers.rememberSticky(sessionKey, model, accountID, time.Now())
}

func (p *AccountPool) RecordModelSuccess(accountID, model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.breakers.success(accountID, model)
}

func (p *AccountPool) RecordModelFailure(accountID, model string, reason FailureReason, retryAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()

	now := time.Now()
	delay := 30 * time.Second
	switch reason {
	case FailureReasonModelCapacity:
		delay = modelCapacityBaseCooldown
	case FailureReasonRateLimited:
		delay = time.Minute
	case FailureReasonTemporaryLimited:
		delay = temporaryLimitMultiAccountCooldownForCount(1)
	case FailureReasonAuthExpired:
		delay = 10 * time.Minute
	case FailureReasonQuotaExhausted, FailureReasonSuspended:
		delay = time.Hour
	case FailureReasonTransientNetwork, FailureReasonUpstream5xx:
		delay = 30 * time.Second
	}
	if retryAt.After(now) {
		delay = retryAt.Sub(now)
	} else if !retryAt.IsZero() {
		delay = time.Nanosecond
	}
	p.breakers.open(accountID, model, reason, now, delay)
}

func (p *AccountPool) getNextForModelExceptLocked(model string, excluded map[string]bool) *config.Account {
	p.ensureStateLocked()
	if len(p.accounts) == 0 {
		return nil
	}

	allowOverUsage := config.GetAllowOverUsage()
	now := time.Now()
	p.clearExpiredCooldownsLocked(now)
	n := len(p.accounts)
	seen := make(map[string]bool)
	var best *config.Account
	hasSuccessEvidence := p.hasModelContentSuccessEvidenceLocked(model)

	for i := 0; i < n; i++ {
		idx := atomic.AddUint64(&p.currentIndex, 1) % uint64(n)
		acc := &p.accounts[idx]

		if seen[acc.ID] {
			continue
		}
		if excluded != nil && excluded[acc.ID] {
			seen[acc.ID] = true
			continue
		}
		if !p.accountHasModel(acc.ID, model) {
			seen[acc.ID] = true
			continue
		}
		if !p.accountBaseUsableLocked(acc, now, allowOverUsage) {
			seen[acc.ID] = true
			continue
		}
		if !p.breakers.isClosed(acc.ID, model) {
			seen[acc.ID] = true
			continue
		}
		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID, model) {
			best = acc
		}
		if p.strategy == StrategyRoundRobin || (!hasSuccessEvidence && p.isIdleHealthyLocked(acc.ID)) {
			return acc
		}
	}
	if best != nil {
		return best
	}

	for i := range p.accounts {
		acc := &p.accounts[i]
		if excluded != nil && excluded[acc.ID] {
			continue
		}
		if !p.accountHasModel(acc.ID, model) {
			continue
		}
		if !p.accountBaseUsableLocked(acc, now, allowOverUsage) {
			continue
		}
		if !p.breakers.isClosed(acc.ID, model) {
			continue
		}
		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID, model) {
			best = acc
		}
	}
	return best
}

func (p *AccountPool) BeginNextForModelExcept(model string, excluded map[string]bool) (*config.Account, func()) {
	return p.beginNextForModelExcept(model, excluded, "", false)
}

func (p *AccountPool) BeginNextForModelSessionExcept(model, sessionKey string, excluded map[string]bool) (*config.Account, func()) {
	return p.beginNextForModelExcept(model, excluded, sessionKey, true)
}

func (p *AccountPool) beginNextForModelExcept(model string, excluded map[string]bool, sessionKey string, allowBreakerProbe bool) (*config.Account, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()

	now := time.Now()
	if sticky := p.breakers.stickyAccount(sessionKey, model, now); sticky != "" && (excluded == nil || !excluded[sticky]) {
		for i := range p.accounts {
			if p.accounts[i].ID != sticky || !p.accountHasModel(sticky, model) || !p.accountUsableForModelLocked(&p.accounts[i], model, now) {
				continue
			}
			p.reserveAccountForModelLocked(sticky, model, now, allowBreakerProbe)
			return &p.accounts[i], p.releaseAccountRequestFunc(sticky)
		}
	}

	acc := p.getNextForModelExceptLocked(model, excluded)
	if acc == nil && allowBreakerProbe {
		acc = p.nextBreakerProbeForModelLocked(model, excluded, now)
	}
	if acc == nil {
		return nil, func() {}
	}
	p.reserveAccountForModelLocked(acc.ID, model, now, allowBreakerProbe)
	p.breakers.rememberSticky(sessionKey, model, acc.ID, now)

	return acc, p.releaseAccountRequestFunc(acc.ID)
}

func (p *AccountPool) nextBreakerProbeForModelLocked(model string, excluded map[string]bool, now time.Time) *config.Account {
	allowOverUsage := config.GetAllowOverUsage()
	for i := range p.accounts {
		acc := &p.accounts[i]
		if excluded != nil && excluded[acc.ID] {
			continue
		}
		if !p.accountHasModel(acc.ID, model) || !p.accountBaseUsableLocked(acc, now, allowOverUsage) {
			continue
		}
		if p.breakers.canProbe(acc.ID, model, now) {
			return acc
		}
	}
	return nil
}

func (p *AccountPool) reserveAccountForModelLocked(accountID, model string, now time.Time, allowBreakerProbe bool) {
	if allowBreakerProbe && p.breakers.canProbe(accountID, model, now) {
		p.breakers.markProbe(accountID, model, now)
	}
	health := p.runtimeHealthForLocked(accountID)
	health.activeConnections++
	health.lastUpdatedAt = now.Unix()
}

func (p *AccountPool) releaseAccountRequestFunc(accountID string) func() {
	var once sync.Once
	release := func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.ensureStateLocked()
			health := p.runtimeHealthForLocked(accountID)
			if health.activeConnections > 0 {
				health.activeConnections--
			}
			health.lastUpdatedAt = time.Now().Unix()
		})
	}
	return release
}

func (p *AccountPool) accountUsableForModelLocked(acc *config.Account, model string, now time.Time) bool {
	if acc == nil {
		return false
	}
	if !p.accountBaseUsableLocked(acc, now, config.GetAllowOverUsage()) {
		return false
	}
	return p.breakers.canUse(acc.ID, model, now)
}

func (p *AccountPool) accountBaseUsableLocked(acc *config.Account, now time.Time, allowOverUsage bool) bool {
	if !acc.Enabled && p.hasExplicitEnabledAccountsLocked() {
		return false
	}
	if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
		return false
	}
	if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
		return false
	}
	if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-tokenRefreshSkewSeconds && strings.TrimSpace(acc.RefreshToken) == "" {
		return false
	}
	if isOverUsageLimit(*acc) && !acc.AllowOverage && !allowOverUsage {
		return false
	}
	return true
}

func (p *AccountPool) isIdleHealthyLocked(id string) bool {
	health := p.runtimeHealth[id]
	if health == nil {
		return true
	}
	return health.activeConnections == 0 && health.score() >= 90
}

func (p *AccountPool) isBetterCandidateLocked(candidateID, currentID, model string) bool {
	if p.strategy == StrategyRoundRobin {
		return false
	}
	if strings.TrimSpace(model) != "" {
		if candidateAt, ok := p.modelSuccess[modelContentSuccessKey(candidateID, model)]; ok {
			currentAt, currentOK := p.modelSuccess[modelContentSuccessKey(currentID, model)]
			if !currentOK || candidateAt.After(currentAt) {
				return true
			}
			if currentAt.After(candidateAt) {
				return false
			}
		} else if _, currentOK := p.modelSuccess[modelContentSuccessKey(currentID, model)]; currentOK {
			return false
		}
	}
	candidate := p.runtimeHealth[candidateID]
	current := p.runtimeHealth[currentID]
	if candidate == nil && current == nil {
		return false
	}
	if candidate == nil {
		return current.activeConnections > 0 || current.score() < 100
	}
	if current == nil {
		return candidate.activeConnections == 0 && candidate.score() >= 100
	}
	if candidate.activeConnections != current.activeConnections {
		return candidate.activeConnections < current.activeConnections
	}
	if p.strategy != StrategyLeastConnections && candidate.score() != current.score() {
		return candidate.score() > current.score()
	}
	return candidate.avgLatencyMS < current.avgLatencyMS
}

func (p *AccountPool) hasModelContentSuccessEvidenceLocked(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" || len(p.modelSuccess) == 0 {
		return false
	}
	suffix := "\x00" + normalizedBreakerModel(model)
	for key := range p.modelSuccess {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func (p *AccountPool) hasExplicitEnabledAccountsLocked() bool {
	for i := range p.accounts {
		if p.accounts[i].Enabled {
			return true
		}
	}
	return false
}

// GetByID 根据 ID 获取账号
func (p *AccountPool) GetByID(id string) *config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			return &p.accounts[i]
		}
	}
	return nil
}

// RecordSuccess 记录请求成功，清除冷却
func (p *AccountPool) RecordSuccess(id string) {
	p.RecordSuccessWithLatency(id, 0)
}

func (p *AccountPool) RecordSuccessWithLatency(id string, latency time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.clearFailureStateLocked(id)
	health := p.runtimeHealthForLocked(id)
	health.recentSuccesses++
	health.lastUpdatedAt = time.Now().Unix()
	if latency > 0 {
		latencyMS := latency.Milliseconds()
		if health.avgLatencyMS == 0 {
			health.avgLatencyMS = latencyMS
		} else {
			health.avgLatencyMS = (health.avgLatencyMS*9 + latencyMS) / 10
		}
	}
	trimRuntimeWindowLocked(health)
	if config.Get() != nil {
		_ = config.ClearAccountHealth(id)
	}
}

func (p *AccountPool) clearExpiredCooldownsLocked(now time.Time) {
	nowUnix := now.Unix()
	for key, cooldown := range p.groupCooldowns {
		if now.Before(cooldown) {
			continue
		}
		delete(p.groupCooldowns, key)
		delete(p.groupFailures, key)
	}
	cleared := make(map[string]bool)
	for i := range p.accounts {
		id := p.accounts[i].ID
		if cleared[id] {
			continue
		}
		if cooldown, ok := p.cooldowns[id]; ok && now.Before(cooldown) {
			continue
		}
		if p.accounts[i].CooldownUntil > 0 && nowUnix < p.accounts[i].CooldownUntil {
			continue
		}
		if _, ok := p.cooldowns[id]; ok || p.accounts[i].CooldownUntil > 0 {
			p.clearFailureStateLocked(id)
			if config.Get() != nil {
				_ = config.ClearAccountHealth(id)
			}
			cleared[id] = true
		}
	}
}

func (p *AccountPool) clearFailureStateLocked(id string) {
	delete(p.cooldowns, id)
	p.errorCounts[id] = 0
	delete(p.failures, id)
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			p.accounts[i].FailureCount = 0
			p.accounts[i].LastFailureReason = ""
			p.accounts[i].LastFailureAt = 0
			p.accounts[i].CooldownUntil = 0
		}
	}
}

// RecordFailure 记录请求错误，设置冷却
func (p *AccountPool) RecordFailure(id string, reason FailureReason) {
	if reason == FailureReasonModelCapacity {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()

	p.errorCounts[id]++
	p.failures[id] = reason
	now := time.Now()
	health := p.runtimeHealthForLocked(id)
	health.recentFailures++
	health.lastUpdatedAt = now.Unix()
	trimRuntimeWindowLocked(health)
	cooldown := time.Duration(0)

	switch reason {
	case FailureReasonQuotaExhausted, FailureReasonSuspended:
		cooldown = time.Hour
	case FailureReasonAuthExpired:
		cooldown = 10 * time.Minute
	case FailureReasonRateLimited:
		cooldown = time.Minute
	case FailureReasonTemporaryLimited:
		cooldown = p.temporaryLimitCooldownForCountLocked(id, p.errorCounts[id])
	case FailureReasonTransientNetwork, FailureReasonUpstream5xx:
		if p.errorCounts[id] >= 3 {
			cooldown = time.Minute
		}
	default:
		if p.errorCounts[id] >= 3 {
			cooldown = time.Minute
		}
	}

	for i := range p.accounts {
		if p.accounts[i].ID != id {
			continue
		}
		p.accounts[i].FailureCount = p.errorCounts[id]
		p.accounts[i].LastFailureReason = string(reason)
		p.accounts[i].LastFailureAt = now.Unix()
		if cooldown > 0 {
			until := now.Add(cooldown)
			p.cooldowns[id] = until
			p.accounts[i].CooldownUntil = until.Unix()
		}
		if config.Get() != nil {
			_ = config.UpdateAccountHealth(id, p.accounts[i].LastFailureReason, p.accounts[i].LastFailureAt, p.accounts[i].CooldownUntil, p.accounts[i].FailureCount)
		}
	}

	if cooldown == 0 && (reason == FailureReasonTransientNetwork || reason == FailureReasonUpstream5xx) && p.errorCounts[id] < 3 {
		return
	}
}

func (p *AccountPool) RecordFailureUntil(id string, reason FailureReason, resetAt time.Time) {
	if reason == FailureReasonModelCapacity {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()

	p.errorCounts[id]++
	p.failures[id] = reason
	now := time.Now()
	health := p.runtimeHealthForLocked(id)
	health.recentFailures++
	health.lastUpdatedAt = now.Unix()
	trimRuntimeWindowLocked(health)
	if !resetAt.After(now) {
		resetAt = now
	}
	if reason == FailureReasonTemporaryLimited {
		floor := now.Add(p.temporaryLimitCooldownForCountLocked(id, p.errorCounts[id]))
		if resetAt.Before(floor) {
			resetAt = floor
		}
	}

	for i := range p.accounts {
		if p.accounts[i].ID != id {
			continue
		}
		p.accounts[i].FailureCount = p.errorCounts[id]
		p.accounts[i].LastFailureReason = string(reason)
		p.accounts[i].LastFailureAt = now.Unix()
		p.cooldowns[id] = resetAt
		p.accounts[i].CooldownUntil = resetAt.Unix()
		if config.Get() != nil {
			_ = config.UpdateAccountHealth(id, p.accounts[i].LastFailureReason, p.accounts[i].LastFailureAt, p.accounts[i].CooldownUntil, p.accounts[i].FailureCount)
		}
	}
}

func (p *AccountPool) recordRiskGroupCooldownLocked(account config.Account, reason FailureReason, until time.Time) {
	key := AccountRiskGroupKey(account)
	if key == "" {
		return
	}
	if current, ok := p.groupCooldowns[key]; !ok || until.After(current) {
		p.groupCooldowns[key] = until
	}
	p.groupFailures[key] = reason
}

func (p *AccountPool) temporaryLimitCooldownForCountLocked(id string, count int) time.Duration {
	base := temporaryLimitMultiAccountBaseCooldown
	if p.enabledAccountCountLocked() <= 1 {
		base = temporaryLimitSingleAccountBaseCooldown
	}
	return temporaryLimitCooldownForCount(base, count)
}

func (p *AccountPool) enabledAccountCountLocked() int {
	count := 0
	seen := make(map[string]bool, len(p.accounts))
	for _, acc := range p.accounts {
		if seen[acc.ID] {
			continue
		}
		seen[acc.ID] = true
		if acc.Enabled {
			count++
		}
	}
	if count == 0 && len(seen) > 0 {
		return len(seen)
	}
	return count
}

func temporaryLimitMultiAccountCooldownForCount(count int) time.Duration {
	return temporaryLimitCooldownForCount(temporaryLimitMultiAccountBaseCooldown, count)
}

func temporaryLimitCooldownForCount(base time.Duration, count int) time.Duration {
	if count < 1 {
		count = 1
	}
	cooldown := base
	for i := 1; i < count; i++ {
		if cooldown >= temporaryLimitMaxCooldown/2 {
			return temporaryLimitMaxCooldown
		}
		cooldown *= 2
	}
	if cooldown > temporaryLimitMaxCooldown {
		return temporaryLimitMaxCooldown
	}
	return cooldown
}

func TemporaryLimitRetryAfterFloor() time.Duration {
	return temporaryLimitMultiAccountBaseCooldown
}

func (p *AccountPool) BeginRequest(id string) func() {
	p.mu.Lock()
	p.ensureStateLocked()
	health := p.runtimeHealthForLocked(id)
	health.activeConnections++
	health.lastUpdatedAt = time.Now().Unix()
	p.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.ensureStateLocked()
			health := p.runtimeHealthForLocked(id)
			if health.activeConnections > 0 {
				health.activeConnections--
			}
			health.lastUpdatedAt = time.Now().Unix()
		})
	}
}

func (p *AccountPool) GetRuntimeHealth(id string) RuntimeHealth {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.runtimeHealth == nil || p.runtimeHealth[id] == nil {
		return runtimeHealthState{}.export()
	}
	return p.runtimeHealth[id].export()
}

func (p *AccountPool) ModelBlockState(model string, now time.Time) ModelBlockState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.clearExpiredCooldownsLocked(now)

	state := ModelBlockState{}
	seen := make(map[string]bool)
	allowOverUsage := config.GetAllowOverUsage()
	for i := range p.accounts {
		acc := p.accounts[i]
		if seen[acc.ID] {
			continue
		}
		seen[acc.ID] = true
		if !p.accountHasModel(acc.ID, model) {
			continue
		}
		if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-tokenRefreshSkewSeconds {
			continue
		}
		if isOverUsageLimit(acc) && !acc.AllowOverage && !allowOverUsage {
			continue
		}
		state.AccountsEvaluated++

		reason := FailureReasonUnknown
		retryAt := time.Time{}
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			reason = p.failures[acc.ID]
			retryAt = cooldown
		}
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			if reason == "" || reason == FailureReasonUnknown {
				reason = FailureReason(acc.LastFailureReason)
			}
			if retryAt.IsZero() || time.Unix(acc.CooldownUntil, 0).After(retryAt) {
				retryAt = time.Unix(acc.CooldownUntil, 0)
			}
		}
		if !p.breakers.isClosed(acc.ID, model) {
			if reason == "" || reason == FailureReasonUnknown {
				reason = p.failures[acc.ID]
			}
			if e := p.breakers.entries[breakerKey(acc.ID, model)]; e != nil && e.RetryAt.After(retryAt) {
				retryAt = e.RetryAt
				if reason == "" || reason == FailureReasonUnknown {
					reason = e.Reason
				}
			}
		}
		if reason == "" || reason == FailureReasonUnknown {
			continue
		}
		state.Blocked++
		if state.LastReason == "" || reasonPriority(reason) > reasonPriority(state.LastReason) {
			state.LastReason = reason
		}
		if retryAt.After(state.RetryAt) {
			state.RetryAt = retryAt
		}
	}
	state.AllBlocked = state.AccountsEvaluated > 0 && state.Blocked == state.AccountsEvaluated
	return state
}

func (p *AccountPool) ModelAccountBlockState(accountID, model string, now time.Time) ModelAccountBlockState {
	accountID = strings.TrimSpace(accountID)
	model = strings.TrimSpace(model)
	if accountID == "" || model == "" {
		return ModelAccountBlockState{CircuitState: string(breakerClosed)}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.clearExpiredCooldownsLocked(now)

	entry := p.breakers.entries[breakerKey(accountID, model)]
	if entry == nil || entry.Status == breakerClosed {
		return ModelAccountBlockState{CircuitState: string(breakerClosed)}
	}
	return ModelAccountBlockState{
		Blocked:      !p.breakers.canUse(accountID, model, now),
		CircuitState: string(entry.Status),
		Reason:       entry.Reason,
		RetryAt:      entry.RetryAt,
	}
}

func reasonPriority(reason FailureReason) int {
	switch reason {
	case FailureReasonModelCapacity:
		return 95
	case FailureReasonTemporaryLimited:
		return 100
	case FailureReasonRateLimited:
		return 90
	case FailureReasonQuotaExhausted:
		return 80
	case FailureReasonSuspended:
		return 70
	case FailureReasonAuthExpired:
		return 60
	case FailureReasonUpstream5xx:
		return 50
	case FailureReasonTransientNetwork:
		return 40
	default:
		return 0
	}
}

func (p *AccountPool) runtimeHealthForLocked(id string) *runtimeHealthState {
	health := p.runtimeHealth[id]
	if health == nil {
		health = &runtimeHealthState{}
		p.runtimeHealth[id] = health
	}
	return health
}

func trimRuntimeWindowLocked(health *runtimeHealthState) {
	if health.recentSuccesses+health.recentFailures <= 100 {
		return
	}
	health.recentSuccesses = health.recentSuccesses * 9 / 10
	health.recentFailures = health.recentFailures * 9 / 10
}

// UpdateToken 更新账号 Token
func (p *AccountPool) UpdateToken(id, accessToken, refreshToken string, expiresAt int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			p.accounts[i].AccessToken = accessToken
			if refreshToken != "" {
				p.accounts[i].RefreshToken = refreshToken
			}
			p.accounts[i].ExpiresAt = expiresAt
		}
	}
}

// Count 返回账号总数
func (p *AccountPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.totalAccounts > 0 {
		return p.totalAccounts
	}

	seen := make(map[string]bool)
	for _, acc := range p.accounts {
		seen[acc.ID] = true
	}
	return len(seen)
}

// AvailableCount 返回可用账号数
func (p *AccountPool) AvailableCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.clearExpiredCooldownsLocked(now)
	count := 0
	seen := make(map[string]bool)
	for _, acc := range p.accounts {
		if seen[acc.ID] {
			continue
		}
		seen[acc.ID] = true
		if !p.accountBaseUsableLocked(&acc, now, config.GetAllowOverUsage()) {
			continue
		}
		count++
	}
	return count
}

// UpdateStats 更新账号统计
func (p *AccountPool) UpdateStats(id string, tokens int, credits float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var updated bool
	var requestCount, errorCount, totalTokens int
	var totalCredits float64
	var lastUsed int64
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			if !updated {
				p.accounts[i].RequestCount++
				p.accounts[i].TotalTokens += tokens
				p.accounts[i].TotalCredits += credits
				p.accounts[i].LastUsed = time.Now().Unix()

				requestCount = p.accounts[i].RequestCount
				errorCount = p.accounts[i].ErrorCount
				totalTokens = p.accounts[i].TotalTokens
				totalCredits = p.accounts[i].TotalCredits
				lastUsed = p.accounts[i].LastUsed
				updated = true
				continue
			}
			p.accounts[i].RequestCount = requestCount
			p.accounts[i].ErrorCount = errorCount
			p.accounts[i].TotalTokens = totalTokens
			p.accounts[i].TotalCredits = totalCredits
			p.accounts[i].LastUsed = lastUsed
		}
	}
	if updated {
		go config.UpdateAccountStats(id, requestCount, errorCount, totalTokens, totalCredits, lastUsed)
	}
}

// GetAllAccounts 获取所有账号副本
func (p *AccountPool) GetAllAccounts() []config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]config.Account, len(p.accounts))
	copy(result, p.accounts)
	return result
}

func (p *AccountPool) IsCoolingDown(id string, now time.Time) bool {
	return p.CooldownState(id, now).CoolingDown
}

func (p *AccountPool) CooldownState(id string, now time.Time) CooldownState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state := CooldownState{}
	if cooldown, ok := p.cooldowns[id]; ok && now.Before(cooldown) {
		state.CoolingDown = true
		state.Reason = p.failures[id]
		state.RetryAt = cooldown
	}
	for _, acc := range p.accounts {
		if acc.ID != id {
			continue
		}
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			retryAt := time.Unix(acc.CooldownUntil, 0)
			state.CoolingDown = true
			if state.Reason == "" || state.Reason == FailureReasonUnknown {
				state.Reason = FailureReason(acc.LastFailureReason)
			}
			if retryAt.After(state.RetryAt) {
				state.RetryAt = retryAt
			}
		}
	}
	if state.Reason == "" {
		state.Reason = FailureReasonUnknown
	}
	return state
}

func isOverUsageLimit(acc config.Account) bool {
	return acc.UsageLimit > 0 && acc.UsageCurrent >= acc.UsageLimit
}

func effectiveWeight(weight int) int {
	if weight < 1 {
		return 1
	}
	return weight
}

func effectiveOverageWeight(weight int) int {
	if weight < 1 {
		return 1
	}
	if weight > overageFrequencyScale {
		return overageFrequencyScale
	}
	return weight
}
