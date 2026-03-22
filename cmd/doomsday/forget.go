package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/types"
	"github.com/spf13/cobra"
)

var (
	forgetFlagDryRun bool
	forgetFlagYes    bool
)

var forgetCmd = &cobra.Command{
	Use:   "forget <snapshot-id> [snapshot-id...]",
	Short: "Remove snapshot metadata",
	Long: `Remove one or more snapshot metadata entries from the repository.

This removes only the snapshot metadata — it does NOT delete the actual
backup data. To reclaim space from unreferenced data, run "doomsday client prune"
after forgetting snapshots.

Requires confirmation unless --yes is specified.

Examples:
  doomsday client forget abc123def456
  doomsday client forget abc123 def456 ghi789 --yes
  doomsday client forget abc123 --dry-run`,
	Args: minArgs(1),
	RunE: runForget,
}

func init() {
	forgetCmd.Flags().BoolVarP(&forgetFlagDryRun, "dry-run", "n", false, "show what would be forgotten without making changes")
	forgetCmd.Flags().BoolVarP(&forgetFlagYes, "yes", "y", false, "skip confirmation prompt")
}

func runForget(cmd *cobra.Command, args []string) error {
	snapshotIDs := args

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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Exclusive, "forget")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	// Resolve snapshot IDs (handles "latest" and prefix matching).
	var resolvedIDs []string
	for _, id := range snapshotIDs {
		resolved, err := resolveSnapshotID(ctx, r, "", id)
		if err != nil {
			return fmt.Errorf("resolve snapshot %s: %w", id, err)
		}
		resolvedIDs = append(resolvedIDs, resolved)
	}

	// Show what will be forgotten.
	for _, id := range resolvedIDs {
		snap, _ := r.LoadSnapshot(ctx, id)
		logger.Info("Will forget snapshot",
			"id", id[:12],
			"time", snap.Time.Local().Format("2006-01-02 15:04:05"),
			"hostname", snap.Hostname,
		)
	}

	if forgetFlagDryRun {
		logger.Info("Dry run: no snapshots were removed", "count", len(resolvedIDs))
		if flagJSON {
			return renderForgetJSON(resolvedIDs, true)
		}
		return nil
	}

	if !forgetFlagYes {
		fmt.Printf("\nForget %d snapshot(s)? This cannot be undone. [y/N] ", len(resolvedIDs))
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			logger.Info("Aborted")
			return nil
		}
	}

	var removed []string
	var errors []string
	for _, id := range resolvedIDs {
		name := id + ".json"
		if err := backend.Remove(ctx, types.FileTypeSnapshot, name); err != nil {
			logger.Error("Failed to remove snapshot", "id", id[:12], "error", err)
			errors = append(errors, fmt.Sprintf("%s: %s", id[:12], err.Error()))
		} else {
			logger.Info("Forgot snapshot", "id", id[:12])
			removed = append(removed, id)
		}
	}

	logger.Info("Forget complete", "removed", len(removed), "failed", len(errors))

	if len(removed) > 0 {
		logger.Info("Run 'doomsday client prune' to reclaim space from unreferenced data")
	}

	if flagJSON {
		return renderForgetJSON(removed, false)
	}

	if len(errors) > 0 {
		return fmt.Errorf("%d snapshot(s) could not be removed", len(errors))
	}

	return nil
}

type forgetResultJSON struct {
	DryRun    bool     `json:"dry_run"`
	Forgotten []string `json:"forgotten"`
	Count     int      `json:"count"`
}

func renderForgetJSON(snapshotIDs []string, dryRun bool) error {
	out := forgetResultJSON{
		DryRun:    dryRun,
		Forgotten: snapshotIDs,
		Count:     len(snapshotIDs),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
