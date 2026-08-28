package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitReference(t *testing.T) {
	for _, tc := range []struct{ ref, repo, version string }{
		{"nginx:1.25", "nginx", "1.25"},
		{"myapp:latest", "myapp", "latest"},
		{"org/myapp:v1", "org/myapp", "v1"},
		// The registry port is a colon that is not a tag separator. Splitting
		// on it would collapse a whole registry into repository "localhost".
		{"localhost:5000/myapp:v1", "localhost:5000/myapp", "v1"},
		{"localhost:5000/myapp", "localhost:5000/myapp", ""},
		{"nginx", "nginx", ""},
	} {
		repo, version := SplitReference(tc.ref)
		assert.Equal(t, tc.repo, repo, tc.ref)
		assert.Equal(t, tc.version, version, tc.ref)
	}
}

func TestIsAnonymousVolume(t *testing.T) {
	hex64 := "0f4c1e9b2a3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
	assert.True(t, IsAnonymousVolume(hex64))
	assert.False(t, IsAnonymousVolume("webapp_pgdata"))
	assert.False(t, IsAnonymousVolume(hex64[:63]))
	assert.False(t, IsAnonymousVolume("g"+hex64[1:]))
}

func TestMatchKeep(t *testing.T) {
	names := []string{"registry.local/team/app:v1", "sha256:11aa22bb33cc", "11aa22bb33cc"}

	// * spans / on purpose, so one pattern covers a whole registry.
	assert.Equal(t, "registry.local/*", MatchKeep([]string{"registry.local/*"}, names))
	assert.Equal(t, "*/app:*", MatchKeep([]string{"*/app:*"}, names))
	assert.Equal(t, "sha256:11aa*", MatchKeep([]string{"sha256:11aa*"}, names))
	assert.Empty(t, MatchKeep([]string{"other/*"}, names))
	assert.Empty(t, MatchKeep(nil, names))
}

func TestMatchKeepDoesNotOvermatch(t *testing.T) {
	assert.Empty(t, MatchKeep([]string{"myapp:*"}, []string{"myapp2:v1"}))
	assert.Equal(t, "myapp:*", MatchKeep([]string{"myapp:*"}, []string{"myapp:v1"}))
}

func TestShortID(t *testing.T) {
	assert.Equal(t, "11aa22bb33cc", ShortID("sha256:11aa22bb33cc44dd"))
	assert.Equal(t, "abc", ShortID("abc"))
}
