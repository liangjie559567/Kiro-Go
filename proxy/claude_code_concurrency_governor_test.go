package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"kiro-go/config"
)

func testClaudeCodeGovernorConfig() config.ClaudeCodeGovernorConfig {
	return config.ClaudeCodeGovernorConfig{
		Enabled:                         true,
		Models:                          []string{"claude-opus-4.7"},
		InteractiveReservedPerSession:   1,
		SubagentMaxConcurrentPerSession: 2,
	}
}

func TestClaudeCodeGovernorAllowsMainWhenSubagentsFull(t *testing.T) {
	gov := newClaudeCodeConcurrencyGovernor(testClaudeCodeGovernorConfig())

	first, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("first subagent acquire: %v", err)
	}
	defer first.Release()

	second, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:         "claude-opus-4.7",
		SessionID:     "session-1",
		ParentAgentID: "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("second subagent acquire: %v", err)
	}
	defer second.Release()

	interactive, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("interactive acquire with full subagents: %v", err)
	}
	defer interactive.Release()

	if !interactive.Applied {
		t.Fatalf("interactive Applied = false, want true")
	}
	if interactive.Role != "interactive" {
		t.Fatalf("interactive Role = %q, want interactive", interactive.Role)
	}
}

func TestClaudeCodeGovernorQueuesExtraSubagentsPerSession(t *testing.T) {
	gov := newClaudeCodeConcurrencyGovernor(testClaudeCodeGovernorConfig())

	first, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("first subagent acquire: %v", err)
	}
	defer first.Release()

	second, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-2",
	}, time.Second)
	if err != nil {
		t.Fatalf("second subagent acquire: %v", err)
	}
	defer second.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	extra, err := gov.Acquire(ctx, claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-3",
	}, 200*time.Millisecond)
	if extra.Release != nil {
		extra.Release()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("extra subagent acquire err = %v, want context deadline exceeded", err)
	}
}

func TestClaudeCodeGovernorDoesNotApplyWithoutSession(t *testing.T) {
	gov := newClaudeCodeConcurrencyGovernor(testClaudeCodeGovernorConfig())

	decision, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:   "claude-opus-4.7",
		AgentID: "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("acquire without session: %v", err)
	}
	defer decision.Release()

	if decision.Applied {
		t.Fatalf("Applied = true, want false")
	}
}

func TestClaudeCodeGovernorDoesNotApplyToNonOpusModel(t *testing.T) {
	gov := newClaudeCodeConcurrencyGovernor(testClaudeCodeGovernorConfig())

	decision, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-sonnet-4.5",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("acquire non-opus model: %v", err)
	}
	defer decision.Release()

	if decision.Applied {
		t.Fatalf("Applied = true, want false")
	}
}

func TestClaudeCodeGovernorCanceledContextBeforeAcquire(t *testing.T) {
	gov := newClaudeCodeConcurrencyGovernor(testClaudeCodeGovernorConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	decision, err := gov.Acquire(ctx, claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
	}, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire err = %v, want context canceled", err)
	}
	if decision.Applied {
		t.Fatalf("Applied = true, want false")
	}
	decision.Release()
}

func TestClaudeCodeGovernorEnforcesQueueMaxDepth(t *testing.T) {
	cfg := testClaudeCodeGovernorConfig()
	cfg.QueueMaxDepth = 1
	gov := newClaudeCodeConcurrencyGovernor(cfg)

	first, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("first subagent acquire: %v", err)
	}
	defer first.Release()

	second, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-2",
	}, time.Second)
	if err != nil {
		t.Fatalf("second subagent acquire: %v", err)
	}
	defer second.Release()

	waiterStarted := make(chan struct{})
	waiterDone := make(chan error, 1)
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	defer cancelWaiter()
	go func() {
		close(waiterStarted)
		decision, err := gov.Acquire(waiterCtx, claudeCodeAdmissionRequest{
			Model:     "claude-opus-4.7",
			SessionID: "session-1",
			AgentID:   "agent-3",
		}, time.Second)
		decision.Release()
		waiterDone <- err
	}()

	<-waiterStarted
	waitUntilGovernorWaiting(t, gov, "session-1", 1)

	start := time.Now()
	overflow, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-4",
	}, time.Second)
	overflow.Release()
	if !errors.Is(err, errClaudeCodeGovernorQueueFull) {
		t.Fatalf("overflow acquire err = %v, want queue full", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("overflow acquire took %s, want quick queue-full failure", elapsed)
	}

	cancelWaiter()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first waiter err = %v, want context canceled", err)
	}
}

func TestClaudeCodeGovernorReleaseIsIdempotent(t *testing.T) {
	gov := newClaudeCodeConcurrencyGovernor(testClaudeCodeGovernorConfig())

	decision, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}, time.Second)
	if err != nil {
		t.Fatalf("subagent acquire: %v", err)
	}

	decision.Release()
	decision.Release()

	first, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-2",
	}, time.Second)
	if err != nil {
		t.Fatalf("first acquire after double release: %v", err)
	}
	defer first.Release()

	second, err := gov.Acquire(context.Background(), claudeCodeAdmissionRequest{
		Model:     "claude-opus-4.7",
		SessionID: "session-1",
		AgentID:   "agent-3",
	}, time.Second)
	if err != nil {
		t.Fatalf("second acquire after double release: %v", err)
	}
	defer second.Release()
}

func waitUntilGovernorWaiting(t *testing.T, gov *claudeCodeConcurrencyGovernor, sessionID string, want int) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		gov.mu.Lock()
		got := 0
		if gate := gov.sessions[sessionID]; gate != nil {
			got = gate.waiting
		}
		gov.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("governor waiting count for %s did not reach %d", sessionID, want)
}
