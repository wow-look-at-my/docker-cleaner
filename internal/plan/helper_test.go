package plan

import (
	"context"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// now is the clock reading every test computes against.
var now = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }

func defaults() Options {
	return Options{Age: 30 * 24 * time.Hour, BuildCacheAge: 7 * 24 * time.Hour}
}

// container builds an exited container of the given age.
func container(id, name, image string, exited time.Duration) dockercli.Container {
	c := dockercli.Container{ID: id, Name: "/" + name, Image: image, Created: ago(exited + time.Hour)}
	c.State.Status = "exited"
	c.State.FinishedAt = ago(exited)
	return c
}

func image(id, created string, tags ...string) dockercli.Image {
	return dockercli.Image{ID: id, RepoTags: tags, Created: created}
}

// noComposeFiles is discovery on a machine with no compose projects at all,
// having searched exhaustively.
func noComposeFiles() *compose.Discovery {
	return &compose.Discovery{Claims: compose.Resolve(context.Background(), nil, nil, nil), Complete: true}
}

// incompleteSearch is discovery that could not look everywhere.
func incompleteSearch() *compose.Discovery {
	d := noComposeFiles()
	d.Complete = false
	d.Failures = []string{"/srv: permission denied"}
	return d
}

func target(targets []Target, name string) (Target, bool) {
	for _, t := range targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

func keptReason(p Plan, name string) (Reason, bool) {
	for _, k := range p.Kept {
		if k.Name == name {
			return k.Reason, true
		}
	}
	return "", false
}
