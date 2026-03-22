package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/types"
)

func testRepo(t *testing.T) *repo.Repository {
	t.Helper()
	b, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	master, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.Init(context.Background(), b, master)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// testRepoWithBackup creates a repo, writes source files, runs a backup, and
// returns the repo and the backend base path (for on-disk corruption tests).
func testRepoWithBackup(t *testing.T) (*repo.Repository, string) {
	t.Helper()
	repoDir := t.TempDir()
	b, err := local.New(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	master, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.Init(context.Background(), b, master)
	if err != nil {
		t.Fatal(err)
	}

	// Create source files to back up.
	srcDir := t.TempDir()
	for _, name := range []string{"hello.txt", "world.txt"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("content of "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Run a backup.
	ctx := context.Background()
	_, err = backup.Run(ctx, r, backup.Options{
		Paths:    []string{srcDir},
		Hostname: "test-host",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Re-open the repo so indexes are loaded from disk (matches real usage).
	r, err = repo.Open(ctx, b, master)
	if err != nil {
		t.Fatal(err)
	}

	return r, repoDir
}

func TestCheckEmptyRepo(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	report, err := Run(ctx, r, LevelStructure)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() {
		t.Errorf("empty repo should be OK, got %d errors", len(report.Errors))
	}
}

func TestCheckLevels(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	// Structure check
	report, err := Run(ctx, r, LevelStructure)
	if err != nil {
		t.Fatal(err)
	}
	if report.Level != LevelStructure {
		t.Errorf("level = %d", report.Level)
	}

	// Header check
	report, err = Run(ctx, r, LevelHeaders)
	if err != nil {
		t.Fatal(err)
	}
	if report.Level != LevelHeaders {
		t.Errorf("level = %d", report.Level)
	}

	// Full check
	report, err = Run(ctx, r, LevelFull)
	if err != nil {
		t.Fatal(err)
	}
	if report.Level != LevelFull {
		t.Errorf("level = %d", report.Level)
	}
}

func TestReportOK(t *testing.T) {
	r := &Report{}
	if !r.OK() {
		t.Error("empty report should be OK")
	}
	r.Errors = append(r.Errors, Error{Message: "test"})
	if r.OK() {
		t.Error("report with errors should not be OK")
	}
}

// ---------------------------------------------------------------------------
// TestCheckWithRealData: backup real files, run check at all three levels
// ---------------------------------------------------------------------------

func TestCheckWithRealData(t *testing.T) {
	r, _ := testRepoWithBackup(t)
	ctx := context.Background()

	levels := []struct {
		name  string
		level Level
	}{
		{"Structure", LevelStructure},
		{"Headers", LevelHeaders},
		{"Full", LevelFull},
	}

	for _, tc := range levels {
		t.Run(tc.name, func(t *testing.T) {
			report, err := Run(ctx, r, tc.level)
			if err != nil {
				t.Fatalf("Run at %s: %v", tc.name, err)
			}
			if !report.OK() {
				for _, e := range report.Errors {
					t.Logf("  error: pack=%s blob=%s msg=%s", e.Pack, e.BlobID, e.Message)
				}
				t.Fatalf("expected OK report at %s, got %d errors", tc.name, len(report.Errors))
			}
		})
	}

	// Run a full check and verify counters are positive.
	report, err := Run(ctx, r, LevelFull)
	if err != nil {
		t.Fatal(err)
	}
	if report.PacksChecked == 0 {
		t.Error("expected PacksChecked > 0 after a real backup")
	}
	if report.BlobsChecked == 0 {
		t.Error("expected BlobsChecked > 0 after a real backup")
	}
	if report.SnapshotsChecked == 0 {
		t.Error("expected SnapshotsChecked > 0 after a real backup")
	}
}

// ---------------------------------------------------------------------------
// TestCheckCorruptedPack: corrupt a pack file on disk, verify check catches it
// ---------------------------------------------------------------------------

func TestCheckCorruptedPack(t *testing.T) {
	r, repoDir := testRepoWithBackup(t)
	ctx := context.Background()

	// Find a pack file on disk and corrupt it.
	// Pack files live under repoDir/data/<hex-prefix>/<packname>.
	dataDir := filepath.Join(repoDir, types.FileTypePack.String())
	packPath := findFirstFile(t, dataDir)
	if packPath == "" {
		t.Fatal("no pack file found after backup")
	}

	// Read the pack, flip a byte near the middle, write it back.
	data, err := os.ReadFile(packPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 10 {
		t.Fatalf("pack file unexpectedly small: %d bytes", len(data))
	}
	mid := len(data) / 2
	data[mid] ^= 0xFF
	if err := os.WriteFile(packPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Run header-level check. The corrupted pack should cause an error because
	// either the SHA-256 name will not match or the header decryption will fail.
	report, err := Run(ctx, r, LevelHeaders)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if report.OK() {
		t.Error("expected errors in report after corrupting a pack file, but report.OK() == true")
	}
	if len(report.Errors) == 0 {
		t.Error("expected at least one error after pack corruption")
	}

	// Log the errors for debugging.
	for _, e := range report.Errors {
		t.Logf("detected corruption: pack=%s blob=%s msg=%s", e.Pack, e.BlobID, e.Message)
	}
}

// findFirstFile recursively walks a directory tree and returns the path to
// the first regular file found, or "" if none.
func findFirstFile(t *testing.T, dir string) string {
	t.Helper()
	var found string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if found != "" {
			return filepath.SkipAll
		}
		if !d.IsDir() {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
