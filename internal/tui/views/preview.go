package views

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"

	"github.com/jclement/doomsday/internal/tui"
)

// PreviewModel displays a file's contents with syntax highlighting
// as a full-screen modal overlay.
type PreviewModel struct {
	tuiStyles tui.Styles
	theme     tui.Theme

	active    bool
	filename  string
	lines     []string // rendered lines (with ANSI)
	rawLines  int      // total lines in file
	scrollTop int
	width     int
	height    int
	binary    bool
	truncated bool
	fileSize  int64
}

// NewPreviewModel creates a new file preview model.
func NewPreviewModel(tuiStyles tui.Styles, theme tui.Theme) PreviewModel {
	return PreviewModel{
		tuiStyles: tuiStyles,
		theme:     theme,
	}
}

// Show loads file content into the preview pane.
func (m *PreviewModel) Show(filename string, content []byte, fileSize int64) {
	m.active = true
	m.filename = filename
	m.scrollTop = 0
	m.fileSize = fileSize
	m.truncated = len(content) >= 1<<20

	// Binary detection: check first 512 bytes for null bytes
	checkLen := min(len(content), 512)
	m.binary = bytes.ContainsRune(content[:checkLen], 0)

	if m.binary {
		m.lines = []string{fmt.Sprintf("Binary file (%s)", formatBytes(fileSize))}
		m.rawLines = 1
		return
	}

	text := string(content)
	m.rawLines = strings.Count(text, "\n") + 1

	// Syntax highlight
	m.lines = highlightContent(text, filename)
}

// Dismiss closes the preview.
func (m *PreviewModel) Dismiss() {
	m.active = false
	m.lines = nil
}

// IsActive returns true if the preview is showing.
func (m *PreviewModel) IsActive() bool {
	return m.active
}

// ScrollDown scrolls the preview down by n lines.
func (m *PreviewModel) ScrollDown(n int) {
	maxScroll := max(0, len(m.lines)-m.viewableHeight()+2)
	m.scrollTop = min(m.scrollTop+n, maxScroll)
}

// ScrollUp scrolls the preview up by n lines.
func (m *PreviewModel) ScrollUp(n int) {
	m.scrollTop = max(0, m.scrollTop-n)
}

// ScrollToTop scrolls to the top.
func (m *PreviewModel) ScrollToTop() {
	m.scrollTop = 0
}

// ScrollToBottom scrolls to the bottom.
func (m *PreviewModel) ScrollToBottom() {
	m.scrollTop = max(0, len(m.lines)-m.viewableHeight()+2)
}

func (m *PreviewModel) viewableHeight() int {
	// border(2) + title(1) + separator(1) + footer(1) + panel margins(2) + outer margin(2)
	return max(m.height-9, 3)
}

// Render renders the preview as a full-screen modal overlay.
func (m *PreviewModel) Render(width, height int) string {
	if !m.active {
		return ""
	}
	m.width = width
	m.height = height

	// Box dimensions: nearly full screen with small margin
	boxW := min(width-4, width)
	contentW := max(boxW-6, 10) // border (2) + padding (2) + safety margin (2)
	contentH := m.viewableHeight()

	// Title header with scroll indicator
	title := m.filename
	if !m.binary && len(m.lines) > contentH {
		maxScroll := len(m.lines) - contentH
		pct := 0
		if maxScroll > 0 {
			pct = m.scrollTop * 100 / maxScroll
		}
		title += fmt.Sprintf("  %d%%  %d lines", pct, m.rawLines)
	} else if !m.binary {
		title += fmt.Sprintf("  %d lines", m.rawLines)
	}

	titleStyled := lipgloss.NewStyle().
		Foreground(m.theme.Colors.Primary).
		Bold(true).
		Render(title)

	// Build visible content lines, truncated to fit width
	var contentLines []string

	if m.binary {
		contentLines = m.lines
	} else {
		start := m.scrollTop
		end := min(start+contentH, len(m.lines))
		if start < len(m.lines) {
			for _, line := range m.lines[start:end] {
				contentLines = append(contentLines, truncateVisual(line, contentW))
			}
		}
	}

	if m.truncated {
		contentLines = append(contentLines,
			m.tuiStyles.Muted.Render(fmt.Sprintf("--- truncated at 1 MiB (file is %s) ---", formatBytes(m.fileSize))))
	}

	content := strings.Join(contentLines, "\n")

	// Footer hints
	footer := m.tuiStyles.Muted.Render("  j/k scroll · g/G top/bottom · esc close")

	// Assemble: title + separator + content + footer
	sep := m.tuiStyles.Muted.Render(strings.Repeat("─", min(contentW, boxW-4)))
	fullContent := titleStyled + "\n" + sep + "\n" + content + "\n" + footer

	pc := panelColors{
		BorderActive: m.theme.Colors.Primary,
		BorderMuted:  m.theme.Colors.BorderMuted,
		TitleActive:  m.theme.Colors.Primary,
		TitleMuted:   m.theme.Colors.TextMuted,
	}

	box := RenderPanel("Preview", fullContent, boxW, height-2, true, pc)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// truncateVisual truncates a string (which may contain ANSI escapes) to fit
// within maxWidth visible characters. It strips ANSI to count visible width.
func truncateVisual(s string, maxWidth int) string {
	visible := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip ANSI escape sequence
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
					i++
				}
				if i < len(s) {
					i++ // skip terminating letter
				}
			}
			continue
		}
		if s[i] == '\t' {
			visible += 4 // tab = ~4 spaces
		} else {
			visible++
		}
		if visible > maxWidth {
			return s[:i]
		}
		i++
	}
	return s
}

// highlightContent applies syntax highlighting and returns rendered lines.
func highlightContent(text, filename string) []string {
	// Find lexer by filename
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	// Use a terminal-friendly style
	style := styles.Get("monokai")

	// Terminal256 formatter for ANSI output
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		return addLineNumbers(strings.Split(text, "\n"))
	}

	var buf bytes.Buffer
	err = formatter.Format(&buf, style, iterator)
	if err != nil {
		return addLineNumbers(strings.Split(text, "\n"))
	}

	return addLineNumbers(strings.Split(buf.String(), "\n"))
}

// addLineNumbers prepends line numbers to each line.
func addLineNumbers(lines []string) []string {
	// Remove trailing empty line from split
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	width := len(fmt.Sprintf("%d", len(lines)))
	result := make([]string, len(lines))
	for i, line := range lines {
		num := fmt.Sprintf("%*d", width, i+1)
		result[i] = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Render(num) + "  " + line
	}
	return result
}
