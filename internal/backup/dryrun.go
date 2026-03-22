package backup

import (
	"context"
	"os"
	"path/filepath"
)

// DryRunResult holds the results of a dry-run backup scan.
type DryRunResult struct {
	FilesTotal int64 // total regular files discovered
	DirsTotal  int64 // total directories discovered
	BytesTotal int64 // total size of regular files in bytes
	Errors     int64 // files/dirs that couldn't be read
}

// DryRun walks the filesystem and reports what would be backed up,
// without reading file contents or writing to any repository.
func DryRun(ctx context.Context, opts Options) (*DryRunResult, error) {
	progress := newProgressTracker(opts.OnProgress)

	for _, rootPath := range opts.Paths {
		rootPath = filepath.Clean(rootPath)
		walkCfg := buildWalkerConfig(opts, rootPath)
		entries := walkFilesystem(ctx, []string{rootPath}, walkCfg, progress)
		for entry := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if entry.Err != nil {
				continue
			}
			if entry.Info != nil && entry.Info.Mode().IsRegular() {
				progress.bytesRead.Add(entry.Info.Size())
			}
		}
	}

	stats := progress.snapshot()
	return &DryRunResult{
		FilesTotal: stats.FilesTotal,
		DirsTotal:  stats.DirsTotal,
		BytesTotal: stats.BytesRead,
		Errors:     stats.Errors,
	}, nil
}

// DryRunPaths returns the list of filesystem paths that would be included
// in a backup, applying the exclusion patterns.
func DryRunPaths(ctx context.Context, opts Options) ([]string, error) {
	progress := newProgressTracker(nil)
	var paths []string

	for _, rootPath := range opts.Paths {
		rootPath = filepath.Clean(rootPath)
		walkCfg := buildWalkerConfig(opts, rootPath)
		entries := walkFilesystem(ctx, []string{rootPath}, walkCfg, progress)
		for entry := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if entry.Err != nil || entry.Info == nil {
				continue
			}
			if entry.Info.Mode().IsRegular() || entry.Info.Mode()&os.ModeSymlink != 0 {
				paths = append(paths, entry.Path)
			}
		}
	}

	return paths, nil
}

// buildWalkerConfig creates a walkerConfig for a specific root path,
// merging global excludes with any per-source overrides.
func buildWalkerConfig(opts Options, rootPath string) walkerConfig {
	cfg := walkerConfig{
		Excludes: opts.Excludes,
	}
	if ps, ok := opts.PerSource[rootPath]; ok {
		if len(ps.Excludes) > 0 {
			merged := make([]string, 0, len(cfg.Excludes)+len(ps.Excludes))
			merged = append(merged, cfg.Excludes...)
			merged = append(merged, ps.Excludes...)
			cfg.Excludes = merged
		}
		cfg.OneFilesystem = ps.OneFilesystem
	}
	return cfg
}
