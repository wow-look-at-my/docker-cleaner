//go:build !linux

package compose

import "os"

// identify cannot name a file by device and inode portably, so off Linux the
// path stands in. The walk there does not stop at mount boundaries; it still
// refuses to follow symlinks, it still stops at its depth limit, and mount
// discovery itself needs /proc/self/mountinfo, so this path is only reached
// when a caller supplies its own roots.
func identify(path string) (fileID, bool) {
	if _, err := os.Lstat(path); err != nil {
		return fileID{}, false
	}
	return fileID{path: path}, true
}
