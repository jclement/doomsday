package backup

import (
	"os"
)

// getDeviceID returns the device ID from file info (for one_filesystem support).
// Windows does not expose device IDs via syscall, so always returns 0.
func getDeviceID(_ os.FileInfo) uint64 {
	return 0
}

// extractMetadata reads extended metadata from file info.
// Windows does not expose Unix-style metadata (uid, gid, inode, etc.).
func extractMetadata(info os.FileInfo) fileMetadata {
	return fileMetadata{
		Mode:    info.Mode(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
}
