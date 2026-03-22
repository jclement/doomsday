package compress

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"small", []byte("hello world")},
		{"repeated", bytes.Repeat([]byte("doomsday "), 10000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed := Compress(tt.data, 3)
			decompressed, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress: %v", err)
			}
			if !bytes.Equal(decompressed, tt.data) {
				t.Errorf("roundtrip failed: got %d bytes, want %d", len(decompressed), len(tt.data))
			}
		})
	}
}

func TestIncompressibleSkip(t *testing.T) {
	// Random data is incompressible
	data := make([]byte, 4096)
	rand.Read(data)

	compressed := Compress(data, 3)

	// Should be prefixed with uncompressed marker
	if compressed[0] != prefixUncompressed {
		t.Error("expected incompressible data to be stored uncompressed")
	}

	// Verify roundtrip
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Error("roundtrip failed for incompressible data")
	}
}

func TestCompressibleData(t *testing.T) {
	data := bytes.Repeat([]byte("doomsday backup for the end of the world "), 1000)
	compressed := Compress(data, 3)

	// Should actually compress
	if compressed[0] != prefixZstd {
		t.Error("expected compressible data to be compressed")
	}

	// Should be smaller
	if len(compressed) >= len(data) {
		t.Errorf("compressed size %d >= original %d", len(compressed), len(data))
	}
}

func TestDecompressInvalidPrefix(t *testing.T) {
	_, err := Decompress([]byte{0xFF, 0x01, 0x02})
	if err == nil {
		t.Error("expected error for unknown prefix")
	}
}

func TestDecompressEmpty(t *testing.T) {
	_, err := Decompress([]byte{})
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestDecompressCorrupted(t *testing.T) {
	_, err := Decompress([]byte{prefixZstd, 0x00, 0x01, 0x02, 0x03})
	if err == nil {
		t.Error("expected error for corrupted zstd data")
	}
}

func FuzzCompressRoundtrip(f *testing.F) {
	// Seed corpus: empty, small text, compressible, random
	f.Add([]byte{})
	f.Add([]byte("hello world"))
	f.Add(bytes.Repeat([]byte("doomsday "), 1000))
	randomData := make([]byte, 4096)
	rand.Read(randomData)
	f.Add(randomData)

	f.Fuzz(func(t *testing.T, data []byte) {
		compressed := Compress(data, 3)
		decompressed, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Errorf("roundtrip mismatch: original %d bytes, got %d bytes", len(data), len(decompressed))
		}
	})
}
