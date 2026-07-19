package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// ReplayConfig specifies which JSONL logs to replay.
// Any empty path is skipped.
type ReplayConfig struct {
	GSRLog  string // path to gsr-server/truthmachine-YYYY-MM-DD.log
	HRLog   string // path to heart-rate-server/heartrate-YYYY-MM-DD.log
	QLabLog string // path to a v2 truthmachine-v2-YYYY-MM-DD.log with type:"osc" records
}

// StartReplay launches goroutines for each configured log path.
// All three run concurrently with real-time pacing (sleep between records).
// Replay goroutines call proc.Ingest() directly, bypassing the TCP listener.
func StartReplay(cfg ReplayConfig, gsr *GSRProcessor, hr *HRProcessor,
	rr *RRProcessor, events EventSink, log *AppLogger) {
	if cfg.GSRLog != "" {
		go func() {
			if err := replayGSR(cfg.GSRLog, gsr, log); err != nil {
				log.Event(channelSystem, "replay_error", "source", "gsr", "err", err.Error())
			}
		}()
	}
	if cfg.HRLog != "" {
		go func() {
			if err := replayHR(cfg.HRLog, hr, log); err != nil {
				log.Event(channelSystem, "replay_error", "source", "hr", "err", err.Error())
			}
		}()
	}
	if cfg.QLabLog != "" {
		go func() {
			if err := replayQLab(cfg.QLabLog, gsr, hr, rr, events, log); err != nil {
				log.Event(channelSystem, "replay_error", "source", "qlab", "err", err.Error())
			}
		}()
	}
}

// ── GSR replay ────────────────────────────────────────────────────────────────
// Reads the old gsr-server JSONL format: {"type":"sample","raw":2041,"contact":true,...}
// Replays at original speed using the "t" field timestamps.

func replayGSR(path string, proc *GSRProcessor, log *AppLogger) error {
	records, err := loadRecords(path)
	if err != nil {
		return err
	}
	log.Event(channelSystem, "replay_start", "source", "gsr", "path", path, "records", len(records))

	replayStart := time.Now()
	firstT := records[0].T

	for _, rec := range records {
		// Old gsr-server logs have no "channel" field (keep them); v2 unified
		// logs interleave gsr/hr/rr/system in one file (skip everything but gsr).
		if rec.Channel != "" && rec.Channel != "gsr" {
			continue
		}

		offset := rec.T.Sub(firstT)
		waitUntil := replayStart.Add(offset)
		if d := time.Until(waitUntil); d > 0 {
			time.Sleep(d)
		}

		switch rec.Type {
		case "sample":
			raw, _ := jsonFloat(rec.Raw, "raw")
			boardMs := uint64(rec.T.Sub(firstT).Milliseconds())
			proc.Ingest(raw, boardMs)

		case "event":
			switch rec.Event {
			case "calibrated":
				mu, _ := jsonFloat(rec.Extra, "mu")
				sigma, _ := jsonFloat(rec.Extra, "sigma")
				if mu > 0 && sigma > 0 {
					proc.RestoreCalibrated(mu, sigma)
				}
			}
			// state_change, quality_change: let the processor derive these naturally.
		}
	}

	log.Event(channelSystem, "replay_done", "source", "gsr")
	return nil
}

// ── HR replay ─────────────────────────────────────────────────────────────────
// Reads the heart-rate-server JSONL format: {"type":"sample","bpm":72,"valid":true,...}

func replayHR(path string, proc *HRProcessor, log *AppLogger) error {
	records, err := loadRecords(path)
	if err != nil {
		return err
	}
	log.Event(channelSystem, "replay_start", "source", "hr", "path", path, "records", len(records))

	proc.OnConnect() // prime warmup guard as if the sensor just connected

	replayStart := time.Now()
	firstT := records[0].T

	for _, rec := range records {
		if rec.Channel != "" && rec.Channel != "hr" {
			continue
		}

		offset := rec.T.Sub(firstT)
		waitUntil := replayStart.Add(offset)
		if d := time.Until(waitUntil); d > 0 {
			time.Sleep(d)
		}

		switch rec.Type {
		case "sample":
			// v2 unified logs use "raw" for every channel's sample (see logger.go's
			// Sample()); old heart-rate-server logs used "bpm" — accept either.
			bpm, ok := jsonFloat(rec.Raw, "raw")
			if !ok {
				bpm, _ = jsonFloat(rec.Raw, "bpm")
			}
			boardMs := uint64(rec.T.Sub(firstT).Milliseconds())
			proc.Ingest(bpm, boardMs)

		case "event":
			switch rec.Event {
			case "calibrated":
				mu, _ := jsonFloat(rec.Extra, "mu")
				sigma, _ := jsonFloat(rec.Extra, "sigma")
				if mu > 0 && sigma > 0 {
					proc.RestoreCalibrated(mu, sigma)
				}
			}
		}
	}

	log.Event(channelSystem, "replay_done", "source", "hr")
	return nil
}

// ── QLab replay ───────────────────────────────────────────────────────────────
// Reads a v2 JSONL log containing type:"osc" records (produced by the OSC bridge in step 7).
// dir:"in" OSC messages trigger processor commands; dir:"out" are displayed only.

func replayQLab(path string, gsr *GSRProcessor, hr *HRProcessor, rr *RRProcessor,
	events EventSink, log *AppLogger) error {
	records, err := loadRecords(path)
	if err != nil {
		return err
	}

	// Filter to OSC records only.
	var oscRecs []rawRecord
	for _, r := range records {
		if r.Type == "osc" {
			oscRecs = append(oscRecs, r)
		}
	}
	if len(oscRecs) == 0 {
		return fmt.Errorf("no osc records found in %s (has the OSC bridge run yet?)", path)
	}

	log.Event(channelSystem, "replay_start", "source", "qlab", "path", path, "osc_records", len(oscRecs))

	replayStart := time.Now()
	firstT := oscRecs[0].T

	for _, rec := range oscRecs {
		offset := rec.T.Sub(firstT)
		waitUntil := replayStart.Add(offset)
		if d := time.Until(waitUntil); d > 0 {
			time.Sleep(d)
		}

		// Only act on inbound QLab commands; outbound are logged only.
		dir, _ := rec.Extra["dir"].(string)
		addr, _ := rec.Extra["address"].(string)
		if dir != "in" {
			log.LogOSC("replay-out", addr, nil)
			continue
		}

		switch addr {
		case "/calibrate":
			gsr.StartCalibrate()
			hr.StartCalibrate()
			rr.StartCalibrate()
		case "/interrogate":
			// Same ordering requirement as the live OSC bridge — see net_osc.go.
			select {
			case events <- ProcessorEvent{Kind: ProcEventInterrogateStart, T: time.Now()}:
			default:
			}
			gsr.StartInterrogate()
			hr.StartInterrogate()
			rr.StartInterrogate()
		case "/reset":
			gsr.FreshenBaseline()
			hr.FreshenBaseline()
			rr.FreshenBaseline()
		}
		log.LogOSC("replay-in", addr, nil)
	}

	log.Event(channelSystem, "replay_done", "source", "qlab")
	return nil
}

// ── Record loading ────────────────────────────────────────────────────────────

type rawRecord struct {
	Type    string         `json:"type"`
	T       time.Time      `json:"t"`
	Event   string         `json:"event"`
	Channel string         `json:"channel"` // empty for old single-channel-server logs; "gsr"/"hr"/"rr"/"system" for v2 unified logs
	Raw     map[string]any // the full JSON for field extraction
	Extra   map[string]any // alias for same map
}

func loadRecords(path string) ([]rawRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var records []rawRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB per line
	for sc.Scan() {
		line := sc.Text()
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue // skip malformed lines
		}

		rec := rawRecord{Raw: m, Extra: m}

		if t, ok := m["t"].(string); ok {
			rec.T, _ = time.Parse(time.RFC3339Nano, t)
		}
		if typ, ok := m["type"].(string); ok {
			rec.Type = typ
		}
		if ev, ok := m["event"].(string); ok {
			rec.Event = ev
		}
		if ch, ok := m["channel"].(string); ok {
			rec.Channel = ch
		}

		if rec.T.IsZero() {
			continue // skip records with no timestamp
		}
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no valid records in %s", path)
	}
	return records, nil
}

// jsonFloat extracts a float64 from a JSON map, handling both float64 and json.Number.
func jsonFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
