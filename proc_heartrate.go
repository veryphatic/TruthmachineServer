package main

import (
	"math"
	"sync"
	"time"
)

// Set from Config.HR at startup in main.go — requires restart to take effect.
var (
	hrRingSize      = 240
	hrBaselineAlpha = 0.005
	hrSigmaFloor    = 3.0
	hrMinValidBPM   = 30.0
	hrMaxValidBPM   = 250.0
	hrWarmupDur     = 2 * time.Second
	hrClampMin      = 40.0
	hrClampMax      = 140.0
)

const (
	hrStuckIdentical = 40 // consecutive identical integer BPM → STUCK (~20s at 2 Hz)
)

// HRProcessor ports heart-rate-server/processor.go into the unified v2 package.
// Key difference from GSR: z-score is normal (BPM rises on arousal), and a
// warmupRequired guard suppresses stale holdover values when the finger is first placed.
type HRProcessor struct {
	lock sync.Mutex

	ring     ringBuf
	baseline BaselineTracker
	sc       scorer
	quality  Quality
	state    ServerState

	calBuf []float64
	calEnd time.Time

	intBuf      []float64
	intEnd      time.Time
	cooldownEnd time.Time

	lastSample        time.Time
	lastQualityChange time.Time
	count             uint64
	sesMin            float64
	sesMax            float64
	lastDisplay       float64 // last valid BPM for Snapshot

	identicalRun int
	lastBpmInt   int
	warmupUntil  time.Time // readings before this time are suppressed as possible stale holdover values

	degradedSince     time.Time // zero when quality is OK; set when it first becomes non-OK
	recentScores      []float64 // rolling history of real (non-estimated) L scores, for the degraded fallback
	intCount          int       // interrogations started, for the elevate-probability ramp
	ambientEstimateL  float64   // cached fallback L for the periodic ambient cue / TUI readout
	ambientEstimateAt time.Time // when ambientEstimateL was last (re)computed

	events EventSink
	log    *AppLogger
}

func newHRProcessor(log *AppLogger, events EventSink) *HRProcessor {
	return &HRProcessor{
		ring:     newRingBuf(hrRingSize),
		baseline: newBaselineTracker(hrBaselineAlpha, hrSigmaFloor),
		sc:       newScorer(false), // normal direction
		quality:  QualityDisconnected,
		sesMin:   hrMaxValidBPM,
		events:   events,
		log:      log,
	}
}

func (p *HRProcessor) pushEvent(e ProcessorEvent) {
	if p.events == nil {
		return
	}
	select {
	case p.events <- e:
	default:
	}
}

func (p *HRProcessor) setState(s ServerState) {
	if p.state == s {
		return
	}
	prev := p.state
	p.state = s
	p.log.Event(ChannelHR, "state_change", "from", prev.String(), "to", s.String())
	p.pushEvent(ProcessorEvent{Kind: ProcEventStateChange, Channel: ChannelHR, State: s, T: time.Now()})
}

// Ingest is called from the TCP listener goroutine. raw is the BPM value from the sensor.
// raw==0 signals the warmup reset; raw outside [30, 250] is treated as invalid.
func (p *HRProcessor) Ingest(raw float64, boardMs uint64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	now := time.Now()
	p.lastSample = now
	p.count++

	bpm := raw
	valid := bpm >= hrMinValidBPM && bpm <= hrMaxValidBPM

	// Warmup guard: the Grove HR sensor retains its last BPM across power cycles,
	// so readings immediately after connect could be stale holdover from the
	// previous session. Suppress everything for a short fixed window instead of
	// waiting for an explicit bpm==0 reset sample, which this sensor never sends.
	if now.Before(p.warmupUntil) {
		valid = false
	}

	// Floor/ceil a physiologically-implausible-but-valid BPM before it reaches
	// the ring/baseline/scorer. Narrower than [hrMinValidBPM, hrMaxValidBPM]
	// above, so it never changes the OUT_OF_RANGE verdict.
	value := clampFloat64(bpm, hrClampMin, hrClampMax)

	if valid {
		p.lastDisplay = value
		if value < p.sesMin {
			p.sesMin = value
		}
		if value > p.sesMax {
			p.sesMax = value
		}
	}

	p.ring.push(value)

	// Track consecutive identical integer BPM readings, computed from the
	// unclamped bpm so a sustained real reading pinned at the clamp ceiling
	// doesn't get misread as a latched sensor.
	if valid {
		bpmInt := int(math.Round(bpm))
		if bpmInt == p.lastBpmInt {
			p.identicalRun++
		} else {
			p.identicalRun = 0
			p.lastBpmInt = bpmInt
		}
	} else {
		p.identicalRun = 0
	}

	prev := p.quality
	var next Quality
	if !valid {
		next = QualityOutOfRange
	} else if p.identicalRun >= hrStuckIdentical {
		next = QualityStuck
	} else {
		next = QualityOK
	}
	if next != prev && now.Sub(p.lastQualityChange) >= time.Second {
		p.quality = next
		p.lastQualityChange = now
		p.log.Event(ChannelHR, "quality_change", "quality", p.quality.String())
		p.pushEvent(ProcessorEvent{Kind: ProcEventQualityChange, Channel: ChannelHR, Quality: next, T: now})
	}
	updateDegradedSince(p.quality, now, &p.degradedSince)

	if valid {
		p.baseline.update(value)
	}

	if p.state == StateCalibrating && valid {
		p.calBuf = append(p.calBuf, value)
	}
	if p.state == StateInterrogating && valid {
		p.intBuf = append(p.intBuf, value)
	}

	display := value
	if !valid {
		display = 0
	}
	p.log.Sample(ChannelHR, raw, display, p.quality, p.baseline.mu, p.baseline.sigma(), p.state)
}

func (p *HRProcessor) Tick() {
	p.lock.Lock()
	defer p.lock.Unlock()

	now := time.Now()

	if !p.lastSample.IsZero() {
		elapsed := now.Sub(p.lastSample)
		prev := p.quality
		switch {
		case elapsed > disconnectAfter() && p.quality != QualityDisconnected && p.quality != QualityOutOfRange:
			p.quality = QualityDisconnected
		case elapsed > staleAfter() && p.quality == QualityOK:
			p.quality = QualityStale
		case elapsed <= staleAfter() && (p.quality == QualityStale || p.quality == QualityDisconnected):
			p.quality = QualityOK
		}
		if p.quality != prev {
			p.log.Event(ChannelHR, "quality_change", "quality", p.quality.String())
			p.pushEvent(ProcessorEvent{Kind: ProcEventQualityChange, Channel: ChannelHR, Quality: p.quality, T: now})
		}
		updateDegradedSince(p.quality, now, &p.degradedSince)
	}

	switch p.state {
	case StateCalibrating:
		if now.After(p.calEnd) {
			n := len(p.calBuf)
			p.baseline.calibrate(p.calBuf)
			p.calBuf = nil
			p.setState(StateIdle)
			p.log.Event(ChannelHR, "calibrated", "mu", p.baseline.mu, "sigma", p.baseline.sigma(), "n", n)
			p.pushEvent(ProcessorEvent{
				Kind: ProcEventCalibrated, Channel: ChannelHR,
				Mu: p.baseline.mu, Sigma: p.baseline.sigma(), N: n, T: now,
			})
		}

	case StateInterrogating:
		if now.After(p.intEnd) {
			p.doScore()
		}

	case StateCooldown:
		if now.After(p.cooldownEnd) {
			p.baseline.frozen = false
			p.setState(StateIdle)
		}
	}
}

func (p *HRProcessor) doScore() {
	n := len(p.intBuf)

	degraded := n == 0 || (!p.degradedSince.IsZero() && time.Since(p.degradedSince) >= degradedGrace())

	var z, L float64
	var estimated bool
	if degraded {
		L, estimated = estimateL(p.recentScores, p.intCount, getDegradedCfg())
	} else {
		sum := 0.0
		for _, v := range p.intBuf {
			sum += v
		}
		windowMean := sum / float64(n)
		z, L = p.sc.score(windowMean, p.baseline.mu, p.baseline.sigma())
	}
	p.intBuf = nil

	if !estimated {
		p.recentScores = pushRecentScore(p.recentScores, L, getDegradedCfg().EstimateWindowN)
	}

	p.log.Scored(ChannelHR, z, L, n)
	p.pushEvent(ProcessorEvent{Kind: ProcEventScored, Channel: ChannelHR, Z: z, L: L, N: n, IsEstimated: estimated, T: time.Now()})

	p.baseline.frozen = true
	p.cooldownEnd = time.Now().Add(cooldownDur())
	p.setState(StateCooldown)
}

func (p *HRProcessor) Snapshot() ChannelSnapshot {
	p.lock.Lock()
	defer p.lock.Unlock()

	var rem int
	switch p.state {
	case StateCalibrating:
		rem = max0(int(time.Until(p.calEnd).Seconds()) + 1)
	case StateInterrogating:
		rem = max0(int(time.Until(p.intEnd).Seconds()) + 1)
	case StateCooldown:
		rem = max0(int(time.Until(p.cooldownEnd).Seconds()) + 1)
	}

	z, L := p.sc.score(p.lastDisplay, p.baseline.mu, p.baseline.sigma())

	display := p.lastDisplay
	estimated := false
	if !p.degradedSince.IsZero() && time.Since(p.degradedSince) >= degradedGrace() && p.baseline.calibrated {
		// Ambient BPM fallback: the performer's own calibrated resting rate is a
		// more plausible "still connected" reading than any L-derived number.
		display = p.baseline.mu
		if p.ambientEstimateAt.IsZero() || time.Since(p.ambientEstimateAt) >= degradedAmbientRefresh {
			p.ambientEstimateL, _ = estimateL(p.recentScores, p.intCount, getDegradedCfg())
			p.ambientEstimateAt = time.Now()
		}
		z, L = 0, p.ambientEstimateL
		estimated = true
	}

	return ChannelSnapshot{
		Channel:      ChannelHR,
		DisplayValue: display,
		Mu:           p.baseline.mu,
		Sigma:        p.baseline.sigma(),
		Z:            z,
		L:            L,
		Quality:      p.quality,
		State:        p.state,
		StateRemainS: rem,
		Calibrated:   p.baseline.calibrated,
		IsEstimated:  estimated,
		Sparkline:    p.ring.lastSparkline(),
		LastSample:   p.lastSample,
	}
}

func (p *HRProcessor) StartCalibrate() {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != StateIdle {
		return
	}
	p.calBuf = nil
	p.calEnd = time.Now().Add(calDuration())
	p.setState(StateCalibrating)
	p.log.Event(ChannelHR, "start_calibrate")
}

// StartInterrogate begins a new interrogation window. If the channel is in
// COOLDOWN, the new request debounces (kills) the warmdown and starts
// immediately instead of waiting it out — the new window still emits its
// own L value on completion via the normal doScore() path.
func (p *HRProcessor) StartInterrogate() {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != StateIdle && p.state != StateCooldown {
		return
	}
	cooldownSkipped := p.state == StateCooldown
	p.cooldownEnd = time.Time{}
	p.intBuf = nil
	p.intEnd = time.Now().Add(intDuration())
	p.baseline.frozen = true
	p.intCount++
	p.setState(StateInterrogating)
	p.log.Event(ChannelHR, "start_interrogate", "mu", p.baseline.mu, "sigma", p.baseline.sigma(), "cooldown_skipped", cooldownSkipped)
}

func (p *HRProcessor) SetSensitivity(k float64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.sc.k = k
	p.log.Event(ChannelHR, "sensitivity", "k", k)
}

func (p *HRProcessor) FreshenBaseline() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.baseline.freshen(15) // ~3s warmup at 5 Hz; shows UNSET then auto-recovers
	p.cooldownEnd = time.Time{}
	if p.state == StateCooldown {
		p.setState(StateIdle)
	}
	// /reset is the operator's "start fresh" gesture — clear the degraded-fallback history too.
	p.recentScores = nil
	p.intCount = 0
	p.degradedSince = time.Time{}
	p.ambientEstimateAt = time.Time{}
	p.log.Event(ChannelHR, "freshen_baseline", "mu", p.baseline.mu, "sigma", p.baseline.sigma())
}

func (p *HRProcessor) RestoreCalibrated(mu, sigma float64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.baseline.mu = mu
	p.baseline.vari = sigma * sigma
	p.baseline.calibrated = true
	p.baseline.frozen = false
}

func (p *HRProcessor) ForceState(s ServerState) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.state = s
}

// OnConnect arms the warmup guard to suppress stale holdover BPM readings.
func (p *HRProcessor) OnConnect() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.warmupUntil = time.Now().Add(hrWarmupDur)
	p.identicalRun = 0
}

func (p *HRProcessor) OnDisconnect() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.quality = QualityDisconnected
}

// IsConnected reports whether the HR channel currently has a live signal.
// Used by the RR stub to decide whether to fake data — see proc_stub.go.
func (p *HRProcessor) IsConnected() bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.quality != QualityDisconnected
}
