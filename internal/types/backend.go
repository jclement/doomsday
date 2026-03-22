package types

import (
	"context"
	"io"
)

// FileInfo describes a file in the backend.
type FileInfo struct {
	Name string
	Size int64
}

// Backend is the storage abstraction for repository data.
// Implementations exist for local filesystem, SFTP, and S3-compatible stores.
type Backend interface {
	// Location returns a human-readable description of the backend.
	Location() string

	// Save writes data from rd to the backend under the given type and name.
	// The name should not include path separators — the backend maps FileType to directories.
	Save(ctx context.Context, t FileType, name string, rd io.Reader) error

	// Load reads a range of bytes from the named file.
	// If length is 0, the entire file is returned starting from offset.
	Load(ctx context.Context, t FileType, name string, offset, length int64) (io.ReadCloser, error)

	// Stat returns info about the named file.
	Stat(ctx context.Context, t FileType, name string) (FileInfo, error)

	// Remove deletes the named file.
	Remove(ctx context.Context, t FileType, name string) error

	// List calls fn for each file of the given type.
	// If fn returns an error, iteration stops and that error is returned.
	List(ctx context.Context, t FileType, fn func(FileInfo) error) error

	// Close releases any resources held by the backend.
	Close() error
}
