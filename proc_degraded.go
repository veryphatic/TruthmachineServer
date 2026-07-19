package main

import (
	"math/rand"
	"time"
)

// degradedAmbientRefresh is how often the ambient-cue fallback value is
// re-rolled while a channel stays degraded. Snapshot() is polled every 100ms
// by the TUI tick, so re-estimating on every call would make the ambient cue
// flicker; holding it steady for a few seconds reads as a plausible resting
// signal instead.
const degradedAmbientRefresh = 3 * time.Second

// updateDegradedSince tracks how long a channel has been continuously non-OK,
// for the degraded-channel estimator's grace period. Call after quality is
// finalized in both Ingest() and Tick().
func updateDegradedSince(quality Quality, now time.Time, since *time.Time) {
	if quality != QualityOK {
		if since.IsZero() {
			*since = now
		}
	} else {
		*since = time.Time{}
	}
}

// estimateL returns a plausible fallback L for a channel that can't be scored
// for real, based on the average of its own recent real scores. With a
// probability that rises the further into the show it is (dev-log spec:
// 10-15% of the time, lower under ElevateCountLow interrogations, higher past
// ElevateCountHigh), it nudges the estimate upward instead of returning a flat
// average — elevated reports "hang in there" rather than going silent,
// without every fallback reading identically.
func estimateL(recentScores []float64, intCount int, cfg DegradedCfg) (L float64, elevated bool) {
	if len(recentScores) == 0 {
		L = 15 // neutral-low default before this channel has ever scored for real
	} else {
		sum := 0.0
		for _, v := range recentScores {
			sum += v
		}
		L = sum / float64(len(recentScores))
	}

	prob := cfg.ElevateProbLow
	switch {
	case intCount > cfg.ElevateCountHigh:
		prob = cfg.ElevateProbHigh
	case intCount >= cfg.ElevateCountLow:
		prob = (cfg.ElevateProbLow + cfg.ElevateProbHigh) / 2
	}

	if rand.Float64() < prob {
		elevated = true
		L += 15 + rand.Float64()*15
	}

	if L < 0 {
		L = 0
	}
	if L > 100 {
		L = 100
	}
	return L, elevated
}

// pushRecentScore appends a real score to the rolling history, trimmed to cap.
func pushRecentScore(scores []float64, L float64, cap int) []float64 {
	scores = append(scores, L)
	if cap > 0 && len(scores) > cap {
		scores = scores[len(scores)-cap:]
	}
	return scores
}
