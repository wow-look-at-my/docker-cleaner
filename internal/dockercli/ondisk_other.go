//go:build !unix

package dockercli

import "io/fs"

// onDisk falls back to the reported length where no stat block count exists.
func onDisk(info fs.FileInfo) int64 { return info.Size() }
