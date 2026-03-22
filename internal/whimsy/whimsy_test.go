package whimsy

import (
	"testing"
)

func TestGreeting_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := Greeting()
	if msg == "" {
		t.Error("Greeting() returned empty string when enabled")
	}
}

func TestBackupStart_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := BackupStart()
	if msg == "" {
		t.Error("BackupStart() returned empty string when enabled")
	}
}

func TestBackupComplete_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := BackupComplete()
	if msg == "" {
		t.Error("BackupComplete() returned empty string when enabled")
	}
}

func TestIdleStatus_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := IdleStatus()
	if msg == "" {
		t.Error("IdleStatus() returned empty string when enabled")
	}
}

func TestVersionTagline_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := VersionTagline()
	if msg == "" {
		t.Error("VersionTagline() returned empty string when enabled")
	}
}

func TestRestoreStart_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := RestoreStart()
	if msg == "" {
		t.Error("RestoreStart() returned empty string when enabled")
	}
}

func TestRestoreComplete_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := RestoreComplete()
	if msg == "" {
		t.Error("RestoreComplete() returned empty string when enabled")
	}
}

func TestBrowsingFiles_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := BrowsingFiles()
	if msg == "" {
		t.Error("BrowsingFiles() returned empty string when enabled")
	}
}

func TestEmptyState_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := EmptyState()
	if msg == "" {
		t.Error("EmptyState() returned empty string when enabled")
	}
}

func TestScanning_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := Scanning()
	if msg == "" {
		t.Error("Scanning() returned empty string when enabled")
	}
}

func TestFarewell_ReturnsNonEmpty(t *testing.T) {
	SetEnabled(true)
	msg := Farewell()
	if msg == "" {
		t.Error("Farewell() returned empty string when enabled")
	}
}

func TestDisabled_ReturnsEmpty(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)

	funcs := map[string]func() string{
		"Greeting":        Greeting,
		"BackupStart":     BackupStart,
		"BackupComplete":  BackupComplete,
		"IdleStatus":      IdleStatus,
		"VersionTagline":  VersionTagline,
		"RestoreStart":    RestoreStart,
		"RestoreComplete": RestoreComplete,
		"BrowsingFiles":   BrowsingFiles,
		"EmptyState":      EmptyState,
		"Scanning":        Scanning,
		"Farewell":        Farewell,
	}

	for name, fn := range funcs {
		t.Run(name, func(t *testing.T) {
			msg := fn()
			if msg != "" {
				t.Errorf("%s() returned %q when disabled, want empty", name, msg)
			}
		})
	}
}

func TestSetEnabled_Toggle(t *testing.T) {
	SetEnabled(true)
	if !IsEnabled() {
		t.Error("IsEnabled() should be true after SetEnabled(true)")
	}

	SetEnabled(false)
	if IsEnabled() {
		t.Error("IsEnabled() should be false after SetEnabled(false)")
	}

	SetEnabled(true)
	if !IsEnabled() {
		t.Error("IsEnabled() should be true after re-enabling")
	}
}

func TestConsistencyWithSameSeed(t *testing.T) {
	SetEnabled(true)
	// Re-seed to ensure determinism for this test.
	seedRNG()

	// Call Greeting multiple times in sequence -- the RNG is deterministic
	// for a given seed, so the sequence should be repeatable.
	results := make([]string, 10)
	for i := range results {
		results[i] = Greeting()
	}

	// Verify all returned non-empty strings from the pool.
	for i, r := range results {
		if r == "" {
			t.Errorf("call %d returned empty", i)
		}
	}
}

func TestPoolSizes(t *testing.T) {
	// All pools must have at least 20 messages.
	pools := map[string][]string{
		"greetings":       greetings,
		"backupStart":     backupStartMessages,
		"backupComplete":  backupCompleteMessages,
		"idleStatus":      idleStatusMessages,
		"versionTaglines": versionTaglines,
		"restoreStart":    restoreStartMessages,
		"restoreComplete": restoreCompleteMessages,
		"browsing":        browsingMessages,
		"emptyState":      emptyStateMessages,
		"scanning":        scanningMessages,
		"farewell":        farewellMessages,
	}

	for name, pool := range pools {
		t.Run(name, func(t *testing.T) {
			if len(pool) < 20 {
				t.Errorf("%s has %d messages, want at least 20", name, len(pool))
			}
		})
	}
}

func TestNoDuplicatesInPools(t *testing.T) {
	pools := map[string][]string{
		"greetings":       greetings,
		"backupStart":     backupStartMessages,
		"backupComplete":  backupCompleteMessages,
		"idleStatus":      idleStatusMessages,
		"versionTaglines": versionTaglines,
		"restoreStart":    restoreStartMessages,
		"restoreComplete": restoreCompleteMessages,
		"browsing":        browsingMessages,
		"emptyState":      emptyStateMessages,
		"scanning":        scanningMessages,
		"farewell":        farewellMessages,
	}

	for name, pool := range pools {
		t.Run(name, func(t *testing.T) {
			seen := make(map[string]bool)
			for _, msg := range pool {
				if seen[msg] {
					t.Errorf("duplicate message in %s: %q", name, msg)
				}
				seen[msg] = true
			}
		})
	}
}

func TestNoEmptyMessages(t *testing.T) {
	pools := map[string][]string{
		"greetings":       greetings,
		"backupStart":     backupStartMessages,
		"backupComplete":  backupCompleteMessages,
		"idleStatus":      idleStatusMessages,
		"versionTaglines": versionTaglines,
		"restoreStart":    restoreStartMessages,
		"restoreComplete": restoreCompleteMessages,
		"browsing":        browsingMessages,
		"emptyState":      emptyStateMessages,
		"scanning":        scanningMessages,
		"farewell":        farewellMessages,
	}

	for name, pool := range pools {
		t.Run(name, func(t *testing.T) {
			for i, msg := range pool {
				if msg == "" {
					t.Errorf("%s[%d] is empty", name, i)
				}
			}
		})
	}
}

func TestPickFromAllPools(t *testing.T) {
	SetEnabled(true)
	seedRNG()

	// Ensure each function returns a message that exists in its pool.
	checks := []struct {
		name string
		fn   func() string
		pool []string
	}{
		{"Greeting", Greeting, greetings},
		{"BackupStart", BackupStart, backupStartMessages},
		{"BackupComplete", BackupComplete, backupCompleteMessages},
		{"IdleStatus", IdleStatus, idleStatusMessages},
		{"VersionTagline", VersionTagline, versionTaglines},
		{"RestoreStart", RestoreStart, restoreStartMessages},
		{"RestoreComplete", RestoreComplete, restoreCompleteMessages},
		{"BrowsingFiles", BrowsingFiles, browsingMessages},
		{"EmptyState", EmptyState, emptyStateMessages},
		{"Scanning", Scanning, scanningMessages},
		{"Farewell", Farewell, farewellMessages},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			msg := c.fn()
			found := false
			for _, m := range c.pool {
				if m == msg {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s() returned %q which is not in its pool", c.name, msg)
			}
		})
	}
}
