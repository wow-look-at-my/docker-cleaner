package plan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

func TestStaleContainerSelectedByFinishedAt(t *testing.T) {
	snap := dockercli.Snapshot{Containers: []dockercli.Container{
		container("c1", "web-old", "sha256:img1", 47*24*time.Hour),
		container("c2", "web-new", "sha256:img1", 5*24*time.Hour),
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Containers, 1)
	assert.Equal(t, "web-old", p.Containers[0].Name)
	assert.Equal(t, [][]string{{"rm", "c1"}}, p.Containers[0].Commands)

	reason, ok := keptReason(p, "web-new")
	require.True(t, ok)
	assert.Equal(t, ReasonTooRecent, reason)
}

// A container that never ran carries FinishedAt at the zero time, which parses
// to year one and therefore beats every cutoff. Treating that as an age would
// delete containers created minutes ago.
func TestZeroFinishedAtIsNeverStale(t *testing.T) {
	for _, finished := range []string{"0001-01-01T00:00:00Z", ""} {
		c := dockercli.Container{ID: "c1", Name: "/fresh", Created: ago(time.Minute)}
		c.State.Status = "exited"
		c.State.FinishedAt = finished

		p := Compute(dockercli.Snapshot{Containers: []dockercli.Container{c}}, noComposeFiles(), defaults(), now)

		assert.Empty(t, p.Containers, finished)
		reason, ok := keptReason(p, "fresh")
		require.True(t, ok, finished)
		assert.Equal(t, ReasonNeverRan, reason, finished)
	}
}

// A created container never ran, so it ages by creation instead.
func TestCreatedContainerAgesByCreation(t *testing.T) {
	old := dockercli.Container{ID: "c1", Name: "/old-probe", Created: ago(88 * 24 * time.Hour)}
	old.State.Status = "created"
	recent := dockercli.Container{ID: "c2", Name: "/new-probe", Created: ago(time.Hour)}
	recent.State.Status = "created"

	p := Compute(dockercli.Snapshot{Containers: []dockercli.Container{old, recent}}, noComposeFiles(), defaults(), now)

	require.Len(t, p.Containers, 1)
	assert.Equal(t, "old-probe", p.Containers[0].Name)
	assert.Contains(t, p.Containers[0].Detail, "never started")
}

func TestLiveStatesKeptWithTheirOwnReason(t *testing.T) {
	for status, want := range map[string]Reason{
		"running":    ReasonRunning,
		"paused":     ReasonPaused,
		"restarting": ReasonRestarting,
		"removing":   ReasonRemoving,
		"wedged":     ReasonUnknownState,
	} {
		c := dockercli.Container{ID: "c1", Name: "/svc", Created: ago(90 * 24 * time.Hour)}
		c.State.Status = status
		c.State.FinishedAt = ago(90 * 24 * time.Hour)

		p := Compute(dockercli.Snapshot{Containers: []dockercli.Container{c}}, noComposeFiles(), defaults(), now)

		assert.Empty(t, p.Containers, status)
		reason, ok := keptReason(p, "svc")
		require.True(t, ok, status)
		assert.Equal(t, want, reason, status)
	}
}

// A dead container with a real FinishedAt is still just an old container.
func TestDeadContainerWithRealTimestampIsSelected(t *testing.T) {
	c := container("c1", "dead-one", "sha256:img1", 60*24*time.Hour)
	c.State.Status = "dead"

	p := Compute(dockercli.Snapshot{Containers: []dockercli.Container{c}}, noComposeFiles(), defaults(), now)

	require.Len(t, p.Containers, 1)
	assert.Equal(t, "dead-one", p.Containers[0].Name)
}

// A restart policy does not exempt a container, but it is worth seeing before
// confirming, so it rides along as a note.
func TestRestartPolicyAnnotatedNotExempted(t *testing.T) {
	c := container("c1", "svc", "sha256:img1", 40*24*time.Hour)
	c.HostConfig.RestartPolicy.Name = "unless-stopped"

	p := Compute(dockercli.Snapshot{Containers: []dockercli.Container{c}}, noComposeFiles(), defaults(), now)

	require.Len(t, p.Containers, 1)
	assert.Contains(t, p.Containers[0].Note, "unless-stopped")
}

func TestNoContainersFlagEmptiesTheStep(t *testing.T) {
	snap := dockercli.Snapshot{Containers: []dockercli.Container{
		container("c1", "web-old", "sha256:img1", 47*24*time.Hour),
	}}
	o := defaults()
	o.SkipContainers = true

	p := Compute(snap, noComposeFiles(), o, now)

	assert.Empty(t, p.Containers)
	reason, ok := keptReason(p, "web-old")
	require.True(t, ok)
	assert.Equal(t, ReasonStepDisabled, reason)
}
