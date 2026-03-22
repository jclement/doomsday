package cache

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/jclement/doomsday/internal/types"
)

func randomID() types.BlobID {
	var id types.BlobID
	rand.Read(id[:])
	return id
}

func TestPutAndGet(t *testing.T) {
	c, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	id := randomID()
	data := []byte("cached blob data")

	if err := c.Put(id, data); err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Error("data mismatch")
	}
}

func TestGetMiss(t *testing.T) {
	c, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(randomID())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for cache miss")
	}
}

func TestHas(t *testing.T) {
	c, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	id := randomID()
	if c.Has(id) {
		t.Error("should not have blob before Put")
	}

	c.Put(id, []byte("data"))
	if !c.Has(id) {
		t.Error("should have blob after Put")
	}
}
