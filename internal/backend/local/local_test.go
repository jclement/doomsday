package local

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/jclement/doomsday/internal/types"
)

func testBackend(t *testing.T) *Backend {
	t.Helper()
	dir := t.TempDir()
	b, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
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

	b.Save(ctx, types.FileTypeIndex, "idx-001", bytes.NewReader(data))

	rc, err := b.Load(ctx, types.FileTypeIndex, "idx-001", 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if string(got) != "3456" {
		t.Errorf("got %q, want %q", got, "3456")
	}
}

func TestStat(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	data := []byte("test data for stat")

	b.Save(ctx, types.FileTypeSnapshot, "snap-stat", bytes.NewReader(data))

	info, err := b.Stat(ctx, types.FileTypeSnapshot, "snap-stat")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(data)) {
		t.Errorf("size = %d, want %d", info.Size, len(data))
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

	b.Save(ctx, types.FileTypeSnapshot, "snap-rm", bytes.NewReader([]byte("data")))

	if err := b.Remove(ctx, types.FileTypeSnapshot, "snap-rm"); err != nil {
		t.Fatal(err)
	}

	// Should be gone
	_, err := b.Stat(ctx, types.FileTypeSnapshot, "snap-rm")
	if err == nil {
		t.Error("file should be removed")
	}

	// Remove again should be idempotent
	if err := b.Remove(ctx, types.FileTypeSnapshot, "snap-rm"); err != nil {
		t.Errorf("idempotent remove failed: %v", err)
	}
}

func TestList(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	for _, name := range []string{"snap-c", "snap-a", "snap-b"} {
		b.Save(ctx, types.FileTypeSnapshot, name, bytes.NewReader([]byte("data")))
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
	// Should be sorted
	if names[0] != "snap-a" || names[1] != "snap-b" || names[2] != "snap-c" {
		t.Errorf("names = %v, want sorted", names)
	}
}

func TestPackFileHexPrefix(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()

	name := "abcdef1234567890abcdef1234567890"
	b.Save(ctx, types.FileTypePack, name, bytes.NewReader([]byte("pack data")))

	// Should be readable
	rc, err := b.Load(ctx, types.FileTypePack, name, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "pack data" {
		t.Error("pack data mismatch")
	}

	// List should find it
	var found bool
	b.List(ctx, types.FileTypePack, func(fi types.FileInfo) error {
		if fi.Name == name {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("pack file not found in list")
	}
}

func TestLocation(t *testing.T) {
	b := testBackend(t)
	if b.Location() == "" {
		t.Error("Location should not be empty")
	}
}

func TestOpen_NotExist(t *testing.T) {
	_, err := Open("/nonexistent/path/to/repo")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}
