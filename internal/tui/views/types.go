package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
)

// SnapshotItem holds display data for a single snapshot row.
type SnapshotItem struct {
	ID       string
	Time     time.Time
	Hostname string
	Paths    []string
	Tags     []string

	// Summary stats
	TotalFiles     int64
	TotalSize      int64
	DataAdded      int64
	FilesNew       int64
	FilesChanged   int64
	FilesUnchanged int64
	Duration       time.Duration
}

// browserEntry represents a single entry in the file browser.
type browserEntry struct {
	node    tree.Node
	subtree types.BlobID // for directories
}

// pathEntry tracks a position in the directory navigation stack.
type pathEntry struct {
	treeID types.BlobID
	name   string
}

// TreeLoadedMsg is sent when a tree has been loaded from the repo.
type TreeLoadedMsg struct {
	Entries []browserEntry
	Path    string        // e.g., "/" or "/Documents"
	TreeID  types.BlobID  // the tree ID that was loaded (needed for pathStack root)
	Err     error
}

// TreeToEntries converts a tree.Tree to browser entries.
// Directories are listed first, then files, alphabetically within each group.
func TreeToEntries(t *tree.Tree) []browserEntry {
	entries := make([]browserEntry, 0, len(t.Nodes))

	var dirs, files []browserEntry
	for _, n := range t.Nodes {
		e := browserEntry{node: n, subtree: n.Subtree}
		if n.Type == tree.NodeTypeDir {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	entries = append(entries, dirs...)
	entries = append(entries, files...)
	return entries
}

// fileIcon returns a text icon for a node type.
func fileIcon(t tree.NodeType) string {
	switch t {
	case tree.NodeTypeDir:
		return "dir"
	case tree.NodeTypeFile:
		return "file"
	case tree.NodeTypeSymlink:
		return "link"
	case tree.NodeTypeDev:
		return "dev"
	case tree.NodeTypeFIFO:
		return "fifo"
	case tree.NodeTypeSocket:
		return "sock"
	default:
		return "?"
	}
}

// BackupPhase represents the current phase of a backup operation.
type BackupPhase int

const (
	BackupPhaseScanning   BackupPhase = iota
	BackupPhaseProcessing
	BackupPhaseUploading
	BackupPhaseFinalizing
	BackupPhaseComplete
	BackupPhaseError
)

// BackupProgress tracks the progress of a backup operation.
type BackupProgress struct {
	ConfigName     string
	Phase          BackupPhase
	FilesTotal     int64
	FilesProcessed int64
	FilesCurrent   string
	BytesTotal     int64
	BytesProcessed int64
	BytesPerSecond float64
	ETA            time.Duration
	PacksTotal     int
	PacksUploaded  int

	// Summary fields (populated on completion)
	FilesNew       int64
	FilesChanged   int64
	FilesUnchanged int64
	DataAdded      int64
	Duration       time.Duration
}

// StatsToProgress converts backup.Stats to a BackupProgress for the progress modal.
func StatsToProgress(stats backup.Stats) BackupProgress {
	phase := BackupPhaseProcessing
	if stats.FilesProcessed == 0 {
		phase = BackupPhaseScanning
	}
	if stats.PacksFlushed > 0 && stats.FilesProcessed > 0 {
		phase = BackupPhaseUploading
	}

	var speed float64
	if stats.Elapsed > 0 {
		speed = float64(stats.BytesRead) / stats.Elapsed.Seconds()
	}

	var eta time.Duration
	if stats.FilesTotal > 0 && stats.FilesProcessed > 0 {
		pct := float64(stats.FilesProcessed) / float64(stats.FilesTotal)
		if pct > 0 {
			remaining := stats.Elapsed.Seconds() / pct * (1 - pct)
			eta = time.Duration(remaining * float64(time.Second))
		}
	}

	return BackupProgress{
		Phase:          phase,
		FilesTotal:     stats.FilesTotal,
		FilesProcessed: stats.FilesProcessed,
		BytesTotal:     stats.BytesRead + stats.BytesNew,
		BytesProcessed: stats.BytesRead,
		BytesPerSecond: speed,
		ETA:            eta,
		PacksTotal:     int(stats.PacksFlushed + stats.ChunksNew/100),
		PacksUploaded:  int(stats.PacksFlushed),
	}
}

// --- Formatting Helpers ---

// formatRelativeTime returns a human-friendly relative time string.
func formatRelativeTime(t time.Time) string {
	now := time.Now()
	d := now.Sub(t)

	if d < 0 {
		d = -d
		switch {
		case d < time.Minute:
			return "in <1m"
		case d < time.Hour:
			return fmt.Sprintf("in %dm", int(d.Minutes()))
		case d < 24*time.Hour:
			h := int(d.Hours())
			m := int(d.Minutes()) % 60
			if m > 0 {
				return fmt.Sprintf("in %dh%dm", h, m)
			}
			return fmt.Sprintf("in %dh", h)
		default:
			return fmt.Sprintf("in %dd", int(d.Hours()/24))
		}
	}

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m > 0 {
			return fmt.Sprintf("%dh%dm ago", h, m)
		}
		return fmt.Sprintf("%dh ago", h)
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02 15:04")
	}
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(b int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)

	switch {
	case b >= tib:
		return fmt.Sprintf("%.1f TiB", float64(b)/float64(tib))
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib))
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(mib))
	case b >= kib:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(kib))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// formatCount formats a large number with comma separators.
func formatCount(n int64) string {
	if n < 0 {
		return "-" + formatCount(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}

	s := fmt.Sprintf("%d", n)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// formatDuration formats a duration in a compact human-readable way.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m < 60 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

// truncatePath shortens a file path to fit within maxLen characters.
func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	if maxLen <= 5 {
		return path[:maxLen]
	}
	keep := maxLen - 3
	front := keep / 3
	back := keep - front
	return path[:front] + "..." + path[len(path)-back:]
}

// formatSnapshotTime formats a snapshot time for display.
func formatSnapshotTime(t time.Time) string {
	now := time.Now()
	if now.Sub(t) < 24*time.Hour && t.Day() == now.Day() {
		return t.Format("Today 15:04")
	}
	if now.Sub(t) < 48*time.Hour {
		return t.Format("Yesterday 15:04")
	}
	return t.Format("Jan 02 15:04")
}

// wordWrap wraps a string to the given width, breaking on spaces.
func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}

	var lines []string
	currentLine := words[0]

	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) > width {
			lines = append(lines, currentLine)
			currentLine = word
		} else {
			currentLine += " " + word
		}
	}
	lines = append(lines, currentLine)
	return strings.Join(lines, "\n")
}

// shortID truncates a snapshot ID for display.
func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}
