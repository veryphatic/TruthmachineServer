package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── Tea message types ─────────────────────────────────────────────────────────

// TickMsg fires every 100ms to poll processor snapshots and update the display.
type TickMsg time.Time

// ScoredMsg carries the result of one channel's interrogation window.
type ScoredMsg struct {
	Channel     ChannelID
	Z, L        float64
	N           int
	IsEstimated bool
	T           time.Time
}

// StateChangeMsg fires when any processor transitions state.
type StateChangeMsg struct {
	Channel ChannelID
	New     ServerState
}

// CalibrationDoneMsg fires when a channel completes calibration.
type CalibrationDoneMsg struct {
	Channel   ChannelID
	Mu, Sigma float64
	N         int
}

// QualityChangeMsg fires when a channel's quality changes.
type QualityChangeMsg struct {
	Channel ChannelID
	Q       Quality
}

// LogLineMsg carries a new JSONL log line to the log pane.
type LogLineMsg struct{ Line string }

// QuitDisarmMsg fires after 2.5s to disarm the quit-confirm state.
type QuitDisarmMsg struct{}

// ScoreTimeoutMsg fires if a score aggregation round exceeds 12s.
type ScoreTimeoutMsg struct{ Round int }

// BaselineFreshenedMsg fires when FreshenBaseline is triggered (hotkey [b] or OSC /reset).
type BaselineFreshenedMsg struct{}

// InterrogateStartMsg fires whenever /interrogate is triggered from a source other
// than the keyboard hotkey (OSC, replay), so the score-aggregation round still starts.
type InterrogateStartMsg struct{}

// BaselineRefreshMsg fires on OSC /baseline — same as [b] hotkey, history untouched.
type BaselineRefreshMsg struct{}

// SensitivitySetMsg fires on OSC /sensitivity; K is already clamped to 0.1–5.0.
type SensitivitySetMsg struct{ K float64 }

// MuteToggleMsg fires on OSC /mute/{gsr,hr,rr}.
type MuteToggleMsg struct{ Channel ChannelID }

// RandomLowMsg fires on OSC /random_low — same as [r] hotkey.
type RandomLowMsg struct{}

// ManualLMsg fires on OSC /manual_l; L is the raw (not yet clamped) override value.
type ManualLMsg struct{ L float64 }

// ── Command factories ─────────────────────────────────────────────────────────

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// waitForEvent blocks until the EventSink delivers a ProcessorEvent,
// then returns it as the appropriate tea.Msg type.
func waitForEvent(events EventSink) tea.Cmd {
	return func() tea.Msg {
		e := <-events
		switch e.Kind {
		case ProcEventScored:
			return ScoredMsg{Channel: e.Channel, Z: e.Z, L: e.L, N: e.N, IsEstimated: e.IsEstimated, T: e.T}
		case ProcEventStateChange:
			return StateChangeMsg{Channel: e.Channel, New: e.State}
		case ProcEventCalibrated:
			return CalibrationDoneMsg{Channel: e.Channel, Mu: e.Mu, Sigma: e.Sigma, N: e.N}
		case ProcEventQualityChange:
			return QualityChangeMsg{Channel: e.Channel, Q: e.Quality}
		case ProcEventFreshen:
			return BaselineFreshenedMsg{}
		case ProcEventInterrogateStart:
			return InterrogateStartMsg{}
		case ProcEventBaselineRefresh:
			return BaselineRefreshMsg{}
		case ProcEventSensitivity:
			return SensitivitySetMsg{K: e.Value}
		case ProcEventMuteToggle:
			return MuteToggleMsg{Channel: e.Channel}
		case ProcEventRandomLow:
			return RandomLowMsg{}
		case ProcEventManualL:
			return ManualLMsg{L: e.Value}
		default:
			return TickMsg(e.T)
		}
	}
}

// waitForLogLine blocks until a new JSONL line arrives for the log pane.
func waitForLogLine(logCh <-chan string) tea.Cmd {
	return func() tea.Msg {
		line := <-logCh
		return LogLineMsg{Line: line}
	}
}

// quitDisarmCmd fires QuitDisarmMsg after 2.5s.
func quitDisarmCmd() tea.Cmd {
	return tea.Tick(2500*time.Millisecond, func(time.Time) tea.Msg {
		return QuitDisarmMsg{}
	})
}

// scoreTimeoutCmd fires ScoreTimeoutMsg after 12s to handle muted/disconnected channels.
func scoreTimeoutCmd(round int) tea.Cmd {
	return tea.Tick(12*time.Second, func(time.Time) tea.Msg {
		return ScoreTimeoutMsg{Round: round}
	})
}
