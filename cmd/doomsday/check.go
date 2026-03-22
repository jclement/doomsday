package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jclement/doomsday/internal/check"
	"github.com/jclement/doomsday/internal/lock"
	"github.com/spf13/cobra"
)

var (
	checkFlagReadData bool
	checkFlagLevel    string
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Verify repository integrity",
	Long: `Run integrity verification on a backup repository.

Three check levels:
  structure  - Verify indexes and snapshot references (fast, no data download)
  headers    - Download pack headers, verify against index entries
  full       - Decrypt every blob, verify HMAC-SHA256 content IDs (slow)

The default level is "structure". Use --read-data for a full check.

Examples:
  doomsday client check
  doomsday client check --level headers
  doomsday client check --read-data
  doomsday client check --json`,
	RunE: runCheck,
}

func init() {
	checkCmd.Flags().BoolVar(&checkFlagReadData, "read-data", false, "full data verification (decrypt and verify every blob)")
	checkCmd.Flags().StringVar(&checkFlagLevel, "level", "structure", "check level: structure, headers, full")
}

func runCheck(cmd *cobra.Command, args []string) error {
	// Determine check level.
	level := check.LevelStructure
	if checkFlagReadData {
		level = check.LevelFull
	} else {
		switch checkFlagLevel {
		case "structure":
			level = check.LevelStructure
		case "headers":
			level = check.LevelHeaders
		case "full":
			level = check.LevelFull
		default:
			return fmt.Errorf("unknown check level %q (use: structure, headers, full)", checkFlagLevel)
		}
	}

	levelName := [...]string{"structure", "headers", "full"}[level]
	logger.Info("Running integrity check", "level", levelName)

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
			logger.Warn("Received signal, stopping check...", "signal", sig)
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

	lk, err := lock.Acquire(ctx, backend, r.Keys().SubKeys.Config, lock.Shared, "check")
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lk.Release(ctx)

	report, err := check.Run(ctx, r, level)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}

	if flagJSON {
		type errorJSON struct {
			Pack    string `json:"pack,omitempty"`
			BlobID  string `json:"blob_id,omitempty"`
			Message string `json:"message"`
		}
		var errs []errorJSON
		for _, e := range report.Errors {
			errs = append(errs, errorJSON{Pack: e.Pack, BlobID: e.BlobID, Message: e.Message})
		}
		type checkResultJSON struct {
			Level            string      `json:"level"`
			OK               bool        `json:"ok"`
			PacksChecked     int         `json:"packs_checked"`
			BlobsChecked     int         `json:"blobs_checked"`
			SnapshotsChecked int         `json:"snapshots_checked"`
			Errors           []errorJSON `json:"errors"`
		}
		out := checkResultJSON{
			Level:            levelName,
			OK:               report.OK(),
			PacksChecked:     report.PacksChecked,
			BlobsChecked:     report.BlobsChecked,
			SnapshotsChecked: report.SnapshotsChecked,
			Errors:           errs,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
		if !report.OK() {
			return fmt.Errorf("integrity check found %d error(s)", len(report.Errors))
		}
		return nil
	}

	logger.Info("Check results",
		"level", levelName,
		"snapshots", report.SnapshotsChecked,
		"packs", report.PacksChecked,
		"blobs", report.BlobsChecked,
		"errors", len(report.Errors),
	)

	for _, e := range report.Errors {
		logger.Error("Integrity error", "pack", e.Pack, "blob", e.BlobID, "message", e.Message)
	}

	if report.OK() {
		logger.Info("No errors found, repository is healthy")
	} else {
		return fmt.Errorf("integrity check found %d error(s)", len(report.Errors))
	}

	return nil
}
