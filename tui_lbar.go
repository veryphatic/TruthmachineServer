package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const lBarMinWidth = 20

// renderLBar renders the L · COMBINED panel with a large L number and a bar that
// fills the panel's inner content width (mirrors the sensor sparkline sizing).
func renderLBar(m model) string {
	L := m.combinedL
	col := lColor(L)

	w := m.width - 2
	barW := w - 4
	if barW < lBarMinWidth {
		barW = lBarMinWidth
	}

	sc := getScoringCfg()
	lNum := lipgloss.NewStyle().Foreground(col).Bold(true).
		Render(fmt.Sprintf("%.0f", L))
	note := lipgloss.NewStyle().Foreground(colDim).
		Render(fmt.Sprintf("   weighted deception likelihood · gsr·%.1f  hr·%.1f  rr·%.1f",
			sc.WeightGSR, sc.WeightHR, sc.WeightRR))

	bar := renderProgressBar(int(L), 100, barW, col)
	ticks := lipgloss.NewStyle().Foreground(colDim).
		Render(ticksLine(barW))

	body := lNum + note + "\n" + bar + "\n" + ticks
	return titledPanel("L · COMBINED", body, w, colPanelBorder, colPurple)
}

// lColor returns the threshold color for a given L value.
func lColor(L float64) lipgloss.Color {
	switch {
	case L >= 66:
		return colGreen
	case L >= 33:
		return colAmber
	default:
		return colRed
	}
}

// ticksLine returns a scale-ticks string aligned under the bar: 0, 33, 66, 100.
func ticksLine(barW int) string {
	var sb strings.Builder
	sb.WriteString("0")
	t33 := barW*33/100 - 1
	t66 := barW*66/100 - 2
	t100 := barW - 3
	for i := 1; i < barW; i++ {
		switch i {
		case t33:
			sb.WriteString("33")
		case t66:
			sb.WriteString("66")
		case t100:
			sb.WriteString("100")
		default:
			sb.WriteString(" ")
		}
	}
	return sb.String()
}
