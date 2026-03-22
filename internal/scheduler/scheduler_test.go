package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/config"
)

// ---------------------------------------------------------------------------
// State persistence tests
// ---------------------------------------------------------------------------

func TestStatePersistenceRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Saving and reloading should preserve all fields.
	original := NewState()
	t1 := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	t2 := time.Date(2025, 6, 14, 8, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 6, 13, 12, 0, 0, 0, time.UTC)

	original.SetLastRun("home", t1)
	original.SetLastRun("projects", t2)
	original.SetLastPruneRun("home", t2)
	original.SetLastCheckRun("home", t3)

	if err := SaveState(path, original); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if got := loaded.LastRun("home"); !got.Equal(t1) {
		t.Errorf("LastRun(home) = %v, want %v", got, t1)
	}
	if got := loaded.LastRun("projects"); !got.Equal(t2) {
		t.Errorf("LastRun(projects) = %v, want %v", got, t2)
	}
	if got := loaded.LastPruneRun("home"); !got.Equal(t2) {
		t.Errorf("LastPruneRun(home) = %v, want %v", got, t2)
	}
	if got := loaded.LastCheckRun("home"); !got.Equal(t3) {
		t.Errorf("LastCheckRun(home) = %v, want %v", got, t3)
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState should not error on missing file: %v", err)
	}
	if state == nil {
		t.Fatal("LoadState returned nil state for missing file")
	}
	if got := state.LastRun("anything"); !got.IsZero() {
		t.Errorf("expected zero time for unknown config, got %v", got)
	}
}

func TestLoadStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadState(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSaveStateCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "state.json")

	s := NewState()
	s.SetLastRun("test", time.Now())

	if err := SaveState(path, s); err != nil {
		t.Fatalf("SaveState should create dirs: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not created: %v", err)
	}
}

func TestSaveStateAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write initial state.
	s := NewState()
	s.SetLastRun("v1", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}

	// Write updated state.
	s.SetLastRun("v2", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := SaveState(path, s); err != nil {
		t.Fatal(err)
	}

	// No temp file should linger.
	tmp := path + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("temp file should not exist after successful save")
	}

	// Verify the file is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var check State
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("state file is not valid JSON: %v", err)
	}
}

func TestStateNilMaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write a state file with explicit null maps.
	data := []byte(`{"last_backup": null, "last_prune": null, "last_check": null}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	s, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Should be able to set values without panic.
	s.SetLastRun("test", time.Now())
	s.SetLastPruneRun("test", time.Now())
	s.SetLastCheckRun("test", time.Now())
}

// ---------------------------------------------------------------------------
// IsDue / schedule parsing tests
// ---------------------------------------------------------------------------

func TestIsDue(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		lastRun  time.Time
		interval time.Duration
		want     bool
	}{
		{
			name:     "never run before",
			lastRun:  time.Time{},
			interval: time.Hour,
			want:     true,
		},
		{
			name:     "just ran",
			lastRun:  now.Add(-5 * time.Minute),
			interval: time.Hour,
			want:     false,
		},
		{
			name:     "exactly due",
			lastRun:  now.Add(-time.Hour),
			interval: time.Hour,
			want:     true,
		},
		{
			name:     "overdue",
			lastRun:  now.Add(-3 * time.Hour),
			interval: time.Hour,
			want:     true,
		},
		{
			name:     "zero interval means never due",
			lastRun:  time.Time{},
			interval: 0,
			want:     false,
		},
		{
			name:     "negative interval means never due",
			lastRun:  time.Time{},
			interval: -time.Hour,
			want:     false,
		},
		{
			name:     "daily not yet due",
			lastRun:  now.Add(-12 * time.Hour),
			interval: 24 * time.Hour,
			want:     false,
		},
		{
			name:     "daily overdue",
			lastRun:  now.Add(-25 * time.Hour),
			interval: 24 * time.Hour,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDue(tt.lastRun, tt.interval, now)
			if got != tt.want {
				t.Errorf("IsDue(%v, %v, %v) = %v, want %v", tt.lastRun, tt.interval, now, got, tt.want)
			}
		})
	}
}

func TestParseScheduleIntegration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"hourly", time.Hour, false},
		{"daily", 24 * time.Hour, false},
		{"weekly", 7 * 24 * time.Hour, false},
		{"4h", 4 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"", 0, true},
		{"bogus", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := config.ParseSchedule(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSchedule(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err == nil && d != tt.expected {
				t.Errorf("ParseSchedule(%q) = %v, want %v", tt.input, d, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Scheduler Run tests
// ---------------------------------------------------------------------------

func TestRunBackupDue(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	cfg := &config.Config{
		Schedule: "hourly",
		Sources:  []config.SourceConfig{{Path: "/home"}},
	}

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	backupRan := false

	result, err := Run(context.Background(), Options{
		Config:     cfg,
		ConfigName: "test",
		StatePath:  statePath,
		Backup: func(_ context.Context) error {
			backupRan = true
			return nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.BackupsRun != 1 {
		t.Errorf("BackupsRun = %d, want 1", result.BackupsRun)
	}
	if !backupRan {
		t.Error("backup should have run")
	}

	// Verify state was persisted.
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := state.LastRun("test"); !got.Equal(now) {
		t.Errorf("state.LastRun(test) = %v, want %v", got, now)
	}
}

func TestRunBackupNotDue(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Pre-populate state with recent run.
	state := NewState()
	state.SetLastRun("test", now.Add(-30*time.Minute)) // ran 30m ago, schedule is hourly
	if err := SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Schedule: "hourly",
		Sources:  []config.SourceConfig{{Path: "/home"}},
	}

	backupRan := false
	result, err := Run(context.Background(), Options{
		Config:     cfg,
		ConfigName: "test",
		StatePath:  statePath,
		Backup: func(_ context.Context) error {
			backupRan = true
			return nil
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.BackupsRun != 0 {
		t.Errorf("BackupsRun = %d, want 0", result.BackupsRun)
	}
	if result.BackupsSkipped != 1 {
		t.Errorf("BackupsSkipped = %d, want 1", result.BackupsSkipped)
	}
	if backupRan {
		t.Error("backup should not have run")
	}
}

func TestRunNoScheduleSkipped(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	cfg := &config.Config{
		Sources: []config.SourceConfig{{Path: "/data"}}, // no schedule
	}

	result, err := Run(context.Background(), Options{
		Config:     cfg,
		ConfigName: "test",
		StatePath:  statePath,
		Backup: func(_ context.Context) error {
			t.Error("backup should not have been called")
			return nil
		},
		Now: func() time.Time { return time.Now() },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.BackupsSkipped != 1 {
		t.Errorf("BackupsSkipped = %d, want 1", result.BackupsSkipped)
	}
}

func TestRunBackupFailure(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	cfg := &config.Config{
		Schedule: "hourly",
		Sources:  []config.SourceConfig{{Path: "/bad"}},
	}

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	result, err := Run(context.Background(), Options{
		Config:     cfg,
		ConfigName: "test",
		StatePath:  statePath,
		Backup: func(_ context.Context) error {
			return fmt.Errorf("simulated failure")
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.BackupsRun != 0 {
		t.Errorf("BackupsRun = %d, want 0", result.BackupsRun)
	}
	if len(result.Errors) != 1 {
		t.Errorf("Errors = %d, want 1", len(result.Errors))
	}
}

func TestRunAutoPruneAndCheck(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	cfg := &config.Config{
		Schedule: "hourly",
		Sources:  []config.SourceConfig{{Path: "/home"}},
	}

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	var pruned, checked bool

	result, err := Run(context.Background(), Options{
		Config:     cfg,
		ConfigName: "test",
		StatePath:  statePath,
		Backup: func(_ context.Context) error {
			return nil
		},
		Prune: func(_ context.Context) error {
			pruned = true
			return nil
		},
		Check: func(_ context.Context) error {
			checked = true
			return nil
		},
		Now:           func() time.Time { return now },
		PruneInterval: time.Hour, // short interval to ensure it triggers
		CheckInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !pruned {
		t.Error("prune was not called")
	}
	if !checked {
		t.Error("check was not called")
	}
	if result.PrunesRun != 1 {
		t.Errorf("PrunesRun = %d, want 1", result.PrunesRun)
	}
	if result.ChecksRun != 1 {
		t.Errorf("ChecksRun = %d, want 1", result.ChecksRun)
	}
}

func TestRunPruneNotDue(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Pre-populate state: prune ran recently.
	state := NewState()
	state.SetLastPruneRun("test", now.Add(-time.Hour))
	if err := SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Schedule: "hourly",
		Sources:  []config.SourceConfig{{Path: "/home"}},
	}

	var pruned bool
	result, err := Run(context.Background(), Options{
		Config:     cfg,
		ConfigName: "test",
		StatePath:  statePath,
		Backup: func(_ context.Context) error {
			return nil
		},
		Prune: func(_ context.Context) error {
			pruned = true
			return nil
		},
		Now:           func() time.Time { return now },
		PruneInterval: DefaultPruneInterval, // weekly
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if pruned {
		t.Error("prune should not have run (not due)")
	}
	if result.PrunesRun != 0 {
		t.Errorf("PrunesRun = %d, want 0", result.PrunesRun)
	}
}

func TestRunContextCancellation(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	cfg := &config.Config{
		Schedule: "hourly",
		Sources:  []config.SourceConfig{{Path: "/home"}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := Run(ctx, Options{
		Config:     cfg,
		ConfigName: "test",
		StatePath:  statePath,
		Backup: func(_ context.Context) error {
			return nil
		},
		Now: func() time.Time { return time.Now() },
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestRunMissingConfig(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Backup: func(_ context.Context) error { return nil },
	})
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestRunMissingBackupFunc(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Config: &config.Config{},
	})
	if err == nil {
		t.Fatal("expected error for nil backup func")
	}
}

// ---------------------------------------------------------------------------
// Template rendering tests
// ---------------------------------------------------------------------------

func TestRenderSystemdService(t *testing.T) {
	data := installData{
		BinaryPath: "/usr/local/bin/doomsday",
		Home:       "/home/testuser",
		LogPath:    "/home/testuser/.local/state/doomsday/cron.log",
	}

	out, err := RenderSystemdServiceFrom(data)
	if err != nil {
		t.Fatalf("RenderSystemdServiceFrom: %v", err)
	}

	tests := []struct {
		name     string
		contains string
	}{
		{"has unit section", "[Unit]"},
		{"has service section", "[Service]"},
		{"has type oneshot", "Type=oneshot"},
		{"has exec start", "ExecStart=/usr/local/bin/doomsday cron"},
		{"has home env", "HOME=/home/testuser"},
		{"has description", "Description=Doomsday Scheduled Backup"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(out, tt.contains) {
				t.Errorf("output missing %q.\nGot:\n%s", tt.contains, out)
			}
		})
	}
}

func TestRenderSystemdTimer(t *testing.T) {
	data := installData{
		BinaryPath: "/usr/local/bin/doomsday",
		Home:       "/home/testuser",
	}

	out, err := RenderSystemdTimerFrom(data)
	if err != nil {
		t.Fatalf("RenderSystemdTimerFrom: %v", err)
	}

	tests := []struct {
		name     string
		contains string
	}{
		{"has unit section", "[Unit]"},
		{"has timer section", "[Timer]"},
		{"has install section", "[Install]"},
		{"has persistent", "Persistent=true"},
		{"has on boot", "OnBootSec=5min"},
		{"has interval", "OnUnitActiveSec=15min"},
		{"has timers target", "WantedBy=timers.target"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(out, tt.contains) {
				t.Errorf("output missing %q.\nGot:\n%s", tt.contains, out)
			}
		})
	}
}

func TestRenderLaunchdPlist(t *testing.T) {
	data := installData{
		BinaryPath: "/usr/local/bin/doomsday",
		Home:       "/Users/testuser",
		LogPath:    "/Users/testuser/.local/state/doomsday/cron.log",
	}

	out, err := RenderLaunchdPlistFrom(data)
	if err != nil {
		t.Fatalf("RenderLaunchdPlistFrom: %v", err)
	}

	tests := []struct {
		name     string
		contains string
	}{
		{"has xml header", "<?xml version="},
		{"has plist tag", "<plist version="},
		{"has label", "<string>com.doomsday.cron</string>"},
		{"has binary path", "<string>/usr/local/bin/doomsday</string>"},
		{"has cron arg", "<string>cron</string>"},
		{"has start interval", "<integer>900</integer>"},
		{"has run at load", "<true/>"},
		{"has log path", "<string>/Users/testuser/.local/state/doomsday/cron.log</string>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(out, tt.contains) {
				t.Errorf("output missing %q.\nGot:\n%s", tt.contains, out)
			}
		})
	}
}

func TestRenderLaunchdPlistXMLValidity(t *testing.T) {
	data := installData{
		BinaryPath: "/usr/local/bin/doomsday",
		Home:       "/Users/testuser",
		LogPath:    "/Users/testuser/.local/state/doomsday/cron.log",
	}

	out, err := RenderLaunchdPlistFrom(data)
	if err != nil {
		t.Fatal(err)
	}

	// Basic structural checks (not a full XML parse, but catches major issues).
	if !strings.HasPrefix(out, "<?xml") {
		t.Error("plist should start with XML declaration")
	}
	if !strings.Contains(out, "</plist>") {
		t.Error("plist should have closing </plist> tag")
	}
	if strings.Count(out, "<dict>") != strings.Count(out, "</dict>") {
		t.Error("mismatched <dict> tags")
	}
	if strings.Count(out, "<array>") != strings.Count(out, "</array>") {
		t.Error("mismatched <array> tags")
	}
}
