package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff <snapshot1> <snapshot2>",
	Short: "Show changes between two snapshots",
	Long: `Compare two snapshots and show added, modified, and removed files.

Supports "latest" as a snapshot identifier.

Output format:
  + added_file
  - removed_file
  M modified_file

Examples:
  doomsday client diff abc123 def456
  doomsday client diff abc123 latest
  doomsday client diff abc123 def456 --json`,
	Args: exactArgs(2),
	RunE: runDiff,
}

// diffChange represents a single file change between snapshots.
type diffChange struct {
	Action string `json:"action"` // "added", "removed", "modified"
	Path   string `json:"path"`
	Size   int64  `json:"size,omitempty"`
}

func runDiff(cmd *cobra.Command, args []string) error {
	snapID1 := args[0]
	snapID2 := args[1]

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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Shared, "diff")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	snapID1, err = resolveSnapshotID(ctx, r, "", snapID1)
	if err != nil {
		return fmt.Errorf("resolve snapshot (snap1): %w", err)
	}
	snapID2, err = resolveSnapshotID(ctx, r, "", snapID2)
	if err != nil {
		return fmt.Errorf("resolve snapshot (snap2): %w", err)
	}

	snap1, err := r.LoadSnapshot(ctx, snapID1)
	if err != nil {
		return fmt.Errorf("load snapshot %s: %w", snapID1, err)
	}

	snap2, err := r.LoadSnapshot(ctx, snapID2)
	if err != nil {
		return fmt.Errorf("load snapshot %s: %w", snapID2, err)
	}

	files1, err := buildFileMap(ctx, r, snap1.Tree, "/")
	if err != nil {
		return fmt.Errorf("walk snapshot %s: %w", snapID1, err)
	}

	files2, err := buildFileMap(ctx, r, snap2.Tree, "/")
	if err != nil {
		return fmt.Errorf("walk snapshot %s: %w", snapID2, err)
	}

	var changes []diffChange

	for path, node2 := range files2 {
		node1, exists := files1[path]
		if !exists {
			changes = append(changes, diffChange{Action: "added", Path: path, Size: node2.Size})
		} else if isNodeModified(node1, node2) {
			changes = append(changes, diffChange{Action: "modified", Path: path, Size: node2.Size})
		}
	}

	for path, node1 := range files1 {
		if _, exists := files2[path]; !exists {
			changes = append(changes, diffChange{Action: "removed", Path: path, Size: node1.Size})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})

	return renderDiffResults(snapID1, snapID2, changes)
}

// buildFileMap recursively walks a tree and builds a map of path -> node.
func buildFileMap(ctx context.Context, r *repo.Repository, treeID types.BlobID, prefix string) (map[string]tree.Node, error) {
	result := make(map[string]tree.Node)

	t, err := loadTreeFromRepo(ctx, r, treeID)
	if err != nil {
		return nil, err
	}

	for _, node := range t.Nodes {
		nodePath := prefix + node.Name
		result[nodePath] = node

		if node.Type == tree.NodeTypeDir && !node.Subtree.IsZero() {
			subMap, err := buildFileMap(ctx, r, node.Subtree, nodePath+"/")
			if err != nil {
				return nil, err
			}
			for k, v := range subMap {
				result[k] = v
			}
		}
	}

	return result, nil
}

// isNodeModified determines if a node has changed between two snapshots.
func isNodeModified(a, b tree.Node) bool {
	if a.Type != b.Type {
		return true
	}
	if a.Size != b.Size {
		return true
	}
	if !a.ModTime.Equal(b.ModTime) {
		return true
	}
	if a.Mode != b.Mode {
		return true
	}
	if a.Type == tree.NodeTypeFile {
		if len(a.Content) != len(b.Content) {
			return true
		}
		for i := range a.Content {
			if a.Content[i] != b.Content[i] {
				return true
			}
		}
	}
	if a.Type == tree.NodeTypeDir {
		if a.Subtree != b.Subtree {
			return true
		}
	}
	if a.Type == tree.NodeTypeSymlink {
		if a.SymlinkTarget != b.SymlinkTarget {
			return true
		}
	}
	return false
}

func renderDiffResults(snapID1, snapID2 string, changes []diffChange) error {
	if flagJSON {
		type diffSummaryJSON struct {
			Added    int `json:"added"`
			Modified int `json:"modified"`
			Removed  int `json:"removed"`
		}
		type diffResultJSON struct {
			Snapshot1 string          `json:"snapshot1"`
			Snapshot2 string          `json:"snapshot2"`
			Changes   []diffChange    `json:"changes"`
			Summary   diffSummaryJSON `json:"summary"`
		}
		out := diffResultJSON{
			Snapshot1: snapID1,
			Snapshot2: snapID2,
			Changes:   changes,
			Summary: diffSummaryJSON{
				Added:    countChanges(changes, "added"),
				Modified: countChanges(changes, "modified"),
				Removed:  countChanges(changes, "removed"),
			},
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(changes) == 0 {
		logger.Info("No differences found between snapshots",
			"snap1", snapID1[:12],
			"snap2", snapID2[:12],
		)
		return nil
	}

	for _, c := range changes {
		var prefix string
		switch c.Action {
		case "added":
			prefix = "+"
		case "removed":
			prefix = "-"
		case "modified":
			prefix = "M"
		}
		fmt.Printf("%s %s\n", prefix, c.Path)
	}

	added := countChanges(changes, "added")
	modified := countChanges(changes, "modified")
	removed := countChanges(changes, "removed")
	fmt.Printf("\n%d added, %d modified, %d removed\n", added, modified, removed)

	return nil
}

func countChanges(changes []diffChange, action string) int {
	count := 0
	for _, c := range changes {
		if c.Action == action {
			count++
		}
	}
	return count
}
