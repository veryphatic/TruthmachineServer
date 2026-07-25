package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Palette ───────────────────────────────────────────────────────────────────

var (
	colBG          = lipgloss.Color("#16161e")
	colPanelBorder = lipgloss.Color("#2a2c3d")
	colText        = lipgloss.Color("#a9b1d6")
	colTextBright  = lipgloss.Color("#c0caf5")
	colWhite       = lipgloss.Color("#ffffff")
	colDim         = lipgloss.Color("#565f89")
	colDimmer      = lipgloss.Color("#414868")
	colGreen       = lipgloss.Color("#9ece6a")
	colAmber       = lipgloss.Color("#e0af68")
	colRed         = lipgloss.Color("#f7768e")
	colBlue        = lipgloss.Color("#7aa2f7")
	colCyan        = lipgloss.Color("#7dcfff")
	colPurple      = lipgloss.Color("#bb9af7")
)

// ── View mode ─────────────────────────────────────────────────────────────────

type viewMode int

const (
	viewDashboard viewMode = iota
	viewLog
	viewHelp
)

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	// Server components
	gsrProc *GSRProcessor
	hrProc  *HRProcessor
	rrProc  *RRProcessor
	events  EventSink
	log     *AppLogger

	// Per-channel snapshots polled every 100ms
	snaps [3]ChannelSnapshot

	// Combined deception likelihood
	combinedL   float64
	sensitivity float64
	muted       [3]bool

	// Session start time
	startTime time.Time

	// Interrogation history (newest at highest index)
	history  []ScoreRecord
	histNext int // auto-incremented for each new row

	// Pending score aggregation: when an interrogation fires,
	// we wait for all active channels' ScoredMsg before writing the history row.
	pendingL         [3]float64 // -1 = not yet received
	pendingEstimated [3]bool
	pendingRound     int
	pendingActive    bool

	// View state
	view      viewMode
	quitArmed bool
	quitAt    time.Time

	// Log pane (bubbles/viewport)
	logVP     viewport.Model
	logLines  []string // raw JSONL lines (un-styled); re-filtered on every append + filter change
	logFilter logFilter

	// Input modal
	inputMode inputKind
	textInput textinput.Model

	// Terminal dimensions
	width, height int

	// Path to truthmachine.json — used to persist mute state on toggle.
	cfgPath string

	// OSC bridge — nil when OSC is disabled (bind failure at startup).
	osc *OSCBridge

	// lastFreshen tracks the most recent FreshenBaseline call for the transient sub-note.
	lastFreshen time.Time
}

// newModel constructs the initial model. The Bubble Tea program must be started after.
func newModel(gsr *GSRProcessor, hr *HRProcessor, rr *RRProcessor,
	events EventSink, log *AppLogger, cfgPath string, initMuted [3]bool, bridge *OSCBridge) model {
	m := model{
		gsrProc:     gsr,
		hrProc:      hr,
		rrProc:      rr,
		events:      events,
		log:         log,
		sensitivity: getScoringCfg().DefaultSensitivity,
		startTime:   time.Now(),
		pendingL:    [3]float64{-1, -1, -1},
		logFilter:   logFilterOSC,
		muted:       initMuted,
		cfgPath:     cfgPath,
		osc:         bridge,
		width:       120,
		height:      40,
	}
	m.logVP = initLogViewport(m.width, m.height)
	return m
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		waitForEvent(m.events),
		waitForLogLine(m.log.LogCh),
	)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpH := m.height - 4
		if vpH < 5 {
			vpH = 5
		}
		m.logVP.Width = m.width
		m.logVP.Height = vpH
		m.logVP.SetContent(strings.Join(m.logLines, "\n"))
		m.logVP.GotoBottom()
		return m, nil

	case TickMsg:
		m.snaps[ChannelGSR] = m.gsrProc.Snapshot()
		m.snaps[ChannelHR] = m.hrProc.Snapshot()
		m.snaps[ChannelRR] = m.rrProc.Snapshot()
		m.combinedL = computeCombined(m.snaps, m.muted)
		if m.osc != nil {
			m.osc.SendPeriodic(m.snaps)
		}
		if m.quitArmed && time.Since(m.quitAt) > 2500*time.Millisecond {
			m.quitArmed = false
		}
		return m, tickCmd()

	case ScoredMsg:
		if m.pendingActive {
			m.pendingL[msg.Channel] = msg.L
			m.pendingEstimated[msg.Channel] = msg.IsEstimated
			if m.allScoredOrInactive() {
				m = m.finalizeScore()
			}
		}
		return m, waitForEvent(m.events)

	case CalibrationDoneMsg:
		m.histNext++
		m.history = append(m.history, ScoreRecord{
			N: m.histNext, Time: time.Now(), IsCalib: true,
			GSR_L: -1, HR_L: -1, RR_L: -1,
		})
		return m, waitForEvent(m.events)

	case BaselineFreshenedMsg:
		m = m.appendFreshenRow(true)
		return m, waitForEvent(m.events)

	case InterrogateStartMsg:
		m, cmd := m.startPendingRound()
		return m, tea.Batch(cmd, waitForEvent(m.events))

	case StateChangeMsg, QualityChangeMsg:
		return m, waitForEvent(m.events)

	case LogLineMsg:
		appendLogLine(&m, msg.Line)
		return m, waitForLogLine(m.log.LogCh)

	case QuitDisarmMsg:
		m.quitArmed = false
		return m, nil

	case ScoreTimeoutMsg:
		if m.pendingActive && m.pendingRound == msg.Round {
			m = m.finalizeScore()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// ── Key handling ──────────────────────────────────────────────────────────────

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Modal intercepts all keys.
	if m.inputMode != inputNone {
		return m.handleModalKey(msg)
	}

	// Log view intercepts navigation.
	if m.view == viewLog {
		return m.handleLogKey(msg)
	}

	// Help view: any key closes.
	if m.view == viewHelp {
		m.view = viewDashboard
		return m, nil
	}

	// Dashboard hotkeys. Case-insensitive: shifted/caps-lock letters behave the same.
	switch strings.ToLower(msg.String()) {
	case "c":
		m.gsrProc.StartCalibrate()
		m.hrProc.StartCalibrate()
		m.rrProc.StartCalibrate()

	case "i":
		var cmd tea.Cmd
		m, cmd = m.startPendingRound()
		m.gsrProc.StartInterrogate()
		m.hrProc.StartInterrogate()
		m.rrProc.StartInterrogate()
		return m, cmd

	case "s":
		m.inputMode = inputSensitivity
		m.textInput = newTextInput(fmt.Sprintf("%.1f", m.sensitivity))
		return m, textinput.Blink

	case "b":
		m.gsrProc.FreshenBaseline()
		m.hrProc.FreshenBaseline()
		m.rrProc.FreshenBaseline()
		m = m.appendFreshenRow(false)

	case "x":
		m.inputMode = inputResetConfirm
		return m, nil

	case "m":
		m.inputMode = inputManualL
		m.textInput = newTextInput("50")
		return m, textinput.Blink

	case "r":
		// Random low: 1–5
		l := float64(1 + time.Now().UnixNano()%5)
		m.histNext++
		m.history = append(m.history, ScoreRecord{
			N: m.histNext, Time: time.Now(),
			GSR_L: -1, HR_L: -1, RR_L: -1,
			Combined: l, IsForced: true,
		})
		m.combinedL = l
		m.log.Event(channelSystem, "random_low", "L", l)
		if m.osc != nil {
			m.osc.SendL(l)
		}

	case "u":
		m.inputMode = inputMute

	case "l":
		m.view = viewLog
		m.logVP.GotoBottom()

	case "?":
		m.view = viewHelp

	case "q":
		if m.quitArmed {
			return m, tea.Quit
		}
		m.quitArmed = true
		m.quitAt = time.Now()
		return m, quitDisarmCmd()
	}

	return m, nil
}

func (m model) handleLogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "l", "esc":
		m.view = viewDashboard
		return m, nil
	case "f":
		m.logFilter = m.logFilter.Next()
		rebuildLogViewport(&m)
		return m, nil
	}
	var cmd tea.Cmd
	m.logVP, cmd = m.logVP.Update(msg)
	return m, cmd
}

func (m model) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "esc":
		m.inputMode = inputNone
		return m, nil
	case "enter":
		return m.applyModal()
	}

	if m.inputMode == inputMute {
		switch strings.ToLower(msg.String()) {
		case "g":
			m.muted[ChannelGSR] = !m.muted[ChannelGSR]
		case "h":
			m.muted[ChannelHR] = !m.muted[ChannelHR]
		case "r":
			m.muted[ChannelRR] = !m.muted[ChannelRR]
		default:
			m.inputMode = inputNone
			return m, nil
		}
		m.inputMode = inputNone
		if m.cfgPath != "" {
			if err := saveMuteConfig(m.cfgPath, m.muted); err != nil {
				m.log.Event(channelSystem, "config_error", "err", err.Error())
			}
		}
		return m, nil
	}

	if m.inputMode == inputResetConfirm {
		if strings.ToLower(msg.String()) == "y" {
			m.gsrProc.FreshenBaseline()
			m.hrProc.FreshenBaseline()
			m.rrProc.FreshenBaseline()
			m.log.Event(channelSystem, "reset", "source", "keyboard")
			m = m.appendFreshenRow(true)
		}
		m.inputMode = inputNone
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) applyModal() (tea.Model, tea.Cmd) {
	val := strings.TrimSpace(m.textInput.Value())
	switch m.inputMode {
	case inputSensitivity:
		if k, err := strconv.ParseFloat(val, 64); err == nil {
			if k < 0.1 {
				k = 0.1
			}
			if k > 5.0 {
				k = 5.0
			}
			m.sensitivity = k
			m.gsrProc.SetSensitivity(k)
			m.hrProc.SetSensitivity(k)
			m.rrProc.SetSensitivity(k)
		}
	case inputManualL:
		if L, err := strconv.ParseFloat(val, 64); err == nil {
			if L < 0 {
				L = 0
			}
			if L > 100 {
				L = 100
			}
			m.histNext++
			m.history = append(m.history, ScoreRecord{
				N: m.histNext, Time: time.Now(),
				GSR_L: -1, HR_L: -1, RR_L: -1,
				Combined: L, IsForced: true,
			})
			m.combinedL = L
			m.log.Event(channelSystem, "manual_l", "L", L)
			if m.osc != nil {
				m.osc.SendL(L)
			}
		}
	}
	m.inputMode = inputNone
	return m, nil
}

// appendFreshenRow records a baseline-freshen event in the history table.
// When clearHistory is true (OSC /reset, [x] confirmed reset), it wipes the
// existing table first so the new row becomes #1 of a fresh performance.
func (m model) appendFreshenRow(clearHistory bool) model {
	m.lastFreshen = time.Now()
	if clearHistory {
		m.history = nil
		m.histNext = 0
	}
	m.histNext++
	m.history = append(m.history, ScoreRecord{
		N: m.histNext, Time: time.Now(), IsFresh: true,
		GSR_L: -1, HR_L: -1, RR_L: -1,
	})
	return m
}

// ── Score aggregation helpers ─────────────────────────────────────────────────

// startPendingRound begins a new score-aggregation round: clears pending state and
// arms a timeout that finalizes the round even if a channel never reports back
// (muted/disconnected). Called by both the keyboard "i" hotkey and
// InterrogateStartMsg (OSC /interrogate, replay-qlab) — any source that fires
// StartInterrogate() on the processors must also call this, or the resulting
// ScoredMsg events are silently dropped and no history row/OSC L cue is ever sent.
func (m model) startPendingRound() (model, tea.Cmd) {
	m.pendingL = [3]float64{-1, -1, -1}
	m.pendingEstimated = [3]bool{}
	m.pendingActive = true
	m.pendingRound++
	return m, scoreTimeoutCmd(m.pendingRound)
}

func (m model) allScoredOrInactive() bool {
	for i, L := range m.pendingL {
		if L >= 0 {
			continue
		}
		// Channel i hasn't reported yet — is it inactive?
		if m.muted[i] {
			continue
		}
		q := m.snaps[i].Quality
		if q == QualityDisconnected || q == QualityStuck || q == QualityOutOfRange {
			continue
		}
		return false
	}
	return true
}

func (m model) finalizeScore() model {
	m.pendingActive = false
	comb := computeCombined(m.snaps, m.muted)

	// pendingL already carries -1 for channels that didn't score.
	gsrL := m.pendingL[0]
	hrL := m.pendingL[1]
	rrL := m.pendingL[2]

	m.histNext++
	m.history = append(m.history, ScoreRecord{
		N:        m.histNext,
		Time:     time.Now(),
		GSR_L:    gsrL,
		HR_L:     hrL,
		RR_L:     rrL,
		Combined: comb,
		GSR_Est:  m.pendingEstimated[0],
		HR_Est:   m.pendingEstimated[1],
		RR_Est:   m.pendingEstimated[2],
	})
	m.combinedL = comb
	m.pendingL = [3]float64{-1, -1, -1}
	m.pendingEstimated = [3]bool{}
	if m.osc != nil {
		m.osc.SendL(comb)
	}
	return m
}

// computeCombined computes the weighted combined L across non-muted, non-disconnected channels.
func computeCombined(snaps [3]ChannelSnapshot, muted [3]bool) float64 {
	sc := getScoringCfg()
	weights := [3]float64{sc.WeightGSR, sc.WeightHR, sc.WeightRR}
	total, denom := 0.0, 0.0
	for i, s := range snaps {
		if muted[i] {
			continue
		}
		if s.Quality == QualityDisconnected || s.Quality == QualityStuck || s.Quality == QualityOutOfRange {
			continue
		}
		total += s.L * weights[i]
		denom += weights[i]
	}
	if denom == 0 {
		return 0
	}
	return total / denom
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	var content string
	switch m.view {
	case viewLog:
		content = renderLogView(m)
	case viewHelp:
		content = renderHelp(m)
	default:
		dash := renderDashboard(m)
		if m.inputMode != inputNone {
			content = renderModal(m, dash)
		} else {
			content = dash
		}
	}
	// Pad to terminal height so Bubble Tea clears ghost lines on resize.
	if m.height > 0 {
		lines := strings.Count(content, "\n") + 1
		if lines < m.height {
			content += strings.Repeat("\n", m.height-lines)
		}
	}
	return content
}

func renderDashboard(m model) string {
	sections := []string{
		renderHeader(m),
		"",
		renderEvent(m),
		"",
		renderLBar(m),
		"",
		renderChannels(m),
		"",
		renderHistory(m),
		"",
		renderStatus(m),
		"",
		renderFooter(m),
	}
	return strings.Join(sections, "\n")
}

func renderFooter(m model) string {
	accent := lipgloss.NewStyle().Foreground(colPurple)
	dim := lipgloss.NewStyle().Foreground(colText)

	qStyle := accent
	if m.quitArmed {
		qStyle = lipgloss.NewStyle().Foreground(colRed)
	}
	resetStyle := lipgloss.NewStyle().Foreground(colRed)

	type item struct {
		key, label string
		style      lipgloss.Style
	}
	items := []item{
		{"c", "alibrate", accent},
		{"i", "nterrogate", accent},
		{"s", "ensitivity", accent},
		{"b", "aseline", accent},
		{"m", "anual-L", accent},
		{"r", "andom-low", accent},
		{"u", "mute", accent},
		{"l", "og", accent},
		{"x", " reset", resetStyle},
		{"?", "help", accent},
		{"q", "uit", qStyle},
	}

	const cols = 3

	cells := make([]string, len(items))
	cellW := 0
	for i, it := range items {
		cells[i] = it.style.Render("["+it.key+"]") + dim.Render(it.label)
		if w := lipgloss.Width(cells[i]); w > cellW {
			cellW = w
		}
	}
	cellW += 2 // gutter between columns

	var lines []string
	for i := 0; i < len(cells); i += cols {
		var row strings.Builder
		row.WriteString("  ")
		for c := 0; c < cols && i+c < len(cells); c++ {
			cell := cells[i+c]
			row.WriteString(cell)
			if c < cols-1 && i+c+1 < len(cells) {
				pad := cellW - lipgloss.Width(cell)
				row.WriteString(strings.Repeat(" ", pad))
			}
		}
		lines = append(lines, row.String())
	}
	out := strings.Join(lines, "\n")
	if m.quitArmed {
		out += "\n  " + lipgloss.NewStyle().Foreground(colRed).Render("↳ press [q] again to quit")
	}
	return out
}
