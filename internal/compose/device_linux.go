//go:build linux

package compose

import (
	"os"
	"syscall"
)

// identify names a directory by device and inode. The device stops a walk at a
// mount boundary. The inode marks a directory the walk has already read, which
// ends a descent that leads back into the tree.
func identify(path string) (fileID, bool) {
	st, err := os.Lstat(path)
	if err != nil {
		return fileID{}, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{path: path}, true
	}
	return fileID{dev: uint64(sys.Dev), ino: uint64(sys.Ino)}, true
}
