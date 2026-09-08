package dockercli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mountWith builds a volume directory holding the given bytes.
func mountWith(t *testing.T, name string, bytes int) Volume {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name, "_data")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "blob"), make([]byte, bytes), 0o644))
	return Volume{Name: name, Mountpoint: dir}
}

// The size of a volume is the bytes under its mountpoint, counted through every
// directory below it.
func TestAVolumeIsMeasuredThroughItsWholeTree(t *testing.T) {
	t.Parallel()

	sizes := MeasureVolumes(context.Background(), []Volume{
		mountWith(t, "small", 128),
		mountWith(t, "large", 4096),
	}, nil)

	assert.Equal(t, int64(128), sizes["small"])
	assert.Equal(t, int64(4096), sizes["large"])
}

// A volume nothing can read is absent from the result. Reporting it as empty
// would be a measurement nobody made, so the report says "unmeasured".
func TestAnUnreadableVolumeIsAbsentRatherThanEmpty(t *testing.T) {
	t.Parallel()

	sizes := MeasureVolumes(context.Background(), []Volume{
		{Name: "gone", Mountpoint: filepath.Join(t.TempDir(), "no-such-directory")},
		{Name: "remote", Mountpoint: ""},
		mountWith(t, "real", 64),
	}, nil)

	assert.NotContains(t, sizes, "gone")
	assert.NotContains(t, sizes, "remote")
	assert.Equal(t, int64(64), sizes["real"])
}

// An interrupt has to reach the walk, which means watching the context rather
// than working through every volume.
func TestMeasuringStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sizes := MeasureVolumes(ctx, []Volume{mountWith(t, "any", 32)}, nil)

	assert.Empty(t, sizes)
}
