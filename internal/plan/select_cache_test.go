package plan

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

func lastUsed(d time.Duration) *string {
	s := ago(d)
	return &s
}

// Cache is aged by last use, not by creation: when something was built says
// nothing about whether it is still wanted.
func TestCacheIsAgedByLastUse(t *testing.T) {
	snap := dockercli.Snapshot{Caches: []dockercli.Cache{{Builder: "default", Records: []dockercli.CacheRecord{
		{ID: "old-but-used", Size: 100, CreatedAt: ago(300 * 24 * time.Hour), LastUsedAt: lastUsed(time.Hour)},
		{ID: "new-but-idle", Size: 200, CreatedAt: ago(20 * 24 * time.Hour), LastUsedAt: lastUsed(20 * 24 * time.Hour)},
	}}}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Caches, 1)
	assert.Equal(t, 1, p.Caches[0].Records)
	assert.Equal(t, int64(200), p.Caches[0].Size)
}

// A record never used falls back to its creation time.
func TestCacheNeverUsedFallsBackToCreation(t *testing.T) {
	snap := dockercli.Snapshot{Caches: []dockercli.Cache{{Builder: "default", Records: []dockercli.CacheRecord{
		{ID: "stale", Size: 50, CreatedAt: ago(30 * 24 * time.Hour)},
		{ID: "fresh", Size: 60, CreatedAt: ago(time.Hour)},
	}}}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Caches, 1)
	assert.Equal(t, 1, p.Caches[0].Records)
	assert.Equal(t, int64(50), p.Caches[0].Size)
}

func TestCacheInUseIsNeverPruned(t *testing.T) {
	snap := dockercli.Snapshot{Caches: []dockercli.Cache{{Builder: "default", Records: []dockercli.CacheRecord{
		{ID: "busy", Size: 100, InUse: true, CreatedAt: ago(300 * 24 * time.Hour)},
	}}}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Caches)
	reason, ok := keptReason(p, "default")
	require.True(t, ok)
	assert.Equal(t, ReasonCacheInUse, reason)
}

// buildx prunes one builder at a time, so every builder needs its own command.
// A plain prune would silently leave every non-default builder untouched.
func TestEveryBuilderGetsItsOwnPrune(t *testing.T) {
	stale := []dockercli.CacheRecord{{ID: "s", Size: 10, CreatedAt: ago(30 * 24 * time.Hour)}}
	snap := dockercli.Snapshot{Caches: []dockercli.Cache{
		{Builder: "default", Records: stale},
		{Builder: "ci-builder", Records: stale},
	}}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	require.Len(t, p.Caches, 2)
	assert.Equal(t,
		[]string{"buildx", "prune", "--builder", "default", "--force", "--filter", "until=168h"},
		p.Caches[0].Command)
	assert.Equal(t,
		[]string{"buildx", "prune", "--builder", "ci-builder", "--force", "--filter", "until=168h"},
		p.Caches[1].Command)
}

// The cache threshold is its own flag: 7d for cache while containers stay 30d.
func TestCacheAgeIsIndependentOfContainerAge(t *testing.T) {
	snap := dockercli.Snapshot{
		Containers: []dockercli.Container{container("c1", "svc", "sha256:i", 10*24*time.Hour)},
		Caches: []dockercli.Cache{{Builder: "default", Records: []dockercli.CacheRecord{
			{ID: "r", Size: 10, LastUsedAt: lastUsed(10 * 24 * time.Hour)},
		}}},
	}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	assert.Empty(t, p.Containers, "10 days is inside the 30 day container window")
	require.Len(t, p.Caches, 1, "10 days is outside the 7 day cache window")
	assert.Equal(t, "168h", p.Caches[0].Until)
}

func TestCacheUnavailableIsReportedNotIgnored(t *testing.T) {
	snap := dockercli.Snapshot{CacheUnavailable: "cannot enumerate builders: boom"}

	p := Compute(snap, noComposeFiles(), defaults(), now)

	reason, ok := keptReason(p, "build cache")
	require.True(t, ok)
	assert.Equal(t, ReasonCacheUnusable, reason)
}

// The same inputs in any order must produce the same plan, so a dry run can
// stand in for the apply that follows it.
func TestPlanIsDeterministicUnderShuffledInput(t *testing.T) {
	base := dockercli.Snapshot{
		Containers: []dockercli.Container{
			container("c1", "a", "sha256:i1", 47*24*time.Hour),
			container("c2", "b", "sha256:i2", 60*24*time.Hour),
			container("c3", "c", "sha256:i3", 2*24*time.Hour),
		},
		Images: []dockercli.Image{
			image("sha256:i1", ago(200*24*time.Hour), "app:v1"),
			image("sha256:i2", ago(150*24*time.Hour), "app:v2"),
			image("sha256:i3", ago(10*24*time.Hour), "app:v3"),
		},
		Volumes:  []dockercli.Volume{localVolume("v1"), localVolume("v2")},
		Networks: []dockercli.Network{{ID: "n1", Name: "x", Scope: "local"}, {ID: "n2", Name: "y", Scope: "local"}},
	}
	want := Compute(base, noComposeFiles(), defaults(), now)

	rng := rand.New(rand.NewSource(1))
	for range 20 {
		s := base
		s.Containers = append([]dockercli.Container(nil), base.Containers...)
		s.Images = append([]dockercli.Image(nil), base.Images...)
		s.Volumes = append([]dockercli.Volume(nil), base.Volumes...)
		rng.Shuffle(len(s.Containers), func(i, j int) { s.Containers[i], s.Containers[j] = s.Containers[j], s.Containers[i] })
		rng.Shuffle(len(s.Images), func(i, j int) { s.Images[i], s.Images[j] = s.Images[j], s.Images[i] })
		rng.Shuffle(len(s.Volumes), func(i, j int) { s.Volumes[i], s.Volumes[j] = s.Volumes[j], s.Volumes[i] })

		assert.Equal(t, want, Compute(s, noComposeFiles(), defaults(), now))
	}
}

func TestEachSkipFlagEmptiesItsSection(t *testing.T) {
	snap := dockercli.Snapshot{
		Images:   []dockercli.Image{image("sha256:d", ago(3*24*time.Hour))},
		Volumes:  []dockercli.Volume{localVolume("v1")},
		Networks: []dockercli.Network{{ID: "n1", Name: "x", Scope: "local"}},
		Caches: []dockercli.Cache{{Builder: "default", Records: []dockercli.CacheRecord{
			{ID: "r", Size: 10, CreatedAt: ago(30 * 24 * time.Hour)},
		}}},
	}
	o := defaults()
	o.SkipImages, o.SkipVolumes, o.SkipNetworks, o.SkipCache = true, true, true, true

	p := Compute(snap, noComposeFiles(), o, now)

	assert.Empty(t, p.Images)
	assert.Empty(t, p.Volumes)
	assert.Empty(t, p.Networks)
	assert.Empty(t, p.Caches)
	assert.True(t, p.Empty())
}
