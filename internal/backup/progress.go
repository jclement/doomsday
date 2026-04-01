// Package backup implements the backup engine for doomsday.
// It orchestrates walking the filesystem, chunking files, deduplicating,
// compressing, encrypting, packing, and saving snapshots.
package backup

import (
	"sync"
	"sync/atomic"
	"time"
)

// ProgressFunc is called periodically during backup to report progress.
// Implementations must be safe for concurrent use.
type ProgressFunc func(Stats)

// Stats holds real-time backup statistics.
type Stats struct {
	// Filesystem walk
	FilesTotal     int64 // total files discovered by walker
	DirsTotal      int64 // total directories discovered
	FilesProcessed int64 // files fully processed (chunked + packed)
	FilesChanged   int64 // files with at least one new chunk
	FilesUnchanged int64 // files with all chunks already in repo

	// Data
	BytesRead   int64 // bytes read from filesystem
	BytesNew    int64 // bytes of new (non-duplicate) data
	BytesPacked int64 // bytes written to packs (post-compress+encrypt)
	ChunksTotal int64 // total chunks produced
	ChunksDup   int64 // chunks skipped (dedup)
	ChunksNew   int64 // chunks stored (new)

	// Packs
	PacksFlushed int64

	// Errors
	Errors int64

	// Current
	CurrentFile string // relative path of file currently being processed

	// Timing
	StartTime time.Time
	Elapsed   time.Duration
}

// progressTracker provides atomic counters for backup statistics.
// It is safe for concurrent use from multiple goroutines.
type progressTracker struct {
	_        sync.Mutex // reserved for future use
	callback ProgressFunc

	filesTotal     atomic.Int64
	dirsTotal      atomic.Int64
	filesProcessed atomic.Int64
	filesChanged   atomic.Int64
	filesUnchanged atomic.Int64
	bytesRead      atomic.Int64
	bytesNew       atomic.Int64
	bytesPacked    atomic.Int64
	chunksTotal    atomic.Int64
	chunksDup      atomic.Int64
	chunksNew      atomic.Int64
	packsFlushed   atomic.Int64
	errors         atomic.Int64
	currentFile    atomic.Value // string

	startTime time.Time
}

func newProgressTracker(fn ProgressFunc) *progressTracker {
	return &progressTracker{
		callback:  fn,
		startTime: time.Now(),
	}
}

// snapshot returns the current stats.
func (p *progressTracker) snapshot() Stats {
	s := Stats{
		FilesTotal:     p.filesTotal.Load(),
		DirsTotal:      p.dirsTotal.Load(),
		FilesProcessed: p.filesProcessed.Load(),
		FilesChanged:   p.filesChanged.Load(),
		FilesUnchanged: p.filesUnchanged.Load(),
		BytesRead:      p.bytesRead.Load(),
		BytesNew:       p.bytesNew.Load(),
		BytesPacked:    p.bytesPacked.Load(),
		ChunksTotal:    p.chunksTotal.Load(),
		ChunksDup:      p.chunksDup.Load(),
		ChunksNew:      p.chunksNew.Load(),
		PacksFlushed:   p.packsFlushed.Load(),
		Errors:         p.errors.Load(),
		StartTime:      p.startTime,
		Elapsed:        time.Since(p.startTime),
	}
	if v := p.currentFile.Load(); v != nil {
		s.CurrentFile = v.(string)
	}
	return s
}

// report calls the callback (if set) with current stats.
func (p *progressTracker) report() {
	if p.callback != nil {
		p.callback(p.snapshot())
	}
}
