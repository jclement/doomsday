package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/restore"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	restoreFlagTarget    string
	restoreFlagOverwrite bool
	restoreFlagDryRun    bool
)

var restoreCmd = &cobra.Command{
	Use:   "restore <snap[:path]>",
	Short: "Restore files from a backup snapshot",
	Long: `Restore files from a snapshot.

The <snap[:path]> argument specifies which snapshot to restore from:
  <snapshot-id>          - restore the entire snapshot
  <snapshot-id>:<path>   - restore a specific path from the snapshot
  latest                 - restore from the most recent snapshot
  latest:<path>          - restore a specific path from the most recent snapshot

Examples:
  doomsday client restore abc123 --target /tmp/restore
  doomsday client restore abc123:Documents/taxes --target /tmp/taxes
  doomsday client restore latest --target /tmp/restore --overwrite`,
	Args: exactArgs(1),
	RunE: runRestore,
}

func init() {
	restoreCmd.Flags().StringVarP(&restoreFlagTarget, "target", "t", "", "target directory for restore (required)")
	restoreCmd.Flags().BoolVar(&restoreFlagOverwrite, "overwrite", false, "overwrite existing files")
	restoreCmd.Flags().BoolVarP(&restoreFlagDryRun, "dry-run", "n", false, "show what would be restored without writing")
	_ = restoreCmd.MarkFlagRequired("target")
}

func runRestore(cmd *cobra.Command, args []string) error {
	snapArg := args[0]
	snapshotID, includePath := parseSnapPath(snapArg)

	logger.Info("Restoring", "snapshot", snapshotID, "path", includePath, "target", restoreFlagTarget)

	if restoreFlagDryRun {
		logger.Info("Dry run mode: no files will be written")
	}

	if !restoreFlagDryRun {
		targetParent := filepath.Dir(restoreFlagTarget)
		fi, err := os.Stat(targetParent)
		if err != nil {
			return fmt.Errorf("target parent directory does not exist: %s", targetParent)
		}
		if !fi.IsDir() {
			return fmt.Errorf("target parent is not a directory: %s", targetParent)
		}
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Warn("Received signal, stopping restore...", "signal", sig)
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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Shared, "restore")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	resolved, err := resolveSnapshotID(ctx, r, "", snapshotID)
	if err != nil {
		return fmt.Errorf("resolve snapshot: %w", err)
	}
	if resolved != snapshotID {
		logger.Info("Resolved snapshot", "input", snapshotID, "id", resolved)
	}

	isInteractive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) && !flagJSON && !flagQuiet && !restoreFlagDryRun

	// Set up progress display.
	var prog *restoreProgress
	if isInteractive {
		prog = newRestoreProgress(dest.Name)
	}

	opts := restore.Options{
		Overwrite: restoreFlagOverwrite,
		DryRun:    restoreFlagDryRun,
		OnProgress: func(ev restore.ProgressEvent) {
			if prog != nil {
				prog.Update(ev)
			} else if !flagJSON && ev.Path != "" {
				logger.Info("Restoring",
					"file", ev.Path,
					"progress", fmt.Sprintf("%d/%d", ev.FilesCompleted, ev.FilesTotal),
				)
			}
		},
	}

	if includePath != "" {
		opts.IncludePaths = []string{includePath}
	}

	if err := restore.Run(ctx, r, resolved, restoreFlagTarget, opts); err != nil {
		if prog != nil {
			prog.Clear()
		}
		return fmt.Errorf("restore: %w", err)
	}

	if prog != nil {
		prog.Clear()
	}

	logger.Info("Restore complete", "snapshot", resolved, "target", restoreFlagTarget)

	if flagJSON {
		printRestoreJSON(resolved, restoreFlagTarget)
	}

	return nil
}

// restoreProgress shows a single updating line for restore progress.
type restoreProgress struct {
	name       string
	termWidth  int
	spinIdx    int
	lastRender int64 // unix millis of last render
}

func newRestoreProgress(name string) *restoreProgress {
	w := 80
	if tw, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && tw > 0 {
		w = tw
	}
	return &restoreProgress{name: name, termWidth: w}
}

func (p *restoreProgress) Update(ev restore.ProgressEvent) {
	now := unixMillis()
	if now-p.lastRender < 200 {
		return
	}
	p.lastRender = now
	p.spinIdx = (p.spinIdx + 1) % len(spinnerFrames)

	spinner := cliStyles.Brand.Render(spinnerFrames[p.spinIdx])
	sep := cliStyles.Muted.Render(" · ")

	var parts []string
	if ev.FilesTotal > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d files", ev.FilesCompleted, ev.FilesTotal))
	}
	parts = append(parts, fmt.Sprintf("%s written", formatBytes(ev.BytesWritten)))
	if ev.Path != "" {
		maxLen := p.termWidth - 40 - len(p.name)
		if maxLen < 10 {
			maxLen = 10
		}
		file := ev.Path
		if len(file) > maxLen {
			file = "..." + file[len(file)-maxLen+3:]
		}
		parts = append(parts, cliStyles.Muted.Render(file))
	}

	line := fmt.Sprintf("  %s %s  %s", spinner, p.name, strings.Join(parts, sep))
	if len(line) > p.termWidth-1 {
		line = line[:p.termWidth-4] + "..."
	}
	if pad := p.termWidth - 1 - len(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	fmt.Fprintf(os.Stderr, "\r%s", line)
}

func (p *restoreProgress) Clear() {
	fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", p.termWidth-1))
}

func unixMillis() int64 {
	return time.Now().UnixMilli()
}

// printRestoreJSON outputs restore results as JSON.
func printRestoreJSON(snapshotID, target string) {
	type restoreResultJSON struct {
		Snapshot string `json:"snapshot"`
		Target   string `json:"target"`
		Status   string `json:"status"`
	}
	out := restoreResultJSON{
		Snapshot: snapshotID,
		Target:   target,
		Status:   "ok",
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}

// parseSnapPath splits "snapid:path" into its components.
func parseSnapPath(s string) (snapshotID, path string) {
	parts := strings.SplitN(s, ":", 2)
	snapshotID = parts[0]
	if len(parts) > 1 {
		path = parts[1]
	}
	return
}
