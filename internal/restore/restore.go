// Package restore implements the restore engine for extracting files from
// a doomsday repository snapshot to a target directory.
//
// Restore guarantees (from spec):
//   - Per-file atomic writes: each file written to a temp file, renamed to
//     final name only after full content is written and verified.
//   - Content verification: after decrypting each blob, the repository
//     verifies HMAC-SHA256 matches the blob ID.
//   - Write ordering: directories first, then files, then permissions,
//     timestamps last.
package restore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
)

// ProgressEvent describes the current state of the restore operation.
type ProgressEvent struct {
	// Path is the relative path of the item being processed.
	Path string

	// BytesWritten is the number of content bytes written so far for this file.
	BytesWritten int64

	// TotalBytes is the expected total size of this file (from metadata).
	TotalBytes int64

	// FilesCompleted is the total number of files finished so far.
	FilesCompleted int64

	// FilesTotal is the total number of files to be restored (0 if unknown).
	FilesTotal int64

	// IsDryRun indicates this event comes from a dry-run (no writes occurred).
	IsDryRun bool
}

// Options configures a restore operation.
type Options struct {
	// IncludePaths limits restore to entries matching these path prefixes.
	// An empty slice means restore everything. Paths are slash-separated
	// relative to the snapshot root.
	IncludePaths []string

	// Overwrite controls whether existing files at the target are replaced.
	// When false, existing files cause the restore to fail for that entry.
	Overwrite bool

	// DryRun when true walks the tree and invokes OnProgress but writes
	// nothing to the filesystem.
	DryRun bool

	// OnProgress is called for each file restored (or would-be restored in
	// dry-run mode). May be nil.
	OnProgress func(ProgressEvent)
}

// Run restores the snapshot identified by snapshotID from the repository
// into targetDir. It follows the ordering guarantees described in the spec:
//
//  1. Load and parse the snapshot metadata.
//  2. Walk the tree recursively, creating directories first.
//  3. Write file contents via atomic temp-file-then-rename.
//  4. Create symlinks.
//  5. Apply permissions and ownership.
//  6. Set timestamps last (writing into a directory changes mtime).
func Run(ctx context.Context, r *repo.Repository, snapshotID string, targetDir string, opts Options) error {
	// Resolve target to an absolute path.
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("restore: resolve target: %w", err)
	}

	// Load the snapshot.
	snap, err := r.LoadSnapshot(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("restore: load snapshot %s: %w", snapshotID, err)
	}

	if snap.Tree.IsZero() {
		return fmt.Errorf("restore: snapshot %s has no root tree", snapshotID)
	}

	// Load the root tree blob.
	rootTree, err := loadTree(ctx, r, snap.Tree)
	if err != nil {
		return fmt.Errorf("restore: load root tree: %w", err)
	}

	// Phase 1: collect the full plan (directory structure + file list).
	var plan restorePlan
	if err := buildPlan(ctx, r, rootTree, "", &plan, opts.IncludePaths); err != nil {
		return fmt.Errorf("restore: build plan: %w", err)
	}

	// Phase 2: create all directories (depth-first order is natural from
	// the recursive walk, but we sort by depth so parents exist first).
	if !opts.DryRun {
		for _, d := range plan.dirs {
			dirPath := filepath.Join(absTarget, d.relPath)
			if err := validateRestorePath(absTarget, dirPath); err != nil {
				return err
			}
			// Use permissive mode during restore; real mode applied in Phase 5.
			if err := os.MkdirAll(dirPath, 0700); err != nil {
				return fmt.Errorf("restore: mkdir %s: %w", d.relPath, err)
			}
		}
	}

	// Phase 3: write files atomically.
	var filesCompleted int64
	for _, f := range plan.files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("restore: %w", err)
		}

		if opts.OnProgress != nil {
			opts.OnProgress(ProgressEvent{
				Path:           f.relPath,
				TotalBytes:     f.size,
				FilesCompleted: filesCompleted,
				FilesTotal:     int64(len(plan.files)),
				IsDryRun:       opts.DryRun,
			})
		}

		if !opts.DryRun {
			finalPath := filepath.Join(absTarget, f.relPath)
			if err := validateRestorePath(absTarget, finalPath); err != nil {
				return err
			}

			if !opts.Overwrite {
				if _, err := os.Lstat(finalPath); err == nil {
					return fmt.Errorf("restore: file exists (overwrite=false): %s", f.relPath)
				}
			}

			if err := restoreFile(ctx, r, f.content, finalPath); err != nil {
				return fmt.Errorf("restore: write %s: %w", f.relPath, err)
			}
		}
		filesCompleted++
	}

	// Phase 3b: create symlinks.
	for _, s := range plan.symlinks {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("restore: %w", err)
		}

		if !opts.DryRun {
			linkPath := filepath.Join(absTarget, s.relPath)
			if err := validateRestorePath(absTarget, linkPath); err != nil {
				return err
			}

			// SECURITY: Validate that symlink targets don't escape the restore
			// directory. Absolute targets are rejected; relative targets are
			// resolved against the link's parent directory.
			target := s.symlinkTarget
			if filepath.IsAbs(target) {
				return fmt.Errorf("restore: symlink %s: absolute symlink target %q not allowed", s.relPath, target)
			}
			resolvedTarget := filepath.Join(filepath.Dir(linkPath), target)
			if err := validateRestorePath(absTarget, resolvedTarget); err != nil {
				return fmt.Errorf("restore: symlink %s: target escapes restore directory: %w", s.relPath, err)
			}

			if opts.Overwrite {
				_ = os.Remove(linkPath)
			}

			if err := os.Symlink(target, linkPath); err != nil {
				return fmt.Errorf("restore: symlink %s: %w", s.relPath, err)
			}
		}
	}

	// Phase 4: apply permissions to files and directories.
	// Files first, then directories (so directory write permission is
	// available during the file-write phase above).
	if !opts.DryRun {
		for _, f := range plan.files {
			finalPath := filepath.Join(absTarget, f.relPath)
			if err := os.Chmod(finalPath, f.mode.Perm()); err != nil {
				return fmt.Errorf("restore: chmod %s: %w", f.relPath, err)
			}
		}
		// Directories in reverse order so children are set before parents
		// (a restrictive parent mode should not block child chmod).
		// Best-effort: intermediate/wrapper directories (like /Users)
		// may not be owned by the current user — skip errors.
		for i := len(plan.dirs) - 1; i >= 0; i-- {
			d := plan.dirs[i]
			dirPath := filepath.Join(absTarget, d.relPath)
			_ = os.Chmod(dirPath, d.mode.Perm())
		}
	}

	// Phase 5: set timestamps last. Directories in reverse order so that
	// parent mtime is not disturbed by setting children timestamps.
	if !opts.DryRun {
		for _, f := range plan.files {
			finalPath := filepath.Join(absTarget, f.relPath)
			if err := os.Chtimes(finalPath, f.accessTime, f.modTime); err != nil {
				return fmt.Errorf("restore: chtimes %s: %w", f.relPath, err)
			}
		}
		for _, s := range plan.symlinks {
			_ = s // intentionally no-op for now
		}
		// Best-effort for directories — wrapper dirs may not be owned by us.
		for i := len(plan.dirs) - 1; i >= 0; i-- {
			d := plan.dirs[i]
			dirPath := filepath.Join(absTarget, d.relPath)
			_ = os.Chtimes(dirPath, d.accessTime, d.modTime)
		}
	}

	// Final progress notification.
	if opts.OnProgress != nil {
		opts.OnProgress(ProgressEvent{
			FilesCompleted: filesCompleted,
			FilesTotal:     int64(len(plan.files)),
			IsDryRun:       opts.DryRun,
		})
	}

	return nil
}

// restorePlan holds the ordered list of items to restore.
type restorePlan struct {
	dirs     []planEntry
	files    []planEntry
	symlinks []planEntry
}

// planEntry holds only the fields needed for restore, not the full tree.Node.
// This reduces memory for large restores (millions of files).
type planEntry struct {
	relPath       string
	mode          os.FileMode
	modTime       time.Time
	accessTime    time.Time
	size          int64
	content       []types.BlobID // file content blob IDs
	symlinkTarget string
}

// buildPlan recursively walks the snapshot tree and populates the plan.
// Directories are added in top-down order. Files and symlinks are appended
// in walk order. includePaths filters entries if non-empty.
func buildPlan(ctx context.Context, r *repo.Repository, t *tree.Tree, prefix string, plan *restorePlan, includePaths []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, node := range t.Nodes {
		// SECURITY: Reject node names that could escape the restore target
		// directory via path traversal (e.g., "../", "/", embedded separators).
		if err := validateNodeName(node.Name); err != nil {
			return fmt.Errorf("unsafe node name %q: %w", node.Name, err)
		}

		relPath := filepath.Join(prefix, node.Name)

		if len(includePaths) > 0 && !pathIncluded(relPath, includePaths) {
			continue
		}

		switch node.Type {
		case tree.NodeTypeDir:
			plan.dirs = append(plan.dirs, planEntry{
				relPath: relPath, mode: node.Mode,
				modTime: node.ModTime, accessTime: node.AccessTime,
			})

			if !node.Subtree.IsZero() {
				subtree, err := loadTree(ctx, r, node.Subtree)
				if err != nil {
					return fmt.Errorf("load subtree for %s: %w", relPath, err)
				}
				if err := buildPlan(ctx, r, subtree, relPath, plan, includePaths); err != nil {
					return err
				}
			}

		case tree.NodeTypeFile:
			plan.files = append(plan.files, planEntry{
				relPath: relPath, mode: node.Mode, size: node.Size,
				modTime: node.ModTime, accessTime: node.AccessTime,
				content: node.Content,
			})

		case tree.NodeTypeSymlink:
			plan.symlinks = append(plan.symlinks, planEntry{
				relPath: relPath, mode: node.Mode,
				symlinkTarget: node.SymlinkTarget,
			})

		default:
			// Dev, FIFO, socket: metadata-only types. Skip during restore
			// (they require special privileges and are host-specific).
		}
	}

	return nil
}

// pathIncluded returns true if relPath matches any of the include prefixes.
// A path is included if it equals a prefix, is a child of a prefix, or is
// an ancestor of a prefix (so that intermediate directories are created).
func pathIncluded(relPath string, includePaths []string) bool {
	// Normalize to forward slashes for matching.
	normalized := filepath.ToSlash(relPath)
	for _, inc := range includePaths {
		inc = filepath.ToSlash(inc)
		inc = strings.TrimSuffix(inc, "/")
		if normalized == inc {
			return true
		}
		// relPath is a child of an include prefix.
		if strings.HasPrefix(normalized, inc+"/") {
			return true
		}
		// relPath is an ancestor of an include prefix (directory on the way).
		if strings.HasPrefix(inc, normalized+"/") {
			return true
		}
	}
	return false
}

// loadTree loads and decodes a tree blob from the repository.
func loadTree(ctx context.Context, r *repo.Repository, id types.BlobID) (*tree.Tree, error) {
	data, err := r.LoadBlob(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load tree blob %s: %w", id.Short(), err)
	}

	t, err := tree.Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal tree %s: %w", id.Short(), err)
	}

	return t, nil
}

// restoreFile writes a single file atomically. Content blobs are loaded,
// decompressed, and written to a temp file. On success the temp file is
// renamed to the final path.
func restoreFile(ctx context.Context, r *repo.Repository, content []types.BlobID, finalPath string) error {
	dir := filepath.Dir(finalPath)

	// Generate a random suffix for the temp file.
	var rndBuf [8]byte
	if _, err := rand.Read(rndBuf[:]); err != nil {
		return fmt.Errorf("generate temp suffix: %w", err)
	}
	tmpName := filepath.Join(dir, ".doomsday.tmp."+hex.EncodeToString(rndBuf[:]))

	// Create the temp file with owner-only write permission.
	f, err := os.OpenFile(tmpName, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	// Ensure cleanup on any error path.
	success := false
	defer func() {
		if !success {
			f.Close()
			os.Remove(tmpName)
		}
	}()

	// Write each content blob sequentially.
	var written int64
	for _, blobID := range content {
		if err := ctx.Err(); err != nil {
			return err
		}

		// LoadBlob handles decryption, decompression, and content ID
		// verification internally (content verification guarantee from the spec).
		data, err := r.LoadBlob(ctx, blobID)
		if err != nil {
			return fmt.Errorf("load blob %s: %w", blobID.Short(), err)
		}

		n, err := f.Write(data)
		if err != nil {
			return fmt.Errorf("write blob %s: %w", blobID.Short(), err)
		}
		written += int64(n)
	}

	// Sync to disk before rename for durability.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Atomic rename: this is the commit point. If we crash before this,
	// only the temp file exists (cleaned up by the user or next restore).
	if err := os.Rename(tmpName, finalPath); err != nil {
		return fmt.Errorf("rename to final: %w", err)
	}

	success = true
	return nil
}

// validateNodeName rejects tree node names that could cause path traversal.
func validateNodeName(name string) error {
	if name == "" {
		return fmt.Errorf("empty name")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("path traversal component")
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("contains path separator or null byte")
	}
	return nil
}

// validateRestorePath ensures the final path stays within the target directory.
func validateRestorePath(absTarget, finalPath string) error {
	cleaned := filepath.Clean(finalPath)
	// Root target: everything is valid (can't escape /).
	if absTarget == "/" {
		return nil
	}
	// Ensure the cleaned path starts with the target directory.
	if !strings.HasPrefix(cleaned+string(filepath.Separator), absTarget+string(filepath.Separator)) {
		return fmt.Errorf("restore: path traversal detected: %s escapes target %s", cleaned, absTarget)
	}
	return nil
}

// FilesRestored is a convenience type returned by DryRunCount when the
// caller just needs to know the scope of the restore without executing it.
type FilesRestored struct {
	Dirs     int
	Files    int
	Symlinks int
}

// DryRunCount performs a dry run and returns the number of items that would
// be restored. This is a lightweight alternative to Run with DryRun=true
// when no per-file progress callback is needed.
func DryRunCount(ctx context.Context, r *repo.Repository, snapshotID string, opts Options) (*FilesRestored, error) {
	snap, err := r.LoadSnapshot(ctx, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("restore.DryRunCount: load snapshot: %w", err)
	}
	if snap.Tree.IsZero() {
		return nil, fmt.Errorf("restore.DryRunCount: snapshot has no root tree")
	}

	rootTree, err := loadTree(ctx, r, snap.Tree)
	if err != nil {
		return nil, fmt.Errorf("restore.DryRunCount: load root tree: %w", err)
	}

	var plan restorePlan
	if err := buildPlan(ctx, r, rootTree, "", &plan, opts.IncludePaths); err != nil {
		return nil, fmt.Errorf("restore.DryRunCount: build plan: %w", err)
	}

	return &FilesRestored{
		Dirs:     len(plan.dirs),
		Files:    len(plan.files),
		Symlinks: len(plan.symlinks),
	}, nil
}
