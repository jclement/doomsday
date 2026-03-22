package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View identifies the currently active TUI view.
type View int

const (
	ViewDashboard View = iota
	ViewHelp
)

// viewNames maps view identifiers to display names.
var viewNames = map[View]string{
	ViewDashboard: "Dashboard",
	ViewHelp:      "Help",
}

// ViewName returns the display name for a view.
func ViewName(v View) string {
	if name, ok := viewNames[v]; ok {
		return name
	}
	return "Unknown"
}

// ViewModel is the interface that all view sub-models must implement
// to participate in the app's view switching mechanism.
type ViewModel interface {
	Init() tea.Cmd
	Update(tea.Msg) (ViewModel, tea.Cmd)
	View() string
}

// AppModel is the root Bubble Tea model for the doomsday TUI.
type AppModel struct {
	// Configuration
	keys   AppKeyMap
	styles Styles
	theme  Theme

	// View state
	activeView View
	prevView   View
	helpActive bool

	// Layout
	width  int
	height int
	ready  bool

	// Sub-model update/render delegates.
	viewRenderFunc func(View, int, int) string
	viewUpdateFunc func(View, tea.Msg) tea.Cmd
	viewInitFunc   func() tea.Cmd

	// modalActiveFunc returns true when a modal overlay is active,
	// which suppresses global keybindings (quit, view switching, etc.)
	modalActiveFunc func() bool

	// statusMessageFunc returns a transient status message to show in the bar.
	statusMessageFunc func() string

	// toolbarFunc returns the context-sensitive toolbar for the status bar.
	toolbarFunc func() string

	// dissolveActiveFunc returns true when the dissolve animation is running.
	dissolveActiveFunc func() bool
}

// AppOption is a functional option for configuring the App model.
type AppOption func(*AppModel)

// WithTheme sets the theme for the app.
func WithTheme(theme Theme) AppOption {
	return func(m *AppModel) {
		m.theme = theme
		m.styles = NewStyles(theme)
	}
}

// WithThemeMode sets the theme mode for the app.
func WithThemeMode(mode ThemeMode) AppOption {
	return func(m *AppModel) {
		m.theme = NewTheme(mode)
		m.styles = NewStyles(m.theme)
	}
}

// WithViewRenderFunc sets the function used to render view content.
func WithViewRenderFunc(fn func(View, int, int) string) AppOption {
	return func(m *AppModel) {
		m.viewRenderFunc = fn
	}
}

// WithViewUpdateFunc sets the function used to dispatch updates to views.
func WithViewUpdateFunc(fn func(View, tea.Msg) tea.Cmd) AppOption {
	return func(m *AppModel) {
		m.viewUpdateFunc = fn
	}
}

// WithModalActiveFunc sets a function that returns true when a modal overlay
// is active. When active, global keybindings (quit, view switching) are suppressed.
func WithModalActiveFunc(fn func() bool) AppOption {
	return func(m *AppModel) {
		m.modalActiveFunc = fn
	}
}

// WithStatusMessageFunc sets a function that returns a transient status message
// to display in the status bar.
func WithStatusMessageFunc(fn func() string) AppOption {
	return func(m *AppModel) {
		m.statusMessageFunc = fn
	}
}

// WithViewInitFunc sets the function called during Init to initialize views.
func WithViewInitFunc(fn func() tea.Cmd) AppOption {
	return func(m *AppModel) {
		m.viewInitFunc = fn
	}
}

// WithToolbarFunc sets the function that returns the context-sensitive toolbar.
func WithToolbarFunc(fn func() string) AppOption {
	return func(m *AppModel) {
		m.toolbarFunc = fn
	}
}

// WithDissolveActiveFunc sets a function that returns true during dissolve animation.
func WithDissolveActiveFunc(fn func() bool) AppOption {
	return func(m *AppModel) {
		m.dissolveActiveFunc = fn
	}
}

// NewApp creates a new root TUI application model.
func NewApp(opts ...AppOption) AppModel {
	theme := DefaultTheme()
	styles := NewStyles(theme)

	m := AppModel{
		keys:       DefaultAppKeyMap(),
		styles:     styles,
		theme:      theme,
		activeView: ViewDashboard,
		prevView:   ViewDashboard,
	}

	for _, opt := range opts {
		opt(&m)
	}

	return m
}

// Theme returns the current theme.
func (m AppModel) Theme() Theme {
	return m.theme
}

// Styles returns the current styles.
func (m AppModel) Styles() Styles {
	return m.styles
}

// ActiveView returns the currently active view.
func (m AppModel) ActiveView() View {
	return m.activeView
}

// Width returns the terminal width.
func (m AppModel) Width() int {
	return m.width
}

// Height returns the terminal height.
func (m AppModel) Height() int {
	return m.height
}

// Init implements tea.Model.
func (m AppModel) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.EnterAltScreen}
	if m.viewInitFunc != nil {
		cmds = append(cmds, m.viewInitFunc())
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true

	case tea.KeyMsg:
		// Dissolve animation owns everything except ctrl+c
		if m.dissolveActiveFunc != nil && m.dissolveActiveFunc() {
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			// Fall through to viewUpdateFunc for dissolve tick handling
			break
		}

		// When a modal is active, skip global keys and delegate to views.
		modalActive := m.modalActiveFunc != nil && m.modalActiveFunc()

		// ctrl+c always quits, even in modals
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		if modalActive {
			break
		}

		// Global key handling (only when no modal is active)
		switch {
		case key.Matches(msg, m.keys.Help):
			if m.helpActive {
				m.helpActive = false
				m.activeView = m.prevView
			} else {
				m.prevView = m.activeView
				m.helpActive = true
				m.activeView = ViewHelp
			}
			return m, nil

		case key.Matches(msg, m.keys.Back):
			if m.helpActive {
				m.helpActive = false
				m.activeView = m.prevView
				return m, nil
			}
		}
	}

	// Delegate to view-specific update
	if m.viewUpdateFunc != nil {
		cmd := m.viewUpdateFunc(m.activeView, msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View implements tea.Model.
func (m AppModel) View() string {
	if !m.ready {
		return "\n  Initializing doomsday TUI..."
	}

	var sections []string

	// Main content area
	contentHeight := m.height - 1 // -1 for status bar
	content := m.renderContent(contentHeight)
	sections = append(sections, content)

	// Status bar at the bottom (unless dissolve is active)
	if m.dissolveActiveFunc == nil || !m.dissolveActiveFunc() {
		sections = append(sections, m.renderStatusBar())
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderContent renders the active view's content.
func (m AppModel) renderContent(contentHeight int) string {
	if m.viewRenderFunc != nil {
		return m.viewRenderFunc(m.activeView, m.width, contentHeight)
	}
	return m.styles.Muted.Render("\n  No view content available.\n")
}

// renderStatusBar builds the bottom status bar.
func (m AppModel) renderStatusBar() string {
	statusLine := ""

	// Use toolbar from drill-down if available
	if m.toolbarFunc != nil {
		statusLine = m.toolbarFunc()
	}

	if statusLine == "" {
		// Fallback: basic status bar
		sep := lipgloss.NewStyle().Foreground(m.theme.Colors.BorderMuted).Render(" · ")
		parts := []string{
			m.styles.HelpKey.Render("?") + " " + m.styles.HelpDesc.Render("help"),
			m.styles.HelpKey.Render("q") + " " + m.styles.HelpDesc.Render("quit"),
		}
		statusLine = " " + strings.Join(parts, sep)
	}

	// Show transient status message on the right side if available.
	if m.statusMessageFunc != nil {
		if msg := m.statusMessageFunc(); msg != "" {
			msgRendered := lipgloss.NewStyle().
				Foreground(m.theme.Colors.Accent).
				Bold(true).
				Render(msg)
			statusVisual := lipgloss.Width(statusLine)
			msgVisual := lipgloss.Width(msgRendered)
			gap := max(2, m.width-statusVisual-msgVisual-2)
			statusLine = statusLine + strings.Repeat(" ", gap) + msgRendered
		}
	}

	return lipgloss.NewStyle().
		Foreground(m.theme.Colors.TextMuted).
		MaxWidth(m.width).
		Render(statusLine)
}

// SwitchView changes the active view programmatically.
func (m *AppModel) SwitchView(v View) {
	m.prevView = m.activeView
	m.activeView = v
	m.helpActive = v == ViewHelp
}
