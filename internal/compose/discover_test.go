package compose

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// write puts an empty file where a test needs a file to exist.
func write(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, nil, 0o644))
}

// configRunner answers `docker compose config` with a project per file.
type configRunner struct {
	byFile map[string]string
	fail   map[string]string
	calls  int
}

func (r *configRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	r.calls++
	file := args[2]
	if msg, bad := r.fail[file]; bad {
		return nil, []byte(msg), errors.New("exit status 1")
	}
	body, ok := r.byFile[file]
	if !ok {
		return nil, []byte("no configuration file provided"), errors.New("exit status 1")
	}
	return []byte(body), nil, nil
}

// project renders what `docker compose config` prints for the named project.
// Marshalling beats pasting the text together: a name carrying a quote would
// otherwise produce a document the reader cannot parse.
func project(name string) string {
	body, err := json.Marshal(map[string]any{
		"name":     name,
		"services": map[string]any{"db": map[string]any{"image": "postgres:16"}},
		"volumes":  map[string]any{"data": map[string]any{}},
		"networks": map[string]any{"default": map[string]any{}},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func labelled(project, files string) dockercli.Container {
	var c dockercli.Container
	c.Config.Labels = map[string]string{
		LabelProject:     project,
		LabelConfigFiles: files,
		LabelWorkingDir:  filepath.Dir(files),
	}
	return c
}

// The load-bearing performance claim: on a machine the tool already knows, a
// run is index lookups and stat calls. Nothing reads a directory.
func TestAKnownProjectCostsNoProbe(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "webapp", "compose.yaml")
	write(t, file)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{file}, seen)
	r := &configRunner{byFile: map[string]string{file: project("webapp")}}

	d := Discover(context.Background(), r, nil, []string{"webapp"}, Options{Index: idx, Now: seen})

	assert.True(t, d.Complete)
	assert.Equal(t, Alive, d.Resolve("webapp"))
}

// Container labels are exact and free, so a running stack needs no index entry
// and no look around the disk either.
func TestContainerLabelsResolveAProjectForFree(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "webapp", "compose.yaml")
	write(t, file)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	r := &configRunner{byFile: map[string]string{file: project("webapp")}}

	d := Discover(context.Background(), r, []dockercli.Container{labelled("webapp", file)},
		[]string{"webapp"}, Options{Index: idx, Now: seen})

	assert.Equal(t, Alive, d.Resolve("webapp"))
	assert.Equal(t, []string{file}, idx.Files("webapp"), "the label is remembered for after the down")
}

// A project whose containers are gone is found beside the ones docker still
// names, which is where compose put it. A crawl of the machine finds the same
// file and reads every other file on the disk to get there.
func TestAProjectIsFoundBesideTheOnesDockerNames(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "webapp", "compose.yaml")
	dead := filepath.Join(dir, "archive", "compose.yaml")
	write(t, live)
	write(t, dead)
	r := &configRunner{byFile: map[string]string{
		live: project("webapp"),
		dead: project("archive"),
	}}

	d := Discover(context.Background(), r, []dockercli.Container{labelled("webapp", live)},
		[]string{"webapp", "archive"},
		Options{Index: LoadIndex(filepath.Join(dir, "projects.json")), Now: seen})

	assert.Equal(t, Alive, d.Resolve("archive"))
}

// A project found this way is remembered, so the next run costs nothing.
func TestTheProbeFeedsTheIndex(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "webapp", "compose.yaml")
	dead := filepath.Join(dir, "archive", "compose.yaml")
	write(t, live)
	write(t, dead)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	r := &configRunner{byFile: map[string]string{
		live: project("webapp"),
		dead: project("archive"),
	}}

	Discover(context.Background(), r, []dockercli.Container{labelled("webapp", live)},
		[]string{"webapp", "archive"}, Options{Index: idx, Now: seen})

	assert.Equal(t, []string{dead}, idx.Files("archive"))
}

// Compose names a project after its directory, lowercased and stripped, so the
// directory rarely spells the name exactly.
func TestAProjectMatchesItsDirectoryAsComposeNamesIt(t *testing.T) {
	assert.Equal(t, "myapp", ProjectName("My.App"))
	assert.Equal(t, "compose-manager", ProjectName("compose-manager"))
	assert.Equal(t, "stack_one", ProjectName("Stack_One"))
	assert.Equal(t, "app", ProjectName("__app"))
}

// Deleting the compose file is what retires a project. Docker named that file
// already, so its absence is a fact rather than a failure to look.
func TestAVanishedFileDockerOnceNamedMeansDeleted(t *testing.T) {
	dir := t.TempDir()
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{filepath.Join(dir, "webapp", "compose.yaml")}, seen)

	d := Discover(context.Background(), &configRunner{}, nil, []string{"webapp"},
		Options{Index: idx, Now: seen})

	assert.True(t, d.Complete)
	assert.Equal(t, Deleted, d.Resolve("webapp"))
}

// The record that retires a project is kept while a resource still claims it,
// so a dry run and the real run that follows reach the same conclusion.
func TestTheEvidenceOfDeletionSurvivesTheRunThatFindsIt(t *testing.T) {
	dir := t.TempDir()
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{filepath.Join(dir, "webapp", "compose.yaml")}, seen)

	Discover(context.Background(), &configRunner{}, nil, []string{"webapp"}, Options{Index: idx, Now: seen})
	again := Discover(context.Background(), &configRunner{}, nil, []string{"webapp"}, Options{Index: idx, Now: seen})

	assert.Equal(t, Deleted, again.Resolve("webapp"))
}

// After no docker resource claims a project, nothing is left to remember.
func TestAnUnclaimedProjectLeavesTheIndex(t *testing.T) {
	dir := t.TempDir()
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{filepath.Join(dir, "webapp", "compose.yaml")}, seen)

	Discover(context.Background(), &configRunner{}, nil, nil, Options{Index: idx, Now: seen})

	assert.Empty(t, idx.Projects)
}

// A project nothing ever named a file for is unknown, not deleted. Not looking
// in the right place is never evidence.
func TestAProjectNothingEverNamedIsUnknown(t *testing.T) {
	d := Discover(context.Background(), &configRunner{}, nil, []string{"mystery"},
		Options{Index: LoadIndex(filepath.Join(t.TempDir(), "projects.json")), Now: seen})

	assert.Equal(t, Unknown, d.Resolve("mystery"))
}

// A compose file that would not render may name the missing project, so the run
// says so rather than retiring anything.
func TestAnUnreadableComposeFileMeansUnknown(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "webapp", "compose.yaml")
	write(t, file)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{file}, seen)
	r := &configRunner{fail: map[string]string{file: "yaml: line 3: did not find expected key"}}

	d := Discover(context.Background(), r, nil, []string{"webapp"}, Options{Index: idx, Now: seen})

	assert.False(t, d.Complete)
	assert.Equal(t, Unknown, d.Resolve("webapp"))
}

// A directory the probe cannot read is reported, so the reader knows the look
// around was not the whole story.
func TestAnUnreadableDirectoryIsReported(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.MkdirAll(filepath.Join(locked, "webapp"), 0o755))
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("root reads a 0000 directory anyway")
	}
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("other", []string{filepath.Join(locked, "other", "compose.yaml")}, seen)

	d := Discover(context.Background(), &configRunner{}, nil, []string{"webapp"},
		Options{Index: idx, Now: seen})

	require.NotEmpty(t, d.Failures)
	assert.Contains(t, d.Failures[0], locked)
}

func TestTheIndexWarningReachesTheReport(t *testing.T) {
	idx := LoadIndex(filepath.Join(t.TempDir(), "projects.json"))
	idx.Warning = "cannot write the project index"

	d := Discover(context.Background(), &configRunner{}, nil, []string{"gone"}, Options{Index: idx, Now: seen})

	assert.Contains(t, d.Warning, "cannot write")
}

func TestConfigFilesLabelIsSplitOnCommas(t *testing.T) {
	assert.Equal(t, []string{"/a/compose.yaml", "/a/override.yaml"},
		splitList("/a/compose.yaml, /a/override.yaml"))
	assert.Nil(t, splitList(""))
	assert.Nil(t, splitList(" , "))
}
