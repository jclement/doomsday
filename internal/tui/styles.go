package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Styles holds all lipgloss styles used throughout the TUI.
// Built from a Theme so they adapt to dark/light mode.
type Styles struct {
	// Layout
	App       lipgloss.Style
	TabBar    lipgloss.Style
	TabActive lipgloss.Style
	TabItem   lipgloss.Style
	Content   lipgloss.Style
	StatusBar lipgloss.Style

	// Typography
	Title       lipgloss.Style
	Subtitle    lipgloss.Style
	Heading     lipgloss.Style
	Body        lipgloss.Style
	Muted       lipgloss.Style
	Bold        lipgloss.Style
	Code        lipgloss.Style
	Whimsy      lipgloss.Style

	// Status indicators
	StatusOK      lipgloss.Style
	StatusWarning lipgloss.Style
	StatusError   lipgloss.Style
	StatusBadge   lipgloss.Style

	// Cards and containers
	Card         lipgloss.Style
	CardActive   lipgloss.Style
	CardHeader   lipgloss.Style
	Panel        lipgloss.Style
	PanelBorder  lipgloss.Style

	// Data display
	Key         lipgloss.Style
	Value       lipgloss.Style
	Label       lipgloss.Style
	Number      lipgloss.Style
	Timestamp   lipgloss.Style
	Path        lipgloss.Style
	BlobID      lipgloss.Style

	// Interactive
	Selected    lipgloss.Style
	Cursor      lipgloss.Style
	Highlight   lipgloss.Style

	// Table
	TableHeader   lipgloss.Style
	TableCell     lipgloss.Style
	TableSelected lipgloss.Style

	// Backup config items
	ConfigName    lipgloss.Style
	ConfigOK      lipgloss.Style
	ConfigWarning lipgloss.Style
	ConfigError   lipgloss.Style

	// Progress
	ProgressLabel lipgloss.Style
	ProgressValue lipgloss.Style
	ProgressSpeed lipgloss.Style
	ProgressETA   lipgloss.Style

	// Dividers
	Divider       lipgloss.Style

	// Logo / brand
	Logo          lipgloss.Style
	LogoAccent    lipgloss.Style

	// Help
	HelpKey  lipgloss.Style
	HelpDesc lipgloss.Style
	HelpSep  lipgloss.Style
}

// NewStyles creates a complete style set from a theme.
func NewStyles(theme Theme) Styles {
	c := theme.Colors

	return Styles{
		// Layout
		App: lipgloss.NewStyle().
			Padding(1, 2),

		TabBar: lipgloss.NewStyle().
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottomForeground(c.Border).
			Padding(0, 1).
			MarginBottom(1),

		TabActive: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.Primary).
			Padding(0, 1).
			BorderBottom(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderBottomForeground(c.Primary),

		TabItem: lipgloss.NewStyle().
			Foreground(c.TextMuted).
			Padding(0, 1),

		Content: lipgloss.NewStyle().
			Padding(0, 1),

		StatusBar: lipgloss.NewStyle().
			Foreground(c.TextMuted).
			MarginTop(1).
			Padding(0, 1),

		// Typography
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.Primary).
			MarginBottom(1),

		Subtitle: lipgloss.NewStyle().
			Foreground(c.Secondary).
			Italic(true),

		Heading: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.TextPrimary).
			MarginBottom(1),

		Body: lipgloss.NewStyle().
			Foreground(c.TextPrimary),

		Muted: lipgloss.NewStyle().
			Foreground(c.TextMuted),

		Bold: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.TextPrimary),

		Code: lipgloss.NewStyle().
			Foreground(c.Accent),

		Whimsy: lipgloss.NewStyle().
			Foreground(c.Secondary).
			Italic(true).
			Padding(0, 1),

		// Status indicators
		StatusOK: lipgloss.NewStyle().
			Foreground(c.StatusOK),

		StatusWarning: lipgloss.NewStyle().
			Foreground(c.StatusWarning),

		StatusError: lipgloss.NewStyle().
			Foreground(c.StatusError).
			Bold(true),

		StatusBadge: lipgloss.NewStyle().
			Padding(0, 1),

		// Cards and containers
		Card: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.Border).
			Padding(1, 2).
			MarginBottom(1),

		CardActive: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.BorderActive).
			Padding(1, 2).
			MarginBottom(1),

		CardHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.Primary).
			MarginBottom(1),

		Panel: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(c.BorderMuted).
			Padding(1, 2),

		PanelBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.Border).
			Padding(0, 1),

		// Data display
		Key: lipgloss.NewStyle().
			Foreground(c.Secondary).
			Bold(true),

		Value: lipgloss.NewStyle().
			Foreground(c.TextPrimary),

		Label: lipgloss.NewStyle().
			Foreground(c.TextSecondary),

		Number: lipgloss.NewStyle().
			Foreground(c.Accent).
			Bold(true),

		Timestamp: lipgloss.NewStyle().
			Foreground(c.TextSecondary),

		Path: lipgloss.NewStyle().
			Foreground(c.Accent),

		BlobID: lipgloss.NewStyle().
			Foreground(c.TextMuted),

		// Interactive
		Selected: lipgloss.NewStyle().
			Foreground(c.TextInverse).
			Background(c.Selection).
			Bold(true),

		Cursor: lipgloss.NewStyle().
			Foreground(c.Primary).
			Bold(true),

		Highlight: lipgloss.NewStyle().
			Foreground(c.Highlight).
			Bold(true),

		// Table
		TableHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.Secondary).
			Padding(0, 1).
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottomForeground(c.Border),

		TableCell: lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(c.TextPrimary),

		TableSelected: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.Primary).
			Background(c.Selection),

		// Backup config items
		ConfigName: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.TextPrimary),

		ConfigOK: lipgloss.NewStyle().
			Foreground(c.StatusOK),

		ConfigWarning: lipgloss.NewStyle().
			Foreground(c.StatusWarning),

		ConfigError: lipgloss.NewStyle().
			Foreground(c.StatusError).
			Bold(true),

		// Progress
		ProgressLabel: lipgloss.NewStyle().
			Foreground(c.TextSecondary),

		ProgressValue: lipgloss.NewStyle().
			Foreground(c.TextPrimary).
			Bold(true),

		ProgressSpeed: lipgloss.NewStyle().
			Foreground(c.Accent),

		ProgressETA: lipgloss.NewStyle().
			Foreground(c.TextMuted),

		// Dividers
		Divider: lipgloss.NewStyle().
			Foreground(c.Border),

		// Logo / brand
		Logo: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.Primary),

		LogoAccent: lipgloss.NewStyle().
			Bold(true).
			Foreground(c.Accent),

		// Help
		HelpKey: lipgloss.NewStyle().
			Foreground(c.Secondary).
			Bold(true),

		HelpDesc: lipgloss.NewStyle().
			Foreground(c.TextMuted),

		HelpSep: lipgloss.NewStyle().
			Foreground(c.BorderMuted),
	}
}

// Divider returns a horizontal divider string of the given width.
func (s Styles) RenderDivider(width int) string {
	line := ""
	for i := 0; i < width; i++ {
		line += "─"
	}
	return s.Divider.Render(line)
}

// StatusDot returns a colored dot for the given status.
func (s Styles) StatusDot(status string) string {
	switch status {
	case "ok", "green":
		return s.StatusOK.Render("●")
	case "warning", "yellow":
		return s.StatusWarning.Render("●")
	case "error", "red":
		return s.StatusError.Render("●")
	default:
		return s.Muted.Render("○")
	}
}

// StatusText returns styled text with status coloring.
func (s Styles) StatusText(status, text string) string {
	switch status {
	case "ok", "green":
		return s.StatusOK.Render(text)
	case "warning", "yellow":
		return s.StatusWarning.Render(text)
	case "error", "red":
		return s.StatusError.Render(text)
	default:
		return s.Muted.Render(text)
	}
}
