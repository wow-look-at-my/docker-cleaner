package dockercli

import (
	"fmt"
	"strings"
)

// Summary renders what the machine holds, out of the disk-usage pass the run
// already made. Shelling out to `docker system df` for this table instead makes
// the daemon walk every volume again, which is the slowest thing it does.
//
// Reclaimable counts what nothing refers to now. It answers a different
// question from the plan below it, which also weighs age and compose projects.
func Summary(du DiskUsage) string {
	rows := []struct {
		label string
		total int
		size  int64
		free  int64
	}{
		{"Images", len(du.Images), 0, 0},
		{"Containers", len(du.Containers), 0, 0},
		{"Local Volumes", len(du.Volumes), 0, 0},
		{"Build Cache", len(du.BuildCache), 0, 0},
	}

	for _, i := range du.Images {
		rows[0].size += i.Size
		if i.Containers == 0 {
			rows[0].free += i.Size
		}
	}
	for _, c := range du.Containers {
		rows[1].size += c.SizeRw
		rows[1].free += c.SizeRw
	}
	for _, v := range du.Volumes {
		rows[2].size += v.UsageData.Size
		if v.UsageData.RefCount == 0 {
			rows[2].free += v.UsageData.Size
		}
	}
	for _, b := range du.BuildCache {
		rows[3].size += b.Size
		if !b.InUse {
			rows[3].free += b.Size
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-15s %8s %12s %12s\n", "TYPE", "TOTAL", "SIZE", "RECLAIMABLE")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-15s %8d %12s %12s\n", r.label, r.total, Bytes(r.size), Bytes(r.free))
	}
	return b.String()
}

// Bytes renders a size the way docker does, in decimal units.
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
	return fmt.Sprintf("%.3g%cB", float64(n)/float64(div), "kMGTPE"[exp])
}
