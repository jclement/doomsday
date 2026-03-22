package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/tui"
	"github.com/jclement/doomsday/internal/types"
	"github.com/jclement/doomsday/internal/whimsy"
)

// LevelKind identifies the type of a drill-down column.
type LevelKind int

const (
	LevelDestinations LevelKind = iota // destinations for a config
	LevelSnapshots                     // snapshots for a config+dest
	LevelFiles                         // file browser within a snapshot
)

// listItem is one row in a drill-down column.
type listItem struct {
	id       string // unique identifier
	label    string // primary display text (filterable)
	sublabel string // secondary text (right-aligned)
	status   string // "ok"/"warning"/"error"/""

	// Underlying data (at most one is set)
	dest  *config.DestConfig
	snap  *SnapshotItem
	entry *browserEntry
}

// drillLevel holds the state for one miller column.
type drillLevel struct {
	kind      LevelKind
	title     string
	items     []listItem
	filtered  []int  // indices into items matching filter
	filter    string // current filter text
	cursor    int    // cursor in filtered list
	scrollTop int    // scroll offset

	// Context passed down the stack
	configName string
	dest       *config.DestConfig
	snapshotID string

	// File browser directory stack (within a LevelFiles column)
	pathStack []pathEntry
}

// DrillDownModel is the miller column navigation component.
type DrillDownModel struct {
	styles     tui.Styles
	theme      tui.Theme
	stack      []drillLevel // navigation stack (one per visible column)
	configName string       // backup config name (e.g. hostname)

	width  int
	height int
	ready  bool

	whimsy string // current whimsy message
}

// NewDrillDownModel creates a new drill-down model starting at the destinations level.
// If there is only one destination it starts at snapshots instead.
func NewDrillDownModel(styles tui.Styles, theme tui.Theme, cfg *config.Config, configName string) DrillDownModel {
	m := DrillDownModel{
		styles:     styles,
		theme:      theme,
		configName: configName,
		whimsy:     whimsy.Greeting(),
	}

	if len(cfg.Destinations) == 1 {
		// Single dest → start at snapshots.
		dest := cfg.Destinations[0]
		root := BuildSnapshotsLevel(&dest)
		m.stack = []drillLevel{root}
	} else {
		// Multiple dests → start at destinations.
		root := BuildDestinationsLevel(cfg.Destinations)
		m.stack = []drillLevel{root}
	}

	return m
}

// --- Accessors ---

// Depth returns the number of columns visible.
func (m *DrillDownModel) Depth() int {
	return len(m.stack)
}

// CurrentLevel returns the rightmost (active) column.
func (m *DrillDownModel) CurrentLevel() *drillLevel {
	if len(m.stack) == 0 {
		return nil
	}
	return &m.stack[len(m.stack)-1]
}

// SelectedItem returns the currently highlighted item, or nil.
func (m *DrillDownModel) SelectedItem() *listItem {
	lvl := m.CurrentLevel()
	if lvl == nil {
		return nil
	}
	return lvl.selectedItem()
}

// SelectedConfigName returns the config name from context (any level).
func (m *DrillDownModel) SelectedConfigName() string {
	for i := len(m.stack) - 1; i >= 0; i-- {
		if m.stack[i].configName != "" {
			return m.stack[i].configName
		}
	}
	return m.configName
}

// SelectedSnapshotID returns the snapshot ID from context (any level).
func (m *DrillDownModel) SelectedSnapshotID() string {
	for i := len(m.stack) - 1; i >= 0; i-- {
		if m.stack[i].snapshotID != "" {
			return m.stack[i].snapshotID
		}
	}
	return ""
}

// SelectedDest returns the destination config from context.
func (m *DrillDownModel) SelectedDest() *config.DestConfig {
	for i := len(m.stack) - 1; i >= 0; i-- {
		if m.stack[i].dest != nil {
			return m.stack[i].dest
		}
	}
	return nil
}

// IsFiltering returns true if the active column has a filter.
func (m *DrillDownModel) IsFiltering() bool {
	lvl := m.CurrentLevel()
	return lvl != nil && lvl.filter != ""
}

// --- Navigation ---

// Push adds a new column on the right.
func (m *DrillDownModel) Push(level drillLevel) {
	level.rebuildFilter()
	m.stack = append(m.stack, level)
}

// Pop removes the rightmost column. Returns false if already at root.
func (m *DrillDownModel) Pop() bool {
	if len(m.stack) <= 1 {
		return false
	}
	m.stack = m.stack[:len(m.stack)-1]
	// Refresh whimsy when returning to root level
	if len(m.stack) == 1 {
		m.whimsy = whimsy.IdleStatus()
	}
	return true
}

// CursorUp moves cursor up in the active column.
func (m *DrillDownModel) CursorUp() {
	if lvl := m.CurrentLevel(); lvl != nil {
		if lvl.cursor > 0 {
			lvl.cursor--
		}
	}
}

// CursorDown moves cursor down in the active column.
func (m *DrillDownModel) CursorDown() {
	if lvl := m.CurrentLevel(); lvl != nil {
		if lvl.cursor < len(lvl.filtered)-1 {
			lvl.cursor++
		}
	}
}

// CursorTop moves to the first item.
func (m *DrillDownModel) CursorTop() {
	if lvl := m.CurrentLevel(); lvl != nil {
		lvl.cursor = 0
	}
}

// CursorBottom moves to the last item.
func (m *DrillDownModel) CursorBottom() {
	if lvl := m.CurrentLevel(); lvl != nil {
		lvl.cursor = max(0, len(lvl.filtered)-1)
	}
}

// PageDown moves cursor down by n rows.
func (m *DrillDownModel) PageDown(n int) {
	if lvl := m.CurrentLevel(); lvl != nil {
		lvl.cursor = min(lvl.cursor+n, max(0, len(lvl.filtered)-1))
	}
}

// PageUp moves cursor up by n rows.
func (m *DrillDownModel) PageUp(n int) {
	if lvl := m.CurrentLevel(); lvl != nil {
		lvl.cursor = max(lvl.cursor-n, 0)
	}
}

// --- Filter ---

// ApplyFilterChar adds a character to the filter.
func (m *DrillDownModel) ApplyFilterChar(ch string) {
	if lvl := m.CurrentLevel(); lvl != nil {
		lvl.filter += ch
		lvl.rebuildFilter()
	}
}

// BackspaceFilter removes the last filter character.
// Returns true if a character was removed, false if filter was already empty.
func (m *DrillDownModel) BackspaceFilter() bool {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.filter == "" {
		return false
	}
	lvl.filter = lvl.filter[:len(lvl.filter)-1]
	lvl.rebuildFilter()
	return true
}

// ClearFilter clears the filter on the active column.
// Returns true if there was a filter to clear.
func (m *DrillDownModel) ClearFilter() bool {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.filter == "" {
		return false
	}
	lvl.filter = ""
	lvl.rebuildFilter()
	return true
}

// --- Config Updates ---

// SetSnapshotItems sets snapshot items for the current snapshot level.
func (m *DrillDownModel) SetSnapshotItems(items []SnapshotItem) {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.kind != LevelSnapshots {
		return
	}
	lvl.items = nil
	for i := range items {
		snap := &items[i]
		shortID := snap.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		sub := formatSnapshotTime(snap.Time)
		if snap.TotalSize > 0 {
			sub += "  " + formatBytes(snap.TotalSize)
		}
		lvl.items = append(lvl.items, listItem{
			id:       snap.ID,
			label:    shortID,
			sublabel: sub,
			snap:     snap,
		})
	}
	lvl.rebuildFilter()
}

// SetFileEntries sets file entries for the current file browser level.
func (m *DrillDownModel) SetFileEntries(entries []browserEntry) {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.kind != LevelFiles {
		return
	}
	lvl.items = nil
	for i := range entries {
		e := &entries[i]
		icon := fileIcon(e.node.Type)
		label := e.node.Name
		sub := ""
		if e.node.Type == tree.NodeTypeFile {
			sub = formatBytes(e.node.Size)
		} else if e.node.Type == tree.NodeTypeDir {
			sub = "<dir>"
		}
		entry := entries[i]
		lvl.items = append(lvl.items, listItem{
			id:       icon + " " + label,
			label:    label,
			sublabel: sub,
			entry:    &entry,
		})
	}
	lvl.rebuildFilter()
}

// FileCurrentPath returns the current browsing path in the file browser level.
func (m *DrillDownModel) FileCurrentPath() string {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.kind != LevelFiles {
		return "/"
	}
	if len(lvl.pathStack) == 0 {
		return "/"
	}
	var parts []string
	for _, p := range lvl.pathStack {
		if p.name != "/" {
			parts = append(parts, p.name)
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

// FileCanAscend returns true if the file browser can go up a directory.
func (m *DrillDownModel) FileCanAscend() bool {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.kind != LevelFiles {
		return false
	}
	return len(lvl.pathStack) > 1
}

// FileAscend pops one directory in the file browser. Returns parent tree ID.
func (m *DrillDownModel) FileAscend() types.BlobID {
	lvl := m.CurrentLevel()
	if lvl == nil || len(lvl.pathStack) <= 1 {
		return types.BlobID{}
	}
	lvl.pathStack = lvl.pathStack[:len(lvl.pathStack)-1]
	return lvl.pathStack[len(lvl.pathStack)-1].treeID
}

// FileDescend pushes into a subdirectory. Returns the subtree ID.
func (m *DrillDownModel) FileDescend() types.BlobID {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.kind != LevelFiles {
		return types.BlobID{}
	}
	item := lvl.selectedItem()
	if item == nil || item.entry == nil || item.entry.node.Type != tree.NodeTypeDir {
		return types.BlobID{}
	}
	if item.entry.subtree.IsZero() {
		return types.BlobID{}
	}
	lvl.pathStack = append(lvl.pathStack, pathEntry{
		treeID: item.entry.subtree,
		name:   item.entry.node.Name,
	})
	return item.entry.subtree
}

// FileCanDescend returns true if the selected entry is a directory.
func (m *DrillDownModel) FileCanDescend() bool {
	item := m.SelectedItem()
	return item != nil && item.entry != nil &&
		item.entry.node.Type == tree.NodeTypeDir && !item.entry.subtree.IsZero()
}

// FileSelectedPath returns the full path of the selected file/dir.
func (m *DrillDownModel) FileSelectedPath() string {
	item := m.SelectedItem()
	if item == nil || item.entry == nil {
		return ""
	}
	base := m.FileCurrentPath()
	if base == "/" {
		return item.entry.node.Name
	}
	return strings.TrimPrefix(base, "/") + "/" + item.entry.node.Name
}

// --- Rendering ---

// Render renders all visible miller columns.
func (m *DrillDownModel) Render(width, height int) string {
	m.width = width
	m.height = height
	m.ready = true

	if len(m.stack) == 0 {
		return m.styles.Muted.Render("No data.")
	}

	pc := panelColors{
		BorderActive: m.theme.Colors.BorderActive,
		BorderMuted:  m.theme.Colors.BorderMuted,
		TitleActive:  m.theme.Colors.Primary,
		TitleMuted:   m.theme.Colors.TextMuted,
	}

	// Column width allocation
	totalCols := len(m.stack)
	maxVisible := 3
	if totalCols > maxVisible {
		// Drop leftmost columns if too many
		totalCols = maxVisible
	}
	startIdx := len(m.stack) - totalCols

	// Active column gets ~65% of width, parents share the rest
	activeW := width
	parentW := 0
	if totalCols > 1 {
		parentW = max(width*30/(100*(totalCols-1)), 14)
		activeW = width - parentW*(totalCols-1)
		activeW = max(activeW, 20)
	}

	contentH := height

	var columns []string
	for ci := startIdx; ci < len(m.stack); ci++ {
		isActive := ci == len(m.stack)-1
		colW := parentW
		if isActive {
			colW = activeW
		}

		lvl := &m.stack[ci]
		title := lvl.title
		if lvl.filter != "" {
			title += " [" + lvl.filter + "]"
		}

		content := m.renderColumnContent(lvl, colW-4, contentH-2, isActive)
		panel := RenderPanel(title, content, colW, contentH, isActive, pc)
		columns = append(columns, panel)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, columns...)
}

// RenderActiveColumn renders only the active (rightmost) column as a single panel.
// Used when preview is active to avoid squeezing the full miller column layout.
func (m *DrillDownModel) RenderActiveColumn(width, height int) string {
	if len(m.stack) == 0 {
		return m.styles.Muted.Render("No data.")
	}

	pc := panelColors{
		BorderActive: m.theme.Colors.BorderActive,
		BorderMuted:  m.theme.Colors.BorderMuted,
		TitleActive:  m.theme.Colors.Primary,
		TitleMuted:   m.theme.Colors.TextMuted,
	}

	lvl := &m.stack[len(m.stack)-1]
	title := lvl.title
	if lvl.filter != "" {
		title += " [" + lvl.filter + "]"
	}

	content := m.renderColumnContent(lvl, width-4, height-2, true)
	return RenderPanel(title, content, width, height, true, pc)
}

// renderColumnContent renders the rows of a single column.
func (m *DrillDownModel) renderColumnContent(lvl *drillLevel, width, height int, isActive bool) string {
	if len(lvl.items) == 0 {
		if isActive && (lvl.kind == LevelSnapshots || lvl.kind == LevelFiles) {
			return m.styles.Muted.Render("Loading...")
		}
		return m.styles.Muted.Render("Nothing here yet.")
	}

	if len(lvl.filtered) == 0 {
		return m.styles.Muted.Render("No matches for \"" + lvl.filter + "\"")
	}

	// Adjust scroll to keep cursor visible
	visibleRows := max(height, 1)
	if lvl.cursor < lvl.scrollTop {
		lvl.scrollTop = lvl.cursor
	}
	if lvl.cursor >= lvl.scrollTop+visibleRows {
		lvl.scrollTop = lvl.cursor - visibleRows + 1
	}
	if lvl.scrollTop < 0 {
		lvl.scrollTop = 0
	}

	var lines []string
	endIdx := min(lvl.scrollTop+visibleRows, len(lvl.filtered))
	for vi := lvl.scrollTop; vi < endIdx; vi++ {
		idx := lvl.filtered[vi]
		item := &lvl.items[idx]
		selected := vi == lvl.cursor

		line := m.renderItem(item, lvl, selected, isActive, width)
		lines = append(lines, line)
	}

	// Whimsy at bottom of root column
	if isActive && len(m.stack) == 1 && m.whimsy != "" {
		remaining := visibleRows - len(lines)
		if remaining > 2 {
			lines = append(lines, "")
			wrapped := wordWrap("\""+m.whimsy+"\"", max(width-4, 10))
			for _, wl := range strings.Split(wrapped, "\n") {
				lines = append(lines, m.styles.Whimsy.Render(wl))
			}
		}
	}

	return strings.Join(lines, "\n")
}

// renderItem renders one list item row.
func (m *DrillDownModel) renderItem(item *listItem, lvl *drillLevel, selected, isActive bool, width int) string {
	prefix := "  "
	if selected && isActive {
		prefix = lipgloss.NewStyle().Foreground(m.theme.Colors.Primary).Render("> ")
	} else if selected {
		prefix = lipgloss.NewStyle().Foreground(m.theme.Colors.TextMuted).Render("> ")
	}

	// Status dot for configs
	dot := ""
	if item.status != "" {
		dot = m.styles.StatusDot(item.status) + " "
	}

	// Label
	label := item.label
	labelStyle := m.styles.Body
	if selected && isActive {
		labelStyle = lipgloss.NewStyle().Foreground(m.theme.Colors.Primary).Bold(true)
	}

	// For file entries, show icon
	if item.entry != nil {
		icon := fileIcon(item.entry.node.Type)
		iconStyle := m.styles.Muted
		if item.entry.node.Type == tree.NodeTypeDir {
			iconStyle = lipgloss.NewStyle().Foreground(m.theme.Colors.Secondary)
		}
		dot = iconStyle.Render(icon) + " "
	}

	labelStr := labelStyle.Render(label)

	// Sublabel right-aligned if space allows
	sublabel := ""
	if item.sublabel != "" {
		sublabel = m.styles.Muted.Render(item.sublabel)
	}

	labelVisual := lipgloss.Width(prefix) + lipgloss.Width(dot) + lipgloss.Width(labelStr)
	subVisual := lipgloss.Width(sublabel)

	// Indicator that this row's child is drilled into (for parent columns)
	indicator := ""
	if !isActive && selected {
		indicator = m.styles.Muted.Render(" >")
		labelVisual += 2
	}

	gap := width - labelVisual - subVisual
	if gap < 1 {
		// Not enough room for sublabel — truncate label or drop sublabel
		if width-labelVisual < 2 {
			return prefix + dot + labelStr + indicator
		}
		gap = 1
	}

	if sublabel == "" {
		return prefix + dot + labelStr + indicator
	}
	return prefix + dot + labelStr + indicator + strings.Repeat(" ", gap) + sublabel
}

// Toolbar returns a context-sensitive toolbar string.
func (m *DrillDownModel) Toolbar() string {
	lvl := m.CurrentLevel()
	if lvl == nil {
		return ""
	}

	var parts []string
	sep := m.styles.Muted.Render(" · ")

	switch lvl.kind {
	case LevelDestinations:
		parts = append(parts,
			m.styles.HelpKey.Render("b")+" "+m.styles.HelpDesc.Render("backup"),
			m.styles.HelpKey.Render("enter")+" "+m.styles.HelpDesc.Render("snapshots"),
		)
		if len(m.stack) > 1 {
			parts = append(parts,
				m.styles.HelpKey.Render("bksp/esc")+" "+m.styles.HelpDesc.Render("back"),
			)
		}

	case LevelSnapshots:
		parts = append(parts,
			m.styles.HelpKey.Render("r")+" "+m.styles.HelpDesc.Render("restore"),
			m.styles.HelpKey.Render("d")+" "+m.styles.HelpDesc.Render("delete"),
			m.styles.HelpKey.Render("enter")+" "+m.styles.HelpDesc.Render("browse"),
		)
		if len(m.stack) > 1 {
			parts = append(parts,
				m.styles.HelpKey.Render("bksp/esc")+" "+m.styles.HelpDesc.Render("back"),
			)
		}

	case LevelFiles:
		parts = append(parts,
			m.styles.HelpKey.Render("r")+" "+m.styles.HelpDesc.Render("restore"),
			m.styles.HelpKey.Render("enter")+" "+m.styles.HelpDesc.Render("open/preview"),
			m.styles.HelpKey.Render("bksp/esc")+" "+m.styles.HelpDesc.Render("back"),
		)
	}

	if lvl.filter != "" {
		parts = append(parts,
			m.styles.HelpKey.Render("esc")+" "+m.styles.HelpDesc.Render("clear filter"),
		)
	}

	parts = append(parts,
		m.styles.HelpKey.Render("/")+" "+m.styles.HelpDesc.Render("filter"),
		m.styles.HelpKey.Render("?")+" "+m.styles.HelpDesc.Render("help"),
		m.styles.HelpKey.Render("q")+" "+m.styles.HelpDesc.Render("quit"),
	)

	return " " + strings.Join(parts, sep)
}

// --- Level Builder Helpers ---

// BuildDestinationsLevel creates a destinations column.
func BuildDestinationsLevel(dests []config.DestConfig) drillLevel {
	lvl := drillLevel{
		kind:  LevelDestinations,
		title: "Destinations",
	}

	for i := range dests {
		dc := &dests[i]
		sub := dc.Type
		switch dc.Type {
		case "local":
			sub += "  " + dc.Path
		case "s3":
			sub += "  " + dc.Bucket
		case "sftp":
			sub += "  " + dc.Host + ":" + dc.BasePath
		}
		dest := dests[i]
		lvl.items = append(lvl.items, listItem{
			id:       dc.Name,
			label:    dc.Name,
			sublabel: sub,
			dest:     &dest,
		})
	}
	lvl.rebuildFilter()
	return lvl
}

// BuildSnapshotsLevel creates an empty snapshots column (items set later via SetSnapshotItems).
func BuildSnapshotsLevel(dest *config.DestConfig) drillLevel {
	title := dest.Name + " · Snapshots"

	return drillLevel{
		kind:  LevelSnapshots,
		title: title,
		dest:  dest,
	}
}

// BuildFilesLevel creates an empty file browser column (items set later via SetFileEntries).
func BuildFilesLevel(configName, snapshotID string) drillLevel {
	shortSnap := snapshotID
	if len(shortSnap) > 8 {
		shortSnap = shortSnap[:8]
	}

	return drillLevel{
		kind:       LevelFiles,
		title:      "[" + shortSnap + "] · Files",
		configName: configName,
		snapshotID: snapshotID,
		pathStack:  []pathEntry{{name: "/"}},
	}
}

// --- drillLevel helpers ---

func (l *drillLevel) selectedItem() *listItem {
	if l.cursor < 0 || l.cursor >= len(l.filtered) {
		return nil
	}
	idx := l.filtered[l.cursor]
	return &l.items[idx]
}

func (l *drillLevel) rebuildFilter() {
	l.filtered = nil
	if l.filter == "" {
		for i := range l.items {
			l.filtered = append(l.filtered, i)
		}
	} else {
		lower := strings.ToLower(l.filter)
		for i := range l.items {
			if strings.Contains(strings.ToLower(l.items[i].label), lower) ||
				strings.Contains(strings.ToLower(l.items[i].sublabel), lower) {
				l.filtered = append(l.filtered, i)
			}
		}
	}
	// Clamp cursor
	if l.cursor >= len(l.filtered) {
		l.cursor = max(0, len(l.filtered)-1)
	}
	l.scrollTop = 0
}

// title for the file browser level including path
func (m *DrillDownModel) fileLevelTitle() string {
	lvl := m.CurrentLevel()
	if lvl == nil || lvl.kind != LevelFiles {
		return ""
	}
	shortSnap := lvl.snapshotID
	if len(shortSnap) > 8 {
		shortSnap = shortSnap[:8]
	}
	path := m.FileCurrentPath()
	return fmt.Sprintf("[%s] · %s", shortSnap, path)
}
