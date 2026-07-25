package main

import "time"

type ChannelID int

const (
	ChannelGSR ChannelID = iota
	ChannelHR
	ChannelRR
	channelSystem // logger-internal; used for OSC and system events
)

func (c ChannelID) String() string {
	return [...]string{"gsr", "hr", "rr", "system"}[c]
}

type Quality int

const (
	QualityOK Quality = iota
	QualityStale
	QualityDisconnected
	QualityStuck
	QualityOutOfRange
)

func (q Quality) String() string {
	return [...]string{"OK", "STALE", "DISCONNECTED", "STUCK", "OUT_OF_RANGE"}[q]
}

type ServerState int

const (
	StateIdle ServerState = iota
	StateCalibrating
	StateInterrogating
	StateCooldown
)

func (s ServerState) String() string {
	return [...]string{"IDLE", "CALIBRATING", "INTERROGATING", "COOLDOWN"}[s]
}

// ChannelSnapshot is a value-copy of per-channel state captured every 100ms by the TUI tick.
// It is taken under the processor mutex and released before rendering.
type ChannelSnapshot struct {
	Channel      ChannelID
	DisplayValue float64 // ADC count (GSR), BPM (HR), computed RR BPM (RR)
	Mu, Sigma    float64
	Z, L         float64
	Quality      Quality
	State        ServerState
	StateRemainS int // seconds remaining in timed state
	Calibrated   bool
	IsStub       bool // true when RR is running synthetic data
	IsEstimated  bool // true when Z/L is a degraded-channel fallback estimate, not a real reading
	Muted        bool
	Sparkline    [sparklineLen]float64 // last sparklineLen display values, oldest first, zero-padded
	LastSample   time.Time
}

// ScoreRecord is one row in the interrogation history table.
type ScoreRecord struct {
	N        int
	Time     time.Time
	Label    string
	GSR_L    float64 // -1 if disconnected or muted
	HR_L     float64
	RR_L     float64
	Combined float64
	GSR_Est  bool // true if the corresponding _L is a degraded-channel fallback estimate, not a real reading
	HR_Est   bool
	RR_Est   bool
	IsCalib  bool
	IsForced bool
	IsFresh  bool
}

// Processor interface — GSRProcessor, HRProcessor, RRProcessor all implement this.
type Processor interface {
	Ingest(raw float64, boardMs uint64)
	Tick()
	Snapshot() ChannelSnapshot
	StartCalibrate()
	StartInterrogate()
	SetSensitivity(k float64)
	FreshenBaseline()
	RestoreCalibrated(mu, sigma float64) // replay: bypass calibration window
	ForceState(s ServerState)            // replay: bypass state guards
	OnConnect()
	OnDisconnect()
}

// ProcessorEvent carries async notifications from processor goroutines to the TUI.
// Using a concrete type (not tea.Msg) avoids importing bubbletea in core types.
type ProcEventKind int

const (
	ProcEventScored ProcEventKind = iota
	ProcEventStateChange
	ProcEventCalibrated
	ProcEventQualityChange
	ProcEventFreshen
	ProcEventInterrogateStart // pushed whenever /interrogate fires from any source (OSC, replay), so the TUI's score-aggregation round starts even without the keyboard hotkey
	ProcEventBaselineRefresh  // OSC /baseline — same as [b] hotkey: freshen only, history untouched
	ProcEventSensitivity      // OSC /sensitivity — Value carries the (already-clamped) k applied to all processors
	ProcEventMuteToggle       // OSC /mute/{gsr,hr,rr} — Channel identifies which to toggle
	ProcEventRandomLow        // OSC /random_low — same as [r] hotkey
	ProcEventManualL          // OSC /manual_l — Value carries the L override
	ProcEventShowHistory      // OSC /history — same as [h] hotkey
)

type ProcessorEvent struct {
	Kind        ProcEventKind
	Channel     ChannelID
	State       ServerState // ProcEventStateChange
	Quality     Quality     // ProcEventQualityChange
	Z, L        float64     // ProcEventScored
	N           int         // ProcEventScored
	IsEstimated bool        // ProcEventScored — true if L is a degraded-channel fallback estimate
	Mu, Sigma   float64     // ProcEventCalibrated
	Value       float64     // ProcEventSensitivity (k), ProcEventManualL (L)
	T           time.Time
}

// EventSink is the buffered channel processors push events to.
type EventSink chan ProcessorEvent
