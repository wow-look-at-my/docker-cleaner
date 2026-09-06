package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
	"github.com/wow-look-at-my/docker-cleaner/internal/report"
)

var now = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }

// inspectDoc renders what `docker inspect` prints. A name carrying a quote
// breaks a document pasted together, so this marshals it.
func inspectDoc(objects ...map[string]any) string {
	body, err := json.Marshal(objects)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func imageDoc(id, tag, created string, size int64) map[string]any {
	return map[string]any{"Id": id, "RepoTags": []string{tag}, "Created": created, "Size": size}
}

// docker is a stand-in daemon: it answers the read phase from strings and
// records every command the apply phase issues.
type docker struct {
	calls []string
	fail  map[string]string
	reads map[string]string
}

func newDocker() *docker {
	return &docker{fail: map[string]string{}, reads: map[string]string{
		"version --format json": `{"Client":{"Version":"29.3.1"},"Server":{"Version":"29.3.1"}}`,
		"system df":             "TYPE     TOTAL  ACTIVE  SIZE   RECLAIMABLE\nImages   2      1       384MB  134MB\n",
		"system df -v --format json": `{"LayersSize":1,"Images":[{"Id":"sha256:i1","Size":268435456}],` +
			`"Containers":[{"Id":"c1","SizeRw":4096}],"Volumes":[],"BuildCache":[]}`,
		"ps -aq --no-trunc": "c1\n",
		"container inspect c1": inspectDoc(map[string]any{
			"Id": "c1", "Name": "/web-old", "Image": "sha256:i1",
			"Created":         ago(300 * 24 * time.Hour),
			"State":           map[string]any{"Status": "exited", "FinishedAt": ago(47 * 24 * time.Hour)},
			"Config":          map[string]any{"Image": "myapp:v1", "Labels": map[string]string{}},
			"Mounts":          []any{},
			"NetworkSettings": map[string]any{"Networks": map[string]any{}},
		}),
		"image ls --no-trunc --format json": "{\"ID\":\"sha256:i1\"}\n{\"ID\":\"sha256:i2\"}\n",
		"image inspect sha256:i1 sha256:i2": inspectDoc(
			imageDoc("sha256:i1", "myapp:v1", ago(200*24*time.Hour), 268435456),
			imageDoc("sha256:i2", "myapp:v2", ago(10*24*time.Hour), 1000),
		),
		"volume ls --format json":             "",
		"network ls --no-trunc --format json": "",
		"buildx ls --format json":             "",
	}}
}

func (d *docker) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	joined := strings.Join(args, " ")
	d.calls = append(d.calls, joined)
	if msg, bad := d.fail[joined]; bad {
		return nil, []byte(msg), errors.New("exit status 1")
	}
	if out, ok := d.reads[joined]; ok {
		return []byte(out), nil, nil
	}
	if strings.HasPrefix(joined, "compose ") {
		return nil, []byte("no configuration file provided"), errors.New("exit status 1")
	}
	return nil, nil, nil
}

// mutations are the calls that change the machine.
func (d *docker) mutations() []string {
	var out []string
	for _, c := range d.calls {
		switch {
		case strings.HasPrefix(c, "rm "), strings.HasPrefix(c, "rmi "),
			strings.HasPrefix(c, "volume rm "), strings.HasPrefix(c, "network rm "),
			strings.HasPrefix(c, "buildx prune"):
			out = append(out, c)
		}
	}
	return out
}

// config builds a run whose compose search is exhaustive and cheap: a single mount,
// an empty directory, so nothing on disk claims anything.
func config(t *testing.T, d *docker) (Config, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	empty := t.TempDir()
	mountinfo := filepath.Join(dir, "mountinfo")
	require.NoError(t, os.WriteFile(mountinfo,
		[]byte("27 1 259:2 / "+empty+" rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw\n"), 0o644))

	var stdout, stderr bytes.Buffer
	return Config{
		Options:     plan.Options{Age: 30 * 24 * time.Hour, BuildCacheAge: 7 * 24 * time.Hour},
		IndexPath:   filepath.Join(dir, "projects.json"),
		MountInfo:   mountinfo,
		DockerRoot:  "/var/lib/docker",
		Runner:      d,
		Stdout:      &stdout,
		Stderr:      &stderr,
		Stdin:       strings.NewReader(""),
		Interactive: false,
		Now:         now,
	}, &stdout, &stderr
}

// The whole promise of --dry-run is that it changes nothing, so the assertion
// is on the commands issued, not on the words printed.
func TestDryRunIssuesNoMutatingCommand(t *testing.T) {
	d := newDocker()
	c, stdout, _ := config(t, d)
	c.DryRun = true

	code := Do(context.Background(), c)

	assert.Equal(t, ExitOK, code)
	assert.Empty(t, d.mutations())
	assert.Contains(t, stdout.String(), "DRY RUN")
	assert.Contains(t, stdout.String(), "web-old")
	assert.Contains(t, stdout.String(), "Dry run: nothing was removed.")
}

func TestApplyRunsExactlyThePlannedCommands(t *testing.T) {
	d := newDocker()
	c, stdout, _ := config(t, d)
	c.Yes = true

	code := Do(context.Background(), c)

	assert.Equal(t, ExitOK, code)
	assert.Equal(t, []string{"rm c1", "rmi myapp:v1"}, d.mutations())
	assert.Contains(t, stdout.String(), "ok       docker rm c1")
	assert.Contains(t, stdout.String(), "AFTER")
}

// The newest image of a repository is never removed, whatever else happens.
func TestTheNewestImageSurvivesAnApply(t *testing.T) {
	d := newDocker()
	c, _, _ := config(t, d)
	c.Yes = true

	Do(context.Background(), c)

	assert.NotContains(t, d.mutations(), "rmi myapp:v2")
}

func TestDeclinedPromptRemovesNothing(t *testing.T) {
	d := newDocker()
	c, stdout, _ := config(t, d)
	c.Interactive = true
	c.Stdin = strings.NewReader("n\n")

	code := Do(context.Background(), c)

	assert.Equal(t, ExitOK, code)
	assert.Empty(t, d.mutations())
	assert.Contains(t, stdout.String(), "Aborted")
}

func TestAcceptedPromptApplies(t *testing.T) {
	for _, answer := range []string{"y\n", "YES\n"} {
		d := newDocker()
		c, _, _ := config(t, d)
		c.Interactive = true
		c.Stdin = strings.NewReader(answer)

		assert.Equal(t, ExitOK, Do(context.Background(), c), answer)
		assert.Equal(t, []string{"rm c1", "rmi myapp:v1"}, d.mutations(), answer)
	}
}

// Nothing can answer a prompt with no terminal, and a silent yes is the answer
// this tool must never assume.
func TestNoTerminalAndNoYesRefusesToRun(t *testing.T) {
	d := newDocker()
	c, _, stderr := config(t, d)

	code := Do(context.Background(), c)

	assert.Equal(t, ExitUsage, code)
	assert.Empty(t, d.mutations())
	assert.Contains(t, stderr.String(), "--yes")
}

// A partial read makes a confident, wrong plan, so a broken read stops the run
// before anything is removed.
func TestUnreadableDockerStopsBeforeAnyRemoval(t *testing.T) {
	for _, call := range []string{"version --format json", "ps -aq --no-trunc"} {
		d := newDocker()
		d.fail[call] = "Cannot connect to the Docker daemon at unix:///var/run/docker.sock"
		c, _, stderr := config(t, d)
		c.Yes = true

		code := Do(context.Background(), c)

		assert.Equal(t, ExitEnvironment, code, call)
		assert.Empty(t, d.mutations(), call)
		assert.Contains(t, stderr.String(), "docker-cleaner:", call)
	}
}

// A container that raced back to running still holds its image. Removing that
// image anyway is exactly the deletion this tool exists to avoid.
func TestAContainerThatWouldNotGoKeepsWhatItHolds(t *testing.T) {
	d := newDocker()
	d.fail["rm c1"] = "Error response from daemon: cannot remove a running container"
	c, stdout, _ := config(t, d)
	c.Yes = true

	code := Do(context.Background(), c)

	assert.Equal(t, ExitApplyFailed, code)
	assert.Equal(t, []string{"rm c1"}, d.mutations())
	assert.Contains(t, stdout.String(), "skipped  myapp:v1: web-old was not removed")
}

func TestAFailedRemovalIsReportedAndTheRunContinues(t *testing.T) {
	d := newDocker()
	d.fail["rmi myapp:v1"] = "Error response from daemon: conflict: unable to delete"
	c, stdout, _ := config(t, d)
	c.Yes = true

	code := Do(context.Background(), c)

	assert.Equal(t, ExitApplyFailed, code)
	assert.Contains(t, stdout.String(), "FAILED   docker rmi myapp:v1")
	assert.Contains(t, stdout.String(), "conflict: unable to delete")
}

func TestJSONDryRunIsParseableAndChangesNothing(t *testing.T) {
	d := newDocker()
	c, stdout, _ := config(t, d)
	c.DryRun, c.JSON = true, true

	code := Do(context.Background(), c)

	require.Equal(t, ExitOK, code)
	assert.Empty(t, d.mutations())

	var doc report.Doc
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &doc))
	assert.True(t, doc.DryRun)
	assert.True(t, doc.ComposeComplete)
	assert.Equal(t, [][]string{{"rm", "c1"}}, doc.Containers[0].Commands)
	assert.Empty(t, doc.Operations, "a dry run executes nothing, so it has no history")
}

// With --json the apply log goes to stderr, or stdout stops being parseable.
func TestJSONApplyRecordsWhatRanAndKeepsStdoutClean(t *testing.T) {
	d := newDocker()
	c, stdout, stderr := config(t, d)
	c.JSON, c.Yes = true, true

	code := Do(context.Background(), c)

	require.Equal(t, ExitOK, code)
	var doc report.Doc
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &doc))
	assert.Equal(t, []string{"rm", "c1"}, doc.Operations[0].Argv)
	assert.True(t, doc.Operations[0].OK)
	assert.Equal(t, ExitOK, doc.ExitCode)
	assert.Contains(t, stderr.String(), "APPLY")
}

// Without a mount table the search cannot be exhaustive, and saying otherwise
// would let "no compose file found" mean "that project was deleted".
func TestAMissingMountTableMakesTheSearchIncomplete(t *testing.T) {
	d := newDocker()
	c, stdout, _ := config(t, d)
	c.MountInfo = filepath.Join(t.TempDir(), "absent")
	c.DryRun = true

	code := Do(context.Background(), c)

	assert.Equal(t, ExitOK, code)
	assert.Contains(t, stdout.String(), "SEARCH INCOMPLETE")
	assert.Contains(t, stdout.String(), "COULD NOT SEARCH: cannot read")
}

// A machine with nothing to clean says so and asks nothing.
func TestAQuietRunNeedsNoConfirmation(t *testing.T) {
	d := newDocker()
	d.reads["ps -aq --no-trunc"] = ""
	d.reads["image ls --no-trunc --format json"] = "{\"ID\":\"sha256:i2\"}\n"
	d.reads["image inspect sha256:i2"] = inspectDoc(imageDoc("sha256:i2", "myapp:v2", ago(10*24*time.Hour), 1000))
	c, stdout, _ := config(t, d)

	code := Do(context.Background(), c)

	assert.Equal(t, ExitOK, code)
	assert.Empty(t, d.mutations())
	assert.Contains(t, stdout.String(), "Nothing to remove.")
}

// The index is what keeps the next run to stat calls: a live container names
// its compose files, and the tool writes that down for after the down.
func TestTheRunPersistsWhatItLearned(t *testing.T) {
	file := filepath.Join(t.TempDir(), "compose.yaml")
	require.NoError(t, os.WriteFile(file, []byte("services: {}\n"), 0o644))

	d := newDocker()
	d.reads["container inspect c1"] = inspectDoc(map[string]any{
		"Id": "c1", "Name": "/webapp-db-1", "Image": "sha256:i1",
		"Created": ago(2 * 24 * time.Hour),
		"State":   map[string]any{"Status": "running", "FinishedAt": "0001-01-01T00:00:00Z"},
		"Config": map[string]any{"Image": "myapp:v1", "Labels": map[string]string{
			"com.docker.compose.project":              "webapp",
			"com.docker.compose.project.config_files": file,
		}},
		"Mounts":          []any{map[string]any{"Type": "volume", "Name": "webapp_pgdata"}},
		"NetworkSettings": map[string]any{"Networks": map[string]any{}},
	})
	d.reads["compose -f "+file+" config --format json"] =
		`{"name":"webapp","services":{"db":{"image":"myapp:v1"}},"volumes":{"pgdata":{}}}`
	c, _, _ := config(t, d)
	c.DryRun = true

	require.Equal(t, ExitOK, Do(context.Background(), c))

	body, err := os.ReadFile(c.IndexPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), file)
}

// A project the index cannot explain is worth a walk; a project it can is not. This
// is the whole speed claim, and it is asserted on the directory count.
func TestAKnownProjectCostsNoWalk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.WriteFile(file, []byte("services: {}\n"), 0o644))

	d := newDocker()
	d.reads["volume ls --format json"] = `{"Name":"webapp_pgdata"}` + "\n"
	d.reads["volume inspect webapp_pgdata"] = `[{"Name":"webapp_pgdata","Driver":"local","Scope":"local",` +
		`"Labels":{"com.docker.compose.project":"webapp"}}]`
	d.reads["compose -f "+file+" config --format json"] =
		`{"name":"webapp","volumes":{"pgdata":{}}}`
	c, stdout, _ := config(t, d)
	c.DryRun = true
	c.MountInfo = filepath.Join(t.TempDir(), "mountinfo")
	require.NoError(t, os.WriteFile(c.MountInfo,
		[]byte("27 1 259:2 / "+dir+" rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw\n"), 0o644))

	// The earlier run learns the project from the walk, the later from the index.
	require.Equal(t, ExitOK, Do(context.Background(), c))
	stdout.Reset()
	require.Equal(t, ExitOK, Do(context.Background(), c))

	assert.Contains(t, stdout.String(), "0 directories walked")
	assert.NotContains(t, stdout.String(), "webapp_pgdata")
}
