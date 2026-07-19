package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// AppLogger writes a unified JSONL file with one record per line.
// Every record carries "t" (RFC3339Nano), "channel" ("gsr"/"hr"/"rr"/"system"), and "type".
// The logCh channel delivers new lines to the TUI log pane without blocking the hot path.
type AppLogger struct {
	f     *os.File
	mu    sync.Mutex
	LogCh chan string
}

func newLogger() (*AppLogger, error) {
	name := "truthmachine-v2-" + time.Now().Format("2006-01-02") + ".log"
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	return &AppLogger{f: f, LogCh: make(chan string, 500)}, nil
}

func (l *AppLogger) Close() {
	if l.f != nil {
		_ = l.f.Close()
	}
}

func (l *AppLogger) FileName() string {
	if l.f == nil {
		return ""
	}
	return l.f.Name()
}

func (l *AppLogger) write(ch ChannelID, kv map[string]any) {
	kv["t"] = time.Now().UTC().Format(time.RFC3339Nano)
	kv["channel"] = ch.String()
	b, err := json.Marshal(kv)
	if err != nil {
		return
	}
	line := string(b)
	l.mu.Lock()
	_, _ = l.f.WriteString(line + "\n")
	l.mu.Unlock()
	// Non-blocking: if TUI is slow the oldest line is just dropped.
	select {
	case l.LogCh <- line:
	default:
	}
}

// Sample logs one raw sensor reading. display is the human-facing value:
// raw ADC for GSR, BPM for HR, computed RR BPM for RR (0 when unknown).
func (l *AppLogger) Sample(ch ChannelID, raw, display float64, quality Quality, mu, sigma float64, state ServerState) {
	l.write(ch, map[string]any{
		"type":    "sample",
		"raw":     raw,
		"display": display,
		"quality": quality.String(),
		"mu":      mu,
		"sigma":   sigma,
		"state":   state.String(),
	})
}

// Scored logs the result of one interrogation window.
func (l *AppLogger) Scored(ch ChannelID, z, L float64, n int) {
	l.write(ch, map[string]any{
		"type": "scored",
		"z":    z,
		"L":    L,
		"n":    n,
	})
}

// Event logs a named event (state change, calibration, quality change, etc.).
// kv is alternating key/value pairs: "mu", 2108.0, "sigma", 82.0, ...
func (l *AppLogger) Event(ch ChannelID, event string, kv ...any) {
	m := map[string]any{
		"type":  "event",
		"event": event,
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if key, ok := kv[i].(string); ok {
			m[key] = kv[i+1]
		}
	}
	l.write(ch, m)
}

// LogOSC pre-wires OSC event logging for step 7 (OSC bridge).
// dir is "in" (from QLab) or "out" (to QLab).
func (l *AppLogger) LogOSC(dir, address string, args []any) {
	l.write(channelSystem, map[string]any{
		"type":    "osc",
		"dir":     dir,
		"address": address,
		"args":    args,
	})
}
