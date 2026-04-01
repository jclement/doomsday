// Package tui implements the Bubble Tea terminal user interface for doomsday.
package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// ThemeMode represents the terminal color scheme.
type ThemeMode int

const (
	// ThemeAuto detects the terminal background color.
	ThemeAuto ThemeMode = iota
	// ThemeDark uses the dark color palette.
	ThemeDark
	// ThemeLight uses the light color palette.
	ThemeLight
)

// ParseThemeMode converts a config string to a ThemeMode.
func ParseThemeMode(s string) ThemeMode {
	switch s {
	case "dark":
		return ThemeDark
	case "light":
		return ThemeLight
	default:
		return ThemeAuto
	}
}

// ColorPalette holds all colors used throughout the TUI.
type ColorPalette struct {
	// Primary brand colors
	Primary   lipgloss.Color
	Secondary lipgloss.Color
	Accent    lipgloss.Color

	// Status colors
	StatusOK      lipgloss.Color
	StatusWarning lipgloss.Color
	StatusError   lipgloss.Color

	// Text colors
	TextPrimary   lipgloss.Color
	TextSecondary lipgloss.Color
	TextMuted     lipgloss.Color
	TextInverse   lipgloss.Color

	// Surface colors
	Surface       lipgloss.Color
	SurfaceAlt    lipgloss.Color
	SurfaceBright lipgloss.Color
	Background    lipgloss.Color

	// Border colors
	Border       lipgloss.Color
	BorderActive lipgloss.Color
	BorderMuted  lipgloss.Color

	// Special purpose
	Highlight lipgloss.Color
	Selection lipgloss.Color

	// Progress bar gradient
	ProgressStart lipgloss.Color
	ProgressEnd   lipgloss.Color
}

// Theme holds the active color palette and metadata.
type Theme struct {
	Mode   ThemeMode
	Colors ColorPalette
	IsDark bool
	Name   string
}

// DarkPalette returns the dark theme color palette.
// Apocalyptic aesthetic: deep reds, ambers, muted greens on dark surfaces.
func DarkPalette() ColorPalette {
	return ColorPalette{
		Primary:   lipgloss.Color("#FF6B35"), // burnt orange
		Secondary: lipgloss.Color("#D4A574"), // dusty gold
		Accent:    lipgloss.Color("#C73E1D"), // deep crimson

		StatusOK:      lipgloss.Color("#4ADE80"), // green
		StatusWarning: lipgloss.Color("#FBBF24"), // amber
		StatusError:   lipgloss.Color("#F87171"), // red

		TextPrimary:   lipgloss.Color("#E8E0D8"), // warm white
		TextSecondary: lipgloss.Color("#A8A09A"), // warm gray
		TextMuted:     lipgloss.Color("#6B6560"), // dim warm gray
		TextInverse:   lipgloss.Color("#1A1614"), // near black

		Surface:       lipgloss.Color("#2A2420"), // dark warm brown
		SurfaceAlt:    lipgloss.Color("#3A3430"), // slightly lighter
		SurfaceBright: lipgloss.Color("#4A4440"), // card surface
		Background:    lipgloss.Color("#1A1614"), // deepest

		Border:       lipgloss.Color("#4A4440"), // subtle border
		BorderActive: lipgloss.Color("#FF6B35"), // active element border
		BorderMuted:  lipgloss.Color("#3A3430"), // barely visible

		Highlight: lipgloss.Color("#FF6B35"), // matches primary
		Selection: lipgloss.Color("#3A3430"), // subtle selection bg

		ProgressStart: lipgloss.Color("#C73E1D"), // crimson
		ProgressEnd:   lipgloss.Color("#FF6B35"), // orange
	}
}

// LightPalette returns the light theme color palette.
func LightPalette() ColorPalette {
	return ColorPalette{
		Primary:   lipgloss.Color("#C73E1D"), // deep crimson
		Secondary: lipgloss.Color("#8B6914"), // dark gold
		Accent:    lipgloss.Color("#FF6B35"), // burnt orange

		StatusOK:      lipgloss.Color("#16A34A"), // green
		StatusWarning: lipgloss.Color("#D97706"), // amber
		StatusError:   lipgloss.Color("#DC2626"), // red

		TextPrimary:   lipgloss.Color("#1A1614"), // near black
		TextSecondary: lipgloss.Color("#5A544E"), // warm gray
		TextMuted:     lipgloss.Color("#8A847E"), // light warm gray
		TextInverse:   lipgloss.Color("#F5F0EB"), // warm white

		Surface:       lipgloss.Color("#F5F0EB"), // warm off-white
		SurfaceAlt:    lipgloss.Color("#EBE6E1"), // slightly darker
		SurfaceBright: lipgloss.Color("#FFFFFF"), // white
		Background:    lipgloss.Color("#FAF8F5"), // lightest

		Border:       lipgloss.Color("#D5D0CB"), // subtle border
		BorderActive: lipgloss.Color("#C73E1D"), // active
		BorderMuted:  lipgloss.Color("#EBE6E1"), // barely visible

		Highlight: lipgloss.Color("#C73E1D"),
		Selection: lipgloss.Color("#EBE6E1"),

		ProgressStart: lipgloss.Color("#C73E1D"),
		ProgressEnd:   lipgloss.Color("#FF6B35"),
	}
}

// detectDarkMode tries to determine if the terminal has a dark background.
// Falls back to dark mode since most terminals are dark.
func detectDarkMode() bool {
	// Check COLORFGBG env (format: "fg;bg" where bg >= 8 is dark)
	if fgbg := os.Getenv("COLORFGBG"); fgbg != "" {
		// If the background value is low (0-6), it's dark
		for i := len(fgbg) - 1; i >= 0; i-- {
			if fgbg[i] == ';' {
				bg := fgbg[i+1:]
				if bg == "0" || bg == "1" || bg == "2" || bg == "3" ||
					bg == "4" || bg == "5" || bg == "6" {
					return true
				}
				if bg == "7" || bg == "15" {
					return false
				}
				break
			}
		}
	}
	// Default to dark -- the vast majority of developer terminals are dark
	return true
}

// NewTheme creates a theme based on the given mode.
func NewTheme(mode ThemeMode) Theme {
	isDark := true
	switch mode {
	case ThemeDark:
		isDark = true
	case ThemeLight:
		isDark = false
	default:
		isDark = detectDarkMode()
	}

	t := Theme{
		Mode:   mode,
		IsDark: isDark,
	}

	if isDark {
		t.Colors = DarkPalette()
		t.Name = "doomsday-dark"
	} else {
		t.Colors = LightPalette()
		t.Name = "doomsday-light"
	}

	return t
}

// DefaultTheme returns the default auto-detect theme.
func DefaultTheme() Theme {
	return NewTheme(ThemeAuto)
}
