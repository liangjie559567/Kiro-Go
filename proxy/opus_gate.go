package proxy

import (
	"errors"
	"time"
)

var errOpus47GateTimeout = errors.New("opus 4.7 concurrency gate timeout")

type opus47Gate struct {
	slots chan struct{}
	queue chan struct{}
}

func newOpus47Gate(maxConcurrent, maxWaiting int) *opus47Gate {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	if maxWaiting < 0 {
		maxWaiting = 0
	}
	return &opus47Gate{
		slots: make(chan struct{}, maxConcurrent),
		queue: make(chan struct{}, maxConcurrent+maxWaiting),
	}
}

func (g *opus47Gate) acquire(timeout time.Duration) (func(), error) {
	if g == nil {
		return func() {}, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case g.queue <- struct{}{}:
	case <-timer.C:
		return nil, errOpus47GateTimeout
	}

	select {
	case g.slots <- struct{}{}:
		return func() {
			<-g.slots
			<-g.queue
		}, nil
	case <-timer.C:
		<-g.queue
		return nil, errOpus47GateTimeout
	}
}
