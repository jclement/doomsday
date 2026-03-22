package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jclement/doomsday/internal/chunker"
	"github.com/jclement/doomsday/internal/compress"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
	"golang.org/x/sync/errgroup"
)

// SourceOptions holds per-source configuration for the backup.
type SourceOptions struct {
	// Excludes is additional exclude patterns for this source (merged with global).
	Excludes []string
	// OneFilesystem prevents crossing filesystem boundaries for this source.
	OneFilesystem bool
}

// Options configures a backup run.
type Options struct {
	// Paths is the list of filesystem paths to back up.
	Paths []string
	// Excludes is a list of global glob patterns to exclude.
	Excludes []string
	// PerSource is optional per-path configuration. Keys are cleaned absolute paths.
	// Per-source excludes are merged with global Excludes for that specific root.
	PerSource map[string]SourceOptions
	// ConfigName is the name of the backup configuration (stored in snapshot).
	ConfigName string
	// Hostname is the machine hostname (stored in snapshot).
	Hostname string
	// CompressionLevel is the zstd compression level (0 = default).
	CompressionLevel int
	// OnProgress is called periodically with backup statistics.
	OnProgress ProgressFunc
	// Tags are optional tags stored in the snapshot.
	Tags []string
	// FileSaverWorkers controls the number of concurrent file processing goroutines.
	// Default: 2.
	FileSaverWorkers int
	// DryRun walks and chunks files but does not write any data to the repository.
	// The returned snapshot will have accurate stats but is not persisted.
	DryRun bool
}

// Run executes a backup and returns the resulting snapshot.
//
// Pipeline:
//  1. Walk filesystem, producing file entries
//  2. For each file: read -> chunk (FastCDC) -> for each chunk: dedup check -> compress -> encrypt -> pack
//  3. Accumulate blobs in packer. When packer is full (~16 MiB), flush pack via repo.SavePack
//  4. Build tree blobs for directory structure
//  5. Create snapshot metadata, save it
func Run(ctx context.Context, r *repo.Repository, opts Options) (*snapshot.Snapshot, error) {
	startTime := time.Now()

	if len(opts.Paths) == 0 {
		return nil, fmt.Errorf("backup.Run: no paths specified")
	}
	if opts.CompressionLevel == 0 {
		opts.CompressionLevel = 3
	}
	if opts.Hostname == "" {
		opts.Hostname, _ = os.Hostname()
	}
	workers := opts.FileSaverWorkers
	if workers <= 0 {
		workers = 2
	}

	progress := newProgressTracker(opts.OnProgress)

	// Create packers for data and tree blobs
	dataPacker := newPacker(r, types.BlobTypeData, progress)
	treePacker := newPacker(r, types.BlobTypeTree, progress)

	// Phase 1: Walk filesystem and process files concurrently.
	// We collect directory structure as we go for tree building.

	type fileResult struct {
		relPath string
		node    tree.Node
		err     error
	}

	// We process one root at a time for simplicity in tree building.
	// Each root produces one tree hierarchy.
	rootTrees := make([]types.BlobID, len(opts.Paths))

	for ri, rootPath := range opts.Paths {
		rootPath = filepath.Clean(rootPath)

		// Build per-root walker config: global excludes + per-source overrides.
		walkCfg := buildWalkerConfig(opts, rootPath)

		// Walk this root
		entries := walkFilesystem(ctx, []string{rootPath}, walkCfg, progress)

		// Collect entries grouped by directory.
		// dirChildren and dirOrder are only written by the walker goroutine
		// and read after g.Wait(), so no mutex is needed.
		dirChildren := make(map[string][]tree.Node) // relDir -> child nodes
		dirOrder := []string{}                       // track discovery order of directories

		// Use errgroup for concurrent file processing
		g, gctx := errgroup.WithContext(ctx)

		// Channel for files that need processing
		type fileWork struct {
			entry   walkEntry
			relPath string
		}
		fileCh := make(chan fileWork, 256)
		resultCh := make(chan fileResult, 256)

		// File saver workers: read, chunk, dedup, compress, encrypt, pack
		for w := 0; w < workers; w++ {
			g.Go(func() error {
				for work := range fileCh {
					node, err := processFile(gctx, r, dataPacker, work.entry, opts.CompressionLevel, progress)
					select {
					case resultCh <- fileResult{relPath: work.relPath, node: node, err: err}:
					case <-gctx.Done():
						return gctx.Err()
					}
				}
				return nil
			})
		}

		// Result collector goroutine: the only writer to fileNodes.
		fileNodes := make(map[string]tree.Node) // relPath -> node
		done := make(chan struct{})
		go func() {
			defer close(done)
			for res := range resultCh {
				if res.err != nil {
					slog.Warn("backup: file processing error", "path", res.relPath, "error", res.err)
					progress.errors.Add(1)
					continue
				}
				fileNodes[res.relPath] = res.node
			}
		}()

		// Walk and dispatch entries.
		// This goroutine handles directories, symlinks, and special files synchronously.
		// Regular files are dispatched to workers via fileCh.
		// Symlinks and special files are sent through resultCh (like regular files)
		// so that only the collector goroutine writes to fileNodes.
		g.Go(func() error {
			defer close(fileCh)
			for entry := range entries {
				if gctx.Err() != nil {
					return gctx.Err()
				}

				if entry.Err != nil {
					slog.Warn("backup: walk error", "path", entry.Path, "error", entry.Err)
					progress.errors.Add(1)
					continue
				}

				info := entry.Info
				if info == nil {
					continue
				}

				relPath := entry.RelPath

				switch {
				case info.IsDir():
					// Track directory (will be populated later with children).
					// Only the walker goroutine writes to dirChildren/dirOrder here,
					// and they are read only after g.Wait(), so this is safe.
					if _, exists := dirChildren[relPath]; !exists {
						dirChildren[relPath] = nil
						dirOrder = append(dirOrder, relPath)
					}

				case info.Mode()&os.ModeSymlink != 0:
					// Symlink: read target, create node, send through resultCh
					target, err := os.Readlink(entry.Path)
					if err != nil {
						progress.errors.Add(1)
						continue
					}
					meta := extractMetadata(info)
					node := tree.Node{
						Name:          filepath.Base(relPath),
						Type:          tree.NodeTypeSymlink,
						Mode:          meta.Mode,
						UID:           meta.UID,
						GID:           meta.GID,
						ModTime:       meta.ModTime,
						AccessTime:    meta.AccessTime,
						ChangeTime:    meta.ChangeTime,
						Inode:         meta.Inode,
						Links:         meta.Links,
						SymlinkTarget: target,
					}
					select {
					case resultCh <- fileResult{relPath: relPath, node: node}:
					case <-gctx.Done():
						return gctx.Err()
					}

				case info.Mode().IsRegular():
					// Regular file: dispatch to worker
					select {
					case fileCh <- fileWork{entry: entry, relPath: relPath}:
					case <-gctx.Done():
						return gctx.Err()
					}

				default:
					// Special files (devices, FIFOs, sockets): metadata only
					meta := extractMetadata(info)
					nodeType := tree.NodeTypeFile
					if info.Mode()&os.ModeDevice != 0 {
						nodeType = tree.NodeTypeDev
					} else if info.Mode()&os.ModeNamedPipe != 0 {
						nodeType = tree.NodeTypeFIFO
					} else if info.Mode()&os.ModeSocket != 0 {
						nodeType = tree.NodeTypeSocket
					}
					node := tree.Node{
						Name:       filepath.Base(relPath),
						Type:       nodeType,
						Mode:       meta.Mode,
						UID:        meta.UID,
						GID:        meta.GID,
						ModTime:    meta.ModTime,
						AccessTime: meta.AccessTime,
						ChangeTime: meta.ChangeTime,
						Inode:      meta.Inode,
						Links:      meta.Links,
					}
					select {
					case resultCh <- fileResult{relPath: relPath, node: node}:
					case <-gctx.Done():
						return gctx.Err()
					}
				}
			}
			return nil
		})

		// Wait for all file workers and the walker to complete
		if err := g.Wait(); err != nil {
			return nil, fmt.Errorf("backup.Run: %w", err)
		}
		close(resultCh)
		<-done

		// Phase 2: Build tree structure bottom-up.
		// At this point, all goroutines are done. Safe to read fileNodes and dirChildren.

		// Assign file nodes to their parent directories.
		for relPath, node := range fileNodes {
			parentDir := filepath.Dir(relPath)
			if parentDir == "." {
				parentDir = ""
			}
			dirChildren[parentDir] = append(dirChildren[parentDir], node)
		}

		// Sort directories by depth (deepest first) for bottom-up tree building.
		// Depth is the number of path components: "" = 0, "a" = 1, "a/b" = 2, etc.
		sort.Slice(dirOrder, func(i, j int) bool {
			di := dirDepth(dirOrder[i])
			dj := dirDepth(dirOrder[j])
			return di > dj
		})

		// Build tree blobs bottom-up
		dirTreeIDs := make(map[string]types.BlobID)

		for _, dirRel := range dirOrder {
			children := dirChildren[dirRel]

			// Sort children by name for deterministic trees
			sort.Slice(children, func(i, j int) bool {
				return children[i].Name < children[j].Name
			})

			// Add subdirectory entries (directories whose parent is this dir).
			// These were already processed because we iterate deepest-first.
			for _, otherDir := range dirOrder {
				if otherDir == dirRel {
					continue
				}
				otherParent := filepath.Dir(otherDir)
				if otherParent == "." {
					otherParent = ""
				}
				if otherParent == dirRel {
					subtreeID, ok := dirTreeIDs[otherDir]
					if !ok {
						continue
					}
					dirName := filepath.Base(otherDir)
					// Check if we already have this directory node
					found := false
					for idx := range children {
						if children[idx].Name == dirName && children[idx].Type == tree.NodeTypeDir {
							children[idx].Subtree = subtreeID
							found = true
							break
						}
					}
					if !found {
						children = append(children, tree.Node{
							Name:    dirName,
							Type:    tree.NodeTypeDir,
							Mode:    os.ModeDir | 0755,
							Subtree: subtreeID,
						})
					}
				}
			}

			// Re-sort after adding subdirs
			sort.Slice(children, func(i, j int) bool {
				return children[i].Name < children[j].Name
			})

			// Serialize tree
			t := &tree.Tree{Nodes: children}
			treeData, err := tree.Marshal(t)
			if err != nil {
				return nil, fmt.Errorf("backup.Run: marshal tree %q: %w", dirRel, err)
			}

			// Compute content ID for the tree blob
			treeID := r.ContentID(treeData)

			// Dedup: check if this tree already exists
			if r.Index().CheckAndAdd(treeID) {
				// New tree blob: compress, encrypt, pack
				compressed := compress.Compress(treeData, opts.CompressionLevel)
				encrypted, err := r.EncryptTreeBlob(treeID, compressed)
				if err != nil {
					return nil, fmt.Errorf("backup.Run: encrypt tree: %w", err)
				}
				if err := treePacker.AddBlob(ctx, treeID, encrypted, uint32(len(treeData))); err != nil {
					return nil, fmt.Errorf("backup.Run: pack tree: %w", err)
				}
			}

			dirTreeIDs[dirRel] = treeID
		}

		// The root tree is the deepest dir that was the backup root.
		// With absolute paths, the initial relPath is e.g. "home/user".
		// We need to wrap it in intermediate directory nodes up to "".
		absRel := strings.TrimPrefix(filepath.ToSlash(rootPath), "/")
		rootTreeID, ok := dirTreeIDs[absRel]
		if !ok {
			if tid, ok2 := dirTreeIDs["."]; ok2 {
				rootTreeID = tid
			} else if tid, ok2 := dirTreeIDs[""]; ok2 {
				rootTreeID = tid
			} else {
				// Edge case: empty directory
				emptyTree := &tree.Tree{Nodes: nil}
				treeData, err := tree.Marshal(emptyTree)
				if err != nil {
					return nil, fmt.Errorf("backup.Run: marshal empty tree: %w", err)
				}
				rootTreeID = r.ContentID(treeData)
				if r.Index().CheckAndAdd(rootTreeID) {
					compressed := compress.Compress(treeData, opts.CompressionLevel)
					encrypted, err := r.EncryptTreeBlob(rootTreeID, compressed)
					if err != nil {
						return nil, fmt.Errorf("backup.Run: encrypt root tree: %w", err)
					}
					if err := treePacker.AddBlob(ctx, rootTreeID, encrypted, uint32(len(treeData))); err != nil {
						return nil, fmt.Errorf("backup.Run: pack root tree: %w", err)
					}
				}
			}
		}

		// Wrap the root tree in intermediate directory nodes for each
		// path component. E.g. "home/user" becomes:
		//   "" -> {home -> {user -> rootTreeID}}
		parts := strings.Split(absRel, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			wrapperNode := tree.Node{
				Name:    parts[i],
				Type:    tree.NodeTypeDir,
				Mode:    0755 | os.ModeDir,
				ModTime: startTime,
				Subtree: rootTreeID,
			}
			wrapperTree := &tree.Tree{Nodes: []tree.Node{wrapperNode}}
			treeData, err := tree.Marshal(wrapperTree)
			if err != nil {
				return nil, fmt.Errorf("backup.Run: marshal wrapper tree: %w", err)
			}
			wrapperID := r.ContentID(treeData)
			if r.Index().CheckAndAdd(wrapperID) {
				compressed := compress.Compress(treeData, opts.CompressionLevel)
				encrypted, err := r.EncryptTreeBlob(wrapperID, compressed)
				if err != nil {
					return nil, fmt.Errorf("backup.Run: encrypt wrapper tree: %w", err)
				}
				if err := treePacker.AddBlob(ctx, wrapperID, encrypted, uint32(len(treeData))); err != nil {
					return nil, fmt.Errorf("backup.Run: pack wrapper tree: %w", err)
				}
			}
			rootTreeID = wrapperID
		}

		rootTrees[ri] = rootTreeID
	}

	// Phase 3: Flush remaining data in packers.
	// Strict ordering per spec:
	// (a) Flush all data packs
	if err := dataPacker.Flush(ctx); err != nil {
		return nil, fmt.Errorf("backup.Run: flush data packs: %w", err)
	}
	// (b) Flush all tree packs
	if err := treePacker.Flush(ctx); err != nil {
		return nil, fmt.Errorf("backup.Run: flush tree packs: %w", err)
	}

	// If multiple roots, create a meta-tree that references each root tree.
	// For a single root, use its tree directly.
	var snapshotTree types.BlobID
	if len(rootTrees) == 1 {
		snapshotTree = rootTrees[0]
	} else {
		// Each rootTree is wrapped in the full absolute path hierarchy.
		// Merge them by combining top-level entries. For overlapping
		// directory names (e.g. two sources under /home), we merge
		// their subtrees recursively.
		merged, err := mergeTrees(ctx, r, treePacker, rootTrees, opts.CompressionLevel)
		if err != nil {
			return nil, fmt.Errorf("backup.Run: merge trees: %w", err)
		}
		snapshotTree = merged
	}

	// (c) Save index to backend
	if err := r.SaveIndex(ctx); err != nil {
		return nil, fmt.Errorf("backup.Run: save index: %w", err)
	}

	// (d) Create and save snapshot
	snapID, err := generateSnapshotID()
	if err != nil {
		return nil, fmt.Errorf("backup.Run: generate snapshot ID: %w", err)
	}

	stats := progress.snapshot()
	duration := time.Since(startTime)

	snap := &snapshot.Snapshot{
		ID:               snapID,
		Time:             startTime,
		Hostname:         opts.Hostname,
		Paths:            opts.Paths,
		Tags:             opts.Tags,
		Tree:             snapshotTree,
		BackupConfigName: opts.ConfigName,
		Summary: &snapshot.Summary{
			FilesChanged:   stats.FilesChanged,
			FilesUnchanged: stats.FilesUnchanged,
			DataAdded:      stats.BytesNew,
			TotalSize:      stats.BytesRead,
			TotalFiles:     stats.FilesProcessed,
			DirsNew:        stats.DirsTotal,
			Duration:       duration,
		},
	}

	if err := r.SaveSnapshot(ctx, snap); err != nil {
		return nil, fmt.Errorf("backup.Run: save snapshot: %w", err)
	}

	// Final progress report
	progress.report()

	return snap, nil
}

// processFile reads a file, chunks it with FastCDC, deduplicates, compresses,
// encrypts, and packs each chunk. Returns a tree.Node describing the file.
func processFile(ctx context.Context, r *repo.Repository, packer *packer, entry walkEntry, compressionLevel int, progress *progressTracker) (tree.Node, error) {
	progress.currentFile.Store(entry.RelPath)
	meta := extractMetadata(entry.Info)
	node := tree.Node{
		Name:       filepath.Base(entry.RelPath),
		Type:       tree.NodeTypeFile,
		Mode:       meta.Mode,
		Size:       meta.Size,
		UID:        meta.UID,
		GID:        meta.GID,
		ModTime:    meta.ModTime,
		AccessTime: meta.AccessTime,
		ChangeTime: meta.ChangeTime,
		Inode:      meta.Inode,
		Links:      meta.Links,
	}

	// Open file for reading
	f, err := os.Open(entry.Path)
	if err != nil {
		return node, fmt.Errorf("open %s: %w", entry.Path, err)
	}
	defer f.Close()

	// Chunk with FastCDC
	contentIDKey := r.Keys().SubKeys.ContentID
	ch := chunker.New(f, func(data []byte) types.BlobID {
		return crypto.ContentID(contentIDKey, data)
	})

	var blobIDs []types.BlobID
	var hadNewChunks bool

	for {
		if ctx.Err() != nil {
			return node, ctx.Err()
		}

		chunk, err := ch.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return node, fmt.Errorf("chunk %s: %w", entry.Path, err)
		}

		progress.chunksTotal.Add(1)
		progress.bytesRead.Add(int64(chunk.Length))

		blobIDs = append(blobIDs, chunk.ID)

		// Dedup: check if we already have this chunk
		if !r.Index().CheckAndAdd(chunk.ID) {
			// Already exists -- skip
			progress.chunksDup.Add(1)
			progress.report()
			continue
		}

		// New chunk: compress -> encrypt -> pack
		hadNewChunks = true
		progress.chunksNew.Add(1)
		progress.bytesNew.Add(int64(chunk.Length))

		compressed := compress.Compress(chunk.Data, compressionLevel)
		encrypted, err := r.EncryptDataBlob(chunk.ID, compressed)
		if err != nil {
			return node, fmt.Errorf("encrypt chunk %s: %w", entry.Path, err)
		}

		if err := packer.AddBlob(ctx, chunk.ID, encrypted, uint32(chunk.Length)); err != nil {
			return node, fmt.Errorf("pack chunk %s: %w", entry.Path, err)
		}

		progress.report()
	}

	node.Content = blobIDs
	progress.filesProcessed.Add(1)
	if hadNewChunks || len(blobIDs) == 0 {
		progress.filesChanged.Add(1)
	} else {
		progress.filesUnchanged.Add(1)
	}
	progress.report()

	return node, nil
}

// dirDepth returns the depth of a directory path.
// "" = 0, "a" = 1, "a/b" = 2, etc.
func dirDepth(p string) int {
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// generateSnapshotID creates a random snapshot identifier.
func generateSnapshotID() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("generate snapshot ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// mergeTrees merges multiple root trees into one by combining overlapping
// directory entries. Each rootTree is a tree blob that starts from the
// filesystem root (e.g. both have a "home" entry if backing up paths
// under /home).
func mergeTrees(ctx context.Context, r *repo.Repository, packer *packer, treeIDs []types.BlobID, compressionLevel int) (types.BlobID, error) {
	// Load all trees and collect their nodes.
	nodesByName := make(map[string][]tree.Node) // name -> nodes from different trees
	var names []string

	for _, tid := range treeIDs {
		data, err := r.LoadBlob(ctx, tid)
		if err != nil {
			return types.BlobID{}, fmt.Errorf("load tree for merge: %w", err)
		}
		t, err := tree.Unmarshal(data)
		if err != nil {
			return types.BlobID{}, fmt.Errorf("unmarshal tree for merge: %w", err)
		}
		for _, node := range t.Nodes {
			if _, exists := nodesByName[node.Name]; !exists {
				names = append(names, node.Name)
			}
			nodesByName[node.Name] = append(nodesByName[node.Name], node)
		}
	}

	sort.Strings(names)

	var merged []tree.Node
	for _, name := range names {
		nodes := nodesByName[name]
		if len(nodes) == 1 {
			merged = append(merged, nodes[0])
			continue
		}
		// Multiple nodes with the same name — must all be dirs to merge.
		allDirs := true
		var subtreeIDs []types.BlobID
		for _, n := range nodes {
			if n.Type != tree.NodeTypeDir {
				allDirs = false
				break
			}
			if !n.Subtree.IsZero() {
				subtreeIDs = append(subtreeIDs, n.Subtree)
			}
		}
		if !allDirs || len(subtreeIDs) == 0 {
			// Can't merge — just take the first one.
			merged = append(merged, nodes[0])
			continue
		}
		// Recursively merge the subtrees.
		mergedSubtree, err := mergeTrees(ctx, r, packer, subtreeIDs, compressionLevel)
		if err != nil {
			return types.BlobID{}, err
		}
		node := nodes[0]
		node.Subtree = mergedSubtree
		merged = append(merged, node)
	}

	// Save the merged tree.
	mergedTree := &tree.Tree{Nodes: merged}
	treeData, err := tree.Marshal(mergedTree)
	if err != nil {
		return types.BlobID{}, fmt.Errorf("marshal merged tree: %w", err)
	}
	mergedID := r.ContentID(treeData)
	if r.Index().CheckAndAdd(mergedID) {
		compressed := compress.Compress(treeData, compressionLevel)
		encrypted, err := r.EncryptTreeBlob(mergedID, compressed)
		if err != nil {
			return types.BlobID{}, fmt.Errorf("encrypt merged tree: %w", err)
		}
		if err := packer.AddBlob(ctx, mergedID, encrypted, uint32(len(treeData))); err != nil {
			return types.BlobID{}, fmt.Errorf("pack merged tree: %w", err)
		}
		if err := packer.Flush(ctx); err != nil {
			return types.BlobID{}, fmt.Errorf("flush merged tree: %w", err)
		}
	}
	return mergedID, nil
}
