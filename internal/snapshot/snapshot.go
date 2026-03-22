// Package snapshot defines snapshot metadata structures.
package snapshot

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jclement/doomsday/internal/types"
)

// Snapshot contains metadata for a single point-in-time backup.
type Snapshot struct {
	ID               string       `json:"id"`
	Time             time.Time    `json:"time"`               // backup start time
	Hostname         string       `json:"hostname"`
	Paths            []string     `json:"paths"`
	Tags             []string     `json:"tags,omitempty"`
	Tree             types.BlobID `json:"tree"`               // root tree blob ID
	BackupConfigName string       `json:"backup_config_name"`

	// Summary statistics
	Summary *Summary `json:"summary,omitempty"`
}

// Summary holds backup run statistics.
type Summary struct {
	FilesNew       int64         `json:"files_new"`
	FilesChanged   int64         `json:"files_changed"`
	FilesUnchanged int64         `json:"files_unchanged"`
	DirsNew        int64         `json:"dirs_new"`
	DirsChanged    int64         `json:"dirs_changed"`
	DirsUnchanged  int64         `json:"dirs_unchanged"`
	DataAdded      int64         `json:"data_added"`       // bytes of new data
	TotalSize      int64         `json:"total_size"`       // total bytes in snapshot
	TotalFiles     int64         `json:"total_files"`
	Duration       time.Duration `json:"duration"`
}

// Marshal serializes a snapshot to JSON.
func Marshal(s *Snapshot) ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("snapshot.Marshal: %w", err)
	}
	return data, nil
}

// Unmarshal deserializes a snapshot from JSON.
func Unmarshal(data []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("snapshot.Unmarshal: %w", err)
	}
	return &s, nil
}
