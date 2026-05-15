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
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "eof"), strings.Contains(msg, "network"):
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
	cooldowns     map[string]time.Time // 账号冷却时间
	errorCounts   map[string]int       // 连续错误计数
	failures      map[string]FailureReason
}

var (
	pool     *AccountPool
	poolOnce sync.Once
)

// GetPool 获取全局账号池单例
func GetPool() *AccountPool {
	poolOnce.Do(func() {
		pool = &AccountPool{
			cooldowns:   make(map[string]time.Time),
			errorCounts: make(map[string]int),
			failures:    make(map[string]FailureReason),
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

	now := time.Now()
	p.clearExpiredCooldownsLocked(now)
	n := len(p.accounts)
	seen := make(map[string]bool)

	// 加权轮询查找可用账号
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
		if acc.ExpiresAt > 0 && time.Now().Unix() > acc.ExpiresAt-300 {
			seen[acc.ID] = true
			continue
		}

		// 跳过额度已用尽的账号（适用于所有订阅类型）
		if isOverUsageLimit(*acc) && !acc.AllowOverage {
			seen[acc.ID] = true
			continue
		}

		return acc
	}

	// 无可用账号时直接返回 nil。冷却账号必须等到冷却结束后再重新进入调度，
	// 否则 Opus 4.7 容量不足时会在同一冷却窗口内反复打同一个账号。
	for i := range p.accounts {
		acc := &p.accounts[i]
		if excluded != nil && excluded[acc.ID] {
			continue
		}
		// 额度用尽的账号不作为 fallback
		if isOverUsageLimit(*acc) && !acc.AllowOverage {
			continue
		}
		if acc.CooldownUntil > 0 && now.Unix() < acc.CooldownUntil {
			continue
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok {
			if now.Before(cooldown) {
				continue
			}
			return acc
		} else {
			return acc
		}
	}
	return nil
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
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureStateLocked()
	p.clearFailureStateLocked(id)
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
