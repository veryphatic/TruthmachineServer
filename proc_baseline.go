package main

import "math"

// BaselineTracker implements the sliding EWMA baseline (Option 3).
// Unlike the per-server versions that use package-level constants, this carries
// alpha and sigmaFloor as instance fields so each channel can differ:
//   GSR:  alpha=0.005, sigmaFloor=10.0
//   HR:   alpha=0.005, sigmaFloor=3.0
//   RR:   alpha=0.01,  sigmaFloor=1.5
type BaselineTracker struct {
	mu         float64
	vari       float64
	n          int
	frozen     bool
	calibrated bool
	freshenN   int       // samples remaining before auto-recalibration after a freshen
	freshenBuf []float64 // samples collected during the current freshen warmup window

	alpha      float64
	sigmaFloor float64
}

func newBaselineTracker(alpha, sigmaFloor float64) BaselineTracker {
	return BaselineTracker{alpha: alpha, sigmaFloor: sigmaFloor}
}

func (b *BaselineTracker) sigma() float64 {
	s := math.Sqrt(b.vari)
	if s < b.sigmaFloor {
		return b.sigmaFloor
	}
	return s
}

// freshen marks the baseline as uncalibrated and schedules auto-recalibration
// after warmupN good samples. Called by FreshenBaseline on each processor.
func (b *BaselineTracker) freshen(warmupN int) {
	b.calibrated = false
	b.frozen = false
	b.freshenN = warmupN
	b.freshenBuf = nil
}

// update slides the EWMA with a 3σ gate to reject arousal spikes.
// During the post-freshen warmup window, it buffers samples and batch-seeds
// a fresh μ/σ from them (via calibrate) once the warmup completes, instead
// of silently resuming EWMA drift from the pre-freshen baseline.
func (b *BaselineTracker) update(raw float64) {
	if b.frozen {
		return
	}
	if !b.calibrated {
		if b.freshenN > 0 {
			b.freshenBuf = append(b.freshenBuf, raw)
			b.freshenN--
			if b.freshenN == 0 {
				b.calibrate(b.freshenBuf)
				b.freshenBuf = nil
			}
		}
		return
	}
	if math.Abs(raw-b.mu) > 3*b.sigma() {
		return
	}
	diff := raw - b.mu
	b.mu += b.alpha * diff
	b.vari = (1 - b.alpha) * (b.vari + b.alpha*diff*diff)
	b.n++
}

// calibrate hard-sets μ/σ from a collected sample window.
// Uses batch mean/variance (not EWMA) so the anchor reflects a clean known-good window.
func (b *BaselineTracker) calibrate(samples []float64) {
	if len(samples) == 0 {
		return
	}
	sum := 0.0
	for _, s := range samples {
		sum += s
	}
	mean := sum / float64(len(samples))
	vsum := 0.0
	for _, s := range samples {
		d := s - mean
		vsum += d * d
	}
	b.mu = mean
	b.vari = vsum / float64(len(samples))
	floor := b.sigmaFloor * b.sigmaFloor
	if b.vari < floor {
		b.vari = floor
	}
	b.n = len(samples)
	b.frozen = false
	b.calibrated = true
}
