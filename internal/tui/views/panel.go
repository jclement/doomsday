package views

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// RenderPanel draws a bordered panel with a title in the top border.
// active controls whether the border is highlighted (focused) or dim.
func RenderPanel(title string, content string, width, height int, active bool, colors panelColors) string {
	borderStyle := lipgloss.RoundedBorder()

	borderColor := colors.BorderMuted
	titleColor := colors.TitleMuted
	if active {
		borderColor = colors.BorderActive
		titleColor = colors.TitleActive
	}

	// Build the inner content area.
	// lipgloss Width includes padding but excludes borders.
	// Padding(0,1) adds 1 char on each side horizontally.
	styleWidth := max(width-2, 1)   // subtract 2 for border chars only
	textWidth := max(width-4, 1)    // subtract 2 border + 2 padding = usable text area
	innerHeight := max(height-2, 1) // subtract 2 for top+bottom border

	// Pad or truncate content to fit
	contentLines := strings.Split(content, "\n")

	// Ensure we have exactly innerHeight lines
	for len(contentLines) < innerHeight {
		contentLines = append(contentLines, "")
	}
	if len(contentLines) > innerHeight {
		contentLines = contentLines[:innerHeight]
	}

	// Truncate/pad each line to textWidth
	for i, line := range contentLines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > textWidth {
			contentLines[i] = lipgloss.NewStyle().MaxWidth(textWidth).Render(line)
		}
	}

	body := strings.Join(contentLines, "\n")

	// Render the panel with lipgloss border and padding
	style := lipgloss.NewStyle().
		Border(borderStyle).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(styleWidth).
		Height(innerHeight)

	rendered := style.Render(body)

	// Now replace the top border with one containing the title
	if title != "" {
		lines := strings.Split(rendered, "\n")
		if len(lines) > 0 {
			lines[0] = buildTitleBorder(title, width, borderColor, titleColor, borderStyle)
			rendered = strings.Join(lines, "\n")
		}
	}

	return rendered
}

// buildTitleBorder creates a top border line with an embedded title.
// e.g. "--- Title --------"
func buildTitleBorder(title string, width int, borderColor, titleColor lipgloss.Color, border lipgloss.Border) string {
	titleStr := " " + title + " "
	titleRendered := lipgloss.NewStyle().Foreground(titleColor).Bold(true).Render(titleStr)
	titleVisualWidth := lipgloss.Width(titleRendered)

	borderFg := lipgloss.NewStyle().Foreground(borderColor)

	// Top left corner
	topLeft := borderFg.Render(border.TopLeft)

	// The dash character from the border
	dash := border.Top

	// We need: corner(1) + dash(1) + title + dashes to fill + corner(1)
	// Available space for dashes = width - 2 (corners) - titleVisualWidth
	// But we add one dash before the title
	remainingDashes := width - 2 - titleVisualWidth - 1 // -1 for the dash before title
	if remainingDashes < 0 {
		remainingDashes = 0
	}

	result := topLeft +
		borderFg.Render(dash) +
		titleRendered +
		borderFg.Render(strings.Repeat(dash, remainingDashes)) +
		borderFg.Render(border.TopRight)

	return result
}

// panelColors holds the colors needed for panel rendering.
type panelColors struct {
	BorderActive lipgloss.Color
	BorderMuted  lipgloss.Color
	TitleActive  lipgloss.Color
	TitleMuted   lipgloss.Color
}
