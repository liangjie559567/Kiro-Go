package pool

import (
	"testing"
	"time"
)

func TestModelBreakerOpenHalfOpenClose(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newModelBreakerState()

	b.open("acct-1", "claude-opus-4.7", FailureReasonUpstream5xx, now, 30*time.Second)
	if b.canUse("acct-1", "claude-opus-4.7", now.Add(10*time.Second)) {
		t.Fatalf("expected account blocked while breaker is open")
	}
	if !b.canProbe("acct-1", "claude-opus-4.7", now.Add(31*time.Second)) {
		t.Fatalf("expected half-open probe after backoff")
	}
	b.markProbe("acct-1", "claude-opus-4.7", now.Add(31*time.Second))
	if b.canUse("acct-1", "claude-opus-4.7", now.Add(32*time.Second)) {
		t.Fatalf("expected active half-open probe to block parallel use")
	}
	b.success("acct-1", "claude-opus-4.7")
	if !b.canUse("acct-1", "claude-opus-4.7", now.Add(32*time.Second)) {
		t.Fatalf("expected account usable after success")
	}
}

func TestStickyAccountEscapesWhenBreakerOpen(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newModelBreakerState()

	b.rememberSticky("session-1", "claude-sonnet-4.5", "acct-1", now)
	b.open("acct-1", "claude-sonnet-4.5", FailureReasonRateLimited, now, time.Minute)
	if got := b.stickyAccount("session-1", "claude-sonnet-4.5", now.Add(5*time.Second)); got != "" {
		t.Fatalf("expected sticky account escaped while open, got %q", got)
	}
	b.rememberSticky("session-1", "claude-sonnet-4.5", "acct-2", now.Add(6*time.Second))
	if got := b.stickyAccount("session-1", "claude-sonnet-4.5", now.Add(7*time.Second)); got != "acct-2" {
		t.Fatalf("expected acct-2 sticky account, got %q", got)
	}
	if got := b.stickyAccount("session-1", "claude-sonnet-4.5", now.Add(31*time.Minute)); got != "" {
		t.Fatalf("expected expired sticky account to be ignored, got %q", got)
	}
}
