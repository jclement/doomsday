package lock

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/types"
)

func testKey() [32]byte {
	var key [32]byte
	rand.Read(key[:])
	return key
}

func testBackend(t *testing.T) types.Backend {
	t.Helper()
	b, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAcquireRelease(t *testing.T) {
	b := testBackend(t)
	key := testKey()
	ctx := context.Background()

	lock, err := Acquire(ctx, b, key, Exclusive, "backup")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestExclusiveConflict(t *testing.T) {
	b := testBackend(t)
	key := testKey()
	ctx := context.Background()

	lock1, err := Acquire(ctx, b, key, Exclusive, "backup")
	if err != nil {
		t.Fatal(err)
	}
	defer lock1.Release(ctx)

	_, err = Acquire(ctx, b, key, Exclusive, "prune")
	if err == nil {
		t.Error("expected lock conflict")
	}
}

func TestSharedConcurrent(t *testing.T) {
	b := testBackend(t)
	key := testKey()
	ctx := context.Background()

	lock1, err := Acquire(ctx, b, key, Shared, "check")
	if err != nil {
		t.Fatal(err)
	}
	defer lock1.Release(ctx)

	lock2, err := Acquire(ctx, b, key, Shared, "restore")
	if err != nil {
		t.Fatalf("shared locks should coexist: %v", err)
	}
	defer lock2.Release(ctx)
}

func TestExclusiveBlocksShared(t *testing.T) {
	b := testBackend(t)
	key := testKey()
	ctx := context.Background()

	lock1, err := Acquire(ctx, b, key, Exclusive, "backup")
	if err != nil {
		t.Fatal(err)
	}
	defer lock1.Release(ctx)

	_, err = Acquire(ctx, b, key, Shared, "check")
	if err == nil {
		t.Error("exclusive should block shared")
	}
}

func TestRemoveAll(t *testing.T) {
	b := testBackend(t)
	key := testKey()
	ctx := context.Background()

	lock1, _ := Acquire(ctx, b, key, Exclusive, "backup")
	lock1.cancel() // stop refresh but don't remove
	<-lock1.refreshed

	// Should have a lock file
	var count int
	b.List(ctx, types.FileTypeLock, func(fi types.FileInfo) error {
		count++
		return nil
	})
	if count == 0 {
		t.Fatal("expected at least one lock file")
	}

	// Remove all
	if err := RemoveAll(ctx, b); err != nil {
		t.Fatal(err)
	}

	count = 0
	b.List(ctx, types.FileTypeLock, func(fi types.FileInfo) error {
		count++
		return nil
	})
	if count != 0 {
		t.Errorf("expected 0 lock files, got %d", count)
	}
}
