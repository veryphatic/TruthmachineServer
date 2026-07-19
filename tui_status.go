package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderStatus renders the STATUS panel showing per-channel connection and baseline state.
func renderStatus(m model) string {
	cols := make([]string, 3)
	for i, s := range m.snaps {
		cols[i] = renderStatusChannel(s, m.muted[i])
	}
	body := strings.Join(cols, "   │   ")
	return titledPanel("STATUS", body, m.width-2, colPanelBorder, colDim)
}

func renderStatusChannel(s ChannelSnapshot, muted bool) string {
	name := [...]string{"GSR", "HR", "RR"}[s.Channel]
	port := listenAddr // all channels share one TCP port in v2

	var dot, dotLabel string
	if muted {
		dot = lipgloss.NewStyle().Foreground(colDim).Render("◌")
		dotLabel = port + "  muted"
	} else if s.IsStub {
		dot = lipgloss.NewStyle().Foreground(colCyan).Render("○")
		dotLabel = ":stub  synthetic"
	} else {
		col := qualityCol(s.Quality)
		dot = lipgloss.NewStyle().Foreground(col).Render("●")
		dotLabel = port
		if !s.LastSample.IsZero() {
			age := time.Since(s.LastSample)
			dotLabel += fmt.Sprintf("  last %.2fs", age.Seconds())
		}
	}

	line1 := lipgloss.NewStyle().Foreground(colTextBright).Render(name) +
		"  " + dot + "  " +
		lipgloss.NewStyle().Foreground(colDim).Render(dotLabel)

	var line2 string
	if s.Calibrated {
		line2 = lipgloss.NewStyle().Foreground(colGreen).Render("baseline: SET")
	} else {
		line2 = lipgloss.NewStyle().Foreground(colAmber).Render("baseline: UNSET")
	}

	return line1 + "\n" + line2
}

func qualityCol(q Quality) lipgloss.Color {
	switch q {
	case QualityOK:
		return colGreen
	case QualityStale:
		return colAmber
	default:
		return colRed
	}
}
