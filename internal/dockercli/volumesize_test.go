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

// The size of a volume is what its files occupy under the mountpoint, counted
// through every directory below it.
func TestAVolumeIsMeasuredThroughItsWholeTree(t *testing.T) {
	t.Parallel()

	sizes := MeasureVolumes(context.Background(), []Volume{
		mountWith(t, "small", 128),
		mountWith(t, "large", 1<<20),
	}, nil)

	assert.GreaterOrEqual(t, sizes["small"], int64(128))
	assert.GreaterOrEqual(t, sizes["large"], int64(1<<20))
	assert.Less(t, sizes["small"], sizes["large"])
}

// Removing a volume gives back the blocks its files occupy, not the lengths
// they report. A sparse file reports far more than it holds.
func TestASparseFileIsMeasuredByWhatItOccupies(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "sparse", "_data")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	f, err := os.Create(filepath.Join(dir, "hole"))
	require.NoError(t, err)
	require.NoError(t, f.Truncate(1<<30))
	require.NoError(t, f.Close())

	sizes := MeasureVolumes(context.Background(), []Volume{{Name: "sparse", Mountpoint: dir}}, nil)

	assert.Less(t, sizes["sparse"], int64(1<<20), "a hole occupies nothing")
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
	assert.Contains(t, sizes, "real")
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
