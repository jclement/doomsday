package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/pkg/sftp"
)

// newTestHandler creates a Handler with a temporary jail directory for testing.
// It returns the handler and the resolved (real) jail directory path, which
// accounts for macOS symlink resolution (e.g. /var -> /private/var).
func newTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()

	jailDir := t.TempDir()
	logger := log.Default()
	h := NewHandler(jailDir, 0, true, logger)
	// Return the handler's resolved jail dir, not the raw t.TempDir().
	return h, h.jailDir
}

// ─── Path Jailing Tests ──────────────────────────────────────────────────────

func TestResolvePath_Root(t *testing.T) {
	h, jailDir := newTestHandler(t)

	resolved, err := h.resolvePath("/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != jailDir {
		t.Errorf("expected %q, got %q", jailDir, resolved)
	}
}

func TestResolvePath_CleanPath(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create a subdirectory so the path resolves.
	subDir := filepath.Join(jailDir, "data")
	if err := os.Mkdir(subDir, 0700); err != nil {
		t.Fatal(err)
	}

	resolved, err := h.resolvePath("/data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != subDir {
		t.Errorf("expected %q, got %q", subDir, resolved)
	}
}

func TestResolvePath_DotDotCleanedToJail(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// "/../../../etc/passwd" cleans to "/etc/passwd" via filepath.Clean,
	// which maps to <jail>/etc/passwd. This is WITHIN the jail, which is
	// correct -- filepath.Clean strips the traversal. The path does not
	// exist as a file, but path resolution succeeds (for new file creation).
	resolved, err := h.resolvePath("/../../../etc/passwd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should resolve to <jail>/etc/passwd, NOT to /etc/passwd on the real FS.
	expected := filepath.Join(jailDir, "etc", "passwd")
	if resolved != expected {
		t.Errorf("expected %q, got %q", expected, resolved)
	}
}

func TestResolvePath_DotDotMiddleCleanedToJail(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create a subdirectory to make the first component valid.
	if err := os.Mkdir(filepath.Join(jailDir, "data"), 0700); err != nil {
		t.Fatal(err)
	}

	// "/data/../../etc/passwd" cleans to "/etc/passwd" which maps to <jail>/etc/passwd.
	resolved, err := h.resolvePath("/data/../../etc/passwd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(jailDir, "etc", "passwd")
	if resolved != expected {
		t.Errorf("expected %q, got %q", expected, resolved)
	}
}

func TestResolvePath_SymlinkEscape(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create a symlink inside the jail that points outside.
	outsideDir := t.TempDir()
	// Resolve the outside dir too, for consistency.
	outsideDir, _ = filepath.EvalSymlinks(outsideDir)
	link := filepath.Join(jailDir, "escape")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Fatal(err)
	}

	_, err := h.resolvePath("/escape")
	if err == nil {
		t.Fatal("expected error for symlink escape, got nil")
	}
}

func TestResolvePath_SymlinkInsideJail(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create a real directory and a symlink to it within the jail.
	realDir := filepath.Join(jailDir, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(jailDir, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	resolved, err := h.resolvePath("/link")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != realDir {
		t.Errorf("expected %q, got %q", realDir, resolved)
	}
}

func TestResolvePath_SymlinkChainEscape(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Chain: jail/a -> jail/b -> outside
	outsideDir := t.TempDir()
	outsideDir, _ = filepath.EvalSymlinks(outsideDir)

	linkB := filepath.Join(jailDir, "b")
	if err := os.Symlink(outsideDir, linkB); err != nil {
		t.Fatal(err)
	}
	linkA := filepath.Join(jailDir, "a")
	if err := os.Symlink(linkB, linkA); err != nil {
		t.Fatal(err)
	}

	_, err := h.resolvePath("/a")
	if err == nil {
		t.Fatal("expected error for chained symlink escape, got nil")
	}
}

func TestResolvePath_NewFile(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Resolving a path to a non-existent file should succeed if the parent exists.
	resolved, err := h.resolvePath("/newfile.dat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(jailDir, "newfile.dat")
	if resolved != expected {
		t.Errorf("expected %q, got %q", expected, resolved)
	}
}

func TestResolvePath_NestedNewFile(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create parent dir.
	if err := os.Mkdir(filepath.Join(jailDir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}

	resolved, err := h.resolvePath("/sub/newfile.dat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(jailDir, "sub", "newfile.dat")
	if resolved != expected {
		t.Errorf("expected %q, got %q", expected, resolved)
	}
}

func TestResolvePath_EmptyPath(t *testing.T) {
	h, jailDir := newTestHandler(t)

	resolved, err := h.resolvePath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != jailDir {
		t.Errorf("expected %q, got %q", jailDir, resolved)
	}
}

func TestResolvePath_DotPath(t *testing.T) {
	h, jailDir := newTestHandler(t)

	resolved, err := h.resolvePath(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != jailDir {
		t.Errorf("expected %q, got %q", jailDir, resolved)
	}
}

func TestResolvePath_MultipleSlashes(t *testing.T) {
	h, jailDir := newTestHandler(t)

	if err := os.Mkdir(filepath.Join(jailDir, "data"), 0700); err != nil {
		t.Fatal(err)
	}

	resolved, err := h.resolvePath("///data///")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(jailDir, "data")
	if resolved != expected {
		t.Errorf("expected %q, got %q", expected, resolved)
	}
}

// ─── isWithinDir Tests ────────────────────────────────────────────────────────

func TestIsWithinDir(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{"equal", "/jail", "/jail", true},
		{"direct child", "/jail", "/jail/file", true},
		{"nested child", "/jail", "/jail/a/b/c", true},
		{"sibling", "/jail", "/jail2/file", false},
		{"parent", "/jail/sub", "/jail", false},
		{"prefix attack", "/jail", "/jailbreak/file", false},
		{"relative escape", "/jail", "/etc/passwd", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWithinDir(tt.parent, tt.child)
			if got != tt.want {
				t.Errorf("isWithinDir(%q, %q) = %v, want %v", tt.parent, tt.child, got, tt.want)
			}
		})
	}
}

// ─── Handler Whitelist Tests ─────────────────────────────────────────────────

func TestFilecmd_MkdirAllowed(t *testing.T) {
	h, jailDir := newTestHandler(t)

	req := sftp.NewRequest("Mkdir", "/newdir")
	err := h.Filecmd(req)
	if err != nil {
		t.Fatalf("mkdir should be allowed: %v", err)
	}

	// Verify directory was created.
	info, err := os.Stat(filepath.Join(jailDir, "newdir"))
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestFilecmd_MkdirNested(t *testing.T) {
	h, jailDir := newTestHandler(t)

	req := sftp.NewRequest("Mkdir", "/a/b/c")
	err := h.Filecmd(req)
	if err != nil {
		t.Fatalf("nested mkdir should be allowed: %v", err)
	}

	info, err := os.Stat(filepath.Join(jailDir, "a", "b", "c"))
	if err != nil {
		t.Fatalf("nested directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestFilecmd_RemoveFile_AppendOnly(t *testing.T) {
	h, jailDir := newTestHandler(t)

	testFile := filepath.Join(jailDir, "target.txt")
	if err := os.WriteFile(testFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Remove", "/target.txt")
	err := h.Filecmd(req)
	if err != sftp.ErrSSHFxPermissionDenied {
		t.Errorf("expected ErrSSHFxPermissionDenied for remove in append-only mode, got %v", err)
	}

	// File should still exist.
	if _, err := os.Stat(testFile); err != nil {
		t.Error("file should still exist in append-only mode")
	}
}

func TestFilecmd_RemoveFile_ReadWrite(t *testing.T) {
	jailDir := t.TempDir()
	logger := log.Default()
	h := NewHandler(jailDir, 0, false, logger)

	testFile := filepath.Join(h.jailDir, "target.txt")
	if err := os.WriteFile(testFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Remove", "/target.txt")
	err := h.Filecmd(req)
	if err != nil {
		t.Errorf("expected file remove to succeed, got %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("file was not deleted")
	}
}

func TestFilecmd_RemoveDirRejected(t *testing.T) {
	// Even in read-write mode, directory removal is rejected.
	jailDir := t.TempDir()
	logger := log.Default()
	h := NewHandler(jailDir, 0, false, logger)

	dirPath := filepath.Join(h.jailDir, "subdir")
	if err := os.Mkdir(dirPath, 0700); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Remove", "/subdir")
	err := h.Filecmd(req)
	if err != sftp.ErrSSHFxOpUnsupported {
		t.Errorf("expected ErrSSHFxOpUnsupported for dir remove, got %v", err)
	}
}

func TestFilecmd_RmdirRejected(t *testing.T) {
	h, jailDir := newTestHandler(t)

	if err := os.Mkdir(filepath.Join(jailDir, "keepme"), 0700); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Rmdir", "/keepme")
	err := h.Filecmd(req)
	if err != sftp.ErrSSHFxOpUnsupported {
		t.Errorf("expected ErrSSHFxOpUnsupported, got %v", err)
	}
}

func TestFilecmd_RenameAllowed(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create a source file.
	srcPath := filepath.Join(jailDir, "a.tmp")
	if err := os.WriteFile(srcPath, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Rename", "/a.tmp")
	req.Target = "/a.final"
	err := h.Filecmd(req)
	if err != nil {
		t.Errorf("expected rename to succeed, got %v", err)
	}

	// Source should not exist, target should.
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("source file still exists after rename")
	}
	dstPath := filepath.Join(jailDir, "a.final")
	if _, err := os.Stat(dstPath); os.IsNotExist(err) {
		t.Error("target file does not exist after rename")
	}
}

func TestFilecmd_RenameOverwriteRejected(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create both source and target.
	if err := os.WriteFile(filepath.Join(jailDir, "src.tmp"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jailDir, "existing"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Rename", "/src.tmp")
	req.Target = "/existing"
	err := h.Filecmd(req)
	if err != sftp.ErrSSHFxPermissionDenied {
		t.Errorf("expected ErrSSHFxPermissionDenied for overwrite rename, got %v", err)
	}

	// Target should retain original content.
	data, _ := os.ReadFile(filepath.Join(jailDir, "existing"))
	if string(data) != "old" {
		t.Errorf("target was overwritten: got %q, want %q", data, "old")
	}
}

func TestFilecmd_SetstatAllowed(t *testing.T) {
	h, _ := newTestHandler(t)

	req := sftp.NewRequest("Setstat", "/anything")
	err := h.Filecmd(req)
	if err != nil {
		t.Errorf("expected setstat to silently succeed, got %v", err)
	}
}

func TestFilecmd_LinkRejected(t *testing.T) {
	h, _ := newTestHandler(t)

	req := sftp.NewRequest("Link", "/a")
	req.Target = "/b"
	err := h.Filecmd(req)
	if err != sftp.ErrSSHFxOpUnsupported {
		t.Errorf("expected ErrSSHFxOpUnsupported, got %v", err)
	}
}

func TestFilecmd_SymlinkRejected(t *testing.T) {
	h, _ := newTestHandler(t)

	req := sftp.NewRequest("Symlink", "/a")
	req.Target = "/b"
	err := h.Filecmd(req)
	if err != sftp.ErrSSHFxOpUnsupported {
		t.Errorf("expected ErrSSHFxOpUnsupported, got %v", err)
	}
}

// ─── Filelist Tests ──────────────────────────────────────────────────────────

func TestFilelist_StatRoot(t *testing.T) {
	h, _ := newTestHandler(t)

	req := sftp.NewRequest("Stat", "/")
	lister, err := h.Filelist(req)
	if err != nil {
		t.Fatalf("stat root should succeed: %v", err)
	}

	infos := make([]os.FileInfo, 1)
	n, _ := lister.ListAt(infos, 0)
	if n != 1 {
		t.Fatalf("expected 1 entry, got %d", n)
	}
	if !infos[0].IsDir() {
		t.Error("root should be a directory")
	}
}

func TestFilelist_ListDir(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create some files.
	if err := os.WriteFile(filepath.Join(jailDir, "a.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jailDir, "b.txt"), []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("List", "/")
	lister, err := h.Filelist(req)
	if err != nil {
		t.Fatalf("list root should succeed: %v", err)
	}

	infos := make([]os.FileInfo, 10)
	n, _ := lister.ListAt(infos, 0)
	if n < 2 {
		t.Errorf("expected at least 2 entries, got %d", n)
	}
}

func TestFilelist_ReadlinkRejected(t *testing.T) {
	h, _ := newTestHandler(t)

	req := sftp.NewRequest("Readlink", "/anything")
	_, err := h.Filelist(req)
	if err != sftp.ErrSSHFxOpUnsupported {
		t.Errorf("expected ErrSSHFxOpUnsupported, got %v", err)
	}
}

// ─── Fileread Tests ──────────────────────────────────────────────────────────

func TestFileread_ExistingFile(t *testing.T) {
	h, jailDir := newTestHandler(t)

	content := []byte("hello doomsday")
	if err := os.WriteFile(filepath.Join(jailDir, "test.txt"), content, 0600); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Get", "/test.txt")
	reader, err := h.Fileread(req)
	if err != nil {
		t.Fatalf("read should succeed: %v", err)
	}

	buf := make([]byte, 64)
	n, _ := reader.ReadAt(buf, 0)
	if string(buf[:n]) != string(content) {
		t.Errorf("expected %q, got %q", content, buf[:n])
	}

	// Clean up the file handle.
	if closer, ok := reader.(interface{ Close() error }); ok {
		closer.Close()
	}
}

func TestFileread_NonExistentFile(t *testing.T) {
	h, _ := newTestHandler(t)

	req := sftp.NewRequest("Get", "/nonexistent.txt")
	_, err := h.Fileread(req)
	if err != sftp.ErrSSHFxNoSuchFile {
		t.Errorf("expected ErrSSHFxNoSuchFile, got %v", err)
	}
}

func TestFileread_SymlinkEscapeBlocked(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create a symlink that points to a real file outside the jail.
	outsideDir := t.TempDir()
	outsideDir, _ = filepath.EvalSymlinks(outsideDir)
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(jailDir, "sneaky")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatal(err)
	}

	req := sftp.NewRequest("Get", "/sneaky")
	_, err := h.Fileread(req)
	if err == nil {
		t.Fatal("expected error for symlink escape read")
	}
}

// ─── Quota Tests ─────────────────────────────────────────────────────────────

func TestQuotaWriter_EnforcesLimit(t *testing.T) {
	jailDir := t.TempDir()
	logger := log.Default()
	h := NewHandler(jailDir, 100, true, logger) // 100 byte quota

	// Create a file to write into.
	resolvedJail := h.jailDir
	testFile := filepath.Join(resolvedJail, "quota_test.dat")
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatal(err)
	}

	qw := &quotaWriter{
		file:    f,
		handler: h,
		path:    testFile,
	}

	// Write 50 bytes -- should succeed.
	data := make([]byte, 50)
	_, err = qw.WriteAt(data, 0)
	if err != nil {
		t.Fatalf("write within quota should succeed: %v", err)
	}

	// Write another 60 bytes -- should fail (50+60 > 100).
	bigData := make([]byte, 60)
	_, err = qw.WriteAt(bigData, 50)
	if err == nil {
		t.Fatal("write exceeding quota should fail")
	}

	qw.Close()
}

func TestQuotaWriter_UnlimitedWhenZero(t *testing.T) {
	jailDir := t.TempDir()
	logger := log.Default()
	h := NewHandler(jailDir, 0, true, logger) // 0 = unlimited

	resolvedJail := h.jailDir
	testFile := filepath.Join(resolvedJail, "unlimited.dat")
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatal(err)
	}

	qw := &quotaWriter{
		file:    f,
		handler: h,
		path:    testFile,
	}

	// Write a large chunk -- should succeed with unlimited quota.
	data := make([]byte, 10000)
	_, err = qw.WriteAt(data, 0)
	if err != nil {
		t.Fatalf("unlimited write should succeed: %v", err)
	}

	qw.Close()
}

// ─── Client Manager Tests ────────────────────────────────────────────────────

func TestClientManager_NewAndList(t *testing.T) {
	dataDir := t.TempDir()

	clients := []ClientConfig{
		{Name: "testclient", QuotaBytes: 1024},
	}

	cm, err := NewClientManager(dataDir, clients)
	if err != nil {
		t.Fatal(err)
	}

	result := cm.List()
	if len(result) != 1 {
		t.Fatalf("expected 1 client, got %d", len(result))
	}
	if result[0].Name != "testclient" {
		t.Errorf("expected name %q, got %q", "testclient", result[0].Name)
	}
}

func TestClientManager_DuplicateRejected(t *testing.T) {
	dataDir := t.TempDir()

	clients := []ClientConfig{
		{Name: "dup"},
		{Name: "dup"},
	}

	_, err := NewClientManager(dataDir, clients)
	if err == nil {
		t.Fatal("expected error for duplicate client names")
	}
}

func TestClientManager_InvalidNameRejected(t *testing.T) {
	dataDir := t.TempDir()

	clients := []ClientConfig{
		{Name: "../escape"},
	}

	_, err := NewClientManager(dataDir, clients)
	if err == nil {
		t.Fatal("expected error for invalid client name")
	}
}

func TestClientManager_Get(t *testing.T) {
	dataDir := t.TempDir()

	clients := []ClientConfig{
		{Name: "myclient", QuotaBytes: 4096},
	}

	cm, err := NewClientManager(dataDir, clients)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := cm.Get("myclient")
	if !ok {
		t.Fatal("expected to find client")
	}
	if got.QuotaBytes != 4096 {
		t.Errorf("expected quota 4096, got %d", got.QuotaBytes)
	}

	_, ok = cm.Get("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent client")
	}
}

func TestClientManager_CreatesJailDirs(t *testing.T) {
	dataDir := t.TempDir()

	clients := []ClientConfig{
		{Name: "alpha"},
		{Name: "beta"},
	}

	_, err := NewClientManager(dataDir, clients)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(dataDir, name)
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("expected jail dir for %q to exist: %v", name, err)
		} else if !info.IsDir() {
			t.Errorf("expected %q to be a directory", dir)
		}
	}
}

func TestClientManager_Replace(t *testing.T) {
	dataDir := t.TempDir()

	clients := []ClientConfig{
		{Name: "alpha"},
		{Name: "beta"},
	}

	cm, err := NewClientManager(dataDir, clients)
	if err != nil {
		t.Fatal(err)
	}

	if len(cm.List()) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(cm.List()))
	}

	// Replace with a new set including a new client.
	newClients := []ClientConfig{
		{Name: "alpha"},
		{Name: "gamma"},
	}

	if err := cm.Replace(newClients); err != nil {
		t.Fatal(err)
	}

	if len(cm.List()) != 2 {
		t.Fatalf("expected 2 clients after replace, got %d", len(cm.List()))
	}

	if _, ok := cm.Get("beta"); ok {
		t.Error("expected beta to be gone after replace")
	}
	if _, ok := cm.Get("gamma"); !ok {
		t.Error("expected gamma to exist after replace")
	}

	// Verify jail dir created for gamma.
	gammaDir := filepath.Join(dataDir, "gamma")
	if _, err := os.Stat(gammaDir); err != nil {
		t.Errorf("expected jail dir for gamma: %v", err)
	}
}

func TestClientManager_ReplaceRejectsDuplicate(t *testing.T) {
	dataDir := t.TempDir()
	cm, err := NewClientManager(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = cm.Replace([]ClientConfig{
		{Name: "dup"},
		{Name: "dup"},
	})
	if err == nil {
		t.Error("expected error for duplicate names in Replace")
	}
}

// ─── Mkdir symlink escape ────────────────────────────────────────────────────

func TestFilecmd_MkdirSymlinkEscapeBlocked(t *testing.T) {
	h, jailDir := newTestHandler(t)

	// Create a symlink inside the jail pointing outside.
	outsideDir := t.TempDir()
	outsideDir, _ = filepath.EvalSymlinks(outsideDir)
	link := filepath.Join(jailDir, "escape")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Fatal(err)
	}

	// Attempt to mkdir through the escaping symlink.
	req := sftp.NewRequest("Mkdir", "/escape/pwned")
	err := h.Filecmd(req)
	if err == nil {
		t.Fatal("mkdir through symlink escape should be blocked")
	}

	// Verify nothing was created outside the jail.
	if _, err := os.Stat(filepath.Join(outsideDir, "pwned")); err == nil {
		t.Error("directory was created outside jail despite rejection")
	}
}
