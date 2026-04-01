// Package config handles YAML configuration loading, validation, and secret resolution
// for doomsday. Uses gopkg.in/yaml.v3 directly -- no Viper.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level doomsday client configuration.
// One config file = one backup. Use -c flag for separate backups.
type Config struct {
	Key           string              `yaml:"key"`                     // encryption key (hex, env:, file:, cmd:)
	Sources       []SourceConfig      `yaml:"sources"`                 // directories to back up
	Exclude       []string            `yaml:"exclude,omitempty"`       // global glob patterns to exclude (applied to ALL sources)
	Destinations  []DestConfig        `yaml:"destinations"`            // where to back up to
	Schedule      string              `yaml:"schedule,omitempty"`      // default schedule: hourly | daily | weekly | 4h | etc.
	PreBackup     string              `yaml:"pre_backup,omitempty"`    // command before backup
	PostBackup    string              `yaml:"post_backup,omitempty"`   // command after backup
	Retention     RetentionConfig     `yaml:"retention"`               // default snapshot retention policy
	Settings      SettingsConfig      `yaml:"settings"`                // global settings
	Notifications NotificationsConfig `yaml:"notifications,omitempty"` // notification config
}

// SourceConfig defines a single backup source directory.
// Supports YAML unmarshaling from both a bare string ("- /path")
// and a full struct ("- path: /path\n  exclude: [...]").
type SourceConfig struct {
	Path          string   `yaml:"path"`
	Exclude       []string `yaml:"exclude,omitempty"`        // per-source excludes (merged with global)
	OneFilesystem bool     `yaml:"one_filesystem,omitempty"` // don't cross filesystem boundaries for this source
}

// UnmarshalYAML allows SourceConfig to be specified as either a bare string
// or a full object in YAML.
func (s *SourceConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try bare string first.
	var path string
	if err := unmarshal(&path); err == nil {
		s.Path = path
		return nil
	}
	// Fall back to full struct.
	type raw SourceConfig
	return unmarshal((*raw)(s))
}

// SettingsConfig holds global settings that apply to all operations.
type SettingsConfig struct {
	Compression       string `yaml:"compression"`                  // zstd | none
	CompressionLevel  int    `yaml:"compression_level"`            // zstd level, default 3
	CacheDir          string `yaml:"cache_dir,omitempty"`          // default ~/.cache/doomsday
	LogLevel          string `yaml:"log_level,omitempty"`          // debug | info | warn | error
	Whimsy            *bool  `yaml:"whimsy,omitempty"`             // nil means default (true)
	BandwidthUpload   string `yaml:"bandwidth_upload,omitempty"`   // e.g. "10MiB/s", "" = unlimited
	BandwidthDownload string `yaml:"bandwidth_download,omitempty"` // e.g. "50MiB/s", "" = unlimited
}

// DestConfig defines a backup destination.
// Each destination is an independent repository with its own snapshots.
type DestConfig struct {
	Name      string           `yaml:"name"`                // unique name for this destination
	Type      string           `yaml:"type"`                // sftp | s3 | local
	Active    *bool            `yaml:"active,omitempty"`    // nil/true = included in default backup; false = only with explicit name
	Schedule  string           `yaml:"schedule,omitempty"`  // per-dest schedule override (overrides global)
	Retention *RetentionConfig `yaml:"retention,omitempty"` // per-dest retention override (overrides global)

	// SFTP fields
	Host     string `yaml:"host,omitempty"`
	Port     int    `yaml:"port,omitempty"`
	User     string `yaml:"user,omitempty"`
	SSHKey   string `yaml:"ssh_key,omitempty"`   // inline Ed25519 private key (base64, from server one-liner)
	KeyFile  string `yaml:"key_file,omitempty"`  // path to SSH key file (for generic SFTP)
	Password string `yaml:"password,omitempty"`  // SSH password auth (env:/file:/cmd: supported)
	HostKey  string `yaml:"host_key,omitempty"`  // pinned host key fingerprint (SHA256:...)
	BasePath string `yaml:"base_path,omitempty"` // remote base path

	// S3 fields
	Endpoint  string `yaml:"endpoint,omitempty"`
	KeyID     string `yaml:"key_id,omitempty"`     // env:/file:/cmd: supported
	SecretKey string `yaml:"secret_key,omitempty"` // env:/file:/cmd: supported
	Bucket    string `yaml:"bucket,omitempty"`

	// Local fields
	Path string `yaml:"path,omitempty"`
}

// RetentionConfig defines how many snapshots to keep per time bucket.
type RetentionConfig struct {
	KeepLast    int    `yaml:"keep_last,omitempty"`
	KeepHourly  int    `yaml:"keep_hourly,omitempty"`
	KeepDaily   int    `yaml:"keep_daily,omitempty"`
	KeepWeekly  int    `yaml:"keep_weekly,omitempty"`
	KeepMonthly int    `yaml:"keep_monthly,omitempty"`
	KeepYearly  int    `yaml:"keep_yearly,omitempty"` // -1 = forever
	KeepWithin  string `yaml:"keep_within,omitempty"` // e.g. "30d"
	MaxSize     string `yaml:"max_size,omitempty"`    // e.g. "500GiB"
}

// NotificationTarget defines a single notification channel.
type NotificationTarget struct {
	Type     string `yaml:"type"`              // command | webhook
	Command  string `yaml:"command,omitempty"` // for type=command
	URL      string `yaml:"url,omitempty"`     // for type=webhook
	Method   string `yaml:"method,omitempty"`  // for type=webhook (default POST)
	Template string `yaml:"template,omitempty"`
}

// NotificationsConfig controls when and how notifications are sent.
type NotificationsConfig struct {
	Policy        string               `yaml:"policy,omitempty"`         // always | on_failure | never
	EscalateAfter string               `yaml:"escalate_after,omitempty"` // e.g. "3d"
	Targets       []NotificationTarget `yaml:"targets,omitempty"`
}

// WhimsyEnabled returns whether whimsy messages are enabled.
// Defaults to true if not explicitly set in the config.
func (c *Config) WhimsyEnabled() bool {
	if c.Settings.Whimsy == nil {
		return true
	}
	return *c.Settings.Whimsy
}

// IsActive returns whether this destination is included in default backup runs.
// Defaults to true if not explicitly set.
func (d *DestConfig) IsActive() bool {
	if d.Active == nil {
		return true
	}
	return *d.Active
}

// FindDestination returns the destination config with the given name.
func (c *Config) FindDestination(name string) (*DestConfig, error) {
	for i := range c.Destinations {
		if c.Destinations[i].Name == name {
			return &c.Destinations[i], nil
		}
	}
	return nil, fmt.Errorf("config.FindDestination: destination %q not found", name)
}

// ActiveDestinations returns all destinations where active is true (or unset).
func (c *Config) ActiveDestinations() []DestConfig {
	var active []DestConfig
	for _, d := range c.Destinations {
		if d.IsActive() {
			active = append(active, d)
		}
	}
	return active
}

// SourcePaths returns a flat list of source directory paths with ~ expanded.
func (c *Config) SourcePaths() []string {
	paths := make([]string, len(c.Sources))
	for i, s := range c.Sources {
		paths[i] = ExpandPath(s.Path)
	}
	return paths
}

// MergedExcludes returns global excludes merged with per-source excludes.
func (c *Config) MergedExcludes(src SourceConfig) []string {
	if len(src.Exclude) == 0 {
		return c.Exclude
	}
	merged := make([]string, 0, len(c.Exclude)+len(src.Exclude))
	merged = append(merged, c.Exclude...)
	merged = append(merged, src.Exclude...)
	return merged
}

// AllExcludes returns global excludes merged with all per-source excludes (deduplicated).
func (c *Config) AllExcludes() []string {
	seen := make(map[string]bool)
	var result []string
	for _, e := range c.Exclude {
		if !seen[e] {
			seen[e] = true
			result = append(result, e)
		}
	}
	for _, src := range c.Sources {
		for _, e := range src.Exclude {
			if !seen[e] {
				seen[e] = true
				result = append(result, e)
			}
		}
	}
	return result
}

// EffectiveSchedule returns the schedule for a destination,
// falling back to the global schedule if the destination has no override.
func (c *Config) EffectiveSchedule(dest DestConfig) string {
	if dest.Schedule != "" {
		return dest.Schedule
	}
	return c.Schedule
}

// EffectiveRetention returns the retention policy for a destination,
// falling back to the global retention if the destination has no override.
func (c *Config) EffectiveRetention(dest DestConfig) RetentionConfig {
	if dest.Retention != nil {
		return *dest.Retention
	}
	return c.Retention
}

// DefaultConfigDir returns the default configuration directory path,
// respecting XDG_CONFIG_HOME if set.
func DefaultConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "doomsday")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "doomsday")
	}
	return filepath.Join(home, ".config", "doomsday")
}

// DefaultClientConfigPath returns the default client config path.
func DefaultClientConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "client.yaml")
}

// Save writes the client config to a YAML file.
func Save(path string, cfg *Config) error {
	path = ExpandPath(path)

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config.Save: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("config.Save: %w", err)
	}
	return nil
}

// Load reads and parses a YAML config file from the given path.
func Load(path string) (*Config, error) {
	path = ExpandPath(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config.Load: %w", err)
	}

	return Parse(data)
}

// Parse parses YAML configuration data from bytes. It applies defaults
// for unset fields but does NOT run validation -- call Validate() separately.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config.Parse: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// applyDefaults fills in default values for fields not set in the config file.
func applyDefaults(cfg *Config) {
	if cfg.Settings.Compression == "" {
		cfg.Settings.Compression = "zstd"
	}
	if cfg.Settings.CompressionLevel == 0 {
		cfg.Settings.CompressionLevel = 3
	}
	if cfg.Settings.CacheDir == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			cfg.Settings.CacheDir = filepath.Join(home, ".cache", "doomsday")
		}
	}
	if cfg.Settings.LogLevel == "" {
		cfg.Settings.LogLevel = "info"
	}
	if cfg.Notifications.Policy == "" {
		cfg.Notifications.Policy = "on_failure"
	}
}

// Validate checks the configuration for required fields and logical consistency.
// Returns a slice of all validation errors found (empty means valid).
func (c *Config) Validate() []error {
	var errs []error

	// Encryption key is required
	if c.Key == "" {
		errs = append(errs, fmt.Errorf("config.Validate: key is required (hex string, env:VAR, file:path, or cmd:command)"))
	}

	// Settings: compression
	switch c.Settings.Compression {
	case "zstd", "none":
		// valid
	default:
		errs = append(errs, fmt.Errorf("config.Validate: settings.compression must be \"zstd\" or \"none\", got %q", c.Settings.Compression))
	}

	// Settings: compression_level (zstd supports 1-19, but we allow 0 as "default")
	if c.Settings.CompressionLevel < 0 || c.Settings.CompressionLevel > 19 {
		errs = append(errs, fmt.Errorf("config.Validate: settings.compression_level must be 0-19, got %d", c.Settings.CompressionLevel))
	}

	// Settings: log_level
	switch c.Settings.LogLevel {
	case "debug", "info", "warn", "error":
		// valid
	default:
		errs = append(errs, fmt.Errorf("config.Validate: settings.log_level must be debug|info|warn|error, got %q", c.Settings.LogLevel))
	}

	// Sources
	if len(c.Sources) == 0 {
		errs = append(errs, fmt.Errorf("config.Validate: sources must not be empty"))
	}
	for i, src := range c.Sources {
		if src.Path == "" {
			errs = append(errs, fmt.Errorf("config.Validate: sources[%d].path is required", i))
		}
	}

	// Destinations
	if len(c.Destinations) == 0 {
		errs = append(errs, fmt.Errorf("config.Validate: at least one destination is required"))
	}

	seenNames := make(map[string]bool)
	for i, dest := range c.Destinations {
		if dest.Name == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destinations[%d].name is required", i))
		} else if seenNames[dest.Name] {
			errs = append(errs, fmt.Errorf("config.Validate: duplicate destination name %q", dest.Name))
		} else {
			seenNames[dest.Name] = true
		}
		errs = append(errs, validateDest(dest.Name, dest)...)
	}

	// Retention
	errs = append(errs, validateRetention(c.Retention)...)

	// Notifications: policy
	switch c.Notifications.Policy {
	case "always", "on_failure", "never":
		// valid
	default:
		errs = append(errs, fmt.Errorf("config.Validate: notifications.policy must be always|on_failure|never, got %q", c.Notifications.Policy))
	}

	// Notifications: targets
	for i, tgt := range c.Notifications.Targets {
		switch tgt.Type {
		case "command":
			if tgt.Command == "" {
				errs = append(errs, fmt.Errorf("config.Validate: notifications.targets[%d] type=command requires command field", i))
			}
		case "webhook":
			if tgt.URL == "" {
				errs = append(errs, fmt.Errorf("config.Validate: notifications.targets[%d] type=webhook requires url field", i))
			}
		default:
			errs = append(errs, fmt.Errorf("config.Validate: notifications.targets[%d] unknown type %q (must be command|webhook)", i, tgt.Type))
		}
	}

	return errs
}

// validateDest checks a destination for type-specific required fields.
func validateDest(name string, dest DestConfig) []error {
	var errs []error

	switch dest.Type {
	case "sftp":
		if dest.Host == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destination %q: sftp requires host", name))
		}
		if dest.User == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destination %q: sftp requires user", name))
		}
	case "s3":
		if dest.Endpoint == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destination %q: s3 requires endpoint", name))
		}
		if dest.Bucket == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destination %q: s3 requires bucket", name))
		}
		if dest.KeyID == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destination %q: s3 requires key_id", name))
		}
		if dest.SecretKey == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destination %q: s3 requires secret_key", name))
		}
	case "local":
		if dest.Path == "" {
			errs = append(errs, fmt.Errorf("config.Validate: destination %q: local requires path", name))
		}
	case "":
		errs = append(errs, fmt.Errorf("config.Validate: destination %q: type is required", name))
	default:
		errs = append(errs, fmt.Errorf("config.Validate: destination %q: unknown type %q (must be sftp|s3|local)", name, dest.Type))
	}

	return errs
}

// validateRetention checks retention config for logical consistency.
func validateRetention(ret RetentionConfig) []error {
	var errs []error

	// -1 means "forever", 0 means "not set", positive is a count.
	fields := map[string]int{
		"keep_last":    ret.KeepLast,
		"keep_hourly":  ret.KeepHourly,
		"keep_daily":   ret.KeepDaily,
		"keep_weekly":  ret.KeepWeekly,
		"keep_monthly": ret.KeepMonthly,
		"keep_yearly":  ret.KeepYearly,
	}

	for field, val := range fields {
		if val < -1 {
			errs = append(errs, fmt.Errorf("config.Validate: retention.%s must be >= -1, got %d", field, val))
		}
	}

	return errs
}

// ExpandPath replaces a leading ~ with the user's home directory.
func ExpandPath(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// ParseSchedule parses schedule strings like "hourly", "4h", "daily", "weekly".
func ParseSchedule(s string) (time.Duration, error) {
	switch s {
	case "hourly":
		return time.Hour, nil
	case "daily":
		return 24 * time.Hour, nil
	case "weekly":
		return 7 * 24 * time.Hour, nil
	default:
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("config.ParseSchedule: %w", err)
		}
		return d, nil
	}
}
