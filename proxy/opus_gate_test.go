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
	release1, gated, err := g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("first acquire after recovery gated=%v err=%v", gated, err)
	}
	defer release1()
	release2, gated, err := g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("second acquire after recovery gated=%v err=%v", gated, err)
	}
	defer release2()
	release3, gated, err := g.acquire("claude-opus-4.7", time.Millisecond)
	if err != errOpus47GateTimeout || !gated {
		if release3 != nil {
			release3()
		}
		t.Fatalf("third acquire after recovery gated=%v err=%v, want timeout", gated, err)
	}
}

func TestModelAdmissionGateEnforcesReducedLimitAcrossExistingRequests(t *testing.T) {
	g := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 0},
		},
	})
	releases := make([]func(), 0, 4)
	for i := 0; i < 4; i++ {
		release, gated, err := g.acquire("claude-opus-4.7", time.Second)
		if err != nil || !gated {
			t.Fatalf("base acquire %d gated=%v err=%v", i+1, gated, err)
		}
		releases = append(releases, release)
	}
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 2 {
		t.Fatalf("after pressure effective concurrency = %d, want 2", got)
	}
	snapshots := g.snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot after pressure, got %#v", snapshots)
	}
	if snapshots[0].ActiveRequests != 4 || snapshots[0].EffectiveMaxConcurrent != 2 {
		t.Fatalf("expected reduced limit to share active request accounting, got %#v", snapshots[0])
	}
	if release, gated, err := g.acquire("claude-opus-4.7", time.Millisecond); err != errOpus47GateTimeout || !gated {
		if release != nil {
			release()
		}
		t.Fatalf("acquire with 4 active after reduction gated=%v err=%v, want timeout", gated, err)
	}
	releases[0]()
	if release, gated, err := g.acquire("claude-opus-4.7", time.Millisecond); err != errOpus47GateTimeout || !gated {
		if release != nil {
			release()
		}
		t.Fatalf("acquire with 3 active after reduction gated=%v err=%v, want timeout", gated, err)
	}
	releases[1]()
	if release, gated, err := g.acquire("claude-opus-4.7", time.Millisecond); err != errOpus47GateTimeout || !gated {
		if release != nil {
			release()
		}
		t.Fatalf("acquire with 2 active after reduction gated=%v err=%v, want timeout", gated, err)
	}
	releases[2]()
	release, gated, err := g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("acquire with 1 active after reduction gated=%v err=%v", gated, err)
	}
	release()
	releases[3]()
}

func TestModelAdmissionGateRecordsQueueTimeoutPressure(t *testing.T) {
	g := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 1, MaxWaiting: 0},
		},
	})
	release, gated, err := g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("initial acquire gated=%v err=%v", gated, err)
	}
	defer release()
	if releaseTimeout, gated, err := g.acquire("claude-opus-4.7", time.Millisecond); err != errOpus47GateTimeout || !gated {
		if releaseTimeout != nil {
			releaseTimeout()
		}
		t.Fatalf("timeout acquire gated=%v err=%v, want timeout", gated, err)
	}
	snapshots := g.snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("expected one pressure snapshot, got %#v", snapshots)
	}
	if snapshots[0].RecentQueueTimeouts != 1 {
		t.Fatalf("recent queue timeouts = %d, want 1 in %#v", snapshots[0].RecentQueueTimeouts, snapshots[0])
	}
}
