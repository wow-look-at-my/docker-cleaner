//go:build !linux

package compose

import "os"

// deviceOf cannot identify a filesystem portably. Off Linux the walk therefore
// does not stop at mount boundaries; it still refuses to follow symlinks, and
// mount discovery itself needs /proc/self/mountinfo, so this path is only
// reached when a caller supplies its own roots.
func deviceOf(path string) (uint64, bool) {
	if _, err := os.Lstat(path); err != nil {
		return 0, false
	}
	return 0, true
}
