//go:build unix

package dockercli

import (
	"io/fs"
	"syscall"
)

// blockSize is the unit st_blocks counts, fixed by the stat contract.
const blockSize = 512

// onDisk is what removing a file gives back: the blocks it occupies, not the
// length it reports. A sparse file holds fewer.
func onDisk(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * blockSize
	}
	return info.Size()
}
