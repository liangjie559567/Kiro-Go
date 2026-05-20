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
	mu            sync.Mutex
	active        int
	waiting       int
	maxConcurrent int
	maxWaiting    int
	notify        chan struct{}
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
	gate          *opus47Gate
}

type admissionPressureState struct {
	score                  int
	expiresAt              time.Time
	retryAt                time.Time
	circuitState           string
	lastPressureReason     string
	effectiveMaxConcurrent int
	recentCapacityErrors   int
	recentQueueTimeouts    int
	recentSuccesses        int
	halfOpenSuccesses      int
	lastPressureAt         time.Time
	lastSuccessAt          time.Time
}

type AdmissionPressureSnapshot struct {
	Model                  string    `json:"model"`
	Score                  int       `json:"score"`
	Active                 bool      `json:"active"`
	ReducedConcurrency     bool      `json:"reducedConcurrency"`
	CircuitState           string    `json:"circuitState,omitempty"`
	RetryAfterSeconds      int       `json:"retryAfterSeconds,omitempty"`
	LastPressureReason     string    `json:"lastPressureReason,omitempty"`
	LastPressureAt         time.Time `json:"lastPressureAt,omitempty"`
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
		gate:          newOpus47Gate(maxConcurrent, maxWaiting),
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
	if gate == nil {
		g.mu.RUnlock()
		return func() {}, false, nil
	}
	inner := gate.gate
	g.mu.RUnlock()
	if inner == nil {
		return func() {}, false, nil
	}
	release, err := inner.acquireWithLimit(timeout, func() int {
		return g.effectiveMaxConcurrent(normalizedModel)
	})
	if err == errOpus47GateTimeout {
		g.recordQueueTimeout(normalizedModel)
	}
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
	if retryAt.After(now) {
		expiresAt = retryAt
	} else if retryAt.After(expiresAt) {
		expiresAt = retryAt
	}
	state := g.pressureStateForUpdateLocked(model, gate, now, false)
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
	state.lastPressureReason = pressureReasonForStatus(statusCode)
	state.expiresAt = expiresAt
	if retryAt.After(state.retryAt) {
		state.retryAt = retryAt
	}
	if state.retryAt.After(state.expiresAt) {
		state.expiresAt = state.retryAt
	}
	if state.score >= 4 {
		if state.retryAt.IsZero() || !state.retryAt.After(now) || state.retryAt.Before(expiresAt) {
			state.retryAt = expiresAt
		}
		state.effectiveMaxConcurrent = 1
	}
	state.circuitState = circuitStateForPressure(state, now)
	state.halfOpenSuccesses = 0
}

func (g *modelAdmissionGateSet) recordQueueTimeout(model string) {
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
	ttl := g.pressureT
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	state := g.pressureStateForUpdateLocked(model, gate, now, false)
	state.score += 2
	if state.score > 6 {
		state.score = 6
	}
	state.recentQueueTimeouts++
	state.lastPressureAt = now
	state.lastPressureReason = "queue_timeout"
	if !state.expiresAt.After(now) {
		state.expiresAt = now.Add(ttl)
	}
	if state.score >= 4 {
		if state.retryAt.IsZero() || state.retryAt.Before(state.expiresAt) {
			state.retryAt = state.expiresAt
		}
	}
	state.circuitState = circuitStateForPressure(state, now)
	state.halfOpenSuccesses = 0
}

func (g *modelAdmissionGateSet) recordSuccess(model string, latency time.Duration) {
	if g == nil {
		return
	}
	model = normalizeAdmissionModel(model)
	if model == "" {
		return
	}
	broadcastContinuity := false
	g.mu.Lock()
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	if gate == nil {
		g.mu.Unlock()
		return
	}
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	state := g.pressure[model]
	if state == nil {
		g.mu.Unlock()
		return
	}
	state.recentSuccesses++
	state.lastSuccessAt = now
	if circuitStateForPressure(state, now) == "half_open" {
		if latency < 5*time.Second {
			state.halfOpenSuccesses++
			if state.halfOpenSuccesses >= 2 {
				state.score = 0
				state.recentCapacityErrors = 0
				state.recentQueueTimeouts = 0
				state.halfOpenSuccesses = 0
				state.retryAt = time.Time{}
				state.expiresAt = time.Time{}
				state.effectiveMaxConcurrent = gate.maxConcurrent
				state.circuitState = circuitStateForPressure(state, now)
				if gate.gate != nil {
					gate.gate.broadcast()
				}
				broadcastContinuity = true
				g.mu.Unlock()
				if broadcastContinuity {
					contentContinuityGateGlobal.broadcast(model)
				}
				return
			}
		} else {
			state.halfOpenSuccesses = 0
		}
		if latency < 5*time.Second && state.effectiveMaxConcurrent < gate.maxConcurrent {
			state.effectiveMaxConcurrent++
			if gate.gate != nil {
				gate.gate.broadcast()
			}
			broadcastContinuity = true
		}
		state.circuitState = circuitStateForPressure(state, now)
		g.mu.Unlock()
		if broadcastContinuity {
			contentContinuityGateGlobal.broadcast(model)
		}
		return
	}
	effectiveBefore := state.effectiveMaxConcurrent
	if latency < 5*time.Second && now.After(state.expiresAt) && state.effectiveMaxConcurrent < gate.maxConcurrent {
		state.effectiveMaxConcurrent++
	}
	if state.score > 0 && latency < 5*time.Second {
		state.score--
	}
	if state.effectiveMaxConcurrent > effectiveBefore && gate.gate != nil {
		gate.gate.broadcast()
		broadcastContinuity = true
	}
	g.mu.Unlock()
	if broadcastContinuity {
		contentContinuityGateGlobal.broadcast(model)
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
	if state == nil || state.effectiveMaxConcurrent <= 0 {
		return gate.maxConcurrent, 0
	}
	return state.effectiveMaxConcurrent, state.score
}

func (g *modelAdmissionGateSet) modelSnapshot(model string) AdmissionPressureSnapshot {
	model = normalizeAdmissionModel(model)
	snaps := g.snapshot()
	for _, snap := range snaps {
		if snap.Model == model {
			return snap
		}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	gate := g.models[model]
	if gate == nil {
		gate = g.def
	}
	maxConcurrent := 0
	if gate != nil {
		maxConcurrent = gate.maxConcurrent
	}
	return AdmissionPressureSnapshot{
		Model:                  model,
		CircuitState:           "closed",
		MaxConcurrent:          maxConcurrent,
		EffectiveMaxConcurrent: maxConcurrent,
	}
}

func circuitStateForPressure(state *admissionPressureState, now time.Time) string {
	if state == nil || state.score <= 0 {
		return "closed"
	}
	if state.retryAt.After(now) {
		return "open"
	}
	if state.score >= 4 {
		return "half_open"
	}
	if now.Before(state.expiresAt) {
		return "degraded"
	}
	return "closed"
}

func pressureReasonForStatus(statusCode int) string {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return "rate_limited_or_model_capacity"
	case statusCode == http.StatusServiceUnavailable:
		return "service_unavailable"
	case statusCode >= 500:
		return "upstream_5xx"
	default:
		return "latency"
	}
}

func (g *modelAdmissionGateSet) pressureStateForUpdateLocked(model string, gate *adaptiveAdmissionGate, now time.Time, resetExpired bool) *admissionPressureState {
	if g.pressure == nil {
		g.pressure = make(map[string]*admissionPressureState)
	}
	state := g.pressure[model]
	if state == nil || (resetExpired && now.After(state.expiresAt)) {
		state = &admissionPressureState{
			circuitState:           "closed",
			effectiveMaxConcurrent: gate.maxConcurrent,
		}
		g.pressure[model] = state
	}
	if state.effectiveMaxConcurrent <= 0 {
		state.effectiveMaxConcurrent = gate.maxConcurrent
	}
	return state
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
		effective := maxConcurrent
		if state.effectiveMaxConcurrent > 0 {
			effective = state.effectiveMaxConcurrent
		}
		var activeRequests, queueDepth int
		if gate != nil {
			activeRequests, queueDepth = gate.gate.snapshot()
		}
		expiresIn := int64(0)
		if active {
			expiresIn = state.expiresAt.Sub(now).Milliseconds()
		}
		circuitState := circuitStateForPressure(state, now)
		if circuitState == "" {
			circuitState = "closed"
		}
		retryAfterSeconds := 0
		if state.retryAt.After(now) {
			retryAfterSeconds = int((state.retryAt.Sub(now) + time.Second - 1) / time.Second)
		}
		out = append(out, AdmissionPressureSnapshot{
			Model:                  model,
			Score:                  state.score,
			Active:                 active,
			ReducedConcurrency:     active && state.score >= 2 && maxConcurrent > 1,
			CircuitState:           circuitState,
			RetryAfterSeconds:      retryAfterSeconds,
			LastPressureReason:     state.lastPressureReason,
			LastPressureAt:         state.lastPressureAt,
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
		maxConcurrent: maxConcurrent,
		maxWaiting:    maxWaiting,
		notify:        make(chan struct{}),
	}
}

func (g *opus47Gate) acquire(timeout time.Duration, limits ...func() int) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	if len(limits) > 0 && limits[0] != nil {
		return g.acquireWithLimit(timeout, limits[0])
	}
	return g.acquireWithLimit(timeout, func() int {
		return g.maxConcurrent
	})
}

func (g *opus47Gate) acquireWithLimit(timeout time.Duration, limit func() int) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	queued := false
	for {
		currentLimit := 1
		if limit != nil {
			currentLimit = limit()
		}
		if currentLimit <= 0 {
			currentLimit = 1
		}

		g.mu.Lock()
		if g.active < currentLimit {
			if queued && g.waiting > 0 {
				g.waiting--
			}
			g.active++
			g.mu.Unlock()
			break
		}
		if !queued {
			if g.waiting >= g.maxWaiting {
				g.mu.Unlock()
				return nil, errOpus47GateTimeout
			}
			g.waiting++
			queued = true
		}
		notify := g.notify
		g.mu.Unlock()
		select {
		case <-notify:
		case <-timer.C:
			g.mu.Lock()
			if queued && g.waiting > 0 {
				g.waiting--
			}
			g.mu.Unlock()
			return nil, errOpus47GateTimeout
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			if g.active > 0 {
				g.active--
			}
			g.broadcastLocked()
			g.mu.Unlock()
		})
	}, nil
}

func (g *opus47Gate) broadcastLocked() {
	close(g.notify)
	g.notify = make(chan struct{})
}

func (g *opus47Gate) broadcast() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.broadcastLocked()
	g.mu.Unlock()
}

func (g *opus47Gate) snapshot() (active int, queueDepth int) {
	if g == nil {
		return 0, 0
	}
	g.mu.Lock()
	active = g.active
	queueDepth = g.waiting
	g.mu.Unlock()
	return active, queueDepth
}
