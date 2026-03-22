package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/tui/views"
)

// testSetup creates a test environment with a local backup and a running web server.
type testSetup struct {
	t         *testing.T
	repoDir   string
	sourceDir string
	masterKey crypto.MasterKey
	cfg       *config.Config
	session   *views.Session
	srv       *Server
	baseURL   string
	client    *http.Client
}

func newTestSetup(t *testing.T) *testSetup {
	t.Helper()

	repoDir := t.TempDir()
	sourceDir := t.TempDir()

	// Create source files.
	writeTestFile(t, sourceDir, "hello.txt", "Hello, world!\n")
	writeTestFile(t, sourceDir, "docs/readme.md", "# README\nThis is a test.\n")
	writeTestFile(t, sourceDir, "docs/notes.txt", "Some notes here.\n")
	writeTestFile(t, sourceDir, "images/test.png", "fake-png-data-for-testing")

	// Create master key.
	var masterKey crypto.MasterKey
	if _, err := rand.Read(masterKey[:]); err != nil {
		t.Fatalf("generate master key: %v", err)
	}

	// Init repo and run backup.
	ctx := context.Background()
	backend, err := local.New(repoDir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	r, err := repo.Init(ctx, backend, masterKey)
	if err != nil {
		t.Fatalf("repo.Init: %v", err)
	}

	snap, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test",
		Hostname:         "test-host",
		CompressionLevel: 3,
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}
	t.Logf("Created snapshot %s with %d files", snap.ID[:10], snap.Summary.TotalFiles)

	backend.Close()

	// Build config that matches the repo.
	cfg := &config.Config{
		Key: "unused", // we'll unlock with masterKey directly
		Sources: []config.SourceConfig{
			{Path: sourceDir},
		},
		Destinations: []config.DestConfig{
			{Name: "local-test", Type: "local", Path: repoDir},
		},
	}

	// Create session and unlock.
	session := views.NewSession(cfg)
	session.UnlockWithKey(masterKey)

	// Start web server.
	srv, err := New(session, "test")
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(func() {
		cancel()
		session.Close()
	})

	go srv.Serve(ctx)

	// Wait for server to be ready.
	baseURL := "http://" + srv.listener.Addr().String()
	client := &http.Client{Timeout: 10 * time.Second}

	// Set auth cookie by hitting the index page with token.
	jar := &tokenJar{token: srv.token}
	client.Jar = jar

	ts := &testSetup{
		t:         t,
		repoDir:   repoDir,
		sourceDir: sourceDir,
		masterKey: masterKey,
		cfg:       cfg,
		session:   session,
		srv:       srv,
		baseURL:   baseURL,
		client:    client,
	}

	return ts
}

// tokenJar is a simple cookie jar that sends the auth token as a cookie.
type tokenJar struct {
	token   string
	cookies []*http.Cookie
}

func (j *tokenJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.cookies = append(j.cookies, cookies...)
}

func (j *tokenJar) Cookies(u *url.URL) []*http.Cookie {
	// Always send auth cookie.
	return append(j.cookies, &http.Cookie{
		Name:  "doomsday_token",
		Value: j.token,
	})
}

func (ts *testSetup) get(path string, params map[string]string) (int, []byte) {
	ts.t.Helper()
	u := ts.baseURL + path
	if len(params) > 0 {
		q := url.Values{}
		for k, v := range params {
			q.Set(k, v)
		}
		u += "?" + q.Encode()
	}
	resp, err := ts.client.Get(u)
	if err != nil {
		ts.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func (ts *testSetup) getJSON(path string, params map[string]string, out any) {
	ts.t.Helper()
	code, body := ts.get(path, params)
	if code != 200 {
		ts.t.Fatalf("GET %s: status %d, body: %s", path, code, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		ts.t.Fatalf("GET %s: unmarshal: %v\nbody: %s", path, err, body)
	}
}

func writeTestFile(t *testing.T, base, relPath, content string) {
	t.Helper()
	abs := filepath.Join(base, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// --- Tests ---

func TestWebDestinations(t *testing.T) {
	ts := newTestSetup(t)

	var dests []destJSON
	ts.getJSON("/api/destinations", nil, &dests)

	if len(dests) != 1 {
		t.Fatalf("expected 1 destination, got %d", len(dests))
	}
	if dests[0].Name != "local-test" {
		t.Errorf("expected dest name 'local-test', got %q", dests[0].Name)
	}
	if dests[0].Type != "local" {
		t.Errorf("expected dest type 'local', got %q", dests[0].Type)
	}
}

func TestWebSnapshots(t *testing.T) {
	ts := newTestSetup(t)

	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)

	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].Hostname != "test-host" {
		t.Errorf("expected hostname 'test-host', got %q", snaps[0].Hostname)
	}
	if snaps[0].TotalFiles < 4 {
		t.Errorf("expected >= 4 files, got %d", snaps[0].TotalFiles)
	}
}

func TestWebTree(t *testing.T) {
	ts := newTestSetup(t)

	// Get snapshot ID.
	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)
	snapID := snaps[0].ID

	// List root tree.
	var entries []treeEntryJSON
	ts.getJSON("/api/tree", map[string]string{
		"dest": "local-test", "snapshot": snapID, "path": "/",
	}, &entries)

	// Should have dirs and files from the source.
	if len(entries) == 0 {
		t.Fatal("expected tree entries, got none")
	}
	t.Logf("Root tree has %d entries", len(entries))
	for _, e := range entries {
		t.Logf("  %s (type=%s, dir=%v)", e.Name, e.Type, e.IsDir)
	}
}

func TestWebFileView(t *testing.T) {
	ts := newTestSetup(t)

	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)
	snapID := snaps[0].ID

	// Navigate to find hello.txt. The tree root contains the sourceDir path components.
	// Walk down to find the file.
	filePath := findFileInTree(t, ts, snapID, "hello.txt")
	if filePath == "" {
		t.Fatal("could not find hello.txt in snapshot tree")
	}
	t.Logf("Found hello.txt at path: %s", filePath)

	// View the file.
	var fileData map[string]any
	ts.getJSON("/api/file", map[string]string{
		"dest": "local-test", "snapshot": snapID, "path": filePath, "mode": "view",
	}, &fileData)

	if fileData["binary"] != false {
		t.Errorf("expected text file, got binary=%v", fileData["binary"])
	}
	content := fileData["content"].(string)
	if !strings.Contains(content, "Hello") {
		t.Errorf("expected content to contain 'Hello', got: %s", content[:min(len(content), 200)])
	}
}

func TestWebRestore(t *testing.T) {
	ts := newTestSetup(t)

	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)
	snapID := snaps[0].ID

	// Restore to a temp directory.
	restoreDir := t.TempDir()
	target := filepath.Join(restoreDir, "restored")

	// Call restore API (SSE stream).
	params := url.Values{
		"dest":     {"local-test"},
		"snapshot": {snapID},
		"target":   {target},
	}
	u := ts.baseURL + "/api/restore?" + params.Encode()
	resp, err := ts.client.Get(u)
	if err != nil {
		t.Fatalf("GET /api/restore: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("restore returned %d: %s", resp.StatusCode, body)
	}

	// Read SSE stream to completion.
	body, _ := io.ReadAll(resp.Body)
	sseData := string(body)
	t.Logf("SSE response:\n%s", sseData)

	if strings.Contains(sseData, "restore_error") {
		t.Fatalf("restore returned error in SSE stream")
	}
	if !strings.Contains(sseData, "restore_done") {
		t.Fatalf("restore did not complete - no restore_done event found")
	}

	// Verify files were actually restored.
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Fatalf("restore target %s does not exist", target)
	}

	// Collect restored files.
	restoredFiles := collectFiles(t, target)
	t.Logf("Restored %d files:", len(restoredFiles))
	for path := range restoredFiles {
		t.Logf("  %s", path)
	}

	if len(restoredFiles) == 0 {
		t.Fatal("no files were restored!")
	}

	// Check that hello.txt was restored with correct content.
	found := false
	for path, content := range restoredFiles {
		if strings.HasSuffix(path, "hello.txt") {
			found = true
			if string(content) != "Hello, world!\n" {
				t.Errorf("hello.txt content mismatch: got %q", string(content))
			}
		}
	}
	if !found {
		t.Error("hello.txt not found in restored files")
	}
}

func TestWebFind(t *testing.T) {
	ts := newTestSetup(t)

	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)
	snapID := snaps[0].ID

	var result map[string]any
	ts.getJSON("/api/find", map[string]string{
		"dest": "local-test", "snapshot": snapID, "pattern": "*.txt",
	}, &result)

	count := int(result["count"].(float64))
	if count < 2 {
		t.Errorf("expected at least 2 .txt matches, got %d", count)
	}
	t.Logf("Find *.txt returned %d matches", count)
}

func TestWebCompare(t *testing.T) {
	// Create a setup with two snapshots (different content).
	repoDir := t.TempDir()
	sourceDir := t.TempDir()

	writeTestFile(t, sourceDir, "same.txt", "unchanged content\n")
	writeTestFile(t, sourceDir, "modified.txt", "version 1\n")
	writeTestFile(t, sourceDir, "removed.txt", "will be removed\n")

	var masterKey crypto.MasterKey
	rand.Read(masterKey[:])

	ctx := context.Background()
	backend, _ := local.New(repoDir)
	r, _ := repo.Init(ctx, backend, masterKey)

	snap1, err := backup.Run(ctx, r, backup.Options{
		Paths: []string{sourceDir}, ConfigName: "test", Hostname: "test-host", CompressionLevel: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Modify files for second snapshot.
	writeTestFile(t, sourceDir, "modified.txt", "version 2 - bigger now\n")
	os.Remove(filepath.Join(sourceDir, "removed.txt"))
	writeTestFile(t, sourceDir, "added.txt", "new file\n")

	snap2, err := backup.Run(ctx, r, backup.Options{
		Paths: []string{sourceDir}, ConfigName: "test", Hostname: "test-host", CompressionLevel: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.Close()

	t.Logf("Snap A: %s, Snap B: %s", snap1.ID[:10], snap2.ID[:10])

	cfg := &config.Config{
		Key:          "unused",
		Sources:      []config.SourceConfig{{Path: sourceDir}},
		Destinations: []config.DestConfig{{Name: "local-test", Type: "local", Path: repoDir}},
	}
	session := views.NewSession(cfg)
	session.UnlockWithKey(masterKey)
	defer session.Close()

	srv, _ := New(session, "test")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go srv.Serve(ctx)

	client := &http.Client{Timeout: 10 * time.Second, Jar: &tokenJar{token: srv.token}}
	ts := &testSetup{t: t, baseURL: "http://" + srv.listener.Addr().String(), client: client}

	var result map[string]any
	ts.getJSON("/api/compare", map[string]string{
		"dest": "local-test", "a": snap1.ID, "b": snap2.ID,
	}, &result)

	entries := result["entries"].([]any)
	t.Logf("Compare returned %d changed entries", len(entries))

	statuses := make(map[string]string)
	for _, raw := range entries {
		e := raw.(map[string]any)
		path := e["path"].(string)
		status := e["status"].(string)
		statuses[path] = status
		t.Logf("  %s: %s (A=%v B=%v)", status, path, e["size_a_human"], e["size_b_human"])
	}

	// Verify expected changes.
	foundMod, foundAdd, foundRem := false, false, false
	for path, status := range statuses {
		if strings.HasSuffix(path, "modified.txt") && status == "modified" {
			foundMod = true
		}
		if strings.HasSuffix(path, "added.txt") && status == "added" {
			foundAdd = true
		}
		if strings.HasSuffix(path, "removed.txt") && status == "removed" {
			foundRem = true
		}
	}
	if !foundMod {
		t.Error("expected modified.txt to be 'modified'")
	}
	if !foundAdd {
		t.Error("expected added.txt to be 'added'")
	}
	if !foundRem {
		t.Error("expected removed.txt to be 'removed'")
	}
}

// --- Helpers ---

// findFileInTree recursively searches the snapshot tree for a file by name.
func findFileInTree(t *testing.T, ts *testSetup, snapID, filename string) string {
	t.Helper()
	return walkAPITree(t, ts, snapID, "/", filename, 0)
}

func walkAPITree(t *testing.T, ts *testSetup, snapID, path, filename string, depth int) string {
	t.Helper()
	if depth > 20 {
		return ""
	}

	var entries []treeEntryJSON
	ts.getJSON("/api/tree", map[string]string{
		"dest": "local-test", "snapshot": snapID, "path": path,
	}, &entries)

	for _, e := range entries {
		entryPath := path
		if !strings.HasSuffix(entryPath, "/") {
			entryPath += "/"
		}
		entryPath += e.Name

		if e.Name == filename && !e.IsDir {
			return entryPath
		}
		if e.IsDir {
			result := walkAPITree(t, ts, snapID, entryPath, filename, depth+1)
			if result != "" {
				return result
			}
		}
	}
	return ""
}

func TestWebAuthRequired(t *testing.T) {
	ts := newTestSetup(t)

	// Use a client without the auth cookie.
	noAuthClient := &http.Client{Timeout: 10 * time.Second}

	endpoints := []string{
		"/api/destinations",
		"/api/snapshots?dest=local-test",
		"/api/tree?dest=local-test&snapshot=abc&path=/",
		"/api/file?dest=local-test&snapshot=abc&path=/foo&mode=view",
		"/api/find?dest=local-test&snapshot=abc&pattern=*.txt",
		"/api/restore?dest=local-test&snapshot=abc&target=/tmp/x",
		"/api/compare?dest=local-test&a=abc&b=def",
	}
	for _, ep := range endpoints {
		resp, err := noAuthClient.Get(ts.baseURL + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without auth: expected 401, got %d", ep, resp.StatusCode)
		}
	}

	// Wrong token should also be rejected.
	wrongTokenClient := &http.Client{
		Timeout: 10 * time.Second,
		Jar:     &tokenJar{token: "wrong-token-value"},
	}
	resp, err := wrongTokenClient.Get(ts.baseURL + "/api/destinations")
	if err != nil {
		t.Fatalf("GET with wrong token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET with wrong token: expected 401, got %d", resp.StatusCode)
	}
}

func TestWebMissingParameters(t *testing.T) {
	ts := newTestSetup(t)

	tests := []struct {
		name   string
		path   string
		params map[string]string
		code   int
	}{
		{"snapshots missing dest", "/api/snapshots", nil, 400},
		{"tree missing dest", "/api/tree", map[string]string{"snapshot": "abc"}, 400},
		{"tree missing snapshot", "/api/tree", map[string]string{"dest": "local-test"}, 400},
		{"file missing params", "/api/file", nil, 400},
		{"find missing params", "/api/find", nil, 400},
		{"restore missing params", "/api/restore", nil, 400},
		{"compare missing params", "/api/compare", map[string]string{"dest": "local-test"}, 400},
		{"snapshots bad dest", "/api/snapshots", map[string]string{"dest": "nonexistent"}, 404},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := ts.get(tc.path, tc.params)
			if code != tc.code {
				t.Errorf("expected status %d, got %d", tc.code, code)
			}
		})
	}
}

func TestWebFileDownload(t *testing.T) {
	ts := newTestSetup(t)

	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)
	snapID := snaps[0].ID

	filePath := findFileInTree(t, ts, snapID, "hello.txt")
	if filePath == "" {
		t.Fatal("could not find hello.txt in snapshot tree")
	}

	// Download the file.
	u := ts.baseURL + "/api/file?" + (&url.URL{RawQuery: url.Values{
		"dest": {"local-test"}, "snapshot": {snapID}, "path": {filePath}, "mode": {"download"},
	}.Encode()}).RawQuery
	resp, err := ts.client.Get(u)
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("download returned %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %q", ct)
	}

	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "hello.txt") {
		t.Errorf("expected Content-Disposition to contain hello.txt, got %q", cd)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Hello, world!\n" {
		t.Errorf("download content mismatch: got %q", string(body))
	}
}

func TestWebTreePathNotFound(t *testing.T) {
	ts := newTestSetup(t)

	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)
	snapID := snaps[0].ID

	code, _ := ts.get("/api/tree", map[string]string{
		"dest": "local-test", "snapshot": snapID, "path": "/nonexistent/path",
	})
	if code != 404 {
		t.Errorf("expected 404 for nonexistent tree path, got %d", code)
	}
}

func TestWebFindInvalidPattern(t *testing.T) {
	ts := newTestSetup(t)

	var snaps []snapJSON
	ts.getJSON("/api/snapshots", map[string]string{"dest": "local-test"}, &snaps)
	snapID := snaps[0].ID

	code, _ := ts.get("/api/find", map[string]string{
		"dest": "local-test", "snapshot": snapID, "pattern": "[invalid",
	})
	if code != 400 {
		t.Errorf("expected 400 for invalid glob pattern, got %d", code)
	}
}

func collectFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
	return files
}
