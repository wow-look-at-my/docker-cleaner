package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cancelled search must end, and it must say that it ended early. Silence
// here would let "no compose file found" mean "the project was deleted", which
// is the difference between keeping a volume and deleting it.
func TestACancelledScanStopsAndReportsItself(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "deep", "compose.yaml"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}}).Scan(ctx)

	require.NoError(t, err)
	assert.False(t, res.Complete())
	require.NotEmpty(t, res.Failures)
	assert.Contains(t, res.Failures[0], "context canceled")
}

// The deadline is what makes the walk safe to start on an unknown machine: a
// directory read that never returns costs the run its patience, not its life.
func TestAScanPastItsDeadlineStopsAndReportsItself(t *testing.T) {
	root := t.TempDir()
	for i := range 200 {
		write(t, filepath.Join(root, strconv.Itoa(i), "compose.yaml"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}}).Scan(ctx)

	require.NoError(t, err)
	assert.False(t, res.Complete())
}

// A directory that leads back into the tree used to cost the walk everything:
// it read the same names again at every level for as long as anybody waited.
// The depth limit ends the descent and says where it stopped, which keeps the
// projects under that branch rather than calling them deleted.
func TestADescentThatNeverEndsStopsAtTheDepthLimit(t *testing.T) {
	root := t.TempDir()
	deep := root
	for range 12 {
		deep = filepath.Join(deep, "down")
	}
	require.NoError(t, os.MkdirAll(deep, 0o755))
	write(t, filepath.Join(deep, "compose.yaml"))

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}, MaxDepth: 4}).Scan(context.Background())

	require.NoError(t, err)
	assert.False(t, res.Complete())
	assert.Contains(t, res.Failures[0], "deeper than 4 directories")
	assert.Empty(t, res.Files, "the file sits under the branch the walk refused")
}

// A bind mount can lead the walk back to the top of the tree it is already
// walking. Recognising the directory by device and inode is what ends it: by
// path, every route is a new name for the same work.
func TestABindMountBackToTheRootIsReadOnlyTheTimeItMustBe(t *testing.T) {
	root := t.TempDir()
	for i := range 20 {
		require.NoError(t, os.Mkdir(filepath.Join(root, "d"+strconv.Itoa(i)), 0o755))
	}
	back := filepath.Join(root, "back")
	require.NoError(t, os.Mkdir(back, 0o755))
	if err := exec.Command("mount", "--bind", root, back).Run(); err != nil {
		t.Skip("a bind mount is what this test measures, and this machine refuses:", err)
	}
	t.Cleanup(func() { _ = exec.Command("umount", back).Run() })

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}}).Scan(context.Background())

	require.NoError(t, err)
	assert.True(t, res.Complete())
	assert.Equal(t, 21, res.Dirs, "the root and its subdirectories, each read the time it must be")
}

// A walk under the limit reports nothing about depth, so the limit never turns
// an ordinary machine into an incomplete search.
func TestAnOrdinaryDepthIsNotReported(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "b", "compose.yaml"))

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}, MaxDepth: 8}).Scan(context.Background())

	require.NoError(t, err)
	assert.True(t, res.Complete())
	assert.Equal(t, []string{filepath.Join(root, "a", "b", "compose.yaml")}, res.Files)
}
