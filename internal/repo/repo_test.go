package repo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/types"
)

func testMasterKey(t *testing.T) crypto.MasterKey {
	t.Helper()
	key, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testRepo(t *testing.T) *Repository {
	t.Helper()
	b, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	master := testMasterKey(t)
	r, err := Init(ctx, b, master)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInitAndOpen(t *testing.T) {
	dir := t.TempDir()
	b, _ := local.New(dir)
	ctx := context.Background()
	master := testMasterKey(t)

	r, err := Init(ctx, b, master)
	if err != nil {
		t.Fatal(err)
	}
	if r.Config().Version != FormatVersion {
		t.Errorf("version = %d", r.Config().Version)
	}
	if r.RepoID() == "" {
		t.Error("empty repo ID")
	}

	// Re-open
	b2, _ := local.Open(dir)
	r2, err := Open(ctx, b2, master)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r2.RepoID() != r.RepoID() {
		t.Errorf("repo ID mismatch: %q vs %q", r2.RepoID(), r.RepoID())
	}
}

func TestOpenWrongKey(t *testing.T) {
	dir := t.TempDir()
	b, _ := local.New(dir)
	ctx := context.Background()
	master := testMasterKey(t)
	Init(ctx, b, master)

	wrongKey := testMasterKey(t)
	b2, _ := local.Open(dir)
	_, err := Open(ctx, b2, wrongKey)
	if err == nil {
		t.Error("expected error with wrong key")
	}
}

func TestSaveAndLoadSnapshot(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	snap := &snapshot.Snapshot{
		ID:               "test-snap-001",
		Time:             time.Now().Truncate(time.Second),
		Hostname:         "test-host",
		Paths:            []string{"/tmp/test"},
		BackupConfigName: "test",
		Tree:             types.BlobID{0x01, 0x02},
	}

	if err := r.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}

	loaded, err := r.LoadSnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != snap.ID {
		t.Errorf("ID = %q", loaded.ID)
	}
	if loaded.Hostname != snap.Hostname {
		t.Errorf("Hostname = %q", loaded.Hostname)
	}
	if loaded.Tree != snap.Tree {
		t.Error("Tree mismatch")
	}
}

func TestListSnapshots(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	for _, id := range []string{"snap-a", "snap-b", "snap-c"} {
		snap := &snapshot.Snapshot{ID: id, Time: time.Now(), Hostname: "test"}
		r.SaveSnapshot(ctx, snap)
	}

	ids, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Errorf("listed %d snapshots, want 3", len(ids))
	}
}

func TestSaveAndLoadIndex(t *testing.T) {
	dir := t.TempDir()
	b, _ := local.New(dir)
	ctx := context.Background()
	master := testMasterKey(t)

	r, _ := Init(ctx, b, master)

	// Add some blobs to the index
	id1 := r.ContentID([]byte("chunk-1"))
	id2 := r.ContentID([]byte("chunk-2"))
	r.Index().Add("pack-001", []types.PackedBlob{
		{ID: id1, Type: types.BlobTypeData, Offset: 0, Length: 100},
		{ID: id2, Type: types.BlobTypeData, Offset: 100, Length: 200},
	})

	if err := r.SaveIndex(ctx); err != nil {
		t.Fatal(err)
	}

	// Re-open and verify index loaded
	b2, _ := local.Open(dir)
	r2, err := Open(ctx, b2, master)
	if err != nil {
		t.Fatal(err)
	}

	if r2.Index().Len() != 2 {
		t.Errorf("index has %d entries, want 2", r2.Index().Len())
	}

	e, ok := r2.Index().Lookup(id1)
	if !ok {
		t.Fatal("id1 not found")
	}
	if e.PackID != "pack-001" {
		t.Errorf("pack = %q", e.PackID)
	}
}

func TestIndexCache_PopulatesOnOpen(t *testing.T) {
	repoDir := t.TempDir()
	cacheDir := t.TempDir()
	b, _ := local.New(repoDir)
	ctx := context.Background()
	master := testMasterKey(t)

	// Init repo and add some index entries.
	r, _ := Init(ctx, b, master)
	id1 := r.ContentID([]byte("chunk-1"))
	r.Index().Add("pack-001", []types.PackedBlob{
		{ID: id1, Type: types.BlobTypeData, Offset: 0, Length: 100},
	})
	if err := r.SaveIndex(ctx); err != nil {
		t.Fatal(err)
	}
	r.Close()

	// Open with cache dir — should populate cache.
	b2, _ := local.Open(repoDir)
	r2, err := Open(ctx, b2, master, WithCacheDir(cacheDir))
	if err != nil {
		t.Fatal(err)
	}
	repoID := r2.RepoID()
	r2.Close()

	// Verify cache directory was populated.
	indexCacheDir := filepath.Join(cacheDir, repoID, "index")
	entries, err := os.ReadDir(indexCacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cached index file, got %d", len(entries))
	}
}

func TestIndexCache_HitOnSecondOpen(t *testing.T) {
	repoDir := t.TempDir()
	cacheDir := t.TempDir()
	b, _ := local.New(repoDir)
	ctx := context.Background()
	master := testMasterKey(t)

	// Init, add data, save index.
	r, _ := Init(ctx, b, master)
	id1 := r.ContentID([]byte("data-1"))
	id2 := r.ContentID([]byte("data-2"))
	r.Index().Add("pack-001", []types.PackedBlob{
		{ID: id1, Type: types.BlobTypeData, Offset: 0, Length: 100},
		{ID: id2, Type: types.BlobTypeData, Offset: 100, Length: 200},
	})
	if err := r.SaveIndex(ctx); err != nil {
		t.Fatal(err)
	}
	r.Close()

	// First open: populates cache from backend.
	b2, _ := local.Open(repoDir)
	r2, err := Open(ctx, b2, master, WithCacheDir(cacheDir))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Index().Len() != 2 {
		t.Fatalf("first open: index has %d entries, want 2", r2.Index().Len())
	}
	r2.Close()

	// Second open: should load from cache. Verify index still correct.
	b3, _ := local.Open(repoDir)
	r3, err := Open(ctx, b3, master, WithCacheDir(cacheDir))
	if err != nil {
		t.Fatal(err)
	}
	if r3.Index().Len() != 2 {
		t.Fatalf("second open: index has %d entries, want 2", r3.Index().Len())
	}
	e, ok := r3.Index().Lookup(id1)
	if !ok {
		t.Fatal("id1 not found on cached open")
	}
	if e.PackID != "pack-001" {
		t.Errorf("pack = %q", e.PackID)
	}
	r3.Close()
}

func TestIndexCache_SaveIndexWritesToCache(t *testing.T) {
	repoDir := t.TempDir()
	cacheDir := t.TempDir()
	b, _ := local.New(repoDir)
	ctx := context.Background()
	master := testMasterKey(t)

	// Init with cache dir.
	r, _ := Init(ctx, b, master)
	r.cacheDir = cacheDir
	repoID := r.RepoID()

	id1 := r.ContentID([]byte("chunk-1"))
	r.Index().Add("pack-001", []types.PackedBlob{
		{ID: id1, Type: types.BlobTypeData, Offset: 0, Length: 100},
	})
	if err := r.SaveIndex(ctx); err != nil {
		t.Fatal(err)
	}

	// Verify the index file was cached.
	indexCacheDir := filepath.Join(cacheDir, repoID, "index")
	entries, err := os.ReadDir(indexCacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cached index file after SaveIndex, got %d", len(entries))
	}
}

func TestIndexCache_PrunesStaleEntries(t *testing.T) {
	repoDir := t.TempDir()
	cacheDir := t.TempDir()
	b, _ := local.New(repoDir)
	ctx := context.Background()
	master := testMasterKey(t)

	// Init, save index.
	r, _ := Init(ctx, b, master)
	id1 := r.ContentID([]byte("chunk-1"))
	r.Index().Add("pack-001", []types.PackedBlob{
		{ID: id1, Type: types.BlobTypeData, Offset: 0, Length: 100},
	})
	if err := r.SaveIndex(ctx); err != nil {
		t.Fatal(err)
	}
	repoID := r.RepoID()
	r.Close()

	// First open: populates cache.
	b2, _ := local.Open(repoDir)
	r2, err := Open(ctx, b2, master, WithCacheDir(cacheDir))
	if err != nil {
		t.Fatal(err)
	}
	r2.Close()

	// Plant a stale cache file (simulates an index that was pruned remotely).
	staleFile := filepath.Join(cacheDir, repoID, "index", "stale-index-file.json")
	if err := os.WriteFile(staleFile, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}

	// Re-open: should prune the stale file.
	b3, _ := local.Open(repoDir)
	r3, err := Open(ctx, b3, master, WithCacheDir(cacheDir))
	if err != nil {
		t.Fatal(err)
	}
	r3.Close()

	// Verify stale file was removed.
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Error("stale cache file was not pruned")
	}

	// Verify valid cache file still exists.
	entries, err := os.ReadDir(filepath.Join(cacheDir, repoID, "index"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 cached index file after prune, got %d", len(entries))
	}
}

func TestOpenWithoutCache_StillWorks(t *testing.T) {
	dir := t.TempDir()
	b, _ := local.New(dir)
	ctx := context.Background()
	master := testMasterKey(t)

	r, _ := Init(ctx, b, master)
	id1 := r.ContentID([]byte("chunk-1"))
	r.Index().Add("pack-001", []types.PackedBlob{
		{ID: id1, Type: types.BlobTypeData, Offset: 0, Length: 100},
	})
	r.SaveIndex(ctx)
	r.Close()

	// Open without WithCacheDir (no options).
	b2, _ := local.Open(dir)
	r2, err := Open(ctx, b2, master)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Index().Len() != 1 {
		t.Errorf("index has %d entries, want 1", r2.Index().Len())
	}
	r2.Close()
}

func TestContentID_Deterministic(t *testing.T) {
	r := testRepo(t)
	data := []byte("test data for content ID")
	id1 := r.ContentID(data)
	id2 := r.ContentID(data)
	if id1 != id2 {
		t.Error("content ID not deterministic")
	}
}

func TestEncryptDecryptDataBlob(t *testing.T) {
	r := testRepo(t)
	plaintext := []byte("test blob data")
	id := r.ContentID(plaintext)

	ciphertext, err := r.EncryptDataBlob(id, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Decrypt via crypto package directly to verify
	decrypted, err := crypto.DecryptBlob(r.Keys().SubKeys.Data, id, types.BlobTypeData, r.RepoID(), ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != string(plaintext) {
		t.Error("roundtrip failed")
	}
}
