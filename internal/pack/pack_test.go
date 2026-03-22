package pack

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/jclement/doomsday/internal/types"
)

// noopEncrypt/noopDecrypt for testing without real crypto
func noopEncrypt(data []byte) ([]byte, error) {
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}
func noopDecrypt(data []byte) ([]byte, error) {
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

func randomBlobID(t *testing.T) types.BlobID {
	t.Helper()
	var id types.BlobID
	rand.Read(id[:])
	return id
}

func TestWriteReadRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	// Add a few blobs
	blobs := []struct {
		id   types.BlobID
		typ  types.BlobType
		data []byte
	}{
		{randomBlobID(t), types.BlobTypeData, []byte("first chunk of data")},
		{randomBlobID(t), types.BlobTypeData, []byte("second chunk of data, longer this time")},
		{randomBlobID(t), types.BlobTypeTree, []byte(`{"entries":[]}`)},
	}

	for _, b := range blobs {
		if err := w.AddBlob(b.id, b.typ, uint32(len(b.data)), b.data); err != nil {
			t.Fatalf("AddBlob: %v", err)
		}
	}

	if err := w.Finalize(noopEncrypt); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	if w.Count() != 3 {
		t.Errorf("Count() = %d, want 3", w.Count())
	}

	// Read back
	data := buf.Bytes()
	r := bytes.NewReader(data)

	header, err := ReadHeader(r, int64(len(data)), noopDecrypt)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	if len(header) != 3 {
		t.Fatalf("header has %d entries, want 3", len(header))
	}

	for i, entry := range header {
		if entry.ID != blobs[i].id {
			t.Errorf("entry %d: ID mismatch", i)
		}
		if entry.Type != blobs[i].typ {
			t.Errorf("entry %d: type mismatch", i)
		}

		blob, err := ReadBlob(r, entry)
		if err != nil {
			t.Fatalf("ReadBlob %d: %v", i, err)
		}
		if !bytes.Equal(blob, blobs[i].data) {
			t.Errorf("entry %d: data mismatch", i)
		}
	}
}

func TestEmptyPack(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Finalize(noopEncrypt); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	data := buf.Bytes()
	r := bytes.NewReader(data)
	header, err := ReadHeader(r, int64(len(data)), noopDecrypt)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 0 {
		t.Errorf("expected empty header, got %d entries", len(header))
	}
}

func TestReadHeader_TooSmall(t *testing.T) {
	r := bytes.NewReader([]byte{0x01, 0x02})
	_, err := ReadHeader(r, 2, noopDecrypt)
	if err == nil {
		t.Error("expected error for too-small file")
	}
}

func TestReadHeader_InvalidHeaderLength(t *testing.T) {
	// Create a pack where header length claims to be larger than file
	data := []byte{0xFF, 0xFF, 0xFF, 0xFF} // header length = max uint32
	r := bytes.NewReader(data)
	_, err := ReadHeader(r, 4, noopDecrypt)
	if err == nil {
		t.Error("expected error for invalid header length")
	}
}

func TestHeaderMarshalUnmarshal(t *testing.T) {
	h := Header{
		{ID: randomBlobID(t), Type: types.BlobTypeData, Offset: 0, Length: 100, UncompressedLength: 200},
		{ID: randomBlobID(t), Type: types.BlobTypeTree, Offset: 100, Length: 50, UncompressedLength: 0},
	}

	data, err := MarshalHeader(h)
	if err != nil {
		t.Fatal(err)
	}

	h2, err := UnmarshalHeader(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(h2) != len(h) {
		t.Fatalf("header length mismatch: %d vs %d", len(h2), len(h))
	}

	for i := range h {
		if h[i].ID != h2[i].ID || h[i].Type != h2[i].Type || h[i].Offset != h2[i].Offset || h[i].Length != h2[i].Length {
			t.Errorf("entry %d mismatch", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Validation limit tests
// ---------------------------------------------------------------------------

func TestReadHeader_TooManyEntries(t *testing.T) {
	// Construct a pack whose header contains more entries than MaxEntriesPerPack.
	// We build a JSON header with MaxEntriesPerPack+1 entries, write it as
	// the "encrypted" header (using noopEncrypt which is identity), and append
	// the 4-byte LE header length trailer.

	entries := make(Header, MaxEntriesPerPack+1)
	for i := range entries {
		entries[i] = HeaderEntry{
			ID:     randomBlobID(t),
			Type:   types.BlobTypeData,
			Offset: 0,
			Length: 1,
		}
	}

	headerJSON, err := MarshalHeader(entries)
	if err != nil {
		t.Fatalf("MarshalHeader: %v", err)
	}

	encrypted, err := noopEncrypt(headerJSON)
	if err != nil {
		t.Fatalf("noopEncrypt: %v", err)
	}

	// Build the full pack: just the header + 4-byte length (no blob data).
	var buf bytes.Buffer
	buf.Write(encrypted)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(encrypted)))
	buf.Write(lenBuf[:])

	data := buf.Bytes()
	r := bytes.NewReader(data)
	_, err = ReadHeader(r, int64(len(data)), noopDecrypt)
	if err == nil {
		t.Error("expected error for too many entries, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("expected 'exceeds maximum' in error, got: %v", err)
	}
}

func TestReadHeader_BlobTooLarge(t *testing.T) {
	// Construct a pack header with a single entry whose Length > MaxBlobSize.
	entries := Header{
		{
			ID:     randomBlobID(t),
			Type:   types.BlobTypeData,
			Offset: 0,
			Length: MaxBlobSize + 1,
		},
	}

	headerJSON, err := MarshalHeader(entries)
	if err != nil {
		t.Fatalf("MarshalHeader: %v", err)
	}

	encrypted, err := noopEncrypt(headerJSON)
	if err != nil {
		t.Fatalf("noopEncrypt: %v", err)
	}

	// The fake pack needs enough "blob region" for the offset+length check to
	// not fail before we hit the MaxBlobSize check. We prepend dummy bytes
	// equal to the claimed offset+length so that the region size is sufficient.
	blobRegion := make([]byte, MaxBlobSize+1)

	var buf bytes.Buffer
	buf.Write(blobRegion)
	buf.Write(encrypted)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(encrypted)))
	buf.Write(lenBuf[:])

	data := buf.Bytes()
	r := bytes.NewReader(data)
	_, err = ReadHeader(r, int64(len(data)), noopDecrypt)
	if err == nil {
		t.Error("expected error for blob too large, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("expected 'exceeds maximum' in error, got: %v", err)
	}
}

func TestReadHeader_HeaderTooLarge(t *testing.T) {
	// Construct a minimal pack where the trailing 4-byte header length
	// claims a size larger than MaxHeaderSize.
	// We only need 4 bytes (the length trailer) because ReadHeader reads
	// the last 4 bytes first, and should reject before attempting to
	// allocate the oversized header buffer.
	//
	// We need total file size > claimed header length + 4 so that the
	// "exceeds file size" check doesn't fire before the MaxHeaderSize check.
	// Since MaxHeaderSize is 64 MiB, we instead set the value to MaxHeaderSize+1
	// and make totalSize just large enough that it passes the file-size check.
	// But actually allocating that much memory would be wasteful.
	//
	// The check order in ReadHeader is:
	//   1. totalSize < 4?
	//   2. read last 4 bytes for headerLen
	//   3. headerLen == 0?
	//   4. headerLen > MaxHeaderSize?       <-- we want to trigger this
	//   5. headerLen + 4 > totalSize?
	//
	// So we set headerLen = MaxHeaderSize+1. Step 4 fires before step 5,
	// regardless of totalSize, as long as totalSize >= 4.

	claimedLen := uint32(MaxHeaderSize + 1)
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], claimedLen)

	r := bytes.NewReader(buf[:])
	_, err := ReadHeader(r, 4, noopDecrypt)
	if err == nil {
		t.Error("expected error for header too large, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("expected 'exceeds maximum' in error, got: %v", err)
	}
}

func FuzzReadHeader(f *testing.F) {
	// Seed with a valid pack
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.AddBlob(types.BlobID{}, types.BlobTypeData, 5, []byte("hello"))
	w.Finalize(noopEncrypt)
	f.Add(buf.Bytes())

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		// Should never panic, always return clean error or valid header
		ReadHeader(r, int64(len(data)), noopDecrypt)
	})
}
