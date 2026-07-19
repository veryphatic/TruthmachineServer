package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// titledPanel renders a rounded-border panel with the title embedded in the top border line.
// Width w includes the two border characters. Minimum usable width is 10.
func titledPanel(title, body string, w int, borderCol, titleCol lipgloss.Color) string {
	if w < 10 {
		w = 10
	}
	innerW := w - 2 // subtract left and right border runes

	bc := lipgloss.NewStyle().Foreground(borderCol)
	tc := lipgloss.NewStyle().Foreground(titleCol)

	// Title embeds as: ╭─ TITLE ─────╮
	titleStr := "─ " + title + " "
	titleW := lipgloss.Width(titleStr) // rune-safe visual width
	dashRem := innerW - titleW
	if dashRem < 0 {
		dashRem = 0
	}
	top := bc.Render("╭") + tc.Render(titleStr) + bc.Render(strings.Repeat("─", dashRem)+"╮")

	bottom := bc.Render("╰" + strings.Repeat("─", innerW) + "╯")

	// Body rows: each line gets side border characters and 1-space padding.
	contentW := innerW - 2 // space for " content " (1 pad each side)
	bodyLines := strings.Split(body, "\n")
	rows := make([]string, len(bodyLines))
	for i, line := range bodyLines {
		lw := lipgloss.Width(line)
		pad := contentW - lw
		if pad < 0 {
			pad = 0
		}
		rows[i] = bc.Render("│") + " " + line + strings.Repeat(" ", pad) + " " + bc.Render("│")
	}

	return top + "\n" + strings.Join(rows, "\n") + "\n" + bottom
}
