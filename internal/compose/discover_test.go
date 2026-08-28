package compose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// forbiddenScanner fails the test if the run walks the disk at all.
type forbiddenScanner struct{ t *testing.T }

func (s forbiddenScanner) Scan() (ScanResult, error) {
	s.t.Fatal("the index answered every project, so nothing should have walked the disk")
	return ScanResult{}, nil
}

type stubScanner struct {
	res    ScanResult
	err    error
	called int
}

func (s *stubScanner) Scan() (ScanResult, error) {
	s.called++
	return s.res, s.err
}

// configRunner answers `docker compose config` with one project per file.
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

func project(name string) string {
	return `{"name":"` + name + `","services":{"db":{"image":"postgres:16"}},` +
		`"volumes":{"data":{}},"networks":{"default":{}}}`
}

func labelled(project, files string) dockercli.Container {
	var c dockercli.Container
	c.Config.Labels = map[string]string{LabelProject: project, LabelConfigFiles: files}
	return c
}

// The load-bearing performance claim: on a machine the tool already knows, a
// run is index lookups and stat calls. Nothing touches the filesystem tree.
func TestAKnownProjectCostsNoWalk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	write(t, file)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{file}, seen)
	r := &configRunner{byFile: map[string]string{file: project("webapp")}}

	d := Discover(context.Background(), r, nil, []string{"webapp"},
		Options{Index: idx, Scanner: forbiddenScanner{t}, Now: seen})

	assert.Zero(t, d.DirsWalked)
	assert.True(t, d.Complete)
	assert.Equal(t, Alive, d.Resolve("webapp"))
}

// Container labels are exact and free, so a running stack needs no index entry
// and no walk either.
func TestContainerLabelsResolveAProjectForFree(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	write(t, file)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	r := &configRunner{byFile: map[string]string{file: project("webapp")}}

	d := Discover(context.Background(), r, []dockercli.Container{labelled("webapp", file)},
		[]string{"webapp"}, Options{Index: idx, Scanner: forbiddenScanner{t}, Now: seen})

	assert.Equal(t, Alive, d.Resolve("webapp"))
	assert.Equal(t, []string{file}, idx.Files("webapp"), "the label is remembered for after the down")
}

func TestAnUnknownProjectTriggersTheWalk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	write(t, file)
	scanner := &stubScanner{res: ScanResult{Files: []string{file}, Dirs: 42}}
	r := &configRunner{byFile: map[string]string{file: project("webapp")}}

	d := Discover(context.Background(), r, nil, []string{"webapp"},
		Options{Index: LoadIndex(filepath.Join(dir, "projects.json")), Scanner: scanner, Now: seen})

	assert.Equal(t, 1, scanner.called)
	assert.Equal(t, 42, d.DirsWalked)
	assert.Equal(t, Alive, d.Resolve("webapp"))
}

func TestRescanWalksEvenWhenTheIndexAnswers(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	write(t, file)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{file}, seen)
	scanner := &stubScanner{res: ScanResult{Files: []string{file}, Dirs: 7}}
	r := &configRunner{byFile: map[string]string{file: project("webapp")}}

	d := Discover(context.Background(), r, nil, []string{"webapp"},
		Options{Index: idx, Scanner: scanner, Rescan: true, Now: seen})

	assert.Equal(t, 1, scanner.called)
	assert.Equal(t, 7, d.DirsWalked)
}

// Deleting the compose file is what retires a project, and an exhaustive
// search is what makes that safe to act on.
func TestAnExhaustiveSearchThatFindsNothingMeansDeleted(t *testing.T) {
	dir := t.TempDir()
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Record("webapp", []string{filepath.Join(dir, "compose.yaml")}, seen)
	scanner := &stubScanner{res: ScanResult{Dirs: 100}}

	d := Discover(context.Background(), &configRunner{}, nil, []string{"webapp"},
		Options{Index: idx, Scanner: scanner, Now: seen})

	assert.True(t, d.Complete)
	assert.Equal(t, Deleted, d.Resolve("webapp"))
	assert.Empty(t, idx.Projects, "the index tracks the disk, so a dead project is dropped")
}

// Not looking is never evidence of deletion.
func TestAnIncompleteSearchMeansUnknown(t *testing.T) {
	scanner := &stubScanner{res: ScanResult{Failures: []string{"/srv: permission denied"}}}

	d := Discover(context.Background(), &configRunner{}, nil, []string{"webapp"},
		Options{Index: LoadIndex(filepath.Join(t.TempDir(), "projects.json")), Scanner: scanner, Now: seen})

	assert.False(t, d.Complete)
	assert.Equal(t, Unknown, d.Resolve("webapp"))
	assert.Equal(t, []string{"/srv: permission denied"}, d.Failures)
}

func TestAScannerErrorIsAlsoIncomplete(t *testing.T) {
	scanner := &stubScanner{err: errors.New("cannot read /proc/self/mountinfo")}

	d := Discover(context.Background(), &configRunner{}, nil, []string{"webapp"},
		Options{Index: LoadIndex(filepath.Join(t.TempDir(), "projects.json")), Scanner: scanner, Now: seen})

	assert.False(t, d.Complete)
	assert.Equal(t, Unknown, d.Resolve("webapp"))
	assert.Contains(t, d.Failures[0], "mountinfo")
}

// A stack downed before the tool was installed leaves nothing to read labels
// from, so the walk finds the file and the index remembers it from then on.
func TestTheWalkFeedsTheIndex(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	write(t, file)
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	r := &configRunner{byFile: map[string]string{file: project("webapp")}}

	Discover(context.Background(), r, nil, []string{"webapp"},
		Options{Index: idx, Scanner: &stubScanner{res: ScanResult{Files: []string{file}}}, Now: seen})

	assert.Equal(t, []string{file}, idx.Files("webapp"))
}

func TestSkippedMountsAndWarningsReachTheReport(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o755))
	idx := LoadIndex(filepath.Join(dir, "projects.json"))
	idx.Warning = "cannot write the project index"
	scanner := &stubScanner{res: ScanResult{Skipped: []Mount{{Point: "/proc", Skip: "kernel filesystem (proc)"}}}}

	d := Discover(context.Background(), &configRunner{}, nil, []string{"gone"},
		Options{Index: idx, Scanner: scanner, Now: seen})

	require.Len(t, d.Skipped, 1)
	assert.Equal(t, "/proc", d.Skipped[0].Point)
	assert.Contains(t, d.Warning, "cannot write")
}

func TestConfigFilesLabelIsSplitOnCommas(t *testing.T) {
	assert.Equal(t, []string{"/a/compose.yaml", "/a/override.yaml"},
		splitList("/a/compose.yaml, /a/override.yaml"))
	assert.Nil(t, splitList(""))
	assert.Nil(t, splitList(" , "))
}
