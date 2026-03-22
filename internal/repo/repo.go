// Package repo provides the repository abstraction that orchestrates
// backends, crypto, index, packing, and snapshot management.
package repo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jclement/doomsday/internal/compress"
	"github.com/jclement/doomsday/internal/crypto"
	"github.com/jclement/doomsday/internal/index"
	"github.com/jclement/doomsday/internal/pack"
	"github.com/jclement/doomsday/internal/snapshot"
	"github.com/jclement/doomsday/internal/types"
)

// FormatVersion is the current repository format version.
const FormatVersion = 1

// Config is the repository configuration stored encrypted at the repo root.
type Config struct {
	Version int    `json:"version"`
	ID      string `json:"id"`

	// Chunker parameters (immutable after creation)
	ChunkerAlgorithm string `json:"chunker_algorithm"`
	ChunkerMinSize   int    `json:"chunker_min_size"`
	ChunkerMaxSize   int    `json:"chunker_max_size"`
	ChunkerTargetSize int   `json:"chunker_target_size"`
}

// Option configures optional repository behavior.
type Option func(*openOptions)

type openOptions struct {
	cacheDir string
}

// WithCacheDir enables local caching of index files in the given directory.
// Cached index files are stored under <dir>/<repoID>/index/ and are
// content-addressable (filename = SHA-256 of encrypted content), so a
// cached file with the right name is guaranteed correct.
func WithCacheDir(dir string) Option {
	return func(o *openOptions) { o.cacheDir = dir }
}

// Repository provides access to a doomsday backup repository.
type Repository struct {
	backend  types.Backend
	keys     *crypto.KeySet
	config   *Config
	idx      *index.Index
	cacheDir string // local index cache directory (empty = disabled)
}

// Init creates a new repository at the given backend.
func Init(ctx context.Context, backend types.Backend, masterKey crypto.MasterKey) (*Repository, error) {
	// Derive sub-keys
	subKeys, err := crypto.DeriveSubKeys(masterKey)
	if err != nil {
		return nil, fmt.Errorf("repo.Init: %w", err)
	}

	ks := &crypto.KeySet{Master: masterKey, SubKeys: *subKeys}

	// Generate repo ID
	repoID, err := crypto.GenerateRepoID()
	if err != nil {
		return nil, fmt.Errorf("repo.Init: %w", err)
	}

	// Create config
	cfg := &Config{
		Version:           FormatVersion,
		ID:                repoID,
		ChunkerAlgorithm:  "fastcdc",
		ChunkerMinSize:    512 * 1024,
		ChunkerMaxSize:    8 * 1024 * 1024,
		ChunkerTargetSize: 1024 * 1024,
	}

	// Encrypt and save config
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("repo.Init: marshal config: %w", err)
	}

	encrypted, err := crypto.EncryptRaw(subKeys.Config, cfgData, crypto.AADConfig)
	if err != nil {
		return nil, fmt.Errorf("repo.Init: encrypt config: %w", err)
	}

	if err := backend.Save(ctx, types.FileTypeConfig, "config", bytes.NewReader(encrypted)); err != nil {
		return nil, fmt.Errorf("repo.Init: save config: %w", err)
	}

	return &Repository{
		backend: backend,
		keys:    ks,
		config:  cfg,
		idx:     index.New(),
	}, nil
}

// Open opens an existing repository.
func Open(ctx context.Context, backend types.Backend, masterKey crypto.MasterKey, opts ...Option) (*Repository, error) {
	var oo openOptions
	for _, o := range opts {
		o(&oo)
	}

	subKeys, err := crypto.DeriveSubKeys(masterKey)
	if err != nil {
		return nil, fmt.Errorf("repo.Open: %w", err)
	}
	ks := &crypto.KeySet{Master: masterKey, SubKeys: *subKeys}

	// Load and decrypt config
	rc, err := backend.Load(ctx, types.FileTypeConfig, "config", 0, 0)
	if err != nil {
		return nil, fmt.Errorf("repo.Open: load config: %w", err)
	}
	encrypted, err := io.ReadAll(io.LimitReader(rc, 1<<20)) // 1 MiB max config
	if closeErr := rc.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("repo.Open: read config: %w", err)
	}

	cfgData, err := crypto.DecryptRaw(subKeys.Config, encrypted, crypto.AADConfig)
	if err != nil {
		return nil, fmt.Errorf("repo.Open: %w", types.ErrInvalidKey)
	}

	var cfg Config
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return nil, fmt.Errorf("repo.Open: parse config: %w", err)
	}

	if cfg.Version > FormatVersion {
		return nil, types.ErrVersionTooNew
	}

	repo := &Repository{
		backend:  backend,
		keys:     ks,
		config:   &cfg,
		idx:      index.New(),
		cacheDir: oo.cacheDir,
	}

	// Load all index files
	if err := repo.loadIndexes(ctx); err != nil {
		return nil, fmt.Errorf("repo.Open: %w", err)
	}

	return repo, nil
}

// Close zeroes all key material held by the repository.
// The underlying backend is not closed (callers manage backend lifecycle).
func (r *Repository) Close() {
	if r.keys != nil {
		r.keys.Zero()
	}
}

// Config returns the repository configuration.
func (r *Repository) Config() *Config { return r.config }

// Keys returns the key set.
func (r *Repository) Keys() *crypto.KeySet { return r.keys }

// Index returns the blob index.
func (r *Repository) Index() *index.Index { return r.idx }

// Backend returns the underlying backend.
func (r *Repository) Backend() types.Backend { return r.backend }

// ReplaceIndex swaps the repository's in-memory index with a new one.
// Used by prune to install a garbage-collected index.
func (r *Repository) ReplaceIndex(idx *index.Index) { r.idx = idx }

// RepoID returns the repository identifier.
func (r *Repository) RepoID() string { return r.config.ID }

// SavePack encrypts and saves a pack file. Returns the pack ID (SHA-256 of ciphertext).
func (r *Repository) SavePack(ctx context.Context, packData []byte, blobType types.BlobType) (string, error) {
	// Pack ID = SHA-256 of ciphertext contents
	hash := sha256.Sum256(packData)
	packID := hex.EncodeToString(hash[:])

	if err := r.backend.Save(ctx, types.FileTypePack, packID, bytes.NewReader(packData)); err != nil {
		return "", fmt.Errorf("repo.SavePack: %w", err)
	}

	return packID, nil
}

// LoadBlob loads and decrypts a single blob from its pack.
func (r *Repository) LoadBlob(ctx context.Context, id types.BlobID) ([]byte, error) {
	entry, ok := r.idx.Lookup(id)
	if !ok {
		return nil, fmt.Errorf("repo.LoadBlob: %w: %s", types.ErrNotFound, id.Short())
	}

	rc, err := r.backend.Load(ctx, types.FileTypePack, entry.PackID, int64(entry.Offset), int64(entry.Length))
	if err != nil {
		return nil, fmt.Errorf("repo.LoadBlob: load pack: %w", err)
	}
	ciphertext, err := io.ReadAll(io.LimitReader(rc, int64(pack.MaxBlobSize)+1))
	if closeErr := rc.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("repo.LoadBlob: read: %w", err)
	}

	// Determine sub-key based on blob type
	var subKey [32]byte
	switch entry.Type {
	case types.BlobTypeData:
		subKey = r.keys.SubKeys.Data
	case types.BlobTypeTree:
		subKey = r.keys.SubKeys.Tree
	}

	plaintext, err := crypto.DecryptBlob(subKey, id, entry.Type, r.config.ID, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("repo.LoadBlob: %w", err)
	}

	// All blobs are compressed before encryption. Decompress first so we
	// can verify the content ID against the original (uncompressed) data,
	// which is how the ID was computed during backup.
	decompressed, err := compress.Decompress(plaintext)
	if err != nil {
		return nil, fmt.Errorf("repo.LoadBlob: decompress: %w", err)
	}

	// Verify content ID against raw (decompressed) data
	computedID := crypto.ContentID(r.keys.SubKeys.ContentID, decompressed)
	if computedID != id {
		return nil, fmt.Errorf("repo.LoadBlob: %w: content ID mismatch", types.ErrCorrupted)
	}

	return decompressed, nil
}

// SaveSnapshot saves a snapshot metadata file to the repository.
func (r *Repository) SaveSnapshot(ctx context.Context, snap *snapshot.Snapshot) error {
	data, err := snapshot.Marshal(snap)
	if err != nil {
		return fmt.Errorf("repo.SaveSnapshot: %w", err)
	}

	encrypted, err := crypto.EncryptRaw(r.keys.SubKeys.Snapshot, data, crypto.AADSnapshot)
	if err != nil {
		return fmt.Errorf("repo.SaveSnapshot: %w", err)
	}

	name := snap.ID + ".json"
	return r.backend.Save(ctx, types.FileTypeSnapshot, name, bytes.NewReader(encrypted))
}

// LoadSnapshot loads and decrypts a snapshot by ID.
func (r *Repository) LoadSnapshot(ctx context.Context, id string) (*snapshot.Snapshot, error) {
	name := id + ".json"
	rc, err := r.backend.Load(ctx, types.FileTypeSnapshot, name, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("repo.LoadSnapshot: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(rc, 10<<20)) // 10 MiB max snapshot
	if closeErr := rc.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("repo.LoadSnapshot: read: %w", err)
	}

	decrypted, err := crypto.DecryptRaw(r.keys.SubKeys.Snapshot, data, crypto.AADSnapshot)
	if err != nil {
		return nil, fmt.Errorf("repo.LoadSnapshot: %w", err)
	}

	return snapshot.Unmarshal(decrypted)
}

// DeleteSnapshot removes a snapshot metadata file from the repository.
// This does NOT remove any data blobs; use prune for that.
func (r *Repository) DeleteSnapshot(ctx context.Context, id string) error {
	name := id + ".json"
	if err := r.backend.Remove(ctx, types.FileTypeSnapshot, name); err != nil {
		return fmt.Errorf("repo.DeleteSnapshot: %w", err)
	}
	return nil
}

// ListSnapshots returns all snapshot IDs in the repository.
func (r *Repository) ListSnapshots(ctx context.Context) ([]string, error) {
	var ids []string
	err := r.backend.List(ctx, types.FileTypeSnapshot, func(fi types.FileInfo) error {
		name := fi.Name
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("repo.ListSnapshots: %w", err)
	}
	return ids, nil
}

// SaveIndex serializes and saves the current index to the backend.
func (r *Repository) SaveIndex(ctx context.Context) error {
	data, err := r.idx.Marshal()
	if err != nil {
		return fmt.Errorf("repo.SaveIndex: %w", err)
	}

	encrypted, err := crypto.EncryptRaw(r.keys.SubKeys.Index, data, crypto.AADIndex)
	if err != nil {
		return fmt.Errorf("repo.SaveIndex: encrypt: %w", err)
	}

	// Index file name: SHA-256 of encrypted content
	hash := sha256.Sum256(encrypted)
	name := hex.EncodeToString(hash[:]) + ".json"

	if err := r.backend.Save(ctx, types.FileTypeIndex, name, bytes.NewReader(encrypted)); err != nil {
		return err
	}

	// Cache the encrypted index locally for future opens.
	if dir := r.indexCacheDir(); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			slog.Warn("repo: failed to create index cache dir", "error", err)
		} else if err := os.WriteFile(filepath.Join(dir, name), encrypted, 0600); err != nil {
			slog.Warn("repo: failed to cache index file", "name", name, "error", err)
		}
	}

	return nil
}

// loadIndexes loads all index files from the backend into memory.
// When a local cache directory is configured, cached index files are read
// from disk instead of being re-downloaded from the remote backend.
func (r *Repository) loadIndexes(ctx context.Context) error {
	cacheDir := r.indexCacheDir()

	// Collect the set of remote index file names.
	var remoteFiles []types.FileInfo
	if err := r.backend.List(ctx, types.FileTypeIndex, func(fi types.FileInfo) error {
		remoteFiles = append(remoteFiles, fi)
		return nil
	}); err != nil {
		return fmt.Errorf("list indexes: %w", err)
	}

	// Load each index file, preferring the local cache when available.
	for _, fi := range remoteFiles {
		data, err := r.loadIndexFile(ctx, fi.Name, cacheDir)
		if err != nil {
			return err
		}

		decrypted, err := crypto.DecryptRaw(r.keys.SubKeys.Index, data, crypto.AADIndex)
		if err != nil {
			return fmt.Errorf("decrypt index %s: %w", fi.Name, err)
		}

		if err := r.idx.Unmarshal(decrypted); err != nil {
			return err
		}
	}

	// Remove stale cache entries that no longer exist on the remote
	// (e.g. after prune rewrites the index).
	if cacheDir != "" {
		r.pruneIndexCache(cacheDir, remoteFiles)
	}

	return nil
}

// loadIndexFile returns the encrypted bytes for a single index file.
// It reads from the local cache if available, otherwise from the backend
// (and caches the result for next time).
func (r *Repository) loadIndexFile(ctx context.Context, name, cacheDir string) ([]byte, error) {
	// Try local cache first.
	if cacheDir != "" {
		cached, err := os.ReadFile(filepath.Join(cacheDir, name))
		if err == nil {
			slog.Debug("repo: index cache hit", "name", name)
			return cached, nil
		}
		// Cache miss or read error — fall through to backend.
	}

	// Download from backend.
	rc, err := r.backend.Load(ctx, types.FileTypeIndex, name, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("load index %s: %w", name, err)
	}
	data, err := io.ReadAll(io.LimitReader(rc, 256<<20)) // 256 MiB max index
	if closeErr := rc.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("read index %s: %w", name, err)
	}

	// Cache for next time.
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			slog.Warn("repo: failed to create index cache dir", "error", err)
		} else if err := os.WriteFile(filepath.Join(cacheDir, name), data, 0600); err != nil {
			slog.Warn("repo: failed to cache index file", "name", name, "error", err)
		} else {
			slog.Debug("repo: cached index file", "name", name)
		}
	}

	return data, nil
}

// pruneIndexCache removes cached index files that no longer exist on the remote.
func (r *Repository) pruneIndexCache(cacheDir string, remoteFiles []types.FileInfo) {
	remote := make(map[string]struct{}, len(remoteFiles))
	for _, fi := range remoteFiles {
		remote[fi.Name] = struct{}{}
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return // cache dir may not exist yet
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := remote[e.Name()]; !ok {
			os.Remove(filepath.Join(cacheDir, e.Name()))
			slog.Debug("repo: removed stale cached index", "name", e.Name())
		}
	}
}

// indexCacheDir returns the resolved index cache directory for this repo,
// or "" if caching is disabled.
func (r *Repository) indexCacheDir() string {
	if r.cacheDir == "" {
		return ""
	}
	return filepath.Join(r.cacheDir, r.config.ID, "index")
}

// EncryptDataBlob encrypts a data blob for the repository.
func (r *Repository) EncryptDataBlob(id types.BlobID, plaintext []byte) ([]byte, error) {
	return crypto.EncryptBlob(r.keys.SubKeys.Data, id, types.BlobTypeData, r.config.ID, plaintext)
}

// EncryptTreeBlob encrypts a tree blob for the repository.
func (r *Repository) EncryptTreeBlob(id types.BlobID, plaintext []byte) ([]byte, error) {
	return crypto.EncryptBlob(r.keys.SubKeys.Tree, id, types.BlobTypeTree, r.config.ID, plaintext)
}

// packHeaderAAD builds the AAD for pack header encryption, binding the repo ID
// and blob type to prevent cross-repo and cross-type header substitution.
func packHeaderAAD(repoID string, blobType types.BlobType) []byte {
	aad := make([]byte, len(crypto.AADPackHeader)+1+len(repoID))
	copy(aad, crypto.AADPackHeader)
	aad[len(crypto.AADPackHeader)] = byte(blobType)
	copy(aad[len(crypto.AADPackHeader)+1:], repoID)
	return aad
}

// EncryptHeader encrypts a pack header using the appropriate sub-key.
// The repo ID and blob type are included in the AAD for domain separation.
func (r *Repository) EncryptHeader(blobType types.BlobType) pack.EncryptFunc {
	var key [32]byte
	switch blobType {
	case types.BlobTypeData:
		key = r.keys.SubKeys.Data
	case types.BlobTypeTree:
		key = r.keys.SubKeys.Tree
	}
	aad := packHeaderAAD(r.config.ID, blobType)
	return func(plaintext []byte) ([]byte, error) {
		return crypto.EncryptRaw(key, plaintext, aad)
	}
}

// DecryptHeader decrypts a pack header using the appropriate sub-key.
// The repo ID and blob type are included in the AAD for verification.
func (r *Repository) DecryptHeader(blobType types.BlobType) pack.DecryptFunc {
	var key [32]byte
	switch blobType {
	case types.BlobTypeData:
		key = r.keys.SubKeys.Data
	case types.BlobTypeTree:
		key = r.keys.SubKeys.Tree
	}
	aad := packHeaderAAD(r.config.ID, blobType)
	return func(ciphertext []byte) ([]byte, error) {
		return crypto.DecryptRaw(key, ciphertext, aad)
	}
}

// ContentID computes the keyed content ID for data.
func (r *Repository) ContentID(data []byte) types.BlobID {
	return crypto.ContentID(r.keys.SubKeys.ContentID, data)
}
