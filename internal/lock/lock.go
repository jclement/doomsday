// Package lock provides repository locking with exclusive/shared semantics,
// stale detection, and HMAC authentication.
package lock

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jclement/doomsday/internal/types"
)

const (
	staleTimeout    = 30 * time.Minute
	refreshInterval = 60 * time.Second
)

// Type represents the lock type.
type Type string

const (
	Exclusive Type = "exclusive"
	Shared    Type = "shared"
)

// LockFile is the on-disk lock format.
type LockFile struct {
	Hostname  string `json:"hostname"`
	PID       int    `json:"pid"`
	Created   string `json:"created"`
	Refreshed string `json:"refreshed"`
	Type      Type   `json:"type"`
	Operation string `json:"operation"`
	HMAC      string `json:"hmac"`
}

// maxRefreshFailures is the number of consecutive refresh failures before
// the lock is considered lost and the operation should be aborted.
const maxRefreshFailures = 5

// Lock represents an active lock on a repository.
type Lock struct {
	backend             types.Backend
	hmacKey             [32]byte
	name                string
	lockType            Type
	cancel              context.CancelFunc
	refreshed           chan struct{} // closed when refresh goroutine exits
	consecutiveFailures atomic.Int32
}

// Acquire attempts to acquire a lock on the repository.
// Returns ErrLockConflict if a conflicting lock exists.
//
// Uses a write-first-then-check pattern to avoid TOCTOU races:
//  1. Write our lock file first.
//  2. Check for conflicting locks (excluding our own).
//  3. If conflict found, remove our lock and return error.
func Acquire(ctx context.Context, backend types.Backend, hmacKey [32]byte, lockType Type, operation string) (*Lock, error) {
	// Generate a unique lock name
	var nameBuf [8]byte
	if _, err := io.ReadFull(rand.Reader, nameBuf[:]); err != nil {
		return nil, fmt.Errorf("lock.Acquire: %w", err)
	}
	name := hex.EncodeToString(nameBuf[:]) + ".json"

	hostname, _ := os.Hostname()
	now := time.Now().UTC()

	lf := &LockFile{
		Hostname:  hostname,
		PID:       os.Getpid(),
		Created:   now.Format(time.RFC3339),
		Refreshed: now.Format(time.RFC3339),
		Type:      lockType,
		Operation: operation,
	}

	// Compute HMAC for authentication
	lf.HMAC = computeHMAC(hmacKey, lf)

	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("lock.Acquire: marshal: %w", err)
	}

	// Step 1: Write our lock file FIRST (before checking for conflicts).
	if err := backend.Save(ctx, types.FileTypeLock, name, strings.NewReader(string(data))); err != nil {
		return nil, fmt.Errorf("lock.Acquire: save: %w", err)
	}

	// Step 2: Check for conflicting locks (excluding our own).
	if err := checkConflicts(ctx, backend, hmacKey, lockType, name); err != nil {
		// Step 3: Conflict found — remove our lock and return error.
		backend.Remove(ctx, types.FileTypeLock, name)
		return nil, err
	}

	// Start refresh goroutine — derived from the caller's context so that
	// cancelling the parent operation also stops the refresh loop.
	refreshCtx, cancel := context.WithCancel(ctx)
	lock := &Lock{
		backend:   backend,
		hmacKey:   hmacKey,
		name:      name,
		lockType:  lockType,
		cancel:    cancel,
		refreshed: make(chan struct{}),
	}
	go lock.refreshLoop(refreshCtx)

	return lock, nil
}

// Release removes the lock file and stops the refresh goroutine.
func (l *Lock) Release(ctx context.Context) error {
	l.cancel()
	<-l.refreshed // wait for refresh goroutine to exit
	return l.backend.Remove(ctx, types.FileTypeLock, l.name)
}

// refreshLoop periodically updates the lock's refreshed timestamp.
func (l *Lock) refreshLoop(ctx context.Context) {
	defer close(l.refreshed)
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.refresh(ctx)
		}
	}
}

// RefreshFailed reports whether lock refresh has failed too many consecutive
// times, meaning the lock may have been lost and the operation should abort.
func (l *Lock) RefreshFailed() bool {
	return l.consecutiveFailures.Load() >= int32(maxRefreshFailures)
}

func (l *Lock) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	rc, err := l.backend.Load(ctx, types.FileTypeLock, l.name, 0, 0)
	if err != nil {
		failures := l.consecutiveFailures.Add(1)
		slog.Warn("lock refresh: failed to load lock file", "lock", l.name, "error", err, "consecutive_failures", failures)
		if failures >= int32(maxRefreshFailures) {
			slog.Error("lock refresh: too many consecutive failures, lock may be lost — cancelling operation", "lock", l.name)
			l.cancel()
		}
		return
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		failures := l.consecutiveFailures.Add(1)
		slog.Warn("lock refresh: failed to read lock data", "lock", l.name, "error", err, "consecutive_failures", failures)
		return
	}

	var lf LockFile
	if json.Unmarshal(data, &lf) != nil {
		l.consecutiveFailures.Add(1)
		return
	}

	lf.Refreshed = time.Now().UTC().Format(time.RFC3339)
	lf.HMAC = computeHMAC(l.hmacKey, &lf)

	newData, err := json.MarshalIndent(&lf, "", "  ")
	if err != nil {
		l.consecutiveFailures.Add(1)
		return
	}

	if err := l.backend.Save(ctx, types.FileTypeLock, l.name, strings.NewReader(string(newData))); err != nil {
		failures := l.consecutiveFailures.Add(1)
		slog.Warn("lock refresh: failed to save updated lock", "lock", l.name, "error", err, "consecutive_failures", failures)
		if failures >= int32(maxRefreshFailures) {
			slog.Error("lock refresh: too many consecutive failures, lock may be lost — cancelling operation", "lock", l.name)
			l.cancel()
		}
		return
	}

	// Success — reset counter.
	l.consecutiveFailures.Store(0)
}

func checkConflicts(ctx context.Context, backend types.Backend, hmacKey [32]byte, requestedType Type, ownName string) error {
	var locks []LockFile
	err := backend.List(ctx, types.FileTypeLock, func(fi types.FileInfo) error {
		// Skip our own lock file.
		if fi.Name == ownName {
			return nil
		}

		rc, err := backend.Load(ctx, types.FileTypeLock, fi.Name, 0, 0)
		if err != nil {
			return nil // skip unreadable locks
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil
		}
		var lf LockFile
		if json.Unmarshal(data, &lf) != nil {
			return nil
		}

		// Verify HMAC — reject unauthentic locks
		if !verifyHMAC(hmacKey, &lf) {
			// Remove unauthenticated lock (could be from attacker)
			backend.Remove(ctx, types.FileTypeLock, fi.Name)
			return nil
		}

		// Check staleness
		refreshed, err := time.Parse(time.RFC3339, lf.Refreshed)
		if err != nil {
			backend.Remove(ctx, types.FileTypeLock, fi.Name)
			return nil
		}
		if time.Since(refreshed) > staleTimeout {
			backend.Remove(ctx, types.FileTypeLock, fi.Name)
			return nil
		}

		locks = append(locks, lf)
		return nil
	})
	if err != nil {
		return fmt.Errorf("lock.checkConflicts: %w", err)
	}

	// Check for conflicts
	for _, lf := range locks {
		if requestedType == Exclusive || lf.Type == Exclusive {
			return fmt.Errorf("lock.Acquire: %w: held by %s (PID %d, %s since %s)",
				types.ErrLockConflict, lf.Hostname, lf.PID, lf.Operation, lf.Created)
		}
	}
	return nil
}

func computeHMAC(key [32]byte, lf *LockFile) string {
	// HMAC over all fields except the HMAC itself
	msg := fmt.Sprintf("%s|%d|%s|%s|%s|%s",
		lf.Hostname, lf.PID, lf.Created, lf.Refreshed, lf.Type, lf.Operation)
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyHMAC(key [32]byte, lf *LockFile) bool {
	expected := computeHMAC(key, lf)
	return hmac.Equal([]byte(expected), []byte(lf.HMAC))
}

// RemoveAll removes all lock files (for `doomsday unlock`).
func RemoveAll(ctx context.Context, backend types.Backend) error {
	var names []string
	err := backend.List(ctx, types.FileTypeLock, func(fi types.FileInfo) error {
		names = append(names, fi.Name)
		return nil
	})
	if err != nil {
		return fmt.Errorf("lock.RemoveAll: %w", err)
	}
	for _, name := range names {
		backend.Remove(ctx, types.FileTypeLock, name)
	}
	return nil
}
