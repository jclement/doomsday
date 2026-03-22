package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jclement/doomsday/internal/config"
)

// DefaultPruneInterval is how often auto-prune runs (weekly).
const DefaultPruneInterval = 7 * 24 * time.Hour

// DefaultCheckInterval is how often auto-check (structure level) runs (weekly).
const DefaultCheckInterval = 7 * 24 * time.Hour

// BackupFunc is called to execute a backup.
// The scheduler does not import backup/ directly to avoid circular deps.
type BackupFunc func(ctx context.Context) error

// PruneFunc is called to run auto-prune.
type PruneFunc func(ctx context.Context) error

// CheckFunc is called to run an auto-check (structure level).
type CheckFunc func(ctx context.Context) error

// Options configures a scheduler run.
type Options struct {
	// Config is the full doomsday configuration.
	Config *config.Config

	// ConfigName identifies this config in the state file.
	// If empty, "default" is used.
	ConfigName string

	// StatePath overrides the default state file location.
	// If empty, DefaultStatePath() is used.
	StatePath string

	// Backup is called when backup is due.
	Backup BackupFunc

	// Prune is called after backup when auto-prune is due.
	// If nil, auto-prune is skipped.
	Prune PruneFunc

	// Check is called after prune when auto-check is due.
	// If nil, auto-check is skipped.
	Check CheckFunc

	// Logger is the structured logger for scheduler messages.
	// If nil, slog.Default() is used.
	Logger *slog.Logger

	// Now returns the current time. If nil, time.Now is used.
	// This exists for testability.
	Now func() time.Time

	// PruneInterval overrides DefaultPruneInterval. Zero means use default.
	PruneInterval time.Duration

	// CheckInterval overrides DefaultCheckInterval. Zero means use default.
	CheckInterval time.Duration
}

// Result summarizes a single scheduler invocation.
type Result struct {
	// BackupsRun is the number of backups that were executed (0 or 1).
	BackupsRun int
	// BackupsSkipped is 1 if the backup was not due, 0 otherwise.
	BackupsSkipped int
	// PrunesRun is the number of auto-prunes that were executed.
	PrunesRun int
	// ChecksRun is the number of auto-checks that were executed.
	ChecksRun int
	// Errors collects all non-fatal errors encountered during the run.
	Errors []error
}

// Run executes one scheduler pass: check if the config's schedule is due
// and run backup/prune/check as appropriate.
//
// It is designed to be called from cron/systemd/launchd. It runs once
// and returns. The caller is responsible for locking.
//
// On each invocation:
//  1. Read state.json for last run times
//  2. Run backup if due (respecting schedule field)
//  3. After backup: auto-prune if due (weekly by default)
//  4. After prune: auto-check if due (weekly, structure-level)
//  5. Update state.json
//  6. On any failure: return errors in Result (caller handles notifications)
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("scheduler.Run: config is required")
	}
	if opts.Backup == nil {
		return nil, fmt.Errorf("scheduler.Run: backup function is required")
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	pruneInterval := opts.PruneInterval
	if pruneInterval == 0 {
		pruneInterval = DefaultPruneInterval
	}
	checkInterval := opts.CheckInterval
	if checkInterval == 0 {
		checkInterval = DefaultCheckInterval
	}

	statePath := opts.StatePath
	if statePath == "" {
		statePath = DefaultStatePath()
	}

	configName := opts.ConfigName
	if configName == "" {
		configName = "default"
	}

	// Step 1: Load state
	state, err := LoadState(statePath)
	if err != nil {
		return nil, fmt.Errorf("scheduler.Run: %w", err)
	}

	result := &Result{}

	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("scheduler.Run: %w", err)
	}

	// Skip if no schedule is set (manual only).
	schedule := opts.Config.Schedule
	if schedule == "" {
		logger.Debug("skipping backup with no schedule", "name", configName)
		result.BackupsSkipped++
	} else {
		// Parse the schedule interval.
		interval, err := config.ParseSchedule(schedule)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("scheduler.Run: config %q: %w", configName, err))
		} else {
			currentTime := now()

			// Check if backup is due.
			if !IsDue(state.LastRun(configName), interval, currentTime) {
				logger.Debug("backup not due",
					"name", configName,
					"last_run", state.LastRun(configName),
					"interval", interval,
				)
				result.BackupsSkipped++
			} else {
				// Run backup.
				logger.Info("running scheduled backup", "name", configName)
				if err := opts.Backup(ctx); err != nil {
					result.Errors = append(result.Errors, fmt.Errorf("scheduler.Run: backup %q: %w", configName, err))
				} else {
					result.BackupsRun++
					state.SetLastRun(configName, currentTime)

					// Auto-prune if due (weekly by default).
					if opts.Prune != nil && IsDue(state.LastPruneRun(configName), pruneInterval, currentTime) {
						logger.Info("running auto-prune", "name", configName)
						if err := opts.Prune(ctx); err != nil {
							result.Errors = append(result.Errors, fmt.Errorf("scheduler.Run: prune %q: %w", configName, err))
						} else {
							result.PrunesRun++
							state.SetLastPruneRun(configName, currentTime)
						}
					}

					// Auto-check if due (weekly, structure level).
					if opts.Check != nil && IsDue(state.LastCheckRun(configName), checkInterval, currentTime) {
						logger.Info("running auto-check", "name", configName)
						if err := opts.Check(ctx); err != nil {
							result.Errors = append(result.Errors, fmt.Errorf("scheduler.Run: check %q: %w", configName, err))
						} else {
							result.ChecksRun++
							state.SetLastCheckRun(configName, currentTime)
						}
					}
				}
			}
		}
	}

	// Persist state
	if err := SaveState(statePath, state); err != nil {
		return result, fmt.Errorf("scheduler.Run: %w", err)
	}

	logger.Info("scheduler pass complete",
		"backups_run", result.BackupsRun,
		"backups_skipped", result.BackupsSkipped,
		"prunes_run", result.PrunesRun,
		"checks_run", result.ChecksRun,
		"errors", len(result.Errors),
	)

	return result, nil
}
