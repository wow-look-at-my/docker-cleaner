package report

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// A volume the walk could not read has no size, and printing it as empty says
// the run measured something it never measured.
func TestAnUnmeasuredVolumeIsCountedButNotSized(t *testing.T) {
	t.Parallel()

	out := Summary(dockercli.DiskUsage{Volumes: []dockercli.VolumeUsage{
		{Name: "read", Size: 1 << 20, Measured: true},
		{Name: "unreadable"},
	}}, "")

	assert.Contains(t, out, "Local Volumes          2        1.0MB  +1 unmeasured")
}

// Volumes none of which were measured have a known count and no size at all.
// Summing them reports every volume empty.
func TestVolumesNoneOfWhichWereMeasuredHaveNoSize(t *testing.T) {
	t.Parallel()

	out := Summary(dockercli.DiskUsage{Volumes: []dockercli.VolumeUsage{
		{Name: "alpha"}, {Name: "beta"},
	}}, "")

	assert.Contains(t, out, "Local Volumes          2            ?  +2 unmeasured")
}


// `buildx du` failing leaves no records. Rendering that as 0B reports an empty
// cache, which is a measurement nobody made.
func TestAnUnreadableBuildCacheReadsAsUnknownNotEmpty(t *testing.T) {
	t.Parallel()

	out := Summary(dockercli.DiskUsage{}, "cannot read build cache: exit status 1")

	assert.Contains(t, out, "Build Cache            ?            ?  not readable")
	assert.NotContains(t, out, "Build Cache            0           0B")
}

// A cache that really is empty still reads as empty.
func TestAReadableEmptyBuildCacheReadsAsZero(t *testing.T) {
	t.Parallel()

	out := Summary(dockercli.DiskUsage{}, "")

	assert.Contains(t, out, "Build Cache            0           0B")
}
