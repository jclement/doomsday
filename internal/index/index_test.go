package index

import (
	"crypto/rand"
	"sync"
	"testing"

	"github.com/jclement/doomsday/internal/types"
)

func randomID() types.BlobID {
	var id types.BlobID
	rand.Read(id[:])
	return id
}

func TestCheckAndAdd_NewBlob(t *testing.T) {
	idx := New()
	id := randomID()

	if !idx.CheckAndAdd(id) {
		t.Error("first CheckAndAdd should return true")
	}
	if idx.CheckAndAdd(id) {
		t.Error("second CheckAndAdd should return false (pending)")
	}
}

func TestCheckAndAdd_ExistingBlob(t *testing.T) {
	idx := New()
	id := randomID()

	// Pre-populate as stored
	idx.Add("pack-1", []types.PackedBlob{{ID: id, Offset: 0, Length: 100}})

	if idx.CheckAndAdd(id) {
		t.Error("CheckAndAdd on existing blob should return false")
	}
}

func TestCheckAndAdd_Concurrent(t *testing.T) {
	idx := New()
	id := randomID()

	var wg sync.WaitGroup
	claimed := make(chan bool, 100)

	// 100 goroutines race to claim the same blob
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed <- idx.CheckAndAdd(id)
		}()
	}

	wg.Wait()
	close(claimed)

	// Exactly one should have claimed it
	claimCount := 0
	for c := range claimed {
		if c {
			claimCount++
		}
	}
	if claimCount != 1 {
		t.Errorf("expected exactly 1 claim, got %d", claimCount)
	}
}

func TestLookup(t *testing.T) {
	idx := New()
	id := randomID()

	idx.Add("pack-abc", []types.PackedBlob{
		{ID: id, Type: types.BlobTypeData, Offset: 42, Length: 100, UncompressedLength: 200},
	})

	entry, ok := idx.Lookup(id)
	if !ok {
		t.Fatal("Lookup should find the blob")
	}
	if entry.PackID != "pack-abc" {
		t.Errorf("PackID = %q, want %q", entry.PackID, "pack-abc")
	}
	if entry.Offset != 42 {
		t.Errorf("Offset = %d, want 42", entry.Offset)
	}
}

func TestLookup_NotFound(t *testing.T) {
	idx := New()
	_, ok := idx.Lookup(randomID())
	if ok {
		t.Error("Lookup should return false for unknown blob")
	}
}

func TestHas(t *testing.T) {
	idx := New()
	id := randomID()

	if idx.Has(id) {
		t.Error("Has should return false for unknown blob")
	}

	idx.CheckAndAdd(id) // now pending
	if !idx.Has(id) {
		t.Error("Has should return true for pending blob")
	}

	idx.Add("pack-1", []types.PackedBlob{{ID: id, Offset: 0, Length: 50}})
	if !idx.Has(id) {
		t.Error("Has should return true for stored blob")
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	idx := New()
	id1 := randomID()
	id2 := randomID()

	idx.Add("pack-a", []types.PackedBlob{
		{ID: id1, Type: types.BlobTypeData, Offset: 0, Length: 100},
	})
	idx.Add("pack-b", []types.PackedBlob{
		{ID: id2, Type: types.BlobTypeTree, Offset: 50, Length: 200},
	})

	data, err := idx.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	idx2 := New()
	if err := idx2.Unmarshal(data); err != nil {
		t.Fatal(err)
	}

	if idx2.Len() != 2 {
		t.Errorf("deserialized index has %d entries, want 2", idx2.Len())
	}

	e1, ok := idx2.Lookup(id1)
	if !ok {
		t.Fatal("id1 not found")
	}
	if e1.PackID != "pack-a" {
		t.Errorf("id1 pack = %q, want pack-a", e1.PackID)
	}
}

func TestPackIDs(t *testing.T) {
	idx := New()
	idx.Add("pack-a", []types.PackedBlob{{ID: randomID(), Offset: 0, Length: 10}})
	idx.Add("pack-b", []types.PackedBlob{{ID: randomID(), Offset: 0, Length: 10}})
	idx.Add("pack-a", []types.PackedBlob{{ID: randomID(), Offset: 10, Length: 10}})

	packs := idx.PackIDs()
	if len(packs) != 2 {
		t.Errorf("PackIDs returned %d, want 2", len(packs))
	}
}

func TestLen(t *testing.T) {
	idx := New()
	if idx.Len() != 0 {
		t.Error("empty index should have length 0")
	}
	idx.Add("p", []types.PackedBlob{{ID: randomID()}, {ID: randomID()}})
	if idx.Len() != 2 {
		t.Errorf("Len() = %d, want 2", idx.Len())
	}
}

func FuzzIndexUnmarshal(f *testing.F) {
	// Seed corpus: valid index JSON, invalid data, empty
	idx := New()
	idx.Add("pack-seed", []types.PackedBlob{
		{ID: randomID(), Type: types.BlobTypeData, Offset: 0, Length: 100},
	})
	validData, _ := idx.Marshal()
	f.Add(validData)
	f.Add([]byte{})
	f.Add([]byte("not json"))
	f.Add([]byte(`{"entries":{}}`))
	f.Add([]byte(`{"entries":{"badhex":{"pack_id":"p","offset":0,"length":1,"type":0}}}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic. Errors are acceptable.
		target := New()
		_ = target.Unmarshal(data)
	})
}
