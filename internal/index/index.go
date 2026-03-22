// Package index provides the in-memory blob-to-pack index with atomic dedup checking.
package index

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jclement/doomsday/internal/types"
)

// Entry records where a blob lives within a pack file.
type Entry struct {
	PackID             string         `json:"pack_id"`
	Offset             uint32         `json:"offset"`
	Length             uint32         `json:"length"`
	Type               types.BlobType `json:"type"`
	UncompressedLength uint32         `json:"uncompressed_length"`
}

// Index is the in-memory mapping of blob IDs to their pack file locations.
// Thread-safe for concurrent access.
type Index struct {
	mu      sync.RWMutex
	entries map[types.BlobID]Entry
	pending map[types.BlobID]struct{} // blobs being processed (not yet in a pack)
	strings map[string]string         // interned strings (PackID dedup)
}

// New creates an empty index.
func New() *Index {
	return &Index{
		entries: make(map[types.BlobID]Entry),
		pending: make(map[types.BlobID]struct{}),
		strings: make(map[string]string),
	}
}

// intern returns a shared copy of s, deduplicating identical strings.
func (idx *Index) intern(s string) string {
	if interned, ok := idx.strings[s]; ok {
		return interned
	}
	idx.strings[s] = s
	return s
}

// Add registers blobs from a completed pack file into the index.
func (idx *Index) Add(packID string, blobs []types.PackedBlob) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	interned := idx.intern(packID)
	for _, b := range blobs {
		idx.entries[b.ID] = Entry{
			PackID:             interned,
			Offset:             b.Offset,
			Length:             b.Length,
			Type:               b.Type,
			UncompressedLength: b.UncompressedLength,
		}
		delete(idx.pending, b.ID)
	}
}

// Lookup returns the pack location for a blob ID.
func (idx *Index) Lookup(id types.BlobID) (Entry, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	e, ok := idx.entries[id]
	return e, ok
}

// Has returns true if the blob is known (either stored or pending).
func (idx *Index) Has(id types.BlobID) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, inEntries := idx.entries[id]
	_, inPending := idx.pending[id]
	return inEntries || inPending
}

// CheckAndAdd atomically checks if a blob is known and, if not, marks it as pending.
// Returns true if this call claimed the blob (caller should store it).
// Returns false if the blob already exists or is pending (caller should skip).
//
// This is the critical dedup operation — a single mutex-protected check-and-set.
// No separate Has() + AddPending() to avoid TOCTOU races.
func (idx *Index) CheckAndAdd(id types.BlobID) bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, exists := idx.entries[id]; exists {
		return false
	}
	if _, pending := idx.pending[id]; pending {
		return false
	}

	idx.pending[id] = struct{}{}
	return true
}

// Len returns the total number of stored blobs (not counting pending).
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

// AllEntries returns a snapshot of all stored entries (for serialization).
func (idx *Index) AllEntries() map[types.BlobID]Entry {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	result := make(map[types.BlobID]Entry, len(idx.entries))
	for k, v := range idx.entries {
		result[k] = v
	}
	return result
}

// PackIDs returns the set of unique pack IDs referenced in the index.
func (idx *Index) PackIDs() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, e := range idx.entries {
		seen[e.PackID] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	return result
}

// serializedIndex is the JSON format for persisting the index.
type serializedIndex struct {
	Entries map[string]Entry `json:"entries"` // hex-encoded BlobID -> Entry
}

// Marshal serializes the index to JSON.
func (idx *Index) Marshal() ([]byte, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	si := serializedIndex{
		Entries: make(map[string]Entry, len(idx.entries)),
	}
	for id, e := range idx.entries {
		si.Entries[id.String()] = e
	}

	data, err := json.Marshal(si)
	if err != nil {
		return nil, fmt.Errorf("index.Marshal: %w", err)
	}
	return data, nil
}

// Unmarshal deserializes an index from JSON and merges into this index.
func (idx *Index) Unmarshal(data []byte) error {
	var si serializedIndex
	if err := json.Unmarshal(data, &si); err != nil {
		return fmt.Errorf("index.Unmarshal: %w", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	for hexID, e := range si.Entries {
		id, err := types.ParseBlobID(hexID)
		if err != nil {
			return fmt.Errorf("index.Unmarshal: %w", err)
		}
		e.PackID = idx.intern(e.PackID)
		idx.entries[id] = e
	}
	return nil
}
