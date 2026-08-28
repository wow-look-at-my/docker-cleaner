package plan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

func TestNewestPerRepositoryIsKept(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:v1", ago(200*24*time.Hour), "myapp:v1"),
		image("sha256:v2", ago(100*24*time.Hour), "myapp:v2"),
		image("sha256:v3", ago(10*24*time.Hour), "myapp:v3"),
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Len(t, p.Images, 2)
	_, kept := target(p.Images, "myapp:v3")
	assert.False(t, kept, "the newest image of a repository is never removed")
	reason, ok := keptReason(p, "myapp:v3")
	require.True(t, ok)
	assert.Equal(t, ReasonNewestInRepo, reason)
}

// A registry port is not a tag separator, so these are one repository.
func TestNewestPerRepositoryHandlesRegistryPorts(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:a", ago(200*24*time.Hour), "localhost:5000/tools:build-1"),
		image("sha256:b", ago(10*24*time.Hour), "localhost:5000/tools:build-2"),
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Images, 1)
	assert.Equal(t, "localhost:5000/tools:build-1", p.Images[0].Name)
}

func TestLatestTagIsKeptEvenWhenOlder(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:old", ago(300*24*time.Hour), "nginx:latest"),
		image("sha256:new", ago(1*24*time.Hour), "nginx:1.27"),
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Images)
	reason, ok := keptReason(p, "nginx:latest")
	require.True(t, ok)
	assert.Equal(t, ReasonTaggedLatest, reason)
}

// An image referenced by a container that survives is in use, whatever state
// that container is in. Stopped counts.
func TestImageHeldByStoppedContainerIsKept(t *testing.T) {
	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{container("c1", "recent", "sha256:v1", 2*24*time.Hour)},
		Images: []dockercli.Image{
			image("sha256:v1", ago(200*24*time.Hour), "myapp:v1"),
			image("sha256:v2", ago(10*24*time.Hour), "myapp:v2"),
		},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Images)
	reason, ok := keptReason(p, "myapp:v1")
	require.True(t, ok)
	assert.Equal(t, ReasonReferenced, reason)
}

// Removing a stale container frees its image in the same run, and the plan
// says which container did it.
func TestCascadeFreesImageAndAttributesIt(t *testing.T) {
	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{container("c1", "web-old", "sha256:v1", 47*24*time.Hour)},
		Images: []dockercli.Image{
			image("sha256:v1", ago(200*24*time.Hour), "myapp:v1"),
			image("sha256:v2", ago(10*24*time.Hour), "myapp:v2"),
		},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	got, ok := target(p.Images, "myapp:v1")
	require.True(t, ok)
	assert.Equal(t, []string{"web-old"}, got.FreedBy)
}

// An id tagged in two repositories and kept for one must not be untagged in
// the other: a partial untag destroys a reference the user still has.
func TestImageKeptInOneRepositoryIsNotUntaggedInAnother(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:shared", ago(100*24*time.Hour), "mirror:old", "myapp:v1"),
		image("sha256:newer", ago(10*24*time.Hour), "mirror:new"),
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	for _, tg := range p.Images {
		for _, cmd := range tg.Commands {
			assert.NotContains(t, cmd, "mirror:old")
			assert.NotContains(t, cmd, "myapp:v1")
		}
	}
}

// Each tag is removed by reference, because an id with several tags cannot be
// removed by id.
func TestMultiTagImageRemovesEveryReference(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:a", ago(100*24*time.Hour), "repo-a:v1", "repo-b:v1"),
		image("sha256:newerA", ago(1*24*time.Hour), "repo-a:v2"),
		image("sha256:newerB", ago(1*24*time.Hour), "repo-b:v2"),
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	got, ok := target(p.Images, "repo-a:v1, repo-b:v1")
	require.True(t, ok)
	assert.Equal(t, [][]string{{"rmi", "repo-a:v1"}, {"rmi", "repo-b:v1"}}, got.Commands)
}

func TestUntaggedImageIsRemovedByID(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{image("sha256:dangling", ago(3*24*time.Hour))}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Images, 1)
	assert.Equal(t, [][]string{{"rmi", "sha256:dangling"}}, p.Images[0].Commands)
}

// A digest-pinned image has no tags, so it looks like a rebuild leftover.
// It still goes, but the plan shows the digest so it can be vetoed.
func TestDigestPinnedImageIsNoted(t *testing.T) {
	img := image("sha256:pinned", ago(3*24*time.Hour))
	img.RepoDigests = []string{"ghcr.io/acme/api@sha256:pinned"}

	p := Compute(dockercli.Snapshot{Images: []dockercli.Image{img}}, noComposeFiles(), defaults(), now)

	require.Len(t, p.Images, 1)
	assert.Contains(t, p.Images[0].Note, "ghcr.io/acme/api@sha256:pinned")
}

// Docker refuses to delete an image another image is built on, so listing one
// would promise a removal that cannot happen.
func TestParentOfKeptImageIsNotPlanned(t *testing.T) {
	base := image("sha256:base", ago(300*24*time.Hour))
	mid := image("sha256:mid", ago(200*24*time.Hour))
	mid.Parent = "sha256:base"
	top := image("sha256:top", ago(100*24*time.Hour), "app:latest")
	top.Parent = "sha256:mid"

	p := Compute(dockercli.Snapshot{Images: []dockercli.Image{base, mid, top}}, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Images, "a whole parent chain under a kept image survives")
	for _, id := range []string{"sha256:base <untagged>", "sha256:mid <untagged>"} {
		reason, ok := keptReason(p, ShortID(id[:13])+" <untagged>")
		if ok {
			assert.Equal(t, ReasonParentOfKept, reason)
		}
	}
}

func TestKeepGlobPinsAnImage(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:v1", ago(200*24*time.Hour), "base/tools:2024"),
		image("sha256:v2", ago(10*24*time.Hour), "base/tools:2026"),
	}}
	o := defaults()
	o.Keep = []string{"base/*"}

	p := Compute(snap, noComposeFiles(), o, now)

	assert.Empty(t, p.Images)
	reason, ok := keptReason(p, "base/tools:2024")
	require.True(t, ok)
	assert.Equal(t, ReasonKeepPattern, reason)
}

// Without a usable timestamp there is no defensible newest, so the whole
// repository stays rather than a coin flip deciding what to delete.
func TestRepositoryWithoutTimestampsKeepsEverything(t *testing.T) {
	snap := dockercli.Snapshot{Images: []dockercli.Image{
		image("sha256:a", "", "handmade:one"),
		image("sha256:b", "", "handmade:two"),
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Images)
	reason, ok := keptReason(p, "handmade:one")
	require.True(t, ok)
	assert.Equal(t, ReasonNoTimestamps, reason)
}
