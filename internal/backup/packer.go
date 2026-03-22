package backup

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/jclement/doomsday/internal/pack"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/types"
)

// packTargetSize is the target pack file size (~16 MiB).
const packTargetSize = 16 * 1024 * 1024

// pendingBlob represents a blob waiting to be packed.
type pendingBlob struct {
	ID                 types.BlobID
	Type               types.BlobType
	Ciphertext         []byte
	UncompressedLength uint32
}

// packer accumulates encrypted blobs and flushes them as pack files
// when the accumulated size reaches the target (~16 MiB).
//
// Thread-safe: multiple goroutines can call AddBlob concurrently.
type packer struct {
	mu       sync.Mutex
	r        *repo.Repository
	blobType types.BlobType
	pending  []pendingBlob
	size     uint32 // current accumulated ciphertext size
	progress *progressTracker

	// flushed tracks all blobs that have been written to packs,
	// keyed by packID for index registration.
	flushed []flushedPack
}

// flushedPack records a completed pack and the blobs it contains.
type flushedPack struct {
	PackID string
	Blobs  []types.PackedBlob
}

// preparedPack holds a fully serialized pack ready for I/O.
type preparedPack struct {
	data    []byte
	entries pack.Header
}

// newPacker creates a packer for the given blob type.
func newPacker(r *repo.Repository, bt types.BlobType, progress *progressTracker) *packer {
	return &packer{
		r:        r,
		blobType: bt,
		progress: progress,
	}
}

// AddBlob adds an encrypted blob to the packer. If the packer is full,
// it flushes the current pack before adding the new blob.
func (p *packer) AddBlob(ctx context.Context, id types.BlobID, ciphertext []byte, uncompressedLen uint32) error {
	p.mu.Lock()

	// If adding this blob would exceed the target, prepare a pack under the lock
	// and then do the I/O outside the lock to avoid blocking other workers.
	var prepared *preparedPack
	if p.size > 0 && p.size+uint32(len(ciphertext)) > packTargetSize {
		var err error
		prepared, err = p.prepareLocked()
		if err != nil {
			p.mu.Unlock()
			return err
		}
	}

	p.pending = append(p.pending, pendingBlob{
		ID:                 id,
		Type:               p.blobType,
		Ciphertext:         ciphertext,
		UncompressedLength: uncompressedLen,
	})
	p.size += uint32(len(ciphertext))
	p.mu.Unlock()

	// Perform I/O outside the lock.
	if prepared != nil {
		if err := p.savePack(ctx, prepared); err != nil {
			return err
		}
	}

	return nil
}

// Flush writes any remaining pending blobs as a pack file.
// Must be called after all blobs have been added to ensure nothing is left behind.
func (p *packer) Flush(ctx context.Context) error {
	p.mu.Lock()
	prepared, err := p.prepareLocked()
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if prepared == nil {
		return nil
	}
	return p.savePack(ctx, prepared)
}

// prepareLocked serializes the current pending blobs into a pack buffer and
// resets the pending list. Caller must hold p.mu. Returns nil if nothing pending.
func (p *packer) prepareLocked() (*preparedPack, error) {
	if len(p.pending) == 0 {
		return nil, nil
	}

	// Build the pack file in memory
	var buf bytes.Buffer
	pw := pack.NewWriter(&buf)

	for _, blob := range p.pending {
		if err := pw.AddBlob(blob.ID, blob.Type, blob.UncompressedLength, blob.Ciphertext); err != nil {
			return nil, fmt.Errorf("backup.packer: add blob: %w", err)
		}
	}

	// Finalize with encrypted header
	encryptFn := p.r.EncryptHeader(p.blobType)
	if err := pw.Finalize(encryptFn); err != nil {
		return nil, fmt.Errorf("backup.packer: finalize: %w", err)
	}

	// Reset pending under the lock
	p.pending = p.pending[:0]
	p.size = 0

	return &preparedPack{
		data:    buf.Bytes(),
		entries: pw.Entries(),
	}, nil
}

// savePack writes a prepared pack to the backend and registers blobs in the index.
// Called outside the mutex so I/O doesn't block other workers.
func (p *packer) savePack(ctx context.Context, pp *preparedPack) error {
	packID, err := p.r.SavePack(ctx, pp.data, p.blobType)
	if err != nil {
		return fmt.Errorf("backup.packer: save pack: %w", err)
	}

	// Build the packed blob list from the header entries
	blobs := make([]types.PackedBlob, len(pp.entries))
	for i, e := range pp.entries {
		blobs[i] = types.PackedBlob{
			ID:                 e.ID,
			Type:               e.Type,
			PackID:             packID,
			Offset:             e.Offset,
			Length:             e.Length,
			UncompressedLength: e.UncompressedLength,
		}
	}

	// Register blobs in the index (Index has its own locking)
	p.r.Index().Add(packID, blobs)

	// Track the flushed pack (protected by mu since flushed is read later)
	p.mu.Lock()
	p.flushed = append(p.flushed, flushedPack{
		PackID: packID,
		Blobs:  blobs,
	})
	p.mu.Unlock()

	if p.progress != nil {
		p.progress.packsFlushed.Add(1)
		p.progress.bytesPacked.Add(int64(len(pp.data)))
		p.progress.report()
	}

	return nil
}

// FlushedPacks returns all packs that have been flushed.
func (p *packer) FlushedPacks() []flushedPack {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]flushedPack, len(p.flushed))
	copy(result, p.flushed)
	return result
}
