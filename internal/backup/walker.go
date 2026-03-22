package backup

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// walkEntry represents a filesystem entry discovered by the walker.
type walkEntry struct {
	// Path is the absolute filesystem path.
	Path string
	// RelPath is the path relative to the backup root.
	RelPath string
	// Info is the file info from stat.
	Info os.FileInfo
	// Err is set if stat or read failed for this entry (non-fatal).
	Err error
	// Dev is the device number (for one_filesystem support).
	Dev uint64
}

// walkerConfig controls the filesystem walk.
type walkerConfig struct {
	// Excludes is a list of glob patterns to exclude.
	Excludes []string
	// OneFilesystem prevents crossing filesystem boundaries.
	OneFilesystem bool
}

// walkFilesystem walks the given root paths and sends entries on the returned channel.
// The channel is closed when the walk is complete or the context is cancelled.
// Directories are sent before their contents (pre-order).
// Errors on individual files are sent as entries with Err set (non-fatal).
func walkFilesystem(ctx context.Context, roots []string, cfg walkerConfig, progress *progressTracker) <-chan walkEntry {
	ch := make(chan walkEntry, 256)
	go func() {
		defer close(ch)
		for _, root := range roots {
			root = filepath.Clean(root)
			if err := walkRoot(ctx, root, cfg, progress, ch); err != nil {
				// Context cancelled — stop
				return
			}
		}
	}()
	return ch
}

// walkRoot walks a single root path.
func walkRoot(ctx context.Context, root string, cfg walkerConfig, progress *progressTracker, ch chan<- walkEntry) error {
	info, err := os.Lstat(root)
	if err != nil {
		select {
		case ch <- walkEntry{Path: root, RelPath: ".", Err: err}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	var rootDev uint64
	if cfg.OneFilesystem {
		rootDev = getDeviceID(info)
	}

	// Use the absolute path (without leading separator) as the initial relPath.
	// This stores full filesystem paths in the tree, making "restore in place"
	// trivial: just restore to "/".
	initialRel := strings.TrimPrefix(filepath.ToSlash(root), "/")
	return walkDir(ctx, root, root, initialRel, info, rootDev, cfg, progress, ch)
}

// walkDir recursively walks a directory.
func walkDir(ctx context.Context, root, absPath, relPath string, info os.FileInfo, rootDev uint64, cfg walkerConfig, progress *progressTracker, ch chan<- walkEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	dev := getDeviceID(info)

	// Check one_filesystem: skip if different device
	if cfg.OneFilesystem && rootDev != 0 && dev != rootDev {
		return nil
	}

	// Send the directory entry itself
	entry := walkEntry{
		Path:    absPath,
		RelPath: relPath,
		Info:    info,
		Dev:     dev,
	}
	select {
	case ch <- entry:
	case <-ctx.Done():
		return ctx.Err()
	}
	progress.dirsTotal.Add(1)
	progress.report()

	// Read directory contents
	entries, err := os.ReadDir(absPath)
	if err != nil {
		select {
		case ch <- walkEntry{Path: absPath, RelPath: relPath, Err: err}:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	// Sort entries for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, de := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}

		name := de.Name()
		childRel := joinRelPath(relPath, name)
		childAbs := filepath.Join(absPath, name)

		// Check exclusions
		if shouldExclude(childRel, name, de.IsDir(), cfg.Excludes) {
			continue
		}

		childInfo, err := de.Info()
		if err != nil {
			select {
			case ch <- walkEntry{Path: childAbs, RelPath: childRel, Err: err}:
			case <-ctx.Done():
				return ctx.Err()
			}
			progress.errors.Add(1)
			continue
		}

		// Handle symlinks: stat the link itself (Lstat behavior from ReadDir), don't follow
		if childInfo.Mode()&os.ModeSymlink != 0 {
			// Send symlink entry
			select {
			case ch <- walkEntry{Path: childAbs, RelPath: childRel, Info: childInfo, Dev: dev}:
			case <-ctx.Done():
				return ctx.Err()
			}
			progress.filesTotal.Add(1)
			progress.report()
			continue
		}

		if childInfo.IsDir() {
			if err := walkDir(ctx, root, childAbs, childRel, childInfo, rootDev, cfg, progress, ch); err != nil {
				return err
			}
		} else {
			select {
			case ch <- walkEntry{Path: childAbs, RelPath: childRel, Info: childInfo, Dev: dev}:
			case <-ctx.Done():
				return ctx.Err()
			}
			progress.filesTotal.Add(1)
			progress.report()
		}
	}

	return nil
}

// joinRelPath joins a relative directory path with a child name.
func joinRelPath(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

// shouldExclude checks if a path matches any exclusion pattern.
// Patterns follow glob semantics. A pattern ending with "/" matches only directories.
func shouldExclude(relPath, name string, isDir bool, excludes []string) bool {
	for _, pattern := range excludes {
		// Directory-only patterns (ending with /)
		dirOnly := strings.HasSuffix(pattern, "/")
		pat := strings.TrimSuffix(pattern, "/")

		if dirOnly && !isDir {
			continue
		}

		// Match against the basename
		if matched, _ := filepath.Match(pat, name); matched {
			return true
		}

		// Match against the relative path (for patterns with path separators)
		if strings.Contains(pat, "/") || strings.Contains(pat, string(filepath.Separator)) {
			if matched, _ := filepath.Match(pat, relPath); matched {
				return true
			}
		}

		// Wildcard prefix match: pattern like "*/node_modules/" matches at any depth
		if strings.HasPrefix(pat, "*/") {
			subPat := pat[2:]
			if matched, _ := filepath.Match(subPat, name); matched {
				if !dirOnly || isDir {
					return true
				}
			}
		}
	}
	return false
}

// fileMetadata extracts metadata from os.FileInfo suitable for tree nodes.
type fileMetadata struct {
	Mode       os.FileMode
	Size       int64
	ModTime    time.Time
	UID        uint32
	GID        uint32
	Inode      uint64
	Links      uint64
	DevMajor   uint32
	DevMinor   uint32
	AccessTime time.Time
	ChangeTime time.Time
}
