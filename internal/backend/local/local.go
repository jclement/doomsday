// Package local implements a Backend backed by the local filesystem.
package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/jclement/doomsday/internal/types"
)

// Compile-time interface assertion.
var _ types.Backend = (*Backend)(nil)

// Backend stores repository data on the local filesystem.
type Backend struct {
	basePath string
}

// New creates a local backend rooted at basePath.
// Creates the directory structure if it doesn't exist.
func New(basePath string) (*Backend, error) {
	b := &Backend{basePath: basePath}
	for _, ft := range []types.FileType{
		types.FileTypePack, types.FileTypeIndex, types.FileTypeSnapshot,
		types.FileTypeKey, types.FileTypeLock,
	} {
		dir := b.dir(ft)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("local.New: create %s: %w", dir, err)
		}
	}
	return b, nil
}

// Open opens an existing local backend without creating directories.
func Open(basePath string) (*Backend, error) {
	info, err := os.Stat(basePath)
	if err != nil {
		return nil, fmt.Errorf("local.Open: %w", types.ErrRepoNotFound)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local.Open: %s is not a directory", basePath)
	}
	return &Backend{basePath: basePath}, nil
}

func (b *Backend) Location() string {
	return b.basePath
}

func (b *Backend) Save(_ context.Context, t types.FileType, name string, rd io.Reader) error {
	if err := types.ValidateName(name); err != nil {
		return fmt.Errorf("local.Save: %w", err)
	}
	path := b.path(t, name)

	// Ensure parent directory exists (for pack files with hex prefix subdirs)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("local.Save: mkdir: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("local.Save: create temp: %w", err)
	}

	if _, err := io.Copy(f, rd); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("local.Save: write: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("local.Save: sync: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("local.Save: close: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("local.Save: rename: %w", err)
	}

	return nil
}

func (b *Backend) Load(_ context.Context, t types.FileType, name string, offset, length int64) (io.ReadCloser, error) {
	if err := types.ValidateName(name); err != nil {
		return nil, fmt.Errorf("local.Load: %w", err)
	}
	path := b.path(t, name)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("local.Load: %w", types.ErrNotFound)
		}
		return nil, fmt.Errorf("local.Load: %w", err)
	}

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, fmt.Errorf("local.Load: seek: %w", err)
		}
	}

	if length > 0 {
		return &limitedReadCloser{Reader: io.LimitReader(f, length), Closer: f}, nil
	}

	return f, nil
}

func (b *Backend) Stat(_ context.Context, t types.FileType, name string) (types.FileInfo, error) {
	if err := types.ValidateName(name); err != nil {
		return types.FileInfo{}, fmt.Errorf("local.Stat: %w", err)
	}
	path := b.path(t, name)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return types.FileInfo{}, fmt.Errorf("local.Stat: %w", types.ErrNotFound)
		}
		return types.FileInfo{}, fmt.Errorf("local.Stat: %w", err)
	}
	return types.FileInfo{Name: name, Size: info.Size()}, nil
}

func (b *Backend) Remove(_ context.Context, t types.FileType, name string) error {
	if err := types.ValidateName(name); err != nil {
		return fmt.Errorf("local.Remove: %w", err)
	}
	path := b.path(t, name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // idempotent
		}
		return fmt.Errorf("local.Remove: %w", err)
	}
	return nil
}

func (b *Backend) List(_ context.Context, t types.FileType, fn func(types.FileInfo) error) error {
	if t == types.FileTypeConfig {
		// Config is a single file, not a directory. Check if it exists.
		path := b.path(t, "")
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("local.List: %w", err)
		}
		return fn(types.FileInfo{Name: "config", Size: info.Size()})
	}

	dir := b.dir(t)

	if t == types.FileTypePack {
		// Pack files use hex prefix subdirectories
		return b.listPackFiles(dir, fn)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("local.List: %w", err)
	}

	// Sort for deterministic ordering
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if err := fn(types.FileInfo{Name: entry.Name(), Size: info.Size()}); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) listPackFiles(dir string, fn func(types.FileInfo) error) error {
	// Walk hex prefix subdirectories
	prefixes, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("local.List: %w", err)
	}

	for _, prefix := range prefixes {
		if !prefix.IsDir() {
			continue
		}
		subdir := filepath.Join(dir, prefix.Name())
		entries, err := os.ReadDir(subdir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			// Return plain filename (without prefix dir) for consistency
			// with Save/Load which accept plain names.
			if err := fn(types.FileInfo{Name: entry.Name(), Size: info.Size()}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Backend) Close() error {
	return nil
}

// dir returns the directory path for a file type.
func (b *Backend) dir(t types.FileType) string {
	return filepath.Join(b.basePath, t.String())
}

// path returns the full file path for a file type and name.
func (b *Backend) path(t types.FileType, name string) string {
	if t == types.FileTypePack && len(name) >= 2 {
		// Pack files get a two-char hex prefix subdirectory
		return filepath.Join(b.basePath, t.String(), name[:2], name)
	}
	if t == types.FileTypeConfig {
		// Config is a single file at the repo root
		return filepath.Join(b.basePath, "config")
	}
	return filepath.Join(b.basePath, t.String(), name)
}

// limitedReadCloser wraps an io.Reader and io.Closer.
type limitedReadCloser struct {
	Reader io.Reader
	Closer io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.Reader.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.Closer.Close() }
