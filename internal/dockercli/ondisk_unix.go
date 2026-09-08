//go:build unix

package dockercli

import (
	"io/fs"
	"syscall"
)

// blockSize is the unit st_blocks counts, fixed by the stat contract whatever
// the filesystem's own block size is.
const blockSize = 512

// onDisk is what removing a file gives back, which is the blocks it occupies
// rather than the length it reports. A sparse file holds fewer, and a small
// file in a large block holds more.
func onDisk(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * blockSize
	}
	return info.Size()
}
