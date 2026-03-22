package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullConfigYAML is the simplified config format for roundtrip testing.
const fullConfigYAML = `
key: "file:~/.config/doomsday/master.key"

sources:
  - path: "/Users/jsc"
    one_filesystem: true
  - path: "/Users/jsc/Developer"
    exclude: ["vendor/", ".git/"]

exclude:
  - "*.tmp"
  - ".cache/"
  - "node_modules/"
  - ".Trash/"
  - "*.sock"
schedule: hourly

retention:
  keep_last: 5
  keep_hourly: 24
  keep_daily: 7
  keep_weekly: 4
  keep_monthly: 12
  keep_yearly: -1

destinations:
  - name: nas
    type: sftp
    host: nas.local
    port: 8420
    user: laptop
    ssh_key: "base64-ed25519-key-here"
    host_key: "SHA256:abc123"
  - name: b2
    type: s3
    endpoint: "https://s3.us-west-004.backblazeb2.com"
    key_id: "env:DOOMSDAY_B2_KEY_ID"
    secret_key: "env:DOOMSDAY_B2_APP_KEY"
    bucket: jsc-doomsday-backups
  - name: local-drive
    type: local
    path: /mnt/backup-drive
  - name: usb
    type: local
    path: /mnt/usb
    active: false

settings:
  compression: zstd
  compression_level: 3
  log_level: info
  whimsy: true

notifications:
  policy: on_failure
  escalate_after: "3d"
  targets:
    - type: command
      command: "ntfy pub --title 'Doomsday backup failed' doomsday-alerts"
`

func TestParse_FullConfig(t *testing.T) {
	cfg, err := Parse([]byte(fullConfigYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Key
	if cfg.Key != "file:~/.config/doomsday/master.key" {
		t.Errorf("key = %q", cfg.Key)
	}

	// Sources
	if len(cfg.Sources) != 2 || cfg.Sources[0].Path != "/Users/jsc" {
		t.Errorf("sources = %v", cfg.Sources)
	}
	if !cfg.Sources[0].OneFilesystem {
		t.Error("sources[0].one_filesystem should be true")
	}
	if len(cfg.Sources[1].Exclude) != 2 {
		t.Errorf("sources[1].exclude len = %d, want 2", len(cfg.Sources[1].Exclude))
	}

	// SourcePaths helper
	paths := cfg.SourcePaths()
	if len(paths) != 2 || paths[0] != "/Users/jsc" {
		t.Errorf("SourcePaths() = %v", paths)
	}

	// Exclude
	if len(cfg.Exclude) != 5 {
		t.Errorf("exclude len = %d, want 5", len(cfg.Exclude))
	}

	// Schedule
	if cfg.Schedule != "hourly" {
		t.Errorf("schedule = %q", cfg.Schedule)
	}

	// Settings
	if cfg.Settings.Compression != "zstd" {
		t.Errorf("settings.compression = %q", cfg.Settings.Compression)
	}
	if cfg.Settings.CompressionLevel != 3 {
		t.Errorf("settings.compression_level = %d", cfg.Settings.CompressionLevel)
	}
	if cfg.Settings.LogLevel != "info" {
		t.Errorf("settings.log_level = %q", cfg.Settings.LogLevel)
	}
	if !cfg.WhimsyEnabled() {
		t.Error("whimsy should be enabled")
	}

	// Destinations
	if len(cfg.Destinations) != 4 {
		t.Fatalf("expected 4 destinations, got %d", len(cfg.Destinations))
	}

	nas := cfg.Destinations[0]
	if nas.Name != "nas" || nas.Type != "sftp" || nas.Host != "nas.local" || nas.Port != 8420 || nas.User != "laptop" {
		t.Errorf("nas destination = %+v", nas)
	}
	if nas.SSHKey != "base64-ed25519-key-here" {
		t.Errorf("nas.ssh_key = %q", nas.SSHKey)
	}
	if nas.HostKey != "SHA256:abc123" {
		t.Errorf("nas.host_key = %q", nas.HostKey)
	}
	if !nas.IsActive() {
		t.Error("nas should be active by default")
	}

	b2 := cfg.Destinations[1]
	if b2.Name != "b2" || b2.Type != "s3" || b2.Bucket != "jsc-doomsday-backups" {
		t.Errorf("b2 destination = %+v", b2)
	}
	if b2.KeyID != "env:DOOMSDAY_B2_KEY_ID" {
		t.Errorf("b2.key_id = %q, want env:DOOMSDAY_B2_KEY_ID", b2.KeyID)
	}

	local := cfg.Destinations[2]
	if local.Name != "local-drive" || local.Type != "local" || local.Path != "/mnt/backup-drive" {
		t.Errorf("local-drive destination = %+v", local)
	}
	if !local.IsActive() {
		t.Error("local-drive should be active by default")
	}

	usb := cfg.Destinations[3]
	if usb.Name != "usb" || usb.IsActive() {
		t.Errorf("usb should be inactive, got active=%v name=%q", usb.IsActive(), usb.Name)
	}

	// Active destinations should exclude usb
	active := cfg.ActiveDestinations()
	if len(active) != 3 {
		t.Errorf("expected 3 active destinations, got %d", len(active))
	}

	// Retention
	if cfg.Retention.KeepLast != 5 {
		t.Errorf("retention.keep_last = %d", cfg.Retention.KeepLast)
	}
	if cfg.Retention.KeepYearly != -1 {
		t.Errorf("retention.keep_yearly = %d, want -1", cfg.Retention.KeepYearly)
	}

	// Notifications
	if cfg.Notifications.Policy != "on_failure" {
		t.Errorf("notifications.policy = %q", cfg.Notifications.Policy)
	}
	if cfg.Notifications.EscalateAfter != "3d" {
		t.Errorf("notifications.escalate_after = %q", cfg.Notifications.EscalateAfter)
	}
	if len(cfg.Notifications.Targets) != 1 {
		t.Fatalf("expected 1 notification target, got %d", len(cfg.Notifications.Targets))
	}
	if cfg.Notifications.Targets[0].Type != "command" {
		t.Errorf("notifications.target[0].type = %q", cfg.Notifications.Targets[0].Type)
	}
}

func TestParse_Defaults(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp/data
destinations:
  - name: d
    type: local
    path: /tmp/backups
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if cfg.Settings.Compression != "zstd" {
		t.Errorf("default compression = %q, want zstd", cfg.Settings.Compression)
	}
	if cfg.Settings.CompressionLevel != 3 {
		t.Errorf("default compression_level = %d, want 3", cfg.Settings.CompressionLevel)
	}
	if cfg.Settings.LogLevel != "info" {
		t.Errorf("default log_level = %q, want info", cfg.Settings.LogLevel)
	}
	if !cfg.WhimsyEnabled() {
		t.Error("whimsy should default to enabled")
	}
	if cfg.Notifications.Policy != "on_failure" {
		t.Errorf("default notifications.policy = %q, want on_failure", cfg.Notifications.Policy)
	}
}

func TestParse_WhimsyDisabled(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp
settings:
  whimsy: false
destinations:
  - name: d
    type: local
    path: /tmp
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WhimsyEnabled() {
		t.Error("whimsy should be disabled when explicitly set to false")
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	_, err := Parse([]byte("key: [[[not valid yaml"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg, err := Parse([]byte(fullConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	if len(errs) != 0 {
		t.Errorf("expected no validation errors, got %d:", len(errs))
		for _, e := range errs {
			t.Errorf("  - %v", e)
		}
	}
}

func TestValidate_MissingKey(t *testing.T) {
	yamlData := `
sources:
  - /tmp
destinations:
  - name: d
    type: local
    path: /tmp
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "key is required") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for missing key")
	}
}

func TestValidate_MissingSources(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
destinations:
  - name: d
    type: local
    path: /tmp
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "sources must not be empty") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for missing sources")
	}
}

func TestValidate_NoDestinations(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "at least one destination is required") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for no destinations")
	}
}

func TestValidate_DuplicateDestinationNames(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: dupe
    type: local
    path: /a
  - name: dupe
    type: local
    path: /b
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "duplicate destination name") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for duplicate destination names")
	}
}

func TestValidate_InvalidCompression(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp
settings:
  compression: lz4
destinations:
  - name: d
    type: local
    path: /tmp
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "compression") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for invalid compression")
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp
settings:
  log_level: trace
destinations:
  - name: d
    type: local
    path: /tmp
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "log_level") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for invalid log_level")
	}
}

func TestValidate_DestinationTypes(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError string
	}{
		{
			name: "sftp missing host",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: bad
    type: sftp
    user: backup
`,
			wantError: "sftp requires host",
		},
		{
			name: "sftp missing user",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: bad
    type: sftp
    host: example.com
`,
			wantError: "sftp requires user",
		},
		{
			name: "s3 missing bucket",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: bad
    type: s3
    endpoint: "https://s3.example.com"
    key_id: id
    secret_key: secret
`,
			wantError: "s3 requires bucket",
		},
		{
			name: "local missing path",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: bad
    type: local
`,
			wantError: "local requires path",
		},
		{
			name: "unknown type",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: bad
    type: ftp
`,
			wantError: "unknown type",
		},
		{
			name: "missing type",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: bad
    host: example.com
`,
			wantError: "type is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			errs := cfg.Validate()
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tt.wantError) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected validation error containing %q, got %v", tt.wantError, errs)
			}
		})
	}
}

func TestValidate_InvalidRetention(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp
retention:
  keep_daily: -5
destinations:
  - name: d
    type: local
    path: /tmp
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "keep_daily") && strings.Contains(e.Error(), ">= -1") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for invalid retention value")
	}
}

func TestValidate_NotificationTargets(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError string
	}{
		{
			name: "command missing command field",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: d
    type: local
    path: /tmp
notifications:
  policy: always
  targets:
    - type: command
`,
			wantError: "requires command field",
		},
		{
			name: "webhook missing url",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: d
    type: local
    path: /tmp
notifications:
  targets:
    - type: webhook
`,
			wantError: "requires url field",
		},
		{
			name: "unknown notification type",
			yaml: `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: d
    type: local
    path: /tmp
notifications:
  targets:
    - type: email
`,
			wantError: "unknown type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			errs := cfg.Validate()
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tt.wantError) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected validation error containing %q, got %v", tt.wantError, errs)
			}
		})
	}
}

func TestValidate_InvalidNotificationPolicy(t *testing.T) {
	yamlData := `
key: "file:/tmp/key"
sources:
  - /tmp
destinations:
  - name: d
    type: local
    path: /tmp
notifications:
  policy: sometimes
`
	cfg, err := Parse([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "notifications.policy") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for invalid notification policy")
	}
}

func TestLoad_FileFromDisk(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(cfgPath, []byte(fullConfigYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Key != "file:~/.config/doomsday/master.key" {
		t.Errorf("unexpected key = %q", cfg.Key)
	}
	if len(cfg.Destinations) != 4 {
		t.Errorf("expected 4 destinations, got %d", len(cfg.Destinations))
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/client.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFindDestination(t *testing.T) {
	cfg, err := Parse([]byte(fullConfigYAML))
	if err != nil {
		t.Fatal(err)
	}

	dest, err := cfg.FindDestination("nas")
	if err != nil {
		t.Fatalf("FindDestination(nas): %v", err)
	}
	if dest.Name != "nas" {
		t.Errorf("FindDestination returned wrong dest: %q", dest.Name)
	}

	_, err = cfg.FindDestination("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent destination name")
	}
}

func TestActiveDestinations(t *testing.T) {
	cfg, err := Parse([]byte(fullConfigYAML))
	if err != nil {
		t.Fatal(err)
	}

	active := cfg.ActiveDestinations()
	if len(active) != 3 {
		t.Fatalf("expected 3 active destinations, got %d", len(active))
	}

	// usb should not be in active list
	for _, d := range active {
		if d.Name == "usb" {
			t.Error("usb should not be in active destinations")
		}
	}
}

func TestParseSchedule(t *testing.T) {
	tests := []struct {
		input   string
		wantH   float64
		wantErr bool
	}{
		{"hourly", 1, false},
		{"daily", 24, false},
		{"weekly", 168, false},
		{"4h", 4, false},
		{"30m", 0.5, false},
		{"invalid", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := ParseSchedule(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSchedule(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				gotH := d.Hours()
				if gotH != tt.wantH {
					t.Errorf("ParseSchedule(%q) = %v hours, want %v", tt.input, gotH, tt.wantH)
				}
			}
		})
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~", home},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ExpandPath(tt.input)
			if got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Secret resolution tests ---

func TestResolveSecret_Literal(t *testing.T) {
	val, err := ResolveSecret("plain-value")
	if err != nil {
		t.Fatal(err)
	}
	if val != "plain-value" {
		t.Errorf("got %q, want %q", val, "plain-value")
	}
}

func TestResolveSecret_Empty(t *testing.T) {
	val, err := ResolveSecret("")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("got %q, want empty", val)
	}
}

func TestResolveSecret_Env(t *testing.T) {
	t.Setenv("DOOMSDAY_TEST_SECRET", "s3cr3t")

	val, err := ResolveSecret("env:DOOMSDAY_TEST_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if val != "s3cr3t" {
		t.Errorf("got %q, want %q", val, "s3cr3t")
	}
}

func TestResolveSecret_EnvNotSet(t *testing.T) {
	_, err := ResolveSecret("env:DOOMSDAY_DEFINITELY_NOT_SET_12345")
	if err == nil {
		t.Error("expected error for unset env var")
	}
}

func TestResolveSecret_EnvEmptyName(t *testing.T) {
	_, err := ResolveSecret("env:")
	if err == nil {
		t.Error("expected error for empty env var name")
	}
}

func TestResolveSecret_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}

	val, err := ResolveSecret("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	if val != "file-secret" {
		t.Errorf("got %q, want %q", val, "file-secret")
	}
}

func TestResolveSecret_FileNotFound(t *testing.T) {
	_, err := ResolveSecret("file:/nonexistent/secret.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestResolveSecret_FileEmptyPath(t *testing.T) {
	_, err := ResolveSecret("file:")
	if err == nil {
		t.Error("expected error for empty file path")
	}
}

func TestResolveSecret_Cmd(t *testing.T) {
	val, err := ResolveSecret("cmd:echo hello-from-cmd")
	if err != nil {
		t.Fatal(err)
	}
	if val != "hello-from-cmd" {
		t.Errorf("got %q, want %q", val, "hello-from-cmd")
	}
}

func TestResolveSecret_CmdFailure(t *testing.T) {
	_, err := ResolveSecret("cmd:false")
	if err == nil {
		t.Error("expected error for failing command")
	}
}

func TestResolveSecret_CmdEmpty(t *testing.T) {
	_, err := ResolveSecret("cmd:")
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestResolveSecret_CmdStripsTrailingNewlines(t *testing.T) {
	val, err := ResolveSecret("cmd:printf 'value\\n\\n'")
	if err != nil {
		t.Fatal(err)
	}
	if val != "value" {
		t.Errorf("got %q, want %q", val, "value")
	}
}

func TestResolveDestSecrets(t *testing.T) {
	t.Setenv("TEST_KEY_ID", "my-key-id")
	t.Setenv("TEST_SECRET", "my-secret")

	dest := &DestConfig{
		Name:      "test",
		Type:      "s3",
		Endpoint:  "https://s3.example.com",
		KeyID:     "env:TEST_KEY_ID",
		SecretKey: "env:TEST_SECRET",
		Bucket:    "test-bucket",
	}

	if err := ResolveDestSecrets(dest, "test"); err != nil {
		t.Fatal(err)
	}

	if dest.KeyID != "my-key-id" {
		t.Errorf("key_id = %q, want %q", dest.KeyID, "my-key-id")
	}
	if dest.SecretKey != "my-secret" {
		t.Errorf("secret_key = %q, want %q", dest.SecretKey, "my-secret")
	}
}

func TestResolveDestSecrets_Error(t *testing.T) {
	dest := &DestConfig{
		Name:      "test",
		Type:      "s3",
		KeyID:     "env:DOOMSDAY_DEFINITELY_NOT_SET_67890",
		SecretKey: "literal-ok",
	}
	err := ResolveDestSecrets(dest, "test")
	if err == nil {
		t.Error("expected error when env var is missing")
	}
}

func TestResolveDestSecrets_EnvOverride(t *testing.T) {
	t.Setenv("DOOMSDAY_MY_S3_KEY_ID", "override-key")
	t.Setenv("DOOMSDAY_MY_S3_SECRET_KEY", "override-secret")

	dest := &DestConfig{
		Name:      "my-s3",
		Type:      "s3",
		KeyID:     "original-key",
		SecretKey: "original-secret",
	}
	if err := ResolveDestSecrets(dest, "my-s3"); err != nil {
		t.Fatal(err)
	}
	if dest.KeyID != "override-key" {
		t.Errorf("key_id = %q, want %q", dest.KeyID, "override-key")
	}
	if dest.SecretKey != "override-secret" {
		t.Errorf("secret_key = %q, want %q", dest.SecretKey, "override-secret")
	}
}

func TestResolveKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	if err := os.WriteFile(keyPath, []byte("deadbeef\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Key: "file:" + keyPath}
	val, err := ResolveKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if val != "deadbeef" {
		t.Errorf("got %q, want %q", val, "deadbeef")
	}
}

func TestResolveKey_EnvOverride(t *testing.T) {
	t.Setenv("DOOMSDAY_KEY", "env-key-value")

	cfg := &Config{Key: "file:/nonexistent"}
	val, err := ResolveKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if val != "env-key-value" {
		t.Errorf("got %q, want %q", val, "env-key-value")
	}
}

func TestResolveKey_Empty(t *testing.T) {
	cfg := &Config{}
	_, err := ResolveKey(cfg)
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestDestConfig_IsActive(t *testing.T) {
	t.Run("nil means active", func(t *testing.T) {
		d := DestConfig{Active: nil}
		if !d.IsActive() {
			t.Error("nil Active should mean active")
		}
	})

	t.Run("true means active", func(t *testing.T) {
		v := true
		d := DestConfig{Active: &v}
		if !d.IsActive() {
			t.Error("true Active should mean active")
		}
	})

	t.Run("false means inactive", func(t *testing.T) {
		v := false
		d := DestConfig{Active: &v}
		if d.IsActive() {
			t.Error("false Active should mean inactive")
		}
	})
}

// --- Server config tests ---

func TestParseServer(t *testing.T) {
	yamlData := `
data_dir: /var/lib/doomsday
host: "0.0.0.0"
port: 8420
clients:
  - name: laptop
    public_key: "ssh-ed25519 AAAA..."
    quota: 100GiB
  - name: desktop
    public_key: "ssh-ed25519 BBBB..."
`
	cfg, err := ParseServer([]byte(yamlData))
	if err != nil {
		t.Fatalf("ParseServer: %v", err)
	}

	if cfg.DataDir != "/var/lib/doomsday" {
		t.Errorf("data_dir = %q", cfg.DataDir)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("host = %q", cfg.Host)
	}
	if cfg.Port != 8420 {
		t.Errorf("port = %d", cfg.Port)
	}
	if len(cfg.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(cfg.Clients))
	}
	if cfg.Clients[0].Name != "laptop" {
		t.Errorf("client[0].name = %q", cfg.Clients[0].Name)
	}
	if cfg.Clients[0].Quota != "100GiB" {
		t.Errorf("client[0].quota = %q", cfg.Clients[0].Quota)
	}
}

func TestParseServer_Defaults(t *testing.T) {
	yamlData := `data_dir: /data`
	cfg, err := ParseServer([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("default host = %q, want 0.0.0.0", cfg.Host)
	}
	if cfg.Port != 8420 {
		t.Errorf("default port = %d, want 8420", cfg.Port)
	}
}

func TestServerConfig_Validate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg := &ServerConfig{
			DataDir: "/data",
			Host:    "0.0.0.0",
			Port:    8420,
			Clients: []ServerClientConfig{
				{Name: "laptop", PublicKey: "ssh-ed25519 AAAA..."},
			},
		}
		errs := cfg.Validate()
		if len(errs) != 0 {
			t.Errorf("expected no errors, got %v", errs)
		}
	})

	t.Run("missing data_dir", func(t *testing.T) {
		cfg := &ServerConfig{Port: 8420}
		errs := cfg.Validate()
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), "data_dir") {
				found = true
			}
		}
		if !found {
			t.Error("expected error for missing data_dir")
		}
	})

	t.Run("duplicate client names", func(t *testing.T) {
		cfg := &ServerConfig{
			DataDir: "/data",
			Port:    8420,
			Clients: []ServerClientConfig{
				{Name: "x", PublicKey: "k1"},
				{Name: "x", PublicKey: "k2"},
			},
		}
		errs := cfg.Validate()
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), "duplicate client name") {
				found = true
			}
		}
		if !found {
			t.Error("expected error for duplicate client names")
		}
	})
}

func TestServerConfig_FindClient(t *testing.T) {
	cfg := &ServerConfig{
		Clients: []ServerClientConfig{
			{Name: "laptop", PublicKey: "key1"},
			{Name: "desktop", PublicKey: "key2"},
		},
	}

	cl, err := cfg.FindClient("laptop")
	if err != nil {
		t.Fatal(err)
	}
	if cl.Name != "laptop" {
		t.Errorf("got %q", cl.Name)
	}

	_, err = cfg.FindClient("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent client")
	}
}

func TestSaveServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")

	cfg := &ServerConfig{
		DataDir: "/var/lib/doomsday",
		Host:    "0.0.0.0",
		Port:    8420,
		Clients: []ServerClientConfig{
			{Name: "laptop", PublicKey: "ssh-ed25519 AAAA...", Quota: "100GiB"},
		},
	}

	if err := SaveServer(path, cfg); err != nil {
		t.Fatal(err)
	}

	// Reload and verify
	loaded, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DataDir != "/var/lib/doomsday" {
		t.Errorf("data_dir = %q", loaded.DataDir)
	}
	if len(loaded.Clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(loaded.Clients))
	}
	if loaded.Clients[0].Name != "laptop" {
		t.Errorf("client[0].name = %q", loaded.Clients[0].Name)
	}
}

func TestParseServer_Tailscale(t *testing.T) {
	yamlData := `
data_dir: /data
tailscale_hostname: doomsday
tailscale_auth_key: tskey-auth-xxxx
`
	cfg, err := ParseServer([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TailscaleEnabled() {
		t.Error("expected TailscaleEnabled() = true")
	}
	if cfg.TailscaleHostname != "doomsday" {
		t.Errorf("tailscale_hostname = %q", cfg.TailscaleHostname)
	}
	if cfg.TailscaleAuthKey != "tskey-auth-xxxx" {
		t.Errorf("tailscale_auth_key = %q", cfg.TailscaleAuthKey)
	}
}

func TestParseServer_TailscaleDisabled(t *testing.T) {
	yamlData := `data_dir: /data`
	cfg, err := ParseServer([]byte(yamlData))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TailscaleEnabled() {
		t.Error("expected TailscaleEnabled() = false when hostname not set")
	}
}
