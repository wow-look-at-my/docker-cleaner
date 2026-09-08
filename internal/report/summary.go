package report

import (
	"fmt"
	"strings"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// Summary renders what the machine holds, out of the sizes the run measured.
// Shelling out to `docker system df` for this table makes the daemon walk every
// volume, which is the slowest thing it does and the reason it is not used.
//
// A volume nothing could read is counted in the total and left out of the size,
// and the row says how many those are.
func Summary(du dockercli.DiskUsage) string {
	rows := []struct {
		label string
		total int
		size  int64
		note  string
	}{
		{label: "Images", total: len(du.Images)},
		{label: "Containers", total: len(du.Containers)},
		{label: "Local Volumes", total: len(du.Volumes)},
		{label: "Build Cache", total: len(du.BuildCache)},
	}

	for _, i := range du.Images {
		rows[0].size += i.Size
	}
	for _, c := range du.Containers {
		rows[1].size += c.SizeRw
	}
	unmeasured := 0
	for _, v := range du.Volumes {
		rows[2].size += v.Size
		if !v.Measured {
			unmeasured++
		}
	}
	if unmeasured > 0 {
		rows[2].note = fmt.Sprintf("+%d unmeasured", unmeasured)
	}
	for _, b := range du.BuildCache {
		rows[3].size += b.Size
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-15s %8s %12s  %s\n", "TYPE", "TOTAL", "SIZE", "")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-15s %8d %12s  %s\n", r.label, r.total, Bytes(r.size), r.note)
	}
	return b.String()
}
