package main

import (
	"github.com/jclement/doomsday/internal/tui/views"
	"github.com/spf13/cobra"
)

var browseCmd = &cobra.Command{
	Use:   "browse",
	Short: "Browse backups interactively",
	Long: `Open a TUI browser to navigate backup configurations, destinations,
snapshots, and files.

Requires a configured encryption key and at least one destination.

Examples:
  doomsday client browse`,
	RunE: runBrowse,
}

func runBrowse(cmd *cobra.Command, args []string) error {
	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	return views.RunTUI(cfg, backupConfigName())
}
