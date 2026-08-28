// Package report renders a plan for a person and for a machine. Every number
// it prints comes from docker's JSON output, never from parsing a human table.
package report

import "fmt"

// Bytes renders a size the way docker does.
func Bytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "kMGTPE"[exp])
}
