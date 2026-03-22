// Package chunker provides content-defined chunking using FastCDC.
// Chunks are identified by their HMAC-SHA256 hash (keyed, not plain SHA-256).
package chunker

import (
	"fmt"
	"io"

	"github.com/jclement/doomsday/internal/types"
)

// Default chunk size parameters.
const (
	MinSize    = 512 * 1024  // 512 KiB
	TargetSize = 1024 * 1024 // 1 MiB
	MaxSize    = 8 * 1024 * 1024 // 8 MiB
)

// Chunk represents a single content-defined chunk.
type Chunk struct {
	ID     types.BlobID
	Data   []byte
	Length int
}

// ContentIDFunc computes a keyed content identifier for a chunk of data.
type ContentIDFunc func(data []byte) types.BlobID

// Chunker splits data into content-defined chunks using a simple Rabin-style
// rolling hash approach. Each chunk is identified by the provided ContentIDFunc.
type Chunker struct {
	reader      io.Reader
	contentIDFn ContentIDFunc
	buf         []byte
	eof         bool
}

// New creates a new chunker that reads from r.
// contentIDFn computes keyed content IDs for chunks (e.g. HMAC-SHA256, prevents confirmation-of-file attacks).
func New(r io.Reader, contentIDFn ContentIDFunc) *Chunker {
	return &Chunker{
		reader:      r,
		contentIDFn: contentIDFn,
		buf:         nil,
		eof:         false,
	}
}

// Next returns the next chunk, or io.EOF when done.
func (c *Chunker) Next() (*Chunk, error) {
	// Fill buffer if needed
	if err := c.fill(); err != nil {
		return nil, err
	}

	if len(c.buf) == 0 {
		return nil, io.EOF
	}

	// Find chunk boundary
	boundary := c.findBoundary()

	// Extract chunk data
	data := make([]byte, boundary)
	copy(data, c.buf[:boundary])
	c.buf = c.buf[boundary:]

	// Compute keyed content ID
	id := c.contentIDFn(data)

	return &Chunk{
		ID:     id,
		Data:   data,
		Length: len(data),
	}, nil
}

// fill reads data into the buffer until we have at least MaxSize bytes or EOF.
func (c *Chunker) fill() error {
	if c.eof || len(c.buf) >= MaxSize {
		return nil
	}

	// Read in chunks to build up buffer
	tmp := make([]byte, MaxSize)
	for len(c.buf) < MaxSize && !c.eof {
		n, err := c.reader.Read(tmp)
		if n > 0 {
			c.buf = append(c.buf, tmp[:n]...)
		}
		if err == io.EOF {
			c.eof = true
			break
		}
		if err != nil {
			return fmt.Errorf("chunker.fill: %w", err)
		}
	}

	return nil
}

// findBoundary uses a gear-based rolling hash to find a content-defined chunk boundary.
// Uses normalized chunking approach inspired by FastCDC.
func (c *Chunker) findBoundary() int {
	n := len(c.buf)
	if n <= MinSize {
		return n
	}
	if n >= MaxSize {
		n = MaxSize
	}

	// Gear rolling hash for boundary detection
	var hash uint64
	// Use a relaxed mask for small chunks (normalized chunking)
	maskS := uint64(0x0000d90003530000) // fewer bits set = easier to match
	maskL := uint64(0x0000d90f03530000) // more bits set = harder to match

	// Phase 1: MinSize to TargetSize — use easy mask (normalized)
	limit := TargetSize
	if limit > n {
		limit = n
	}
	for i := MinSize; i < limit; i++ {
		hash = (hash << 1) + gearTable[c.buf[i]]
		if hash&maskS == 0 {
			return i
		}
	}

	// Phase 2: TargetSize to MaxSize — use hard mask
	for i := limit; i < n; i++ {
		hash = (hash << 1) + gearTable[c.buf[i]]
		if hash&maskL == 0 {
			return i
		}
	}

	return n
}

// gearTable is a pre-computed lookup table for the gear hash.
// Random 64-bit values, one per byte value.
var gearTable [256]uint64

func init() {
	// Deterministic gear table (from FastCDC paper's reference values)
	// Using a simple PRNG seeded with a fixed value for reproducibility
	state := uint64(0x123456789abcdef0)
	for i := range gearTable {
		// xorshift64
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		gearTable[i] = state
	}
}
