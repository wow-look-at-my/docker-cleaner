package compose

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// Label keys compose writes. Only container labels name files on disk, which
// is why the index exists.
const (
	LabelProject     = "com.docker.compose.project"
	LabelConfigFiles = "com.docker.compose.project.config_files"
)

// Discovery is the compose picture for one run.
type Discovery struct {
	Claims *Claims
	// Index is carried so the caller can persist what this run learned.
	Index *Index
	// Complete false means an unresolved project is unknown, not deleted.
	Complete bool
	Failures []string
	Skipped  []Mount
	// DirsWalked is zero when the index answered everything.
	DirsWalked int
	Warning    string
}

// Options configures discovery.
type Options struct {
	Index   *Index
	Scanner Scanner
	// Rescan walks the disk even when the index already answers everything.
	Rescan bool
	Now    time.Time
}

// Discover resolves every compose project that could own a docker resource.
//
// Container labels are exact and free, so they go first. The index supplies
// what `down` deleted. Only a project that neither can explain is worth a
// filesystem walk, which is what keeps an ordinary run to a handful of stat
// calls.
func Discover(ctx context.Context, r dockercli.Runner, containers []dockercli.Container, wanted []string, o Options) *Discovery {
	idx := o.Index
	d := &Discovery{Complete: true, Index: idx}

	for _, c := range containers {
		project := c.Config.Labels[LabelProject]
		files := splitList(c.Config.Labels[LabelConfigFiles])
		idx.Record(project, files, o.Now)
	}

	files := map[string]bool{}
	unresolved := false
	for _, project := range wanted {
		found := idx.Files(project)
		for _, f := range found {
			files[f] = true
		}
		if len(found) == 0 {
			unresolved = true
		}
	}

	if (unresolved || o.Rescan) && o.Scanner != nil {
		res, err := o.Scanner.Scan()
		if err != nil {
			d.Complete = false
			d.Failures = append(d.Failures, err.Error())
		}
		for _, f := range res.Files {
			files[f] = true
		}
		d.Failures = append(d.Failures, res.Failures...)
		d.Skipped = res.Skipped
		d.DirsWalked = res.Dirs
		if !res.Complete() {
			d.Complete = false
		}
	}

	d.Claims = Resolve(ctx, r, sortedKeys(files))
	for project, p := range d.Claims.Projects {
		idx.Record(project, p.Files, o.Now)
	}
	// Drop a project whose files all vanished, so the index tracks the disk.
	for _, project := range wanted {
		if !d.Claims.Known(project) && len(idx.Files(project)) == 0 {
			idx.Forget(project)
		}
	}
	d.Warning = idx.Warning
	return d
}

// Resolution is what discovery concluded about one project.
type Resolution int

const (
	// Alive: a compose file on disk still declares the project.
	Alive Resolution = iota
	// Deleted: an exhaustive search found no such file.
	Deleted
	// Unknown: the search was not exhaustive, so nothing acts on it.
	Unknown
)

// Resolve classifies a project name found on a docker resource's labels.
func (d *Discovery) Resolve(project string) Resolution {
	switch {
	case d.Claims.Known(project):
		return Alive
	case d.Complete:
		return Deleted
	default:
		return Unknown
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
