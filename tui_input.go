package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)


type inputKind int

const (
	inputNone inputKind = iota
	inputSensitivity
	inputManualL
	inputMute
)

var (
	modalBorderCol = lipgloss.Color("#3b3d52")
	modalBGCol     = lipgloss.Color("#1a1b26")

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(modalBorderCol).
			Background(modalBGCol).
			Padding(1, 2)

	modalTitleStyle = lipgloss.NewStyle().
			Foreground(colPurple).
			Background(modalBGCol).
			Bold(true)

	modalHintStyle = lipgloss.NewStyle().
			Foreground(colDim).
			Background(modalBGCol)
)

// newTextInput creates a styled text input for modal use.
func newTextInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 10
	ti.Width = 30
	ti.Focus()
	return ti
}

// renderModal renders a centered modal dialog on a full-screen dark background.
// The dashboard string is intentionally not used — lipgloss.Place handles centering.
func renderModal(m model, _ string) string {
	var title, prompt, hint string
	switch m.inputMode {
	case inputSensitivity:
		title = "SET SENSITIVITY"
		prompt = "Enter multiplier k — applied to all three channels:"
		hint = fmt.Sprintf("⏎ apply · esc cancel · current ×%.1f · range 0.1–5.0", m.sensitivity)
	case inputManualL:
		title = "MANUAL L INJECTION"
		prompt = "Enter combined L override (0–100) for the current entry:"
		hint = "⏎ apply · esc cancel"
	case inputMute:
		title = "MUTE / UNMUTE CHANNEL"
		prompt = "Press [g] GSR   [h] Heart Rate   [r] Resp Rate"
		hint = "toggles channel mute · esc cancel"
	}

	var body strings.Builder
	body.WriteString(modalTitleStyle.Render(title) + "\n\n")
	body.WriteString(lipgloss.NewStyle().Foreground(colText).Background(modalBGCol).Render(prompt) + "\n\n")
	if m.inputMode != inputMute {
		body.WriteString(m.textInput.View() + "\n\n")
	} else {
		body.WriteString("\n")
	}
	body.WriteString(modalHintStyle.Render(hint))

	modal := modalStyle.Render(body.String())

	// Place the modal centered on a full-screen dark background.
	// This avoids overlaying ANSI-escaped dashboard strings (which breaks escape sequences).
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal,
		lipgloss.WithWhitespaceBackground(colBG))
}
