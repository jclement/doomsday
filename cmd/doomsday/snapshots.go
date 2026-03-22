package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/spf13/cobra"
)

var snapshotsCmd = &cobra.Command{
	Use:   "snapshots",
	Short: "List snapshots",
	Long: `List all snapshots in the repository.

Displays the snapshot ID, timestamp, hostname, and summary statistics
(files, size, duration) for each snapshot.

Examples:
  doomsday client snapshots
  doomsday client snapshots --json`,
	RunE: runSnapshots,
}

func runSnapshots(cmd *cobra.Command, args []string) error {
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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Shared, "snapshots")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	ids, err := r.ListSnapshots(ctx)
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}

	var snaps []*snapshot.Snapshot
	for _, id := range ids {
		snap, err := r.LoadSnapshot(ctx, id)
		if err != nil {
			logger.Warn("Failed to load snapshot", "id", id, "error", err)
			continue
		}
		snaps = append(snaps, snap)
	}

	// Sort by time ascending (oldest first).
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Time.Before(snaps[j].Time)
	})

	if flagJSON {
		type snapJSON struct {
			ID       string    `json:"id"`
			Time     time.Time `json:"time"`
			Hostname string    `json:"hostname"`
			Paths    []string  `json:"paths"`
			Files    int64     `json:"files,omitempty"`
			Size     int64     `json:"size,omitempty"`
			Duration string    `json:"duration,omitempty"`
		}
		var items []snapJSON
		for _, s := range snaps {
			item := snapJSON{
				ID:       s.ID,
				Time:     s.Time,
				Hostname: s.Hostname,
				Paths:    s.Paths,
			}
			if s.Summary != nil {
				item.Files = s.Summary.TotalFiles
				item.Size = s.Summary.TotalSize
				item.Duration = s.Summary.Duration.String()
			}
			items = append(items, item)
		}
		type snapshotsResultJSON struct {
			Dest      string     `json:"dest"`
			Snapshots []snapJSON `json:"snapshots"`
			Count     int        `json:"count"`
		}
		out := snapshotsResultJSON{
			Dest:      dest.Name,
			Snapshots: items,
			Count:     len(items),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(snaps) == 0 {
		logger.Info("No snapshots found", "dest", dest.Name)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTIME\tHOSTNAME\tFILES\tSIZE\tDURATION")
	fmt.Fprintln(w, "--\t----\t--------\t-----\t----\t--------")
	for _, s := range snaps {
		files := "-"
		size := "-"
		dur := "-"
		if s.Summary != nil {
			files = fmt.Sprintf("%d", s.Summary.TotalFiles)
			size = formatBytes(s.Summary.TotalSize)
			dur = s.Summary.Duration.Round(time.Millisecond).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID[:12],
			s.Time.Local().Format("2006-01-02 15:04:05"),
			s.Hostname,
			files,
			size,
			dur,
		)
	}
	w.Flush()

	fmt.Printf("\n%d snapshot(s)\n", len(snaps))
	return nil
}
