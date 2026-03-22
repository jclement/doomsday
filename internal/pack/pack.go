// Package pack implements the doomsday pack file format.
//
// Pack format:
//
//	[EncryptedBlob1][EncryptedBlob2]...[EncryptedBlobN][EncryptedHeader][HeaderLength:4 bytes LE]
//
// The header is at the END for streaming writes (no seek required — essential for S3/B2).
// Each blob is independently encrypted with a per-blob derived key.
// HeaderLength is uint32 little-endian, giving the encrypted header size.
package pack

import (
	"encoding/json"
	"fmt"

	"github.com/jclement/doomsday/internal/types"
)

// HeaderEntry describes a single blob within a pack file.
type HeaderEntry struct {
	ID                 types.BlobID   `json:"id"`
	Type               types.BlobType `json:"type"`
	Offset             uint32         `json:"offset"`
	Length             uint32         `json:"length"`              // encrypted size in pack
	UncompressedLength uint32         `json:"uncompressed_length"` // 0 if not compressed
}

// Header is the collection of blob entries in a pack file.
type Header []HeaderEntry

// MarshalHeader serializes a header to JSON.
func MarshalHeader(h Header) ([]byte, error) {
	data, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("pack.MarshalHeader: %w", err)
	}
	return data, nil
}

// UnmarshalHeader deserializes a header from JSON.
func UnmarshalHeader(data []byte) (Header, error) {
	var h Header
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("pack.UnmarshalHeader: %w", err)
	}
	return h, nil
}
