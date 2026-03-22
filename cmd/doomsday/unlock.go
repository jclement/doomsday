package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/jclement/doomsday/internal/lock"
	"github.com/spf13/cobra"
)

var unlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Remove stale lock files",
	Long: `Remove all lock files from the repository.

Use this when a previous doomsday process crashed without releasing its lock.
The lock system automatically detects stale locks (not refreshed in 30 minutes),
but this command provides a manual override.

WARNING: Only use this if you are certain no other doomsday process is active.

Examples:
  doomsday client unlock`,
	RunE: runUnlock,
}

func runUnlock(cmd *cobra.Command, args []string) error {
	logger.Info("Removing locks")

	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Remove locks on all destinations.
	for i := range cfg.Destinations {
		dest := cfg.Destinations[i]

		backend, err := openBackend(ctx, &dest)
		if err != nil {
			logger.Error("Failed to open backend", "dest", dest.Name, "error", err)
			continue
		}

		if err := lock.RemoveAll(ctx, backend); err != nil {
			backend.Close()
			logger.Error("Failed to remove locks", "dest", dest.Name, "error", err)
			continue
		}

		backend.Close()
		logger.Info("Locks removed", "dest", dest.Name)
	}

	if flagJSON {
		type unlockResultJSON struct {
			Status string `json:"status"`
		}
		out := unlockResultJSON{
			Status: "unlocked",
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	logger.Info("All locks removed")
	return nil
}
