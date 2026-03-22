package views

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jclement/doomsday/internal/backend/local"
	"github.com/jclement/doomsday/internal/backend/s3"
	"github.com/jclement/doomsday/internal/backend/sftp"
	"github.com/jclement/doomsday/internal/config"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/repo"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/tree"
	"github.com/jclement/doomsday/internal/types"
)

// Session manages repository connections for the TUI.
// It lazily opens backends and repos, caching them for reuse.
type Session struct {
	mu        sync.Mutex
	cfg       *config.Config
	masterKey crypto.MasterKey
	unlocked  bool

	// Cached repos keyed by destination name
	repos    map[string]*repo.Repository
	backends map[string]types.Backend
}

// NewSession creates a new TUI session with the given config.
func NewSession(cfg *config.Config) *Session {
	return &Session{
		cfg:      cfg,
		repos:    make(map[string]*repo.Repository),
		backends: make(map[string]types.Backend),
	}
}

// Config returns the session config.
func (s *Session) Config() *config.Config {
	return s.cfg
}

// IsUnlocked returns true if the master key has been set.
func (s *Session) IsUnlocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unlocked
}

// Unlock resolves the master key using the same logic as the CLI.
//
//   - file: → key file (password needed for v1 encrypted)
//   - env:/cmd:/literal → derive via scrypt passphrase
//
// The password parameter is only used for v1 encrypted key files.
func (s *Session) Unlock(password []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyRef := s.cfg.Key

	// Resolve env:/cmd: references to their values.
	resolved, err := config.ResolveKey(s.cfg)
	if err != nil {
		return fmt.Errorf("resolve key: %w", err)
	}

	// file: → read as key file.
	if strings.HasPrefix(keyRef, "file:") {
		keyFilePath := config.ExpandPath(strings.TrimPrefix(keyRef, "file:"))
		return s.unlockFromKeyFile(keyFilePath, password)
	}

	// env:/cmd:/literal → derive via scrypt.
	mk, err := crypto.DeriveKeyFromPassphrase(resolved)
	if err != nil {
		return fmt.Errorf("derive key: %w", err)
	}
	s.masterKey = mk
	s.unlocked = true
	return nil
}

// unlockFromKeyFile reads a key file and decrypts it.
func (s *Session) unlockFromKeyFile(path string, password []byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read key file: %w", err)
	}

	kf, err := crypto.UnmarshalKeyFile(data)
	if err != nil {
		return fmt.Errorf("parse key file: %w", err)
	}

	mk, err := crypto.OpenKeyFile(kf, password)
	if err != nil {
		return fmt.Errorf("unlock failed: wrong password or recovery phrase")
	}

	s.masterKey = mk
	s.unlocked = true
	return nil
}

// TryAutoUnlock attempts to unlock without user interaction.
// Works for env:/cmd:/literal keys (always succeeds) and plaintext key files.
func (s *Session) TryAutoUnlock() bool {
	// Try unlock with no password — works for env:/cmd:/literal keys
	// and plaintext key files.
	if s.Unlock(nil) == nil {
		return true
	}

	// For encrypted key files, try DOOMSDAY_PASSWORD env var.
	pw := os.Getenv("DOOMSDAY_PASSWORD")
	if pw == "" {
		return false
	}
	return s.Unlock([]byte(pw)) == nil
}

// NeedsPassword returns true if the key requires interactive password input.
// Returns false for env:/cmd:/literal keys and plaintext key files.
func (s *Session) NeedsPassword() bool {
	keyRef := s.cfg.Key

	// env:/cmd:/literal keys don't need a password — they're derived via scrypt.
	if !strings.HasPrefix(keyRef, "file:") {
		return false
	}

	keyFilePath := config.ExpandPath(strings.TrimPrefix(keyRef, "file:"))
	data, err := os.ReadFile(keyFilePath)
	if err != nil {
		return true
	}
	kf, err := crypto.UnmarshalKeyFile(data)
	if err != nil {
		return true
	}
	return !kf.IsPlaintext()
}

// OpenRepo opens (or returns cached) repo for a destination.
func (s *Session) OpenRepo(ctx context.Context, dest *config.DestConfig) (*repo.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.unlocked {
		return nil, fmt.Errorf("session not unlocked")
	}

	cacheKey := dest.Name

	if r, ok := s.repos[cacheKey]; ok {
		return r, nil
	}

	backend, err := openBackendFromConfig(ctx, dest)
	if err != nil {
		return nil, fmt.Errorf("open backend %s: %w", dest.Name, err)
	}

	var repoOpts []repo.Option
	if s.cfg.Settings.CacheDir != "" {
		repoOpts = append(repoOpts, repo.WithCacheDir(s.cfg.Settings.CacheDir))
	}
	r, err := repo.Open(ctx, backend, s.masterKey, repoOpts...)
	if err != nil {
		backend.Close()
		return nil, fmt.Errorf("open repo: %w", err)
	}

	s.backends[cacheKey] = backend
	s.repos[cacheKey] = r
	return r, nil
}

// OpenFirstRepo opens (or returns cached) repo for the first active destination.
func (s *Session) OpenFirstRepo(ctx context.Context) (*repo.Repository, error) {
	dests := s.cfg.ActiveDestinations()
	if len(dests) == 0 {
		if len(s.cfg.Destinations) == 0 {
			return nil, fmt.Errorf("no destinations configured")
		}
		return s.OpenRepo(ctx, &s.cfg.Destinations[0])
	}
	return s.OpenRepo(ctx, &dests[0])
}

// FirstDest returns the first active destination, or the first destination.
func (s *Session) FirstDest() *config.DestConfig {
	dests := s.cfg.ActiveDestinations()
	if len(dests) > 0 {
		return &dests[0]
	}
	if len(s.cfg.Destinations) > 0 {
		return &s.cfg.Destinations[0]
	}
	return nil
}

// MasterKey returns the unlocked master key. Panics if not unlocked.
func (s *Session) MasterKey() crypto.MasterKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.masterKey
}

// Close releases all cached resources.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, b := range s.backends {
		b.Close()
	}
	s.backends = make(map[string]types.Backend)
	s.repos = make(map[string]*repo.Repository)

	if s.unlocked {
		s.masterKey.Zero()
		s.unlocked = false
	}
}

// ForgetSnapshot deletes a snapshot from the repository.
func (s *Session) ForgetSnapshot(ctx context.Context, dest *config.DestConfig, snapshotID string) error {
	r, err := s.OpenRepo(ctx, dest)
	if err != nil {
		return err
	}

	if err := r.DeleteSnapshot(ctx, snapshotID); err != nil {
		return fmt.Errorf("forget snapshot: %w", err)
	}
	return nil
}

// LoadSnapshots loads all snapshots from a destination.
func (s *Session) LoadSnapshots(ctx context.Context, dest *config.DestConfig) ([]SnapshotItem, error) {
	r, err := s.OpenRepo(ctx, dest)
	if err != nil {
		return nil, err
	}

	ids, err := r.ListSnapshots(ctx)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	var items []SnapshotItem
	for _, id := range ids {
		snap, err := r.LoadSnapshot(ctx, id)
		if err != nil {
			continue
		}
		item := snapshotToItem(snap)
		items = append(items, item)
	}

	// Sort by time, newest first.
	sort.Slice(items, func(i, j int) bool {
		return items[i].Time.After(items[j].Time)
	})

	return items, nil
}

// LoadTree loads a tree blob from a repo for a destination.
func (s *Session) LoadTree(ctx context.Context, dest *config.DestConfig, treeID types.BlobID) (*tree.Tree, error) {
	r, err := s.OpenRepo(ctx, dest)
	if err != nil {
		return nil, err
	}

	data, err := r.LoadBlob(ctx, treeID)
	if err != nil {
		return nil, fmt.Errorf("load tree blob: %w", err)
	}

	return tree.Unmarshal(data)
}

// SnapshotTreeID returns the root tree ID for a given snapshot.
func (s *Session) SnapshotTreeID(ctx context.Context, dest *config.DestConfig, snapshotID string) (types.BlobID, error) {
	r, err := s.OpenRepo(ctx, dest)
	if err != nil {
		return types.BlobID{}, err
	}

	snap, err := r.LoadSnapshot(ctx, snapshotID)
	if err != nil {
		return types.BlobID{}, err
	}

	return snap.Tree, nil
}

// LoadFileContent loads and concatenates file content blobs from the repo.
func (s *Session) LoadFileContent(ctx context.Context, dest *config.DestConfig, blobIDs []types.BlobID) ([]byte, error) {
	r, err := s.OpenRepo(ctx, dest)
	if err != nil {
		return nil, err
	}

	var content []byte
	for _, id := range blobIDs {
		chunk, err := r.LoadBlob(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load blob %s: %w", id, err)
		}
		content = append(content, chunk...)
		if len(content) > 1<<20 {
			content = content[:1<<20]
			break
		}
	}
	return content, nil
}

// snapshotToItem converts a snapshot.Snapshot to a SnapshotItem for display.
func snapshotToItem(snap *snapshot.Snapshot) SnapshotItem {
	item := SnapshotItem{
		ID:       snap.ID,
		Time:     snap.Time,
		Hostname: snap.Hostname,
		Paths:    snap.Paths,
		Tags:     snap.Tags,
	}
	if snap.Summary != nil {
		item.TotalFiles = snap.Summary.TotalFiles
		item.TotalSize = snap.Summary.TotalSize
		item.DataAdded = snap.Summary.DataAdded
		item.FilesNew = snap.Summary.FilesNew
		item.FilesChanged = snap.Summary.FilesChanged
		item.FilesUnchanged = snap.Summary.FilesUnchanged
		item.Duration = snap.Summary.Duration
	}
	return item
}

// openBackendFromConfig creates a backend from a destination config.
func openBackendFromConfig(ctx context.Context, dest *config.DestConfig) (types.Backend, error) {
	dc := *dest
	if err := config.ResolveDestSecrets(&dc, dc.Name); err != nil {
		return nil, fmt.Errorf("resolve secrets for %s: %w", dest.Name, err)
	}

	switch dc.Type {
	case "local":
		path := config.ExpandPath(dc.Path)
		return local.New(path)

	case "sftp":
		basePath := dc.BasePath
		port := strconv.Itoa(dc.Port)
		if dc.Port == 0 {
			port = "22"
		}

		// Use pinned host key if available, fall back to known_hosts.
		if dc.HostKey != "" {
			hkCb, err := sftp.HostKeyFingerprint(dc.HostKey)
			if err != nil {
				return nil, fmt.Errorf("parse host key for %s: %w", dest.Name, err)
			}
			return sftp.New(dc.Host, port, dc.User, basePath,
				config.ExpandPath(dc.KeyFile), dc.Password, dc.SSHKey, hkCb)
		}

		home, _ := os.UserHomeDir()
		knownHostsPath := home + "/.ssh/known_hosts"
		hostKeyCb, err := sftp.HostKeyFile(knownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts for %s: %w", dest.Name, err)
		}
		return sftp.New(dc.Host, port, dc.User, basePath,
			config.ExpandPath(dc.KeyFile), dc.Password, dc.SSHKey, hostKeyCb)

	case "s3":
		return s3.New(dc.Endpoint, dc.Bucket, "", dc.KeyID, dc.SecretKey, true)

	default:
		return nil, fmt.Errorf("unknown destination type %q", dc.Type)
	}
}
