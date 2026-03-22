package pack

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/jclement/doomsday/internal/types"
)

// EncryptFunc encrypts a header blob. Used to encrypt the pack header.
type EncryptFunc func(plaintext []byte) ([]byte, error)

// Writer writes blobs to a pack file in streaming fashion.
// The header is written at the end, so no seeking is required.
type Writer struct {
	w       io.Writer
	entries Header
	offset  uint32
}

// NewWriter creates a pack writer that writes to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// AddBlob writes an encrypted blob to the pack and records it in the header.
// The caller is responsible for encrypting the blob data before calling this.
func (pw *Writer) AddBlob(id types.BlobID, blobType types.BlobType, uncompressedLen uint32, ciphertext []byte) error {
	n, err := pw.w.Write(ciphertext)
	if err != nil {
		return fmt.Errorf("pack.Writer.AddBlob: %w", err)
	}
	if n != len(ciphertext) {
		return fmt.Errorf("pack.Writer.AddBlob: short write: %d/%d", n, len(ciphertext))
	}

	pw.entries = append(pw.entries, HeaderEntry{
		ID:                 id,
		Type:               blobType,
		Offset:             pw.offset,
		Length:             uint32(len(ciphertext)),
		UncompressedLength: uncompressedLen,
	})
	pw.offset += uint32(len(ciphertext))
	return nil
}

// Finalize writes the encrypted header and header length to complete the pack file.
// The encrypt function is used to encrypt the header data.
func (pw *Writer) Finalize(encrypt EncryptFunc) error {
	headerData, err := MarshalHeader(pw.entries)
	if err != nil {
		return fmt.Errorf("pack.Writer.Finalize: %w", err)
	}

	encryptedHeader, err := encrypt(headerData)
	if err != nil {
		return fmt.Errorf("pack.Writer.Finalize: encrypt header: %w", err)
	}

	// Write encrypted header
	if _, err := pw.w.Write(encryptedHeader); err != nil {
		return fmt.Errorf("pack.Writer.Finalize: write header: %w", err)
	}

	// Write header length as uint32 LE
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(encryptedHeader)))
	if _, err := pw.w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("pack.Writer.Finalize: write header length: %w", err)
	}

	return nil
}

// Entries returns the current header entries (for testing/inspection).
func (pw *Writer) Entries() Header {
	return pw.entries
}

// Count returns the number of blobs written so far.
func (pw *Writer) Count() int {
	return len(pw.entries)
}

// Size returns the current byte offset (total blob data written).
func (pw *Writer) Size() uint32 {
	return pw.offset
}
