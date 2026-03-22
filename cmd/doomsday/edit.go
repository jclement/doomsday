package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var editCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open client config in your editor",
	Long: `Open the client configuration file in your default editor.

Uses $EDITOR, $VISUAL, or falls back to vi.

Examples:
  doomsday client edit
  EDITOR=nano doomsday client edit`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return openEditor(clientConfigPath())
	},
}

var serverEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open server config in your editor",
	Long: `Open the server configuration file in your default editor.

Uses $EDITOR, $VISUAL, or falls back to vi.

Examples:
  doomsday server edit
  EDITOR=nano doomsday server edit`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return openEditor(serverConfigPath())
	},
}

// openEditor opens the given file path in the user's preferred editor.
func openEditor(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("config not found at %s\n\nRun the appropriate init command first", path)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	c := exec.Command(editor, path)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
