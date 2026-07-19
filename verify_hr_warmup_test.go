package main

import (
	"os"
	"testing"
)

func TestReplayHRWarmup(t *testing.T) {
	f, err := os.CreateTemp("", "verify_hr_warmup_*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	log := &AppLogger{f: f, LogCh: make(chan string, 500)}
	defer log.Close()

	proc := newHRProcessor(log, nil)
	if err := replayHR("truthmachine-v2-2026-07-16-heart-rate.log", proc, log); err != nil {
		t.Fatal(err)
	}

	snap := proc.Snapshot()
	t.Logf("final quality=%s display=%.1f", snap.Quality.String(), snap.DisplayValue)
	if snap.Quality != QualityOK {
		t.Fatalf("expected final quality OK, got %s", snap.Quality.String())
	}
	if snap.DisplayValue < 30 || snap.DisplayValue > 250 {
		t.Fatalf("expected plausible final BPM, got %.1f", snap.DisplayValue)
	}
}
