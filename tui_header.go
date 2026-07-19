package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// bannerLines is the 5-row ASCII block-font wordmark for TRUTHMACHINE.
var bannerLines = [3]string{
	`╶┬╴╭─╮╷ ╷╶┬╴╷ ╷╭┬╮╭─╮╭─╴╷ ╷╷╭╮╷╭─╴`,
	` │ ├┬╯│ │ │ ├─┤│││├─┤│  ├─┤││╰┤├╴`,
	` ╵ ╵╰╴╰─╯ ╵ ╵ ╵╵ ╵╵ ╵╰─╴╵ ╵╵╵ ╵╰─╴`,
}

var (
	bannerStyle   = lipgloss.NewStyle().Foreground(colPurple)
	subtitleStyle = lipgloss.NewStyle().Foreground(colDim)
	clockStyle    = lipgloss.NewStyle().Foreground(colCyan)
	sensStyle     = lipgloss.NewStyle().Foreground(colAmber)
	dimLabelStyle = lipgloss.NewStyle().Foreground(colDim)
)

const rightBlockW = 26 // fixed width for the clock / sensitivity column

// renderHeader returns the header band: ASCII wordmark on the left, stats on the right.
func renderHeader(m model) string {
	// Right block: clock, sensitivity — right-aligned, 2 lines tall.
	now := time.Now()
	clock := clockStyle.Render(now.Format("15:04:05"))

	sens := dimLabelStyle.Render("sensitivity ×") +
		sensStyle.Render(" "+fmt.Sprintf("%.1f", m.sensitivity))

	rightBlock := lipgloss.NewStyle().
		Width(rightBlockW).
		Align(lipgloss.Right).
		Render(clock + "\n" + sens)

	// Left block: banner (5 rows) + subtitle (1 row), fills remaining width.
	bannerStr := bannerStyle.Render(strings.Join(bannerLines[:], "\n"))
	subtitle := subtitleStyle.Render(" UNIFIED SENSOR SERVER · COUNTERPILOT")
	leftContent := bannerStr + "\n" + subtitle

	leftW := m.width - rightBlockW
	if leftW < 10 {
		leftW = 10
	}
	leftBlock := lipgloss.NewStyle().Width(leftW).Render(leftContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, rightBlock)
}
