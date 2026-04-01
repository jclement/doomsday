package views

import (
	"math/rand"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jclement/doomsday/internal/tui"
	"github.com/jclement/doomsday/internal/whimsy"
)

// DissolveTickMsg triggers the next frame of the dissolve animation.
type DissolveTickMsg struct{}

// DissolveModel handles the screen dissolve quit animation.
type DissolveModel struct {
	styles tui.Styles
	theme  tui.Theme

	active bool
	cells  []dissolveCell
	width  int
	height int
	tick   int
	done   bool
	rng    *rand.Rand

	farewell string // farewell message
}

type dissolveCell struct {
	original rune
	decayAt  int // tick when this cell starts decaying
	state    int // 0=original, 1=glitch, 2=space
}

var glitchChars = []rune("░▒▓█▄▀╬╫╪┼╳╲╱")

// NewDissolveModel creates a new dissolve animation model.
func NewDissolveModel(styles tui.Styles, theme tui.Theme) DissolveModel {
	return DissolveModel{
		styles: styles,
		theme:  theme,
	}
}

// Start captures the current screen and begins the dissolve animation.
func (m *DissolveModel) Start(screenContent string, width, height int) tea.Cmd {
	m.active = true
	m.done = false
	m.width = width
	m.height = height
	m.tick = 0
	m.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	m.farewell = whimsy.Farewell()

	// Parse screen content into cells
	lines := strings.Split(screenContent, "\n")
	m.cells = nil

	totalFrames := 10 // total animation frames

	for y := 0; y < height; y++ {
		line := ""
		if y < len(lines) {
			line = lines[y]
		}

		x := 0
		for _, r := range line {
			if x >= width {
				break
			}
			// Each cell decays at a random tick, biased slightly top-to-bottom
			bias := float64(y) / float64(max(height, 1)) * 3.0
			decayAt := m.rng.Intn(totalFrames-1) + int(bias)
			if decayAt >= totalFrames {
				decayAt = totalFrames - 1
			}
			m.cells = append(m.cells, dissolveCell{
				original: r,
				decayAt:  decayAt,
			})
			x++
		}
		// Pad rest of line with spaces
		for ; x < width; x++ {
			m.cells = append(m.cells, dissolveCell{
				original: ' ',
				decayAt:  totalFrames, // spaces don't need to decay
			})
		}
	}

	return m.tickCmd()
}

// IsActive returns true if the dissolve animation is running.
func (m *DissolveModel) IsActive() bool {
	return m.active
}

// IsDone returns true if the animation has finished.
func (m *DissolveModel) IsDone() bool {
	return m.done
}

// Update handles tick messages for animation.
func (m *DissolveModel) Update(msg tea.Msg) tea.Cmd {
	if !m.active {
		return nil
	}

	switch msg.(type) {
	case DissolveTickMsg:
		m.tick++

		if m.tick > 12 {
			m.done = true
			return tea.Quit
		}

		// Update cell states
		for i := range m.cells {
			c := &m.cells[i]
			if c.original == ' ' {
				continue
			}
			if m.tick >= c.decayAt && c.state == 0 {
				c.state = 1 // glitch
			}
			if m.tick >= c.decayAt+2 && c.state == 1 {
				c.state = 2 // space
			}
		}

		return m.tickCmd()

	case tea.KeyMsg:
		// Only ctrl+c works during dissolve
		return nil
	}

	return nil
}

// View renders the current dissolve frame.
func (m *DissolveModel) View() string {
	if !m.active {
		return ""
	}

	if m.done {
		// Show farewell message centered
		msg := m.farewell
		if msg == "" {
			msg = "Goodbye."
		}
		styled := lipgloss.NewStyle().
			Foreground(m.theme.Colors.Primary).
			Bold(true).
			Render(msg)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, styled)
	}

	var buf strings.Builder
	for y := 0; y < m.height; y++ {
		if y > 0 {
			buf.WriteRune('\n')
		}
		for x := 0; x < m.width; x++ {
			idx := y*m.width + x
			if idx >= len(m.cells) {
				buf.WriteRune(' ')
				continue
			}
			c := &m.cells[idx]
			switch c.state {
			case 0:
				buf.WriteRune(c.original)
			case 1:
				// Pick a random glitch character
				g := glitchChars[m.rng.Intn(len(glitchChars))]
				buf.WriteRune(g)
			case 2:
				buf.WriteRune(' ')
			}
		}
	}

	return buf.String()
}

func (m *DissolveModel) tickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return DissolveTickMsg{}
	})
}

// stripANSI removes ANSI escape sequences from a string, returning only visible characters.
// This is needed to convert the rendered View() output into individual cells.
func stripANSI(s string) string {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip escape sequence
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
					i++
				}
				if i < len(s) {
					i++ // skip the terminating letter
				}
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		buf.WriteRune(r)
		i += size
	}
	return buf.String()
}
