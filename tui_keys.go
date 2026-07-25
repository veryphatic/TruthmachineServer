package main

import "github.com/charmbracelet/bubbles/key"

// keyMap defines all operator hotkeys. Used by the footer help renderer.
type keyMap struct {
	Calibrate   key.Binding
	Interrogate key.Binding
	Sensitivity key.Binding
	Baseline    key.Binding
	ManualL     key.Binding
	RandomLow   key.Binding
	Mute        key.Binding
	Log         key.Binding
	History     key.Binding
	Reset       key.Binding
	Quit        key.Binding
}

var keys = keyMap{
	Calibrate:   key.NewBinding(key.WithKeys("c"), key.WithHelp("[c]", "calibrate")),
	Interrogate: key.NewBinding(key.WithKeys("i"), key.WithHelp("[i]", "interrogate")),
	Sensitivity: key.NewBinding(key.WithKeys("s"), key.WithHelp("[s]", "sensitivity")),
	Baseline:    key.NewBinding(key.WithKeys("b"), key.WithHelp("[b]", "baseline")),
	ManualL:     key.NewBinding(key.WithKeys("m"), key.WithHelp("[m]", "manual-L")),
	RandomLow:   key.NewBinding(key.WithKeys("r"), key.WithHelp("[r]", "random-low")),
	Mute:        key.NewBinding(key.WithKeys("u"), key.WithHelp("[u]", "mute")),
	Log:         key.NewBinding(key.WithKeys("l"), key.WithHelp("[l]", "log")),
	History:     key.NewBinding(key.WithKeys("h"), key.WithHelp("[h]", "history")),
	Reset:       key.NewBinding(key.WithKeys("x"), key.WithHelp("[x]", "reset")),
	Quit:        key.NewBinding(key.WithKeys("q"), key.WithHelp("[q]", "quit")),
}
