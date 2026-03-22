package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// CLI styles for styled terminal output.
// Uses the doomsday apocalyptic color palette from internal/tui/theme.go.
var cliStyles = struct {
	Brand   lipgloss.Style
	Tagline lipgloss.Style
	Error   lipgloss.Style
	Success lipgloss.Style
	Warning lipgloss.Style
	Muted   lipgloss.Style
	Label   lipgloss.Style
	Value   lipgloss.Style
	Header  lipgloss.Style
	Section lipgloss.Style
}{
	Brand:   lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B35")).Bold(true),
	Tagline: lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A574")).Italic(true),
	Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Bold(true),
	Success: lipgloss.NewStyle().Foreground(lipgloss.Color("#4ADE80")),
	Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24")),
	Muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("#6B6560")),
	Label:   lipgloss.NewStyle().Foreground(lipgloss.Color("#A89F91")).Width(18),
	Value:   lipgloss.NewStyle().Foreground(lipgloss.Color("#E8E0D8")),
	Header:  lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B35")).Bold(true).MarginTop(1),
	Section: lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A574")).Bold(true),
}

// banner is the ASCII art displayed when the root command runs with no args.
const banner = `
  ____   ___   ___  __  __ ____  ____    _ __   __
 |  _ \ / _ \ / _ \|  \/  / ___||  _ \  / \\ \ / /
 | | | | | | | | | | |\/| \___ \| | | |/ _ \\ V /
 | |_| | |_| | |_| | |  | |___) | |_| / ___ \| |
 |____/ \___/ \___/|_|  |_|____/|____/_/   \_\_|
`

// statusDot returns a colored dot for status indicators.
func statusDot(ok bool) string {
	if ok {
		return cliStyles.Success.Render("●")
	}
	return cliStyles.Error.Render("●")
}

// statusLabel returns a colored status word.
func statusLabel(ok bool, yesText, noText string) string {
	if ok {
		return cliStyles.Success.Render(yesText)
	}
	return cliStyles.Error.Render(noText)
}

// kv renders a label: value pair with consistent styling.
func kv(label, value string) string {
	return fmt.Sprintf("  %s %s", cliStyles.Label.Render(label), cliStyles.Value.Render(value))
}

// sectionHeader renders a section header with a line.
func sectionHeader(title string) string {
	styled := cliStyles.Section.Render("── " + title + " ")
	line := cliStyles.Muted.Render(strings.Repeat("─", max(0, 50-len(title))))
	return styled + line
}
