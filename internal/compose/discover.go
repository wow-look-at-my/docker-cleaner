package compose

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
	"github.com/wow-look-at-my/go-containers/set"
)

// Label keys compose writes. Only container labels name files on disk, which
// is why the index exists.
const (
	LabelProject     = "com.docker.compose.project"
	LabelConfigFiles = "com.docker.compose.project.config_files"
)

// Discovery is the compose picture for a run.
type Discovery struct {
	Claims *Claims
	// Index is carried so the caller can persist what this run learned.
	Index *Index
	// Complete false means a compose file this run named went unread.
	Complete bool
	Failures []string
	Warning  string

	// unheard: nothing ever named a file for these, so they are never deleted.
	unheard set.Set[string]
}

// Options configures discovery.
type Options struct {
	Index    *Index
	Now      time.Time
	Progress *progress.Reporter
}

// Discover resolves every compose project that could own a docker resource.
//
// Docker holds the answer, and nothing else is consulted. Every container
// carries the files that declare its project, whether it runs or not, so a
// project that has ever been started names its own files. The index keeps
// those paths after `down` deletes the containers that carried them.
//
// A project neither explains has never been started while anything was
// watching, so nothing is hunted for on the disk. It is unknown, and it keeps
// what it claims.
func Discover(ctx context.Context, r dockercli.Runner, containers []dockercli.Container, wanted []string, o Options) *Discovery {
	idx := o.Index
	d := &Discovery{Complete: true, Index: idx, unheard: set.New[string]()}

	for _, c := range containers {
		labels := c.Config.Labels
		idx.Record(labels[LabelProject], splitList(labels[LabelConfigFiles]), o.Now)
	}

	files := map[string]bool{}
	for _, project := range wanted {
		found := idx.Files(project)
		for _, f := range found {
			files[f] = true
		}
		// Nothing has ever named a file for a project the index never
		// recorded, so a missing file says nothing about it.
		if len(found) == 0 && !idx.Recorded(project) {
			d.unheard.Add(project)
		}
	}

	found := sortedKeys(files)
	if len(found) > 0 {
		o.Progress.Stage("reading compose files")
	}
	d.Claims = Resolve(ctx, r, found, o.Progress)
	for project, p := range d.Claims.Projects {
		idx.Record(project, p.Files, o.Now)
	}

	// A run cut short read neither every directory nor every file, so a project
	// no compose file names is not deleted. This covers the render.
	if ctx.Err() != nil && d.Complete {
		d.Complete = false
		d.Failures = append(d.Failures, "the run ran out of time before every compose file was read")
	}
	if len(d.Claims.Unreadable) > 0 {
		d.Complete = false
	}
	forgetUnclaimed(idx, d.Claims, wanted)
	d.Warning = idx.Warning
	return d
}

// Resolution is what discovery concluded about a project.
type Resolution int

const (
	// Alive: a compose file on disk still declares the project.
	Alive Resolution = iota
	// Deleted: docker named this project's file, and that file is gone.
	Deleted
	// Unknown: nothing named a file for it, so nothing acts on it.
	Unknown
)

// Resolve classifies a project name found on a docker resource's labels.
func (d *Discovery) Resolve(project string) Resolution {
	switch {
	case d.Claims.Known(project):
		return Alive
	case !d.Complete || d.unheard.Contains(project):
		return Unknown
	default:
		return Deleted
	}
}

// forgetUnclaimed drops what the index has no reason to hold. A record whose
// files are gone is the evidence that retires a project, so it stays while a
// docker resource still claims that project. After nothing claims it, there is
// nothing left to remember.
func forgetUnclaimed(idx *Index, claims *Claims, wanted []string) {
	want := set.New[string]()
	for _, project := range wanted {
		want.Add(project)
	}
	var gone []string
	for project := range idx.Projects {
		if !want.Contains(project) && !claims.Known(project) && len(idx.Files(project)) == 0 {
			gone = append(gone, project)
		}
	}
	for _, project := range gone {
		idx.Forget(project)
	}
}

// splitList reads compose's comma-joined config_files label.
func splitList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
