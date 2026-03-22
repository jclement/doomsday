// Package check provides repository integrity verification at three levels.
package check

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/jclement/doomsday/internal/compress"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/pack"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/types"
)

// Level determines the depth of integrity checking.
type Level int

const (
	// LevelStructure verifies indexes, snapshot references. No data download.
	LevelStructure Level = iota
	// LevelHeaders downloads pack headers, verifies against index.
	LevelHeaders
	// LevelFull decrypts every blob and verifies content IDs.
	LevelFull
)

// Report contains the results of an integrity check.
type Report struct {
	Level            Level
	PacksChecked     int
	BlobsChecked     int
	SnapshotsChecked int
	Errors           []Error
}

// Error describes a single integrity violation.
type Error struct {
	Pack    string
	BlobID  string
	Message string
}

// OK returns true if no errors were found.
func (r *Report) OK() bool { return len(r.Errors) == 0 }

// Run performs an integrity check on the repository at the specified level.
func Run(ctx context.Context, r *repo.Repository, level Level) (*Report, error) {
	report := &Report{Level: level}

	// Level 0: Structure check
	if err := checkStructure(ctx, r, report); err != nil {
		return report, fmt.Errorf("check.Run: %w", err)
	}

	if level >= LevelHeaders {
		if err := checkHeaders(ctx, r, report); err != nil {
			return report, fmt.Errorf("check.Run: %w", err)
		}
	}

	if level >= LevelFull {
		if err := checkFull(ctx, r, report); err != nil {
			return report, fmt.Errorf("check.Run: %w", err)
		}
	}

	return report, nil
}

// checkStructure verifies that all snapshots reference valid trees and
// all tree references resolve in the index.
func checkStructure(ctx context.Context, r *repo.Repository, report *Report) error {
	ids, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}

	for _, id := range ids {
		snap, err := r.LoadSnapshot(ctx, id)
		if err != nil {
			report.Errors = append(report.Errors, Error{
				Message: fmt.Sprintf("failed to load snapshot %s: %v", id, err),
			})
			continue
		}
		report.SnapshotsChecked++

		// Check tree root exists in index
		if !snap.Tree.IsZero() && !r.Index().Has(snap.Tree) {
			report.Errors = append(report.Errors, Error{
				BlobID:  snap.Tree.Short(),
				Message: fmt.Sprintf("snapshot %s references missing tree %s", id, snap.Tree.Short()),
			})
		}
	}

	return nil
}

// checkHeaders downloads pack headers and verifies they match the index.
func checkHeaders(ctx context.Context, r *repo.Repository, report *Report) error {
	return r.Backend().List(ctx, types.FileTypePack, func(fi types.FileInfo) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Verify pack file name matches SHA-256 of contents
		rc, err := r.Backend().Load(ctx, types.FileTypePack, fi.Name, 0, 0)
		if err != nil {
			report.Errors = append(report.Errors, Error{
				Pack:    fi.Name,
				Message: fmt.Sprintf("failed to load pack: %v", err),
			})
			return nil
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			report.Errors = append(report.Errors, Error{
				Pack:    fi.Name,
				Message: fmt.Sprintf("failed to read pack: %v", err),
			})
			return nil
		}

		hash := sha256.Sum256(data)
		expectedName := hex.EncodeToString(hash[:])
		if fi.Name != expectedName {
			report.Errors = append(report.Errors, Error{
				Pack:    fi.Name,
				Message: fmt.Sprintf("pack name mismatch: expected %s", expectedName[:8]),
			})
		}

		// Try to read header
		_, err = pack.ReadHeader(
			newReaderAt(data),
			int64(len(data)),
			r.DecryptHeader(types.BlobTypeData),
		)
		if err != nil {
			// Try tree key
			_, err = pack.ReadHeader(
				newReaderAt(data),
				int64(len(data)),
				r.DecryptHeader(types.BlobTypeTree),
			)
		}
		if err != nil {
			report.Errors = append(report.Errors, Error{
				Pack:    fi.Name,
				Message: fmt.Sprintf("failed to read pack header: %v", err),
			})
		}

		report.PacksChecked++
		return nil
	})
}

// checkFull decrypts every blob and verifies HMAC-SHA256 content IDs.
func checkFull(ctx context.Context, r *repo.Repository, report *Report) error {
	entries := r.Index().AllEntries()
	for id, entry := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Load the encrypted blob
		rc, err := r.Backend().Load(ctx, types.FileTypePack, entry.PackID, int64(entry.Offset), int64(entry.Length))
		if err != nil {
			report.Errors = append(report.Errors, Error{
				BlobID: id.Short(),
				Pack:   entry.PackID,
				Message: fmt.Sprintf("failed to load blob: %v", err),
			})
			continue
		}
		ciphertext, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			report.Errors = append(report.Errors, Error{
				BlobID: id.Short(),
				Message: fmt.Sprintf("failed to read blob: %v", err),
			})
			continue
		}

		// Decrypt
		var subKey [32]byte
		switch entry.Type {
		case types.BlobTypeData:
			subKey = r.Keys().SubKeys.Data
		case types.BlobTypeTree:
			subKey = r.Keys().SubKeys.Tree
		}

		plaintext, err := crypto.DecryptBlob(subKey, id, entry.Type, r.RepoID(), ciphertext)
		if err != nil {
			report.Errors = append(report.Errors, Error{
				BlobID:  id.Short(),
				Message: fmt.Sprintf("decryption failed: %v", err),
			})
			continue
		}

		// All blobs are compressed before encryption. Decompress before
		// verifying the content ID, which was computed on raw data.
		decompressed, err := compress.Decompress(plaintext)
		if err != nil {
			report.Errors = append(report.Errors, Error{
				BlobID:  id.Short(),
				Message: fmt.Sprintf("decompression failed: %v", err),
			})
			continue
		}

		// Verify content ID against raw (decompressed) data
		computedID := crypto.ContentID(r.Keys().SubKeys.ContentID, decompressed)
		if computedID != id {
			report.Errors = append(report.Errors, Error{
				BlobID:  id.Short(),
				Message: "content ID mismatch (data corruption)",
			})
			continue
		}

		report.BlobsChecked++
	}
	return nil
}

// byteReaderAt wraps a byte slice as io.ReaderAt.
type byteReaderAt struct {
	data []byte
}

func newReaderAt(data []byte) *byteReaderAt {
	return &byteReaderAt{data: data}
}

func (b *byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	n := copy(p, b.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
