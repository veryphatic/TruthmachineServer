package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// buildHistoryPanel builds the (unclipped) full-session history content —
// every row ever appended to m.history, newest first, no historyMaxRows cap.
// Split out from renderHistoryView so Update() can also refresh
// m.historyVP's persisted content (on open / resize / tick) without going
// through View(), whose model copy is discarded after rendering — see the
// same pattern for m.dashVP and m.helpVP.
func buildHistoryPanel(m model) string {
	var body string
	if len(m.history) == 0 {
		body = lipgloss.NewStyle().Foreground(colDim).Render("no interrogations yet")
	} else {
		lines := make([]string, len(m.history))
		for i := len(m.history) - 1; i >= 0; i-- {
			lines[len(m.history)-1-i] = renderHistoryRow(m.history[i])
		}
		body = strings.Join(lines, "\n")
	}

	hint := lipgloss.NewStyle().Foreground(colDim).Render(
		fmt.Sprintf("↑/↓ · PgUp/PgDn  scroll   ·   any other key  close   ·   %d entries this session", len(m.history)))

	return titledPanel("SESSION HISTORY", body+"\n\n"+hint, m.width-2, colPanelBorder, colDim)
}

// renderHistoryView renders the full-screen scrollable session history.
func renderHistoryView(m model) string {
	m.historyVP.SetContent(buildHistoryPanel(m))
	return m.historyVP.View()
}
