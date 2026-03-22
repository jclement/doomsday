// Package web provides an embedded HTTP server for browsing doomsday backups.
//
// The server binds to localhost only, picks a free port, and includes
// a random auth token in the URL for security. All assets (HTML, CSS, JS)
// are embedded in the binary via go:embed.
package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/restore"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
	"github.com/jclement/doomsday/internal/tui/views"
)

// Server is the web UI HTTP server.
type Server struct {
	session    *views.Session
	configName string
	token      string
	listener   net.Listener
	indexHTML  string // pre-built HTML with Alpine.js inlined
}

// New creates a new web server. It picks a free port on localhost.
func New(session *views.Session, configName string) (*Server, error) {
	// Generate random auth token.
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate auth token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	// Bind to localhost on a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return &Server{
		session:    session,
		configName: configName,
		token:      token,
		listener:   ln,
		indexHTML:  buildIndexHTML(),
	}, nil
}

// URL returns the full URL including auth token.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?token=%s", s.listener.Addr(), s.token)
}

// OpenBrowser attempts to open the URL in the user's default browser.
func (s *Server) OpenBrowser() {
	url := s.URL()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}

// Serve starts serving HTTP requests. Blocks until context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	mux := http.NewServeMux()

	// Auth middleware wraps all handlers.
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get("token")
			if token == "" {
				token = r.Header.Get("X-Auth-Token")
			}
			if token == "" {
				// Check cookie.
				if c, err := r.Cookie("doomsday_token"); err == nil {
					token = c.Value
				}
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}

	// API routes (registered first so they take priority).
	mux.HandleFunc("GET /api/destinations", auth(s.handleDestinations))
	mux.HandleFunc("GET /api/snapshots", auth(s.handleSnapshots))
	mux.HandleFunc("GET /api/tree", auth(s.handleTree))
	mux.HandleFunc("GET /api/file", auth(s.handleFile))
	mux.HandleFunc("GET /api/find", auth(s.handleFind))
	mux.HandleFunc("GET /api/restore", auth(s.handleRestore))
	mux.HandleFunc("GET /api/compare", auth(s.handleCompare))

	// SPA catch-all: any non-API GET serves the index HTML.
	mux.HandleFunc("GET /", auth(s.handleIndex))

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	if err := srv.Serve(s.listener); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleIndex serves the single-page HTML app.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Set cookie so subsequent API calls don't need ?token= in the URL.
	http.SetCookie(w, &http.Cookie{
		Name:     "doomsday_token",
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(s.indexHTML))
}

// --- API Handlers ---

type destJSON struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Active   bool   `json:"active"`
	Location string `json:"location"`
}

func (s *Server) handleDestinations(w http.ResponseWriter, r *http.Request) {
	cfg := s.session.Config()
	var dests []destJSON
	for _, d := range cfg.Destinations {
		dests = append(dests, destJSON{
			Name:     d.Name,
			Type:     d.Type,
			Active:   d.IsActive(),
			Location: destLocation(d),
		})
	}
	writeJSON(w, dests)
}

type snapJSON struct {
	ID             string   `json:"id"`
	ShortID        string   `json:"short_id"`
	Time           string   `json:"time"`
	TimeRelative   string   `json:"time_relative"`
	Hostname       string   `json:"hostname"`
	Paths          string   `json:"paths"`
	PathsList      []string `json:"paths_list"`
	TotalFiles     int64  `json:"total_files"`
	TotalSize      int64  `json:"total_size"`
	TotalSizeHuman string `json:"total_size_human"`
	DataAdded      int64  `json:"data_added"`
	DataAddedHuman string `json:"data_added_human"`
	FilesNew       int64  `json:"files_new"`
	FilesChanged   int64  `json:"files_changed"`
	Duration       string `json:"duration"`
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	destName := r.URL.Query().Get("dest")
	if destName == "" {
		httpError(w, "missing dest parameter", http.StatusBadRequest)
		return
	}

	cfg := s.session.Config()
	dest, err := cfg.FindDestination(destName)
	if err != nil {
		httpError(w, err.Error(), http.StatusNotFound)
		return
	}

	items, err := s.session.LoadSnapshots(r.Context(), dest)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var result []snapJSON
	for _, item := range items {
		sj := snapJSON{
			ID:             item.ID,
			ShortID:        shortID(item.ID),
			Time:           item.Time.Local().Format("2006-01-02 15:04:05"),
			TimeRelative:   relativeTime(item.Time),
			Hostname:       item.Hostname,
			Paths:          strings.Join(item.Paths, ", "),
			PathsList:      item.Paths,
			TotalFiles:     item.TotalFiles,
			TotalSize:      item.TotalSize,
			TotalSizeHuman: humanBytes(item.TotalSize),
			DataAdded:      item.DataAdded,
			DataAddedHuman: humanBytes(item.DataAdded),
			FilesNew:       item.FilesNew,
			FilesChanged:   item.FilesChanged,
			Duration:       humanDuration(item.Duration),
		}
		result = append(result, sj)
	}

	writeJSON(w, result)
}

type treeEntryJSON struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Size          int64  `json:"size"`
	SizeHuman     string `json:"size_human"`
	Mode          string `json:"mode"`
	ModTime       string `json:"mtime"`
	SymlinkTarget string `json:"symlink_target,omitempty"`
	IsDir         bool   `json:"is_dir"`
	HasContent    bool   `json:"has_content"`
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	destName := r.URL.Query().Get("dest")
	snapID := r.URL.Query().Get("snapshot")
	dirPath := r.URL.Query().Get("path")

	if destName == "" || snapID == "" {
		httpError(w, "missing dest or snapshot parameter", http.StatusBadRequest)
		return
	}

	cfg := s.session.Config()
	dest, err := cfg.FindDestination(destName)
	if err != nil {
		httpError(w, err.Error(), http.StatusNotFound)
		return
	}

	treeID, err := s.session.SnapshotTreeID(r.Context(), dest, snapID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Navigate to the requested path.
	currentTree, err := s.session.LoadTree(r.Context(), dest, treeID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if dirPath != "" && dirPath != "/" {
		parts := strings.Split(strings.Trim(dirPath, "/"), "/")
		for _, part := range parts {
			if part == "" {
				continue
			}
			node := currentTree.Find(part)
			if node == nil {
				httpError(w, fmt.Sprintf("path not found: %s", dirPath), http.StatusNotFound)
				return
			}
			if node.Type != tree.NodeTypeDir {
				httpError(w, fmt.Sprintf("%s is not a directory", part), http.StatusBadRequest)
				return
			}
			if node.Subtree.IsZero() {
				httpError(w, fmt.Sprintf("directory %q has no subtree", part), http.StatusInternalServerError)
				return
			}
			currentTree, err = s.session.LoadTree(r.Context(), dest, node.Subtree)
			if err != nil {
				httpError(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	entries := views.TreeToEntries(currentTree)
	var result []treeEntryJSON
	for _, e := range entries {
		mtime := ""
		if !e.Node().ModTime.IsZero() && e.Node().ModTime.Year() > 1970 {
			mtime = e.Node().ModTime.Local().Format("2006-01-02 15:04")
		}
		te := treeEntryJSON{
			Name:          e.Node().Name,
			Type:          string(e.Node().Type),
			Size:          e.Node().Size,
			SizeHuman:     humanBytes(e.Node().Size),
			Mode:          e.Node().Mode.String(),
			ModTime:       mtime,
			SymlinkTarget: e.Node().SymlinkTarget,
			IsDir:         e.Node().Type == tree.NodeTypeDir,
			HasContent:    len(e.Node().Content) > 0,
		}
		result = append(result, te)
	}

	writeJSON(w, result)
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	destName := r.URL.Query().Get("dest")
	snapID := r.URL.Query().Get("snapshot")
	filePath := r.URL.Query().Get("path")
	mode := r.URL.Query().Get("mode") // "view" or "download"

	if destName == "" || snapID == "" || filePath == "" {
		httpError(w, "missing parameters", http.StatusBadRequest)
		return
	}

	cfg := s.session.Config()
	dest, err := cfg.FindDestination(destName)
	if err != nil {
		httpError(w, err.Error(), http.StatusNotFound)
		return
	}

	// Navigate to the file.
	treeID, err := s.session.SnapshotTreeID(r.Context(), dest, snapID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	currentTree, err := s.session.LoadTree(r.Context(), dest, treeID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	parts := strings.Split(strings.Trim(filePath, "/"), "/")
	var targetNode *tree.Node

	for i, part := range parts {
		if part == "" {
			continue
		}
		node := currentTree.Find(part)
		if node == nil {
			httpError(w, fmt.Sprintf("path not found: %s", filePath), http.StatusNotFound)
			return
		}
		if i == len(parts)-1 {
			targetNode = node
		} else {
			if node.Type != tree.NodeTypeDir || node.Subtree.IsZero() {
				httpError(w, fmt.Sprintf("not a directory: %s", part), http.StatusBadRequest)
				return
			}
			currentTree, err = s.session.LoadTree(r.Context(), dest, node.Subtree)
			if err != nil {
				httpError(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	if targetNode == nil {
		httpError(w, "file not found", http.StatusNotFound)
		return
	}

	if targetNode.Type != tree.NodeTypeFile || len(targetNode.Content) == 0 {
		httpError(w, "not a regular file or empty", http.StatusBadRequest)
		return
	}

	// For download mode, stream all content.
	if mode == "download" {
		fileName := sanitizeFilename(path.Base(filePath))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
		w.Header().Set("Content-Type", "application/octet-stream")

		repo, err := s.session.OpenRepo(r.Context(), dest)
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, blobID := range targetNode.Content {
			chunk, err := repo.LoadBlob(r.Context(), blobID)
			if err != nil {
				return // already started writing
			}
			w.Write(chunk)
		}
		return
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	fileName := path.Base(filePath)

	// Images and PDFs: stream full content directly (no size cap).
	if isImageExt(ext) || ext == ".pdf" {
		ct := imageContentType(ext)
		if ext == ".pdf" {
			ct = "application/pdf"
		}
		w.Header().Set("Content-Type", ct)

		repo, err := s.session.OpenRepo(r.Context(), dest)
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, blobID := range targetNode.Content {
			chunk, err := repo.LoadBlob(r.Context(), blobID)
			if err != nil {
				return // already started writing
			}
			w.Write(chunk)
		}
		return
	}

	// Text/binary: load with 1 MiB cap (for syntax highlighting).
	content, err := s.session.LoadFileContent(r.Context(), dest, targetNode.Content)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Check for binary content.
	checkLen := min(len(content), 512)
	isBinary := false
	for _, b := range content[:checkLen] {
		if b == 0 {
			isBinary = true
			break
		}
	}

	if isBinary {
		writeJSON(w, map[string]any{
			"binary":   true,
			"size":     targetNode.Size,
			"filename": fileName,
		})
		return
	}

	// Text file: syntax highlight with chroma HTML output.
	text := string(content)
	highlighted, cssClass := highlightHTML(text, fileName)

	writeJSON(w, map[string]any{
		"binary":    false,
		"content":   highlighted,
		"css_class": cssClass,
		"lines":     strings.Count(text, "\n") + 1,
		"size":      targetNode.Size,
		"filename":  fileName,
		"truncated": len(content) >= 1<<20,
	})
}

func (s *Server) handleFind(w http.ResponseWriter, r *http.Request) {
	destName := r.URL.Query().Get("dest")
	snapID := r.URL.Query().Get("snapshot")
	pattern := r.URL.Query().Get("pattern")

	if destName == "" || snapID == "" || pattern == "" {
		httpError(w, "missing parameters", http.StatusBadRequest)
		return
	}

	// Validate pattern.
	if _, err := filepath.Match(pattern, ""); err != nil {
		httpError(w, fmt.Sprintf("invalid glob pattern: %v", err), http.StatusBadRequest)
		return
	}

	cfg := s.session.Config()
	dest, err := cfg.FindDestination(destName)
	if err != nil {
		httpError(w, err.Error(), http.StatusNotFound)
		return
	}

	repo, err := s.session.OpenRepo(r.Context(), dest)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	snap, err := repo.LoadSnapshot(r.Context(), snapID)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rootData, err := repo.LoadBlob(r.Context(), snap.Tree)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rootTree, err := tree.Unmarshal(rootData)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type findResultJSON struct {
		Path       string `json:"path"`
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		SizeHuman  string `json:"size_human"`
		Type       string `json:"type"`
		Mode       string `json:"mode"`
		ModTime    string `json:"mtime"`
		IsDir      bool   `json:"is_dir"`
		HasContent bool   `json:"has_content"`
	}

	const maxResults = 10000
	var results []findResultJSON
	var limitHit bool

	var walkTreeFind func(ctx context.Context, t *tree.Tree, prefix string) error
	walkTreeFind = func(ctx context.Context, t *tree.Tree, prefix string) error {
		for _, node := range t.Nodes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(results) >= maxResults {
				limitHit = true
				return nil
			}

			nodePath := prefix + node.Name

			matched, _ := filepath.Match(pattern, node.Name)
			if !matched {
				matched, _ = filepath.Match(pattern, nodePath)
			}
			if matched {
				results = append(results, findResultJSON{
					Path:       nodePath,
					Name:       node.Name,
					Size:       node.Size,
					SizeHuman:  humanBytes(node.Size),
					Type:       string(node.Type),
					Mode:       node.Mode.String(),
					ModTime:    node.ModTime.Local().Format("2006-01-02 15:04"),
					IsDir:      node.Type == tree.NodeTypeDir,
					HasContent: len(node.Content) > 0,
				})
			}

			if node.Type == tree.NodeTypeDir && !node.Subtree.IsZero() {
				data, err := repo.LoadBlob(ctx, node.Subtree)
				if err != nil {
					continue
				}
				subtree, err := tree.Unmarshal(data)
				if err != nil {
					continue
				}
				if err := walkTreeFind(ctx, subtree, nodePath+"/"); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walkTreeFind(r.Context(), rootTree, "/"); err != nil && r.Context().Err() == nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"matches":   results,
		"count":     len(results),
		"truncated": limitHit,
	})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	destName := r.URL.Query().Get("dest")
	snapID := r.URL.Query().Get("snapshot")
	restorePath := r.URL.Query().Get("path")
	target := r.URL.Query().Get("target")

	if destName == "" || snapID == "" || target == "" {
		httpError(w, "missing parameters", http.StatusBadRequest)
		return
	}

	cfg := s.session.Config()
	dest, err := cfg.FindDestination(destName)
	if err != nil {
		httpError(w, err.Error(), http.StatusNotFound)
		return
	}

	repo, err := s.session.OpenRepo(r.Context(), dest)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Use SSE for progress.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send an initial event so the client knows we're connected.
	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	sendEvent := func(event string, data any) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
		flusher.Flush()
	}

	// Create a context that is cancelled when the HTTP request context
	// ends (client disconnect or server shutdown).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	opts := restore.Options{
		Overwrite: true,
		OnProgress: func(ev restore.ProgressEvent) {
			sendEvent("progress", map[string]any{
				"path":            ev.Path,
				"files_completed": ev.FilesCompleted,
				"files_total":     ev.FilesTotal,
				"bytes_written":   ev.BytesWritten,
			})
		},
	}

	if restorePath != "" {
		opts.IncludePaths = []string{strings.TrimPrefix(restorePath, "/")}
	}

	// Ensure target parent exists.
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		sendEvent("restore_error", map[string]string{"error": fmt.Sprintf("create target parent: %v", err)})
		return
	}

	if err := restore.Run(ctx, repo, snapID, target, opts); err != nil {
		sendEvent("restore_error", map[string]string{"error": err.Error()})
		return
	}

	sendEvent("restore_done", map[string]string{"target": target})
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	destName := r.URL.Query().Get("dest")
	snapA := r.URL.Query().Get("a")
	snapB := r.URL.Query().Get("b")

	if destName == "" || snapA == "" || snapB == "" {
		httpError(w, "missing dest, a, or b parameter", http.StatusBadRequest)
		return
	}

	cfg := s.session.Config()
	dest, err := cfg.FindDestination(destName)
	if err != nil {
		httpError(w, err.Error(), http.StatusNotFound)
		return
	}

	rp, err := s.session.OpenRepo(r.Context(), dest)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Load both snapshots.
	snapObjA, err := rp.LoadSnapshot(r.Context(), snapA)
	if err != nil {
		httpError(w, fmt.Sprintf("load snapshot A: %v", err), http.StatusInternalServerError)
		return
	}
	snapObjB, err := rp.LoadSnapshot(r.Context(), snapB)
	if err != nil {
		httpError(w, fmt.Sprintf("load snapshot B: %v", err), http.StatusInternalServerError)
		return
	}

	// Walk both trees and collect file metadata.
	filesA, err := collectTreeFiles(r.Context(), rp, snapObjA.Tree, "")
	if err != nil {
		httpError(w, fmt.Sprintf("walk tree A: %v", err), http.StatusInternalServerError)
		return
	}
	filesB, err := collectTreeFiles(r.Context(), rp, snapObjB.Tree, "")
	if err != nil {
		httpError(w, fmt.Sprintf("walk tree B: %v", err), http.StatusInternalServerError)
		return
	}

	// Build diff.
	type compareEntry struct {
		Path       string `json:"path"`
		Status     string `json:"status"` // added, removed, modified, unchanged
		SizeA      int64  `json:"size_a"`
		SizeAHuman string `json:"size_a_human"`
		SizeB      int64  `json:"size_b"`
		SizeBHuman string `json:"size_b_human"`
		ModTimeA   string `json:"mtime_a,omitempty"`
		ModTimeB   string `json:"mtime_b,omitempty"`
		Type       string `json:"type"`
	}

	var entries []compareEntry

	// All paths from both sides.
	allPaths := make(map[string]bool)
	for p := range filesA {
		allPaths[p] = true
	}
	for p := range filesB {
		allPaths[p] = true
	}

	// Sort paths.
	var sortedPaths []string
	for p := range allPaths {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, p := range sortedPaths {
		a, inA := filesA[p]
		b, inB := filesB[p]

		entry := compareEntry{Path: p}

		if inA && inB {
			entry.SizeA = a.size
			entry.SizeAHuman = humanBytes(a.size)
			entry.SizeB = b.size
			entry.SizeBHuman = humanBytes(b.size)
			entry.ModTimeA = a.modTime
			entry.ModTimeB = b.modTime
			entry.Type = a.nodeType

			if a.size != b.size || a.contentID != b.contentID {
				entry.Status = "modified"
			} else {
				entry.Status = "unchanged"
			}
		} else if inA {
			entry.Status = "removed"
			entry.SizeA = a.size
			entry.SizeAHuman = humanBytes(a.size)
			entry.ModTimeA = a.modTime
			entry.Type = a.nodeType
		} else {
			entry.Status = "added"
			entry.SizeB = b.size
			entry.SizeBHuman = humanBytes(b.size)
			entry.ModTimeB = b.modTime
			entry.Type = b.nodeType
		}

		entries = append(entries, entry)
	}

	// Filter: only include changed files by default.
	showAll := r.URL.Query().Get("all") == "true"
	if !showAll {
		var filtered []compareEntry
		for _, e := range entries {
			if e.Status != "unchanged" {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	const maxCompareResults = 10000
	truncated := len(entries) > maxCompareResults
	if truncated {
		entries = entries[:maxCompareResults]
	}

	writeJSON(w, map[string]any{
		"entries":    entries,
		"count":      len(entries),
		"truncated":  truncated,
		"snapshot_a": shortID(snapA),
		"snapshot_b": shortID(snapB),
	})
}

// treeFileInfo holds metadata about a file in a snapshot tree.
type treeFileInfo struct {
	size      int64
	modTime   string
	nodeType  string
	contentID [32]byte // hash of blob IDs for change detection
}

// collectTreeFiles walks a snapshot tree and collects all file entries.
// Respects context cancellation and stops after maxFiles (0 = no limit).
func collectTreeFiles(ctx context.Context, r *repo.Repository, treeID types.BlobID, prefix string) (map[string]treeFileInfo, error) {
	files := make(map[string]treeFileInfo)
	if err := walkTreeFiles(ctx, r, treeID, prefix, files); err != nil {
		return nil, err
	}
	return files, nil
}

func walkTreeFiles(ctx context.Context, r *repo.Repository, treeID types.BlobID, prefix string, files map[string]treeFileInfo) error {
	if treeID.IsZero() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	data, err := r.LoadBlob(ctx, treeID)
	if err != nil {
		return fmt.Errorf("load tree blob: %w", err)
	}
	t, err := tree.Unmarshal(data)
	if err != nil {
		return fmt.Errorf("unmarshal tree: %w", err)
	}

	for _, node := range t.Nodes {
		nodePath := prefix + "/" + node.Name

		if node.Type == tree.NodeTypeDir && !node.Subtree.IsZero() {
			if err := walkTreeFiles(ctx, r, node.Subtree, nodePath, files); err != nil {
				return err
			}
		} else if node.Type == tree.NodeTypeFile {
			// Hash the blob IDs for compact change detection.
			h := sha256.New()
			for _, id := range node.Content {
				h.Write(id[:])
			}
			var cid [32]byte
			copy(cid[:], h.Sum(nil))
			files[nodePath] = treeFileInfo{
				size:      node.Size,
				modTime:   node.ModTime.Local().Format("2006-01-02 15:04"),
				nodeType:  string(node.Type),
				contentID: cid,
			}
		}
	}
	return nil
}

// --- Helpers ---

func destLocation(dest config.DestConfig) string {
	switch dest.Type {
	case "sftp":
		port := dest.Port
		if port == 0 {
			port = 22
		}
		return fmt.Sprintf("%s@%s:%d", dest.User, dest.Host, port)
	case "s3":
		return fmt.Sprintf("%s/%s", dest.Endpoint, dest.Bucket)
	case "local":
		return config.ExpandPath(dest.Path)
	default:
		return ""
	}
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 02")
	}
}

func humanBytes(b int64) string {
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

func humanDuration(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico":
		return true
	}
	return false
}

func imageContentType(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	}
	return "application/octet-stream"
}

// highlightHTML produces syntax-highlighted HTML using chroma.
func highlightHTML(text, filename string) (string, string) {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("monokai")

	formatter := chromahtml.New(
		chromahtml.WithClasses(false), // inline styles for zero-dependency
		chromahtml.WithLineNumbers(true),
		chromahtml.TabWidth(4),
	)

	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		// Fallback: return escaped text.
		return "<pre>" + htmlEscape(text) + "</pre>", ""
	}

	var buf strings.Builder
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "<pre>" + htmlEscape(text) + "</pre>", ""
	}

	return buf.String(), ""
}

// sanitizeFilename removes characters that could cause header injection
// or path traversal in Content-Disposition filenames.
func sanitizeFilename(name string) string {
	// Remove any characters that could break the header or cause issues.
	var sb strings.Builder
	for _, r := range name {
		if r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '/' || r == '\x00' {
			sb.WriteRune('_')
		} else {
			sb.WriteRune(r)
		}
	}
	result := sb.String()
	if result == "" {
		return "download"
	}
	return result
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func httpError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

