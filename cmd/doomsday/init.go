package main

import (
	"fmt"
	"os"

	"github.com/jclement/doomsday/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a starter configuration file",
	Long: `Create a starter configuration file at ~/.config/doomsday/client.yaml.

The generated config includes reasonable defaults for schedule and retention.
Edit it to add your encryption key, sources, and destinations.

The encryption key must be set before any operations will work.
Any string works as a passphrase — it is run through scrypt to derive the key.`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	configDir := config.DefaultConfigDir()
	configFilePath := clientConfigPath()

	if _, err := os.Stat(configFilePath); err == nil {
		return fmt.Errorf("config already exists at %s", configFilePath)
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	content := generateDefaultConfig()

	if err := os.WriteFile(configFilePath, []byte(content), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	logger.Info("Config created", "path", configFilePath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Set your encryption key (any passphrase works)")
	fmt.Println("  2. Add sources and destinations to the config")
	fmt.Println("  3. Run first backup:  doomsday client backup")
	fmt.Println()
	fmt.Println("Edit the config:")
	fmt.Printf("  %s\n", configFilePath)

	return nil
}

func generateDefaultConfig() string {
	return `# Doomsday — backup for the end of the world
# https://github.com/jclement/doomsday

# Encryption key (REQUIRED before any operations)
# Any string works as a passphrase — it is run through scrypt to derive the key.
# Supports: literal passphrase, env:VAR, file:path (key file), cmd:command
# key: my secret passphrase
# key: env:DOOMSDAY_ENCRYPTION_KEY

# ── Sources ──────────────────────────────────────────
sources:
  # - path: ~/Documents
  # - path: ~/Projects
  #   exclude: [node_modules, .git, vendor]

# Global excludes (applied to ALL sources)
exclude:
  - .cache
  - "*.tmp"
  - .Trash

# ── Schedule & Retention (defaults) ──────────────────
schedule: hourly

retention:
  keep_last: 5
  keep_hourly: 24
  keep_daily: 7
  keep_weekly: 4
  keep_monthly: 12
  keep_yearly: -1       # -1 = forever

# ── Destinations ─────────────────────────────────────
# Each destination can override schedule/retention.
# Set active: false for manual-only destinations.
destinations:
  # --- Doomsday Server (SFTP) ---
  # - name: server
  #   type: sftp
  #   host: backup.example.com
  #   port: 8420
  #   user: laptop
  #   ssh_key: "base64-ed25519-key"    # from: doomsday server client add
  #   host_key: "SHA256:xxxx"
  #   schedule: 4h                     # override global
  #   retention:
  #     keep_daily: 30

  # --- Generic SFTP ---
  # - name: nas
  #   type: sftp
  #   host: nas.local
  #   user: backup
  #   key_file: ~/.ssh/id_ed25519      # or use SSH agent
  #   # host_key: "SHA256:yyyy"        # pin host key (optional)

  # --- Backblaze B2 (S3-compatible) ---
  # - name: b2
  #   type: s3
  #   endpoint: s3.us-west-004.backblazeb2.com
  #   bucket: my-doomsday-backups
  #   key_id: env:B2_KEY_ID
  #   secret_key: env:B2_APP_KEY

  # --- Local / USB drive ---
  # - name: usb
  #   type: local
  #   path: /mnt/usb-backup
  #   active: false                    # only with: doomsday client backup usb
  #   schedule: weekly

# ── Settings ─────────────────────────────────────────
settings:
  compression: zstd       # zstd | none
  compression_level: 3
  # cache_dir: ~/.cache/doomsday
  # log_level: info
  # bandwidth_upload: 10MiB/s
  # bandwidth_download: 50MiB/s

# ── Notifications ────────────────────────────────────
# notifications:
#   policy: on_failure    # always | on_failure | never
#   targets:
#     - type: command
#       command: >
#         ntfy pub
#         --title "Doomsday: backup {{.Status}}"
#         doomsday-alerts
#         "{{.Message}}"
#     - type: webhook
#       url: https://hooks.slack.com/services/XXX
`
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
