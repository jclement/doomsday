package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/pkg/sftp"
)

// Handler implements the pkg/sftp request handler interfaces with a strict
// whitelist approach:
//
//   - FileReader  (Fileread)  -- allowed: read files
//   - FileWriter  (Filewrite) -- allowed: create new files only (no overwrite/truncate)
//   - FileLister  (Filelist)  -- allowed: list directories, stat files
//   - FileCmder   (Filecmd)   -- allowed: Mkdir, Remove (files only), Rename (no overwrite)
//
// All paths are jailed to the client's data directory. Symlinks are resolved
// and verified to remain within the jail.
type Handler struct {
	jailDir    string
	quotaBytes int64
	usedBytes  atomic.Int64
	mu         sync.Mutex // protects quota reservation
	appendOnly bool
	logger     *log.Logger
}

// NewHandler creates a new jailed SFTP handler.
// jailDir should be an absolute path. It will be resolved through EvalSymlinks
// to ensure consistent path comparisons (e.g. on macOS /var -> /private/var).
// quotaBytes of 0 means unlimited. When appendOnly is true, clients cannot
// overwrite, truncate, or delete existing files.
func NewHandler(jailDir string, quotaBytes int64, appendOnly bool, logger *log.Logger) *Handler {
	// Resolve the jail directory to its real path to avoid symlink mismatches
	// (e.g. macOS /var -> /private/var).
	realJail, err := filepath.EvalSymlinks(jailDir)
	if err != nil {
		// Fall back to the provided path if resolution fails.
		realJail = jailDir
	}

	h := &Handler{
		jailDir:    realJail,
		quotaBytes: quotaBytes,
		appendOnly: appendOnly,
		logger:     logger,
	}
	// Calculate initial disk usage for quota tracking.
	h.usedBytes.Store(calculateDirSize(realJail))
	return h
}

// calculateDirSize walks a directory tree and sums file sizes.
func calculateDirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// resolvePath maps an SFTP virtual path to a real filesystem path within the jail.
// It cleans the path, joins it with the jail root, resolves symlinks on the existing
// portion, and verifies the result is within the jail. Returns an error if the path
// escapes.
func (h *Handler) resolvePath(reqPath string) (string, error) {
	// Clean and make relative to jail.
	cleaned := filepath.Clean("/" + reqPath)
	if cleaned == "/" {
		return h.jailDir, nil
	}

	// Remove leading slash to get a relative path.
	rel := strings.TrimPrefix(cleaned, "/")
	candidate := filepath.Join(h.jailDir, rel)

	// Walk up to the deepest existing ancestor and resolve symlinks there,
	// then verify the full path stays within the jail.
	resolved, err := resolveWithinJail(h.jailDir, candidate)
	if err != nil {
		return "", err
	}

	return resolved, nil
}

// isLockPath reports whether the SFTP request path is under the locks/ directory.
// Lock files must be removable even in append-only mode.
func isLockPath(reqPath string) bool {
	cleaned := filepath.Clean("/" + reqPath)
	return strings.HasPrefix(cleaned, "/locks/")
}

// resolveWithinJail resolves symlinks on existing path components and verifies
// the resulting path is within jailDir. For paths where some trailing components
// don't yet exist (e.g. new file creation), it resolves the existing prefix and
// appends the remaining components.
func resolveWithinJail(jailDir, candidate string) (string, error) {
	// Try resolving the full path first. If it exists, EvalSymlinks works.
	real, err := filepath.EvalSymlinks(candidate)
	if err == nil {
		if !isWithinDir(jailDir, real) {
			return "", sftp.ErrSSHFxPermissionDenied
		}
		return real, nil
	}

	// Path doesn't fully exist. Find the existing prefix and resolve that.
	dir := filepath.Dir(candidate)
	base := filepath.Base(candidate)

	realDir, err := resolveWithinJail(jailDir, dir)
	if err != nil {
		return "", err
	}

	full := filepath.Join(realDir, base)
	if !isWithinDir(jailDir, full) {
		return "", sftp.ErrSSHFxPermissionDenied
	}

	return full, nil
}

// isWithinDir checks that child is lexically within or equal to parent.
// Both paths must be absolute and already cleaned/resolved.
func isWithinDir(parent, child string) bool {
	// Ensure parent ends with separator for prefix check (unless they're equal).
	if child == parent {
		return true
	}
	parentSlash := parent
	if !strings.HasSuffix(parentSlash, string(filepath.Separator)) {
		parentSlash += string(filepath.Separator)
	}
	return strings.HasPrefix(child, parentSlash)
}

// ---- FileReader implementation ----

// Fileread opens a file for reading. Implements sftp.FileReader.
func (h *Handler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if r.Method != "Get" {
		return nil, sftp.ErrSSHFxOpUnsupported
	}

	realPath, err := h.resolvePath(r.Filepath)
	if err != nil {
		return nil, err
	}

	h.logger.Debug("read", "path", r.Filepath, "real", realPath)

	f, err := os.Open(realPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, sftp.ErrSSHFxNoSuchFile
		}
		return nil, sftp.ErrSSHFxFailure
	}

	return f, nil
}

// ---- FileWriter implementation ----

// Filewrite opens a file for writing. Only new file creation is allowed.
// Overwriting or truncating existing files is rejected. Implements sftp.FileWriter.
func (h *Handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if r.Method != "Put" && r.Method != "Open" {
		return nil, sftp.ErrSSHFxOpUnsupported
	}

	realPath, err := h.resolvePath(r.Filepath)
	if err != nil {
		return nil, err
	}

	flags := r.Pflags()

	if h.appendOnly {
		// Reject truncation in append-only mode.
		if flags.Trunc {
			h.logger.Warn("rejected truncate", "path", r.Filepath)
			return nil, sftp.ErrSSHFxPermissionDenied
		}

		// If file already exists, reject the write (no overwrite).
		if _, statErr := os.Stat(realPath); statErr == nil {
			if !flags.Append {
				h.logger.Warn("rejected overwrite of existing file", "path", r.Filepath)
				return nil, sftp.ErrSSHFxPermissionDenied
			}
		}
	}

	h.logger.Debug("write", "path", r.Filepath, "real", realPath)

	// Ensure parent directory exists.
	dir := filepath.Dir(realPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, sftp.ErrSSHFxFailure
	}

	// Open with create + exclusive if truly new, otherwise allow append.
	openFlags := os.O_WRONLY | os.O_CREATE
	if flags.Append {
		openFlags |= os.O_APPEND
	} else {
		openFlags |= os.O_EXCL // fail if exists (new files only)
	}

	f, err := os.OpenFile(realPath, openFlags, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil, sftp.ErrSSHFxPermissionDenied
		}
		return nil, sftp.ErrSSHFxFailure
	}

	return &quotaWriter{
		file:    f,
		handler: h,
		path:    realPath,
	}, nil
}

// quotaWriter wraps a file with quota enforcement. It tracks bytes written
// and updates the handler's used bytes counter.
type quotaWriter struct {
	file    *os.File
	handler *Handler
	path    string
	written int64
}

func (qw *quotaWriter) WriteAt(p []byte, off int64) (int, error) {
	newBytes := int64(len(p))

	// Check quota before writing.
	if qw.handler.quotaBytes > 0 {
		qw.handler.mu.Lock()
		current := qw.handler.usedBytes.Load()
		if current+newBytes > qw.handler.quotaBytes {
			qw.handler.mu.Unlock()
			qw.handler.logger.Warn("quota exceeded", "path", qw.path,
				"used", current, "quota", qw.handler.quotaBytes, "attempted", newBytes)
			return 0, sftp.ErrSSHFxPermissionDenied
		}
		qw.handler.usedBytes.Add(newBytes)
		qw.handler.mu.Unlock()
	}

	n, err := qw.file.WriteAt(p, off)

	// If write failed or was short, return the over-reserved quota.
	if err != nil || int64(n) < newBytes {
		overReserved := newBytes - int64(n)
		if overReserved > 0 {
			qw.handler.usedBytes.Add(-overReserved)
		}
	}

	qw.written += int64(n)
	return n, err
}

func (qw *quotaWriter) Close() error {
	return qw.file.Close()
}

// ---- FileLister implementation ----

// Filelist returns a ListerAt for directory listing and stat operations.
// Implements sftp.FileLister.
func (h *Handler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	// Check for disallowed methods before resolving the path so that
	// Readlink returns OpUnsupported rather than a path-jailing error.
	switch r.Method {
	case "Readlink":
		// Readlink could be used to probe symlink targets. Deny it.
		return nil, sftp.ErrSSHFxOpUnsupported
	case "List", "Stat":
		// allowed -- continue below
	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}

	realPath, err := h.resolvePath(r.Filepath)
	if err != nil {
		return nil, err
	}

	switch r.Method {
	case "List":
		h.logger.Debug("list", "path", r.Filepath, "real", realPath)

		entries, err := os.ReadDir(realPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, sftp.ErrSSHFxNoSuchFile
			}
			return nil, sftp.ErrSSHFxFailure
		}

		infos := make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue // skip entries we can't stat
			}
			infos = append(infos, info)
		}

		return listerat(infos), nil

	case "Stat":
		h.logger.Debug("stat", "path", r.Filepath, "real", realPath)

		info, err := os.Stat(realPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, sftp.ErrSSHFxNoSuchFile
			}
			return nil, sftp.ErrSSHFxFailure
		}

		return listerat([]os.FileInfo{info}), nil

	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}
}

// listerat implements sftp.ListerAt over a slice of os.FileInfo.
type listerat []os.FileInfo

func (l listerat) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}

	n := copy(ls, l[offset:])
	if n+int(offset) >= len(l) {
		return n, io.EOF
	}
	return n, nil
}

// ---- FileCmder implementation ----

// Filecmd handles file commands. Allowed: Mkdir, Rename (no overwrite), Remove (files only).
// Rejected: Rmdir, Setstat, Link, Symlink.
// Implements sftp.FileCmder.
func (h *Handler) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Mkdir":
		realPath, err := h.resolvePath(r.Filepath)
		if err != nil {
			return err
		}

		h.logger.Debug("mkdir", "path", r.Filepath, "real", realPath)

		if err := os.MkdirAll(realPath, 0700); err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil // already exists, that's fine
			}
			return sftp.ErrSSHFxFailure
		}
		return nil

	case "Rename":
		srcPath, err := h.resolvePath(r.Filepath)
		if err != nil {
			return err
		}
		dstPath, err := h.resolvePath(r.Target)
		if err != nil {
			return err
		}

		// In append-only mode, reject overwriting existing files via rename.
		if h.appendOnly {
			if _, statErr := os.Stat(dstPath); statErr == nil {
				h.logger.Warn("rejected rename: target exists", "src", r.Filepath, "target", r.Target)
				return sftp.ErrSSHFxPermissionDenied
			}
		}

		h.logger.Debug("rename", "src", r.Filepath, "target", r.Target)
		if err := os.Rename(srcPath, dstPath); err != nil {
			return sftp.ErrSSHFxFailure
		}
		return nil

	case "Remove":
		if h.appendOnly && !isLockPath(r.Filepath) {
			h.logger.Warn("rejected remove in append-only mode", "path", r.Filepath)
			return sftp.ErrSSHFxPermissionDenied
		}

		realPath, err := h.resolvePath(r.Filepath)
		if err != nil {
			return err
		}

		// Only allow removing files, not directories.
		info, statErr := os.Stat(realPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return sftp.ErrSSHFxNoSuchFile
			}
			return sftp.ErrSSHFxFailure
		}
		if info.IsDir() {
			h.logger.Warn("rejected remove of directory", "path", r.Filepath)
			return sftp.ErrSSHFxOpUnsupported
		}

		h.logger.Debug("remove", "path", r.Filepath)

		size := info.Size()
		if err := os.Remove(realPath); err != nil {
			return sftp.ErrSSHFxFailure
		}
		// Update quota tracking.
		h.usedBytes.Add(-size)
		return nil

	case "Rmdir":
		h.logger.Warn("rejected rmdir", "path", r.Filepath)
		return sftp.ErrSSHFxOpUnsupported

	case "Setstat":
		// Silently succeed for setstat — many SFTP clients send setstat
		// after writes (e.g., to set permissions/mtime). Rejecting it
		// causes errors in legitimate client operations.
		return nil

	case "Link":
		h.logger.Warn("rejected hardlink", "path", r.Filepath, "target", r.Target)
		return sftp.ErrSSHFxOpUnsupported

	case "Symlink":
		h.logger.Warn("rejected symlink", "path", r.Filepath, "target", r.Target)
		return sftp.ErrSSHFxOpUnsupported

	default:
		h.logger.Warn("rejected unknown command", "method", r.Method, "path", r.Filepath)
		return sftp.ErrSSHFxOpUnsupported
	}
}
