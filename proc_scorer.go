package main

import "math"

// scorer converts a window mean into a 0–100 arousal likelihood (L).
// Formula: L = 100 * tanh(k * z / 2), z clamped to [0, 4].
//
// GSR is inverted (conductance drops on arousal in this wiring, so raw drops →
// z = (μ − window) / σ). HR and RR are normal (rise on arousal → z = (window − μ) / σ).
type scorer struct {
	k        float64 // sensitivity multiplier (default 1.0, range 0.1–5.0)
	inverted bool    // true for GSR
}

func newScorer(inverted bool) scorer {
	return scorer{k: 1.0, inverted: inverted}
}

func (s *scorer) score(windowMean, mu, sigma float64) (z, L float64) {
	if s.inverted {
		z = (mu - windowMean) / sigma
	} else {
		z = (windowMean - mu) / sigma
	}
	if z < 0 {
		z = 0
	}
	if z > 4 {
		z = 4
	}
	L = 100.0 * math.Tanh(s.k*z/2)
	return
}
