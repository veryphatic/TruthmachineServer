package main

import (
	"math"
	"math/rand"
	"sync/atomic"
	"time"
)

// RRStub generates a synthetic ~14 BPM sine-wave respiratory signal at 25 Hz.
// It feeds directly into the RRProcessor and pauses automatically whenever real
// rr,... lines arrive on the TCP connection (5s silence threshold), and also
// whenever neither GSR nor HR has a live signal — with the whole rig unplugged,
// RR should honestly show DISCONNECTED rather than fake a reading on its own.
//
// Formula: raw = 2048 + 1200·sin(2π·n/107) + gauss(σ=15)
// 25 Hz × (60/14) ≈ 107 samples per breath cycle.
const (
	stubHz          = 25
	stubAmplitude   = 1200.0
	stubDCOffset    = 2048.0
	stubNoiseSigma  = 15.0
	stubRealDataGrace = 5 * time.Second
)

// stubPeriodSamples is set from Config.Stub.BreatheBPM at startup in main.go.
var stubPeriodSamples = 107 // ≈ 14 BPM at 25 Hz

// stubPeriodFromBPM converts a breathing rate in BPM to samples at stubHz.
func stubPeriodFromBPM(bpm float64) int {
	return int(math.Round(float64(stubHz) * 60.0 / bpm))
}

// RRStub holds the stub generator state. Create with newRRStub, then call Run() in a goroutine.
type RRStub struct {
	proc *RRProcessor
	gsr  *GSRProcessor
	hr   *HRProcessor

	// lastRealData tracks the last time a real rr,... line was received.
	// Using *time.Time via unsafe so we can store nil (never received) vs zero time.
	lastRealData atomic.Pointer[time.Time]

	sampleN uint64
}

func newRRStub(proc *RRProcessor, gsr *GSRProcessor, hr *HRProcessor) *RRStub {
	return &RRStub{proc: proc, gsr: gsr, hr: hr}
}

// NotifyRealData is called by the TCP listener whenever a real rr,... line arrives.
// This pauses the stub for stubRealDataGrace seconds.
func (s *RRStub) NotifyRealData() {
	now := time.Now()
	s.lastRealData.Store(&now)
	s.proc.SetIsStub(false)
}

func (s *RRStub) isActive() bool {
	t := s.lastRealData.Load()
	recentReal := t != nil && time.Since(*t) <= stubRealDataGrace
	if recentReal {
		return false
	}
	// Rig fully unplugged — don't fake RR data; let it go DISCONNECTED honestly.
	return s.gsr.IsConnected() || s.hr.IsConnected()
}

// Run ticks at 25 Hz until ctx is cancelled via the stop channel.
func (s *RRStub) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second / stubHz)
	defer ticker.Stop()

	startMs := uint64(time.Now().UnixMilli())

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !s.isActive() {
				// Clears the STUB badge so a fully-unplugged rig shows an honest
				// DISCONNECTED once lastSample goes stale, rather than leaving a
				// stub badge over from the last time it was active.
				s.proc.SetIsStub(false)
				continue
			}
			s.proc.SetIsStub(true)

			n := s.sampleN
			s.sampleN++

			angle := 2 * math.Pi * float64(n) / float64(stubPeriodSamples)
			noise := rand.NormFloat64() * stubNoiseSigma
			raw := stubDCOffset + stubAmplitude*math.Sin(angle) + noise

			boardMs := startMs + n*uint64(1000/stubHz)
			s.proc.Ingest(raw, boardMs)
		}
	}
}

