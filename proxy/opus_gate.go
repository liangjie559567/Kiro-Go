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
	slots  chan struct{}
	queue  chan struct{}
	mu     sync.RWMutex
	active int
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
	maxWaiting    int
	base          *opus47Gate
	reduced       *opus47Gate
	dynamic       map[int]*opus47Gate
}

type admissionPressureState struct {
	score                  int
	expiresAt              time.Time
	effectiveMaxConcurrent int
	recentCapacityErrors   int
	recentQueueTimeouts    int
	recentSuccesses        int
	lastPressureAt         time.Time
	lastSuccessAt          time.Time
}

type AdmissionPressureSnapshot struct {
	Model                  string    `json:"model"`
	Score                  int       `json:"score"`
	Active                 bool      `json:"active"`
	ReducedConcurrency     bool      `json:"reducedConcurrency"`
	ExpiresAt              time.Time `json:"expiresAt,omitempty"`
	ExpiresInMs            int64     `json:"expiresInMs,omitempty"`
	MaxConcurrent          int       `json:"maxConcurrent,omitempty"`
	EffectiveMaxConcurrent int       `json:"effectiveMaxConcurrent,omitempty"`
	QueueDepth             int       `json:"queueDepth,omitempty"`
	ActiveRequests         int       `json:"activeRequests,omitempty"`
	RecentCapacityErrors   int       `json:"recentCapacityErrors,omitempty"`
	RecentQueueTimeouts    int       `json:"recentQueueTimeouts,omitempty"`
	RecentSuccesses        int       `json:"recentSuccesses,omitempty"`
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
		maxWaiting:    maxWaiting,
		base:          newOpus47Gate(maxConcurrent, maxWaiting),
		reduced:       newOpus47Gate(1, maxWaiting),
		dynamic:       make(map[int]*opus47Gate),
	}
}

func (g *adaptiveAdmissionGate) gateForLimit(limit int) *opus47Gate {
	if g == nil {
		return nil
	}
	if limit >= g.maxConcurrent {
		return g.base
	}
	if limit <= 1 {
		return g.reduced
	}
	if existing := g.dynamic[limit]; existing != nil {
		return existing
	}
	next := newOpus47Gate(limit, g.maxWaiting)
	g.dynamic[limit] = next
	return next
}

func normalizeAdmissionModel(model string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(model)), ".", "-")
}

func (g *modelAdmissionGateSet) acquire(model string, timeout time.Duration) (func(), bool, error) {
	if g == nil {
		return func() {}, false, nil
	}
	normalizedModel := normalizeAdmissionModel(model)
	g.mu.Lock()
	gate := g.models[normalizedModel]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		g.mu.Unlock()
		return func() {}, false, nil
	}
	effective := gateMaxConcurrent(gate)
	state := g.pressure[normalizedModel]
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	if state != nil && now.Before(state.expiresAt) && state.effectiveMaxConcurrent > 0 {
		effective = state.effectiveMaxConcurrent
	}
	inner := gate.gateForLimit(effective)
	g.mu.Unlock()
	if inner == nil {
		return func() {}, false, nil
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
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		return
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
	state := g.pressureStateForUpdateLocked(model, gate, now, true)
	state.score += score
	if state.score > 6 {
		state.score = 6
	}
	next := state.effectiveMaxConcurrent / 2
	if next < 1 {
		next = 1
	}
	state.effectiveMaxConcurrent = next
	if statusCode == http.StatusTooManyRequests {
		state.recentCapacityErrors++
	}
	if statusCode == http.StatusServiceUnavailable {
		state.recentQueueTimeouts++
	}
	state.lastPressureAt = now
	state.expiresAt = expiresAt
}

func (g *modelAdmissionGateSet) recordSuccess(model string, latency time.Duration) {
	if g == nil {
		return
	}
	model = normalizeAdmissionModel(model)
	if model == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		return
	}
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	state := g.pressureStateForUpdateLocked(model, gate, now, false)
	state.recentSuccesses++
	state.lastSuccessAt = now
	if latency < 5*time.Second && now.After(state.expiresAt) && state.effectiveMaxConcurrent < gate.maxConcurrent {
		state.effectiveMaxConcurrent++
	}
	if state.score > 0 && latency < 5*time.Second {
		state.score--
	}
}

func (g *modelAdmissionGateSet) effectiveMaxConcurrent(model string) int {
	effective, _ := g.admissionMetrics(model)
	return effective
}

func (g *modelAdmissionGateSet) pressureScore(model string) int {
	_, score := g.admissionMetrics(model)
	return score
}

func (g *modelAdmissionGateSet) admissionMetrics(model string) (effectiveMaxConcurrent int, pressureScore int) {
	if g == nil {
		return 0, 0
	}
	model = normalizeAdmissionModel(model)
	g.mu.RLock()
	defer g.mu.RUnlock()
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		return 0, 0
	}
	state := g.pressure[model]
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	if state == nil || state.effectiveMaxConcurrent <= 0 {
		return gate.maxConcurrent, 0
	}
	if now.After(state.expiresAt) && !state.lastSuccessAt.After(state.expiresAt) {
		return gate.maxConcurrent, state.score
	}
	return state.effectiveMaxConcurrent, state.score
}

func (g *modelAdmissionGateSet) pressureStateForUpdateLocked(model string, gate *adaptiveAdmissionGate, now time.Time, resetExpired bool) *admissionPressureState {
	if g.pressure == nil {
		g.pressure = make(map[string]*admissionPressureState)
	}
	state := g.pressure[model]
	if state == nil || (resetExpired && now.After(state.expiresAt)) {
		state = &admissionPressureState{effectiveMaxConcurrent: gate.maxConcurrent}
		g.pressure[model] = state
	}
	if state.effectiveMaxConcurrent <= 0 {
		state.effectiveMaxConcurrent = gate.maxConcurrent
	}
	return state
}

func gateMaxConcurrent(gate *adaptiveAdmissionGate) int {
	if gate == nil {
		return 0
	}
	return gate.maxConcurrent
}

func (g *modelAdmissionGateSet) snapshot() []AdmissionPressureSnapshot {
	if g == nil {
		return nil
	}
	g.mu.Lock()
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
		effective := maxConcurrent
		if state.effectiveMaxConcurrent > 0 && active {
			effective = state.effectiveMaxConcurrent
		}
		var activeRequests, queueDepth int
		if gate != nil {
			activeRequests, queueDepth = gate.gateForLimit(effective).snapshot()
		}
		expiresIn := int64(0)
		if active {
			expiresIn = state.expiresAt.Sub(now).Milliseconds()
		}
		out = append(out, AdmissionPressureSnapshot{
			Model:                  model,
			Score:                  state.score,
			Active:                 active,
			ReducedConcurrency:     active && state.score >= 2 && maxConcurrent > 1,
			ExpiresAt:              state.expiresAt,
			ExpiresInMs:            expiresIn,
			MaxConcurrent:          maxConcurrent,
			EffectiveMaxConcurrent: effective,
			QueueDepth:             queueDepth,
			ActiveRequests:         activeRequests,
			RecentCapacityErrors:   state.recentCapacityErrors,
			RecentQueueTimeouts:    state.recentQueueTimeouts,
			RecentSuccesses:        state.recentSuccesses,
		})
	}
	g.mu.Unlock()
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
	case <-timer.C:
		<-g.queue
		return nil, errOpus47GateTimeout
	}
	g.mu.Lock()
	g.active++
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.slots
			<-g.queue
			g.mu.Lock()
			if g.active > 0 {
				g.active--
			}
			g.mu.Unlock()
		})
	}, nil
}

func (g *opus47Gate) snapshot() (active int, queueDepth int) {
	if g == nil {
		return 0, 0
	}
	g.mu.RLock()
	active = g.active
	g.mu.RUnlock()
	return active, len(g.queue)
}
