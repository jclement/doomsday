package snapshot

import (
	"testing"
	"time"

	"github.com/jclement/doomsday/internal/types"
)

func TestMarshalUnmarshal(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	snap := &Snapshot{
		ID:               "abc123",
		Time:             now,
		Hostname:         "macbook.local",
		Paths:            []string{"/Users/jsc"},
		Tags:             []string{"daily"},
		Tree:             types.BlobID{0x01, 0x02, 0x03},
		BackupConfigName: "home",
		Summary: &Summary{
			FilesNew:     10,
			FilesChanged: 5,
			DataAdded:    1024 * 1024,
			TotalSize:    100 * 1024 * 1024,
			TotalFiles:   1000,
			Duration:     30 * time.Second,
		},
	}

	data, err := Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}

	snap2, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}

	if snap2.ID != snap.ID {
		t.Errorf("ID = %q, want %q", snap2.ID, snap.ID)
	}
	if snap2.Hostname != snap.Hostname {
		t.Errorf("Hostname = %q", snap2.Hostname)
	}
	if snap2.BackupConfigName != "home" {
		t.Errorf("BackupConfigName = %q", snap2.BackupConfigName)
	}
	if snap2.Tree != snap.Tree {
		t.Errorf("Tree mismatch")
	}
	if snap2.Summary == nil {
		t.Fatal("Summary is nil")
	}
	if snap2.Summary.FilesNew != 10 {
		t.Errorf("FilesNew = %d", snap2.Summary.FilesNew)
	}
	if snap2.Summary.DataAdded != 1024*1024 {
		t.Errorf("DataAdded = %d", snap2.Summary.DataAdded)
	}
}

func TestUnmarshal_NoSummary(t *testing.T) {
	snap := &Snapshot{
		ID:       "xyz",
		Hostname: "test",
		Paths:    []string{"/tmp"},
	}

	data, err := Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}

	snap2, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.Summary != nil {
		t.Error("expected nil summary")
	}
}

func TestUnmarshal_Invalid(t *testing.T) {
	_, err := Unmarshal([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func FuzzSnapshotUnmarshal(f *testing.F) {
	// Seed corpus: valid JSON, invalid JSON, empty
	validSnap := &Snapshot{
		ID:       "test-id",
		Hostname: "host",
		Paths:    []string{"/tmp"},
	}
	validData, _ := Marshal(validSnap)
	f.Add(validData)
	f.Add([]byte{})
	f.Add([]byte("not json"))
	f.Add([]byte(`{"id":"x","paths":[]}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic. Errors are acceptable.
		_, _ = Unmarshal(data)
	})
}
