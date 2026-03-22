package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jclement/doomsday/internal/tui"
)

// PasswordModel is a modal overlay for entering the repository password.
type PasswordModel struct {
	styles tui.Styles
	theme  tui.Theme
	input  textinput.Model

	// State
	active   bool
	errMsg   string
	action   PendingAction // what to do after auth
	width    int
	height   int
}

// PendingAction describes what action is queued after authentication.
type PendingAction int

const (
	ActionNone PendingAction = iota
	ActionLoadSnapshots
	ActionRunBackup
	ActionBrowseSnapshot
	ActionRestore
)

// PasswordSubmitMsg is sent when the user submits a password.
type PasswordSubmitMsg struct {
	Password []byte
	Action   PendingAction
}

// PasswordCancelMsg is sent when the user cancels password entry.
type PasswordCancelMsg struct{}

// AuthSuccessMsg is sent when authentication succeeds.
type AuthSuccessMsg struct {
	Action PendingAction
}

// AuthFailMsg is sent when authentication fails.
type AuthFailMsg struct {
	Error  string
	Action PendingAction
}

// NewPasswordModel creates a new password input model.
func NewPasswordModel(styles tui.Styles, theme tui.Theme) PasswordModel {
	ti := textinput.New()
	ti.Placeholder = "password or recovery phrase"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	ti.CharLimit = 512
	ti.Width = 40

	return PasswordModel{
		styles: styles,
		theme:  theme,
		input:  ti,
	}
}

// Show activates the password modal for the given action.
func (m *PasswordModel) Show(action PendingAction) {
	m.active = true
	m.action = action
	m.errMsg = ""
	m.input.Reset()
	m.input.Focus()
}

// Hide deactivates the password modal.
func (m *PasswordModel) Hide() {
	m.active = false
	m.input.Blur()
	m.errMsg = ""
}

// IsActive returns true if the modal is showing.
func (m PasswordModel) IsActive() bool {
	return m.active
}

// SetError displays an error message (e.g., wrong password).
func (m *PasswordModel) SetError(msg string) {
	m.errMsg = msg
	m.input.Reset()
	m.input.Focus()
}

// Update handles input events for the password modal.
func (m PasswordModel) Update(msg tea.Msg) (PasswordModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.Hide()
			return m, func() tea.Msg { return PasswordCancelMsg{} }

		case "enter":
			pw := m.input.Value()
			if pw == "" {
				return m, nil
			}
			action := m.action
			m.input.Blur()
			return m, func() tea.Msg {
				return PasswordSubmitMsg{
					Password: []byte(pw),
					Action:   action,
				}
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the password modal as a centered overlay.
func (m PasswordModel) View(width, height int) string {
	if !m.active {
		return ""
	}

	var lines []string

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.theme.Colors.Primary).
		Render("Enter Repository Password")

	lines = append(lines, title)
	lines = append(lines, "")

	subtitle := m.styles.Muted.Render("Enter your password or 24-word recovery phrase.")
	lines = append(lines, subtitle)
	lines = append(lines, "")

	lines = append(lines, m.input.View())
	lines = append(lines, "")

	if m.errMsg != "" {
		lines = append(lines, m.styles.StatusError.Render(m.errMsg))
		lines = append(lines, "")
	}

	hint := m.styles.Muted.Render("enter to submit  esc to cancel")
	lines = append(lines, hint)

	content := strings.Join(lines, "\n")

	boxW := min(60, width-4)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Colors.Primary).
		Padding(1, 3).
		Width(boxW).
		Render(content)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
