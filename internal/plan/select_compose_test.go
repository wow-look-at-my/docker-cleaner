package plan

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// composeRunner answers `docker compose config` from a canned project.
type composeRunner struct{ json string }

func (r composeRunner) Run(context.Context, ...string) ([]byte, []byte, error) {
	return []byte(r.json), nil, nil
}

const webappProject = `{
  "name": "webapp",
  "services": {"db": {"image": "postgres:16"}},
  "volumes": {"pgdata": {"name": "webapp_pgdata"}},
  "networks": {"default": {"name": "webapp_default"}}
}`

// liveProject is discovery on a machine whose webapp compose file is present.
func liveProject(t *testing.T) *compose.Discovery {
	t.Helper()
	claims := compose.Resolve(context.Background(), composeRunner{webappProject},
		[]string{"/srv/webapp/compose.yaml"}, nil)
	return &compose.Discovery{Claims: claims, Complete: true}
}

func composeVolume(name, project string) dockercli.Volume {
	return dockercli.Volume{
		Name:   name,
		Driver: "local",
		Scope:  "local",
		Labels: map[string]string{compose.LabelProject: project},
	}
}

// The headline case. A stack that is `down` has no containers, exactly like a
// stack that was deleted. Its compose file is the only thing that tells them
// apart, so with the file present the data stays.
func TestVolumeOfDownStackIsKeptWhileItsComposeFileExists(t *testing.T) {
	snap := dockercli.Snapshot{Volumes: []dockercli.Volume{composeVolume("webapp_pgdata", "webapp")}}

	p := Compute(snap, liveProject(t), defaults(), now)

	assert.Empty(t, p.Volumes, "a down stack keeps its database")
	reason, ok := keptReason(p, "webapp_pgdata")
	require.True(t, ok)
	assert.Equal(t, ReasonClaimedByCompos, reason)
}

// The same volume, after the compose file is gone and the search was
// exhaustive. Deleting the file is what retires a project.
func TestVolumeIsCollectedOnceItsComposeFileIsDeleted(t *testing.T) {
	snap := dockercli.Snapshot{Volumes: []dockercli.Volume{composeVolume("webapp_pgdata", "webapp")}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Volumes, 1)
	assert.Equal(t, "webapp_pgdata", p.Volumes[0].Name)
	assert.Equal(t, [][]string{{"volume", "rm", "webapp_pgdata"}}, p.Volumes[0].Commands)
}

// The safety interlock: the same absence proves nothing when the search could
// not run everywhere. Not looking is never evidence of deletion.
func TestVolumeIsKeptWhenTheSearchWasIncomplete(t *testing.T) {
	snap := dockercli.Snapshot{Volumes: []dockercli.Volume{composeVolume("webapp_pgdata", "webapp")}}

	p := Compute(snap, incompleteSearch(), defaults(), now)

	assert.Empty(t, p.Volumes)
	reason, ok := keptReason(p, "webapp_pgdata")
	require.True(t, ok)
	assert.Equal(t, ReasonScanIncomplete, reason)
	assert.False(t, p.ComposeComplete)
}

// A live compose file keeps its image even though no container references it.
func TestImageNamedOnlyByALiveComposeFileIsKept(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:pg16", ago(300*24*time.Hour), "postgres:16"),
		image("sha256:pg17", ago(1*24*time.Hour), "postgres:17"),
	}}

	p := Compute(snap, liveProject(t), defaults(), now)

	assert.Empty(t, p.Images)
	reason, ok := keptReason(p, "postgres:16")
	require.True(t, ok)
	assert.Equal(t, ReasonClaimedByCompos, reason)
}

// Removing a stale compose container does not hand its volume over: the
// compose claim outlives the container.
func TestStaleComposeContainerGoesButItsVolumeSurvives(t *testing.T) {
	c := container("c1", "webapp-db-1", "sha256:pg16", 40*24*time.Hour)
	c.Config.Labels = map[string]string{compose.LabelProject: "webapp"}
	c.Mounts = []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	}{{Type: "volume", Name: "webapp_pgdata"}}

	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{c},
		Volumes:    []dockercli.Volume{composeVolume("webapp_pgdata", "webapp")},
	}

	p := Compute(snap, liveProject(t), defaults(), now)

	require.Len(t, p.Containers, 1)
	assert.Equal(t, "webapp-db-1", p.Containers[0].Name)
	assert.Empty(t, p.Volumes, "the compose file still claims this volume")
}

// A network a live project declares is kept even with nothing attached,
// because the next up would attach it again.
func TestNetworkClaimedByComposeIsKept(t *testing.T) {
	snap := dockercli.Snapshot{Networks: []dockercli.Network{{
		ID: "n1", Name: "webapp_default", Driver: "bridge", Scope: "local",
		Labels: map[string]string{compose.LabelProject: "webapp"},
	}}}

	p := Compute(snap, liveProject(t), defaults(), now)

	assert.Empty(t, p.Networks)
	reason, ok := keptReason(p, "webapp_default")
	require.True(t, ok)
	assert.Equal(t, ReasonClaimedByCompos, reason)
}

func TestComposeProjectsListsEveryLabelledResource(t *testing.T) {
	c := container("c1", "svc", "sha256:img", time.Hour)
	c.Config.Labels = map[string]string{compose.LabelProject: "alpha"}
	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{c},
		Volumes:    []dockercli.Volume{composeVolume("beta_data", "beta")},
		Networks: []dockercli.Network{{
			ID: "n1", Name: "gamma_default",
			Labels: map[string]string{compose.LabelProject: "gamma"},
		}},
	}

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, ComposeProjects(snap))
}
