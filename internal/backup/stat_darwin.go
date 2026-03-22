package backup

import (
	"os"
	"syscall"
	"time"
)

// getDeviceID returns the device ID from file info (for one_filesystem support).
func getDeviceID(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev)
	}
	return 0
}

// extractMetadata reads extended metadata from file info.
func extractMetadata(info os.FileInfo) fileMetadata {
	m := fileMetadata{
		Mode:    info.Mode(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		m.UID = stat.Uid
		m.GID = stat.Gid
		m.Inode = stat.Ino
		m.Links = uint64(stat.Nlink)
		m.AccessTime = time.Unix(stat.Atimespec.Sec, stat.Atimespec.Nsec)
		m.ChangeTime = time.Unix(stat.Ctimespec.Sec, stat.Ctimespec.Nsec)
	}
	return m
}
