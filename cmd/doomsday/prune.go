package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/prune"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/types"
	"github.com/spf13/cobra"
)

var (
	pruneFlagDryRun bool
	pruneFlagYes    bool
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Run retention policy and garbage collect",
	Long: `Apply retention policies and garbage collect unreferenced data.

Prune performs these steps:
  1. Apply retention rules, mark snapshots for removal
  2. Walk all kept snapshots, build referenced blob set
  3. Identify and repack partially-used packs
  4. Delete unreferenced packs and old indexes
  5. Verify structural integrity

Use --dry-run to preview what would be removed without making changes.

Examples:
  doomsday client prune
  doomsday client prune --dry-run
  doomsday client prune --json`,
	RunE: runPrune,
}

func init() {
	pruneCmd.Flags().BoolVarP(&pruneFlagDryRun, "dry-run", "n", false, "show what would be pruned without making changes")
	pruneCmd.Flags().BoolVarP(&pruneFlagYes, "yes", "y", false, "skip confirmation prompt")
}

func runPrune(cmd *cobra.Command, args []string) error {
	logger.Info("Running prune", "dry_run", pruneFlagDryRun)

	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	ret := cfg.Retention
	logger.Info("Retention policy",
		"keep_last", ret.KeepLast,
		"keep_hourly", ret.KeepHourly,
		"keep_daily", ret.KeepDaily,
		"keep_weekly", ret.KeepWeekly,
		"keep_monthly", ret.KeepMonthly,
		"keep_yearly", ret.KeepYearly,
	)

	masterKey, err := openMasterKey(cfg)
	if err != nil {
		return fmt.Errorf("open master key: %w", err)
	}
	defer masterKey.Zero()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Warn("Received signal, stopping prune...", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Exclusive, "prune")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	ids, err := r.ListSnapshots(ctx)
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}

	var matching []*snapshot.Snapshot
	for _, id := range ids {
		snap, err := r.LoadSnapshot(ctx, id)
		if err != nil {
			logger.Warn("Failed to load snapshot", "id", id, "error", err)
			continue
		}
		matching = append(matching, snap)
	}

	var keepWithin time.Duration
	if ret.KeepWithin != "" {
		keepWithin, err = parseKeepWithin(ret.KeepWithin)
		if err != nil {
			logger.Warn("Invalid keep_within value, ignoring", "value", ret.KeepWithin, "error", err)
		}
	}

	policy := prune.Policy{
		KeepLast:    ret.KeepLast,
		KeepHourly:  ret.KeepHourly,
		KeepDaily:   ret.KeepDaily,
		KeepWeekly:  ret.KeepWeekly,
		KeepMonthly: ret.KeepMonthly,
		KeepYearly:  ret.KeepYearly,
		KeepWithin:  keepWithin,
	}

	keep, forget := prune.ApplyPolicy(matching, policy)

	logger.Info("Retention results", "keep", len(keep), "forget", len(forget))

	for _, s := range forget {
		logger.Info("Forgetting snapshot",
			"id", s.ID[:12],
			"time", s.Time.Local().Format("2006-01-02 15:04:05"),
		)
	}

	for _, s := range keep {
		logger.Debug("Keeping snapshot",
			"id", s.ID[:12],
			"time", s.Time.Local().Format("2006-01-02 15:04:05"),
		)
	}

	if !pruneFlagDryRun && len(forget) > 0 {
		if !pruneFlagYes {
			fmt.Printf("\nThis will permanently delete %d snapshot(s) and unreferenced data. Continue? [y/N] ", len(forget))
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				logger.Info("Aborted")
				return nil
			}
		}

		for _, s := range forget {
			name := s.ID + ".json"
			if err := backend.Remove(ctx, types.FileTypeSnapshot, name); err != nil {
				logger.Error("Failed to remove snapshot", "id", s.ID[:12], "error", err)
			}
		}
		logger.Info("Removed forgotten snapshots", "count", len(forget))
	}

	if flagJSON {
		type snapInfo struct {
			ID   string    `json:"id"`
			Time time.Time `json:"time"`
		}
		toInfo := func(snaps []*snapshot.Snapshot) []snapInfo {
			var out []snapInfo
			for _, s := range snaps {
				out = append(out, snapInfo{ID: s.ID, Time: s.Time})
			}
			return out
		}
		type pruneResultJSON struct {
			DryRun bool       `json:"dry_run"`
			Keep   []snapInfo `json:"keep"`
			Forget []snapInfo `json:"forget"`
		}
		out := pruneResultJSON{
			DryRun: pruneFlagDryRun,
			Keep:   toInfo(keep),
			Forget: toInfo(forget),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	return nil
}

// parseKeepWithin converts short-form durations like "30d" to time.Duration.
func parseKeepWithin(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid keep_within %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	if strings.HasSuffix(s, "w") {
		weeks, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid keep_within %q: %w", s, err)
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}

	return time.ParseDuration(s)
}
