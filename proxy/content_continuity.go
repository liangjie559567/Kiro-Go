package proxy

import (
	"context"
	"kiro-go/config"
	"strings"
	"sync"
	"time"
)

var contentContinuityGateGlobal = newContentContinuityGate()

type contentContinuityGate struct {
	mu       sync.Mutex
	notify   map[string]chan struct{}
	waiting  map[string]int
	maxDepth int
}

type contentContinuityWaitResult struct {
	Waited   bool
	TimedOut bool
	Canceled bool
	Duration time.Duration
}

func newContentContinuityGate() *contentContinuityGate {
	gate := &contentContinuityGate{
		notify:   make(map[string]chan struct{}),
		waiting:  make(map[string]int),
		maxDepth: 300,
	}
	if cfg := config.Get(); cfg != nil && cfg.ContentContinuity.MaxQueueDepth > 0 {
		gate.maxDepth = cfg.ContentContinuity.MaxQueueDepth
	}
	return gate
}

func contentContinuityWaitDuration(model string, deadline time.Time) time.Duration {
	cfg := config.Get()
	if cfg == nil || !cfg.ContentContinuity.SupportsModel(model) {
		return 0
	}
	wait := time.Duration(cfg.ContentContinuity.MaxQueueWaitSeconds) * time.Second
	if wait <= 0 {
		return 0
	}
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0
		}
		if remaining < wait {
			wait = remaining
		}
	}
	return wait
}

func stableContentContinuityWaitDuration(model string) time.Duration {
	return stableContentContinuityWaitDurationUntil(model, time.Time{})
}

func stableContentContinuityWaitDurationUntil(model string, deadline time.Time) time.Duration {
	cfg := config.Get()
	wait := time.Duration(0)
	stableOpus47 := isOpus47Model(model)
	if cfg == nil || !cfg.ContentContinuity.SupportsModel(model) {
		if stableOpus47 {
			wait = minStableClaudeCapacityWait
		}
	} else {
		wait = time.Duration(cfg.ContentContinuity.MaxQueueWaitSeconds) * time.Second
		if wait <= 0 {
			wait = minStableClaudeCapacityWait
		}
	}
	if wait <= 0 {
		return 0
	}
	if stableOpus47 && maxStableClaudeCapacityWait > 0 && wait > maxStableClaudeCapacityWait {
		wait = maxStableClaudeCapacityWait
	}
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0
		}
		if remaining < wait {
			wait = remaining
		}
	}
	return wait
}

func (g *contentContinuityGate) setMaxDepthForTest(maxDepth int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.maxDepth = maxDepth
}

func (g *contentContinuityGate) tryEnter(model string) (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	model = normalizeAdmissionModel(model)
	if model == "" {
		model = "default"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maxDepth > 0 && g.waiting[model] >= g.maxDepth {
		return nil, false
	}
	g.waiting[model]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			if g.waiting[model] > 0 {
				g.waiting[model]--
			}
			g.mu.Unlock()
		})
	}, true
}

func (g *contentContinuityGate) channelLocked(model string) chan struct{} {
	ch := g.notify[model]
	if ch == nil {
		ch = make(chan struct{})
		g.notify[model] = ch
	}
	return ch
}

func (g *contentContinuityGate) wait(model string, timeout time.Duration, stillBlocked func() bool) contentContinuityWaitResult {
	return g.waitContext(context.Background(), model, timeout, stillBlocked)
}

func (g *contentContinuityGate) waitContext(ctx context.Context, model string, timeout time.Duration, stillBlocked func() bool) contentContinuityWaitResult {
	return g.waitContextWithHeartbeat(ctx, model, timeout, stillBlocked, 0, nil)
}

func (g *contentContinuityGate) waitContextWithHeartbeat(ctx context.Context, model string, timeout time.Duration, stillBlocked func() bool, heartbeatEvery time.Duration, heartbeat func()) contentContinuityWaitResult {
	start := time.Now()
	result := contentContinuityWaitResult{Waited: true}
	if g == nil || timeout <= 0 {
		result.TimedOut = true
		result.Duration = time.Since(start)
		return result
	}
	release, ok := g.tryEnter(model)
	if !ok {
		result.TimedOut = true
		result.Duration = time.Since(start)
		return result
	}
	defer release()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var heartbeatTicker *time.Ticker
	var heartbeatC <-chan time.Time
	if heartbeatEvery > 0 && heartbeat != nil {
		heartbeat()
		heartbeatTicker = time.NewTicker(heartbeatEvery)
		heartbeatC = heartbeatTicker.C
		defer heartbeatTicker.Stop()
	}
	model = normalizeAdmissionModel(model)
	if strings.TrimSpace(model) == "" {
		model = "default"
	}
	for {
		if stillBlocked != nil && !stillBlocked() {
			result.Duration = time.Since(start)
			return result
		}
		g.mu.Lock()
		ch := g.channelLocked(model)
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			result.Canceled = true
			result.Duration = time.Since(start)
			return result
		case <-ch:
		case <-heartbeatC:
			heartbeat()
		case <-timer.C:
			result.TimedOut = true
			result.Duration = time.Since(start)
			return result
		}
	}
}

func (g *contentContinuityGate) broadcast(model string) {
	if g == nil {
		return
	}
	model = normalizeAdmissionModel(model)
	if model == "" {
		model = "default"
	}
	g.mu.Lock()
	ch := g.channelLocked(model)
	close(ch)
	g.notify[model] = make(chan struct{})
	g.mu.Unlock()
}
