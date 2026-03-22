// Package cache provides a local file-based cache for blob data.
package cache

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jclement/doomsday/internal/types"
)

// VerifyFunc computes the content ID of data for integrity verification.
// It should return the HMAC-SHA256 content address of the data.
type VerifyFunc func(data []byte) types.BlobID

// Cache provides a simple file-based cache for blob data.
// Stores blobs by their hex-encoded ID in a flat directory.
type Cache struct {
	dir    string
	verify VerifyFunc // optional integrity verifier
}

// New creates a cache at the given directory.
// If verify is non-nil, cached blobs are verified on read and discarded
// if their content does not match the expected blob ID.
func New(dir string, verify VerifyFunc) (*Cache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cache.New: %w", err)
	}
	return &Cache{dir: dir, verify: verify}, nil
}

// Get retrieves a cached blob, or nil if not cached.
// If a VerifyFunc was provided, the blob's content is verified against
// the expected ID. Corrupted cache entries are silently removed.
func (c *Cache) Get(id types.BlobID) ([]byte, error) {
	path := c.path(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache.Get: %w", err)
	}

	// SECURITY: Verify cached data integrity to prevent cache poisoning.
	if c.verify != nil {
		actual := c.verify(data)
		if actual != id {
			// Cache entry is corrupted or tampered with — remove it.
			os.Remove(path)
			return nil, nil
		}
	}

	return data, nil
}

// Put stores a blob in the cache.
func (c *Cache) Put(id types.BlobID, data []byte) error {
	path := c.path(id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("cache.Put: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cache.Put: %w", err)
	}
	return nil
}

// Has returns true if the blob is cached.
func (c *Cache) Has(id types.BlobID) bool {
	_, err := os.Stat(c.path(id))
	return err == nil
}

func (c *Cache) path(id types.BlobID) string {
	hex := id.String()
	return filepath.Join(c.dir, hex[:2], hex)
}
