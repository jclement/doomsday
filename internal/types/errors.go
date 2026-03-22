package types

import "errors"

// Sentinel errors used across packages.
var (
	ErrRepoNotFound  = errors.New("repository not found")
	ErrRepoExists    = errors.New("repository already exists")
	ErrDecryptFailed = errors.New("decryption failed: wrong key or corrupted data")
	ErrLockConflict  = errors.New("repository is locked by another process")
	ErrCorrupted     = errors.New("data corruption detected")
	ErrNotFound      = errors.New("not found")
	ErrReadOnly      = errors.New("repository is read-only")
	ErrInvalidKey    = errors.New("invalid key or password")
	ErrVersionTooNew = errors.New("repository version is newer than this binary supports — please update doomsday")
	ErrRollback      = errors.New("repository rollback detected — snapshot(s) missing that were previously seen")
)
