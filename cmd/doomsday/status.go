package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/scheduler"
	"github.com/jclement/doomsday/internal/types"
	"github.com/jclement/doomsday/internal/whimsy"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show backup status overview",
	Long: `The "is everything okay?" command.

Shows backup configuration, last backup time, destination health,
and any warnings. This is the command to run when you want a quick
check that your backups are working.

Examples:
  doomsday client status
  doomsday client status --json`,
	RunE: runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfgPath := clientConfigPath()

	// Detect uninitialized state.
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return renderUninitializedClient(cfgPath)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w\n\nRun 'doomsday client init' to create a new configuration", err)
	}

	configName := backupConfigName()

	// Load scheduler state for last run times.
	state, err := scheduler.LoadState(scheduler.DefaultStatePath())
	if err != nil {
		logger.Debug("Could not load scheduler state", "error", err)
		state = scheduler.NewState()
	}

	if flagJSON {
		return renderStatusJSON(cfg, state, configName)
	}

	return renderStatusDashboard(cfg, state, configName, cfgPath)
}

func renderUninitializedClient(cfgPath string) error {
	if flagJSON {
		out := map[string]string{
			"status":  "uninitialized",
			"message": "No configuration found",
			"config":  cfgPath,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Println()
	fmt.Print(cliStyles.Brand.Render(banner))
	fmt.Println()
	fmt.Println(cliStyles.Warning.Render("  No configuration found."))
	fmt.Println()
	fmt.Println(kv("Expected:", cfgPath))
	fmt.Println()
	fmt.Println("  Get started:")
	fmt.Println(cliStyles.Success.Render("    doomsday client init"))
	fmt.Println()
	fmt.Println(cliStyles.Muted.Render("  This will create a sample config with sensible defaults."))
	fmt.Println(cliStyles.Muted.Render("  Edit it to add your sources, destinations, and passphrase."))
	fmt.Println()

	return nil
}

func renderStatusJSON(cfg *config.Config, state *scheduler.State, configName string) error {
	lastRun := state.LastRun(configName)
	lastStr := "never"
	if !lastRun.IsZero() {
		lastStr = lastRun.Local().Format(time.RFC3339)
	}

	status := "ok"
	overdue := false
	if cfg.Schedule != "" {
		interval, err := config.ParseSchedule(cfg.Schedule)
		if err == nil && !lastRun.IsZero() && time.Since(lastRun) > interval*2 {
			status = "overdue"
			overdue = true
		} else if lastRun.IsZero() {
			status = "never_run"
		}
	} else {
		status = "manual"
	}

	type destInfo struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Active     bool   `json:"active"`
		LastBackup string `json:"last_backup"`
	}
	var dests []destInfo
	for _, d := range cfg.Destinations {
		destLast := state.LastRun(configName + "/" + d.Name)
		destLastStr := "never"
		if !destLast.IsZero() {
			destLastStr = destLast.Local().Format(time.RFC3339)
		}
		dests = append(dests, destInfo{
			Name:       d.Name,
			Type:       d.Type,
			Active:     d.IsActive(),
			LastBackup: destLastStr,
		})
	}

	type statusResultJSON struct {
		Sources      []string   `json:"sources"`
		Schedule     string     `json:"schedule"`
		Destinations []destInfo `json:"destinations"`
		LastBackup   string     `json:"last_backup"`
		Status       string     `json:"status"`
		Overdue      bool       `json:"overdue"`
	}
	out := statusResultJSON{
		Sources:      cfg.SourcePaths(),
		Schedule:     cfg.Schedule,
		Destinations: dests,
		LastBackup:   lastStr,
		Status:       status,
		Overdue:      overdue,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func renderStatusDashboard(cfg *config.Config, state *scheduler.State, configName, cfgPath string) error {
	// Greeting
	if !flagNoWhimsy && !flagQuiet {
		fmt.Println()
		fmt.Println("  " + cliStyles.Tagline.Render(whimsy.Greeting()))
	}

	// Validation warnings
	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Println()
		for _, e := range errs {
			fmt.Println("  " + cliStyles.Warning.Render("! ") + cliStyles.Muted.Render(e.Error()))
		}
	}

	// ── Configuration ──
	fmt.Println()
	fmt.Println(sectionHeader("Configuration"))
	fmt.Println(kv("Config:", cfgPath))

	keyType := describeKeyType(cfg.Key)
	fmt.Println(kv("Key:", keyType))

	schedule := cfg.Schedule
	if schedule == "" {
		schedule = cliStyles.Muted.Render("manual")
	}
	fmt.Println(kv("Schedule:", schedule))

	cacheDir := config.ExpandPath(cfg.Settings.CacheDir)
	if cacheDir == "" {
		cacheDir = cliStyles.Muted.Render("none")
	}
	fmt.Println(kv("Cache:", cacheDir))

	fmt.Println(kv("Compression:", fmt.Sprintf("%s (level %d)", cfg.Settings.Compression, cfg.Settings.CompressionLevel)))

	// ── Sources ──
	fmt.Println()
	fmt.Println(sectionHeader("Sources"))
	if len(cfg.Sources) == 0 {
		fmt.Println("  " + cliStyles.Warning.Render("No sources configured"))
	} else {
		for _, src := range cfg.Sources {
			path := config.ExpandPath(src.Path)
			exists := pathExists(path)
			dot := statusDot(exists)
			extra := ""
			if len(src.Exclude) > 0 {
				extra = cliStyles.Muted.Render(fmt.Sprintf(" (excludes: %s)", strings.Join(src.Exclude, ", ")))
			}
			if !exists {
				extra += " " + cliStyles.Warning.Render("(not found)")
			}
			fmt.Printf("  %s %s%s\n", dot, path, extra)
		}
	}
	if len(cfg.Exclude) > 0 {
		fmt.Println(kv("Global excludes:", strings.Join(cfg.Exclude, ", ")))
	}

	// ── Destinations ──
	fmt.Println()
	fmt.Println(sectionHeader("Destinations"))
	if len(cfg.Destinations) == 0 {
		fmt.Println("  " + cliStyles.Warning.Render("No destinations configured"))
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		for _, dest := range cfg.Destinations {
			renderDestStatus(ctx, cfg, state, configName, dest)
		}
	}

	// ── Scheduler ──
	fmt.Println()
	fmt.Println(sectionHeader("Scheduler"))

	lastRun := state.LastRun(configName)
	if lastRun.IsZero() {
		fmt.Println(kv("Last backup:", cliStyles.Warning.Render("never")))
	} else {
		ago := time.Since(lastRun).Truncate(time.Second)
		timeStr := fmt.Sprintf("%s (%s ago)", lastRun.Local().Format("2006-01-02 15:04"), ago)

		overdue := false
		if cfg.Schedule != "" {
			interval, err := config.ParseSchedule(cfg.Schedule)
			if err == nil && time.Since(lastRun) > interval*2 {
				overdue = true
			}
		}

		if overdue {
			fmt.Println(kv("Last backup:", timeStr+" "+cliStyles.Error.Render("[OVERDUE]")))
		} else {
			fmt.Println(kv("Last backup:", cliStyles.Success.Render(timeStr)))
		}
	}

	installed, schedulerType := detectSchedulerInstalled()
	if installed {
		fmt.Println(kv("Cron:", statusLabel(true, schedulerType+" installed", "")))
	} else {
		fmt.Println(kv("Cron:", cliStyles.Warning.Render("not installed")+" "+cliStyles.Muted.Render("(doomsday client cron install)")))
	}

	// ── Retention ──
	fmt.Println()
	fmt.Println(sectionHeader("Retention"))
	ret := cfg.Retention
	retParts := []string{}
	if ret.KeepLast > 0 {
		retParts = append(retParts, fmt.Sprintf("last %d", ret.KeepLast))
	}
	if ret.KeepHourly > 0 {
		retParts = append(retParts, fmt.Sprintf("%d hourly", ret.KeepHourly))
	}
	if ret.KeepDaily > 0 {
		retParts = append(retParts, fmt.Sprintf("%d daily", ret.KeepDaily))
	}
	if ret.KeepWeekly > 0 {
		retParts = append(retParts, fmt.Sprintf("%d weekly", ret.KeepWeekly))
	}
	if ret.KeepMonthly > 0 {
		retParts = append(retParts, fmt.Sprintf("%d monthly", ret.KeepMonthly))
	}
	if ret.KeepYearly == -1 {
		retParts = append(retParts, "all yearly")
	} else if ret.KeepYearly > 0 {
		retParts = append(retParts, fmt.Sprintf("%d yearly", ret.KeepYearly))
	}
	if len(retParts) == 0 {
		fmt.Println(kv("Policy:", cliStyles.Muted.Render("none configured")))
	} else {
		fmt.Println(kv("Policy:", strings.Join(retParts, ", ")))
	}

	fmt.Println()
	return nil
}

func renderDestStatus(ctx context.Context, cfg *config.Config, state *scheduler.State, configName string, dest config.DestConfig) {
	active := ""
	if !dest.IsActive() {
		active = cliStyles.Muted.Render(" (inactive)")
	}

	// Connection test.
	dc := dest
	reachable := false
	var connErr error

	backend, err := openBackend(ctx, &dc)
	if err != nil {
		connErr = err
	} else {
		connErr = backend.List(ctx, types.FileTypeConfig, func(fi types.FileInfo) error {
			return nil
		})
		backend.Close()
		if connErr == nil {
			reachable = true
		}
	}

	dot := statusDot(reachable)
	typeStr := cliStyles.Muted.Render("[" + dest.Type + "]")

	// Per-destination last backup.
	destLast := state.LastRun(configName + "/" + dest.Name)
	var lastStr string
	if destLast.IsZero() {
		lastStr = cliStyles.Muted.Render("never")
	} else {
		ago := time.Since(destLast).Truncate(time.Second)
		lastStr = fmt.Sprintf("%s (%s ago)", destLast.Local().Format("2006-01-02 15:04"), ago)
	}

	fmt.Printf("  %s %s %s%s\n", dot, cliStyles.Value.Render(dest.Name), typeStr, active)

	destDetail := destLocation(dest)
	if destDetail != "" {
		fmt.Printf("      %s\n", cliStyles.Muted.Render(destDetail))
	}
	fmt.Printf("      Last backup: %s\n", lastStr)

	if !reachable && connErr != nil {
		fmt.Printf("      %s\n", cliStyles.Error.Render(connErr.Error()))
	}

	// Per-dest schedule/retention overrides.
	if dest.Schedule != "" {
		fmt.Printf("      Schedule: %s %s\n", dest.Schedule, cliStyles.Muted.Render("(override)"))
	}
}

// destLocation returns a human-readable location string for a destination.
func destLocation(dest config.DestConfig) string {
	switch dest.Type {
	case "sftp":
		port := dest.Port
		if port == 0 {
			port = 22
		}
		return fmt.Sprintf("%s@%s:%d", dest.User, dest.Host, port)
	case "s3":
		return fmt.Sprintf("%s/%s", dest.Endpoint, dest.Bucket)
	case "local":
		return config.ExpandPath(dest.Path)
	default:
		return ""
	}
}

// describeKeyType returns a human-readable description of the key configuration.
func describeKeyType(key string) string {
	if key == "" {
		return cliStyles.Error.Render("not set")
	}
	if strings.HasPrefix(key, "env:") {
		return fmt.Sprintf("env:%s", key[4:])
	}
	if strings.HasPrefix(key, "file:") {
		return fmt.Sprintf("file:%s", key[5:])
	}
	if strings.HasPrefix(key, "cmd:") {
		return "cmd:..."
	}
	// Literal passphrase — don't show the value.
	return "passphrase " + cliStyles.Muted.Render("(literal)")
}

// pathExists checks if a path exists on the filesystem.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// detectSchedulerInstalled checks for platform-specific scheduler files.
func detectSchedulerInstalled() (bool, string) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return false, ""
		}
		plistPath := home + "/Library/LaunchAgents/com.doomsday.cron.plist"
		if _, err := os.Stat(plistPath); err == nil {
			return true, "launchd"
		}
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			return false, ""
		}
		timerPath := home + "/.config/systemd/user/doomsday.timer"
		if _, err := os.Stat(timerPath); err == nil {
			return true, "systemd"
		}
	}
	return false, ""
}
