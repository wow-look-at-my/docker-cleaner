package compose

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var seen = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func indexFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projects.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestIndexRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "projects.json")
	file := filepath.Join(t.TempDir(), "compose.yaml")
	write(t, file)

	idx := LoadIndex(path)
	idx.Record("webapp", []string{file}, seen)
	idx.Save()

	reloaded := LoadIndex(path)
	assert.Empty(t, reloaded.Warning)
	assert.Equal(t, []string{file}, reloaded.Files("webapp"))
}

// A path is stated, checked, and only then believed. Otherwise a stale entry
// would keep a deleted project alive forever.
func TestARecordedPathIsCheckedBeforeItIsBelieved(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "compose.yaml")
	write(t, gone)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{gone, dir}, seen)

	assert.Equal(t, []string{gone}, idx.Files("webapp"), "a directory is not a compose file")

	require.NoError(t, os.Remove(gone))
	assert.Empty(t, idx.Files("webapp"))
}

func TestRecordUnionsFilesAndIgnoresJunk(t *testing.T) {
	idx := LoadIndex(filepath.Join(t.TempDir(), "projects.json"))

	idx.Record("webapp", []string{"/b/compose.yaml"}, seen)
	idx.Record("webapp", []string{"/a/compose.yaml", "/b/compose.yaml", ""}, seen)
	idx.Record("", []string{"/c/compose.yaml"}, seen)
	idx.Record("empty", nil, seen)

	assert.Equal(t, []string{"/a/compose.yaml", "/b/compose.yaml"}, idx.Projects["webapp"].Files)
	assert.Len(t, idx.Projects, 1)
}

func TestForgetDropsAProject(t *testing.T) {
	idx := LoadIndex(filepath.Join(t.TempDir(), "projects.json"))
	idx.Record("webapp", []string{"/a/compose.yaml"}, seen)

	idx.Forget("webapp")

	assert.Empty(t, idx.Projects)
}

// A half-parsed index reads as "those projects are gone", which is exactly the
// conclusion that deletes data. Every damaged shape is discarded instead.
func TestDamagedIndexIsDiscardedAndRebuilt(t *testing.T) {
	for name, body := range map[string]string{
		"corrupt":   "{\"schema\": 1, \"projects\": {",
		"truncated": "",
		"garbage":   "not json at all",
		"future":    "{\"schema\": 99, \"projects\": {\"webapp\": {\"files\": [\"/a/compose.yaml\"]}}}",
	} {
		idx := LoadIndex(indexFile(t, body))

		assert.Empty(t, idx.Projects, name)
		assert.NotEmpty(t, idx.Warning, name)
		assert.Equal(t, indexSchema, idx.Schema, name)
	}
}

func TestMissingIndexIsNormal(t *testing.T) {
	idx := LoadIndex(filepath.Join(t.TempDir(), "projects.json"))

	assert.Empty(t, idx.Warning)
	assert.Empty(t, idx.Projects)
}

// A non-root run cannot write /var/lib. That has to cost speed, never the run.
func TestUnwritableIndexWarnsInsteadOfFailing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	idx := LoadIndex(filepath.Join(dir, "sub", "projects.json"))
	idx.Record("webapp", []string{"/a/compose.yaml"}, seen)

	idx.Save()

	assert.Contains(t, idx.Warning, "cannot write the project index")
	assert.Equal(t, []string{"/a/compose.yaml"}, idx.Projects["webapp"].Files, "still usable in memory")
}

// A watcher and a manual run write the same file. The rename is atomic, so a
// reader sees a whole index rather than a truncated file.
func TestConcurrentSavesLeaveAReadableIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	file := filepath.Join(t.TempDir(), "compose.yaml")
	write(t, file)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx := LoadIndex(path)
			idx.Record("p"+string(rune('a'+i)), []string{file}, seen)
			idx.Save()
		}()
	}
	wg.Wait()

	final := LoadIndex(path)
	assert.Empty(t, final.Warning, "no writer left a half-written file behind")
	assert.NotEmpty(t, final.Projects)
}

func TestSaveIsANoOpWithoutAPath(t *testing.T) {
	idx := LoadIndex("")
	idx.Record("webapp", []string{"/a/compose.yaml"}, seen)

	idx.Save()

	assert.Empty(t, idx.Warning)
}
