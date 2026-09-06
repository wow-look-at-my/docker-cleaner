package dockercli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgvPerTargetKind(t *testing.T) {
	assert.Equal(t, []string{"rm", "c1"}, RemoveContainer("c1"))
	assert.Equal(t, []string{"rmi", "myapp:v1"}, RemoveImageRef("myapp:v1"))
	assert.Equal(t, []string{"volume", "rm", "pgdata"}, RemoveVolume("pgdata"))
	assert.Equal(t, []string{"network", "rm", "n1"}, RemoveNetwork("n1"))
}

// until= takes a Go duration here, so a 7d threshold has to reach docker as
// 168h. Passing 7d makes buildx reject the whole prune.
func TestPruneBuildCacheArgv(t *testing.T) {
	assert.Equal(t,
		[]string{"buildx", "prune", "--builder", "ci", "--force", "--filter", "until=168h"},
		PruneBuildCache("ci", "168h"))

	assert.Equal(t,
		[]string{"buildx", "prune", "--force", "--filter", "until=24h"},
		PruneBuildCache("", "24h"),
		"an unnamed builder means the configured one, not an empty --builder")
}

// -f on rmi deletes an image a container still holds; -f on rm kills a running
// container; -v on rm deletes the volume the plan promised to keep. The prune
// is the place --force is right, and it means "do not ask".
func TestNoForcingFlagsOutsideThePrune(t *testing.T) {
	commands := [][]string{
		RemoveContainer("c1"),
		RemoveImageRef("myapp:v1"),
		RemoveVolume("pgdata"),
		RemoveNetwork("n1"),
	}

	for _, args := range commands {
		for _, a := range args {
			assert.NotEqual(t, "-f", a, args)
			assert.NotEqual(t, "--force", a, args)
			assert.NotEqual(t, "-v", a, args)
			assert.NotEqual(t, "--volumes", a, args)
		}
	}
}

func TestDoReportsDockersFirstLine(t *testing.T) {
	r := &fixedRunner{
		stderr: "Error response from daemon: conflict: unable to delete\nsecond line",
		err:    errors.New("exit status 1"),
	}

	err := Do(context.Background(), r, RemoveImageRef("myapp:v1"))

	var f *Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, "Error response from daemon: conflict: unable to delete", f.Msg)
	assert.Equal(t, []string{"rmi", "myapp:v1"}, f.Args)
}

// A command that fails with nothing on stderr still has to fail loudly.
func TestDoKeepsTheBareErrorWhenDockerIsSilent(t *testing.T) {
	r := &fixedRunner{err: errors.New("signal: killed")}

	err := Do(context.Background(), r, RemoveVolume("v1"))

	require.Error(t, err)
	assert.Equal(t, "signal: killed", err.Error())
}

func TestDoSucceedsQuietly(t *testing.T) {
	r := &fixedRunner{}

	require.NoError(t, Do(context.Background(), r, RemoveNetwork("n1")))
	assert.Equal(t, [][]string{{"network", "rm", "n1"}}, r.calls)
}

type fixedRunner struct {
	stderr string
	err    error
	calls  [][]string
}

func (r *fixedRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return nil, []byte(r.stderr), r.err
}
