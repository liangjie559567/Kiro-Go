package proxy

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestContentContinuityGateWaitsUntilCapacityRecovers(t *testing.T) {
	gate := newContentContinuityGate()
	started := make(chan struct{})
	done := make(chan contentContinuityWaitResult, 1)
	var blocked atomic.Bool
	blocked.Store(true)
	go func() {
		close(started)
		done <- gate.wait("claude-opus-4.7", 100*time.Millisecond, func() bool {
			return blocked.Load()
		})
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
	blocked.Store(false)
	gate.broadcast("claude-opus-4.7")
	select {
	case got := <-done:
		if !got.Waited {
			t.Fatalf("expected waited result")
		}
		if got.TimedOut {
			t.Fatalf("did not expect timeout")
		}
	case <-time.After(time.Second):
		t.Fatalf("wait did not return after broadcast")
	}
}

func TestContentContinuityGateTimesOut(t *testing.T) {
	gate := newContentContinuityGate()
	got := gate.wait("claude-opus-4.7", time.Millisecond, func() bool {
		return true
	})
	if !got.Waited {
		t.Fatalf("expected waited result")
	}
	if !got.TimedOut {
		t.Fatalf("expected timeout")
	}
	if got.Duration <= 0 {
		t.Fatalf("expected positive duration")
	}
}

func TestContentContinuityGateRejectsWhenQueueFull(t *testing.T) {
	gate := newContentContinuityGate()
	gate.setMaxDepthForTest(1)
	release, ok := gate.tryEnter("claude-opus-4.7")
	if !ok {
		t.Fatalf("expected first waiter to enter")
	}
	defer release()
	if _, ok := gate.tryEnter("claude-opus-4.7"); ok {
		t.Fatalf("expected second waiter to be rejected")
	}
}
