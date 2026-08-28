package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("services: {}\n"), 0o644))
}

// The walk searches by filename, so a project directory that moved since it was
// last seen is still found, at whatever depth it now sits.
func TestWalkFindsEverySpellingAtEveryDepth(t *testing.T) {
	root := t.TempDir()
	want := []string{
		filepath.Join(root, "compose.yaml"),
		filepath.Join(root, "a", "compose.yml"),
		filepath.Join(root, "a", "b", "docker-compose.yaml"),
		filepath.Join(root, "a", "b", "c", "d", "docker-compose.yml"),
	}
	for _, f := range want {
		write(t, f)
	}
	write(t, filepath.Join(root, "a", "notes.yaml"))

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}}).Scan()

	require.NoError(t, err)
	assert.Equal(t, want, res.Files)
	assert.True(t, res.Complete())
	assert.Positive(t, res.Dirs)
}

// A symlink is either a second route to something already walked or a route
// off this filesystem, so following one only costs time.
func TestWalkDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()
	write(t, filepath.Join(real, "compose.yaml"))
	require.NoError(t, os.Symlink(real, filepath.Join(root, "link")))
	require.NoError(t, os.Symlink(filepath.Join(real, "compose.yaml"), filepath.Join(root, "compose.yaml")))

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}}).Scan()

	require.NoError(t, err)
	assert.Empty(t, res.Files)
}

func TestWalkSkipsGitDirectories(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "modules", "x", "compose.yaml"))
	write(t, filepath.Join(root, "compose.yaml"))

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}}).Scan()

	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "compose.yaml")}, res.Files)
}

// The whole escape hatch rests on having actually looked, so a directory the
// walk could not read has to show up as a failure. Reporting it as an empty
// result would turn "I could not look there" into "that project is gone".
func TestUnreadableDirectoryIsReportedAndBreaksCompleteness(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory anyway")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	write(t, filepath.Join(locked, "compose.yaml"))
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	res, err := (&FSScanner{Mounts: []Mount{{Point: root}}}).Scan()

	require.NoError(t, err)
	assert.False(t, res.Complete())
	require.Len(t, res.Failures, 1)
	assert.Contains(t, res.Failures[0], locked)
}

// Every skipped mount is carried out of the scan, because skipping anything is
// a judgement the user should be able to see.
func TestSkippedMountsAreReportedNotDropped(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "compose.yaml"))
	mounts := []Mount{
		{Point: root},
		{Point: "/proc", FSType: "proc", Skip: "kernel filesystem (proc)"},
	}

	res, err := (&FSScanner{Mounts: mounts, Workers: 2}).Scan()

	require.NoError(t, err)
	require.Len(t, res.Skipped, 1)
	assert.Equal(t, "/proc", res.Skipped[0].Point)
	assert.Len(t, res.Files, 1)
}

func TestUnstattableMountIsAFailure(t *testing.T) {
	res, err := (&FSScanner{Mounts: []Mount{{Point: "/definitely/not/here"}}}).Scan()

	require.NoError(t, err)
	assert.False(t, res.Complete())
}

// Several mounts are walked in parallel and their results must all survive.
func TestEveryMountIsWalked(t *testing.T) {
	var mounts []Mount
	var want []string
	for range 8 {
		root := t.TempDir()
		f := filepath.Join(root, "compose.yaml")
		write(t, f)
		mounts = append(mounts, Mount{Point: root})
		want = append(want, f)
	}

	res, err := (&FSScanner{Mounts: mounts, Workers: 4}).Scan()

	require.NoError(t, err)
	assert.ElementsMatch(t, want, res.Files)
}
