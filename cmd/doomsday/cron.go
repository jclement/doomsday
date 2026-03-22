package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/check"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/lock"
	"github.com/jclement/doomsday/internal/notify"
	"github.com/jclement/doomsday/internal/prune"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/scheduler"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/types"
	"github.com/jclement/doomsday/internal/whimsy"
	"github.com/spf13/cobra"
)

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Run scheduled backups (for cron/systemd/launchd)",
	Long: `Execute one scheduler pass: check schedule, run backup if due,
auto-prune, and auto-check.

Designed to be called from cron, systemd timers, or launchd. Runs once
and exits. The scheduler reads and writes state.json to track last run times.

Examples:
  doomsday client cron
  doomsday client cron --json
  */5 * * * * doomsday client cron --quiet`,
	RunE: runCron,
}

var cronInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the scheduler for automatic backups",
	Long: `Auto-detect the platform and install the appropriate scheduler entry:

  Linux (systemd)   User-level systemd timer + service
  macOS (launchd)   LaunchAgent plist
  Other             Prints a manual crontab line

Examples:
  doomsday client cron install`,
	RunE: runCronInstall,
}

var cronUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the installed scheduler",
	RunE:  runCronUninstall,
}

var cronStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show scheduler installation and backup run status",
	RunE:  runCronStatus,
}

func init() {
	cronCmd.AddCommand(cronInstallCmd)
	cronCmd.AddCommand(cronUninstallCmd)
	cronCmd.AddCommand(cronStatusCmd)
}

func runCron(cmd *cobra.Command, args []string) error {
	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	whimsy.SetEnabled(false)

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
			logger.Warn("Received signal, stopping scheduler...", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	notifier := buildNotifier(cfg)
	configName := backupConfigName()
	slogger := slog.Default()

	opts := scheduler.Options{
		Config:     cfg,
		ConfigName: configName,
		Logger:     slogger,
		Backup: func(ctx context.Context) error {
			err := cronRunBackup(ctx, cfg, masterKey)
			if err != nil {
				sendNotification(ctx, notifier, cfg.Notifications.Policy, notify.Event{
					Status:  "failure",
					Message: fmt.Sprintf("Scheduled backup failed: %v", err),
					Config:  configName,
				})
			} else {
				sendNotification(ctx, notifier, cfg.Notifications.Policy, notify.Event{
					Status:  "success",
					Message: "Scheduled backup completed",
					Config:  configName,
				})
			}
			return err
		},
		Prune: func(ctx context.Context) error {
			return cronRunPrune(ctx, cfg, masterKey)
		},
		Check: func(ctx context.Context) error {
			err := cronRunCheck(ctx, cfg, masterKey)
			if err != nil {
				sendNotification(ctx, notifier, cfg.Notifications.Policy, notify.Event{
					Status:  "warning",
					Message: fmt.Sprintf("Integrity check failed: %v", err),
					Config:  configName,
				})
			}
			return err
		},
	}

	result, err := scheduler.Run(ctx, opts)
	if err != nil {
		return fmt.Errorf("scheduler: %w", err)
	}

	if flagJSON {
		var errStrings []string
		for _, e := range result.Errors {
			errStrings = append(errStrings, e.Error())
		}
		type cronResultJSON struct {
			BackupsRun     int      `json:"backups_run"`
			BackupsSkipped int      `json:"backups_skipped"`
			PrunesRun      int      `json:"prunes_run"`
			ChecksRun      int      `json:"checks_run"`
			Errors         []string `json:"errors"`
		}
		out := cronResultJSON{
			BackupsRun:     result.BackupsRun,
			BackupsSkipped: result.BackupsSkipped,
			PrunesRun:      result.PrunesRun,
			ChecksRun:      result.ChecksRun,
			Errors:         errStrings,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			logger.Error("Scheduler error", "error", e)
		}
		return fmt.Errorf("scheduler completed with %d error(s)", len(result.Errors))
	}

	logger.Info("Scheduler pass complete",
		"backups_run", result.BackupsRun,
		"backups_skipped", result.BackupsSkipped,
		"prunes_run", result.PrunesRun,
		"checks_run", result.ChecksRun,
	)

	return nil
}

func cronRunBackup(ctx context.Context, cfg *config.Config, masterKey [32]byte) error {
	hostname, _ := os.Hostname()
	configName := backupConfigName()
	opts := backup.Options{
		Paths:            cfg.SourcePaths(),
		Excludes:         cfg.Exclude,
		PerSource:        buildPerSource(cfg),
		ConfigName:       configName,
		Hostname:         hostname,
		CompressionLevel: cfg.Settings.CompressionLevel,
	}

	destinations := cfg.ActiveDestinations()
	for _, dest := range destinations {
		dc := dest
		backend, err := openBackend(ctx, &dc)
		if err != nil {
			return fmt.Errorf("open backend %s: %w", dest.Name, err)
		}
		defer backend.Close()

		r, err := openRepo(ctx, backend, masterKey, cfg.Settings.CacheDir)
		if err != nil {
			if !errors.Is(err, types.ErrNotFound) {
				return fmt.Errorf("open repo on %s: %w", dest.Name, err)
			}
			logger.Info("Repository not found, initializing", "dest", dest.Name)
			r, err = initRepoIfNeeded(ctx, backend, masterKey, dest.Name)
			if err != nil {
				return err
			}
		}

		lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Exclusive, "cron-backup")
		if err != nil {
			return fmt.Errorf("acquire lock on %s: %w", dest.Name, err)
		}
		defer lk.Release(ctx)

		_, err = backup.Run(ctx, r, opts)
		if err != nil {
			return fmt.Errorf("backup to %s: %w", dest.Name, err)
		}
	}

	return nil
}

func cronRunPrune(ctx context.Context, cfg *config.Config, masterKey [32]byte) error {
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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Exclusive, "cron-prune")
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
			continue
		}
		matching = append(matching, snap)
	}

	ret := cfg.Retention
	var keepWithin time.Duration
	if ret.KeepWithin != "" {
		keepWithin, _ = parseKeepWithin(ret.KeepWithin)
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

	_, forget := prune.ApplyPolicy(matching, policy)

	for _, s := range forget {
		name := s.ID + ".json"
		if err := backend.Remove(ctx, types.FileTypeSnapshot, name); err != nil {
			logger.Warn("Failed to remove snapshot during prune", "id", s.ID[:12], "error", err)
		}
	}

	return nil
}

func cronRunCheck(ctx context.Context, cfg *config.Config, masterKey [32]byte) error {
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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Shared, "cron-check")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	report, err := check.Run(ctx, r, check.LevelStructure)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}

	if !report.OK() {
		return fmt.Errorf("integrity check found %d error(s)", len(report.Errors))
	}

	return nil
}

// initRepoIfNeeded initializes a new repo if openRepo failed.
func initRepoIfNeeded(ctx context.Context, backend types.Backend, masterKey [32]byte, destName string) (*repo.Repository, error) {
	r, err := repo.Init(ctx, backend, masterKey)
	if err != nil {
		return nil, fmt.Errorf("init repo on %s: %w", destName, err)
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// cron install
// ---------------------------------------------------------------------------

func runCronInstall(cmd *cobra.Command, args []string) error {
	msg, err := scheduler.Install()
	if err != nil {
		return fmt.Errorf("cron install: %w", err)
	}

	if flagJSON {
		type cronInstallResultJSON struct {
			Installed bool   `json:"installed"`
			Platform  string `json:"platform"`
			Message   string `json:"message"`
		}
		out := cronInstallResultJSON{
			Installed: true,
			Platform:  runtime.GOOS,
			Message:   msg,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	logger.Info(msg)
	return nil
}

// ---------------------------------------------------------------------------
// cron uninstall
// ---------------------------------------------------------------------------

func runCronUninstall(cmd *cobra.Command, args []string) error {
	platform := runtime.GOOS
	var detail string
	switch platform {
	case "linux":
		timerPath, _ := scheduler.SystemdTimerPath()
		servicePath, _ := scheduler.SystemdServicePath()
		detail = fmt.Sprintf("Removed systemd timer:\n  %s\n  %s", timerPath, servicePath)
	case "darwin":
		plistPath, _ := scheduler.LaunchdPlistPath()
		detail = fmt.Sprintf("Removed launchd plist:\n  %s", plistPath)
	default:
		detail = fmt.Sprintf("Nothing to remove on %s", platform)
	}

	if err := scheduler.Uninstall(); err != nil {
		return fmt.Errorf("cron uninstall: %w", err)
	}

	if flagJSON {
		type cronUninstallResultJSON struct {
			Uninstalled bool   `json:"uninstalled"`
			Platform    string `json:"platform"`
			Message     string `json:"message"`
		}
		out := cronUninstallResultJSON{
			Uninstalled: true,
			Platform:    platform,
			Message:     detail,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	logger.Info(detail)
	return nil
}

// ---------------------------------------------------------------------------
// cron status
// ---------------------------------------------------------------------------

func runCronStatus(cmd *cobra.Command, args []string) error {
	installed, schedulerType := detectSchedulerInstalled()

	statePath := scheduler.DefaultStatePath()
	state, stateErr := scheduler.LoadState(statePath)

	cfg, cfgErr := loadConfig()

	configName := backupConfigName()

	if flagJSON {
		type cronStatusResultJSON struct {
			Installed     bool   `json:"installed"`
			SchedulerType string `json:"scheduler_type"`
			Platform      string `json:"platform"`
			StatePath     string `json:"state_path"`
			LastBackup    string `json:"last_backup,omitempty"`
			Overdue       bool   `json:"overdue"`
			StateError    string `json:"state_error,omitempty"`
			ConfigError   string `json:"config_error,omitempty"`
		}
		out := cronStatusResultJSON{
			Installed:     installed,
			SchedulerType: schedulerType,
			Platform:      runtime.GOOS,
			StatePath:     statePath,
		}
		if state != nil && cfg != nil && cfgErr == nil {
			if t := state.LastRun(configName); !t.IsZero() {
				out.LastBackup = t.Format(time.RFC3339)
			}
			if cfg.Schedule != "" {
				interval, perr := config.ParseSchedule(cfg.Schedule)
				lastRun := state.LastRun(configName)
				if perr == nil && (lastRun.IsZero() || time.Since(lastRun) > interval*2) {
					out.Overdue = true
				}
			}
		}
		if stateErr != nil {
			out.StateError = stateErr.Error()
		}
		if cfgErr != nil {
			out.ConfigError = cfgErr.Error()
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if installed {
		logger.Info("Scheduler installed", "type", schedulerType)
	} else {
		logger.Warn("Scheduler not installed (run 'doomsday client cron install')")
	}

	if stateErr != nil {
		logger.Warn("Could not load state file", "path", statePath, "error", stateErr)
	} else if cfgErr != nil {
		logger.Warn("Could not load config", "error", cfgErr)
	} else if state != nil && cfg != nil {
		lastRun := state.LastRun(configName)
		if lastRun.IsZero() {
			logger.Info("Last backup: never")
		} else {
			ago := time.Since(lastRun).Truncate(time.Second)
			overdueStr := ""
			if cfg.Schedule != "" {
				interval, perr := config.ParseSchedule(cfg.Schedule)
				if perr == nil && time.Since(lastRun) > interval*2 {
					overdueStr = " [OVERDUE]"
				}
			}
			logger.Info(fmt.Sprintf("Last backup: %s (%s ago)%s", lastRun.Local().Format(time.RFC3339), ago, overdueStr))
		}
	}

	return nil
}
