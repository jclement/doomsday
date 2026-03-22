package main

import (
	"github.com/jclement/doomsday/internal/tui/views"
	"github.com/spf13/cobra"
)

var browseTUICmd = &cobra.Command{
	Use:   "browse-tui",
	Short: "Browse backups in the terminal (TUI)",
	Long: `Open a terminal TUI browser to navigate backup configurations, destinations,
snapshots, and files.

Requires a configured encryption key and at least one destination.

Examples:
  doomsday client browse-tui`,
	RunE: runBrowseTUI,
}

func runBrowseTUI(cmd *cobra.Command, args []string) error {
	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	return views.RunTUI(cfg, backupConfigName())
}
