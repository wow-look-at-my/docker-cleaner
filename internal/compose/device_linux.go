//go:build linux

package compose

import (
	"os"
	"syscall"
)

// deviceOf identifies the filesystem a path sits on, so a walk can stop at a
// mount boundary instead of descending into a filesystem it will visit on its
// own terms.
func deviceOf(path string) (uint64, bool) {
	st, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(sys.Dev), true
}
