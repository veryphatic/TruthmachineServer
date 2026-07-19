package main

import (
	"sync"
	"time"
)

// Set from Config.RR at startup in main.go — requires restart to take effect.
var (
	rrRingSize      = 1500
	rrBaselineAlpha = 0.01
	rrSigmaFloor    = 1.5
	rrClampMin      = 6.0
	rrClampMax      = 40.0
)

const (
	// Strap motion detection.
	rrStuckWindow = 125 // 5s at 25 Hz
	rrStuckVarMin = 100.0

	// Crossing detection (all units in boardMs = milliseconds).
	rrCrossBufSize     = 8
	rrMinCrossingGapMs = 1000  // 1s minimum → 60 BPM maximum
	rrMaxCrossingAgeMs = 30000 // 30s without a crossing → QualityStuck

	// DC removal window.
	rrDCWindow = 375 // 15s at 25 Hz removes posture drift
	rrDCWarmup = 50  // samples before attempting crossing detection
)

// RRProcessor ports respiratory-rate-server/processor.go into the unified v2 package.
// The ring stores raw ADC values; breath cycles are detected via upward zero-crossings
// of the DC-removed (AC-coupled) signal. currentRR is the computed BPM displayed to the operator.
// Sparkline tracks the last sparklineLen computed RR values (not raw ADC) via a separate ring.
type RRProcessor struct {
	lock sync.Mutex

	ring     ringBuf
	baseline BaselineTracker
	sc       scorer
	quality  Quality
	state    ServerState

	// Breath cycle detection.
	crossings      [rrCrossBufSize]uint64 // boardMs timestamps of last N upward crossings
	crossHead      int
	crossFill      int
	lastCrossingMs uint64
	prevAC         float64
	currentRR      float64 // most recent valid RR in BPM (0 = not yet computed)

	// Separate 30-slot ring for sparkline (stores computed RR BPM, not raw ADC).
	rrSparkRing ringBuf

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

	IsStub bool // set by stub.go when synthetic data is active

	degradedSince time.Time // zero when quality is OK; set when it first becomes non-OK
	recentScores  []float64 // rolling history of real (non-estimated) L scores, for the degraded fallback
	intCount      int       // interrogations started, for the elevate-probability ramp

	events EventSink
	log    *AppLogger
}

func newRRProcessor(log *AppLogger, events EventSink) *RRProcessor {
	return &RRProcessor{
		ring:        newRingBuf(rrRingSize),
		rrSparkRing: newRingBuf(sparklineLen),
		baseline:    newBaselineTracker(rrBaselineAlpha, rrSigmaFloor),
		sc:          newScorer(false), // normal direction: RR rises on arousal
		quality:     QualityDisconnected,
		sesMin:      4095,
		events:      events,
		log:         log,
	}
}

func (p *RRProcessor) pushEvent(e ProcessorEvent) {
	if p.events == nil {
		return
	}
	select {
	case p.events <- e:
	default:
	}
}

func (p *RRProcessor) setState(s ServerState) {
	if p.state == s {
		return
	}
	prev := p.state
	p.state = s
	p.log.Event(ChannelRR, "state_change", "from", prev.String(), "to", s.String())
	p.pushEvent(ProcessorEvent{Kind: ProcEventStateChange, Channel: ChannelRR, State: s, T: time.Now()})
}

// computeRR derives current RR from the last few crossing intervals (up to 6).
// Must be called with lock held.
func (p *RRProcessor) computeRR() (float64, bool) {
	if p.crossFill < 2 {
		return 0, false
	}
	use := p.crossFill
	if use > 6 {
		use = 6
	}
	intervalSum := 0.0
	intervalCount := 0
	for i := 0; i < use-1; i++ {
		t1 := p.crossings[(p.crossHead-1-i+rrCrossBufSize)%rrCrossBufSize]
		t0 := p.crossings[(p.crossHead-2-i+rrCrossBufSize)%rrCrossBufSize]
		if t1 > t0 {
			intervalSum += float64(t1 - t0)
			intervalCount++
		}
	}
	if intervalCount == 0 {
		return 0, false
	}
	meanIntervalMs := intervalSum / float64(intervalCount)
	rr := 60000.0 / meanIntervalMs
	return rr, rr >= 4 && rr <= 60
}

// Ingest is called ~25 Hz from the TCP listener or stub goroutine. raw is the raw ADC value.
func (p *RRProcessor) Ingest(raw float64, boardMs uint64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	now := time.Now()
	p.lastSample = now
	p.count++

	if raw < p.sesMin {
		p.sesMin = raw
	}
	if raw > p.sesMax {
		p.sesMax = raw
	}

	p.ring.push(raw)

	// Breath cycle detection via upward zero-crossings of the AC-coupled signal.
	if p.ring.fill >= rrDCWarmup {
		dcMean := p.ring.mean(rrDCWindow)
		ac := raw - dcMean
		prevAC := p.prevAC
		p.prevAC = ac

		if prevAC < 0 && ac >= 0 {
			gapOK := p.crossFill == 0 || (boardMs > p.lastCrossingMs &&
				boardMs-p.lastCrossingMs >= rrMinCrossingGapMs)
			if gapOK {
				p.crossings[p.crossHead] = boardMs
				p.crossHead = (p.crossHead + 1) % rrCrossBufSize
				if p.crossFill < rrCrossBufSize {
					p.crossFill++
				}
				p.lastCrossingMs = boardMs

				if rr, ok := p.computeRR(); ok {
					// computeRR() already hard-discards outside [4, 60] as a sanity
					// check; this is a tighter floor/ceil on the plausible-for-this-show
					// range applied to values that pass that check.
					rr = clampFloat64(rr, rrClampMin, rrClampMax)
					p.currentRR = rr
					p.rrSparkRing.push(rr)
					p.baseline.update(rr)
					if p.state == StateCalibrating {
						p.calBuf = append(p.calBuf, rr)
					}
					if p.state == StateInterrogating {
						p.intBuf = append(p.intBuf, rr)
					}
				}
			}
		}
	}

	prev := p.quality
	var next Quality
	if p.ring.fill > rrStuckWindow && p.ring.variance(rrStuckWindow) < rrStuckVarMin {
		next = QualityOutOfRange // strap not worn or too loose to move
	} else if p.crossFill == 0 || (boardMs > p.lastCrossingMs &&
		boardMs-p.lastCrossingMs > rrMaxCrossingAgeMs) {
		next = QualityStuck // strap moving but no clear breath cycles
	} else {
		next = QualityOK
	}
	if next != prev && now.Sub(p.lastQualityChange) >= time.Second {
		p.quality = next
		p.lastQualityChange = now
		p.log.Event(ChannelRR, "quality_change", "quality", p.quality.String())
		p.pushEvent(ProcessorEvent{Kind: ProcEventQualityChange, Channel: ChannelRR, Quality: next, T: now})
	}
	updateDegradedSince(p.quality, now, &p.degradedSince)

	p.log.Sample(ChannelRR, raw, p.currentRR, p.quality, p.baseline.mu, p.baseline.sigma(), p.state)
}

func (p *RRProcessor) Tick() {
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
			p.log.Event(ChannelRR, "quality_change", "quality", p.quality.String())
			p.pushEvent(ProcessorEvent{Kind: ProcEventQualityChange, Channel: ChannelRR, Quality: p.quality, T: now})
		}
		updateDegradedSince(p.quality, now, &p.degradedSince)
	}

	switch p.state {
	case StateCalibrating:
		if now.After(p.calEnd) {
			n := len(p.calBuf)
			if n < 2 {
				// Not enough breath cycles detected — fail gracefully and stay IDLE.
				p.calBuf = nil
				p.setState(StateIdle)
				p.log.Event(ChannelRR, "calibrate_failed", "breath_cycles", n)
			} else {
				p.baseline.calibrate(p.calBuf)
				p.calBuf = nil
				p.setState(StateIdle)
				p.log.Event(ChannelRR, "calibrated", "mu", p.baseline.mu, "sigma", p.baseline.sigma(), "n", n)
				p.pushEvent(ProcessorEvent{
					Kind: ProcEventCalibrated, Channel: ChannelRR,
					Mu: p.baseline.mu, Sigma: p.baseline.sigma(), N: n, T: now,
				})
			}
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

func (p *RRProcessor) doScore() {
	n := len(p.intBuf)

	degradedLong := !p.degradedSince.IsZero() && time.Since(p.degradedSince) >= degradedGrace()
	noData := n == 0 && p.currentRR <= 0 // no crossings this window and never established a reading

	var z, L float64
	var estimated bool
	if degradedLong || noData {
		L, estimated = estimateL(p.recentScores, p.intCount, getDegradedCfg())
	} else {
		var windowMean float64
		if n > 0 {
			sum := 0.0
			for _, v := range p.intBuf {
				sum += v
			}
			windowMean = sum / float64(n)
		} else {
			// No crossings during the window (slow breather) — fall back to most recent estimate.
			windowMean = p.currentRR
		}
		z, L = p.sc.score(windowMean, p.baseline.mu, p.baseline.sigma())
	}
	p.intBuf = nil

	if !estimated {
		p.recentScores = pushRecentScore(p.recentScores, L, getDegradedCfg().EstimateWindowN)
	}

	p.log.Scored(ChannelRR, z, L, n)
	p.pushEvent(ProcessorEvent{Kind: ProcEventScored, Channel: ChannelRR, Z: z, L: L, N: n, IsEstimated: estimated, T: time.Now()})

	p.baseline.frozen = true
	p.cooldownEnd = time.Now().Add(cooldownDur())
	p.setState(StateCooldown)
}

func (p *RRProcessor) Snapshot() ChannelSnapshot {
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

	z, L := p.sc.score(p.currentRR, p.baseline.mu, p.baseline.sigma())

	return ChannelSnapshot{
		Channel:      ChannelRR,
		DisplayValue: p.currentRR,
		Mu:           p.baseline.mu,
		Sigma:        p.baseline.sigma(),
		Z:            z,
		L:            L,
		Quality:      p.quality,
		State:        p.state,
		StateRemainS: rem,
		Calibrated:   p.baseline.calibrated,
		IsStub:       p.IsStub,
		Sparkline:    p.rrSparkRing.lastSparkline(),
		LastSample:   p.lastSample,
	}
}

func (p *RRProcessor) StartCalibrate() {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != StateIdle {
		return
	}
	p.calBuf = nil
	p.calEnd = time.Now().Add(calDuration())
	p.setState(StateCalibrating)
	p.log.Event(ChannelRR, "start_calibrate")
}

func (p *RRProcessor) StartInterrogate() {
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
	p.log.Event(ChannelRR, "start_interrogate", "mu", p.baseline.mu, "sigma", p.baseline.sigma())
}

func (p *RRProcessor) SetSensitivity(k float64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.sc.k = k
	p.log.Event(ChannelRR, "sensitivity", "k", k)
}

func (p *RRProcessor) FreshenBaseline() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.baseline.freshen(50) // ~2s warmup at 25 Hz; shows UNSET then auto-recovers
	p.cooldownEnd = time.Time{}
	if p.state == StateCooldown {
		p.setState(StateIdle)
	}
	// /reset is the operator's "start fresh" gesture — clear the degraded-fallback history too.
	p.recentScores = nil
	p.intCount = 0
	p.degradedSince = time.Time{}
	p.log.Event(ChannelRR, "freshen_baseline", "mu", p.baseline.mu, "sigma", p.baseline.sigma())
}

func (p *RRProcessor) RestoreCalibrated(mu, sigma float64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.baseline.mu = mu
	p.baseline.vari = sigma * sigma
	p.baseline.calibrated = true
	p.baseline.frozen = false
}

func (p *RRProcessor) ForceState(s ServerState) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.state = s
}

func (p *RRProcessor) OnConnect() {}

func (p *RRProcessor) OnDisconnect() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.quality = QualityDisconnected
}

// SetIsStub is called by RRStub to mark whether synthetic data is currently active.
// Stub and real boardMs timestamps live on different clocks (Unix-epoch-scale vs.
// ESP32 uptime), so any transition must flush the crossing-detection ring —
// otherwise computeRR() blends stale entries from one domain with fresh ones
// from the other and currentRR drags toward the old source for several breaths.
func (p *RRProcessor) SetIsStub(v bool) {
	p.lock.Lock()
	if p.IsStub != v {
		p.crossings = [rrCrossBufSize]uint64{}
		p.crossHead = 0
		p.crossFill = 0
		p.lastCrossingMs = 0
		p.prevAC = 0
	}
	p.IsStub = v
	p.lock.Unlock()
}
