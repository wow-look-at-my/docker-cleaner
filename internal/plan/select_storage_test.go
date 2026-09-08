package plan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

func localVolume(name string) dockercli.Volume {
	return dockercli.Volume{Name: name, Driver: "local", Scope: "local"}
}

func TestVolumeAttachedToASurvivingContainerIsKept(t *testing.T) {
	c := container("c1", "db", "sha256:img", 2*24*time.Hour)
	c.Mounts = []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	}{{Type: "volume", Name: "pgdata-prod"}}

	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{c},
		Volumes:    []dockercli.Volume{localVolume("pgdata-prod")},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Volumes)
	reason, ok := keptReason(p, "pgdata-prod")
	require.True(t, ok)
	assert.Equal(t, ReasonInUse, reason)
}

func TestVolumeFreedByCascadeIsAttributed(t *testing.T) {
	c := container("c1", "web-old", "sha256:img", 47*24*time.Hour)
	c.Mounts = []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	}{{Type: "volume", Name: "scratch"}}

	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{c},
		Volumes:    []dockercli.Volume{localVolume("scratch")},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Volumes, 1)
	assert.Equal(t, []string{"web-old"}, p.Volumes[0].FreedBy)
}

func TestNonLocalVolumesAreLeftAlone(t *testing.T) {
	snap := dockercli.Snapshot{Volumes: []dockercli.Volume{
		{Name: "nfs-share", Driver: "nfs", Scope: "local"},
		{Name: "cluster-vol", Driver: "local", Scope: "swarm"},
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Volumes)
}

// A buildx state volume still goes, but it is labelled: it holds that
// builder's whole cache rather than an ageing slice of it.
func TestBuildxStateVolumeIsSelectedWithANote(t *testing.T) {
	snap := dockercli.Snapshot{Volumes: []dockercli.Volume{localVolume("buildx_buildkit_default0_state")}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Volumes, 1)
	assert.Contains(t, p.Volumes[0].Note, "entire cache")
}

func TestAnonymousVolumeIsLabelledAsSuch(t *testing.T) {
	hex64 := "0f4c1e9b2a3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"

	p := Compute(dockercli.Snapshot{Volumes: []dockercli.Volume{localVolume(hex64)}}, noComposeFiles(), defaults(), now)

	require.Len(t, p.Volumes, 1)
	assert.Contains(t, p.Volumes[0].Name, "<anonymous>")
	assert.Equal(t, hex64, p.Volumes[0].ID)
}

// A volume the walk could not read must not read as empty. Both print 0B, and
// only the empty volume was measured.
func TestAVolumeNothingMeasuredIsMarkedRatherThanShownEmpty(t *testing.T) {
	snap := dockercli.Snapshot{
		Volumes: []dockercli.Volume{localVolume("measured"), localVolume("unreadable")},
		DiskUsage: dockercli.DiskUsage{Volumes: []dockercli.VolumeUsage{
			{Name: "measured", Size: 4096, Measured: true},
			{Name: "unreadable"},
		}},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Volumes, 2)
	byName := map[string]Target{p.Volumes[0].ID: p.Volumes[0], p.Volumes[1].ID: p.Volumes[1]}
	assert.False(t, byName["measured"].Unmeasured)
	assert.Equal(t, int64(4096), byName["measured"].Size)
	assert.True(t, byName["unreadable"].Unmeasured)
}

// A volume that really is empty stays a measurement, so it reports its size.
func TestAnEmptyVolumeIsStillAMeasurement(t *testing.T) {
	snap := dockercli.Snapshot{
		Volumes: []dockercli.Volume{localVolume("empty")},
		DiskUsage: dockercli.DiskUsage{
			Volumes: []dockercli.VolumeUsage{{Name: "empty", Measured: true}},
		},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Volumes, 1)
	assert.False(t, p.Volumes[0].Unmeasured)
	assert.Zero(t, p.Volumes[0].Size)
}

func TestPredefinedNetworksNeverAppearInAPlan(t *testing.T) {
	snap := dockercli.Snapshot{Networks: []dockercli.Network{
		{ID: "n1", Name: "bridge", Driver: "bridge", Scope: "local"},
		{ID: "n2", Name: "host", Driver: "host", Scope: "local"},
		{ID: "n3", Name: "none", Driver: "null", Scope: "local"},
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Networks)
}

// network inspect lists live endpoints only, so a stopped container's
// membership shows up nowhere else. Missing it breaks that stack's next start.
func TestNetworkHeldByAStoppedSurvivorIsKept(t *testing.T) {
	c := container("c1", "svc", "sha256:img", 2*24*time.Hour)
	c.NetworkSettings.Networks = map[string]struct {
		NetworkID string `json:"NetworkID"`
	}{"proj_default": {NetworkID: "n1"}}

	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{c},
		Networks:   []dockercli.Network{{ID: "n1", Name: "proj_default", Driver: "bridge", Scope: "local"}},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Networks)
	reason, ok := keptReason(p, "proj_default")
	require.True(t, ok)
	assert.Equal(t, ReasonInUse, reason)
}

func TestSwarmAndConfigOnlyNetworksAreLeftAlone(t *testing.T) {
	snap := dockercli.Snapshot{Networks: []dockercli.Network{
		{ID: "n1", Name: "ingress", Driver: "overlay", Scope: "swarm", Ingress: true},
		{ID: "n2", Name: "tmpl", Driver: "bridge", Scope: "local", ConfigOnly: true},
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Networks)
}

func TestUnusedNetworkIsRemoved(t *testing.T) {
	snap := dockercli.Snapshot{Networks: []dockercli.Network{
		{ID: "n1", Name: "orphan_default", Driver: "bridge", Scope: "local"},
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Networks, 1)
	assert.Equal(t, [][]string{{"network", "rm", "n1"}}, p.Networks[0].Commands)
}
