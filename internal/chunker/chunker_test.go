package chunker

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/jclement/doomsday/internal/types"
)

func testKey() [32]byte {
	var key [32]byte
	rand.Read(key[:])
	return key
}

// testContentIDFunc returns a ContentIDFunc that uses HMAC-SHA256 with the given key.
func testContentIDFunc(key [32]byte) ContentIDFunc {
	return func(data []byte) types.BlobID {
		mac := hmac.New(sha256.New, key[:])
		mac.Write(data)
		var id types.BlobID
		copy(id[:], mac.Sum(nil))
		return id
	}
}

func TestChunkerDeterministic(t *testing.T) {
	key := testKey()
	data := make([]byte, 5*1024*1024) // 5 MiB
	rand.Read(data)

	chunks1 := allChunks(t, data, key)
	chunks2 := allChunks(t, data, key)

	if len(chunks1) != len(chunks2) {
		t.Fatalf("chunk count differs: %d vs %d", len(chunks1), len(chunks2))
	}

	for i := range chunks1 {
		if chunks1[i].ID != chunks2[i].ID {
			t.Errorf("chunk %d ID differs", i)
		}
		if chunks1[i].Length != chunks2[i].Length {
			t.Errorf("chunk %d length differs: %d vs %d", i, chunks1[i].Length, chunks2[i].Length)
		}
	}
}

func TestChunkerReassembly(t *testing.T) {
	key := testKey()
	data := make([]byte, 3*1024*1024) // 3 MiB
	rand.Read(data)

	chunks := allChunks(t, data, key)

	// Reassemble
	var reassembled []byte
	for _, c := range chunks {
		reassembled = append(reassembled, c.Data...)
	}

	if !bytes.Equal(reassembled, data) {
		t.Error("reassembled data doesn't match original")
	}
}

func TestChunkerSizeConstraints(t *testing.T) {
	key := testKey()
	data := make([]byte, 20*1024*1024) // 20 MiB
	rand.Read(data)

	chunks := allChunks(t, data, key)

	for i, c := range chunks {
		isLast := i == len(chunks)-1
		if !isLast {
			if c.Length < MinSize {
				t.Errorf("chunk %d: length %d < min %d", i, c.Length, MinSize)
			}
			if c.Length > MaxSize {
				t.Errorf("chunk %d: length %d > max %d", i, c.Length, MaxSize)
			}
		}
	}
}

func TestChunkerEmpty(t *testing.T) {
	key := testKey()
	c := New(bytes.NewReader(nil), testContentIDFunc(key))
	_, err := c.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestChunkerSmall(t *testing.T) {
	key := testKey()
	data := []byte("hello world")
	chunks := allChunks(t, data, key)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for small data, got %d", len(chunks))
	}
	if !bytes.Equal(chunks[0].Data, data) {
		t.Error("chunk data doesn't match")
	}
}

func TestChunkerKeyedIDs(t *testing.T) {
	key1 := testKey()
	key2 := testKey()
	data := make([]byte, 2*1024*1024)
	rand.Read(data)

	chunks1 := allChunks(t, data, key1)
	chunks2 := allChunks(t, data, key2)

	// Same data, different keys -> same boundaries but different IDs
	if len(chunks1) != len(chunks2) {
		t.Fatalf("chunk count differs: %d vs %d", len(chunks1), len(chunks2))
	}
	for i := range chunks1 {
		if chunks1[i].Length != chunks2[i].Length {
			t.Errorf("chunk %d: lengths differ", i)
		}
		if chunks1[i].ID == chunks2[i].ID {
			t.Errorf("chunk %d: IDs should differ with different keys", i)
		}
	}
}

func allChunks(t *testing.T, data []byte, key [32]byte) []*Chunk {
	t.Helper()
	c := New(bytes.NewReader(data), testContentIDFunc(key))
	var chunks []*Chunk
	for {
		chunk, err := c.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func FuzzChunker(f *testing.F) {
	// Seed corpus: empty, tiny, and a medium-sized blob
	f.Add([]byte{})
	f.Add([]byte("hello world"))
	f.Add(make([]byte, MinSize+1))
	f.Add(make([]byte, MaxSize+1))

	key := testKey()
	idFn := testContentIDFunc(key)

	f.Fuzz(func(t *testing.T, data []byte) {
		c := New(bytes.NewReader(data), idFn)

		var reassembled []byte
		var chunks []*Chunk
		for {
			chunk, err := c.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			chunks = append(chunks, chunk)
			reassembled = append(reassembled, chunk.Data...)
		}

		// Concatenation of all chunks must equal the original input
		if !bytes.Equal(reassembled, data) {
			t.Errorf("reassembled data (%d bytes) != original (%d bytes)", len(reassembled), len(data))
		}

		// All chunk sizes must be within [MinSize, MaxSize], except possibly the last
		for i, ch := range chunks {
			isLast := i == len(chunks)-1
			if !isLast {
				if ch.Length < MinSize {
					t.Errorf("chunk %d: length %d < MinSize %d", i, ch.Length, MinSize)
				}
				if ch.Length > MaxSize {
					t.Errorf("chunk %d: length %d > MaxSize %d", i, ch.Length, MaxSize)
				}
			}
			if ch.Length != len(ch.Data) {
				t.Errorf("chunk %d: Length field %d != len(Data) %d", i, ch.Length, len(ch.Data))
			}
		}
	})
}

func BenchmarkChunker(b *testing.B) {
	key := testKey()
	data := make([]byte, 64*1024*1024) // 64 MiB
	rand.Read(data)

	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	idFn := testContentIDFunc(key)
	for i := 0; i < b.N; i++ {
		c := New(bytes.NewReader(data), idFn)
		for {
			_, err := c.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
