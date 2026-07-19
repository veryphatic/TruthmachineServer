package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var sparkChars = []rune("▁▂▃▄▅▆▇█")

// sparklineRows is the number of stacked text rows each channel sparkline renders as,
// giving the bar graph more vertical resolution than a single block-character row.
const sparklineRows = 2

// renderSparkline maps the last sparklineLen display values to a multi-row, bar-chart-style
// sparkline string (sparklineRows lines tall, joined with "\n"). Each column fills
// from the bottom row up, using the 8-level block characters for sub-row resolution.
// The color is chosen based on current signal quality.
// Zero values (no data yet) render as ░ in the track color, on every row.
// width caps how many of the most-recent samples are drawn (one column each), so the
// line fits whatever panel width is available instead of always drawing sparklineLen columns.
func renderSparkline(values [sparklineLen]float64, quality Quality, width int) string {
	if width <= 0 {
		width = 1
	}
	if width > len(values) {
		width = len(values)
	}
	shown := values[len(values)-width:]

	// Find min/max over non-zero entries to normalize.
	min, max := shown[0], shown[0]
	hasData := false
	for _, v := range shown {
		if v > 0 {
			if !hasData || v < min {
				min = v
			}
			if v > max {
				max = v
			}
			hasData = true
		}
	}

	col := qualitySparkColor(quality)
	trackCol := lipgloss.NewStyle().Foreground(colDim)
	fillStyle := lipgloss.NewStyle().Foreground(col)

	levels := len(sparkChars)
	totalLevels := sparklineRows * levels

	// units[i] is the total filled resolution units (1..totalLevels) for column i,
	// or -1 if there's no sample yet.
	units := make([]int, len(shown))
	for i, v := range shown {
		if v <= 0 {
			units[i] = -1
			continue
		}
		norm := 1.0
		if max > min {
			norm = (v - min) / (max - min)
		}
		u := int(norm*float64(totalLevels-1)) + 1
		if u > totalLevels {
			u = totalLevels
		}
		units[i] = u
	}

	rows := make([]string, sparklineRows)
	for r := 0; r < sparklineRows; r++ {
		rowFromBottom := sparklineRows - 1 - r
		var sb strings.Builder
		for _, u := range units {
			if u < 0 {
				sb.WriteString(trackCol.Render("░"))
				continue
			}
			cellUnits := u - rowFromBottom*levels
			switch {
			case cellUnits <= 0:
				sb.WriteString(" ")
			case cellUnits >= levels:
				sb.WriteString(fillStyle.Render(string(sparkChars[levels-1])))
			default:
				sb.WriteString(fillStyle.Render(string(sparkChars[cellUnits-1])))
			}
		}
		rows[r] = sb.String()
	}
	return strings.Join(rows, "\n")
}

func qualitySparkColor(q Quality) lipgloss.Color {
	switch q {
	case QualityOK:
		return colGreen
	case QualityStale:
		return colAmber
	default:
		return colRed
	}
}
