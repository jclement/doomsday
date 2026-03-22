package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/notify"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/scheduler"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/types"
	"github.com/jclement/doomsday/internal/whimsy"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	backupFlagDryRun bool
)

var backupCmd = &cobra.Command{
	Use:   "backup [destination-name]",
	Short: "Run a backup",
	Long: `Run a backup to all active destinations, or to a specific destination by name.

Use --dry-run to see what would be backed up without writing any data.

Examples:
  doomsday client backup
  doomsday client backup usb
  doomsday client backup --dry-run`,
	Args: maxArgs(1),
	RunE: runBackup,
}

func init() {
	backupCmd.Flags().BoolVarP(&backupFlagDryRun, "dry-run", "n", false, "show what would be backed up without writing data")
}

// backupResult holds the outcome of a single destination backup.
type backupResult struct {
	dest    string
	snap    *snapshot.Snapshot
	elapsed time.Duration
	err     error
}

func runBackup(cmd *cobra.Command, args []string) error {
	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	// Determine which destinations to back up to.
	var destinations []config.DestConfig
	if len(args) > 0 {
		dest, err := cfg.FindDestination(args[0])
		if err != nil {
			return err
		}
		destinations = []config.DestConfig{*dest}
	} else {
		destinations = cfg.ActiveDestinations()
		if len(destinations) == 0 {
			return fmt.Errorf("no active destinations configured")
		}
	}

	// Dry-run mode: walk filesystem only, no auth needed.
	if backupFlagDryRun {
		return runBackupDryRun(cfg)
	}

	// Open master key once.
	masterKey, err := openMasterKey(cfg)
	if err != nil {
		return fmt.Errorf("open master key: %w", err)
	}
	defer masterKey.Zero()

	// Set up signal handling for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Warn("Received signal, shutting down gracefully...", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	notifier := buildNotifier(cfg)
	configName := backupConfigName()
	isInteractive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) && !flagJSON && !flagQuiet
	hostname, _ := os.Hostname()

	logger.Info("Starting backup",
		"paths", strings.Join(cfg.SourcePaths(), ", "),
		"destinations", len(destinations),
	)

	if !flagNoWhimsy && !flagQuiet {
		if msg := whimsy.BackupStart(); msg != "" {
			logger.Info(msg)
		}
	}

	// Set up progress display.
	prog := newMultiDestProgress(isInteractive)
	destIndices := make([]int, len(destinations))
	for i, dest := range destinations {
		destIndices[i] = prog.Register(dest.Name)
	}

	// Run all destinations in parallel.
	results := make([]backupResult, len(destinations))
	var wg sync.WaitGroup
	for i, dest := range destinations {
		wg.Add(1)
		go func(i int, dest config.DestConfig) {
			defer wg.Done()
			idx := destIndices[i]

			startTime := time.Now()
			snap, err := runDestBackup(ctx, cfg, dest, configName, masterKey, hostname, func(stats backup.Stats) {
				prog.Update(idx, stats)
			})

			elapsed := time.Since(startTime)
			results[i] = backupResult{dest: dest.Name, snap: snap, elapsed: elapsed, err: err}
			prog.Complete(idx, err)
		}(i, dest)
	}
	wg.Wait()

	// Clear live progress before printing summaries.
	prog.Clear()

	// Process results.
	var hadErrors bool
	for _, r := range results {
		if r.err != nil {
			logger.Error("Backup failed", "dest", r.dest, "error", r.err)
			hadErrors = true
			sendNotification(ctx, notifier, cfg.Notifications.Policy, notify.Event{
				Status:  "failure",
				Message: fmt.Sprintf("Backup to %q failed: %v", r.dest, r.err),
				Config:  configName,
			})
		} else {
			saveBackupState(configName, r.dest)
			sendNotification(ctx, notifier, cfg.Notifications.Policy, notify.Event{
				Status:  "success",
				Message: fmt.Sprintf("Backup to %q completed successfully", r.dest),
				Config:  configName,
			})
			if flagJSON {
				printBackupJSON(r.dest, r.snap, r.elapsed)
			} else {
				printBackupSummary(r.dest, r.snap, r.elapsed)
			}
		}
	}

	if hadErrors {
		return fmt.Errorf("one or more backups failed")
	}

	if !flagNoWhimsy && !flagQuiet {
		logger.Info(whimsy.BackupComplete())
	}

	return nil
}

// runDestBackup runs a backup to a single destination, calling onProgress for stats updates.
// Returns the snapshot on success.
func runDestBackup(ctx context.Context, cfg *config.Config, dest config.DestConfig, configName string, masterKey [32]byte, hostname string, onProgress backup.ProgressFunc) (*snapshot.Snapshot, error) {
	dc := dest
	backend, err := openBackend(ctx, &dc)
	if err != nil {
		return nil, fmt.Errorf("open backend %s: %w", dest.Name, err)
	}
	defer backend.Close()

	r, err := openRepo(ctx, backend, masterKey, cfg.Settings.CacheDir)
	if err != nil {
		if !errors.Is(err, types.ErrNotFound) {
			return nil, fmt.Errorf("open repo on %s: %w", dest.Name, err)
		}
		r, err = repo.Init(ctx, backend, masterKey)
		if err != nil {
			return nil, fmt.Errorf("init repo on %s: %w", dest.Name, err)
		}
	}

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Exclusive, "backup")
	if err != nil {
		return nil, fmt.Errorf("acquire lock on %s: %w", dest.Name, err)
	}
	defer lk.Release(ctx)

	opts := backup.Options{
		Paths:            cfg.SourcePaths(),
		Excludes:         cfg.Exclude,
		PerSource:        buildPerSource(cfg),
		ConfigName:       configName,
		Hostname:         hostname,
		CompressionLevel: cfg.Settings.CompressionLevel,
		OnProgress:       onProgress,
	}

	snap, err := backup.Run(ctx, r, opts)
	if err != nil {
		return nil, fmt.Errorf("backup to %s: %w", dest.Name, err)
	}

	return snap, nil
}

// printBackupJSON outputs backup results as JSON.
func printBackupJSON(destName string, snap *snapshot.Snapshot, elapsed time.Duration) {
	type backupResultJSON struct {
		Dest           string `json:"dest"`
		Snapshot       string `json:"snapshot"`
		Status         string `json:"status"`
		Elapsed        string `json:"elapsed"`
		FilesTotal     int64  `json:"files_total,omitempty"`
		FilesNew       int64  `json:"files_new,omitempty"`
		FilesUnchanged int64  `json:"files_unchanged,omitempty"`
		Dirs           int64  `json:"dirs,omitempty"`
		DataAdded      int64  `json:"data_added,omitempty"`
		TotalSize      int64  `json:"total_size,omitempty"`
	}
	out := backupResultJSON{
		Dest:     destName,
		Snapshot: snap.ID,
		Status:   "ok",
		Elapsed:  elapsed.String(),
	}
	if snap.Summary != nil {
		out.FilesTotal = snap.Summary.TotalFiles
		out.FilesNew = snap.Summary.FilesNew
		out.FilesUnchanged = snap.Summary.FilesUnchanged
		out.Dirs = snap.Summary.DirsNew
		out.DataAdded = snap.Summary.DataAdded
		out.TotalSize = snap.Summary.TotalSize
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}

// runBackupDryRun scans the filesystem and reports what would be backed up.
func runBackupDryRun(cfg *config.Config) error {
	ctx := context.Background()
	logger.Info("Dry run", "paths", strings.Join(cfg.SourcePaths(), ", "))
	result, err := backup.DryRun(ctx, backup.Options{
		Paths:     cfg.SourcePaths(),
		Excludes:  cfg.Exclude,
		PerSource: buildPerSource(cfg),
	})
	if err != nil {
		return fmt.Errorf("dry run: %w", err)
	}

	if flagJSON {
		type backupDryRunJSON struct {
			DryRun    bool  `json:"dry_run"`
			Files     int64 `json:"files"`
			Dirs      int64 `json:"dirs"`
			TotalSize int64 `json:"total_size"`
			Errors    int64 `json:"errors"`
		}
		out := backupDryRunJSON{
			DryRun:    true,
			Files:     result.FilesTotal,
			Dirs:      result.DirsTotal,
			TotalSize: result.BytesTotal,
			Errors:    result.Errors,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	} else {
		logger.Info("Would back up",
			"files", result.FilesTotal,
			"dirs", result.DirsTotal,
			"size", formatBytes(result.BytesTotal),
		)
		if result.Errors > 0 {
			logger.Warn("Inaccessible entries", "count", result.Errors)
		}
	}
	return nil
}

// printBackupSummary prints a formatted backup summary to the logger.
func printBackupSummary(dest string, snap *snapshot.Snapshot, elapsed time.Duration) {
	s := snap.Summary
	if s == nil {
		logger.Info("Backup complete", "dest", dest, "snapshot", snap.ID)
		return
	}

	logger.Info("Backup complete",
		"dest", dest,
		"snapshot", snap.ID[:12],
		"elapsed", elapsed.Round(time.Millisecond),
	)
	logger.Info("Summary",
		"files", s.TotalFiles,
		"changed", s.FilesNew,
		"unchanged", s.FilesUnchanged,
		"dirs", s.DirsNew,
	)
	logger.Info("Data",
		"total", formatBytes(s.TotalSize),
		"added", formatBytes(s.DataAdded),
	)
}

// saveBackupState records a successful backup in the scheduler state file.
func saveBackupState(configName, destName string) {
	statePath := scheduler.DefaultStatePath()
	state, err := scheduler.LoadState(statePath)
	if err != nil {
		logger.Debug("Could not load scheduler state", "error", err)
		state = scheduler.NewState()
	}
	now := time.Now()
	state.SetLastRun(configName, now)
	state.SetLastRun(configName+"/"+destName, now)
	if err := scheduler.SaveState(statePath, state); err != nil {
		logger.Debug("Could not save scheduler state", "error", err)
	}
}

// formatBytes returns a human-readable byte size.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
