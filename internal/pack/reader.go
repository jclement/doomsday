package pack

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// MaxHeaderSize is the maximum allowed encrypted header size (64 MiB).
	// A legitimate pack header with 100k entries is a few MB at most.
	MaxHeaderSize = 64 << 20

	// MinHeaderSize is the minimum encrypted header (AES-GCM nonce + tag = 28 bytes).
	MinHeaderSize = 28

	// MaxEntriesPerPack limits the number of entries in a single pack header
	// to prevent OOM from a malicious header with millions of entries.
	MaxEntriesPerPack = 100_000

	// MaxBlobSize is the maximum allowed individual blob size (32 MiB).
	// Chunker output is typically much smaller.
	MaxBlobSize = 32 << 20
)

// DecryptFunc decrypts a header blob.
type DecryptFunc func(ciphertext []byte) ([]byte, error)

// ReadHeader reads and decrypts the header from a pack file.
// The pack file must be seekable (io.ReaderAt) since the header is at the end.
func ReadHeader(r io.ReaderAt, totalSize int64, decrypt DecryptFunc) (Header, error) {
	if totalSize < 4 {
		return nil, fmt.Errorf("pack.ReadHeader: file too small (%d bytes)", totalSize)
	}

	// Read header length (last 4 bytes)
	var lenBuf [4]byte
	if _, err := r.ReadAt(lenBuf[:], totalSize-4); err != nil {
		return nil, fmt.Errorf("pack.ReadHeader: read header length: %w", err)
	}
	headerLen := binary.LittleEndian.Uint32(lenBuf[:])

	if headerLen == 0 {
		return nil, fmt.Errorf("pack.ReadHeader: header length is zero")
	}

	if headerLen > MaxHeaderSize {
		return nil, fmt.Errorf("pack.ReadHeader: header length %d exceeds maximum (%d)", headerLen, MaxHeaderSize)
	}

	if int64(headerLen)+4 > totalSize {
		return nil, fmt.Errorf("pack.ReadHeader: header length %d exceeds file size %d", headerLen, totalSize)
	}

	// Read encrypted header
	headerStart := totalSize - 4 - int64(headerLen)
	encryptedHeader := make([]byte, headerLen)
	if _, err := r.ReadAt(encryptedHeader, headerStart); err != nil {
		return nil, fmt.Errorf("pack.ReadHeader: read header: %w", err)
	}

	// Decrypt header
	headerData, err := decrypt(encryptedHeader)
	if err != nil {
		return nil, fmt.Errorf("pack.ReadHeader: decrypt header: %w", err)
	}

	// Parse header
	header, err := UnmarshalHeader(headerData)
	if err != nil {
		return nil, fmt.Errorf("pack.ReadHeader: %w", err)
	}

	// Validate entry count.
	if len(header) > MaxEntriesPerPack {
		return nil, fmt.Errorf("pack.ReadHeader: %d entries exceeds maximum (%d)", len(header), MaxEntriesPerPack)
	}

	// Validate entry offsets and lengths against the blob data region.
	blobRegionSize := uint64(headerStart)
	for i, entry := range header {
		if entry.Length > MaxBlobSize {
			return nil, fmt.Errorf("pack.ReadHeader: entry %d: length %d exceeds maximum (%d)", i, entry.Length, MaxBlobSize)
		}
		end := uint64(entry.Offset) + uint64(entry.Length)
		if end > blobRegionSize {
			return nil, fmt.Errorf("pack.ReadHeader: entry %d: offset+length (%d) exceeds blob region (%d)", i, end, blobRegionSize)
		}
	}

	return header, nil
}

// ReadBlob reads a single blob from a pack file using the header entry's offset and length.
func ReadBlob(r io.ReaderAt, entry HeaderEntry) ([]byte, error) {
	if entry.Length == 0 {
		return nil, fmt.Errorf("pack.ReadBlob: zero-length entry")
	}
	if entry.Length > MaxBlobSize {
		return nil, fmt.Errorf("pack.ReadBlob: entry length %d exceeds maximum (%d)", entry.Length, MaxBlobSize)
	}
	buf := make([]byte, entry.Length)
	n, err := r.ReadAt(buf, int64(entry.Offset))
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("pack.ReadBlob: %w", err)
	}
	if n != int(entry.Length) {
		return nil, fmt.Errorf("pack.ReadBlob: short read: %d/%d", n, entry.Length)
	}
	return buf, nil
}
