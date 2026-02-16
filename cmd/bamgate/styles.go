package main

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

const (
	// Palette
	colorBlack   = "#1C1E1F"
	colorBgDim   = "#21282C"
	colorBg1     = "#313B42"
	colorBg2     = "#353F46"
	colorBg3     = "#3A444B"
	colorBg4     = "#414B53"
	colorGrayDim = "#55626D"
	colorRed     = "#F76C7C"
	colorOrange  = "#F3A96A"
	colorYellow  = "#E3D367"
	colorGreen   = "#9CD57B"
	colorBlue    = "#78CEE9"
	colorPurple  = "#BAA0F8"
	colorFg      = "#E1E2E3"
	colorGray    = "#82878B"
	colorBgRed   = "#FF6D7E"
	colorBgGreen = "#A2E57B"
	colorBgBlue  = "#7CD5F1"
)

var (
	// Base styles for TUI elements.
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorYellow))
	styleKey     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorBlue)) // Blue for keys (Device:, etc.)
	styleActive  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen))
	styleRevoked = lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed))
)

// customHuhTheme returns a huh theme using our palette.
func customHuhTheme() *huh.Theme {
	t := huh.ThemeDracula() // Start with a dark theme base.

	yellow := lipgloss.Color(colorYellow)
	gray := lipgloss.Color(colorGray)
	fg := lipgloss.Color(colorFg)

	// Base
	t.Focused.Base = t.Focused.Base.BorderForeground(yellow).Foreground(fg)
	t.Blurred.Base = t.Blurred.Base.BorderForeground(gray).Foreground(fg)

	// Title
	t.Focused.Title = t.Focused.Title.Foreground(yellow).Bold(true)
	t.Blurred.Title = t.Blurred.Title.Foreground(gray)

	// Description
	t.Focused.Description = t.Focused.Description.Foreground(lipgloss.Color(colorGray))
	t.Blurred.Description = t.Blurred.Description.Foreground(lipgloss.Color(colorGrayDim))

	// Selection
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(yellow).Bold(true)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(yellow)
	// t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(yellow) // If available in this version

	// TextInput
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(yellow)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(lipgloss.Color(colorGrayDim))

	// Directory
	t.Focused.Directory = t.Focused.Directory.Foreground(yellow)

	// Note: We can't easily change the background color of the form itself without
	// wrapping it in a lipgloss style, but huh handles most of this.

	return t
}
