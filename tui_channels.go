package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	chTitleStyle = lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	chLabelStyle = lipgloss.NewStyle().Foreground(colDim)
	chValueStyle = lipgloss.NewStyle().Foreground(colWhite).Bold(true)
	chStatStyle  = lipgloss.NewStyle().Foreground(colDim)
	mutedStyle   = lipgloss.NewStyle().Foreground(colDim)
)

// renderChannels renders the three sensor panels stacked full-width, one per row.
func renderChannels(m model) string {
	w := m.width - 2
	if w < 20 {
		w = 20
	}

	gsrCol := renderChannel(m.snaps[ChannelGSR], m.muted[ChannelGSR], w)
	hrCol := renderChannel(m.snaps[ChannelHR], m.muted[ChannelHR], w)
	rrCol := renderChannel(m.snaps[ChannelRR], m.muted[ChannelRR], w)

	return strings.Join([]string{gsrCol, hrCol, rrCol}, "\n")
}

func renderChannel(s ChannelSnapshot, muted bool, w int) string {
	title := chName(s.Channel)

	var body strings.Builder

	// Row 1: unit label + value
	unit, valStr := channelDisplayUnit(s)
	if muted {
		body.WriteString(mutedStyle.Render(unit + "  " + valStr))
	} else {
		body.WriteString(chLabelStyle.Render(unit))
		body.WriteString("  ")
		body.WriteString(chValueStyle.Render(valStr))
	}
	body.WriteString("\n")

	// Row 2: μ and σ
	muSig := fmt.Sprintf("μ %s   σ %s", formatMu(s), formatSigma(s))
	body.WriteString(chStatStyle.Render(muSig))
	body.WriteString("\n")

	// Row 3: z and L
	zl := fmt.Sprintf("z %+.2f   L %.0f%%", s.Z, s.L)
	body.WriteString(chStatStyle.Render(zl))
	body.WriteString("\n")

	// Sparkline — width matches the panel's inner content area (titledPanel's
	// contentW = w - 4) so it never overruns the border.
	sparkW := w - 4
	if sparkW < 1 {
		sparkW = 1
	}
	if muted {
		mutedRow := mutedStyle.Render(strings.Repeat("░", sparkW))
		rows := make([]string, sparklineRows)
		for i := range rows {
			rows[i] = mutedRow
		}
		body.WriteString(strings.Join(rows, "\n"))
	} else {
		body.WriteString(renderSparkline(s.Sparkline, s.Quality, sparkW))
	}
	body.WriteString("\n")

	// Status dot + label
	body.WriteString(qualityDot(s, muted))
	if !s.LastSample.IsZero() {
		age := time.Since(s.LastSample)
		body.WriteString(chStatStyle.Render(fmt.Sprintf("  last %.2fs", age.Seconds())))
	}

	// Baseline indicator
	body.WriteString("\n")
	if s.Calibrated {
		body.WriteString(lipgloss.NewStyle().Foreground(colGreen).Render("baseline: SET"))
	} else {
		body.WriteString(lipgloss.NewStyle().Foreground(colAmber).Render("baseline: UNSET"))
	}

	return titledPanel(title, body.String(), w, colPanelBorder, colBlue)
}

func chName(ch ChannelID) string {
	return [...]string{"GSR", "HEART RATE", "RESP RATE"}[ch]
}

func channelDisplayUnit(s ChannelSnapshot) (unit, val string) {
	switch s.Channel {
	case ChannelGSR:
		return "raw", fmt.Sprintf("%.0f", s.DisplayValue)
	case ChannelHR:
		return "bpm", fmt.Sprintf("%.1f", s.DisplayValue)
	case ChannelRR:
		return "rr", fmt.Sprintf("%.1f bpm", s.DisplayValue)
	}
	return "", ""
}

func formatMu(s ChannelSnapshot) string {
	switch s.Channel {
	case ChannelGSR:
		return fmt.Sprintf("%.0f", s.Mu)
	default:
		return fmt.Sprintf("%.1f", s.Mu)
	}
}

func formatSigma(s ChannelSnapshot) string {
	switch s.Channel {
	case ChannelGSR:
		return fmt.Sprintf("%.0f", s.Sigma)
	default:
		return fmt.Sprintf("%.1f", s.Sigma)
	}
}

func qualityDot(s ChannelSnapshot, muted bool) string {
	if muted {
		return lipgloss.NewStyle().Foreground(colDim).Render("◌ MUTED")
	}
	if s.IsStub {
		return lipgloss.NewStyle().Foreground(colCyan).Render("○ STUB")
	}
	if s.IsEstimated {
		return lipgloss.NewStyle().Foreground(colCyan).Render("◆ EST")
	}
	dot, col := "●", colGreen
	switch s.Quality {
	case QualityStale:
		col = colAmber
	case QualityDisconnected, QualityStuck, QualityOutOfRange:
		col = colRed
	}
	return lipgloss.NewStyle().Foreground(col).Render(dot + " " + qualityDisplayLabel(s.Quality))
}

// qualityDisplayLabel softens alarm-sounding internal state names for the
// operator-facing status dot, without changing the logged/internal name
// (Quality.String(), used by quality_change events, the help legend, and
// replay comparisons, is unaffected).
func qualityDisplayLabel(q Quality) string {
	if q == QualityOutOfRange {
		return "CHECK CONTACT"
	}
	return q.String()
}
