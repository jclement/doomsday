// Package e2e provides end-to-end tests that exercise the full backup pipeline:
// init → backup → verify → restore → compare → incremental → prune → check.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/check"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/prune"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/restore"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
)

// testEnv bundles everything needed for E2E tests.
type testEnv struct {
	t         *testing.T
	ctx       context.Context
	repoDir   string
	sourceDir string
	masterKey crypto.MasterKey
	backend   types.Backend
	repo      *repo.Repository
}

// newTestEnv creates a fresh repo and source directory.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	repoDir := t.TempDir()
	sourceDir := t.TempDir()

	backend, err := local.New(repoDir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	var masterKey crypto.MasterKey
	if _, err := rand.Read(masterKey[:]); err != nil {
		t.Fatalf("generate master key: %v", err)
	}

	ctx := context.Background()
	r, err := repo.Init(ctx, backend, masterKey)
	if err != nil {
		t.Fatalf("repo.Init: %v", err)
	}

	return &testEnv{
		t:         t,
		ctx:       ctx,
		repoDir:   repoDir,
		sourceDir: sourceDir,
		masterKey: masterKey,
		backend:   backend,
		repo:      r,
	}
}

// reopenRepo closes and reopens the repository (simulates a new process).
func (e *testEnv) reopenRepo() {
	e.t.Helper()
	backend, err := local.New(e.repoDir)
	if err != nil {
		e.t.Fatalf("reopen local.New: %v", err)
	}
	e.backend = backend
	r, err := repo.Open(e.ctx, backend, e.masterKey)
	if err != nil {
		e.t.Fatalf("repo.Open: %v", err)
	}
	e.repo = r
}

// writeFile creates a file with the given content.
func (e *testEnv) writeFile(relPath, content string) {
	e.t.Helper()
	absPath := filepath.Join(e.sourceDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		e.t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		e.t.Fatalf("WriteFile %s: %v", relPath, err)
	}
}

// writeBinaryFile creates a file with pseudo-random binary data.
func (e *testEnv) writeBinaryFile(relPath string, size int) {
	e.t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i * 31 + 17) % 256)
	}
	absPath := filepath.Join(e.sourceDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		e.t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absPath, data, 0644); err != nil {
		e.t.Fatalf("WriteFile %s: %v", relPath, err)
	}
}

// writeSymlink creates a symlink.
func (e *testEnv) writeSymlink(relPath, target string) {
	e.t.Helper()
	absPath := filepath.Join(e.sourceDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		e.t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(target, absPath); err != nil {
		e.t.Fatalf("Symlink %s: %v", relPath, err)
	}
}

// backup runs a backup and returns the snapshot.
func (e *testEnv) backup(configName string) *snapshot.Snapshot {
	e.t.Helper()
	snap, err := backup.Run(e.ctx, e.repo, backup.Options{
		Paths:            []string{e.sourceDir},
		ConfigName:       configName,
		Hostname:         "test-host",
		CompressionLevel: 3,
	})
	if err != nil {
		e.t.Fatalf("backup.Run: %v", err)
	}
	return snap
}

// restore restores a snapshot to a temp directory and returns the path
// where the source files ended up (accounting for absolute paths in the tree).
func (e *testEnv) restore(snapshotID string) string {
	e.t.Helper()
	targetDir := e.t.TempDir()
	err := restore.Run(e.ctx, e.repo, snapshotID, targetDir, restore.Options{
		Overwrite: true,
	})
	if err != nil {
		e.t.Fatalf("restore.Run: %v", err)
	}
	// The tree stores absolute paths, so files are under targetDir + sourceDir.
	return filepath.Join(targetDir, e.sourceDir)
}

// collectFiles walks a directory and returns a map of relPath → content.
func collectFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // skip symlinks for content comparison
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	return files
}

// assertFilesEqual compares files from original and restored directories.
func assertFilesEqual(t *testing.T, original, restored map[string][]byte) {
	t.Helper()

	// Get all keys from both maps
	allKeys := make(map[string]bool)
	for k := range original {
		allKeys[k] = true
	}
	for k := range restored {
		allKeys[k] = true
	}

	for key := range allKeys {
		origData, origOK := original[key]
		restData, restOK := restored[key]

		if origOK && !restOK {
			t.Errorf("file %q exists in original but not in restored", key)
			continue
		}
		if !origOK && restOK {
			t.Errorf("file %q exists in restored but not in original", key)
			continue
		}
		if !bytes.Equal(origData, restData) {
			t.Errorf("file %q content mismatch: original %d bytes, restored %d bytes",
				key, len(origData), len(restData))
		}
	}
}

// TestE2EFullRoundtrip tests: init → backup → check → restore → compare.
func TestE2EFullRoundtrip(t *testing.T) {
	env := newTestEnv(t)

	// Create source files with varied content
	env.writeFile("hello.txt", "Hello, Doomsday!")
	env.writeFile("docs/readme.md", "# Doomsday\nBackup tool for the apocalypse.\n")
	env.writeFile("docs/notes.txt", "Some important notes about survival.\n")
	env.writeFile("config/settings.toml", "[general]\nwhimsy = true\n")
	env.writeBinaryFile("data/binary.dat", 4096)
	env.writeBinaryFile("data/large.bin", 2*1024*1024) // 2 MiB → multiple chunks
	env.writeFile("empty.txt", "")
	env.writeSymlink("link.txt", "hello.txt")

	// Capture original files
	originalFiles := collectFiles(t, env.sourceDir)

	// Backup
	snap := env.backup("test-config")
	t.Logf("Snapshot ID: %s", snap.ID)
	t.Logf("Summary: %d files, %d bytes, duration %v",
		snap.Summary.TotalFiles, snap.Summary.TotalSize, snap.Summary.Duration)

	if snap.Tree.IsZero() {
		t.Fatal("snapshot tree is zero")
	}
	if snap.Summary.TotalFiles == 0 {
		t.Error("no files in summary")
	}

	// Integrity check (all levels)
	for _, level := range []check.Level{check.LevelStructure, check.LevelHeaders, check.LevelFull} {
		report, err := check.Run(env.ctx, env.repo, level)
		if err != nil {
			t.Fatalf("check level %d: %v", level, err)
		}
		if !report.OK() {
			for _, e := range report.Errors {
				t.Errorf("check error: %s", e.Message)
			}
			t.Fatalf("integrity check level %d failed with %d errors", level, len(report.Errors))
		}
		t.Logf("Check level %d: %d packs, %d blobs, %d snapshots — OK",
			level, report.PacksChecked, report.BlobsChecked, report.SnapshotsChecked)
	}

	// Restore
	restoreDir := env.restore(snap.ID)
	restoredFiles := collectFiles(t, restoreDir)

	// Compare: restored files should be byte-identical to originals
	assertFilesEqual(t, originalFiles, restoredFiles)
	t.Logf("Restore verified: %d files byte-identical", len(originalFiles))
}

// TestE2EReopenRepo tests that closing and reopening a repo preserves data.
func TestE2EReopenRepo(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("persist.txt", "This should survive repo close/reopen")
	env.writeBinaryFile("binary.dat", 8192)

	snap := env.backup("persist-test")

	// Reopen the repo (simulates a new process)
	env.reopenRepo()

	// Verify snapshot is still accessible
	loaded, err := env.repo.LoadSnapshot(env.ctx, snap.ID)
	if err != nil {
		t.Fatalf("LoadSnapshot after reopen: %v", err)
	}
	if loaded.ID != snap.ID {
		t.Errorf("snapshot ID mismatch: got %q, want %q", loaded.ID, snap.ID)
	}

	// Restore and verify
	restoreDir := env.restore(snap.ID)
	originalFiles := collectFiles(t, env.sourceDir)
	restoredFiles := collectFiles(t, restoreDir)
	assertFilesEqual(t, originalFiles, restoredFiles)
}

// TestE2EIncrementalBackup tests dedup across multiple backups.
func TestE2EIncrementalBackup(t *testing.T) {
	env := newTestEnv(t)

	// Initial files
	env.writeFile("stable.txt", "This file never changes.")
	env.writeFile("changing.txt", "Version 1")
	env.writeBinaryFile("big.bin", 1024*1024) // 1 MiB

	// First backup
	snap1 := env.backup("incremental")
	idx1 := env.repo.Index().Len()
	t.Logf("Snap 1: %s, index entries: %d", snap1.ID, idx1)

	// Modify one file, add another
	env.writeFile("changing.txt", "Version 2 — apocalypse edition")
	env.writeFile("new_file.txt", "Brand new file from the wasteland")

	// Second backup
	snap2 := env.backup("incremental")
	idx2 := env.repo.Index().Len()
	t.Logf("Snap 2: %s, index entries: %d (grew by %d)", snap2.ID, idx2, idx2-idx1)

	if snap1.ID == snap2.ID {
		t.Error("both backups have the same snapshot ID")
	}

	// Index should grow (new chunks for modified/new files) but not double
	if idx2 <= idx1 {
		t.Error("index did not grow after modification — expected new chunks")
	}

	// Restore both snapshots and verify
	restore1 := env.restore(snap1.ID)
	restore2 := env.restore(snap2.ID)

	// Snap1 restore should have "Version 1"
	data1, err := os.ReadFile(filepath.Join(restore1, "changing.txt"))
	if err != nil {
		t.Fatalf("read changing.txt from snap1 restore: %v", err)
	}
	if string(data1) != "Version 1" {
		t.Errorf("snap1 changing.txt = %q, want %q", string(data1), "Version 1")
	}

	// Snap2 restore should have "Version 2"
	data2, err := os.ReadFile(filepath.Join(restore2, "changing.txt"))
	if err != nil {
		t.Fatalf("read changing.txt from snap2 restore: %v", err)
	}
	if string(data2) != "Version 2 — apocalypse edition" {
		t.Errorf("snap2 changing.txt = %q, want %q", string(data2), "Version 2 — apocalypse edition")
	}

	// Snap2 should have the new file
	newData, err := os.ReadFile(filepath.Join(restore2, "new_file.txt"))
	if err != nil {
		t.Fatalf("read new_file.txt from snap2 restore: %v", err)
	}
	if string(newData) != "Brand new file from the wasteland" {
		t.Errorf("snap2 new_file.txt = %q", string(newData))
	}

	// Snap1 should NOT have the new file
	if _, err := os.ReadFile(filepath.Join(restore1, "new_file.txt")); err == nil {
		t.Error("snap1 should not contain new_file.txt")
	}

	// Stable file should be identical in both
	stable1, _ := os.ReadFile(filepath.Join(restore1, "stable.txt"))
	stable2, _ := os.ReadFile(filepath.Join(restore2, "stable.txt"))
	if !bytes.Equal(stable1, stable2) {
		t.Error("stable.txt content differs between snapshots")
	}
}

// TestE2EMultipleSnapshots tests listing and managing multiple snapshots.
func TestE2EMultipleSnapshots(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("base.txt", "base content")

	// Create several snapshots
	var snapIDs []string
	for i := 0; i < 5; i++ {
		env.writeFile(fmt.Sprintf("file_%d.txt", i), fmt.Sprintf("content %d", i))
		snap := env.backup("multi")
		snapIDs = append(snapIDs, snap.ID)
	}

	// List snapshots
	listed, err := env.repo.ListSnapshots(env.ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}

	if len(listed) != 5 {
		t.Errorf("expected 5 snapshots, got %d", len(listed))
	}

	// All our snapshot IDs should be in the list
	listedMap := make(map[string]bool)
	for _, id := range listed {
		listedMap[id] = true
	}
	for _, id := range snapIDs {
		if !listedMap[id] {
			t.Errorf("snapshot %s not found in list", id)
		}
	}

	// Each snapshot should be independently loadable and restorable
	for i, id := range snapIDs {
		snap, err := env.repo.LoadSnapshot(env.ctx, id)
		if err != nil {
			t.Errorf("LoadSnapshot %s: %v", id, err)
			continue
		}
		if snap.BackupConfigName != "multi" {
			t.Errorf("snap %d config = %q, want %q", i, snap.BackupConfigName, "multi")
		}

		// Restore and check the expected file exists
		restoreDir := env.restore(id)
		expectedFile := filepath.Join(restoreDir, fmt.Sprintf("file_%d.txt", i))
		data, err := os.ReadFile(expectedFile)
		if err != nil {
			t.Errorf("snap %d: missing file_%d.txt after restore: %v", i, i, err)
			continue
		}
		expected := fmt.Sprintf("content %d", i)
		if string(data) != expected {
			t.Errorf("snap %d: file_%d.txt = %q, want %q", i, i, string(data), expected)
		}
	}
}

// TestE2EPruneRetention tests snapshot pruning with retention policies.
func TestE2EPruneRetention(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("data.txt", "some data")

	// Create 10 snapshots
	var snaps []*snapshot.Snapshot
	for i := 0; i < 10; i++ {
		env.writeFile("counter.txt", fmt.Sprintf("iteration %d", i))
		snap := env.backup("prune-test")
		snaps = append(snaps, snap)
	}

	// Apply retention: keep last 3
	keep, forget := prune.ApplyPolicy(snaps, prune.Policy{
		KeepLast: 3,
	})

	if len(keep) != 3 {
		t.Errorf("expected 3 kept, got %d", len(keep))
	}
	if len(forget) != 7 {
		t.Errorf("expected 7 forgotten, got %d", len(forget))
	}

	// The 3 most recent should be kept
	sort.Slice(keep, func(i, j int) bool {
		return keep[i].Time.After(keep[j].Time)
	})
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Time.After(snaps[j].Time)
	})
	for i := 0; i < 3; i++ {
		if keep[i].ID != snaps[i].ID {
			t.Errorf("kept[%d] = %s, want %s", i, keep[i].ID, snaps[i].ID)
		}
	}

	// Verify the kept snapshots can still be restored
	for _, k := range keep {
		restoreDir := env.restore(k.ID)
		data, err := os.ReadFile(filepath.Join(restoreDir, "data.txt"))
		if err != nil {
			t.Errorf("restore kept snapshot %s: %v", k.ID, err)
			continue
		}
		if string(data) != "some data" {
			t.Errorf("data.txt content mismatch in kept snapshot %s", k.ID)
		}
	}
}

// TestE2EPartialRestore tests restoring a subset of files.
func TestE2EPartialRestore(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("docs/readme.md", "# README")
	env.writeFile("docs/guide.txt", "User guide content")
	env.writeFile("src/main.go", "package main")
	env.writeFile("src/util.go", "package main\nfunc util() {}")
	env.writeFile("root.txt", "root file")

	snap := env.backup("partial")

	// Restore only the docs/ subtree.
	// The tree stores absolute paths, so the include path must be
	// relative to the tree root (i.e. the full sourceDir path without
	// leading slash, plus "/docs").
	docsTreePath := filepath.Join(strings.TrimPrefix(env.sourceDir, "/"), "docs")
	targetDir := t.TempDir()
	err := restore.Run(env.ctx, env.repo, snap.ID, targetDir, restore.Options{
		IncludePaths: []string{docsTreePath},
		Overwrite:    true,
	})
	if err != nil {
		t.Fatalf("partial restore: %v", err)
	}

	// Files are restored under targetDir + sourceDir (absolute path preserved).
	effectiveDir := filepath.Join(targetDir, env.sourceDir)

	// docs/ files should exist
	if data, err := os.ReadFile(filepath.Join(effectiveDir, "docs", "readme.md")); err != nil {
		t.Errorf("docs/readme.md missing: %v", err)
	} else if string(data) != "# README" {
		t.Errorf("docs/readme.md = %q", string(data))
	}

	if _, err := os.ReadFile(filepath.Join(effectiveDir, "docs", "guide.txt")); err != nil {
		t.Errorf("docs/guide.txt missing: %v", err)
	}

	// src/ and root.txt should NOT exist
	if _, err := os.ReadFile(filepath.Join(effectiveDir, "src", "main.go")); err == nil {
		t.Error("src/main.go should not exist in partial restore")
	}
	if _, err := os.ReadFile(filepath.Join(effectiveDir, "root.txt")); err == nil {
		t.Error("root.txt should not exist in partial restore")
	}
}

// TestE2ETreeBrowsing tests loading and traversing snapshot trees.
func TestE2ETreeBrowsing(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("alpha.txt", "alpha")
	env.writeFile("subdir/beta.txt", "beta")
	env.writeFile("subdir/deep/gamma.txt", "gamma")
	env.writeSymlink("link.txt", "alpha.txt")

	snap := env.backup("tree-browse")

	// Load root tree and walk down to the source directory level.
	// The tree stores absolute paths, so the root tree starts from the
	// filesystem root. We need to descend through each path component
	// of env.sourceDir to reach the actual source files.
	currentRef := snap.Tree
	// Strip leading slash and split into components.
	relSource := strings.TrimPrefix(env.sourceDir, "/")
	parts := strings.Split(relSource, "/")
	for _, part := range parts {
		blob, err := env.repo.LoadBlob(env.ctx, currentRef)
		if err != nil {
			t.Fatalf("LoadBlob for path component %q: %v", part, err)
		}
		tr, err := tree.Unmarshal(blob)
		if err != nil {
			t.Fatalf("tree.Unmarshal for path component %q: %v", part, err)
		}
		found := false
		for _, n := range tr.Nodes {
			if n.Name == part {
				if n.Subtree.IsZero() {
					t.Fatalf("path component %q has no subtree", part)
				}
				currentRef = n.Subtree
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("path component %q not found in tree", part)
		}
	}

	// Now currentRef points to the source directory tree.
	rootBlob, err := env.repo.LoadBlob(env.ctx, currentRef)
	if err != nil {
		t.Fatalf("LoadBlob source tree: %v", err)
	}
	rootTree, err := tree.Unmarshal(rootBlob)
	if err != nil {
		t.Fatalf("tree.Unmarshal source: %v", err)
	}

	// Check source directory entries
	names := make(map[string]tree.NodeType)
	for _, node := range rootTree.Nodes {
		names[node.Name] = node.Type
	}

	if names["alpha.txt"] != tree.NodeTypeFile {
		t.Errorf("alpha.txt: got type %v, want File", names["alpha.txt"])
	}
	if names["link.txt"] != tree.NodeTypeSymlink {
		t.Errorf("link.txt: got type %v, want Symlink", names["link.txt"])
	}
	if names["subdir"] != tree.NodeTypeDir {
		t.Errorf("subdir: got type %v, want Dir", names["subdir"])
	}

	// Traverse into subdir
	var subdirNode tree.Node
	for _, n := range rootTree.Nodes {
		if n.Name == "subdir" {
			subdirNode = n
			break
		}
	}
	if subdirNode.Subtree.IsZero() {
		t.Fatal("subdir has no subtree reference")
	}

	subdirBlob, err := env.repo.LoadBlob(env.ctx, subdirNode.Subtree)
	if err != nil {
		t.Fatalf("LoadBlob subdir tree: %v", err)
	}
	subdirTree, err := tree.Unmarshal(subdirBlob)
	if err != nil {
		t.Fatalf("tree.Unmarshal subdir: %v", err)
	}

	subdirNames := make(map[string]tree.NodeType)
	for _, node := range subdirTree.Nodes {
		subdirNames[node.Name] = node.Type
	}

	if subdirNames["beta.txt"] != tree.NodeTypeFile {
		t.Errorf("beta.txt missing from subdir tree")
	}
	if subdirNames["deep"] != tree.NodeTypeDir {
		t.Errorf("deep/ missing from subdir tree")
	}
}

// TestE2EEmptyFiles tests backup and restore of empty files.
func TestE2EEmptyFiles(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("empty.txt", "")
	env.writeFile("also_empty.dat", "")
	env.writeFile("not_empty.txt", "has content")

	snap := env.backup("empty-files")

	restoreDir := env.restore(snap.ID)

	// Empty files should be restored as empty
	for _, name := range []string{"empty.txt", "also_empty.dat"} {
		data, err := os.ReadFile(filepath.Join(restoreDir, name))
		if err != nil {
			t.Errorf("missing %s: %v", name, err)
			continue
		}
		if len(data) != 0 {
			t.Errorf("%s should be empty, got %d bytes", name, len(data))
		}
	}

	// Non-empty file should have content
	data, err := os.ReadFile(filepath.Join(restoreDir, "not_empty.txt"))
	if err != nil {
		t.Fatalf("missing not_empty.txt: %v", err)
	}
	if string(data) != "has content" {
		t.Errorf("not_empty.txt = %q", string(data))
	}
}

// TestE2ESymlinks tests backup and restore of symlinks.
func TestE2ESymlinks(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("target.txt", "I am the target")
	env.writeSymlink("relative_link", "target.txt")

	snap := env.backup("symlinks")
	restoreDir := env.restore(snap.ID)

	// Check relative symlink
	target, err := os.Readlink(filepath.Join(restoreDir, "relative_link"))
	if err != nil {
		t.Fatalf("Readlink relative_link: %v", err)
	}
	if target != "target.txt" {
		t.Errorf("relative_link target = %q, want %q", target, "target.txt")
	}
}

// TestE2ESymlinks_AbsoluteRejected tests that absolute symlinks are rejected
// during restore as a security measure against symlink-based path traversal.
func TestE2ESymlinks_AbsoluteRejected(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("target.txt", "I am the target")
	env.writeSymlink("absolute_link", "/nonexistent/absolute/path")

	snap := env.backup("symlinks-abs")

	// Restore should fail because absolute symlink targets are rejected.
	restoreDir := t.TempDir()
	err := restore.Run(context.Background(), env.repo, snap.ID, restoreDir, restore.Options{})
	if err == nil {
		t.Fatal("expected restore to reject absolute symlink target")
	}
	if !strings.Contains(err.Error(), "absolute symlink target") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestE2ELargeFileChunking tests that large files are properly chunked and restored.
func TestE2ELargeFileChunking(t *testing.T) {
	env := newTestEnv(t)

	// Create a file large enough for multiple chunks (> 8 MiB → at least 1 chunk)
	size := 10 * 1024 * 1024 // 10 MiB
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*7 + 13) % 256)
	}
	absPath := filepath.Join(env.sourceDir, "huge.bin")
	if err := os.WriteFile(absPath, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	snap := env.backup("large-chunk")

	if snap.Summary.TotalSize < int64(size) {
		t.Errorf("TotalSize = %d, want >= %d", snap.Summary.TotalSize, size)
	}

	restoreDir := env.restore(snap.ID)

	restored, err := os.ReadFile(filepath.Join(restoreDir, "huge.bin"))
	if err != nil {
		t.Fatalf("read restored huge.bin: %v", err)
	}

	if !bytes.Equal(data, restored) {
		t.Errorf("huge.bin content mismatch: original %d bytes, restored %d bytes", len(data), len(restored))
	}
}

// TestE2ECheckAfterBackup verifies that check passes at all levels after backup.
func TestE2ECheckAfterBackup(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("test.txt", "check me")
	env.writeBinaryFile("data.bin", 65536)

	env.backup("check-test")

	// Run check at all levels
	for _, level := range []check.Level{check.LevelStructure, check.LevelHeaders, check.LevelFull} {
		report, err := check.Run(env.ctx, env.repo, level)
		if err != nil {
			t.Fatalf("check level %d: %v", level, err)
		}
		if !report.OK() {
			for _, e := range report.Errors {
				t.Errorf("check level %d error: %s (blob=%s, pack=%s)",
					level, e.Message, e.BlobID, e.Pack)
			}
			t.Fatalf("check level %d failed", level)
		}
	}
}

// TestE2ECheckAfterReopen verifies check works after reopening the repo.
func TestE2ECheckAfterReopen(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("survive.txt", "across reopens")
	env.writeBinaryFile("data.bin", 32768)
	env.backup("reopen-check")

	// Reopen
	env.reopenRepo()

	report, err := check.Run(env.ctx, env.repo, check.LevelFull)
	if err != nil {
		t.Fatalf("check after reopen: %v", err)
	}
	if !report.OK() {
		for _, e := range report.Errors {
			t.Errorf("error: %s", e.Message)
		}
		t.Fatal("check failed after reopen")
	}
}

// TestE2EConfigParsing tests that a realistic config file parses correctly.
func TestE2EConfigParsing(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.yaml")

	configContent := fmt.Sprintf(`
key: "file:%s/master.key"

sources:
  - /home/user
exclude:
  - "*.tmp"
  - ".cache/"
schedule: hourly

retention:
  keep_last: 5
  keep_daily: 7
  keep_weekly: 4
  keep_monthly: 12
  keep_yearly: -1

destinations:
  - name: local_backup
    type: local
    path: "%s/repo"

settings:
  compression: zstd
  compression_level: 3
  whimsy: true
`, configDir, configDir)

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Verify structure
	if cfg.Schedule != "hourly" {
		t.Errorf("schedule = %q, want %q", cfg.Schedule, "hourly")
	}
	if cfg.Retention.KeepLast != 5 {
		t.Errorf("keep_last = %d, want 5", cfg.Retention.KeepLast)
	}
	if cfg.Retention.KeepYearly != -1 {
		t.Errorf("keep_yearly = %d, want -1", cfg.Retention.KeepYearly)
	}
	if len(cfg.Exclude) != 2 {
		t.Errorf("excludes = %v, want 2 entries", cfg.Exclude)
	}

	// Verify destination
	if len(cfg.Destinations) != 1 {
		t.Fatalf("expected 1 destination, got %d", len(cfg.Destinations))
	}
	if cfg.Destinations[0].Name != "local_backup" {
		t.Errorf("destination name = %q, want %q", cfg.Destinations[0].Name, "local_backup")
	}

	// Whimsy should be enabled
	if !cfg.WhimsyEnabled() {
		t.Error("whimsy should be enabled")
	}
}

// TestE2EExcludePatterns tests that exclude patterns work during backup.
func TestE2EExcludePatterns(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("keep.txt", "keep this")
	env.writeFile("exclude.tmp", "exclude this")
	env.writeFile("subdir/keep.go", "keep go file")
	env.writeFile("subdir/exclude.log", "exclude log")
	env.writeFile(".cache/cached.dat", "exclude cache")

	snap, err := backup.Run(env.ctx, env.repo, backup.Options{
		Paths:            []string{env.sourceDir},
		ConfigName:       "exclude-test",
		Hostname:         "test",
		CompressionLevel: 3,
		Excludes:         []string{"*.tmp", "*.log", ".cache/"},
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	// Restore and check
	restoreDir := env.restore(snap.ID)

	// Kept files should exist
	if _, err := os.ReadFile(filepath.Join(restoreDir, "keep.txt")); err != nil {
		t.Error("keep.txt should exist")
	}
	if _, err := os.ReadFile(filepath.Join(restoreDir, "subdir", "keep.go")); err != nil {
		t.Error("subdir/keep.go should exist")
	}

	// Excluded files should not exist
	if _, err := os.ReadFile(filepath.Join(restoreDir, "exclude.tmp")); err == nil {
		t.Error("exclude.tmp should have been excluded")
	}
	if _, err := os.ReadFile(filepath.Join(restoreDir, "subdir", "exclude.log")); err == nil {
		t.Error("subdir/exclude.log should have been excluded")
	}
}

// TestE2EWrongPassword tests that opening a repo with wrong password fails.
func TestE2EWrongPassword(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("secret.txt", "classified")
	env.backup("secret")

	// Try opening with a different master key
	var wrongKey crypto.MasterKey
	for i := range wrongKey {
		wrongKey[i] = byte(255 - i)
	}

	backend, err := local.New(env.repoDir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	_, err = repo.Open(env.ctx, backend, wrongKey)
	if err == nil {
		t.Fatal("expected error opening repo with wrong key, got nil")
	}
	t.Logf("Correctly rejected wrong key: %v", err)
}

// TestE2EDeletedFileBetweenBackups tests that deleted files are not in new snapshots
// but are preserved in old snapshots.
func TestE2EDeletedFileBetweenBackups(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("permanent.txt", "always here")
	env.writeFile("ephemeral.txt", "will be deleted")

	snap1 := env.backup("deletion")

	// Delete a file
	os.Remove(filepath.Join(env.sourceDir, "ephemeral.txt"))

	snap2 := env.backup("deletion")

	// Restore snap1: both files should exist
	restore1 := env.restore(snap1.ID)
	if _, err := os.ReadFile(filepath.Join(restore1, "ephemeral.txt")); err != nil {
		t.Error("snap1 should have ephemeral.txt")
	}
	if _, err := os.ReadFile(filepath.Join(restore1, "permanent.txt")); err != nil {
		t.Error("snap1 should have permanent.txt")
	}

	// Restore snap2: only permanent.txt should exist
	restore2 := env.restore(snap2.ID)
	if _, err := os.ReadFile(filepath.Join(restore2, "permanent.txt")); err != nil {
		t.Error("snap2 should have permanent.txt")
	}
	if _, err := os.ReadFile(filepath.Join(restore2, "ephemeral.txt")); err == nil {
		t.Error("snap2 should NOT have ephemeral.txt")
	}
}

// TestE2EDryRunRestore tests that dry-run restore does not write files.
func TestE2EDryRunRestore(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("test.txt", "dry run data")
	snap := env.backup("dryrun")

	targetDir := t.TempDir()
	var progressCalls int
	err := restore.Run(env.ctx, env.repo, snap.ID, targetDir, restore.Options{
		DryRun: true,
		OnProgress: func(ev restore.ProgressEvent) {
			progressCalls++
			if !ev.IsDryRun {
				t.Error("progress event should have IsDryRun=true")
			}
		},
	})
	if err != nil {
		t.Fatalf("dry-run restore: %v", err)
	}

	if progressCalls == 0 {
		t.Error("no progress callbacks during dry-run")
	}

	// Target directory should be empty (no files written)
	entries, _ := os.ReadDir(targetDir)
	if len(entries) != 0 {
		t.Errorf("dry-run should not write files, found %d entries", len(entries))
	}
}

// TestE2ESnapshotMetadata verifies snapshot metadata is correctly stored and retrieved.
func TestE2ESnapshotMetadata(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("meta.txt", "metadata test")
	env.writeBinaryFile("data.bin", 2048)

	snap, err := backup.Run(env.ctx, env.repo, backup.Options{
		Paths:            []string{env.sourceDir},
		ConfigName:       "meta-config",
		Hostname:         "apocalypse-bunker",
		CompressionLevel: 3,
		Tags:             []string{"weekly", "important"},
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	// Verify fields
	if snap.Hostname != "apocalypse-bunker" {
		t.Errorf("hostname = %q", snap.Hostname)
	}
	if snap.BackupConfigName != "meta-config" {
		t.Errorf("config name = %q", snap.BackupConfigName)
	}
	if len(snap.Tags) != 2 || snap.Tags[0] != "weekly" || snap.Tags[1] != "important" {
		t.Errorf("tags = %v", snap.Tags)
	}
	if snap.Summary == nil {
		t.Fatal("summary is nil")
	}
	if snap.Summary.Duration <= 0 {
		t.Error("duration should be positive")
	}
	if snap.Time.IsZero() {
		t.Error("time should not be zero")
	}
	if time.Since(snap.Time) > 10*time.Second {
		t.Errorf("snapshot time is too old: %v", snap.Time)
	}

	// Reload and verify persistence
	loaded, err := env.repo.LoadSnapshot(env.ctx, snap.ID)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if loaded.Hostname != snap.Hostname {
		t.Errorf("loaded hostname = %q", loaded.Hostname)
	}
	if loaded.BackupConfigName != snap.BackupConfigName {
		t.Errorf("loaded config = %q", loaded.BackupConfigName)
	}
	if len(loaded.Tags) != 2 {
		t.Errorf("loaded tags = %v", loaded.Tags)
	}
}

// TestE2EDeepDirectoryStructure tests backup/restore of deeply nested directories.
func TestE2EDeepDirectoryStructure(t *testing.T) {
	env := newTestEnv(t)

	// Create a deeply nested structure
	deepPath := strings.Join([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, string(filepath.Separator))
	env.writeFile(filepath.Join(deepPath, "deep.txt"), "found at the bottom")
	env.writeFile("top.txt", "at the top")

	snap := env.backup("deep")
	restoreDir := env.restore(snap.ID)

	// Check deep file
	data, err := os.ReadFile(filepath.Join(restoreDir, deepPath, "deep.txt"))
	if err != nil {
		t.Fatalf("missing deep file: %v", err)
	}
	if string(data) != "found at the bottom" {
		t.Errorf("deep.txt = %q", string(data))
	}

	// Check top file
	data, err = os.ReadFile(filepath.Join(restoreDir, "top.txt"))
	if err != nil {
		t.Fatalf("missing top file: %v", err)
	}
	if string(data) != "at the top" {
		t.Errorf("top.txt = %q", string(data))
	}
}

// TestE2EUnicodeFilenames tests backup/restore of files with unicode names.
func TestE2EUnicodeFilenames(t *testing.T) {
	env := newTestEnv(t)

	env.writeFile("日本語.txt", "Japanese text")
	env.writeFile("émojis_🎉.txt", "party time")
	env.writeFile("spaces in name.txt", "has spaces")
	env.writeFile("special-chars_[1].txt", "brackets")

	originalFiles := collectFiles(t, env.sourceDir)
	snap := env.backup("unicode")
	restoreDir := env.restore(snap.ID)
	restoredFiles := collectFiles(t, restoreDir)

	assertFilesEqual(t, originalFiles, restoredFiles)
}

// TestE2ERestoreProgress tests that restore progress callbacks work correctly.
func TestE2ERestoreProgress(t *testing.T) {
	env := newTestEnv(t)

	for i := 0; i < 5; i++ {
		env.writeFile(fmt.Sprintf("file_%d.txt", i), fmt.Sprintf("content for file %d", i))
	}

	snap := env.backup("progress")

	var events []restore.ProgressEvent
	targetDir := t.TempDir()
	err := restore.Run(env.ctx, env.repo, snap.ID, targetDir, restore.Options{
		Overwrite: true,
		OnProgress: func(ev restore.ProgressEvent) {
			events = append(events, ev)
		},
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if len(events) == 0 {
		t.Fatal("no progress events received")
	}

	// Last event should have all files completed
	last := events[len(events)-1]
	if last.FilesCompleted != last.FilesTotal {
		t.Errorf("final: completed=%d, total=%d", last.FilesCompleted, last.FilesTotal)
	}
	t.Logf("Restore progress: %d events, %d files total", len(events), last.FilesTotal)
}
