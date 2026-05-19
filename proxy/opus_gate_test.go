package proxy

import (
	"kiro-go/config"
	"net/http"
	"testing"
	"time"
)

func TestModelAdmissionGateReducesAndRecoversEffectiveConcurrency(t *testing.T) {
	g := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 10},
		},
	})
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 4 {
		t.Fatalf("initial effective concurrency = %d, want 4", got)
	}
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 2 {
		t.Fatalf("after first pressure effective concurrency = %d, want 2", got)
	}
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 1 {
		t.Fatalf("after second pressure effective concurrency = %d, want 1", got)
	}
	now = now.Add(2 * time.Minute)
	g.recordSuccess("claude-opus-4.7", 2*time.Second)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 2 {
		t.Fatalf("after recovery effective concurrency = %d, want 2", got)
	}
}
