package views

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/restore"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/tui"
	"github.com/jclement/doomsday/internal/types"
)

// -- Messages ---------------------------------------------------------------

// SnapshotsLoadedMsg is sent when snapshots finish loading.
type SnapshotsLoadedMsg struct {
	ConfigName string
	Items      []SnapshotItem
	Err        error
}

// BackupProgressMsg is sent periodically during a backup.
type BackupProgressMsg struct {
	Stats backup.Stats
}

// BackupCompleteMsg is sent when a backup finishes.
type BackupCompleteMsg struct {
	ConfigName string
	SnapshotID string
	Summary    BackupProgress
	Err        error
}

// RestoreProgressMsg is sent during restore.
type RestoreProgressMsg struct {
	Event restore.ProgressEvent
}

// RestoreCompleteMsg is sent when restore finishes.
type RestoreCompleteMsg struct {
	Err error
}

// DeleteCompleteMsg is sent when a snapshot delete finishes.
type DeleteCompleteMsg struct {
	ConfigName string
	SnapshotID string
	Err        error
}

// ErrorMsg is a generic error message.
type ErrorMsg struct {
	Error string
}

// StatusMsg displays a transient status message.
type StatusMsg struct {
	Text string
}

// LogMsg appends a line to the backup activity log.
type LogMsg struct {
	Text string
}

// -- Restore prompt ---------------------------------------------------------

// RestorePromptModel handles the restore target directory input.
type RestorePromptModel struct {
	active     bool
	snapshotID string
	configName string
	subPath    string
	input      string
	cursor     int
	overwrite  bool // whether to overwrite existing files
	field      int  // 0 = target dir, 1 = overwrite toggle
}

// -- Delete confirm prompt ---------------------------------------------------

// DeleteConfirmModel handles the snapshot delete confirmation dialog.
type DeleteConfirmModel struct {
	active     bool
	snapshotID string
	configName string
	confirmed  bool // cursor position: false = No, true = Yes
}

// -- TUIApp -----------------------------------------------------------------

// FileContentLoadedMsg is sent when file content finishes loading for preview.
type FileContentLoadedMsg struct {
	Filename string
	Content  []byte
	FileSize int64
	Err      error
}

// TUIApp holds all the view models and wires them to the AppModel.
type TUIApp struct {
	App           tui.AppModel
	drilldown     DrillDownModel
	help          HelpModel
	password      PasswordModel
	restore       RestorePromptModel
	deleteConfirm DeleteConfirmModel
	progress      ProgressModal
	dissolve      DissolveModel
	preview       PreviewModel
	spinner       spinner.Model

	session *Session

	// Backup state
	backupCancel context.CancelFunc
	backupActive bool

	// Status
	statusMsg    string
	statusExpiry time.Time

	// Program reference for sending messages from goroutines
	program *tea.Program
}

// NewTUIApp creates a fully wired TUI application.
func NewTUIApp(cfg *config.Config, configName string) *TUIApp {
	theme := tui.DefaultTheme()
	styles := tui.NewStyles(theme)

	var session *Session
	if cfg != nil {
		session = NewSession(cfg)
	}

	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(theme.Colors.Primary)),
	)

	a := &TUIApp{
		drilldown: NewDrillDownModel(styles, theme, cfg, configName),
		help:      NewHelpModel(styles, theme),
		password:  NewPasswordModel(styles, theme),
		progress:  NewProgressModal(styles, theme),
		dissolve:  NewDissolveModel(styles, theme),
		preview:   NewPreviewModel(styles, theme),
		spinner:   s,
		session:   session,
	}

	a.help.SetViewContext("drilldown", DefaultHelpCategories("drilldown"))

	a.App = tui.NewApp(
		tui.WithTheme(theme),
		tui.WithViewRenderFunc(a.renderView),
		tui.WithViewUpdateFunc(a.updateView),
		tui.WithViewInitFunc(a.initViews),
		tui.WithModalActiveFunc(a.isModalActive),
		tui.WithStatusMessageFunc(a.getStatusMessage),
		tui.WithToolbarFunc(a.getToolbar),
		tui.WithDissolveActiveFunc(a.isDissolveActive),
	)

	return a
}

// SetProgram sets the tea.Program reference for sending messages from goroutines.
func (a *TUIApp) SetProgram(p *tea.Program) {
	a.program = p
}

// Close releases session resources.
func (a *TUIApp) Close() {
	if a.session != nil {
		a.session.Close()
	}
}

// initViews initializes sub-views and tries env-based auth.
func (a *TUIApp) initViews() tea.Cmd {
	cmds := []tea.Cmd{a.progress.Init(), a.spinner.Tick}

	// Try auto-unlock (empty password or env var).
	if a.session != nil && a.session.TryAutoUnlock() {
		lvl := a.drilldown.CurrentLevel()
		if lvl != nil && lvl.kind == LevelSnapshots {
			// Single dest → already at snapshots, load them.
			if dest := a.currentDest(); dest != nil {
				cmds = append(cmds, a.loadSnapshotsCmd(dest.Name))
			}
		}
	}

	return tea.Batch(cmds...)
}

// isModalActive returns true when a modal overlay is intercepting all input.
func (a *TUIApp) isModalActive() bool {
	return a.password.IsActive() || a.restore.active || a.deleteConfirm.active || a.progress.IsActive() || a.dissolve.IsActive() || a.preview.IsActive()
}

// isDissolveActive returns true when the dissolve animation is running.
func (a *TUIApp) isDissolveActive() bool {
	return a.dissolve.IsActive()
}

// getToolbar returns the drill-down toolbar for the status bar.
func (a *TUIApp) getToolbar() string {
	if a.App.ActiveView() == tui.ViewHelp {
		return ""
	}
	if a.preview.IsActive() {
		styles := a.drilldown.styles
		sep := styles.Muted.Render(" · ")
		parts := []string{
			styles.HelpKey.Render("esc") + " " + styles.HelpDesc.Render("close preview"),
			styles.HelpKey.Render("j/k") + " " + styles.HelpDesc.Render("scroll"),
			styles.HelpKey.Render("q") + " " + styles.HelpDesc.Render("quit"),
		}
		return " " + strings.Join(parts, sep)
	}
	return a.drilldown.Toolbar()
}

// -- Rendering ---------------------------------------------------------------

func (a *TUIApp) renderView(v tui.View, width, height int) string {
	// Dissolve animation takes over rendering.
	if a.dissolve.IsActive() {
		return a.dissolve.View()
	}

	// Password modal takes priority.
	if a.password.IsActive() {
		return a.password.View(width, height)
	}

	// Progress modal overlay.
	if a.progress.IsActive() {
		return a.progress.View(width, height)
	}

	// Restore prompt overlay.
	if a.restore.active {
		return a.renderRestorePrompt(width, height)
	}

	// Delete confirm overlay.
	if a.deleteConfirm.active {
		return a.renderDeleteConfirm(width, height)
	}

	switch v {
	case tui.ViewDashboard:
		if a.preview.IsActive() {
			return a.preview.Render(width, height)
		}
		return a.drilldown.Render(width, height)
	case tui.ViewHelp:
		// Update help context based on current drilldown level.
		viewName := "destinations"
		if lvl := a.drilldown.CurrentLevel(); lvl != nil {
			switch lvl.kind {
			case LevelDestinations:
				viewName = "destinations"
			case LevelSnapshots:
				viewName = "snapshots"
			case LevelFiles:
				viewName = "files"
			}
		}
		a.help.SetViewContext(viewName, DefaultHelpCategories(viewName))
		return a.help.Render(width, height)
	default:
		return ""
	}
}

func (a *TUIApp) renderRestorePrompt(width, height int) string {
	styles := a.drilldown.styles
	theme := a.drilldown.theme

	var lines []string

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.Colors.Primary).
		Render("Restore Snapshot")
	lines = append(lines, title)
	lines = append(lines, "")

	info := styles.Muted.Render(
		fmt.Sprintf("Snapshot: %s", shortID(a.restore.snapshotID)))
	lines = append(lines, info)

	if a.restore.subPath != "" {
		lines = append(lines, styles.Muted.Render(
			fmt.Sprintf("Path: %s", a.restore.subPath)))
	}
	lines = append(lines, "")

	// Target directory field
	dirLabel := styles.Label.Render("Target directory: ")
	if a.restore.field == 0 {
		dirLabel = lipgloss.NewStyle().Foreground(theme.Colors.Primary).Bold(true).Render("Target directory: ")
	}
	input := a.restore.input
	if a.restore.field == 0 && a.restore.cursor <= len(input) {
		input = input[:a.restore.cursor] + "_" + input[a.restore.cursor:]
	}
	lines = append(lines, dirLabel+styles.Value.Render(input))
	lines = append(lines, "")

	// Overwrite toggle
	owLabel := styles.Label.Render("Overwrite existing: ")
	if a.restore.field == 1 {
		owLabel = lipgloss.NewStyle().Foreground(theme.Colors.Primary).Bold(true).Render("Overwrite existing: ")
	}
	owVal := "No"
	if a.restore.overwrite {
		owVal = "Yes"
	}
	owStyle := styles.Value
	if a.restore.field == 1 {
		owStyle = lipgloss.NewStyle().Foreground(theme.Colors.Primary).Bold(true)
	}
	lines = append(lines, owLabel+owStyle.Render(owVal))
	lines = append(lines, "")

	hint := styles.Muted.Render("tab switch field · space toggle · enter start · esc cancel")
	lines = append(lines, hint)

	content := strings.Join(lines, "\n")
	boxW := min(70, width-4)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Colors.Primary).
		Padding(1, 3).
		Width(boxW).
		Render(content)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// -- Update ------------------------------------------------------------------

func (a *TUIApp) updateView(v tui.View, msg tea.Msg) tea.Cmd {
	// Handle dissolve animation ticks.
	if a.dissolve.IsActive() {
		return a.dissolve.Update(msg)
	}

	// Handle global messages first.
	switch msg := msg.(type) {
	case PasswordSubmitMsg:
		return a.handlePasswordSubmit(msg)

	case PasswordCancelMsg:
		action := a.password.action
		a.password.Hide()
		// If we pushed a level before showing the password dialog, undo it.
		if action == ActionLoadSnapshots {
			a.drilldown.Pop()
		}
		return nil

	case AuthSuccessMsg:
		a.password.Hide()
		return a.handleAuthSuccess(msg.Action)

	case AuthFailMsg:
		a.password.SetError(msg.Error)
		return nil

	case SnapshotsLoadedMsg:
		return a.handleSnapshotsLoaded(msg)

	case BackupProgressMsg:
		a.handleBackupProgress(msg)
		return nil

	case BackupCompleteMsg:
		return a.handleBackupComplete(msg)

	case TreeLoadedMsg:
		a.handleTreeLoaded(msg)
		return nil

	case RestoreCompleteMsg:
		a.handleRestoreComplete(msg)
		return nil

	case DeleteCompleteMsg:
		return a.handleDeleteComplete(msg)

	case ErrorMsg:
		a.setStatus(msg.Error, 10*time.Second)
		if a.progress.IsActive() {
			a.progress.AppendLog("ERROR " + msg.Error)
		}
		return nil

	case LogMsg:
		if a.progress.IsActive() {
			a.progress.AppendLog(msg.Text)
		}
		return nil

	case FileContentLoadedMsg:
		a.handleFileContentLoaded(msg)
		return nil

	case RestoreProgressMsg:
		a.handleRestoreProgress(msg)
		return nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return cmd

	}

	// Password modal intercepts all input.
	if a.password.IsActive() {
		var cmd tea.Cmd
		a.password, cmd = a.password.Update(msg)
		return cmd
	}

	// Progress modal intercepts all input.
	if a.progress.IsActive() {
		var cmd tea.Cmd
		a.progress, cmd = a.progress.Update(msg)
		return cmd
	}

	// Restore prompt intercepts all input.
	if a.restore.active {
		return a.handleRestorePromptInput(msg)
	}

	// Delete confirm intercepts all input.
	if a.deleteConfirm.active {
		return a.handleDeleteConfirmInput(msg)
	}

	// View-specific update.
	switch v {
	case tui.ViewDashboard:
		return a.handleDrillDownUpdate(msg)

	case tui.ViewHelp:
		var cmd tea.Cmd
		a.help, cmd = a.help.Update(msg)
		return cmd
	}

	return nil
}

func (a *TUIApp) handleDrillDownUpdate(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		lvl := a.drilldown.CurrentLevel()
		if lvl == nil {
			return nil
		}

		key := msg.String()

		// When preview is active, it captures navigation
		if a.preview.IsActive() {
			return a.handlePreviewInput(key)
		}

		// Handle 'q' for quit with dissolve
		if key == "q" && !a.drilldown.IsFiltering() {
			return a.startDissolve()
		}

		// Navigation keys
		switch key {
		case "j", "down":
			a.drilldown.CursorDown()
			return nil

		case "k", "up":
			a.drilldown.CursorUp()
			return nil

		case "g", "home":
			a.drilldown.CursorTop()
			return nil

		case "G", "end":
			a.drilldown.CursorBottom()
			return nil

		case "ctrl+d", "pgdown":
			a.drilldown.PageDown(20)
			return nil

		case "ctrl+u", "pgup":
			a.drilldown.PageUp(20)
			return nil

		case "enter":
			return a.handleEnter()

		case "backspace":
			// If filtering, remove last filter char
			if a.drilldown.BackspaceFilter() {
				return nil
			}
			// In file browser: backspace only handles directory navigation
			if lvl.kind == LevelFiles {
				if a.drilldown.FileCanAscend() {
					treeID := a.drilldown.FileAscend()
					lvl.title = a.drilldown.fileLevelTitle()
					return a.loadTreeCmd(a.drilldown.SelectedConfigName(), treeID)
				}
				// At root of file browser: pop column to go back
				a.drilldown.Pop()
				return nil
			}
			// Other levels: pop column
			a.drilldown.Pop()
			return nil

		case "esc":
			// Clear filter first
			if a.drilldown.ClearFilter() {
				return nil
			}
			// Always pop column immediately
			if a.drilldown.Depth() > 1 {
				a.drilldown.Pop()
			}
			return nil

		case "b":
			if !a.drilldown.IsFiltering() && lvl.kind == LevelDestinations {
				name := a.drilldown.SelectedConfigName()
				if name != "" {
					return a.requireAuth(ActionRunBackup)
				}
			}

		case "r":
			if !a.drilldown.IsFiltering() && (lvl.kind == LevelSnapshots || lvl.kind == LevelFiles) {
				return a.handleRestore()
			}

		case "d":
			if !a.drilldown.IsFiltering() && lvl.kind == LevelSnapshots {
				return a.handleDeleteSnapshot()
			}
		}

		// Printable characters → filter
		if len(key) == 1 && key != "?" && key != "q" && key != "b" && key != "r" && key != "d" && key != "/" {
			r, _ := utf8.DecodeRuneInString(key)
			if r >= ' ' && r != utf8.RuneError {
				a.drilldown.ApplyFilterChar(key)
				return nil
			}
		}
	}

	return nil
}

func (a *TUIApp) handlePreviewInput(key string) tea.Cmd {
	switch key {
	case "esc", "enter", "backspace":
		a.preview.Dismiss()
	case "j", "down":
		a.preview.ScrollDown(1)
	case "k", "up":
		a.preview.ScrollUp(1)
	case "ctrl+d", "pgdown":
		a.preview.ScrollDown(20)
	case "ctrl+u", "pgup":
		a.preview.ScrollUp(20)
	case "g", "home":
		a.preview.ScrollToTop()
	case "G", "end":
		a.preview.ScrollToBottom()
	case "q":
		a.preview.Dismiss()
		return a.startDissolve()
	}
	return nil
}

func (a *TUIApp) handleEnter() tea.Cmd {
	lvl := a.drilldown.CurrentLevel()
	if lvl == nil {
		return nil
	}
	item := lvl.selectedItem()
	if item == nil {
		return nil
	}

	switch lvl.kind {
	case LevelDestinations:
		if item.dest == nil {
			return nil
		}
		snapLevel := BuildSnapshotsLevel(item.dest)
		a.drilldown.Push(snapLevel)
		return a.requireAuthForSnapshots(item.dest.Name)

	case LevelSnapshots:
		if item.snap == nil {
			return nil
		}
		return a.startBrowse(item.snap.ID, a.drilldown.SelectedConfigName())

	case LevelFiles:
		if a.drilldown.FileCanDescend() {
			treeID := a.drilldown.FileDescend()
			lvl.title = a.drilldown.fileLevelTitle()
			return a.loadTreeCmd(a.drilldown.SelectedConfigName(), treeID)
		}
		// Enter on a file → preview
		return a.openFilePreview()
	}

	return nil
}

func (a *TUIApp) handleRestore() tea.Cmd {
	lvl := a.drilldown.CurrentLevel()
	if lvl == nil {
		return nil
	}

	var snapshotID, configName, subPath string

	switch lvl.kind {
	case LevelSnapshots:
		item := lvl.selectedItem()
		if item == nil || item.snap == nil {
			return nil
		}
		snapshotID = item.snap.ID
		configName = a.drilldown.SelectedConfigName()

	case LevelFiles:
		snapshotID = a.drilldown.SelectedSnapshotID()
		configName = a.drilldown.SelectedConfigName()
		subPath = a.drilldown.FileSelectedPath()
	}

	if snapshotID == "" || configName == "" {
		return nil
	}

	return a.startRestore(snapshotID, configName, subPath)
}

func (a *TUIApp) startDissolve() tea.Cmd {
	// Use drilldown's stored dimensions (set every render cycle).
	// We can't use a.App.Width()/Height() because a.App is the initial copy;
	// bubbletea maintains its own copy that receives WindowSizeMsg.
	w := a.drilldown.width
	h := a.drilldown.height
	if w == 0 || h == 0 {
		return tea.Quit
	}

	screen := a.drilldown.Render(w, h)
	return a.dissolve.Start(stripANSI(screen), w, h)
}

func (a *TUIApp) handleRestorePromptInput(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		switch key {
		case "esc":
			a.restore.active = false
			return nil

		case "enter":
			target := a.restore.input
			if target == "" {
				target = "./restore"
			}
			overwrite := a.restore.overwrite
			a.restore.active = false
			return a.runRestoreCmd(
				a.restore.configName,
				a.restore.snapshotID,
				a.restore.subPath,
				target,
				overwrite,
			)

		case "tab", "shift+tab":
			// Toggle between fields
			a.restore.field = 1 - a.restore.field
			return nil

		case " ":
			// Space toggles overwrite when on that field
			if a.restore.field == 1 {
				a.restore.overwrite = !a.restore.overwrite
			} else {
				// Space in text field
				a.restore.input = a.restore.input[:a.restore.cursor] + " " + a.restore.input[a.restore.cursor:]
				a.restore.cursor++
			}
			return nil

		case "backspace":
			if a.restore.field == 0 && a.restore.cursor > 0 {
				a.restore.input = a.restore.input[:a.restore.cursor-1] + a.restore.input[a.restore.cursor:]
				a.restore.cursor--
			}

		case "left":
			if a.restore.field == 0 && a.restore.cursor > 0 {
				a.restore.cursor--
			}
		case "right":
			if a.restore.field == 0 && a.restore.cursor < len(a.restore.input) {
				a.restore.cursor++
			}

		default:
			if a.restore.field == 0 && len(key) == 1 {
				a.restore.input = a.restore.input[:a.restore.cursor] + key + a.restore.input[a.restore.cursor:]
				a.restore.cursor++
			}
		}
	}
	return nil
}

// -- Auth --------------------------------------------------------------------

func (a *TUIApp) requireAuth(action PendingAction) tea.Cmd {
	if a.session == nil {
		return func() tea.Msg {
			return ErrorMsg{Error: "No config loaded"}
		}
	}
	if a.session.IsUnlocked() {
		return a.handleAuthSuccess(action)
	}
	a.password.Show(action)
	return a.password.input.Focus()
}

func (a *TUIApp) requireAuthForSnapshots(configName string) tea.Cmd {
	if a.session == nil {
		return func() tea.Msg {
			return ErrorMsg{Error: "No config loaded"}
		}
	}
	if a.session.IsUnlocked() {
		return a.loadSnapshotsCmd(configName)
	}
	a.password.Show(ActionLoadSnapshots)
	return a.password.input.Focus()
}

func (a *TUIApp) handlePasswordSubmit(msg PasswordSubmitMsg) tea.Cmd {
	session := a.session
	action := msg.Action
	pw := msg.Password
	return func() tea.Msg {
		if err := session.Unlock(pw); err != nil {
			return AuthFailMsg{Error: err.Error(), Action: action}
		}
		return AuthSuccessMsg{Action: action}
	}
}

func (a *TUIApp) handleAuthSuccess(action PendingAction) tea.Cmd {
	name := a.drilldown.SelectedConfigName()
	switch action {
	case ActionLoadSnapshots:
		if name != "" {
			return a.loadSnapshotsCmd(name)
		}
	case ActionRunBackup:
		if name != "" {
			return a.runBackupCmd(name)
		}
	case ActionBrowseSnapshot:
		snapshotID := a.drilldown.SelectedSnapshotID()
		if snapshotID != "" && name != "" {
			return a.startBrowseAfterAuth(snapshotID, name)
		}
	case ActionRestore:
		// Handled by restore prompt flow
	}
	return nil
}

// -- Snapshot Loading --------------------------------------------------------

func (a *TUIApp) loadSnapshotsCmd(configName string) tea.Cmd {
	session := a.session
	dest := a.currentDest()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		items, err := session.LoadSnapshots(ctx, dest)
		return SnapshotsLoadedMsg{
			ConfigName: configName,
			Items:      items,
			Err:        err,
		}
	}
}

// currentDest returns the current destination from the drilldown context,
// or falls back to the session's first destination.
func (a *TUIApp) currentDest() *config.DestConfig {
	if dest := a.drilldown.SelectedDest(); dest != nil {
		return dest
	}
	if a.session != nil {
		return a.session.FirstDest()
	}
	return nil
}

func (a *TUIApp) handleSnapshotsLoaded(msg SnapshotsLoadedMsg) tea.Cmd {
	if msg.Err != nil {
		return func() tea.Msg {
			return ErrorMsg{Error: fmt.Sprintf("Load snapshots: %v", msg.Err)}
		}
	}

	// Update snapshot items in the drill-down
	a.drilldown.SetSnapshotItems(msg.Items)

	return nil
}

// -- Backup ------------------------------------------------------------------

func (a *TUIApp) runBackupCmd(configName string) tea.Cmd {
	a.backupActive = true

	session := a.session
	program := a.program
	dest := a.currentDest()

	ctx, cancel := context.WithCancel(context.Background())
	a.backupCancel = cancel
	a.progress.Show("Backup", configName, cancel)

	sendLog := func(format string, args ...any) {
		if program != nil {
			program.Send(LogMsg{Text: fmt.Sprintf(format, args...)})
		}
	}

	return func() tea.Msg {
		defer cancel()

		sendLog("Starting backup for %q", configName)

		cfg := session.Config()
		sendLog("Paths: %v", cfg.SourcePaths())
		if len(cfg.Exclude) > 0 {
			sendLog("Excludes: %v", cfg.Exclude)
		}

		sendLog("Opening repository...")
		if dest == nil {
			return BackupCompleteMsg{ConfigName: configName, Err: fmt.Errorf("no destinations configured")}
		}
		r, err := session.OpenRepo(ctx, dest)
		if err != nil {
			return BackupCompleteMsg{ConfigName: configName, Err: err}
		}

		sendLog("Acquiring lock...")
		lk, err := lock.Acquire(ctx, r.Backend(), r.Keys().SubKeys.Config, lock.Exclusive, "backup")
		if err != nil {
			return BackupCompleteMsg{ConfigName: configName, Err: err}
		}
		defer lk.Release(ctx)
		sendLog("Lock acquired")

		hostname, _ := os.Hostname()
		sendLog("Scanning filesystem on %s...", hostname)
		var lastLogFiles int64
		// Build per-source config from sources.
		perSource := make(map[string]backup.SourceOptions, len(cfg.Sources))
		for _, src := range cfg.Sources {
			p := filepath.Clean(config.ExpandPath(src.Path))
			perSource[p] = backup.SourceOptions{
				Excludes:      src.Exclude,
				OneFilesystem: src.OneFilesystem,
			}
		}

		opts := backup.Options{
			Paths:            cfg.SourcePaths(),
			Excludes:         cfg.Exclude,
			PerSource:        perSource,
			ConfigName:       configName,
			Hostname:         hostname,
			CompressionLevel: cfg.Settings.CompressionLevel,
			OnProgress: func(stats backup.Stats) {
				if program != nil {
					program.Send(BackupProgressMsg{Stats: stats})
				}
				if stats.FilesProcessed > 0 && stats.FilesProcessed-lastLogFiles >= 1000 {
					lastLogFiles = stats.FilesProcessed
					sendLog("Processed %d/%d files, %s read, %d new chunks",
						stats.FilesProcessed, stats.FilesTotal,
						formatBytes(stats.BytesRead), stats.ChunksNew)
				}
			},
		}

		snap, err := backup.Run(ctx, r, opts)
		if err != nil {
			sendLog("ERROR: %v", err)
			return BackupCompleteMsg{ConfigName: configName, Err: err}
		}

		sendLog("Snapshot saved: %s", snap.ID[:12])
		summary := BackupProgress{
			ConfigName: configName,
			Phase:      BackupPhaseComplete,
		}
		if snap.Summary != nil {
			summary.FilesNew = snap.Summary.FilesNew
			summary.FilesChanged = snap.Summary.FilesChanged
			summary.FilesUnchanged = snap.Summary.FilesUnchanged
			summary.DataAdded = snap.Summary.DataAdded
			summary.Duration = snap.Summary.Duration
			sendLog("Complete: %d new, %d changed, %d unchanged, %s added in %s",
				snap.Summary.FilesNew, snap.Summary.FilesChanged,
				snap.Summary.FilesUnchanged, formatBytes(snap.Summary.DataAdded),
				formatDuration(snap.Summary.Duration))
		}

		return BackupCompleteMsg{
			ConfigName: configName,
			SnapshotID: snap.ID,
			Summary:    summary,
		}
	}
}

func (a *TUIApp) handleBackupProgress(msg BackupProgressMsg) {
	bp := StatsToProgress(msg.Stats)
	if a.progress.IsActive() {
		a.progress.SetProgress(bp)
	}
}

func (a *TUIApp) handleBackupComplete(msg BackupCompleteMsg) tea.Cmd {
	a.backupActive = false
	a.backupCancel = nil

	if msg.Err != nil {
		if a.progress.IsActive() {
			a.progress.SetError(msg.Err)
		}
		return nil
	}

	if a.progress.IsActive() {
		a.progress.SetComplete(&msg.Summary)
	}

	// Reload snapshots if we're on the snapshots level for this config
	if a.session != nil && a.session.IsUnlocked() {
		return a.loadSnapshotsCmd(msg.ConfigName)
	}
	return nil
}

// -- File Browser ------------------------------------------------------------

func (a *TUIApp) startBrowse(snapshotID, configName string) tea.Cmd {
	if a.session == nil || !a.session.IsUnlocked() {
		a.password.Show(ActionBrowseSnapshot)
		return a.password.input.Focus()
	}
	return a.startBrowseAfterAuth(snapshotID, configName)
}

func (a *TUIApp) startBrowseAfterAuth(snapshotID, configName string) tea.Cmd {
	filesLevel := BuildFilesLevel(configName, snapshotID)
	a.drilldown.Push(filesLevel)

	session := a.session
	dest := a.currentDest()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		treeID, err := session.SnapshotTreeID(ctx, dest, snapshotID)
		if err != nil {
			return TreeLoadedMsg{Err: err}
		}

		t, err := session.LoadTree(ctx, dest, treeID)
		if err != nil {
			return TreeLoadedMsg{Err: err}
		}

		return TreeLoadedMsg{
			Entries: TreeToEntries(t),
			Path:    "/",
			TreeID:  treeID,
		}
	}
}

func (a *TUIApp) loadTreeCmd(configName string, treeID types.BlobID) tea.Cmd {
	session := a.session
	dest := a.currentDest()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		t, err := session.LoadTree(ctx, dest, treeID)
		if err != nil {
			return TreeLoadedMsg{Err: err}
		}

		return TreeLoadedMsg{Entries: TreeToEntries(t)}
	}
}

func (a *TUIApp) handleTreeLoaded(msg TreeLoadedMsg) {
	if msg.Err != nil {
		// Pop the files level on error
		if a.drilldown.CurrentLevel() != nil && a.drilldown.CurrentLevel().kind == LevelFiles {
			a.drilldown.Pop()
		}
		a.setStatus(fmt.Sprintf("Error loading tree: %v", msg.Err), 10*time.Second)
		return
	}
	// Store the root tree ID so ascending back to "/" works correctly.
	if !msg.TreeID.IsZero() {
		lvl := a.drilldown.CurrentLevel()
		if lvl != nil && lvl.kind == LevelFiles && len(lvl.pathStack) > 0 && lvl.pathStack[0].treeID.IsZero() {
			lvl.pathStack[0].treeID = msg.TreeID
		}
	}
	a.drilldown.SetFileEntries(msg.Entries)
}

// -- File Preview ------------------------------------------------------------

func (a *TUIApp) openFilePreview() tea.Cmd {
	item := a.drilldown.SelectedItem()
	if item == nil || item.entry == nil {
		return nil
	}

	node := item.entry.node
	// Only preview regular files
	if node.Type != tree.NodeTypeFile {
		return nil
	}
	if len(node.Content) == 0 {
		a.setStatus("File has no content", 3*time.Second)
		return nil
	}

	session := a.session
	dest := a.currentDest()
	filename := node.Name
	fileSize := node.Size
	blobIDs := node.Content

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		content, err := session.LoadFileContent(ctx, dest, blobIDs)
		return FileContentLoadedMsg{
			Filename: filename,
			Content:  content,
			FileSize: fileSize,
			Err:      err,
		}
	}
}

func (a *TUIApp) handleFileContentLoaded(msg FileContentLoadedMsg) {
	if msg.Err != nil {
		a.setStatus(fmt.Sprintf("Preview error: %v", msg.Err), 10*time.Second)
		return
	}
	a.preview.Show(msg.Filename, msg.Content, msg.FileSize)
}

// -- Restore -----------------------------------------------------------------

func (a *TUIApp) startRestore(snapshotID, configName, subPath string) tea.Cmd {
	if a.session == nil || !a.session.IsUnlocked() {
		return a.requireAuth(ActionRestore)
	}

	a.restore = RestorePromptModel{
		active:     true,
		snapshotID: snapshotID,
		configName: configName,
		subPath:    subPath,
		input:      "./restore",
		cursor:     len("./restore"),
	}
	return nil
}

func (a *TUIApp) runRestoreCmd(configName, snapshotID, subPath, targetDir string, overwrite bool) tea.Cmd {
	session := a.session
	program := a.program
	dest := a.currentDest()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	a.progress.Show("Restore", configName, cancel)

	sendLog := func(format string, args ...any) {
		if program != nil {
			program.Send(LogMsg{Text: fmt.Sprintf(format, args...)})
		}
	}

	return func() tea.Msg {
		defer cancel()

		sendLog("Restoring from snapshot %s to %s", shortID(snapshotID), targetDir)
		if subPath != "" {
			sendLog("Path filter: %s", subPath)
		}

		sendLog("Opening repository...")
		if dest == nil {
			return RestoreCompleteMsg{Err: fmt.Errorf("no destinations configured")}
		}
		r, err := session.OpenRepo(ctx, dest)
		if err != nil {
			sendLog("ERROR: %v", err)
			return RestoreCompleteMsg{Err: err}
		}

		sendLog("Acquiring lock...")
		lk, err := lock.Acquire(ctx, r.Backend(), r.Keys().SubKeys.Config, lock.Shared, "restore")
		if err != nil {
			sendLog("ERROR: %v", err)
			return RestoreCompleteMsg{Err: err}
		}
		defer lk.Release(ctx)

		var lastLogFiles int64
		opts := restore.Options{
			Overwrite: overwrite,
			OnProgress: func(ev restore.ProgressEvent) {
				if program != nil {
					program.Send(RestoreProgressMsg{Event: ev})
				}
				if ev.FilesCompleted > 0 && ev.FilesCompleted-lastLogFiles >= 100 {
					lastLogFiles = ev.FilesCompleted
					sendLog("Restored %d/%d files", ev.FilesCompleted, ev.FilesTotal)
				}
			},
		}
		if subPath != "" {
			opts.IncludePaths = []string{subPath}
		}

		sendLog("Starting restore...")
		err = restore.Run(ctx, r, snapshotID, targetDir, opts)
		if err != nil {
			sendLog("ERROR: %v", err)
		} else {
			sendLog("Restore complete!")
		}
		return RestoreCompleteMsg{Err: err}
	}
}

func (a *TUIApp) handleRestoreProgress(msg RestoreProgressMsg) {
	ev := msg.Event
	a.setStatus(fmt.Sprintf("Restoring: %d/%d files  %s",
		ev.FilesCompleted, ev.FilesTotal, ev.Path), 2*time.Second)

	if a.progress.IsActive() {
		a.progress.SetRestoreProgress(msg)
	}
}

func (a *TUIApp) handleRestoreComplete(msg RestoreCompleteMsg) {
	if msg.Err != nil {
		a.setStatus(fmt.Sprintf("Restore failed: %v", msg.Err), 10*time.Second)
		if a.progress.IsActive() {
			a.progress.SetError(msg.Err)
		}
	} else {
		a.setStatus("Restore complete!", 10*time.Second)
		if a.progress.IsActive() {
			a.progress.SetComplete(nil)
		}
	}
}

// -- Delete Snapshot ---------------------------------------------------------

func (a *TUIApp) handleDeleteSnapshot() tea.Cmd {
	lvl := a.drilldown.CurrentLevel()
	if lvl == nil || lvl.kind != LevelSnapshots {
		return nil
	}

	item := lvl.selectedItem()
	if item == nil || item.snap == nil {
		return nil
	}

	if a.session == nil || !a.session.IsUnlocked() {
		a.setStatus("Unlock required before deleting snapshots", 5*time.Second)
		return nil
	}

	a.deleteConfirm = DeleteConfirmModel{
		active:     true,
		snapshotID: item.snap.ID,
		configName: a.drilldown.SelectedConfigName(),
		confirmed:  false,
	}
	return nil
}

func (a *TUIApp) renderDeleteConfirm(width, height int) string {
	styles := a.drilldown.styles
	theme := a.drilldown.theme

	var lines []string

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.Colors.StatusError).
		Render("Delete Snapshot")
	lines = append(lines, title)
	lines = append(lines, "")

	info := styles.Muted.Render(
		fmt.Sprintf("Snapshot: %s", shortID(a.deleteConfirm.snapshotID)))
	lines = append(lines, info)
	lines = append(lines, "")

	warning := lipgloss.NewStyle().
		Foreground(theme.Colors.StatusError).
		Render("This will remove the snapshot metadata.")
	lines = append(lines, warning)
	lines = append(lines, styles.Muted.Render("Data blobs will remain until you run prune."))
	lines = append(lines, "")

	// Yes / No toggle
	noStyle := styles.Value
	yesStyle := styles.Value
	if a.deleteConfirm.confirmed {
		yesStyle = lipgloss.NewStyle().Foreground(theme.Colors.StatusError).Bold(true)
	} else {
		noStyle = lipgloss.NewStyle().Foreground(theme.Colors.Primary).Bold(true)
	}

	noStr := noStyle.Render("No")
	yesStr := yesStyle.Render("Yes")
	prompt := styles.Label.Render("Delete? ") + noStr + styles.Muted.Render(" / ") + yesStr
	if a.deleteConfirm.confirmed {
		prompt = styles.Label.Render("Delete? ") + noStr + styles.Muted.Render(" / ") + yesStr
	}
	lines = append(lines, prompt)
	lines = append(lines, "")

	hint := styles.Muted.Render("tab/left/right toggle · enter confirm · esc cancel")
	lines = append(lines, hint)

	content := strings.Join(lines, "\n")
	boxW := min(60, width-4)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Colors.StatusError).
		Padding(1, 3).
		Width(boxW).
		Render(content)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (a *TUIApp) handleDeleteConfirmInput(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		switch key {
		case "esc":
			a.deleteConfirm.active = false
			return nil

		case "enter":
			if !a.deleteConfirm.confirmed {
				// Selected "No"
				a.deleteConfirm.active = false
				return nil
			}
			// Selected "Yes" — run delete
			configName := a.deleteConfirm.configName
			snapshotID := a.deleteConfirm.snapshotID
			a.deleteConfirm.active = false
			return a.runDeleteSnapshotCmd(configName, snapshotID)

		case "tab", "shift+tab", "left", "right", "h", "l":
			a.deleteConfirm.confirmed = !a.deleteConfirm.confirmed
			return nil

		case "y":
			a.deleteConfirm.confirmed = true
			return nil

		case "n":
			a.deleteConfirm.confirmed = false
			return nil
		}
	}
	return nil
}

func (a *TUIApp) runDeleteSnapshotCmd(configName, snapshotID string) tea.Cmd {
	session := a.session
	dest := a.currentDest()
	a.setStatus(fmt.Sprintf("Deleting snapshot %s...", shortID(snapshotID)), 30*time.Second)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := session.ForgetSnapshot(ctx, dest, snapshotID)
		return DeleteCompleteMsg{
			ConfigName: configName,
			SnapshotID: snapshotID,
			Err:        err,
		}
	}
}

func (a *TUIApp) handleDeleteComplete(msg DeleteCompleteMsg) tea.Cmd {
	if msg.Err != nil {
		a.setStatus(fmt.Sprintf("Delete failed: %v", msg.Err), 10*time.Second)
		return nil
	}

	a.setStatus(fmt.Sprintf("Snapshot %s deleted", shortID(msg.SnapshotID)), 5*time.Second)

	// Refresh snapshots list
	if a.session != nil && a.session.IsUnlocked() {
		return a.loadSnapshotsCmd(msg.ConfigName)
	}
	return nil
}

// -- Status & Logging --------------------------------------------------------

func (a *TUIApp) getStatusMessage() string {
	if a.statusMsg != "" && time.Now().Before(a.statusExpiry) {
		return a.statusMsg
	}
	a.statusMsg = ""
	return ""
}

func (a *TUIApp) setStatus(msg string, d time.Duration) {
	a.statusMsg = msg
	a.statusExpiry = time.Now().Add(d)
}

// -- RunTUI ------------------------------------------------------------------

// RunTUI creates and runs the TUI.
func RunTUI(cfg *config.Config, configName string) error {
	app := NewTUIApp(cfg, configName)
	defer app.Close()

	p := tea.NewProgram(app.App, tea.WithAltScreen())
	app.SetProgram(p)

	_, err := p.Run()
	return err
}
