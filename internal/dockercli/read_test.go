package dockercli

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadParsesEveryDocument(t *testing.T) {
	f := newFake(t)

	s, err := Read(context.Background(), f)
	require.NoError(t, err)

	require.NotNil(t, s.Version.Server)
	assert.Equal(t, "29.3.1", s.Version.Server.Version)
	assert.Contains(t, s.BeforeText, "RECLAIMABLE")
	assert.Equal(t, int64(1073741824), s.DiskUsage.LayersSize)

	require.Len(t, s.Containers, 2)
	assert.Equal(t, "/webapp-db-1", s.Containers[0].Name)
	assert.Equal(t, "sha256:aaa1", s.Containers[0].Image)
	assert.Equal(t, "postgres:16", s.Containers[0].Config.Image)
	assert.Equal(t, "unless-stopped", s.Containers[0].HostConfig.RestartPolicy.Name)
	assert.Equal(t, "n0000001", s.Containers[0].NetworkSettings.Networks["webapp_default"].NetworkID)

	require.Len(t, s.Images, 2)
	assert.Equal(t, []string{"postgres:16", "postgres:latest"}, s.Images[0].RepoTags)
	assert.Equal(t, "sha256:aaa1", s.Images[1].Parent)

	require.Len(t, s.Volumes, 2)
	assert.Equal(t, "webapp", s.Volumes[0].Labels["com.docker.compose.project"])
	require.Len(t, s.Networks, 2)
	assert.Equal(t, "webapp_default", s.Networks[0].Name)
}

// A volume mounted from the host is a bind, and no docker command deletes it.
// It must survive the read as a bind so nothing downstream can select it.
func TestBindMountIsReadAsABind(t *testing.T) {
	f := newFake(t)

	s, err := Read(context.Background(), f)
	require.NoError(t, err)

	require.Len(t, s.Containers[0].Mounts, 2)
	assert.Equal(t, "volume", s.Containers[0].Mounts[0].Type)
	assert.Equal(t, "bind", s.Containers[0].Mounts[1].Type)
	assert.Empty(t, s.Containers[0].Mounts[1].Name)
}

// image ls repeats an id per tag. Inspecting a duplicate would count the same
// image again.
func TestDuplicateImageRowsAreInspectedOnce(t *testing.T) {
	f := newFake(t)

	_, err := Read(context.Background(), f)
	require.NoError(t, err)

	calls := f.callsWithPrefix("image inspect")
	require.Len(t, calls, 1)
	assert.Equal(t, []string{"image", "inspect", "sha256:aaa1", "sha256:bbb2"}, calls[0])
}

// buildx du and buildx prune both act on a builder, so a read that asks about
// the configured builder reports only that builder's cache.
func TestEveryBuilderIsRead(t *testing.T) {
	f := newFake(t)

	s, err := Read(context.Background(), f)
	require.NoError(t, err)

	require.Len(t, s.Caches, 2)
	assert.Equal(t, "default", s.Caches[0].Builder)
	assert.Equal(t, "ci-builder", s.Caches[1].Builder)
	assert.Equal(t, "2026-08-20T09:00:00Z", *s.Caches[0].Records[0].LastUsedAt)
	assert.True(t, s.Caches[0].Records[1].InUse)
	assert.Nil(t, s.Caches[0].Records[1].LastUsedAt)
	assert.Empty(t, s.CacheUnavailable)
}

// Without buildx the daemon still reports the default builder's cache through
// system df, which is worth pruning even though the builder list is empty.
func TestBuildxMissingFallsBackToSystemDF(t *testing.T) {
	f := newFake(t)
	f.override["buildx ls"] = func() ([]byte, []byte, error) {
		return nil, []byte("docker: 'buildx' is not a docker command"), errors.New("exit status 125")
	}

	s, err := Read(context.Background(), f)
	require.NoError(t, err)

	require.Len(t, s.Caches, 1)
	assert.Empty(t, s.Caches[0].Builder)
	assert.Equal(t, "df1", s.Caches[0].Records[0].ID)
}

// Build cache is a step of many. Losing it is reported, never fatal.
func TestUnreadableCacheIsReportedNotFatal(t *testing.T) {
	f := newFake(t)
	f.override["buildx du"] = func() ([]byte, []byte, error) {
		return nil, []byte("failed to connect to builder"), errors.New("exit status 1")
	}

	s, err := Read(context.Background(), f)
	require.NoError(t, err)

	assert.Empty(t, s.Caches)
	assert.Contains(t, s.CacheUnavailable, "cannot read build cache")
}

func TestUnreachableDaemonIsFatal(t *testing.T) {
	f := newFake(t)
	f.override["version"] = func() ([]byte, []byte, error) {
		return fixture(t, "version_no_daemon.json"), nil, nil
	}

	_, err := Read(context.Background(), f)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon is not reachable")
}

// A read that half-worked produces a confident, wrong plan, so it stops here.
func TestFailedReadIsFatal(t *testing.T) {
	for _, prefix := range []string{"ps -aq", "image ls", "volume ls", "network ls", "system df -v"} {
		f := newFake(t)
		f.override[prefix] = func() ([]byte, []byte, error) {
			return nil, []byte("Cannot connect to the Docker daemon"), errors.New("exit status 1")
		}

		_, err := Read(context.Background(), f)

		require.Error(t, err, prefix)
	}
}

func TestUnparseableOutputIsFatal(t *testing.T) {
	f := newFake(t)
	f.override["image ls"] = func() ([]byte, []byte, error) {
		return []byte("{not json"), nil, nil
	}

	_, err := Read(context.Background(), f)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing output")
}

// Objects vanish between listing and inspecting; docker still prints the ones
// it found, so the run continues with those.
func TestMissingObjectIsNotFatal(t *testing.T) {
	f := newFake(t)
	f.override["container inspect"] = func() ([]byte, []byte, error) {
		return fixture(t, "container_inspect.json"),
			[]byte("Error: No such container: c0ffee9"), errors.New("exit status 1")
	}

	s, err := Read(context.Background(), f)

	require.NoError(t, err)
	assert.Len(t, s.Containers, 2)
}

func TestOtherInspectErrorsAreFatal(t *testing.T) {
	f := newFake(t)
	f.override["container inspect"] = func() ([]byte, []byte, error) {
		return nil, []byte("permission denied while trying to connect"), errors.New("exit status 1")
	}

	_, err := Read(context.Background(), f)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

type recorder struct{ calls [][]string }

func (r *recorder) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return nil, nil, nil
}

// A host with thousands of objects would blow past ARG_MAX in a call.
func TestInspectBatchesWithoutLosingIDs(t *testing.T) {
	var ids []string
	for i := range 250 {
		ids = append(ids, "id"+strconv.Itoa(i))
	}
	r := &recorder{}

	_, err := inspect[Container](context.Background(), r, "container", ids)
	require.NoError(t, err)

	require.Len(t, r.calls, 3)
	var got []string
	for _, c := range r.calls {
		assert.Equal(t, []string{"container", "inspect"}, c[:2])
		got = append(got, c[2:]...)
	}
	assert.Equal(t, ids, got)
}

// docker treats a bare inspect as a usage error, so an empty list must not
// reach it at all.
func TestEmptyListIssuesNoInspect(t *testing.T) {
	r := &recorder{}

	out, err := inspect[Container](context.Background(), r, "container", nil)

	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Empty(t, r.calls)
}

// Some docker versions wrap a listing in an array instead of a line per row.
func TestBothListingShapesDecode(t *testing.T) {
	type row struct {
		ID string `json:"ID"`
	}
	for _, out := range []string{
		"{\"ID\":\"a\"}\n{\"ID\":\"b\"}\n",
		"[{\"ID\":\"a\"},{\"ID\":\"b\"}]",
	} {
		got, err := decodeNDJSON[row]([]byte(out), "test")
		require.NoError(t, err)
		assert.Equal(t, []row{{ID: "a"}, {ID: "b"}}, got)
	}

	empty, err := decodeNDJSON[row]([]byte("  \n"), "test")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestParseTimeRejectsTheZeroValue(t *testing.T) {
	for _, s := range []string{"", "0001-01-01T00:00:00Z", "not a time"} {
		_, ok := ParseTime(s)
		assert.False(t, ok, s)
	}

	got, ok := ParseTime("2026-05-01T08:00:00.123456789Z")
	require.True(t, ok)
	assert.Equal(t, 2026, got.Year())
}

// Every fixture must be named by some test. A fixture nothing reads looks like
// coverage and proves nothing.
func TestEveryFixtureIsUsed(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	require.NoError(t, err)

	for _, e := range entries {
		assert.True(t, usedFixtures.Has(e.Name()), "no test reads testdata/%s", e.Name())
	}
}

func TestArgvRendersTheWholeInvocation(t *testing.T) {
	assert.Equal(t, "docker rm c1", Argv("docker", RemoveContainer("c1")))
	assert.True(t, strings.HasPrefix(Argv("/usr/bin/docker", nil), "/usr/bin/docker"))
}
