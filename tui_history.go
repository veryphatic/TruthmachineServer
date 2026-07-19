package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const historyBarWidth = 8
const historyMaxRows = 7

// renderHistory renders the INTERROGATION HISTORY panel.
// Rows are newest-first, capped at historyMaxRows visible rows.
func renderHistory(m model) string {
	if len(m.history) == 0 {
		body := lipgloss.NewStyle().Foreground(colDim).Render("no interrogations yet")
		return titledPanel("HISTORY", body, m.width-2, colPanelBorder, colDim)
	}

	rows := m.history
	if len(rows) > historyMaxRows {
		rows = rows[len(rows)-historyMaxRows:]
	}

	var lines []string
	for i := len(rows) - 1; i >= 0; i-- {
		lines = append(lines, renderHistoryRow(rows[i]))
	}

	body := strings.Join(lines, "\n")
	return titledPanel("HISTORY", body, m.width-2, colPanelBorder, colDim)
}

func renderHistoryRow(r ScoreRecord) string {
	nStr := lipgloss.NewStyle().Foreground(colBlue).Render(fmt.Sprintf("#%-3d", r.N))
	tStr := lipgloss.NewStyle().Foreground(colDim).Render(r.Time.Format("15:04:05"))

	if r.IsCalib {
		label := lipgloss.NewStyle().Foreground(colPurple).Render("CALIBRATED")
		mu := lipgloss.NewStyle().Foreground(colGreen).Render("μ set  μ set  μ set")
		return nStr + "  " + tStr + "  " + label + "  " + mu
	}

	if r.IsFresh {
		label := lipgloss.NewStyle().Foreground(colGreen).Render("BASELINE FRESHENED")
		return nStr + "  " + tStr + "  " + label
	}

	labelStr := "Interrogation"
	if r.Label != "" {
		labelStr = r.Label
	}
	labelStyle := colDim
	if r.IsForced {
		labelStyle = colPurple
	}
	label := lipgloss.NewStyle().Foreground(labelStyle).Render(labelStr)

	gsrL := formatChannelL(r.GSR_L, r.GSR_Est)
	hrL := formatChannelL(r.HR_L, r.HR_Est)
	rrL := formatChannelL(r.RR_L, r.RR_Est)

	col := lColor(r.Combined)
	bar := lipgloss.NewStyle().Foreground(col).Render(miniBar(r.Combined, historyBarWidth))
	comb := lipgloss.NewStyle().Foreground(col).Bold(true).Render(fmt.Sprintf("%3.0f%%", r.Combined))

	return nStr + "  " + tStr + "  " + label + "  " +
		gsrL + "  " + hrL + "  " + rrL + "  " + bar + "  " + comb
}

func formatChannelL(L float64, estimated bool) string {
	if L < 0 {
		return lipgloss.NewStyle().Foreground(colDim).Render("  —  ")
	}
	if estimated {
		return lipgloss.NewStyle().Foreground(colCyan).Render(fmt.Sprintf("%3.0f%%~", L))
	}
	col := lColor(L)
	return lipgloss.NewStyle().Foreground(col).Render(fmt.Sprintf("%3.0f%%", L))
}

func miniBar(L float64, w int) string {
	filled := int(L / 100 * float64(w))
	if filled > w {
		filled = w
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", w-filled)
}
