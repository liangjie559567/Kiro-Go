package proxy

import (
	"errors"
	"kiro-go/config"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var errOpus47GateTimeout = errors.New("opus 4.7 concurrency gate timeout")

type opus47Gate struct {
	slots chan struct{}
	queue chan struct{}
}

type modelAdmissionGateSet struct {
	mu           sync.RWMutex
	def          *adaptiveAdmissionGate
	models       map[string]*adaptiveAdmissionGate
	enabled      bool
	streamBypass bool
	pressure     map[string]*admissionPressureState
	pressureT    time.Duration
	now          func() time.Time
}

type adaptiveAdmissionGate struct {
	maxConcurrent int
	base          *opus47Gate
	reduced       *opus47Gate
}

type admissionPressureState struct {
	score     int
	expiresAt time.Time
}

type AdmissionPressureSnapshot struct {
	Model              string    `json:"model"`
	Score              int       `json:"score"`
	Active             bool      `json:"active"`
	ReducedConcurrency bool      `json:"reducedConcurrency"`
	ExpiresAt          time.Time `json:"expiresAt,omitempty"`
	ExpiresInMs        int64     `json:"expiresInMs,omitempty"`
	MaxConcurrent      int       `json:"maxConcurrent,omitempty"`
}

func newModelAdmissionGateSet(admission config.ModelAdmissionConfig) *modelAdmissionGateSet {
	g := &modelAdmissionGateSet{
		models:       make(map[string]*adaptiveAdmissionGate),
		streamBypass: admission.StreamBypass,
		pressure:     make(map[string]*admissionPressureState),
		pressureT:    30 * time.Second,
		now:          time.Now,
	}
	if admission.Default.MaxConcurrent > 0 {
		g.def = newAdaptiveAdmissionGate(admission.Default.MaxConcurrent, admission.Default.MaxWaiting)
		g.enabled = true
	}
	for model, rule := range admission.Models {
		model = normalizeAdmissionModel(model)
		if model == "" || rule.MaxConcurrent <= 0 {
			continue
		}
		g.models[model] = newAdaptiveAdmissionGate(rule.MaxConcurrent, rule.MaxWaiting)
		g.enabled = true
	}
	return g
}

func (g *modelAdmissionGateSet) shouldBypassStream(model string) bool {
	if g == nil {
		return true
	}
	g.mu.RLock()
	streamBypass := g.streamBypass
	g.mu.RUnlock()
	return streamBypass && !g.hasPressure(model)
}

func newAdaptiveAdmissionGate(maxConcurrent, maxWaiting int) *adaptiveAdmissionGate {
	return &adaptiveAdmissionGate{
		maxConcurrent: maxConcurrent,
		base:          newOpus47Gate(maxConcurrent, maxWaiting),
		reduced:       newOpus47Gate(1, maxWaiting),
	}
}

func normalizeAdmissionModel(model string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(model)), ".", "-")
}

func (g *modelAdmissionGateSet) acquire(model string, timeout time.Duration) (func(), bool, error) {
	if g == nil {
		return func() {}, false, nil
	}
	normalizedModel := normalizeAdmissionModel(model)
	g.mu.RLock()
	gate := g.models[normalizedModel]
	if gate == nil {
		gate = g.def
	}
	pressure := g.pressure[normalizedModel]
	now := g.now
	g.mu.RUnlock()
	if gate == nil {
		return func() {}, false, nil
	}
	inner := gate.base
	if pressure != nil && now != nil && now().Before(pressure.expiresAt) && pressure.score >= 2 && gate.maxConcurrent > 1 {
		inner = gate.reduced
	}
	release, err := inner.acquire(timeout)
	return release, true, err
}

func (g *modelAdmissionGateSet) hasPressure(model string) bool {
	if g == nil {
		return false
	}
	normalizedModel := normalizeAdmissionModel(model)
	if normalizedModel == "" {
		return false
	}
	g.mu.RLock()
	pressure := g.pressure[normalizedModel]
	now := g.now
	g.mu.RUnlock()
	if pressure == nil || pressure.score < 2 {
		return false
	}
	if now == nil {
		now = time.Now
	}
	return now().Before(pressure.expiresAt)
}

func (g *modelAdmissionGateSet) recordPressure(model string, statusCode int, latency time.Duration) {
	g.recordPressureUntil(model, statusCode, latency, time.Time{})
}

func (g *modelAdmissionGateSet) recordPressureUntil(model string, statusCode int, latency time.Duration, retryAt time.Time) {
	if g == nil {
		return
	}
	model = normalizeAdmissionModel(model)
	if model == "" {
		return
	}
	score := 0
	switch {
	case statusCode == http.StatusTooManyRequests || statusCode >= 500:
		score = 2
	case latency >= 10*time.Second:
		score = 1
	default:
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pressure == nil {
		g.pressure = make(map[string]*admissionPressureState)
	}
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	ttl := g.pressureT
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	expiresAt := now.Add(ttl)
	if retryAt.After(expiresAt) {
		expiresAt = retryAt
	}
	state := g.pressure[model]
	if state == nil || now.After(state.expiresAt) {
		state = &admissionPressureState{}
		g.pressure[model] = state
	}
	state.score += score
	if state.score > 6 {
		state.score = 6
	}
	state.expiresAt = expiresAt
}

func (g *modelAdmissionGateSet) snapshot() []AdmissionPressureSnapshot {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	out := make([]AdmissionPressureSnapshot, 0, len(g.pressure))
	for model, state := range g.pressure {
		if state == nil {
			continue
		}
		active := now.Before(state.expiresAt)
		gate := g.models[model]
		if gate == nil {
			gate = g.def
		}
		maxConcurrent := 0
		if gate != nil {
			maxConcurrent = gate.maxConcurrent
		}
		expiresIn := int64(0)
		if active {
			expiresIn = state.expiresAt.Sub(now).Milliseconds()
		}
		out = append(out, AdmissionPressureSnapshot{
			Model:              model,
			Score:              state.score,
			Active:             active,
			ReducedConcurrency: active && state.score >= 2 && maxConcurrent > 1,
			ExpiresAt:          state.expiresAt,
			ExpiresInMs:        expiresIn,
			MaxConcurrent:      maxConcurrent,
		})
	}
	g.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].Model < out[j].Model
	})
	return out
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
