package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
	"github.com/spf13/cobra"
)

var (
	findFlagSnapshot string
	findFlagAll      bool
)

var findCmd = &cobra.Command{
	Use:   "find <pattern>",
	Short: "Search for files matching a pattern",
	Long: `Search for files matching a glob pattern across snapshots.

By default, searches only the latest snapshot. Use --snapshot to search
a specific snapshot, or --all to search all snapshots.

The pattern uses standard glob syntax (*, ?, [abc]).

Examples:
  doomsday client find "*.pdf"
  doomsday client find "Documents/*.xlsx" --all
  doomsday client find "**/*.go" --snapshot abc123def456
  doomsday client find "*.jpg" --json`,
	Args: exactArgs(1),
	RunE: runFind,
}

func init() {
	findCmd.Flags().StringVar(&findFlagSnapshot, "snapshot", "", "search only this snapshot (default: latest)")
	findCmd.Flags().BoolVar(&findFlagAll, "all", false, "search all snapshots")
}

// findResult holds a single match from the find operation.
type findResult struct {
	SnapshotID string
	SnapTime   time.Time
	Path       string
	Size       int64
	ModTime    time.Time
	Type       tree.NodeType
}

func runFind(cmd *cobra.Command, args []string) error {
	pattern := args[0]

	// Validate glob pattern early.
	if _, err := filepath.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid glob pattern %q: %w\nValid glob syntax: *, ?, [abc], [a-z]", pattern, err)
	}

	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	masterKey, err := openMasterKey(cfg)
	if err != nil {
		return fmt.Errorf("open master key: %w", err)
	}
	defer masterKey.Zero()

	ctx := context.Background()

	dest, err := firstDest(cfg)
	if err != nil {
		return err
	}

	backend, err := openBackend(ctx, dest)
	if err != nil {
		return fmt.Errorf("open backend: %w", err)
	}
	defer backend.Close()

	r, err := openRepo(ctx, backend, masterKey, cfg.Settings.CacheDir)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Shared, "find")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	// Determine which snapshots to search.
	var snapsToSearch []*snapshot.Snapshot

	if findFlagAll {
		ids, err := r.ListSnapshots(ctx)
		if err != nil {
			return fmt.Errorf("list snapshots: %w", err)
		}
		for _, id := range ids {
			snap, err := r.LoadSnapshot(ctx, id)
			if err != nil {
				logger.Warn("Failed to load snapshot", "id", id, "error", err)
				continue
			}
			snapsToSearch = append(snapsToSearch, snap)
		}
		sort.Slice(snapsToSearch, func(i, j int) bool {
			return snapsToSearch[i].Time.Before(snapsToSearch[j].Time)
		})
	} else {
		snapID := findFlagSnapshot
		if snapID == "" {
			snapID = "latest"
		}
		snapID, err = resolveSnapshotID(ctx, r, "", snapID)
		if err != nil {
			return fmt.Errorf("resolve snapshot: %w", err)
		}
		snap, err := r.LoadSnapshot(ctx, snapID)
		if err != nil {
			return fmt.Errorf("load snapshot: %w", err)
		}
		snapsToSearch = append(snapsToSearch, snap)
	}

	// Search each snapshot.
	var results []findResult
	for _, snap := range snapsToSearch {
		matches, err := findInSnapshot(ctx, r, snap, pattern)
		if err != nil {
			logger.Warn("Error searching snapshot", "id", snap.ID[:12], "error", err)
			continue
		}
		results = append(results, matches...)
	}

	return renderFindResults(results)
}

// findInSnapshot searches a single snapshot for files matching the pattern.
func findInSnapshot(ctx context.Context, r *repo.Repository, snap *snapshot.Snapshot, pattern string) ([]findResult, error) {
	rootTree, err := loadTree(ctx, r, snap.Tree)
	if err != nil {
		return nil, fmt.Errorf("load root tree: %w", err)
	}

	var results []findResult
	err = walkTree(ctx, r, rootTree, "/", func(nodePath string, node tree.Node) {
		name := node.Name
		matched, matchErr := filepath.Match(pattern, name)
		if matchErr != nil {
			return
		}
		if !matched {
			// Also try matching against the full path.
			matched, _ = filepath.Match(pattern, nodePath)
		}
		if matched {
			results = append(results, findResult{
				SnapshotID: snap.ID,
				SnapTime:   snap.Time,
				Path:       nodePath,
				Size:       node.Size,
				ModTime:    node.ModTime,
				Type:       node.Type,
			})
		}
	})
	if err != nil {
		return nil, err
	}

	return results, nil
}

// walkTree recursively walks a tree, calling fn for every node.
func walkTree(ctx context.Context, r *repo.Repository, t *tree.Tree, prefix string, fn func(path string, node tree.Node)) error {
	for _, node := range t.Nodes {
		nodePath := prefix + node.Name
		fn(nodePath, node)

		if node.Type == tree.NodeTypeDir && !node.Subtree.IsZero() {
			subtree, err := loadTreeFromRepo(ctx, r, node.Subtree)
			if err != nil {
				return fmt.Errorf("load subtree %q: %w", nodePath, err)
			}
			if err := walkTree(ctx, r, subtree, nodePath+"/", fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadTreeFromRepo loads and unmarshals a tree blob using the repository directly.
func loadTreeFromRepo(ctx context.Context, r *repo.Repository, treeID types.BlobID) (*tree.Tree, error) {
	data, err := r.LoadBlob(ctx, treeID)
	if err != nil {
		return nil, fmt.Errorf("load tree blob: %w", err)
	}
	return tree.Unmarshal(data)
}

func renderFindResults(results []findResult) error {
	if flagJSON {
		type resultJSON struct {
			SnapshotID string    `json:"snapshot_id"`
			SnapTime   time.Time `json:"snapshot_time"`
			Path       string    `json:"path"`
			Size       int64     `json:"size"`
			ModTime    time.Time `json:"mtime"`
			Type       string    `json:"type"`
		}
		var items []resultJSON
		for _, r := range results {
			items = append(items, resultJSON{
				SnapshotID: r.SnapshotID,
				SnapTime:   r.SnapTime,
				Path:       r.Path,
				Size:       r.Size,
				ModTime:    r.ModTime,
				Type:       string(r.Type),
			})
		}
		type findResultsJSON struct {
			Matches []resultJSON `json:"matches"`
			Count   int          `json:"count"`
		}
		out := findResultsJSON{
			Matches: items,
			Count:   len(items),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(results) == 0 {
		logger.Info("No matching files found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SNAPSHOT\tPATH\tSIZE\tMODIFIED")
	fmt.Fprintln(w, "--------\t----\t----\t--------")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.SnapshotID[:12],
			r.Path,
			formatBytes(r.Size),
			r.ModTime.Local().Format("2006-01-02 15:04"),
		)
	}
	w.Flush()

	fmt.Printf("\n%d match(es) found\n", len(results))
	return nil
}
