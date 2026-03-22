package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jclement/doomsday/internal/tui"
)

// HelpCategory represents a group of related key bindings.
type HelpCategory struct {
	Title    string
	Bindings []key.Binding
}

// HelpModel is the Bubble Tea model for the full help reference view.
type HelpModel struct {
	styles tui.Styles
	theme  tui.Theme

	// Current view context
	viewName   string
	categories []HelpCategory

	// Layout
	width  int
	height int
	ready  bool

	// Scroll
	scrollOffset int
	maxScroll    int
}

// NewHelpModel creates a new help reference view.
func NewHelpModel(styles tui.Styles, theme tui.Theme) HelpModel {
	return HelpModel{
		styles: styles,
		theme:  theme,
	}
}

// SetViewContext sets the current view name and updates the help categories.
func (m *HelpModel) SetViewContext(viewName string, categories []HelpCategory) {
	m.viewName = viewName
	m.categories = categories
	m.scrollOffset = 0
}

// DefaultHelpCategories returns the help categories for the drill-down navigation.
func DefaultHelpCategories(viewName string) []HelpCategory {
	appKeys := tui.DefaultAppKeyMap()
	navKeys := tui.DefaultNavigationKeyMap()

	globalCategory := HelpCategory{
		Title: "Global",
		Bindings: []key.Binding{
			appKeys.Help,
			appKeys.Quit,
		},
	}

	navCategory := HelpCategory{
		Title: "Navigation",
		Bindings: []key.Binding{
			navKeys.Up,
			navKeys.Down,
			navKeys.PageUp,
			navKeys.PageDown,
			navKeys.Top,
			navKeys.Bottom,
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "drill in / select")),
			key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "go back")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter / go back")),
		},
	}

	filterCategory := HelpCategory{
		Title: "Filtering",
		Bindings: []key.Binding{
			key.NewBinding(key.WithKeys("a-z"), key.WithHelp("a-z", "type to filter")),
			key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "delete filter char")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		},
	}

	categories := []HelpCategory{globalCategory, navCategory, filterCategory}

	switch viewName {
	case "destinations":
		categories = append(categories, HelpCategory{
			Title: "Destinations",
			Bindings: []key.Binding{
				key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "run backup")),
				key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "view snapshots")),
			},
		})

	case "snapshots":
		categories = append(categories, HelpCategory{
			Title: "Snapshots",
			Bindings: []key.Binding{
				key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restore snapshot")),
				key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete snapshot")),
				key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "browse files")),
			},
		})

	case "files":
		categories = append(categories, HelpCategory{
			Title: "File Browser",
			Bindings: []key.Binding{
				key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open directory")),
				key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "go up")),
				key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restore")),
				key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open/preview")),
			},
		})
	}

	return categories
}

// Init implements tea.Model.
func (m HelpModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m HelpModel) Update(msg tea.Msg) (HelpModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.recalcMaxScroll()

	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if m.scrollOffset < m.maxScroll {
				m.scrollOffset++
			}
		case "k", "up":
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
		case "g", "home":
			m.scrollOffset = 0
		case "G", "end":
			m.scrollOffset = m.maxScroll
		}
	}

	return m, nil
}

// recalcMaxScroll calculates the maximum scroll offset.
func (m *HelpModel) recalcMaxScroll() {
	totalLines := 0
	for _, cat := range m.categories {
		totalLines += 2 + len(cat.Bindings) // title + blank + bindings
	}
	m.maxScroll = max(0, totalLines-m.height+8)
}

// View implements tea.Model.
func (m HelpModel) View() string {
	if !m.ready {
		return "Loading help..."
	}

	var sections []string

	// Header
	title := m.styles.Title.Render("Key Bindings")
	if m.viewName != "" {
		title += m.styles.Muted.Render("  ") +
			m.styles.Subtitle.Render("("+m.viewName+")")
	}
	sections = append(sections, title)
	sections = append(sections, "")

	// Render each category
	for _, cat := range m.categories {
		sections = append(sections, m.renderCategory(cat))
	}

	// Footer hint
	sections = append(sections, "")
	sections = append(sections, m.styles.Muted.Render("Press ? or esc to close help"))

	// Join and apply scrolling
	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	lines := strings.Split(content, "\n")

	// Apply scroll offset
	if m.scrollOffset > 0 && m.scrollOffset < len(lines) {
		lines = lines[m.scrollOffset:]
	}

	// Limit to available height
	viewableHeight := max(m.height-4, 5)
	if len(lines) > viewableHeight {
		lines = lines[:viewableHeight]
	}

	return strings.Join(lines, "\n")
}

// Render renders the help view within a panel of the given dimensions.
func (m HelpModel) Render(width, height int) string {
	// Set dimensions on this value-receiver copy so View() works
	// even if the help model never received a WindowSizeMsg.
	m.width = max(width-4, 10)
	m.height = max(height-4, 5)
	m.ready = true
	m.recalcMaxScroll()

	pc := panelColors{
		BorderActive: m.theme.Colors.BorderActive,
		BorderMuted:  m.theme.Colors.BorderMuted,
		TitleActive:  m.theme.Colors.Primary,
		TitleMuted:   m.theme.Colors.TextMuted,
	}

	content := m.View()
	return RenderPanel("Help", content, width, height, true, pc)
}

// renderCategory renders a single help category with title and bindings.
func (m HelpModel) renderCategory(cat HelpCategory) string {
	var lines []string

	// Category title
	lines = append(lines, m.styles.Heading.Render(cat.Title))

	// Find the max key width for alignment
	maxKeyWidth := 0
	for _, b := range cat.Bindings {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		if len(h.Key) > maxKeyWidth {
			maxKeyWidth = len(h.Key)
		}
	}

	// Render each binding
	for _, b := range cat.Bindings {
		if !b.Enabled() {
			continue
		}
		h := b.Help()

		// Pad key to align descriptions
		keyStr := h.Key
		padding := strings.Repeat(" ", max(0, maxKeyWidth-len(keyStr)+2))

		line := "  " +
			m.styles.HelpKey.Render(keyStr) +
			padding +
			m.styles.HelpDesc.Render(h.Desc)

		lines = append(lines, line)
	}

	lines = append(lines, "") // blank line after category

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
