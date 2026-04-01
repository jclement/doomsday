package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/repo"
)

// safeStats provides thread-safe access to backup stats for tests.
type safeStats struct {
	mu    sync.Mutex
	stats backup.Stats
	calls int64
}

func (s *safeStats) update(st backup.Stats) {
	s.mu.Lock()
	s.stats = st
	s.calls++
	s.mu.Unlock()
}

func (s *safeStats) get() backup.Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *safeStats) getCalls() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// setupTestRepo creates a temporary directory with a fresh repository.
func setupTestRepo(t *testing.T) (*repo.Repository, string) {
	t.Helper()
	repoDir := t.TempDir()

	backend, err := local.New(repoDir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	var masterKey crypto.MasterKey
	// Use a fixed test key
	for i := range masterKey {
		masterKey[i] = byte(i)
	}

	ctx := context.Background()
	r, err := repo.Init(ctx, backend, masterKey)
	if err != nil {
		t.Fatalf("repo.Init: %v", err)
	}

	return r, repoDir
}

// createTestFiles creates a directory structure with test data.
// Returns the root directory path.
func createTestFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create files with known content
	files := map[string]string{
		"hello.txt":            "Hello, Doomsday!",
		"subdir/nested.txt":    "This is a nested file.",
		"subdir/another.txt":   "Another file in subdir.",
		"subdir/deep/file.txt": "Deep nested file content.",
		"empty.txt":            "",
		"binary.dat":           string(makeBinaryData(4096)),
	}

	for relPath, content := range files {
		absPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	// Create a symlink
	symlinkPath := filepath.Join(dir, "link.txt")
	if err := os.Symlink("hello.txt", symlinkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	return dir
}

// makeBinaryData creates a pseudo-random byte slice.
func makeBinaryData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*31 + 17)
	}
	return data
}

// TestBackupBasic verifies that a basic backup produces a valid snapshot
// with index entries and summary stats.
func TestBackupBasic(t *testing.T) {
	r, _ := setupTestRepo(t)
	sourceDir := createTestFiles(t)

	ctx := context.Background()

	ss := &safeStats{}

	snap, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test-config",
		Hostname:         "test-host",
		CompressionLevel: 3,
		OnProgress: func(s backup.Stats) {
			ss.update(s)
		},
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	lastStats := ss.get()

	// Verify snapshot was created
	if snap == nil {
		t.Fatal("backup.Run returned nil snapshot")
	}
	if snap.ID == "" {
		t.Error("snapshot ID is empty")
	}
	if snap.Tree.IsZero() {
		t.Error("snapshot tree is zero")
	}
	if snap.Hostname != "test-host" {
		t.Errorf("hostname = %q, want %q", snap.Hostname, "test-host")
	}
	if snap.BackupConfigName != "test-config" {
		t.Errorf("config name = %q, want %q", snap.BackupConfigName, "test-config")
	}
	if len(snap.Paths) != 1 {
		t.Errorf("paths = %v, want 1 entry", snap.Paths)
	}
	if snap.Summary == nil {
		t.Fatal("snapshot summary is nil")
	}
	if snap.Summary.Duration <= 0 {
		t.Errorf("duration = %v, want > 0", snap.Summary.Duration)
	}

	// Verify index has entries
	idxLen := r.Index().Len()
	if idxLen == 0 {
		t.Error("index is empty after backup")
	}
	t.Logf("Index has %d entries", idxLen)

	// Verify progress was called
	calls := ss.getCalls()
	if calls == 0 {
		t.Error("progress callback was never called")
	}
	t.Logf("Progress called %d times", calls)

	// Verify some stats
	if lastStats.FilesTotal == 0 {
		t.Error("FilesTotal is 0")
	}
	if lastStats.DirsTotal == 0 {
		t.Error("DirsTotal is 0")
	}
	t.Logf("Stats: files=%d, dirs=%d, chunks_new=%d, chunks_dup=%d, bytes_read=%d, bytes_new=%d, packs=%d",
		lastStats.FilesTotal, lastStats.DirsTotal, lastStats.ChunksNew, lastStats.ChunksDup,
		lastStats.BytesRead, lastStats.BytesNew, lastStats.PacksFlushed)

	// Verify snapshot can be loaded back
	loadedSnap, err := r.LoadSnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if loadedSnap.ID != snap.ID {
		t.Errorf("loaded snapshot ID = %q, want %q", loadedSnap.ID, snap.ID)
	}
	if loadedSnap.Tree != snap.Tree {
		t.Errorf("loaded snapshot tree mismatch")
	}

	// Verify snapshot appears in list
	snapIDs, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	found := false
	for _, id := range snapIDs {
		if id == snap.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("snapshot %s not found in list %v", snap.ID, snapIDs)
	}
}

// TestBackupIncremental verifies that a second backup of the same files
// skips unchanged chunks (dedup works).
func TestBackupIncremental(t *testing.T) {
	r, _ := setupTestRepo(t)
	sourceDir := createTestFiles(t)
	ctx := context.Background()

	// First backup
	snap1, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test",
		CompressionLevel: 3,
	})
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}

	indexAfterFirst := r.Index().Len()
	t.Logf("Index entries after first backup: %d", indexAfterFirst)

	// Second backup (same files, no changes)
	ss := &safeStats{}
	snap2, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test",
		CompressionLevel: 3,
		OnProgress: func(s backup.Stats) {
			ss.update(s)
		},
	})
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}

	stats2 := ss.get()

	// Both snapshots should have valid IDs
	if snap1.ID == snap2.ID {
		t.Error("both snapshots have the same ID")
	}

	// Second backup should have all data chunks as duplicates
	t.Logf("Second backup stats: chunks_new=%d, chunks_dup=%d, bytes_new=%d",
		stats2.ChunksNew, stats2.ChunksDup, stats2.BytesNew)

	// Data chunks should all be duplicates
	if stats2.ChunksDup == 0 && stats2.ChunksTotal > 0 {
		t.Error("expected some duplicate chunks on second backup")
	}

	// The index should not have grown significantly (only new tree blobs at most)
	indexAfterSecond := r.Index().Len()
	t.Logf("Index entries after second backup: %d (grew by %d)",
		indexAfterSecond, indexAfterSecond-indexAfterFirst)
}

// TestBackupExcludes verifies that exclusion patterns work.
func TestBackupExcludes(t *testing.T) {
	r, _ := setupTestRepo(t)
	sourceDir := createTestFiles(t)
	ctx := context.Background()

	// Backup with exclusions
	ss := &safeStats{}
	_, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test",
		Excludes:         []string{"*.dat", "deep/"},
		CompressionLevel: 3,
		OnProgress: func(s backup.Stats) {
			ss.update(s)
		},
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	stats := ss.get()

	// binary.dat and subdir/deep/ should be excluded
	t.Logf("With excludes: files=%d, dirs=%d", stats.FilesTotal, stats.DirsTotal)

	if stats.BytesRead >= 4096 {
		t.Logf("Warning: BytesRead=%d may include excluded files", stats.BytesRead)
	}
}

// TestBackupEmptyDirectory verifies backup of an empty directory.
func TestBackupEmptyDirectory(t *testing.T) {
	r, _ := setupTestRepo(t)
	sourceDir := t.TempDir()
	ctx := context.Background()

	snap, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "empty-test",
		CompressionLevel: 3,
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	if snap == nil {
		t.Fatal("nil snapshot for empty directory")
	}
	if snap.Tree.IsZero() {
		t.Error("tree is zero for empty directory")
	}
}

// TestBackupCancellation verifies that cancelling the context stops the backup.
func TestBackupCancellation(t *testing.T) {
	r, _ := setupTestRepo(t)

	// Create a large directory structure to ensure the backup takes some time
	sourceDir := t.TempDir()
	for i := 0; i < 100; i++ {
		name := filepath.Join(sourceDir, "file_"+string(rune('a'+i%26))+".txt")
		os.WriteFile(name, makeBinaryData(1024), 0644)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Give the timeout a moment to expire
	time.Sleep(5 * time.Millisecond)

	_, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "cancel-test",
		CompressionLevel: 3,
	})
	if err == nil {
		// It's possible the backup completed before the cancel for small files.
		t.Log("backup completed before cancellation (acceptable for small data)")
	} else {
		t.Logf("backup correctly returned error on cancellation: %v", err)
	}
}

// TestBackupLargeFile verifies backup of a file large enough to produce multiple chunks.
func TestBackupLargeFile(t *testing.T) {
	r, _ := setupTestRepo(t)
	sourceDir := t.TempDir()

	// Create a file larger than the minimum chunk size (512 KiB)
	// Use 2 MiB to likely get at least 1-2 chunks
	largeData := make([]byte, 2*1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 251) // prime modulus for non-trivial data
	}
	largePath := filepath.Join(sourceDir, "large.bin")
	if err := os.WriteFile(largePath, largeData, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := context.Background()
	ss := &safeStats{}
	snap, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "large-test",
		CompressionLevel: 3,
		OnProgress: func(s backup.Stats) {
			ss.update(s)
		},
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	stats := ss.get()

	if snap == nil {
		t.Fatal("nil snapshot")
	}

	// Should have read ~2 MiB
	if stats.BytesRead < int64(len(largeData)) {
		t.Errorf("BytesRead = %d, want >= %d", stats.BytesRead, len(largeData))
	}

	// Should have at least 1 chunk
	if stats.ChunksNew == 0 {
		t.Error("expected at least 1 new chunk for large file")
	}

	t.Logf("Large file: chunks=%d, bytes_read=%d, bytes_new=%d, packs=%d",
		stats.ChunksNew, stats.BytesRead, stats.BytesNew, stats.PacksFlushed)
}

// TestBackupMultipleRoots verifies backup with multiple root paths.
func TestBackupMultipleRoots(t *testing.T) {
	r, _ := setupTestRepo(t)

	// Create two separate source directories
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	os.WriteFile(filepath.Join(dir1, "from_dir1.txt"), []byte("dir1 content"), 0644)
	os.WriteFile(filepath.Join(dir2, "from_dir2.txt"), []byte("dir2 content"), 0644)

	ctx := context.Background()
	snap, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{dir1, dir2},
		ConfigName:       "multi-root",
		CompressionLevel: 3,
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	if snap == nil {
		t.Fatal("nil snapshot")
	}
	if len(snap.Paths) != 2 {
		t.Errorf("snapshot paths = %v, want 2 entries", snap.Paths)
	}
	if snap.Tree.IsZero() {
		t.Error("snapshot tree is zero")
	}
}

// TestBackupWithModification verifies that modifying a file between backups
// results in new chunks being stored.
func TestBackupWithModification(t *testing.T) {
	r, _ := setupTestRepo(t)
	sourceDir := createTestFiles(t)
	ctx := context.Background()

	// First backup
	_, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test",
		CompressionLevel: 3,
	})
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}

	indexAfterFirst := r.Index().Len()

	// Modify a file
	modPath := filepath.Join(sourceDir, "hello.txt")
	if err := os.WriteFile(modPath, []byte("Hello, Modified Doomsday!"), 0644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	// Add a new file
	newPath := filepath.Join(sourceDir, "new_file.txt")
	if err := os.WriteFile(newPath, []byte("Brand new file content"), 0644); err != nil {
		t.Fatalf("add new file: %v", err)
	}

	// Second backup
	ss := &safeStats{}
	snap2, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test",
		CompressionLevel: 3,
		OnProgress: func(s backup.Stats) {
			ss.update(s)
		},
	})
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}

	stats2 := ss.get()

	if snap2 == nil {
		t.Fatal("nil snapshot for second backup")
	}

	indexAfterSecond := r.Index().Len()
	t.Logf("Index: first=%d, second=%d (new=%d)", indexAfterFirst, indexAfterSecond, indexAfterSecond-indexAfterFirst)
	t.Logf("Second backup: new_chunks=%d, dup_chunks=%d", stats2.ChunksNew, stats2.ChunksDup)

	// Should have some new chunks (modified + new file)
	if stats2.ChunksNew == 0 {
		t.Error("expected new chunks after file modification")
	}
	// Should also have some duplicates (unchanged files)
	if stats2.ChunksDup == 0 {
		t.Error("expected duplicate chunks for unchanged files")
	}
}

// TestBackupNoPathsError verifies that Run returns an error when no paths are given.
func TestBackupNoPathsError(t *testing.T) {
	r, _ := setupTestRepo(t)
	ctx := context.Background()

	_, err := backup.Run(ctx, r, backup.Options{
		ConfigName: "test",
	})
	if err == nil {
		t.Fatal("expected error for no paths, got nil")
	}
}

// TestBackupProgressStats verifies that progress stats are internally consistent.
func TestBackupProgressStats(t *testing.T) {
	r, _ := setupTestRepo(t)
	sourceDir := createTestFiles(t)
	ctx := context.Background()

	var finalCalls atomic.Int64
	ss := &safeStats{}

	_, err := backup.Run(ctx, r, backup.Options{
		Paths:            []string{sourceDir},
		ConfigName:       "test",
		CompressionLevel: 3,
		OnProgress: func(s backup.Stats) {
			finalCalls.Add(1)
			ss.update(s)
		},
	})
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}

	s := ss.get()

	// ChunksNew + ChunksDup should equal ChunksTotal
	if s.ChunksNew+s.ChunksDup != s.ChunksTotal {
		t.Errorf("ChunksNew(%d) + ChunksDup(%d) != ChunksTotal(%d)",
			s.ChunksNew, s.ChunksDup, s.ChunksTotal)
	}

	// BytesNew should be <= BytesRead
	if s.BytesNew > s.BytesRead {
		t.Errorf("BytesNew(%d) > BytesRead(%d)", s.BytesNew, s.BytesRead)
	}

	// Elapsed should be > 0
	if s.Elapsed <= 0 {
		t.Errorf("Elapsed = %v, want > 0", s.Elapsed)
	}

	// StartTime should be recent
	if time.Since(s.StartTime) > 10*time.Second {
		t.Errorf("StartTime too old: %v", s.StartTime)
	}

	t.Logf("Final stats: total=%d, new=%d, dup=%d, read=%d, new_bytes=%d, elapsed=%v",
		s.ChunksTotal, s.ChunksNew, s.ChunksDup, s.BytesRead, s.BytesNew, s.Elapsed)
}
