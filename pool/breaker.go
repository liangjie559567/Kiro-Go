package pool

import (
	"strings"
	"time"
)

const stickyIdleTTL = 30 * time.Minute

type breakerStatus string

const (
	breakerClosed   breakerStatus = "closed"
	breakerOpen     breakerStatus = "open"
	breakerHalfOpen breakerStatus = "half_open"
)

type modelBreakerState struct {
	entries map[string]*breakerEntry
	sticky  map[string]stickyEntry
}

type breakerEntry struct {
	Status  breakerStatus
	Reason  FailureReason
	OpenAt  time.Time
	RetryAt time.Time
	Probing bool
}

type stickyEntry struct {
	AccountID string
	UpdatedAt time.Time
}

func newModelBreakerState() *modelBreakerState {
	return &modelBreakerState{
		entries: make(map[string]*breakerEntry),
		sticky:  make(map[string]stickyEntry),
	}
}

func breakerKey(accountID, model string) string {
	return strings.TrimSpace(accountID) + "\x00" + normalizedBreakerModel(model)
}

func stickyKey(sessionKey, model string) string {
	return strings.TrimSpace(sessionKey) + "\x00" + normalizedBreakerModel(model)
}

func normalizedBreakerModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func (b *modelBreakerState) canUse(accountID, model string, now time.Time) bool {
	if b == nil {
		return true
	}
	e := b.entries[breakerKey(accountID, model)]
	if e == nil || e.Status == breakerClosed {
		return true
	}
	return e.Status == breakerOpen && !now.Before(e.RetryAt) && !e.Probing
}

func (b *modelBreakerState) canProbe(accountID, model string, now time.Time) bool {
	if b == nil {
		return false
	}
	e := b.entries[breakerKey(accountID, model)]
	return e != nil && e.Status == breakerOpen && !now.Before(e.RetryAt) && !e.Probing
}

func (b *modelBreakerState) isClosed(accountID, model string) bool {
	if b == nil {
		return true
	}
	e := b.entries[breakerKey(accountID, model)]
	return e == nil || e.Status == breakerClosed
}

func (b *modelBreakerState) markProbe(accountID, model string, now time.Time) {
	if b == nil {
		return
	}
	e := b.entries[breakerKey(accountID, model)]
	if e == nil {
		return
	}
	e.Status = breakerHalfOpen
	e.Probing = true
}

func (b *modelBreakerState) open(accountID, model string, reason FailureReason, now time.Time, delay time.Duration) {
	if b == nil || strings.TrimSpace(accountID) == "" {
		return
	}
	if delay <= 0 {
		delay = 30 * time.Second
	}
	b.entries[breakerKey(accountID, model)] = &breakerEntry{
		Status:  breakerOpen,
		Reason:  reason,
		OpenAt:  now,
		RetryAt: now.Add(delay),
	}
}

func (b *modelBreakerState) success(accountID, model string) {
	if b == nil {
		return
	}
	delete(b.entries, breakerKey(accountID, model))
}

func (b *modelBreakerState) rememberSticky(sessionKeyValue, model, accountID string, now time.Time) {
	if b == nil || strings.TrimSpace(sessionKeyValue) == "" || strings.TrimSpace(accountID) == "" {
		return
	}
	b.sticky[stickyKey(sessionKeyValue, model)] = stickyEntry{AccountID: strings.TrimSpace(accountID), UpdatedAt: now}
}

func (b *modelBreakerState) stickyAccount(sessionKeyValue, model string, now time.Time) string {
	if b == nil || strings.TrimSpace(sessionKeyValue) == "" {
		return ""
	}
	key := stickyKey(sessionKeyValue, model)
	e, ok := b.sticky[key]
	if !ok {
		return ""
	}
	if now.Sub(e.UpdatedAt) > stickyIdleTTL {
		delete(b.sticky, key)
		return ""
	}
	if !b.canUse(e.AccountID, model, now) {
		return ""
	}
	e.UpdatedAt = now
	b.sticky[key] = e
	return e.AccountID
}
