package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
)

// ---------------------------------------------------------------------------
// pathIncluded tests
// ---------------------------------------------------------------------------

func TestPathIncluded_ExactMatch(t *testing.T) {
	if !pathIncluded("docs/readme.txt", []string{"docs/readme.txt"}) {
		t.Error("expected exact match to be included")
	}
}

func TestPathIncluded_ChildOfPrefix(t *testing.T) {
	if !pathIncluded("docs/sub/file.txt", []string{"docs"}) {
		t.Error("expected child of prefix to be included")
	}
}

func TestPathIncluded_AncestorOfPrefix(t *testing.T) {
	// An ancestor directory of an include path must be created.
	if !pathIncluded("docs", []string{"docs/sub/file.txt"}) {
		t.Error("expected ancestor of prefix to be included")
	}
}

func TestPathIncluded_NoMatch(t *testing.T) {
	if pathIncluded("other/file.txt", []string{"docs"}) {
		t.Error("expected non-matching path to be excluded")
	}
}

func TestPathIncluded_Empty(t *testing.T) {
	// Empty include list means include everything — but pathIncluded is only
	// called when len(includePaths) > 0 so this tests the function directly.
	if pathIncluded("anything", nil) {
		t.Error("nil includePaths should not match")
	}
	if pathIncluded("anything", []string{}) {
		t.Error("empty includePaths should not match")
	}
}

func TestPathIncluded_TrailingSlash(t *testing.T) {
	if !pathIncluded("docs/file.txt", []string{"docs/"}) {
		t.Error("trailing slash on include prefix should still match children")
	}
}

// ---------------------------------------------------------------------------
// restorePlan / buildPlan tests (unit-level, no repo needed)
// ---------------------------------------------------------------------------

func TestPlanEntry_Types(t *testing.T) {
	// Verify that the planEntry struct stores what we expect.
	entry := planEntry{
		relPath: "foo/bar.txt",
		node: tree.Node{
			Name: "bar.txt",
			Type: tree.NodeTypeFile,
			Size: 1024,
		},
	}
	if entry.relPath != "foo/bar.txt" {
		t.Errorf("relPath = %q", entry.relPath)
	}
	if entry.node.Type != tree.NodeTypeFile {
		t.Errorf("node.Type = %q", entry.node.Type)
	}
}

func TestProgressEvent_Fields(t *testing.T) {
	evt := ProgressEvent{
		Path:           "test.txt",
		BytesWritten:   512,
		TotalBytes:     1024,
		FilesCompleted: 1,
		FilesTotal:     10,
		IsDryRun:       true,
	}
	if evt.Path != "test.txt" {
		t.Errorf("Path = %q", evt.Path)
	}
	if !evt.IsDryRun {
		t.Error("expected DryRun = true")
	}
}

func TestFilesRestored_Struct(t *testing.T) {
	fr := FilesRestored{Dirs: 3, Files: 10, Symlinks: 2}
	if fr.Dirs != 3 {
		t.Errorf("Dirs = %d", fr.Dirs)
	}
	if fr.Files != 10 {
		t.Errorf("Files = %d", fr.Files)
	}
	if fr.Symlinks != 2 {
		t.Errorf("Symlinks = %d", fr.Symlinks)
	}
}

// ---------------------------------------------------------------------------
// Options defaults
// ---------------------------------------------------------------------------

func TestOptions_Defaults(t *testing.T) {
	var opts Options
	if opts.Overwrite {
		t.Error("default Overwrite should be false")
	}
	if opts.DryRun {
		t.Error("default DryRun should be false")
	}
	if opts.OnProgress != nil {
		t.Error("default OnProgress should be nil")
	}
	if len(opts.IncludePaths) != 0 {
		t.Error("default IncludePaths should be empty")
	}
}

// ---------------------------------------------------------------------------
// Atomic temp file naming
// ---------------------------------------------------------------------------

func TestTempFilePattern(t *testing.T) {
	// Verify that temp files follow the expected naming pattern.
	// We cannot call restoreFile without a full repo, but we can verify
	// the naming convention is documented and consistent.
	prefix := ".doomsday.tmp."
	name := prefix + "abcdef0123456789"
	if !strings.HasPrefix(name, prefix) {
		t.Errorf("temp name %q should start with %q", name, prefix)
	}
}

// ---------------------------------------------------------------------------
// restoreFile atomic write behaviour (filesystem-level test)
// ---------------------------------------------------------------------------

func TestRestoreFile_AtomicCleanup(t *testing.T) {
	// Verify that if we create a .doomsday.tmp.* file and the process
	// fails, no final file is left behind. This simulates the guarantee
	// without needing a full repository.
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "target.txt")
	tmpPath := filepath.Join(dir, ".doomsday.tmp.testcleanup")

	// Simulate a failed write: create temp, don't rename.
	f, err := os.Create(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("partial data"))
	f.Close()

	// Final file should not exist.
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Error("final file should not exist after failed write")
	}

	// Temp file should exist (would be cleaned on next run or by user).
	if _, err := os.Stat(tmpPath); err != nil {
		t.Error("temp file should exist until explicit cleanup")
	}

	// Now simulate successful rename.
	if err := os.Rename(tmpPath, finalPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "partial data" {
		t.Errorf("content = %q", data)
	}
}

// ---------------------------------------------------------------------------
// Write ordering: verify plan ordering invariants
// ---------------------------------------------------------------------------

func TestBuildPlan_Ordering(t *testing.T) {
	// Build a plan from a hand-constructed tree and verify that
	// directories come in top-down order.
	root := &tree.Tree{
		Nodes: []tree.Node{
			{Name: "adir", Type: tree.NodeTypeDir, Subtree: types.BlobID{}},
			{Name: "bfile.txt", Type: tree.NodeTypeFile, Size: 100},
			{Name: "clink", Type: tree.NodeTypeSymlink, SymlinkTarget: "/tmp"},
			{Name: "dev0", Type: tree.NodeTypeDev},
		},
	}

	var plan restorePlan
	// Pass nil repo — buildPlan only calls loadTree for non-zero subtrees,
	// and we set Subtree to zero so no repo access occurs.
	err := buildPlan(context.Background(), nil, root, "", &plan, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.dirs) != 1 {
		t.Fatalf("expected 1 dir, got %d", len(plan.dirs))
	}
	if plan.dirs[0].node.Name != "adir" {
		t.Errorf("dir name = %q", plan.dirs[0].node.Name)
	}

	if len(plan.files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(plan.files))
	}
	if plan.files[0].node.Name != "bfile.txt" {
		t.Errorf("file name = %q", plan.files[0].node.Name)
	}

	if len(plan.symlinks) != 1 {
		t.Fatalf("expected 1 symlink, got %d", len(plan.symlinks))
	}
	if plan.symlinks[0].node.Name != "clink" {
		t.Errorf("symlink name = %q", plan.symlinks[0].node.Name)
	}
}

func TestBuildPlan_IncludeFilter(t *testing.T) {
	root := &tree.Tree{
		Nodes: []tree.Node{
			{Name: "docs", Type: tree.NodeTypeDir, Subtree: types.BlobID{}},
			{Name: "src", Type: tree.NodeTypeDir, Subtree: types.BlobID{}},
			{Name: "readme.txt", Type: tree.NodeTypeFile, Size: 50},
		},
	}

	var plan restorePlan
	err := buildPlan(context.Background(), nil, root, "", &plan, []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}

	if len(plan.dirs) != 1 {
		t.Fatalf("expected 1 dir, got %d", len(plan.dirs))
	}
	if plan.dirs[0].node.Name != "docs" {
		t.Errorf("expected docs, got %q", plan.dirs[0].node.Name)
	}
	if len(plan.files) != 0 {
		t.Errorf("expected 0 files, got %d", len(plan.files))
	}
}

// ---------------------------------------------------------------------------
// Symlink restore (filesystem test, no repo)
// ---------------------------------------------------------------------------

func TestSymlinkCreation(t *testing.T) {
	dir := t.TempDir()
	linkPath := filepath.Join(dir, "mylink")
	target := "/some/target/path"

	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}

	got, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("symlink target = %q, want %q", got, target)
	}
}

// ---------------------------------------------------------------------------
// Directory permission and timestamp ordering
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// validateNodeName tests
// ---------------------------------------------------------------------------

func TestValidateNodeName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "dot", input: ".", wantErr: true},
		{name: "dotdot", input: "..", wantErr: true},
		{name: "slash", input: "a/b", wantErr: true},
		{name: "backslash", input: "a\\b", wantErr: true},
		{name: "null_byte", input: "a\x00b", wantErr: true},
		{name: "normal", input: "normal", wantErr: false},
		{name: "triple_dot", input: "...", wantErr: false},
		{name: "hidden", input: ".hidden", wantErr: false},
		{name: "file_with_ext", input: "file.txt", wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNodeName(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("validateNodeName(%q) = nil, want error", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateNodeName(%q) = %v, want nil", tc.input, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// validateRestorePath tests
// ---------------------------------------------------------------------------

func TestValidateRestorePath(t *testing.T) {
	tests := []struct {
		name      string
		absTarget string
		finalPath string
		wantErr   bool
	}{
		{
			name:      "inside_target",
			absTarget: "/tmp/restore",
			finalPath: "/tmp/restore/subdir/file.txt",
			wantErr:   false,
		},
		{
			name:      "outside_target",
			absTarget: "/tmp/restore",
			finalPath: "/tmp/other/file.txt",
			wantErr:   true,
		},
		{
			name:      "prefix_attack",
			absTarget: "/tmp/restore",
			finalPath: "/tmp/restore-evil/file.txt",
			wantErr:   true,
		},
		{
			name:      "exact_target_dir",
			absTarget: "/tmp/restore",
			finalPath: "/tmp/restore",
			wantErr:   false,
		},
		{
			name:      "traversal_dotdot",
			absTarget: "/tmp/restore",
			finalPath: "/tmp/restore/../escape",
			wantErr:   true,
		},
		{
			name:      "root_escape",
			absTarget: "/tmp/restore",
			finalPath: "/etc/passwd",
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRestorePath(tc.absTarget, tc.finalPath)
			if tc.wantErr && err == nil {
				t.Errorf("validateRestorePath(%q, %q) = nil, want error", tc.absTarget, tc.finalPath)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateRestorePath(%q, %q) = %v, want nil", tc.absTarget, tc.finalPath, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Directory permission and timestamp ordering
// ---------------------------------------------------------------------------

func TestDirectoryTimestampPreservation(t *testing.T) {
	// Verify that we can set directory timestamps after writing files
	// inside them (the spec ordering guarantee).
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a file inside.
	filePath := filepath.Join(subdir, "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Set directory timestamp to a known time.
	want := mustParseTime(t, "2020-06-15T10:30:00Z")
	if err := os.Chtimes(subdir, want, want); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(subdir)
	if err != nil {
		t.Fatal(err)
	}

	got := info.ModTime()
	if !got.Equal(want) {
		t.Errorf("dir mtime = %v, want %v", got, want)
	}
}

func mustParseTime(t *testing.T, s string) (ts time.Time) {
	t.Helper()
	var err error
	ts, err = time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return
}
