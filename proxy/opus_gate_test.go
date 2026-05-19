package proxy

import (
	"kiro-go/config"
	"net/http"
	"sync"
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

func TestModelAdmissionGateExpiredPressureWaitsForSuccessRecovery(t *testing.T) {
	g := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 0},
		},
	})
	now := time.Unix(2000, 0)
	g.now = func() time.Time { return now }
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 1 {
		t.Fatalf("after pressure effective concurrency = %d, want 1", got)
	}

	now = now.Add(2 * time.Minute)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 1 {
		t.Fatalf("expired pressure effective concurrency = %d, want 1", got)
	}
	release1, gated, err := g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("first acquire after expiry gated=%v err=%v", gated, err)
	}
	if release2, gated, err := g.acquire("claude-opus-4.7", time.Millisecond); err != errOpus47GateTimeout || !gated {
		if release2 != nil {
			release2()
		}
		t.Fatalf("second acquire before recovery gated=%v err=%v, want timeout", gated, err)
	}
	release1()

	now = now.Add(2 * time.Minute)
	g.recordSuccess("claude-opus-4.7", 2*time.Second)
	if got := g.effectiveMaxConcurrent("claude-opus-4.7"); got != 2 {
		t.Fatalf("after recovery success effective concurrency = %d, want 2", got)
	}
	release1, gated, err = g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("first acquire after recovery gated=%v err=%v", gated, err)
	}
	defer release1()
	release2, gated, err := g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("second acquire after recovery gated=%v err=%v", gated, err)
	}
	defer release2()
	if release3, gated, err := g.acquire("claude-opus-4.7", time.Millisecond); err != errOpus47GateTimeout || !gated {
		if release3 != nil {
			release3()
		}
		t.Fatalf("third acquire after recovery gated=%v err=%v, want timeout", gated, err)
	}
}

func TestModelAdmissionGateWaiterWakesWhenRecoveryIncreasesLimit(t *testing.T) {
	g := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 2, MaxWaiting: 1},
		},
	})
	now := time.Unix(3000, 0)
	g.now = func() time.Time { return now }
	g.recordPressure("claude-opus-4.7", http.StatusTooManyRequests, 500*time.Millisecond)
	release, gated, err := g.acquire("claude-opus-4.7", time.Second)
	if err != nil || !gated {
		t.Fatalf("initial acquire gated=%v err=%v", gated, err)
	}
	defer release()

	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		releaseWaiter, _, err := g.acquire("claude-opus-4.7", time.Second)
		if err != nil {
			errs <- err
			return
		}
		acquired <- releaseWaiter
	}()
	defer wg.Wait()

	deadline := time.After(200 * time.Millisecond)
	for {
		snapshots := g.snapshot()
		if len(snapshots) == 1 && snapshots[0].QueueDepth == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("waiter did not queue, snapshots=%#v", snapshots)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	now = now.Add(2 * time.Minute)
	g.recordSuccess("claude-opus-4.7", 2*time.Second)
	select {
	case releaseWaiter := <-acquired:
		releaseWaiter()
	case err := <-errs:
		t.Fatalf("waiter acquire failed: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("waiter did not wake after recovery increased effective limit")
	}
}

func TestModelAdmissionGateSuccessOnUnpressuredModelDoesNotCreateSnapshot(t *testing.T) {
	g := newModelAdmissionGateSet(config.ModelAdmissionConfig{
		Models: map[string]config.ModelAdmissionRule{
			"claude-opus-4.7": {MaxConcurrent: 4, MaxWaiting: 10},
		},
	})
	g.recordSuccess("claude-opus-4.7", 2*time.Second)
	if snapshots := g.snapshot(); len(snapshots) != 0 {
		t.Fatalf("unpressured success created pressure snapshot: %#v", snapshots)
	}
}
