package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/log"
	"github.com/jclement/doomsday/internal/whimsy"
	"github.com/spf13/cobra"
)

// Build-time variables set via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Persistent flag values shared across all commands.
var (
	flagVerbose  bool
	flagQuiet    bool
	flagJSON     bool
	flagNoWhimsy bool
	flagConfig   string
)

// logger is the application-wide logger, configured by persistent flags.
var logger *log.Logger

var rootCmd = &cobra.Command{
	Use:   "doomsday",
	Short: "Backup for the end of the world",
	Long: `Doomsday is an all-in-one, end-to-end encrypted backup solution.
Single Go binary. Restic-level robustness. Can never lose data.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		initLogger()
	},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(cliStyles.Brand.Render(banner))
		fmt.Printf("  %s %s\n", cliStyles.Muted.Render("version"), version)
		if !flagNoWhimsy && !flagQuiet {
			if tagline := whimsy.VersionTagline(); tagline != "" {
				fmt.Printf("  %s\n", cliStyles.Tagline.Render(tagline))
			}
		}
		fmt.Println()
		cmd.Help()
	},
}

// clientCmd is the parent for all client-side backup commands.
var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Backup client commands",
	Long: `Manage backups, snapshots, destinations, and sources.

Run without a subcommand to see status.

Examples:
  doomsday client backup
  doomsday client snapshots
  doomsday client -c ~/alt-config.yaml backup`,
	RunE: runClientStatus,
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "suppress non-essential output")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output in JSON format")
	rootCmd.PersistentFlags().BoolVar(&flagNoWhimsy, "no-whimsy", false, "disable whimsical messages")

	// Client config flag.
	clientCmd.PersistentFlags().StringVarP(&flagConfig, "config", "c", "", "path to config file (default ~/.config/doomsday/client.yaml)")

	// Register top-level commands.
	rootCmd.AddCommand(clientCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)

	// Register client subcommands.
	clientCmd.AddCommand(initCmd)
	clientCmd.AddCommand(editCmd)
	clientCmd.AddCommand(backupCmd)
	clientCmd.AddCommand(restoreCmd)
	clientCmd.AddCommand(snapshotsCmd)
	clientCmd.AddCommand(statusCmd)
	clientCmd.AddCommand(checkCmd)
	clientCmd.AddCommand(pruneCmd)
	clientCmd.AddCommand(unlockCmd)
	clientCmd.AddCommand(lsCmd)
	clientCmd.AddCommand(findCmd)
	clientCmd.AddCommand(diffCmd)
	clientCmd.AddCommand(forgetCmd)
	clientCmd.AddCommand(cronCmd)
	clientCmd.AddCommand(browseCmd)
}

// runClientStatus is the default action when "doomsday client" is invoked
// without a subcommand — shows a quick status overview.
func runClientStatus(cmd *cobra.Command, args []string) error {
	return runStatus(cmd, args)
}

// initLogger configures the charmbracelet/log logger based on flags.
func initLogger() {
	logger = log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		TimeFormat:      "15:04:05",
	})

	switch {
	case flagQuiet:
		logger.SetLevel(log.WarnLevel)
	case flagVerbose:
		logger.SetLevel(log.DebugLevel)
	default:
		logger.SetLevel(log.InfoLevel)
	}
}

// exitError prints a styled error message and exits with the given code.
func exitError(err error, code int) {
	fmt.Fprintf(os.Stderr, "%s %s\n", cliStyles.Error.Render("error:"), err)
	os.Exit(code)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		exitError(err, 1)
	}
}
