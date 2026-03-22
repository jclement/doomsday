package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jclement/doomsday/internal/tui"
	"github.com/jclement/doomsday/internal/whimsy"
)

// ProgressModal is a centered overlay shown during backup and restore operations.
type ProgressModal struct {
	styles  tui.Styles
	theme   tui.Theme
	spinner spinner.Model
	fileBar progress.Model
	byteBar progress.Model

	active     bool
	complete   bool
	opType     string // "Backup" or "Restore"
	configName string
	phase      BackupPhase
	cancelFunc context.CancelFunc

	// Progress
	filesTotal, filesProcessed int64
	bytesTotal, bytesProcessed int64
	bytesPerSecond             float64
	startTime                  time.Time
	endTime                    time.Time
	eta                        time.Duration
	currentFile                string

	// Completion summary
	summary *BackupProgress
	err     error

	// Activity log
	logLines []string

	// Whimsy
	message string
}

// NewProgressModal creates a progress modal.
func NewProgressModal(styles tui.Styles, theme tui.Theme) ProgressModal {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(theme.Colors.Primary)),
	)

	fp := progress.New(
		progress.WithScaledGradient(
			string(theme.Colors.ProgressStart),
			string(theme.Colors.ProgressEnd),
		),
		progress.WithWidth(40),
		progress.WithoutPercentage(),
	)

	bp := progress.New(
		progress.WithScaledGradient(
			string(theme.Colors.ProgressStart),
			string(theme.Colors.ProgressEnd),
		),
		progress.WithWidth(40),
		progress.WithoutPercentage(),
	)

	return ProgressModal{
		styles:  styles,
		theme:   theme,
		spinner: s,
		fileBar: fp,
		byteBar: bp,
	}
}

// Show activates the modal for a backup or restore operation.
func (m *ProgressModal) Show(opType, configName string, cancel context.CancelFunc) {
	m.active = true
	m.complete = false
	m.opType = opType
	m.configName = configName
	m.cancelFunc = cancel
	m.phase = BackupPhaseScanning
	m.filesTotal = 0
	m.filesProcessed = 0
	m.bytesTotal = 0
	m.bytesProcessed = 0
	m.bytesPerSecond = 0
	m.startTime = time.Now()
	m.eta = 0
	m.currentFile = ""
	m.summary = nil
	m.err = nil
	m.logLines = nil

	if opType == "Backup" {
		m.message = whimsy.BackupStart()
	} else {
		m.message = whimsy.RestoreStart()
	}
}

// Dismiss hides the modal.
func (m *ProgressModal) Dismiss() {
	m.active = false
	m.cancelFunc = nil
}

// IsActive returns true if the modal is showing.
func (m *ProgressModal) IsActive() bool {
	return m.active
}

// IsComplete returns true if the operation finished (success or error).
func (m *ProgressModal) IsComplete() bool {
	return m.complete
}

// SetProgress updates from a BackupProgressMsg.
func (m *ProgressModal) SetProgress(p BackupProgress) {
	m.phase = p.Phase
	m.filesTotal = p.FilesTotal
	m.filesProcessed = p.FilesProcessed
	m.bytesTotal = p.BytesTotal
	m.bytesProcessed = p.BytesProcessed
	m.bytesPerSecond = p.BytesPerSecond
	m.eta = p.ETA
	if p.FilesCurrent != "" {
		m.currentFile = p.FilesCurrent
	}
}

// SetRestoreProgress updates from a RestoreProgressMsg.
func (m *ProgressModal) SetRestoreProgress(ev RestoreProgressMsg) {
	m.phase = BackupPhaseProcessing
	m.filesProcessed = ev.Event.FilesCompleted
	m.filesTotal = ev.Event.FilesTotal
	m.bytesProcessed = ev.Event.BytesWritten
	m.bytesTotal = ev.Event.TotalBytes
	if ev.Event.Path != "" {
		m.currentFile = ev.Event.Path
	}

	elapsed := time.Since(m.startTime).Seconds()
	if elapsed > 0 {
		m.bytesPerSecond = float64(m.bytesProcessed) / elapsed
	}
	if m.filesTotal > 0 && m.filesProcessed > 0 {
		pct := float64(m.filesProcessed) / float64(m.filesTotal)
		if pct > 0 {
			remaining := elapsed / pct * (1 - pct)
			m.eta = time.Duration(remaining * float64(time.Second))
		}
	}
}

// SetComplete marks the operation as successfully completed.
func (m *ProgressModal) SetComplete(summary *BackupProgress) {
	m.complete = true
	m.endTime = time.Now()
	m.summary = summary
	m.err = nil
	m.phase = BackupPhaseComplete
	if m.opType == "Restore" {
		m.message = whimsy.RestoreComplete()
	} else {
		m.message = whimsy.BackupComplete()
	}
}

// SetError marks the operation as failed.
func (m *ProgressModal) SetError(err error) {
	m.complete = true
	m.endTime = time.Now()
	m.err = err
	m.phase = BackupPhaseError
}

// AppendLog adds a line to the activity log.
func (m *ProgressModal) AppendLog(line string) {
	m.logLines = append(m.logLines, line)
	if len(m.logLines) > 200 {
		m.logLines = m.logLines[len(m.logLines)-200:]
	}
}

// Init returns initial commands for the modal's spinner.
func (m ProgressModal) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update handles input and animation ticks.
func (m ProgressModal) Update(msg tea.Msg) (ProgressModal, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if m.complete {
				m.Dismiss()
			} else if m.cancelFunc != nil {
				m.cancelFunc()
				m.SetError(fmt.Errorf("cancelled by user"))
			}
			return m, nil

		case "enter":
			if m.complete {
				m.Dismiss()
			}
			return m, nil
		}
		// Suppress all other keys while modal is active.
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		fpModel, fpCmd := m.fileBar.Update(msg)
		if fp, ok := fpModel.(progress.Model); ok {
			m.fileBar = fp
		}
		bpModel, bpCmd := m.byteBar.Update(msg)
		if bp, ok := bpModel.(progress.Model); ok {
			m.byteBar = bp
		}
		return m, tea.Batch(fpCmd, bpCmd)
	}

	return m, nil
}

// View renders the progress modal as a centered overlay.
func (m ProgressModal) View(width, height int) string {
	if !m.active {
		return ""
	}

	var content string
	if m.complete && m.err != nil {
		content = m.renderError()
	} else if m.complete {
		content = m.renderComplete()
	} else {
		content = m.renderProgress()
	}

	boxW := min(76, width-4)

	// Adjust progress bar widths to fit.
	barW := min(boxW-30, 50)
	m.fileBar.Width = barW
	m.byteBar.Width = barW

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Colors.Primary).
		Padding(1, 3).
		Width(boxW).
		Render(content)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// renderProgress renders the in-progress state.
func (m ProgressModal) renderProgress() string {
	var lines []string

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.theme.Colors.Primary).
		Render(m.opType)
	configPart := ""
	if m.configName != "" {
		configPart = m.styles.Muted.Render(" · ") +
			m.styles.Subtitle.Render(m.configName)
	}
	lines = append(lines, title+configPart)

	// Phase + spinner
	phase := m.spinner.View() + " "
	switch m.phase {
	case BackupPhaseScanning:
		phase += m.styles.Bold.Render("Scanning filesystem...")
	case BackupPhaseProcessing:
		phase += m.styles.Bold.Render("Processing files...")
	case BackupPhaseUploading:
		phase += m.styles.Bold.Render("Uploading packs...")
	case BackupPhaseFinalizing:
		phase += m.styles.Bold.Render("Finalizing...")
	default:
		phase += m.styles.Bold.Render("Working...")
	}
	elapsed := ""
	if !m.startTime.IsZero() {
		elapsed = m.styles.Muted.Render("  " + formatDuration(time.Since(m.startTime)))
	}
	lines = append(lines, phase+elapsed)

	// Whimsy
	if m.message != "" {
		lines = append(lines, m.styles.Whimsy.Render("\""+m.message+"\""))
	}

	lines = append(lines, "")

	// File progress bar
	var filePct float64
	if m.filesTotal > 0 {
		filePct = float64(m.filesProcessed) / float64(m.filesTotal)
	}
	fileLabel := m.styles.ProgressLabel.Render("Files  ")
	fileBar := m.fileBar.ViewAs(filePct)
	fileCount := m.styles.ProgressValue.Render(
		fmt.Sprintf(" %s / %s", formatCount(m.filesProcessed), formatCount(m.filesTotal)))
	lines = append(lines, fileLabel+fileBar+fileCount)

	lines = append(lines, "")

	// Byte progress bar
	var bytePct float64
	if m.bytesTotal > 0 {
		bytePct = float64(m.bytesProcessed) / float64(m.bytesTotal)
	}
	byteLabel := m.styles.ProgressLabel.Render("Bytes  ")
	byteBar := m.byteBar.ViewAs(bytePct)
	byteCount := m.styles.ProgressValue.Render(
		fmt.Sprintf(" %s / %s", formatBytes(m.bytesProcessed), formatBytes(m.bytesTotal)))
	lines = append(lines, byteLabel+byteBar+byteCount)

	lines = append(lines, "")

	// Speed + ETA
	var statParts []string
	if m.bytesPerSecond > 0 {
		statParts = append(statParts,
			m.styles.Label.Render("speed: ")+m.styles.ProgressSpeed.Render(formatBytes(int64(m.bytesPerSecond))+"/s"))
	}
	if m.eta > 0 {
		statParts = append(statParts,
			m.styles.Label.Render("eta: ")+m.styles.ProgressETA.Render(formatDuration(m.eta)))
	}
	if len(statParts) > 0 {
		lines = append(lines, strings.Join(statParts, m.styles.Muted.Render("  ")))
	}

	// Current file
	if m.currentFile != "" {
		lines = append(lines,
			m.styles.Label.Render("file: ")+m.styles.Path.Render(truncatePath(m.currentFile, 55)))
	}

	// Activity log (last 6 lines)
	if len(m.logLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.styles.Muted.Render("─── activity ───"))
		visibleLog := m.logLines
		if len(visibleLog) > 6 {
			visibleLog = visibleLog[len(visibleLog)-6:]
		}
		for _, l := range visibleLog {
			if len(l) > 65 {
				l = l[:64] + "~"
			}
			lines = append(lines, m.styles.Muted.Render(l))
		}
	}

	lines = append(lines, "")
	lines = append(lines, m.styles.Muted.Render("esc cancel"))

	return strings.Join(lines, "\n")
}

// renderComplete renders the success state.
func (m ProgressModal) renderComplete() string {
	var lines []string

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.theme.Colors.Primary).
		Render(m.opType + " Complete")
	configPart := ""
	if m.configName != "" {
		configPart = m.styles.Muted.Render(" · ") +
			m.styles.Subtitle.Render(m.configName)
	}
	lines = append(lines, title+configPart)

	if m.message != "" {
		lines = append(lines, m.styles.Whimsy.Render("\""+m.message+"\""))
	}
	lines = append(lines, "")

	lines = append(lines, m.styles.StatusOK.Render("  "+m.opType+" completed successfully"))
	lines = append(lines, "")

	elapsed := m.endTime.Sub(m.startTime)

	if m.summary != nil {
		dur := m.summary.Duration
		if dur == 0 {
			dur = elapsed
		}
		lines = append(lines,
			m.styles.Label.Render("  duration: ")+m.styles.Value.Render(formatDuration(dur)),
			m.styles.Label.Render("  new:      ")+m.styles.Number.Render(formatCount(m.summary.FilesNew))+m.styles.Muted.Render(" files"),
			m.styles.Label.Render("  changed:  ")+m.styles.Number.Render(formatCount(m.summary.FilesChanged))+m.styles.Muted.Render(" files"),
			m.styles.Label.Render("  same:     ")+m.styles.Number.Render(formatCount(m.summary.FilesUnchanged))+m.styles.Muted.Render(" files"),
			m.styles.Label.Render("  added:    ")+m.styles.Number.Render(formatBytes(m.summary.DataAdded))+m.styles.Muted.Render(" new data"),
		)
	} else {
		// Restore or backup without summary
		lines = append(lines,
			m.styles.Label.Render("  duration: ")+m.styles.Value.Render(formatDuration(elapsed)),
			m.styles.Label.Render("  files:    ")+m.styles.Number.Render(formatCount(m.filesProcessed)),
		)
	}

	lines = append(lines, "")
	lines = append(lines, m.styles.Muted.Render("enter dismiss"))

	return strings.Join(lines, "\n")
}

// renderError renders the error state.
func (m ProgressModal) renderError() string {
	var lines []string

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(m.theme.Colors.StatusError).
		Render(m.opType + " Failed")
	configPart := ""
	if m.configName != "" {
		configPart = m.styles.Muted.Render(" · ") +
			m.styles.Subtitle.Render(m.configName)
	}
	lines = append(lines, title+configPart)
	lines = append(lines, "")

	errMsg := "An unknown error occurred."
	if m.err != nil {
		errMsg = m.err.Error()
	}
	lines = append(lines, m.styles.StatusError.Render("  "+errMsg))

	if !m.startTime.IsZero() {
		elapsed := m.endTime.Sub(m.startTime)
		lines = append(lines, "")
		lines = append(lines,
			m.styles.Label.Render("  elapsed: ")+m.styles.Value.Render(formatDuration(elapsed)))
		if m.filesProcessed > 0 {
			lines = append(lines,
				m.styles.Label.Render("  processed: ")+m.styles.Value.Render(
					fmt.Sprintf("%s files, %s",
						formatCount(m.filesProcessed),
						formatBytes(m.bytesProcessed))))
		}
	}

	// Show last few log lines for context
	if len(m.logLines) > 0 {
		lines = append(lines, "")
		visibleLog := m.logLines
		if len(visibleLog) > 4 {
			visibleLog = visibleLog[len(visibleLog)-4:]
		}
		for _, l := range visibleLog {
			if len(l) > 65 {
				l = l[:64] + "~"
			}
			lines = append(lines, m.styles.Muted.Render(l))
		}
	}

	lines = append(lines, "")
	lines = append(lines, m.styles.Muted.Render("enter dismiss"))

	return strings.Join(lines, "\n")
}
