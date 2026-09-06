package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const createEvent = `{"status":"create","id":"c1","Type":"container","Action":"create",` +
	`"Actor":{"ID":"c1","Attributes":{` +
	`"com.docker.compose.project":"webapp",` +
	`"com.docker.compose.project.config_files":"/srv/webapp/compose.yaml,/srv/webapp/override.yaml",` +
	`"com.docker.compose.service":"db","image":"postgres:16","name":"webapp-db-1"}},` +
	`"scope":"local","time":1756400000}`

func TestEventYieldsProjectAndFiles(t *testing.T) {
	project, files, ok := ProjectFromEvent([]byte(createEvent))

	require.True(t, ok)
	assert.Equal(t, "webapp", project)
	assert.Equal(t, []string{"/srv/webapp/compose.yaml", "/srv/webapp/override.yaml"}, files)
}

// A throwaway `compose run` container carries the same labels, and its paths are
// just as true as any other container's.
func TestOneOffEventIsStillASource(t *testing.T) {
	line := `{"Type":"container","Action":"create","Actor":{"Attributes":{` +
		`"com.docker.compose.project":"webapp",` +
		`"com.docker.compose.project.config_files":"/srv/webapp/compose.yaml",` +
		`"com.docker.compose.oneoff":"True"}}}`

	project, files, ok := ProjectFromEvent([]byte(line))

	require.True(t, ok)
	assert.Equal(t, "webapp", project)
	assert.Equal(t, []string{"/srv/webapp/compose.yaml"}, files)
}

func TestEventsWithoutComposeLabelsAreIgnored(t *testing.T) {
	for name, line := range map[string]string{
		"plain container": `{"Type":"container","Action":"create","Actor":{"Attributes":{"image":"alpine"}}}`,
		"no config files": `{"Type":"container","Action":"create","Actor":{"Attributes":{"com.docker.compose.project":"webapp"}}}`,
		"not json":        `{ truncated`,
		"empty":           ``,
	} {
		_, _, ok := ProjectFromEvent([]byte(line))
		assert.False(t, ok, name)
	}
}
