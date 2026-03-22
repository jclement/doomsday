// Package types defines shared types and interfaces used across all doomsday packages.
// This package has zero internal dependencies — only Go stdlib.
package types

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// BlobID is the HMAC-SHA256 content identifier for a blob (32 bytes).
type BlobID [32]byte

// String returns the hex-encoded blob ID.
func (id BlobID) String() string {
	return hex.EncodeToString(id[:])
}

// Short returns the first 8 hex characters for display.
func (id BlobID) Short() string {
	return hex.EncodeToString(id[:4])
}

// IsZero returns true if the blob ID is all zeros.
func (id BlobID) IsZero() bool {
	return id == BlobID{}
}

// ParseBlobID parses a hex-encoded blob ID string.
func ParseBlobID(s string) (BlobID, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return BlobID{}, fmt.Errorf("types.ParseBlobID: %w", err)
	}
	if len(b) != 32 {
		return BlobID{}, fmt.Errorf("types.ParseBlobID: expected 32 bytes, got %d", len(b))
	}
	var id BlobID
	copy(id[:], b)
	return id, nil
}

// FileType identifies the category of a file within the repository layout.
type FileType int

const (
	FileTypePack     FileType = iota // data/ — pack files containing blobs
	FileTypeIndex                    // index/ — blob-to-pack mapping files
	FileTypeSnapshot                 // snapshots/ — snapshot metadata
	FileTypeKey                      // keys/ — key files
	FileTypeConfig                   // config — repository configuration
	FileTypeLock                     // locks/ — lock files
)

func (ft FileType) String() string {
	switch ft {
	case FileTypePack:
		return "data"
	case FileTypeIndex:
		return "index"
	case FileTypeSnapshot:
		return "snapshots"
	case FileTypeKey:
		return "keys"
	case FileTypeConfig:
		return "config"
	case FileTypeLock:
		return "locks"
	default:
		return fmt.Sprintf("unknown(%d)", int(ft))
	}
}

// BlobType identifies the sub-category of a blob within a pack file.
type BlobType uint8

const (
	BlobTypeData BlobType = iota // File content chunk
	BlobTypeTree                 // Directory listing
)

func (bt BlobType) String() string {
	switch bt {
	case BlobTypeData:
		return "data"
	case BlobTypeTree:
		return "tree"
	default:
		return fmt.Sprintf("unknown(%d)", int(bt))
	}
}

// ValidateName checks that a backend file name is safe for use as a path
// component. Rejects names containing path separators, traversal sequences,
// or null bytes that could escape the repository directory.
func ValidateName(name string) error {
	if name == "" {
		return nil // empty names are valid for config file type
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("types.ValidateName: invalid name %q: contains path separator or null byte", name)
	}
	if name == ".." || name == "." {
		return fmt.Errorf("types.ValidateName: invalid name %q: path traversal", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("types.ValidateName: invalid name %q: contains path traversal sequence", name)
	}
	return nil
}

// PackedBlob describes a blob stored within a pack file.
type PackedBlob struct {
	ID                 BlobID
	Type               BlobType
	PackID             string // name of the pack file
	Offset             uint32 // byte offset within the pack
	Length             uint32 // encrypted length in pack
	UncompressedLength uint32 // original size before compression (0 if uncompressed)
}
