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
