// Package scheduler implements cron-mode scheduling of backups, auto-prune,
// and auto-check. It also handles installation of systemd timers and launchd plists.
package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State tracks the last run time for each backup configuration and
// the last auto-prune/auto-check times. It is persisted as JSON.
type State struct {
	mu sync.Mutex

	// LastBackup maps backup config name -> last successful backup time.
	LastBackup map[string]time.Time `json:"last_backup"`
	// LastPrune maps backup config name -> last successful auto-prune time.
	LastPrune map[string]time.Time `json:"last_prune"`
	// LastCheck maps backup config name -> last successful auto-check time.
	LastCheck map[string]time.Time `json:"last_check"`
}

// NewState returns a zero-valued State ready for use.
func NewState() *State {
	return &State{
		LastBackup: make(map[string]time.Time),
		LastPrune:  make(map[string]time.Time),
		LastCheck:  make(map[string]time.Time),
	}
}

// DefaultStatePath returns the default state file path,
// respecting XDG_STATE_HOME on Linux and falling back to
// ~/.config/doomsday/state.json otherwise.
func DefaultStatePath() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "doomsday", "state.json")
	}
	// Fall back to os.UserConfigDir (~/Library/Application Support on macOS,
	// %AppData% on Windows, ~/.config on Linux).
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(".", ".config")
	}
	return filepath.Join(dir, "doomsday", "state.json")
}

// LoadState reads and decodes a State from the file at path.
// If the file does not exist, a fresh State is returned (not an error).
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewState(), nil
		}
		return nil, fmt.Errorf("scheduler.LoadState: %w", err)
	}

	s := NewState()
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("scheduler.LoadState: %w", err)
	}

	// Ensure maps are initialized even if the JSON had null/empty values.
	if s.LastBackup == nil {
		s.LastBackup = make(map[string]time.Time)
	}
	if s.LastPrune == nil {
		s.LastPrune = make(map[string]time.Time)
	}
	if s.LastCheck == nil {
		s.LastCheck = make(map[string]time.Time)
	}

	return s, nil
}

// SaveState encodes the State as JSON and writes it atomically to path.
// Parent directories are created as needed with mode 0700.
func SaveState(path string, s *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("scheduler.SaveState: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("scheduler.SaveState: %w", err)
	}

	// Atomic write: write to temp file then rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("scheduler.SaveState: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Clean up temp on rename failure.
		os.Remove(tmp)
		return fmt.Errorf("scheduler.SaveState: %w", err)
	}

	return nil
}

// LastRun returns the last successful backup time for the given config name.
// Returns the zero time if no run has been recorded.
func (s *State) LastRun(name string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastBackup[name]
}

// SetLastRun records the last successful backup time for the given config name.
func (s *State) SetLastRun(name string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastBackup[name] = t
}

// LastPruneRun returns the last successful auto-prune time for the given config name.
func (s *State) LastPruneRun(name string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastPrune[name]
}

// SetLastPruneRun records the last successful auto-prune time.
func (s *State) SetLastPruneRun(name string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastPrune[name] = t
}

// LastCheckRun returns the last successful auto-check time for the given config name.
func (s *State) LastCheckRun(name string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastCheck[name]
}

// SetLastCheckRun records the last successful auto-check time.
func (s *State) SetLastCheckRun(name string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastCheck[name] = t
}

// IsDue reports whether a backup is due based on its schedule interval and
// last run time. If the schedule is empty, the backup is never due (it must
// be run manually). now is provided for testability.
func IsDue(lastRun time.Time, interval time.Duration, now time.Time) bool {
	if interval <= 0 {
		return false
	}
	if lastRun.IsZero() {
		// Never run before -- always due.
		return true
	}
	return now.Sub(lastRun) >= interval
}
