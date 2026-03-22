package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jclement/doomsday/internal/tui/views"
	"github.com/jclement/doomsday/internal/web"
	"github.com/spf13/cobra"
)

var browseCmd = &cobra.Command{
	Use:   "browse",
	Short: "Browse backups in a web browser",
	Long: `Open a local web UI to navigate backup destinations, snapshots,
and files in your browser.

Starts a localhost-only web server on a random port with an auth
token in the URL. Attempts to open your default browser automatically.

Examples:
  doomsday client browse`,
	RunE: runBrowseWeb,
}

func runBrowseWeb(cmd *cobra.Command, args []string) error {
	cfg, err := loadAndValidateConfig()
	if err != nil {
		return err
	}

	session := views.NewSession(cfg)
	defer session.Close()

	// Try auto-unlock (env/cmd/literal keys, plaintext key files).
	if !session.TryAutoUnlock() {
		if session.NeedsPassword() {
			password, err := promptPassword("Enter repository password: ")
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			if err := session.Unlock(password); err != nil {
				return fmt.Errorf("unlock: %w", err)
			}
		} else {
			return fmt.Errorf("failed to unlock repository")
		}
	}

	srv, err := web.New(session, backupConfigName())
	if err != nil {
		return fmt.Errorf("start web server: %w", err)
	}

	url := srv.URL()
	fmt.Println()
	fmt.Printf("  %s %s\n", cliStyles.Brand.Render("Doomsday Web UI"), cliStyles.Muted.Render("running"))
	fmt.Printf("  %s\n", cliStyles.Value.Render(url))
	fmt.Println()
	fmt.Println(cliStyles.Muted.Render("  Press Ctrl+C to stop"))
	fmt.Println()

	srv.OpenBrowser()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		logger.Info("Shutting down web server...")
		cancel()
	}()

	return srv.Serve(ctx)
}
