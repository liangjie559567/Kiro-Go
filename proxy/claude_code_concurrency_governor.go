package proxy

import (
	"context"
	"errors"
	"sync"
	"time"

	"kiro-go/config"
)

var errClaudeCodeGovernorQueueFull = errors.New("claude code governor queue full")

type claudeCodeConcurrencyGovernor struct {
	mu       sync.Mutex
	cfg      config.ClaudeCodeGovernorConfig
	sessions map[string]*claudeCodeSessionGate
}

type claudeCodeSessionGate struct {
	activeInteractive int
	activeSubagents   int
	waiting           int
}

type claudeCodeAdmissionRequest struct {
	Model         string
	SessionID     string
	AgentID       string
	ParentAgentID string
	Stream        bool
	ClaudeFormat  bool
}

type claudeCodeAdmissionDecision struct {
	Release func()
	Applied bool
	Role    string
	Wait    time.Duration
}

func newClaudeCodeConcurrencyGovernor(cfg config.ClaudeCodeGovernorConfig) *claudeCodeConcurrencyGovernor {
	return &claudeCodeConcurrencyGovernor{
		cfg:      normalizeRuntimeClaudeCodeGovernorConfig(cfg),
		sessions: make(map[string]*claudeCodeSessionGate),
	}
}

func normalizeRuntimeClaudeCodeGovernorConfig(cfg config.ClaudeCodeGovernorConfig) config.ClaudeCodeGovernorConfig {
	if cfg.Models == nil {
		cfg.Models = []string{"claude-opus-4.7"}
	} else {
		cfg.Models = append([]string(nil), cfg.Models...)
	}
	if cfg.InteractiveReservedPerSession == 0 {
		cfg.InteractiveReservedPerSession = 1
	}
	if cfg.SubagentMaxConcurrentPerSession == 0 {
		cfg.SubagentMaxConcurrentPerSession = 2
	}
	if cfg.QueueMaxDepth == 0 {
		cfg.QueueMaxDepth = 300
	}
	return cfg
}

func (g *claudeCodeConcurrencyGovernor) Acquire(ctx context.Context, req claudeCodeAdmissionRequest, timeout time.Duration) (claudeCodeAdmissionDecision, error) {
	noop := claudeCodeAdmissionDecision{Release: func() {}}
	if g == nil || !g.cfg.Enabled || !g.supportsModel(req.Model) || req.SessionID == "" {
		return noop, nil
	}
	if err := ctx.Err(); err != nil {
		return noop, err
	}

	role := "interactive"
	if req.AgentID != "" || req.ParentAgentID != "" {
		role = "subagent"
	}

	start := time.Now()
	if release, ok := g.tryAcquire(req.SessionID, role); ok {
		return claudeCodeAdmissionDecision{
			Release: release,
			Applied: true,
			Role:    role,
			Wait:    time.Since(start),
		}, nil
	}
	if timeout <= 0 {
		return noop, context.DeadlineExceeded
	}
	if !g.reserveWaiter(req.SessionID) {
		return noop, errClaudeCodeGovernorQueueFull
	}
	defer g.releaseWaiter(req.SessionID)

	deadline := start.Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return noop, context.DeadlineExceeded
		}
		poll := 10 * time.Millisecond
		if remaining < poll {
			poll = remaining
		}

		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return noop, ctx.Err()
		case <-timer.C:
		}

		if err := ctx.Err(); err != nil {
			return noop, err
		}
		if release, ok := g.tryAcquire(req.SessionID, role); ok {
			if err := ctx.Err(); err != nil {
				release()
				return noop, err
			}
			return claudeCodeAdmissionDecision{
				Release: release,
				Applied: true,
				Role:    role,
				Wait:    time.Since(start),
			}, nil
		}
	}
}

func (g *claudeCodeConcurrencyGovernor) supportsModel(model string) bool {
	model = normalizeAdmissionModel(model)
	if model == "" {
		return false
	}
	for _, candidate := range g.cfg.Models {
		if normalizeAdmissionModel(candidate) == model {
			return true
		}
	}
	return false
}

func (g *claudeCodeConcurrencyGovernor) reserveWaiter(sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	gate := g.sessions[sessionID]
	if gate == nil {
		gate = &claudeCodeSessionGate{}
		g.sessions[sessionID] = gate
	}
	if gate.waiting >= g.cfg.QueueMaxDepth {
		return false
	}
	gate.waiting++
	return true
}

func (g *claudeCodeConcurrencyGovernor) releaseWaiter(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	gate := g.sessions[sessionID]
	if gate == nil {
		return
	}
	if gate.waiting > 0 {
		gate.waiting--
	}
	if gate.activeInteractive == 0 && gate.activeSubagents == 0 && gate.waiting == 0 {
		delete(g.sessions, sessionID)
	}
}

func (g *claudeCodeConcurrencyGovernor) tryAcquire(sessionID, role string) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	gate := g.sessions[sessionID]
	if gate == nil {
		gate = &claudeCodeSessionGate{}
		g.sessions[sessionID] = gate
	}

	switch role {
	case "subagent":
		if gate.activeSubagents >= g.cfg.SubagentMaxConcurrentPerSession {
			return nil, false
		}
		gate.activeSubagents++
	default:
		limit := g.cfg.InteractiveReservedPerSession
		if limit <= 0 {
			limit = 1
		}
		if gate.activeInteractive >= limit {
			return nil, false
		}
		gate.activeInteractive++
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			g.release(sessionID, role)
		})
	}, true
}

func (g *claudeCodeConcurrencyGovernor) release(sessionID, role string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	gate := g.sessions[sessionID]
	if gate == nil {
		return
	}
	switch role {
	case "subagent":
		if gate.activeSubagents > 0 {
			gate.activeSubagents--
		}
	default:
		if gate.activeInteractive > 0 {
			gate.activeInteractive--
		}
	}
	if gate.activeInteractive == 0 && gate.activeSubagents == 0 && gate.waiting == 0 {
		delete(g.sessions, sessionID)
	}
}
