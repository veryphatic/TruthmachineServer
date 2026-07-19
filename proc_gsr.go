package main

import (
	"sync"
	"time"
)

// Set from Config.GSR at startup in main.go — requires restart to take effect.
var (
	gsrRingSize          = 600
	gsrBaselineAlpha     = 0.005
	gsrSigmaFloor        = 10.0
	gsrMinContact        = 50.0
	gsrMaxContact        = 4060.0
	gsrMaxContactRecover = 4050.0
	gsrDisplaySmoothN    = 8 // ~0.8s at ~10Hz — damps single/multi-sample noise feeding the live drone cue
	gsrClampMin          = 100.0
	gsrClampMax          = 4000.0
)

const (
	gsrStuckWindow = 30  // ~3s at 10 Hz
	gsrStuckVarMin = 1.0 // population variance below this → STUCK
)

// GSRProcessor ports gsr-server/processor.go into the unified v2 package.
// Key difference: z-score is INVERTED (conductance drops on arousal in this wiring,
// so raw ADC drops → z = (μ − window) / σ).
type GSRProcessor struct {
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
	lastDisplay       float64 // last contact-range raw value for Snapshot
	aboveMax          bool    // hysteresis verdict for the gsrMaxContact/gsrMaxContactRecover band

	degradedSince     time.Time // zero when quality is OK; set when it first becomes non-OK
	recentScores      []float64 // rolling history of real (non-estimated) L scores, for the degraded fallback
	intCount          int       // interrogations started, for the elevate-probability ramp
	ambientEstimateL  float64   // cached fallback L for the periodic ambient cue
	ambientEstimateAt time.Time // when ambientEstimateL was last (re)computed

	events EventSink
	log    *AppLogger
}

func newGSRProcessor(log *AppLogger, events EventSink) *GSRProcessor {
	return &GSRProcessor{
		ring:     newRingBuf(gsrRingSize),
		baseline: newBaselineTracker(gsrBaselineAlpha, gsrSigmaFloor),
		sc:       newScorer(true), // inverted
		quality:  QualityDisconnected,
		sesMin:   4095,
		events:   events,
		log:      log,
	}
}

func (p *GSRProcessor) pushEvent(e ProcessorEvent) {
	if p.events == nil {
		return
	}
	select {
	case p.events <- e:
	default:
	}
}

func (p *GSRProcessor) setState(s ServerState) {
	if p.state == s {
		return
	}
	prev := p.state
	p.state = s
	p.log.Event(ChannelGSR, "state_change", "from", prev.String(), "to", s.String())
	p.pushEvent(ProcessorEvent{Kind: ProcEventStateChange, Channel: ChannelGSR, State: s, T: time.Now()})
}

// Ingest is the hot path — called ~10 Hz from the TCP listener goroutine.
func (p *GSRProcessor) Ingest(raw float64, boardMs uint64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	now := time.Now()
	p.lastSample = now
	p.count++

	// Quality/contact classification always runs on the true unclamped raw value.
	p.aboveMax = hysteresisAbove(raw, gsrMaxContact, gsrMaxContactRecover, p.aboveMax)
	contact := raw >= gsrMinContact && !p.aboveMax

	// Floor/ceil a physiologically-implausible-but-in-contact spike before it
	// reaches the ring/baseline/scorer. Narrower than the contact range above,
	// so it never changes the OUT_OF_RANGE/STUCK verdict.
	value := clampFloat64(raw, gsrClampMin, gsrClampMax)

	if contact {
		p.lastDisplay = value
		if value < p.sesMin {
			p.sesMin = value
		}
		if value > p.sesMax {
			p.sesMax = value
		}
	}

	p.ring.push(value)

	prev := p.quality
	var next Quality
	if !contact {
		next = QualityOutOfRange
	} else if p.count > gsrStuckWindow && p.ring.variance(gsrStuckWindow) < gsrStuckVarMin {
		next = QualityStuck
	} else {
		next = QualityOK
	}
	if next != prev && now.Sub(p.lastQualityChange) >= time.Second {
		p.quality = next
		p.lastQualityChange = now
		p.log.Event(ChannelGSR, "quality_change", "quality", p.quality.String())
		p.pushEvent(ProcessorEvent{Kind: ProcEventQualityChange, Channel: ChannelGSR, Quality: next, T: now})
	}
	updateDegradedSince(p.quality, now, &p.degradedSince)

	if contact {
		p.baseline.update(value)
	}

	if p.state == StateCalibrating && contact {
		p.calBuf = append(p.calBuf, value)
	}
	if p.state == StateInterrogating && contact {
		p.intBuf = append(p.intBuf, value)
	}

	display := value
	if !contact {
		display = 0
	}
	p.log.Sample(ChannelGSR, raw, display, p.quality, p.baseline.mu, p.baseline.sigma(), p.state)
}

// Tick is called every second for stale/disconnect detection and state timeouts.
func (p *GSRProcessor) Tick() {
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
			p.log.Event(ChannelGSR, "quality_change", "quality", p.quality.String())
			p.pushEvent(ProcessorEvent{Kind: ProcEventQualityChange, Channel: ChannelGSR, Quality: p.quality, T: now})
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
			p.log.Event(ChannelGSR, "calibrated", "mu", p.baseline.mu, "sigma", p.baseline.sigma(), "n", n)
			p.pushEvent(ProcessorEvent{
				Kind: ProcEventCalibrated, Channel: ChannelGSR,
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

func (p *GSRProcessor) doScore() {
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

	p.log.Scored(ChannelGSR, z, L, n)
	p.pushEvent(ProcessorEvent{Kind: ProcEventScored, Channel: ChannelGSR, Z: z, L: L, N: n, IsEstimated: estimated, T: time.Now()})

	p.baseline.frozen = true
	p.cooldownEnd = time.Now().Add(cooldownDur())
	p.setState(StateCooldown)
}

// Snapshot returns a lock-free copy of current state for the TUI.
func (p *GSRProcessor) Snapshot() ChannelSnapshot {
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

	z, L := p.sc.score(p.ring.mean(gsrDisplaySmoothN), p.baseline.mu, p.baseline.sigma())

	estimated := false
	if !p.degradedSince.IsZero() && time.Since(p.degradedSince) >= degradedGrace() {
		if p.ambientEstimateAt.IsZero() || time.Since(p.ambientEstimateAt) >= degradedAmbientRefresh {
			p.ambientEstimateL, _ = estimateL(p.recentScores, p.intCount, getDegradedCfg())
			p.ambientEstimateAt = time.Now()
		}
		z, L = 0, p.ambientEstimateL
		estimated = true
	}

	return ChannelSnapshot{
		Channel:      ChannelGSR,
		DisplayValue: p.lastDisplay,
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

func (p *GSRProcessor) StartCalibrate() {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != StateIdle {
		return
	}
	p.calBuf = nil
	p.calEnd = time.Now().Add(calDuration())
	p.setState(StateCalibrating)
	p.log.Event(ChannelGSR, "start_calibrate")
}

func (p *GSRProcessor) StartInterrogate() {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != StateIdle {
		return
	}
	p.intBuf = nil
	p.intEnd = time.Now().Add(intDuration())
	p.baseline.frozen = true
	p.intCount++
	p.setState(StateInterrogating)
	p.log.Event(ChannelGSR, "start_interrogate", "mu", p.baseline.mu, "sigma", p.baseline.sigma())
}

func (p *GSRProcessor) SetSensitivity(k float64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.sc.k = k
	p.log.Event(ChannelGSR, "sensitivity", "k", k)
}

func (p *GSRProcessor) FreshenBaseline() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.baseline.freshen(40) // ~2s warmup at 20 Hz; shows UNSET then auto-recovers
	p.cooldownEnd = time.Time{}
	if p.state == StateCooldown {
		p.setState(StateIdle)
	}
	// /reset is the operator's "start fresh" gesture — clear the degraded-fallback history too.
	p.recentScores = nil
	p.intCount = 0
	p.degradedSince = time.Time{}
	p.ambientEstimateAt = time.Time{}
	p.log.Event(ChannelGSR, "freshen_baseline", "mu", p.baseline.mu, "sigma", p.baseline.sigma())
}

func (p *GSRProcessor) RestoreCalibrated(mu, sigma float64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.baseline.mu = mu
	p.baseline.vari = sigma * sigma
	p.baseline.calibrated = true
	p.baseline.frozen = false
}

func (p *GSRProcessor) ForceState(s ServerState) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.state = s
}

func (p *GSRProcessor) OnConnect() {}

func (p *GSRProcessor) OnDisconnect() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.quality = QualityDisconnected
}

// IsConnected reports whether the GSR channel currently has a live signal.
// Used by the RR stub to decide whether to fake data — see proc_stub.go.
func (p *GSRProcessor) IsConnected() bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.quality != QualityDisconnected
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
