package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

// ── Log filter ────────────────────────────────────────────────────────────────

type logFilter int

const (
	logFilterOSC    logFilter = iota // default: only type:"osc" records
	logFilterStream                  // only type:"sample" (raw sensor data)
	logFilterEvents                  // events + scored + osc + config (no samples)
	logFilterAll                     // everything
)

func (f logFilter) String() string {
	return [...]string{"OSC", "STREAM", "EVENTS", "ALL"}[f]
}

func (f logFilter) Next() logFilter {
	return (f + 1) % (logFilterAll + 1)
}

// logLinePassesFilter returns true if the raw JSONL line should be shown
// under the given filter mode.
func logLinePassesFilter(raw string, f logFilter) bool {
	var rec map[string]any
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return f == logFilterAll // malformed lines only in ALL mode
	}
	typ, _ := rec["type"].(string)
	switch f {
	case logFilterOSC:
		return typ == "osc"
	case logFilterStream:
		return typ == "sample"
	case logFilterEvents:
		return typ == "event" || typ == "scored" || typ == "osc" || typ == "config"
	default: // logFilterAll
		return true
	}
}

// ── Viewport init ─────────────────────────────────────────────────────────────

const maxLogLines = 2000

func initLogViewport(w, h int) viewport.Model {
	vp := viewport.New(w, h-4)
	vp.SetContent("")
	return vp
}

// ── Append + rebuild ──────────────────────────────────────────────────────────

// appendLogLine stores the raw JSONL line and rebuilds the viewport content
// with the current filter applied.
func appendLogLine(m *model, raw string) {
	m.logLines = append(m.logLines, raw)
	if len(m.logLines) > maxLogLines {
		m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
	}
	rebuildLogViewport(m)
}

// rebuildLogViewport re-filters all stored raw lines and refreshes the viewport.
// Called on every new line and whenever the filter changes.
func rebuildLogViewport(m *model) {
	var sb strings.Builder
	first := true
	for _, raw := range m.logLines {
		if !logLinePassesFilter(raw, m.logFilter) {
			continue
		}
		if !first {
			sb.WriteByte('\n')
		}
		sb.WriteString(colorLogLine(raw))
		first = false
	}
	m.logVP.SetContent(sb.String())
	m.logVP.GotoBottom()
}

// ── Render ────────────────────────────────────────────────────────────────────

func renderLogView(m model) string {
	// Count filtered lines
	total := len(m.logLines)
	visible := 0
	for _, raw := range m.logLines {
		if logLinePassesFilter(raw, m.logFilter) {
			visible++
		}
	}

	title := lipgloss.NewStyle().Foreground(colPurple).Render("● LIVE LOG · " + m.log.FileName())

	filterLabel := lipgloss.NewStyle().Foreground(colAmber).Render(m.logFilter.String())
	filterHint := lipgloss.NewStyle().Foreground(colDim).Render("[f] filter: ") + filterLabel
	countHint := lipgloss.NewStyle().Foreground(colDim).
		Render(fmt.Sprintf("  %d / %d lines", visible, total))
	closeHint := lipgloss.NewStyle().Foreground(colDim).Render("  [l]/esc close · PgUp/PgDn scroll")

	right := filterHint + countHint + closeHint

	pad := m.width - lipgloss.Width(title) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	header := title + strings.Repeat(" ", pad) + right

	return header + "\n" + m.logVP.View()
}

// ── Line colouring ────────────────────────────────────────────────────────────

// colorLogLine applies ANSI color to a raw JSONL log line based on its "type" field.
func colorLogLine(raw string) string {
	var rec map[string]any
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return lipgloss.NewStyle().Foreground(colRed).Render(raw)
	}

	// Timestamp prefix: positions 11–22 of RFC3339Nano "2006-01-02T15:04:05.999..."
	ts := ""
	if t, ok := rec["t"].(string); ok && len(t) >= 19 {
		if len(t) >= 23 {
			ts = t[11:23]
		} else {
			ts = t[11:]
		}
	}
	tsStr := lipgloss.NewStyle().Foreground(colDimmer).Render(ts)

	// Remove timestamp from the displayed JSON to keep lines short.
	delete(rec, "t")
	b, _ := json.Marshal(rec)
	content := string(b)

	var style lipgloss.Style
	switch rec["type"] {
	case "sample":
		style = lipgloss.NewStyle().Foreground(colDim)
	case "event":
		style = lipgloss.NewStyle().Foreground(colAmber)
	case "scored":
		style = lipgloss.NewStyle().Foreground(colGreen)
	case "osc":
		if dir, _ := rec["dir"].(string); dir == "in" || dir == "replay-in" {
			style = lipgloss.NewStyle().Foreground(colCyan)
		} else {
			style = lipgloss.NewStyle().Foreground(colBlue)
		}
	case "config":
		style = lipgloss.NewStyle().Foreground(colCyan)
	default:
		style = lipgloss.NewStyle().Foreground(colRed)
	}

	return tsStr + "  " + style.Render(content)
}
