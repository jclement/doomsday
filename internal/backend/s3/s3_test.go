package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/jclement/doomsday/internal/types"
)

// --- Unit tests: path/key generation logic (no S3 endpoint needed) ---

func TestKey(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		ft       types.FileType
		fileName string
		want     string
	}{
		{"snapshot with prefix", "backup", types.FileTypeSnapshot, "snap-001", "backup/snapshots/snap-001"},
		{"index with prefix", "backup", types.FileTypeIndex, "idx-001", "backup/index/idx-001"},
		{"key with prefix", "backup", types.FileTypeKey, "key-001", "backup/keys/key-001"},
		{"lock with prefix", "backup", types.FileTypeLock, "lock-001", "backup/locks/lock-001"},
		{"config with prefix", "backup", types.FileTypeConfig, "config", "backup/config"},
		{"pack with hex prefix", "backup", types.FileTypePack, "abcdef0123456789", "backup/data/ab/abcdef0123456789"},
		{"pack short name", "backup", types.FileTypePack, "ff", "backup/data/ff/ff"},

		// No prefix
		{"snapshot no prefix", "", types.FileTypeSnapshot, "snap-001", "snapshots/snap-001"},
		{"index no prefix", "", types.FileTypeIndex, "idx-001", "index/idx-001"},
		{"key no prefix", "", types.FileTypeKey, "key-001", "keys/key-001"},
		{"lock no prefix", "", types.FileTypeLock, "lock-001", "locks/lock-001"},
		{"config no prefix", "", types.FileTypeConfig, "config", "config"},
		{"pack no prefix", "", types.FileTypePack, "abcdef0123456789", "data/ab/abcdef0123456789"},

		// Nested prefix
		{"snapshot nested prefix", "a/b/c", types.FileTypeSnapshot, "snap-001", "a/b/c/snapshots/snap-001"},
		{"config nested prefix", "a/b/c", types.FileTypeConfig, "config", "a/b/c/config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Backend{prefix: tt.prefix}
			got := b.key(tt.ft, tt.fileName)
			if got != tt.want {
				t.Errorf("key(%v, %q) = %q, want %q", tt.ft, tt.fileName, got, tt.want)
			}
		})
	}
}

func TestDir(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		ft     types.FileType
		want   string
	}{
		{"pack with prefix", "backup", types.FileTypePack, "backup/data/"},
		{"index with prefix", "backup", types.FileTypeIndex, "backup/index/"},
		{"snapshot with prefix", "backup", types.FileTypeSnapshot, "backup/snapshots/"},
		{"key with prefix", "backup", types.FileTypeKey, "backup/keys/"},
		{"lock with prefix", "backup", types.FileTypeLock, "backup/locks/"},
		{"config with prefix", "backup", types.FileTypeConfig, "backup/config"},

		{"pack no prefix", "", types.FileTypePack, "data/"},
		{"index no prefix", "", types.FileTypeIndex, "index/"},
		{"snapshot no prefix", "", types.FileTypeSnapshot, "snapshots/"},
		{"config no prefix", "", types.FileTypeConfig, "config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Backend{prefix: tt.prefix}
			got := b.dir(tt.ft)
			if got != tt.want {
				t.Errorf("dir(%v) = %q, want %q", tt.ft, got, tt.want)
			}
		})
	}
}

func TestJoin(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		parts  []string
		want   string
	}{
		{"single part with prefix", "backup", []string{"data"}, "backup/data"},
		{"multi parts with prefix", "backup", []string{"data", "ab", "file"}, "backup/data/ab/file"},
		{"single part no prefix", "", []string{"data"}, "data"},
		{"multi parts no prefix", "", []string{"data", "ab", "file"}, "data/ab/file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Backend{prefix: tt.prefix}
			got := b.join(tt.parts...)
			if got != tt.want {
				t.Errorf("join(%v) = %q, want %q", tt.parts, got, tt.want)
			}
		})
	}
}

func TestLocation(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		bucket   string
		prefix   string
		want     string
	}{
		{
			"with prefix",
			"s3.us-west-004.backblazeb2.com",
			"my-backups",
			"doomsday",
			"s3:s3.us-west-004.backblazeb2.com/my-backups/doomsday",
		},
		{
			"no prefix",
			"s3.amazonaws.com",
			"my-bucket",
			"",
			"s3:s3.amazonaws.com/my-bucket",
		},
		{
			"minio localhost",
			"localhost:9000",
			"test",
			"repo",
			"s3:localhost:9000/test/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Backend{
				endpoint: tt.endpoint,
				bucket:   tt.bucket,
				prefix:   tt.prefix,
			}
			got := b.Location()
			if got != tt.want {
				t.Errorf("Location() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPackHexPrefixVariety(t *testing.T) {
	b := &Backend{prefix: "repo"}

	// Verify different pack names produce different hex prefix subdirectories.
	names := []struct {
		name       string
		wantPrefix string
	}{
		{"aabbccddee1234567890123456789012", "aa"},
		{"ffbbccddee1234567890123456789012", "ff"},
		{"00bbccddee1234567890123456789012", "00"},
		{"1234567890abcdef1234567890abcdef", "12"},
	}

	for _, tt := range names {
		t.Run(tt.name[:4], func(t *testing.T) {
			key := b.key(types.FileTypePack, tt.name)
			wantKey := fmt.Sprintf("repo/data/%s/%s", tt.wantPrefix, tt.name)
			if key != wantKey {
				t.Errorf("key = %q, want %q", key, wantKey)
			}
		})
	}
}

func TestCloseIsNoop(t *testing.T) {
	b := &Backend{}
	if err := b.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// --- Integration tests: require a real S3-compatible endpoint ---
// Set DOOMSDAY_TEST_S3=1 and provide:
//   DOOMSDAY_TEST_S3_ENDPOINT  (e.g. "localhost:9000" or "s3.amazonaws.com")
//   DOOMSDAY_TEST_S3_BUCKET
//   DOOMSDAY_TEST_S3_PREFIX    (optional, defaults to "doomsday-test")
//   DOOMSDAY_TEST_S3_KEY_ID
//   DOOMSDAY_TEST_S3_SECRET_KEY
//   DOOMSDAY_TEST_S3_USE_SSL   (optional, "true" or "false", defaults to "false")

func integrationBackend(t *testing.T) *Backend {
	t.Helper()
	if os.Getenv("DOOMSDAY_TEST_S3") != "1" {
		t.Skip("skipping S3 integration test (set DOOMSDAY_TEST_S3=1)")
	}

	endpoint := os.Getenv("DOOMSDAY_TEST_S3_ENDPOINT")
	bucket := os.Getenv("DOOMSDAY_TEST_S3_BUCKET")
	prefix := os.Getenv("DOOMSDAY_TEST_S3_PREFIX")
	keyID := os.Getenv("DOOMSDAY_TEST_S3_KEY_ID")
	secretKey := os.Getenv("DOOMSDAY_TEST_S3_SECRET_KEY")
	useSSL := os.Getenv("DOOMSDAY_TEST_S3_USE_SSL") == "true"

	if endpoint == "" || bucket == "" || keyID == "" || secretKey == "" {
		t.Fatal("DOOMSDAY_TEST_S3_ENDPOINT, DOOMSDAY_TEST_S3_BUCKET, DOOMSDAY_TEST_S3_KEY_ID, DOOMSDAY_TEST_S3_SECRET_KEY must be set")
	}

	if prefix == "" {
		prefix = "doomsday-test"
	}

	b, err := New(endpoint, bucket, prefix, keyID, secretKey, useSSL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		// Clean up test objects.
		ctx := context.Background()
		for _, ft := range []types.FileType{
			types.FileTypePack, types.FileTypeIndex, types.FileTypeSnapshot,
			types.FileTypeKey, types.FileTypeLock, types.FileTypeConfig,
		} {
			_ = b.List(ctx, ft, func(fi types.FileInfo) error {
				_ = b.Remove(ctx, ft, fi.Name)
				return nil
			})
		}
	})

	return b
}

func TestIntegrationSaveAndLoad(t *testing.T) {
	b := integrationBackend(t)
	ctx := context.Background()
	data := []byte("hello doomsday s3")

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

func TestIntegrationLoadRange(t *testing.T) {
	b := integrationBackend(t)
	ctx := context.Background()
	data := []byte("0123456789")

	if err := b.Save(ctx, types.FileTypeIndex, "idx-range", bytes.NewReader(data)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tests := []struct {
		name     string
		offset   int64
		length   int64
		expected string
	}{
		{"full file", 0, 0, "0123456789"},
		{"from offset with length", 3, 4, "3456"},
		{"from start with length", 0, 5, "01234"},
		{"from offset to end", 7, 0, "789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, err := b.Load(ctx, types.FileTypeIndex, "idx-range", tt.offset, tt.length)
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

func TestIntegrationStat(t *testing.T) {
	b := integrationBackend(t)
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

func TestIntegrationStatNotFound(t *testing.T) {
	b := integrationBackend(t)
	ctx := context.Background()
	_, err := b.Stat(ctx, types.FileTypeSnapshot, "nonexistent-file-xyz")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestIntegrationRemove(t *testing.T) {
	b := integrationBackend(t)
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

func TestIntegrationList(t *testing.T) {
	b := integrationBackend(t)
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
		t.Errorf("names = %v, want sorted [snap-a snap-b snap-c]", names)
	}
}

func TestIntegrationPackFileHexPrefix(t *testing.T) {
	b := integrationBackend(t)
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

func TestIntegrationConfigFile(t *testing.T) {
	b := integrationBackend(t)
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

func TestIntegrationLocation(t *testing.T) {
	b := integrationBackend(t)
	loc := b.Location()
	if loc == "" {
		t.Error("Location should not be empty")
	}
	if len(loc) < 3 || loc[:3] != "s3:" {
		t.Errorf("Location() = %q, should start with s3:", loc)
	}
}
