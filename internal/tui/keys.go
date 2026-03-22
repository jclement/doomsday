package tui

import "github.com/charmbracelet/bubbles/key"

// AppKeyMap defines the global keybindings for the application shell.
type AppKeyMap struct {
	// Actions
	Help    key.Binding
	Quit    key.Binding
	Back    key.Binding
	Enter   key.Binding
	Refresh key.Binding
}

// DefaultAppKeyMap returns the default application-level key bindings.
func DefaultAppKeyMap() AppKeyMap {
	return AppKeyMap{
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
	}
}

// ShortHelp returns key bindings for the short help view.
func (k AppKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

// FullHelp returns key bindings grouped for the full help view.
func (k AppKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Enter, k.Back, k.Refresh, k.Help, k.Quit},
	}
}

// NavigationKeyMap provides vim-style up/down navigation.
type NavigationKeyMap struct {
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Top      key.Binding
	Bottom   key.Binding
}

// DefaultNavigationKeyMap returns standard vim-style navigation bindings.
func DefaultNavigationKeyMap() NavigationKeyMap {
	return NavigationKeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("k/up", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("j/down", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "ctrl+u"),
			key.WithHelp("ctrl+u", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "ctrl+d"),
			key.WithHelp("ctrl+d", "page down"),
		),
		Top: key.NewBinding(
			key.WithKeys("home", "g"),
			key.WithHelp("g", "top"),
		),
		Bottom: key.NewBinding(
			key.WithKeys("end", "G"),
			key.WithHelp("G", "bottom"),
		),
	}
}

// ShortHelp returns bindings for short help.
func (k NavigationKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down}
}

// FullHelp returns bindings for full help.
func (k NavigationKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Top, k.Bottom},
	}
}

// DashboardKeyMap defines keybindings for the dashboard/drill-down view.
type DashboardKeyMap struct {
	NavigationKeyMap
	RunBackup key.Binding
}

// DefaultDashboardKeyMap returns default dashboard view bindings.
func DefaultDashboardKeyMap() DashboardKeyMap {
	return DashboardKeyMap{
		NavigationKeyMap: DefaultNavigationKeyMap(),
		RunBackup: key.NewBinding(
			key.WithKeys("b"),
			key.WithHelp("b", "run backup"),
		),
	}
}

// ShortHelp returns dashboard short help bindings.
func (k DashboardKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.RunBackup}
}

// FullHelp returns dashboard full help bindings.
func (k DashboardKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.RunBackup},
	}
}

// SnapshotKeyMap defines keybindings for the snapshot browser view.
type SnapshotKeyMap struct {
	NavigationKeyMap
	Browse  key.Binding
	Restore key.Binding
	Diff    key.Binding
	Filter  key.Binding
}

// DefaultSnapshotKeyMap returns default snapshot view bindings.
func DefaultSnapshotKeyMap() SnapshotKeyMap {
	return SnapshotKeyMap{
		NavigationKeyMap: DefaultNavigationKeyMap(),
		Browse: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "browse files"),
		),
		Restore: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "restore"),
		),
		Diff: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "diff"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
	}
}

// ShortHelp returns snapshot short help bindings.
func (k SnapshotKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Browse, k.Restore}
}

// FullHelp returns snapshot full help bindings.
func (k SnapshotKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Top, k.Bottom},
		{k.Browse, k.Restore, k.Diff, k.Filter},
	}
}
