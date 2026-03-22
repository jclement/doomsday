package sftp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jclement/doomsday/internal/types"
	"github.com/pkg/sftp"
)

// testPair creates an in-process SFTP client/server pair connected via pipes.
// The server operates against the real filesystem (no SSH needed).
// Returns the sftp.Client. The server and pipes are cleaned up when t finishes.
func testPair(t *testing.T) *sftp.Client {
	t.Helper()

	// Pipe layout (matching pkg/sftp's own test pattern):
	//   cr, sw := io.Pipe()  ->  client reads from cr, server writes to sw
	//   sr, cw := io.Pipe()  ->  server reads from sr, client writes to cw
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()

	server, err := sftp.NewServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, sw})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	go func() {
		_ = server.Serve()
	}()

	client, err := sftp.NewClientPipe(cr, cw)
	if err != nil {
		t.Fatalf("NewClientPipe: %v", err)
	}

	t.Cleanup(func() {
		// Server must close first: this terminates Serve() and closes the
		// server's write pipe, which unblocks the client's recv goroutine.
		server.Close()
		client.Close()
	})

	return client
}

// testBackend creates a Backend backed by an in-process SFTP server.
// The server operates against the real filesystem, so basePath is a real temp dir.
func testBackend(t *testing.T) *Backend {
	t.Helper()

	client := testPair(t)
	basePath := filepath.Join(t.TempDir(), "repo")

	b := &Backend{
		basePath:   basePath,
		host:       "test",
		port:       "22",
		user:       "testuser",
		sshClient:  nil, // no SSH in pipe-based tests
		sftpClient: client,
	}

	// Create directory structure using os.MkdirAll (bypassing SFTP).
	// The SFTP server is backed by the real filesystem, so these directories
	// will be visible to the SFTP client.
	for _, ft := range []types.FileType{
		types.FileTypePack, types.FileTypeIndex, types.FileTypeSnapshot,
		types.FileTypeKey, types.FileTypeLock,
	} {
		dir := b.dir(ft)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}

	return b
}

func TestLocation(t *testing.T) {
	b := testBackend(t)
	loc := b.Location()
	if loc == "" {
		t.Error("Location should not be empty")
	}
	// The location should contain the expected components.
	if len(loc) < 5 || loc[:5] != "sftp:" {
		t.Errorf("Location() = %q, should start with sftp:", loc)
	}
}

func TestSaveAndLoad(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	data := []byte("hello doomsday")

	if err := b.Save(ctx, types.FileTypeSnapshot, "snap-001", bytes.NewReader(data)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc, err := b.Load(ctx, types.FileTypeSnapshot, "snap-001", 0, 0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestLoadRange(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	data := []byte("0123456789")

	if err := b.Save(ctx, types.FileTypeIndex, "idx-001", bytes.NewReader(data)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tests := []struct {
		name     string
		offset   int64
		length   int64
		expected string
	}{
		{"full file", 0, 0, "0123456789"},
		{"from offset", 3, 4, "3456"},
		{"from start", 0, 5, "01234"},
		{"tail", 7, 0, "789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, err := b.Load(ctx, types.FileTypeIndex, "idx-001", tt.offset, tt.length)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()

			got, _ := io.ReadAll(rc)
			if string(got) != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStat(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	data := []byte("test data for stat")

	if err := b.Save(ctx, types.FileTypeSnapshot, "snap-stat", bytes.NewReader(data)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := b.Stat(ctx, types.FileTypeSnapshot, "snap-stat")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "snap-stat" {
		t.Errorf("Name = %q, want %q", info.Name, "snap-stat")
	}
	if info.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", info.Size, len(data))
	}
}

func TestStatNotFound(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	_, err := b.Stat(ctx, types.FileTypeSnapshot, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestRemove(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	if err := b.Save(ctx, types.FileTypeSnapshot, "snap-rm", bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := b.Remove(ctx, types.FileTypeSnapshot, "snap-rm"); err != nil {
		t.Fatal(err)
	}

	// Should be gone.
	_, err := b.Stat(ctx, types.FileTypeSnapshot, "snap-rm")
	if err == nil {
		t.Error("file should be removed")
	}

	// Remove again should be idempotent.
	if err := b.Remove(ctx, types.FileTypeSnapshot, "snap-rm"); err != nil {
		t.Errorf("idempotent remove failed: %v", err)
	}
}

func TestList(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	for _, name := range []string{"snap-c", "snap-a", "snap-b"} {
		if err := b.Save(ctx, types.FileTypeSnapshot, name, bytes.NewReader([]byte("data"))); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}

	var names []string
	err := b.List(ctx, types.FileTypeSnapshot, func(fi types.FileInfo) error {
		names = append(names, fi.Name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(names) != 3 {
		t.Fatalf("listed %d files, want 3", len(names))
	}
	// Should be sorted.
	if names[0] != "snap-a" || names[1] != "snap-b" || names[2] != "snap-c" {
		t.Errorf("names = %v, want sorted", names)
	}
}

func TestListEmpty(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	var count int
	err := b.List(ctx, types.FileTypeSnapshot, func(fi types.FileInfo) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 files, got %d", count)
	}
}

func TestPackFileHexPrefix(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	name := "abcdef1234567890abcdef1234567890"
	if err := b.Save(ctx, types.FileTypePack, name, bytes.NewReader([]byte("pack data"))); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Should be readable.
	rc, err := b.Load(ctx, types.FileTypePack, name, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "pack data" {
		t.Error("pack data mismatch")
	}

	// Stat should work.
	info, err := b.Stat(ctx, types.FileTypePack, name)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != 9 {
		t.Errorf("size = %d, want 9", info.Size)
	}

	// List should find it (plain name, no prefix dir).
	var found bool
	err = b.List(ctx, types.FileTypePack, func(fi types.FileInfo) error {
		if fi.Name == name {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !found {
		t.Error("pack file not found in list")
	}
}

func TestPackFileMultiplePrefixes(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	names := []string{
		"aabbccddee1234567890123456789012",
		"aaffgghhii1234567890123456789012",
		"bbccddee001234567890123456789012",
	}
	for _, name := range names {
		if err := b.Save(ctx, types.FileTypePack, name, bytes.NewReader([]byte("data"))); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}

	var listed []string
	err := b.List(ctx, types.FileTypePack, func(fi types.FileInfo) error {
		listed = append(listed, fi.Name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 3 {
		t.Fatalf("listed %d files, want 3", len(listed))
	}

	// Should be sorted by prefix then name.
	for i := 0; i < len(listed)-1; i++ {
		if listed[i] >= listed[i+1] {
			t.Errorf("not sorted: %v", listed)
			break
		}
	}
}

func TestSaveOverwrite(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	if err := b.Save(ctx, types.FileTypeKey, "key-001", bytes.NewReader([]byte("version1"))); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := b.Save(ctx, types.FileTypeKey, "key-001", bytes.NewReader([]byte("version2"))); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}

	rc, err := b.Load(ctx, types.FileTypeKey, "key-001", 0, 0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if string(got) != "version2" {
		t.Errorf("got %q, want %q", string(got), "version2")
	}
}

func TestConfigFile(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	data := []byte(`{"version":1}`)
	if err := b.Save(ctx, types.FileTypeConfig, "config", bytes.NewReader(data)); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	rc, err := b.Load(ctx, types.FileTypeConfig, "config", 0, 0)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}

	info, err := b.Stat(ctx, types.FileTypeConfig, "config")
	if err != nil {
		t.Fatalf("Stat config: %v", err)
	}
	if info.Size != int64(len(data)) {
		t.Errorf("config size = %d, want %d", info.Size, len(data))
	}
}

func TestMultipleFileTypes(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	fileTypes := []struct {
		ft   types.FileType
		name string
		data string
	}{
		{types.FileTypeSnapshot, "snap-001", "snapshot data"},
		{types.FileTypeIndex, "idx-001", "index data"},
		{types.FileTypeKey, "key-001", "key data"},
		{types.FileTypeLock, "lock-001", "lock data"},
	}

	for _, tt := range fileTypes {
		t.Run(tt.ft.String(), func(t *testing.T) {
			if err := b.Save(ctx, tt.ft, tt.name, bytes.NewReader([]byte(tt.data))); err != nil {
				t.Fatalf("Save: %v", err)
			}

			rc, err := b.Load(ctx, tt.ft, tt.name, 0, 0)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			defer rc.Close()

			got, _ := io.ReadAll(rc)
			if string(got) != tt.data {
				t.Errorf("got %q, want %q", string(got), tt.data)
			}
		})
	}
}

func TestPathLogic(t *testing.T) {
	b := &Backend{basePath: "/myrepo"}

	tests := []struct {
		name     string
		ft       types.FileType
		fileName string
		want     string
	}{
		{"snapshot", types.FileTypeSnapshot, "snap-001", "/myrepo/snapshots/snap-001"},
		{"index", types.FileTypeIndex, "idx-001", "/myrepo/index/idx-001"},
		{"key", types.FileTypeKey, "key-001", "/myrepo/keys/key-001"},
		{"lock", types.FileTypeLock, "lock-001", "/myrepo/locks/lock-001"},
		{"config", types.FileTypeConfig, "config", "/myrepo/config"},
		{"pack with prefix", types.FileTypePack, "abcdef0123456789", "/myrepo/data/ab/abcdef0123456789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.path(tt.ft, tt.fileName)
			if got != tt.want {
				t.Errorf("path(%v, %q) = %q, want %q", tt.ft, tt.fileName, got, tt.want)
			}
		})
	}
}

func TestDirLogic(t *testing.T) {
	b := &Backend{basePath: "/myrepo"}

	tests := []struct {
		name string
		ft   types.FileType
		want string
	}{
		{"pack", types.FileTypePack, "/myrepo/data"},
		{"index", types.FileTypeIndex, "/myrepo/index"},
		{"snapshot", types.FileTypeSnapshot, "/myrepo/snapshots"},
		{"key", types.FileTypeKey, "/myrepo/keys"},
		{"config", types.FileTypeConfig, "/myrepo/config"},
		{"lock", types.FileTypeLock, "/myrepo/locks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.dir(tt.ft)
			if got != tt.want {
				t.Errorf("dir(%v) = %q, want %q", tt.ft, got, tt.want)
			}
		})
	}
}

func TestLoadNotFound(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	_, err := b.Load(ctx, types.FileTypeSnapshot, "nonexistent", 0, 0)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestListCallbackError(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		if err := b.Save(ctx, types.FileTypeSnapshot, name, bytes.NewReader([]byte("data"))); err != nil {
			t.Fatalf("Save(%s): %v", name, err)
		}
	}

	errStop := io.ErrUnexpectedEOF
	var count int
	err := b.List(ctx, types.FileTypeSnapshot, func(fi types.FileInfo) error {
		count++
		if count >= 2 {
			return errStop
		}
		return nil
	})
	if err != errStop {
		t.Errorf("expected callback error, got %v", err)
	}
}

func TestCloseNilClients(t *testing.T) {
	// Closing a backend with nil clients should not panic.
	b := &Backend{}
	if err := b.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
