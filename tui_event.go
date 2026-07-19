package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const eventProgressWidth = 24

// renderEvent renders the CURRENT EVENT panel showing server state,
// a timed progress bar when calibrating or interrogating, and a context sub-note.
func renderEvent(m model) string {
	// Determine consensus state (all channels should be in sync after the model
	// issues commands; we use the GSR snapshot as authoritative for display).
	snap := m.snaps[ChannelGSR]
	state := snap.State
	// During calibration/interrogation, show the remaining time from GSR's snapshot.
	remS := snap.StateRemainS

	stateLabel := stateStyle(state).Bold(true).Render(state.String())

	var body strings.Builder
	body.WriteString(stateLabel)

	// Progress bar during timed states.
	switch state {
	case StateCalibrating:
		total := int(calDuration().Seconds())
		done := total - remS
		if done < 0 {
			done = 0
		}
		bar := renderProgressBar(done, total, eventProgressWidth, colCyan)
		body.WriteString("  " + bar)
		body.WriteString("  " + lipgloss.NewStyle().Foreground(colCyan).
			Render(fmt.Sprintf("%ds / %ds", done, total)))

	case StateInterrogating:
		total := int(intDuration().Seconds())
		done := total - remS
		if done < 0 {
			done = 0
		}
		bar := renderProgressBar(done, total, eventProgressWidth, colRed)
		body.WriteString("  " + bar)
		body.WriteString("  " + lipgloss.NewStyle().Foreground(colRed).
			Render(fmt.Sprintf("%ds / %ds", done, total)))

	case StateCooldown:
		body.WriteString("  " + lipgloss.NewStyle().Foreground(colAmber).
			Render(fmt.Sprintf("%ds to idle", remS)))
	}

	body.WriteString("\n")
	body.WriteString(lipgloss.NewStyle().Foreground(colDim).Render(subNote(m)))

	return titledPanel("CURRENT EVENT", body.String(), m.width-2, colPanelBorder, colPurple)
}

func subNote(m model) string {
	snap := m.snaps[ChannelGSR]
	switch snap.State {
	case StateIdle:
		if !m.lastFreshen.IsZero() && time.Since(m.lastFreshen) < 3*time.Second {
			return "baseline freshened · EWMA tracking resumed"
		}
		if snap.Calibrated {
			return "resting · baselines set · awaiting [i]nterrogation"
		}
		return "resting · baselines UNSET · run [c]alibrate first"
	case StateCalibrating:
		return "collecting baseline samples — sit still, sensors on"
	case StateInterrogating:
		return "scoring window open — measuring z-deviation from baseline"
	case StateCooldown:
		return "cooldown — baselines locked · [b]aseline to skip early"
	}
	return ""
}

func stateStyle(s ServerState) lipgloss.Style {
	switch s {
	case StateCalibrating:
		return lipgloss.NewStyle().Foreground(colCyan)
	case StateInterrogating:
		return lipgloss.NewStyle().Foreground(colRed)
	case StateCooldown:
		return lipgloss.NewStyle().Foreground(colAmber)
	default:
		return lipgloss.NewStyle().Foreground(colDim)
	}
}

// renderProgressBar renders a bar string of exactly `width` cells, filling `done/total` with █ and the rest with ░.
func renderProgressBar(done, total, width int, col lipgloss.Color) string {
	if total <= 0 {
		total = 1
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	fill := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("█", filled))
	track := lipgloss.NewStyle().Foreground(colDim).Render(strings.Repeat("░", width-filled))
	return fill + track
}
