package compose

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resolve(t *testing.T, r *configRunner, files ...string) *Claims {
	t.Helper()
	return Resolve(context.Background(), r, files)
}

// The docker name of a volume is not its compose key. Concatenating the project
// name onto the key gets `name:` and `external:` wrong, and a wrong name means
// the real volume looks unclaimed.
func TestVolumeRealNamesComeFromTheRenderedConfig(t *testing.T) {
	r := &configRunner{byFile: map[string]string{"/srv/app/compose.yaml": `{
	  "name": "webapp",
	  "services": {"db": {"image": "postgres:16"}},
	  "volumes": {
	    "data": {},
	    "renamed": {"name": "custom_bucket"},
	    "shared": {"external": true}
	  },
	  "networks": {"default": {}, "backplane": {"name": "corp_net"}}
	}`}}

	c := resolve(t, r, "/srv/app/compose.yaml")

	assert.Equal(t, []string{"custom_bucket", "shared", "webapp_data"}, c.Projects["webapp"].Volumes)
	assert.Equal(t, []string{"corp_net", "webapp_default"}, c.Projects["webapp"].Networks)

	for name, want := range map[string]bool{
		"webapp_data": true, "custom_bucket": true, "shared": true, "webapp_shared": false,
	} {
		_, ok := c.VolumeProject(name)
		assert.Equal(t, want, ok, name)
	}
}

// A top-level name:, -p, or COMPOSE_PROJECT_NAME all beat the directory name.
func TestTheResolvedProjectNameWins(t *testing.T) {
	r := &configRunner{byFile: map[string]string{
		"/srv/some-directory/compose.yaml": `{"name":"chosen","volumes":{"data":{}}}`,
	}}

	c := resolve(t, r, "/srv/some-directory/compose.yaml")

	require.True(t, c.Known("chosen"))
	assert.False(t, c.Known("some-directory"))
	assert.Equal(t, []string{"chosen_data"}, c.Projects["chosen"].Volumes)
}

// A directory holding both compose.yaml and docker-compose.yml is one project
// described twice. Letting the last file win would drop the other's volumes.
func TestTwoFilesForOneProjectUnionTheirClaims(t *testing.T) {
	r := &configRunner{byFile: map[string]string{
		"/srv/app/compose.yaml": `{"name":"webapp","services":{"a":{"image":"nginx:1.27"}},
		  "volumes":{"data":{}}}`,
		"/srv/app/docker-compose.yml": `{"name":"webapp","services":{"b":{"image":"redis:7"}},
		  "volumes":{"cache":{}}}`,
	}}

	c := resolve(t, r, "/srv/app/compose.yaml", "/srv/app/docker-compose.yml")

	p := c.Projects["webapp"]
	assert.Equal(t, []string{"/srv/app/compose.yaml", "/srv/app/docker-compose.yml"}, p.Files)
	assert.Equal(t, []string{"nginx:1.27", "redis:7"}, p.Images)
	assert.Equal(t, []string{"webapp_cache", "webapp_data"}, p.Volumes)
}

// A file that will not render is unknown, never absent. Promoting a parse
// failure to "this project is gone" is what deletes a live stack's data.
func TestAFileThatWillNotRenderIsRecordedAsUnreadable(t *testing.T) {
	r := &configRunner{
		byFile: map[string]string{"/srv/ok/compose.yaml": project("ok")},
		fail: map[string]string{
			"/srv/broken/compose.yaml": "error while interpolating services.db.image: required variable TAG is missing",
		},
	}

	c := resolve(t, r, "/srv/broken/compose.yaml", "/srv/ok/compose.yaml")

	assert.Contains(t, c.Unreadable["/srv/broken/compose.yaml"], "required variable TAG is missing")
	assert.False(t, c.Known("broken"))
	assert.True(t, c.Known("ok"), "one bad file does not lose the others")
}

func TestUnparseableOrNamelessConfigIsUnreadable(t *testing.T) {
	r := &configRunner{byFile: map[string]string{
		"/srv/a/compose.yaml": "{ this is not json",
		"/srv/b/compose.yaml": `{"volumes":{"data":{}}}`,
	}}

	c := resolve(t, r, "/srv/a/compose.yaml", "/srv/b/compose.yaml")

	assert.Contains(t, c.Unreadable["/srv/a/compose.yaml"], "unparseable compose config")
	assert.Contains(t, c.Unreadable["/srv/b/compose.yaml"], "named no project")
	assert.Empty(t, c.Projects)
}

func TestImageAndNetworkLookups(t *testing.T) {
	r := &configRunner{byFile: map[string]string{"/srv/app/compose.yaml": project("webapp")}}

	c := resolve(t, r, "/srv/app/compose.yaml")

	got, ok := c.ImageProject("postgres:16")
	require.True(t, ok)
	assert.Equal(t, "webapp", got)
	_, ok = c.ImageProject("postgres:17")
	assert.False(t, ok)

	got, ok = c.NetworkProject("webapp_default")
	require.True(t, ok)
	assert.Equal(t, "webapp", got)
}

// A service without an image is built from a Dockerfile and names nothing.
func TestServicesWithoutAnImageClaimNothing(t *testing.T) {
	r := &configRunner{byFile: map[string]string{
		"/srv/app/compose.yaml": `{"name":"webapp","services":{"api":{"build":{"context":"."}}}}`,
	}}

	c := resolve(t, r, "/srv/app/compose.yaml")

	assert.Empty(t, c.Projects["webapp"].Images)
}

func TestNoFilesMeansNoClaims(t *testing.T) {
	r := &configRunner{}

	c := resolve(t, r)

	assert.Empty(t, c.Projects)
	assert.Zero(t, r.calls)
	assert.False(t, c.Known("webapp"))
}

func TestRenderAsksComposeForJSON(t *testing.T) {
	r := &recordingRunner{body: project("webapp")}

	Resolve(context.Background(), r, []string{"/srv/app/compose.yaml"})

	assert.Equal(t,
		[]string{"compose", "-f", "/srv/app/compose.yaml", "config", "--format", "json"},
		r.args)
}

type recordingRunner struct {
	body string
	args []string
}

func (r *recordingRunner) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	r.args = args
	return []byte(r.body), nil, nil
}
