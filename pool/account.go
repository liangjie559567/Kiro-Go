// Package pool 账号池管理
// 实现轮询负载均衡、错误冷却、Token 刷新
package pool

import (
	"kiro-go/config"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const overageFrequencyScale = 10
const tokenRefreshSkewSeconds int64 = 120

type FailureReason string

const (
	FailureReasonUnknown          FailureReason = "unknown"
	FailureReasonQuotaExhausted   FailureReason = "quota_exhausted"
	FailureReasonAuthExpired      FailureReason = "auth_expired"
	FailureReasonSuspended        FailureReason = "suspended"
	FailureReasonRateLimited      FailureReason = "rate_limited"
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

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "quota exhausted"):
		return FailureReasonQuotaExhausted
	case strings.Contains(msg, "temporarily_suspended"), strings.Contains(msg, "account suspended"), strings.Contains(msg, "suspended"):
		return FailureReasonSuspended
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

// AccountPool 账号池
type AccountPool struct {
	mu            sync.RWMutex
	accounts      []config.Account
	totalAccounts int
	currentIndex  uint64
	cooldowns     map[string]time.Time       // 账号冷却时间
	errorCounts   map[string]int             // 连续错误计数
	failures      map[string]FailureReason   // Last failure classification
	modelLists    map[string]map[string]bool // accountID -> set of modelIDs
	runtimeHealth map[string]*runtimeHealthState
}

var (
	pool     *AccountPool
	poolOnce sync.Once
)

// GetPool 获取全局账号池单例
func GetPool() *AccountPool {
	poolOnce.Do(func() {
		pool = &AccountPool{
			cooldowns:     make(map[string]time.Time),
			errorCounts:   make(map[string]int),
			failures:      make(map[string]FailureReason),
			modelLists:    make(map[string]map[string]bool),
			runtimeHealth: make(map[string]*runtimeHealthState),
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
	if p.modelLists == nil {
		p.modelLists = make(map[string]map[string]bool)
	}
	if p.runtimeHealth == nil {
		p.runtimeHealth = make(map[string]*runtimeHealthState)
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

		// 跳过冷却中的账号
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			seen[acc.ID] = true
			continue
		}

		// 跳过账号自身持久化冷却中的账号
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			seen[acc.ID] = true
			continue
		}

		// 跳过即将过期的 Token
		if acc.ExpiresAt > 0 && time.Now().Unix() > acc.ExpiresAt-tokenRefreshSkewSeconds {
			seen[acc.ID] = true
			continue
		}

		// 跳过额度已用尽的账号（账号级 AllowOverage 或全局 AllowOverUsage 可放行）
		if isOverUsageLimit(*acc) && !acc.AllowOverage && !allowOverUsage {
			seen[acc.ID] = true
			continue
		}

		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID) {
			best = acc
		}
		if p.isIdleHealthyLocked(acc.ID) {
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
		// 额度用尽的账号不作为 fallback（除非账号级或全局允许超额）
		if isOverUsageLimit(*acc) && !acc.AllowOverage && !allowOverUsage {
			continue
		}
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			continue
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok {
			if now.Before(cooldown) {
				continue
			}
			if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID) {
				best = acc
			}
		} else {
			if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID) {
				best = acc
			}
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

	if len(p.accounts) == 0 {
		return nil
	}

	allowOverUsage := config.GetAllowOverUsage()
	now := time.Now()
	p.clearExpiredCooldownsLocked(now)
	n := len(p.accounts)
	seen := make(map[string]bool)
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
		if !p.accountHasModel(acc.ID, model) {
			seen[acc.ID] = true
			continue
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			seen[acc.ID] = true
			continue
		}
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			seen[acc.ID] = true
			continue
		}
		if acc.ExpiresAt > 0 && time.Now().Unix() > acc.ExpiresAt-tokenRefreshSkewSeconds {
			seen[acc.ID] = true
			continue
		}
		if isOverUsageLimit(*acc) && !acc.AllowOverage && !allowOverUsage {
			seen[acc.ID] = true
			continue
		}
		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID) {
			best = acc
		}
		if p.isIdleHealthyLocked(acc.ID) {
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
		if isOverUsageLimit(*acc) && !acc.AllowOverage && !allowOverUsage {
			continue
		}
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			continue
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok {
			if now.Before(cooldown) {
				continue
			}
			if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID) {
				best = acc
			}
		}
		if best == nil || p.isBetterCandidateLocked(acc.ID, best.ID) {
			best = acc
		}
	}
	return best
}

func (p *AccountPool) isIdleHealthyLocked(id string) bool {
	health := p.runtimeHealth[id]
	if health == nil {
		return true
	}
	return health.activeConnections == 0 && health.score() >= 90
}

func (p *AccountPool) isBetterCandidateLocked(candidateID, currentID string) bool {
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
	if candidate.score() != current.score() {
		return candidate.score() > current.score()
	}
	return candidate.avgLatencyMS < current.avgLatencyMS
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
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			continue
		}
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
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
	p.mu.RLock()
	defer p.mu.RUnlock()
	if cooldown, ok := p.cooldowns[id]; ok && now.Before(cooldown) {
		return true
	}
	for _, acc := range p.accounts {
		if acc.ID == id && acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			return true
		}
	}
	return false
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
