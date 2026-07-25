package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// ── Config struct ──────────────────────────────────────────────────────────────

type Config struct {
	ListenAddr    string      `json:"listen_addr"`
	OSCTarget     string      `json:"osc_target"`
	OscListenAddr string      `json:"osc_listen_addr"`
	Timing        TimingCfg   `json:"timing"`
	Scoring       ScoringCfg  `json:"scoring"`
	Degraded      DegradedCfg `json:"degraded"`
	GSR           GSRCfg      `json:"gsr"`
	HR            HRCfg       `json:"hr"`
	RR            RRCfg       `json:"rr"`
	Stub          StubCfg     `json:"stub"`
	Mute          MuteCfg     `json:"mute"`
}

// DegradedCfg controls the degraded-channel fallback estimator: what a channel
// reports (ambient cue and interrogation score) once it's been non-OK for too
// long to trust its real reading.
// These are HOT-RELOADABLE — edit truthmachine.json while the server is running.
type DegradedCfg struct {
	GraceS           float64 `json:"grace_s"`            // quality must be non-OK this long before the fallback kicks in
	EstimateWindowN  int     `json:"estimate_window_n"`  // how many of the channel's own recent real scores to average
	ElevateProbLow   float64 `json:"elevate_prob_low"`   // chance of an elevated fallback, interrogation count below ElevateCountLow
	ElevateProbHigh  float64 `json:"elevate_prob_high"`  // chance of an elevated fallback, interrogation count above ElevateCountHigh
	ElevateCountLow  int     `json:"elevate_count_low"`  // interrogation count below which ElevateProbLow applies
	ElevateCountHigh int     `json:"elevate_count_high"` // interrogation count above which ElevateProbHigh applies
}

// MuteCfg sets the startup mute state for each channel and is updated in-place
// whenever the operator toggles a channel mute in the TUI.
type MuteCfg struct {
	GSR bool `json:"gsr"`
	HR  bool `json:"hr"`
	RR  bool `json:"rr"`
}

// TimingCfg controls state machine durations. All values are in seconds.
// These are HOT-RELOADABLE — edit truthmachine.json while the server is running.
type TimingCfg struct {
	CalibrateS   float64 `json:"calibrate_s"`
	InterrogateS float64 `json:"interrogate_s"`
	CooldownS    float64 `json:"cooldown_s"`
	StaleS       float64 `json:"stale_after_s"`
	DisconnectS  float64 `json:"disconnect_after_s"`
}

// ScoringCfg controls the combined-L calculation.
// These are HOT-RELOADABLE — edit truthmachine.json while the server is running.
type ScoringCfg struct {
	DefaultSensitivity float64 `json:"default_sensitivity"`
	WeightGSR          float64 `json:"weight_gsr"`
	WeightHR           float64 `json:"weight_hr"`
	WeightRR           float64 `json:"weight_rr"`
}

// GSRCfg holds per-channel baseline and hardware params.
// Requires restart to take effect.
type GSRCfg struct {
	BaselineAlpha        float64 `json:"baseline_alpha"`
	SigmaFloor           float64 `json:"sigma_floor"`
	MinContactADC        float64 `json:"min_contact_adc"`
	MaxContactADC        float64 `json:"max_contact_adc"`
	MaxContactRecoverADC float64 `json:"max_contact_recover_adc"` // hysteresis: must drop below this to clear OUT_OF_RANGE once MaxContactADC is exceeded
	DisplaySmoothN       int     `json:"display_smooth_n"`        // samples averaged for the periodic/drone score (Snapshot), raw display value is unaffected
	ClampMinADC          float64 `json:"clamp_min_adc"`           // floor/ceil applied to in-contact samples before they reach ring/baseline/scorer; narrower than the min/max contact range, does not affect quality classification
	ClampMaxADC          float64 `json:"clamp_max_adc"`
}

// HRCfg holds HR sensor params.
// Requires restart to take effect.
type HRCfg struct {
	BaselineAlpha float64 `json:"baseline_alpha"`
	SigmaFloor    float64 `json:"sigma_floor"`
	MinValidBPM   float64 `json:"min_valid_bpm"`
	MaxValidBPM   float64 `json:"max_valid_bpm"`
	WarmupS       float64 `json:"warmup_s"`      // time after connect during which readings are suppressed as possibly-stale holdover values
	ClampMinBPM   float64 `json:"clamp_min_bpm"` // floor/ceil applied to valid samples before they reach ring/baseline/scorer; narrower than min/max valid, does not affect quality classification
	ClampMaxBPM   float64 `json:"clamp_max_bpm"`
}

// RRCfg holds respiratory rate baseline params.
// Requires restart to take effect.
type RRCfg struct {
	BaselineAlpha float64 `json:"baseline_alpha"`
	SigmaFloor    float64 `json:"sigma_floor"`
	ClampMinBPM   float64 `json:"clamp_min_bpm"` // floor/ceil applied to a computed breath rate before it reaches ring/baseline/scorer; tighter than the 4-60 hard sanity discard in computeRR()
	ClampMaxBPM   float64 `json:"clamp_max_bpm"`
}

// StubCfg controls the synthetic RR sine-wave generator.
// Requires restart to take effect.
type StubCfg struct {
	BreatheBPM float64 `json:"breathe_bpm"`
}

func defaultConfig() Config {
	return Config{
		ListenAddr:    ":5000",
		OSCTarget:     "192.168.1.100:53000",
		OscListenAddr: ":8765",
		Timing: TimingCfg{
			CalibrateS:   15,
			InterrogateS: 8,
			CooldownS:    15,
			StaleS:       2,
			DisconnectS:  10,
		},
		Scoring: ScoringCfg{
			DefaultSensitivity: 1.0,
			WeightGSR:          0.5,
			WeightHR:           0.3,
			WeightRR:           0.2,
		},
		Degraded: DegradedCfg{
			GraceS:           5.0,
			EstimateWindowN:  5,
			ElevateProbLow:   0.10,
			ElevateProbHigh:  0.15,
			ElevateCountLow:  3,
			ElevateCountHigh: 6,
		},
		GSR: GSRCfg{
			BaselineAlpha:        0.005,
			SigmaFloor:           10.0,
			MinContactADC:        50.0,
			MaxContactADC:        4060.0,
			MaxContactRecoverADC: 4050.0,
			DisplaySmoothN:       8,
			ClampMinADC:          100.0,
			ClampMaxADC:          4000.0,
		},
		HR: HRCfg{
			BaselineAlpha: 0.005,
			SigmaFloor:    3.0,
			MinValidBPM:   30.0,
			MaxValidBPM:   250.0,
			WarmupS:       2.0,
			ClampMinBPM:   40.0,
			ClampMaxBPM:   140.0,
		},
		RR: RRCfg{
			BaselineAlpha: 0.01,
			SigmaFloor:    1.5,
			ClampMinBPM:   6.0,
			ClampMaxBPM:   40.0,
		},
		Stub: StubCfg{
			BreatheBPM: 14.0,
		},
		Mute: MuteCfg{GSR: false, HR: false, RR: false},
	}
}

// defaultConfigPath returns "truthmachine.json" next to the running executable
// rather than relative to the process's working directory. Double-clicking the
// binary (the operator's normal launch path, no terminal) does not reliably set
// cwd to the executable's own folder, so resolving via os.Executable() ensures
// the config sitting beside the binary is the one that's actually read.
func defaultConfigPath() string {
	return filepath.Join(exeDir(), "truthmachine.json")
}

// exeDir returns the directory containing the running executable, falling
// back to "." (the process's working directory) if it can't be determined.
// Used so files the app writes (config, logs) land beside the binary instead
// of wherever the process happened to be launched from.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// loadConfig reads path into a Config, starting from defaults so any missing
// keys retain their default values. Returns defaults + error if the file is
// missing or malformed.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

// writeDefaultConfig writes the default config to path as pretty-printed JSON.
// No-op if the file already exists.
func writeDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	b, err := json.MarshalIndent(defaultConfig(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// saveMuteConfig reads the current config file, updates only the mute fields,
// and writes it back. Called whenever the operator toggles a channel mute so
// the next startup respects the last mute state.
func saveMuteConfig(path string, muted [3]bool) error {
	cfg, err := loadConfig(path)
	if err != nil && !os.IsNotExist(err) {
		cfg = defaultConfig()
	}
	cfg.Mute.GSR = muted[ChannelGSR]
	cfg.Mute.HR = muted[ChannelHR]
	cfg.Mute.RR = muted[ChannelRR]
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// ── Hot-reload: timing atomics ─────────────────────────────────────────────────
//
// Processors call calDuration(), intDuration(), etc. at use-time rather than
// storing the value — so a config reload takes effect on the next [c]/[i] keypress.

var (
	_calNs        atomic.Int64
	_intNs        atomic.Int64
	_cooldownNs   atomic.Int64
	_staleNs      atomic.Int64
	_disconnectNs atomic.Int64
)

func calDuration() time.Duration     { return time.Duration(_calNs.Load()) }
func intDuration() time.Duration     { return time.Duration(_intNs.Load()) }
func cooldownDur() time.Duration     { return time.Duration(_cooldownNs.Load()) }
func staleAfter() time.Duration      { return time.Duration(_staleNs.Load()) }
func disconnectAfter() time.Duration { return time.Duration(_disconnectNs.Load()) }

func applyTimings(t TimingCfg) {
	store := func(a *atomic.Int64, secs float64) {
		a.Store(int64(time.Duration(secs * float64(time.Second))))
	}
	store(&_calNs, t.CalibrateS)
	store(&_intNs, t.InterrogateS)
	store(&_cooldownNs, t.CooldownS)
	store(&_staleNs, t.StaleS)
	store(&_disconnectNs, t.DisconnectS)
}

// ── Hot-reload: scoring config ────────────────────────────────────────────────

var _scoringCfg atomic.Pointer[ScoringCfg]

func getScoringCfg() ScoringCfg {
	if p := _scoringCfg.Load(); p != nil {
		return *p
	}
	return defaultConfig().Scoring
}

func applyScoringCfg(s ScoringCfg) {
	copy := s
	_scoringCfg.Store(&copy)
}

// ── Hot-reload: degraded-channel config ──────────────────────────────────────

var _degradedCfg atomic.Pointer[DegradedCfg]

func getDegradedCfg() DegradedCfg {
	if p := _degradedCfg.Load(); p != nil {
		return *p
	}
	return defaultConfig().Degraded
}

func applyDegradedCfg(d DegradedCfg) {
	copy := d
	_degradedCfg.Store(&copy)
}

func degradedGrace() time.Duration {
	return time.Duration(getDegradedCfg().GraceS * float64(time.Second))
}

// ── OSC target (startup-only) ─────────────────────────────────────────────────

var oscTarget = "127.0.0.1:53000"

// ── Config watcher ────────────────────────────────────────────────────────────

// watchConfig polls path every 2s for mtime changes. On change it re-parses
// the file and calls onReload. Structural params (listen_addr, per-channel
// thresholds, ring sizes) require a restart; timing and scoring are hot-applied.
// Returns a stop function.
func watchConfig(path string, log *AppLogger, onReload func(Config)) func() {
	stopCh := make(chan struct{})
	go func() {
		var lastMod time.Time
		if fi, err := os.Stat(path); err == nil {
			lastMod = fi.ModTime()
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				fi, err := os.Stat(path)
				if err != nil {
					continue
				}
				if !fi.ModTime().After(lastMod) {
					continue
				}
				lastMod = fi.ModTime()
				cfg, err := loadConfig(path)
				if err != nil {
					log.Event(channelSystem, "config_error", "err", err.Error())
					continue
				}
				log.Event(channelSystem, "config_reload",
					"calibrate_s", cfg.Timing.CalibrateS,
					"interrogate_s", cfg.Timing.InterrogateS,
					"cooldown_s", cfg.Timing.CooldownS)
				onReload(cfg)
			}
		}
	}()
	return func() { close(stopCh) }
}
