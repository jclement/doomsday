package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/backup"
	"github.com/jclement/doomsday/internal/backend/s3"
	sftpbackend "github.com/jclement/doomsday/internal/backend/sftp"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/notify"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/types"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// openMasterKey resolves the master key from the config's key field.
//
// Resolution order:
//  1. file: → read as key file (v1 encrypted or v2 plaintext)
//  2. env:/cmd:/literal → derive master key from passphrase via scrypt
//
// Any string value (passphrase, password, hex, whatever) is run through
// scrypt to produce a deterministic 256-bit master key.
func openMasterKey(cfg *config.Config) (crypto.MasterKey, error) {
	var masterKey crypto.MasterKey

	// Try DOOMSDAY_KEY env override first.
	resolved, err := config.ResolveKey(cfg)
	if err != nil {
		return masterKey, fmt.Errorf("resolve key: %w", err)
	}

	// file: → read the file as a key file (not passphrase).
	if strings.HasPrefix(cfg.Key, "file:") {
		keyFilePath := config.ExpandPath(cfg.Key[5:])
		return openKeyFile(keyFilePath)
	}

	// Everything else (env:, cmd:, literal) is a passphrase → scrypt.
	return crypto.DeriveKeyFromPassphrase(resolved)
}

// openKeyFile reads and decrypts a key file (v1 password-protected or v2 plaintext).
func openKeyFile(path string) (crypto.MasterKey, error) {
	var masterKey crypto.MasterKey

	data, err := os.ReadFile(path)
	if err != nil {
		return masterKey, fmt.Errorf("read key file %s: %w", path, err)
	}

	kf, err := crypto.UnmarshalKeyFile(data)
	if err != nil {
		return masterKey, fmt.Errorf("parse key file: %w", err)
	}

	// Version 2 (plaintext): no password needed.
	if kf.IsPlaintext() {
		return crypto.OpenKeyFile(kf, nil)
	}

	// Version 1 (encrypted): try env var first, then prompt.
	if envPw := os.Getenv("DOOMSDAY_PASSWORD"); envPw != "" {
		return crypto.OpenKeyFile(kf, []byte(envPw))
	}

	password, err := promptPassword("Enter repository password: ")
	if err != nil {
		return masterKey, fmt.Errorf("read password: %w", err)
	}

	return crypto.OpenKeyFile(kf, password)
}

// openBackend creates a backend from a destination config.
func openBackend(ctx context.Context, dest *config.DestConfig) (types.Backend, error) {
	if err := config.ResolveDestSecrets(dest, dest.Name); err != nil {
		return nil, fmt.Errorf("resolve secrets for %s: %w", dest.Name, err)
	}

	switch dest.Type {
	case "local":
		path := config.ExpandPath(dest.Path)
		return local.New(path)

	case "sftp":
		port := strconv.Itoa(dest.Port)
		if dest.Port == 0 {
			port = "22"
		}

		// Host key verification: prefer pinned fingerprint, fall back to known_hosts.
		var hostKeyCallback ssh.HostKeyCallback
		if dest.HostKey != "" {
			cb, err := sftpbackend.HostKeyFingerprint(dest.HostKey)
			if err != nil {
				return nil, fmt.Errorf("host key for %s: %w", dest.Name, err)
			}
			hostKeyCallback = cb
		} else {
			// Fall back to system known_hosts.
			home, _ := os.UserHomeDir()
			knownHostsPath := home + "/.ssh/known_hosts"
			cb, err := sftpbackend.HostKeyFile(knownHostsPath)
			if err != nil {
				return nil, fmt.Errorf("load known_hosts for %s: %w (set host_key in destination config)", dest.Name, err)
			}
			hostKeyCallback = cb
		}

		return sftpbackend.New(
			dest.Host,
			port,
			dest.User,
			dest.BasePath,
			config.ExpandPath(dest.KeyFile),
			dest.Password,
			dest.SSHKey,
			hostKeyCallback,
		)

	case "s3":
		useSSL := true
		return s3.New(
			dest.Endpoint,
			dest.Bucket,
			"", // prefix is no longer per-backup-ref
			dest.KeyID,
			dest.SecretKey,
			useSSL,
		)

	default:
		return nil, fmt.Errorf("unknown destination type %q", dest.Type)
	}
}

// openRepo opens an existing repository with the given backend and master key.
// If cacheDir is non-empty, local index caching is enabled.
func openRepo(ctx context.Context, backend types.Backend, masterKey crypto.MasterKey, cacheDir string) (*repo.Repository, error) {
	var opts []repo.Option
	if cacheDir != "" {
		opts = append(opts, repo.WithCacheDir(cacheDir))
	}
	return repo.Open(ctx, backend, masterKey, opts...)
}

// promptPassword reads a password from the terminal with echo disabled.
func promptPassword(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // newline after password input
	if err != nil {
		return nil, err
	}
	return password, nil
}

// resolveSnapshotID resolves a snapshot ID that may be a prefix or "latest".
// configName is used for "latest" resolution — pass "" to match any.
func resolveSnapshotID(ctx context.Context, r *repo.Repository, configName, idOrPrefix string) (string, error) {
	if idOrPrefix == "latest" {
		return resolveLatestSnapshot(ctx, r, configName)
	}

	ids, err := r.ListSnapshots(ctx)
	if err != nil {
		return "", fmt.Errorf("list snapshots: %w", err)
	}

	// Try exact match first.
	for _, id := range ids {
		if id == idOrPrefix {
			return id, nil
		}
	}

	// Try prefix match.
	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(id, idOrPrefix) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("snapshot %q not found", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("snapshot prefix %q is ambiguous (%d matches)", idOrPrefix, len(matches))
	}
}

// resolveLatestSnapshot finds the most recent snapshot ID.
// If configName is non-empty, only snapshots matching that config are considered.
func resolveLatestSnapshot(ctx context.Context, r *repo.Repository, configName string) (string, error) {
	ids, err := r.ListSnapshots(ctx)
	if err != nil {
		return "", fmt.Errorf("list snapshots: %w", err)
	}

	var matching []*snapshot.Snapshot
	for _, id := range ids {
		snap, err := r.LoadSnapshot(ctx, id)
		if err != nil {
			continue
		}
		if configName == "" || snap.BackupConfigName == configName {
			matching = append(matching, snap)
		}
	}

	if len(matching) == 0 {
		if configName != "" {
			return "", fmt.Errorf("no snapshots found for config %q", configName)
		}
		return "", fmt.Errorf("no snapshots found")
	}

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Time.After(matching[j].Time)
	})

	return matching[0].ID, nil
}

// loadConfig loads the doomsday client configuration.
func loadConfig() (*config.Config, error) {
	cfgPath := flagConfig
	if cfgPath == "" {
		cfgPath = config.DefaultClientConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

// loadAndValidateConfig loads config and validates it.
func loadAndValidateConfig() (*config.Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			logger.Error("Config validation", "error", e)
		}
		return nil, fmt.Errorf("invalid configuration (%d errors)", len(errs))
	}
	return cfg, nil
}

// firstDest returns the first active destination config.
// Most read operations only need one destination to access the repo.
func firstDest(cfg *config.Config) (*config.DestConfig, error) {
	active := cfg.ActiveDestinations()
	if len(active) == 0 {
		if len(cfg.Destinations) == 0 {
			return nil, fmt.Errorf("no destinations configured")
		}
		// Fall back to first destination even if inactive.
		return &cfg.Destinations[0], nil
	}
	return &active[0], nil
}

// buildNotifier constructs a Notifier from the config's notification targets.
// Returns nil if no targets are configured or policy is "never".
func buildNotifier(cfg *config.Config) notify.Notifier {
	nc := cfg.Notifications
	if nc.Policy == "never" || len(nc.Targets) == 0 {
		return nil
	}

	var notifiers []notify.Notifier
	for _, tgt := range nc.Targets {
		switch tgt.Type {
		case "command":
			notifiers = append(notifiers, notify.NewCommandNotifier(tgt.Command))
		case "webhook":
			notifiers = append(notifiers, notify.NewWebhookNotifier(tgt.URL, tgt.Method, tgt.Template))
		}
	}

	if len(notifiers) == 0 {
		return nil
	}
	if len(notifiers) == 1 {
		return notifiers[0]
	}
	return &notify.Multi{Notifiers: notifiers}
}

// sendNotification dispatches a notification event if the notifier and policy allow it.
// Logs errors but does not return them — notifications are best-effort.
func sendNotification(ctx context.Context, n notify.Notifier, policy string, event notify.Event) {
	if n == nil {
		return
	}
	if !notify.ShouldNotify(notify.Policy(policy), event) {
		return
	}
	if err := n.Send(ctx, event); err != nil {
		logger.Warn("Notification failed", "error", err)
	}
}

// backupConfigName returns a name to use for the backup config.
// Uses hostname since there are no named backup sets anymore.
func backupConfigName() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "default"
	}
	return hostname
}

// buildPerSource builds a per-source config map from the config's Sources.
// Keys are cleaned absolute paths. Per-source excludes and OneFilesystem are included.
func buildPerSource(cfg *config.Config) map[string]backup.SourceOptions {
	if len(cfg.Sources) == 0 {
		return nil
	}
	perSource := make(map[string]backup.SourceOptions, len(cfg.Sources))
	for _, src := range cfg.Sources {
		path := filepath.Clean(config.ExpandPath(src.Path))
		perSource[path] = backup.SourceOptions{
			Excludes:      src.Exclude,
			OneFilesystem: src.OneFilesystem,
		}
	}
	return perSource
}

// clientConfigPath returns the resolved client config path.
func clientConfigPath() string {
	if flagConfig != "" {
		return flagConfig
	}
	return config.DefaultClientConfigPath()
}

// exactArgs returns a cobra.PositionalArgs that requires exactly n args,
// showing the command's usage line instead of a generic error.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == n {
			return nil
		}
		return fmt.Errorf("Usage: %s\n\nRun '%s --help' for more information", cmd.UseLine(), cmd.CommandPath())
	}
}

// minArgs returns a cobra.PositionalArgs that requires at least n args.
func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			return nil
		}
		return fmt.Errorf("Usage: %s\n\nRun '%s --help' for more information", cmd.UseLine(), cmd.CommandPath())
	}
}

// maxArgs returns a cobra.PositionalArgs that allows at most n args.
func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) <= n {
			return nil
		}
		return fmt.Errorf("too many arguments\n\nUsage: %s\n\nRun '%s --help' for more information", cmd.UseLine(), cmd.CommandPath())
	}
}
