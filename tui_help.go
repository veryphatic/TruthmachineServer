package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderHelp renders the full-screen help view.
// Any key press returns to the dashboard.
func renderHelp(m model) string {
	h := lipgloss.NewStyle().Foreground(colTextBright).Bold(true)
	k := lipgloss.NewStyle().Foreground(colPurple)
	v := lipgloss.NewStyle().Foreground(colText)
	d := lipgloss.NewStyle().Foreground(colDim)
	grn := lipgloss.NewStyle().Foreground(colGreen)
	amb := lipgloss.NewStyle().Foreground(colAmber)
	red := lipgloss.NewStyle().Foreground(colRed)
	cyn := lipgloss.NewStyle().Foreground(colCyan)

	row := func(key, desc string) string {
		keyW := 22
		kStr := k.Render(key)
		pad := keyW - lipgloss.Width(kStr)
		if pad < 1 {
			pad = 1
		}
		return kStr + strings.Repeat(" ", pad) + v.Render(desc)
	}

	qrow := func(dot lipgloss.Style, dotStr, label, desc string) string {
		dotW := 22
		sym := dot.Render(dotStr + " " + label)
		pad := dotW - lipgloss.Width(sym)
		if pad < 1 {
			pad = 1
		}
		return sym + strings.Repeat(" ", pad) + d.Render(desc)
	}

	var sb strings.Builder

	// ── Keyboard shortcuts ────────────────────────────────────────────────────
	sb.WriteString(h.Render("KEYBOARD SHORTCUTS") + "\n")
	sb.WriteString(row("[c]  calibrate",    "15-second baseline sample window — run at session start, sensors on skin") + "\n")
	sb.WriteString(row("[i]  interrogate",  "8-second scoring window — ask the question while this runs") + "\n")
	sb.WriteString(row("[s]  sensitivity",  "Adjust k multiplier (0.1–5.0) — higher k amplifies z-score response") + "\n")
	sb.WriteString(row("[b]  baseline",     "Skip cooldown and resume EWMA updates — only has effect during COOLDOWN") + "\n")
	sb.WriteString(row("[m]  manual L",     "Inject a specific L value (0–100) directly into history") + "\n")
	sb.WriteString(row("[r]  random low",   "Insert a random 1–5 L value (calibration cover story)") + "\n")
	sb.WriteString(row("[u]  mute",         "Toggle a channel out of the combined L weighting") + "\n")
	sb.WriteString(row("[l]  log",          "Full-screen live JSONL event log — PgUp/PgDn to scroll, [l]/esc to close") + "\n")
	sb.WriteString(row("[x]  reset",        "Confirm [y] to clear ALL baselines + history — marks end of performance") + "\n")
	sb.WriteString(row("[?]  help",         "This screen — any key to close") + "\n")
	sb.WriteString(row("[q]  quit",         "Press twice within 2.5s to exit") + "\n")

	sb.WriteString("\n")

	// ── Data values ───────────────────────────────────────────────────────────
	sb.WriteString(h.Render("DATA VALUES") + "\n")
	sb.WriteString(row("L  (lie likelihood)",   "0–100 raw score, sent to QLab clamped 1–99.  " +
		red.Render("<33 red") + "  " +
		amb.Render("33–65 amber") + "  " +
		grn.Render("≥66 green")) + "\n")
	sb.WriteString(d.Render(strings.Repeat(" ", 23) +
		"0 = indistinguishable from baseline · 100 = maximum measured deviation") + "\n")
	sb.WriteString(row("μ  (mu)",    "Adaptive baseline mean (EWMA).  Updated continuously in IDLE.") + "\n")
	sb.WriteString(row("σ  (sigma)", "Adaptive baseline std dev.  Floors: GSR=10  HR=3.0  RR=1.5") + "\n")
	sb.WriteString(row("z  (z-score)", "Standard deviations from baseline.  L = 100 × tanh(k × z / 2)") + "\n")
	sb.WriteString(row("Combined L", "Weighted: GSR×0.5 + HR×0.3 + RR×0.2.  Muted/disconnected excluded.") + "\n")

	sb.WriteString("\n")

	// ── OSC commands ──────────────────────────────────────────────────────────
	sb.WriteString(h.Render("OSC COMMANDS") + "\n")
	sb.WriteString(d.Render("Inbound, no arguments (QLab → server):") + "\n")
	sb.WriteString(row("/calibrate",   "Starts 15s batch calibration on all channels") + "\n")
	sb.WriteString(row("/interrogate", "Starts 8s scoring window on all channels") + "\n")
	sb.WriteString(row("/reset",       "Freshens baselines + clears history — same as [x][y], no confirm") + "\n")
	sb.WriteString(row("/baseline",    "Freshens baselines only, history untouched — same as [b]") + "\n")
	sb.WriteString(row("/mute/gsr",    "Toggles GSR out of the combined L weighting — same as [u][g]") + "\n")
	sb.WriteString(row("/mute/hr",     "Toggles HR out of the combined L weighting — same as [u][h]") + "\n")
	sb.WriteString(row("/mute/rr",     "Toggles RR out of the combined L weighting — same as [u][r]") + "\n")
	sb.WriteString(row("/random_low",  "Inserts a random 1–5 L value — same as [r]") + "\n")
	sb.WriteString(d.Render("Inbound, one numeric argument (QLab → server):") + "\n")
	sb.WriteString(row("/sensitivity <k>",  "Sets k multiplier, clamped 0.1–5.0 — same as [s]") + "\n")
	sb.WriteString(row("/manual_l <L>",     "Injects an L override, clamped 0–100 — same as [m]") + "\n")
	sb.WriteString(d.Render("Outbound (server → QLab):") + "\n")
	sb.WriteString(row("/cue/l{N}/start",    "N = 1–99 combined L — once per completed interrogation") + "\n")
	sb.WriteString(row("/cue/bpm{NNN}/start","NNN = zero-padded BPM — throttled to 1/sec") + "\n")
	sb.WriteString(row("/cue/g{N}/start",    "N = 1–20 GSR cue id — throttled to 1 per 3s") + "\n")
	sb.WriteString(row("/cue/r{N}/start",    "N = 1–20 respiration cue id — throttled to 1 per 3s") + "\n")
	sb.WriteString(row("/cue/p/start",       "Pulse beat — fires in real time at the current BPM rate") + "\n")

	sb.WriteString("\n")

	// ── Signal quality ────────────────────────────────────────────────────────
	sb.WriteString(h.Render("SIGNAL QUALITY") + "\n")
	sb.WriteString(qrow(grn, "●", "OK",            "Signal in range, updating normally") + "\n")
	sb.WriteString(qrow(amb, "●", "STALE",         "No data for >2s") + "\n")
	sb.WriteString(qrow(red, "●", "OUT_OF_RANGE",  "GSR open circuit, or BPM outside 30–250 range") + "\n")
	sb.WriteString(qrow(red, "●", "STUCK",         "Signal not varying — check electrode contact") + "\n")
	sb.WriteString(qrow(red, "●", "DISCONNECTED",  "No data for >10s") + "\n")
	sb.WriteString(qrow(cyn, "○", "STUB",          "RR channel: synthetic sine wave — no hardware connected") + "\n")
	sb.WriteString(qrow(d,   "◌", "MUTED",         "Channel excluded from combined L calculation") + "\n")

	sb.WriteString("\n")

	// ── System states ─────────────────────────────────────────────────────────
	sb.WriteString(h.Render("SYSTEM STATES") + "\n")
	sb.WriteString(row(d.Render("IDLE"),                       "Resting.  EWMA baseline updates freely.") + "\n")
	sb.WriteString(row(cyn.Render("CALIBRATING")+" (15s)",    "Batch-samples μ and σ.  Sit still, sensors firmly on skin.") + "\n")
	sb.WriteString(row(red.Render("INTERROGATING")+" (8s)",   "Scoring window open.  Ask the question here.") + "\n")
	sb.WriteString(row(amb.Render("COOLDOWN")+" (15s)",       "Baseline frozen.  L value locked.  [b] to exit early.") + "\n")

	sb.WriteString("\n")

	// ── GSR channel note ──────────────────────────────────────────────────────
	sb.WriteString(h.Render("CHANNEL NOTES") + "\n")
	sb.WriteString(v.Render("GSR") + d.Render("  Skin conductance (Grove analog, ADC 0–4095).  "+
		"Inverted z-score: conductance drops on arousal.") + "\n")
	sb.WriteString(v.Render("HR ") + d.Render("  Heart rate BPM (Grove I²C).  "+
		"Warmup required after connect — first BPM=0 sample clears warmup guard.") + "\n")
	sb.WriteString(v.Render("RR ") + d.Render("  Respiratory rate BPM (slide-pot, zero-crossing detection).  "+
		"Stub sine wave active until hardware sends rr,… lines.") + "\n")

	sb.WriteString("\n" + d.Render("any key  close help"))

	body := sb.String()
	panel := titledPanel("HELP", body, m.width-2, colPanelBorder, colPurple)

	// Center vertically on a dark background if the panel is shorter than the terminal.
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, panel,
		lipgloss.WithWhitespaceBackground(colBG))
}
