// Package compress provides zstd compression with auto-skip for incompressible data.
package compress

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// Compression type prefix bytes.
const (
	prefixUncompressed byte = 0x00
	prefixZstd         byte = 0x01
)

// Ratio threshold: if compressed >= 97% of original, skip compression.
const skipRatio = 0.97

var (
	encoder *zstd.Encoder
	decoder *zstd.Decoder
)

func init() {
	var err error
	encoder, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(fmt.Sprintf("compress: init encoder: %v", err))
	}
	decoder, err = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(64<<20)) // 64 MiB max decompressed output
	if err != nil {
		panic(fmt.Sprintf("compress: init decoder: %v", err))
	}
}

// Compress compresses data using zstd. If the result is not meaningfully smaller
// than the original (>= 97%), returns the original data uncompressed.
// The output is prefixed with a type byte (0x00=uncompressed, 0x01=zstd).
func Compress(data []byte, level int) []byte {
	if len(data) == 0 {
		return []byte{prefixUncompressed}
	}

	// Use a level-specific encoder if non-default
	enc := encoder
	if level != int(zstd.SpeedDefault) {
		var err error
		enc, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevel(level)))
		if err != nil {
			// Fallback to uncompressed on encoder error
			out := make([]byte, 1+len(data))
			out[0] = prefixUncompressed
			copy(out[1:], data)
			return out
		}
	}

	compressed := enc.EncodeAll(data, nil)

	// Check if compression is worthwhile
	if float64(len(compressed)) >= float64(len(data))*skipRatio {
		out := make([]byte, 1+len(data))
		out[0] = prefixUncompressed
		copy(out[1:], data)
		return out
	}

	out := make([]byte, 1+len(compressed))
	out[0] = prefixZstd
	copy(out[1:], compressed)
	return out
}

// Decompress decompresses data produced by Compress.
func Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("compress.Decompress: empty input")
	}

	switch data[0] {
	case prefixUncompressed:
		result := make([]byte, len(data)-1)
		copy(result, data[1:])
		return result, nil
	case prefixZstd:
		decoded, err := decoder.DecodeAll(data[1:], nil)
		if err != nil {
			return nil, fmt.Errorf("compress.Decompress: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("compress.Decompress: unknown prefix byte 0x%02x", data[0])
	}
}
