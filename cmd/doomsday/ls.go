package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
	"github.com/spf13/cobra"
)

var (
	lsFlagLong bool
)

var lsCmd = &cobra.Command{
	Use:   "ls <snapshot-id>:<path>",
	Short: "List files in a snapshot",
	Long: `List files and directories within a backup snapshot.

The argument specifies the snapshot and optional path:
  <snapshot-id>          - list the root of the snapshot
  <snapshot-id>:<path>   - list a specific directory in the snapshot
  latest                 - list the root of the most recent snapshot
  latest:<path>          - list a specific directory from the most recent snapshot

Examples:
  doomsday client ls latest
  doomsday client ls latest:/Documents
  doomsday client ls abc123def456:/Photos --long
  doomsday client ls latest --json`,
	Args: exactArgs(1),
	RunE: runLs,
}

func init() {
	lsCmd.Flags().BoolVarP(&lsFlagLong, "long", "l", false, "detailed output (mode, size, mtime, name)")
}

func runLs(cmd *cobra.Command, args []string) error {
	snapArg := args[0]

	snapshotID, targetPath := parseSnapPath(snapArg)
	targetPath = path.Clean("/" + targetPath)
	if targetPath == "/" {
		targetPath = ""
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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Shared, "ls")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	snapshotID, err = resolveSnapshotID(ctx, r, "", snapshotID)
	if err != nil {
		return fmt.Errorf("resolve snapshot: %w", err)
	}

	snap, err := r.LoadSnapshot(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("load snapshot: %w", err)
	}

	rootTree, err := loadTree(ctx, r, snap.Tree)
	if err != nil {
		return fmt.Errorf("load root tree: %w", err)
	}

	currentTree := rootTree
	if targetPath != "" {
		parts := strings.Split(strings.TrimPrefix(targetPath, "/"), "/")
		for _, part := range parts {
			if part == "" {
				continue
			}
			node := currentTree.Find(part)
			if node == nil {
				return fmt.Errorf("path not found: %s", targetPath)
			}
			if node.Type != tree.NodeTypeDir {
				return renderLsNodes(snapshotID, targetPath, []tree.Node{*node})
			}
			if node.Subtree.IsZero() {
				return fmt.Errorf("directory %q has no subtree", part)
			}
			currentTree, err = loadTree(ctx, r, node.Subtree)
			if err != nil {
				return fmt.Errorf("load subtree for %q: %w", part, err)
			}
		}
	}

	return renderLsNodes(snapshotID, targetPath, currentTree.Nodes)
}

func renderLsNodes(snapshotID string, dirPath string, nodes []tree.Node) error {
	if flagJSON {
		type nodeJSON struct {
			Name    string    `json:"name"`
			Type    string    `json:"type"`
			Size    int64     `json:"size"`
			Mode    string    `json:"mode"`
			ModTime time.Time `json:"mtime"`
		}
		var items []nodeJSON
		for _, n := range nodes {
			items = append(items, nodeJSON{
				Name:    n.Name,
				Type:    string(n.Type),
				Size:    n.Size,
				Mode:    n.Mode.String(),
				ModTime: n.ModTime,
			})
		}
		type lsResultJSON struct {
			Snapshot string     `json:"snapshot"`
			Path     string     `json:"path"`
			Entries  []nodeJSON `json:"entries"`
			Count    int        `json:"count"`
		}
		out := lsResultJSON{
			Snapshot: snapshotID,
			Path:     dirPath,
			Entries:  items,
			Count:    len(items),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(nodes) == 0 {
		logger.Info("Empty directory")
		return nil
	}

	if lsFlagLong {
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, n := range nodes {
			size := formatBytes(n.Size)
			if n.Type == tree.NodeTypeDir {
				size = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				n.Mode.String(),
				size,
				n.ModTime.Local().Format("2006-01-02 15:04"),
				lsNodeName(n),
			)
		}
		w.Flush()
	} else {
		for _, n := range nodes {
			fmt.Println(lsNodeName(n))
		}
	}

	return nil
}

// lsNodeName returns the display name for a node, with a trailing / for directories.
func lsNodeName(n tree.Node) string {
	if n.Type == tree.NodeTypeDir {
		return n.Name + "/"
	}
	if n.Type == tree.NodeTypeSymlink && n.SymlinkTarget != "" {
		return n.Name + " -> " + n.SymlinkTarget
	}
	return n.Name
}

// loadTree loads and unmarshals a tree blob from the repository.
func loadTree(ctx context.Context, r interface {
	LoadBlob(ctx context.Context, id types.BlobID) ([]byte, error)
}, treeID types.BlobID) (*tree.Tree, error) {
	data, err := r.LoadBlob(ctx, treeID)
	if err != nil {
		return nil, fmt.Errorf("load tree blob: %w", err)
	}
	return tree.Unmarshal(data)
}
