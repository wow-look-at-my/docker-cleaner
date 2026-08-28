package plan

import (
	"sort"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// builder carries the state one Compute call threads through its phases.
type builder struct {
	snap        dockercli.Snapshot
	opt         Options
	now         time.Time
	cutoff      time.Time
	cacheCutoff time.Time
	disco       *compose.Discovery
	refs        *refs
	removing    map[string]bool
	plan        Plan
}

// ComposeProjects lists every project name a docker resource claims to belong
// to. Discovery needs this up front so it knows which projects it must resolve
// before anything can be called garbage.
func ComposeProjects(s dockercli.Snapshot) []string {
	seen := map[string]bool{}
	add := func(labels map[string]string) {
		if p := labels[compose.LabelProject]; p != "" {
			seen[p] = true
		}
	}
	for _, c := range s.Containers {
		add(c.Config.Labels)
	}
	for _, v := range s.Volumes {
		add(v.Labels)
	}
	for _, n := range s.Networks {
		add(n.Labels)
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Compute decides the whole run. It is pure and deterministic: the same
// snapshot, options and clock reading always produce the same plan, which is
// what lets a dry run stand in for the real thing.
func Compute(s dockercli.Snapshot, d *compose.Discovery, o Options, now time.Time) Plan {
	b := &builder{
		snap:        s,
		opt:         o,
		now:         now,
		cutoff:      now.Add(-o.Age),
		cacheCutoff: now.Add(-o.BuildCacheAge),
		disco:       d,
		removing:    map[string]bool{},
	}
	b.plan = Plan{
		Now:             now,
		Age:             o.Age,
		BuildCacheAge:   o.BuildCacheAge,
		Cutoff:          b.cutoff,
		CacheCutoff:     b.cacheCutoff,
		ComposeComplete: d == nil || d.Complete,
		DirsWalked:      dirsWalked(d),
	}
	if d != nil {
		b.plan.ComposeFailures = d.Failures
		for _, m := range d.Skipped {
			b.plan.SkippedMounts = append(b.plan.SkippedMounts, m.Point+" ("+m.Skip+")")
		}
		if d.Warning != "" {
			b.plan.Warnings = append(b.plan.Warnings, d.Warning)
		}
		for file, why := range d.Claims.Unreadable {
			b.plan.Warnings = append(b.plan.Warnings, "compose file "+file+" could not be read: "+why)
		}
		sort.Strings(b.plan.Warnings)
	}

	// Containers go first so the cascade can see what their removal frees.
	// Everything after this consults b.removing rather than the raw container
	// list.
	b.selectContainers()
	b.refs = buildRefs(s.Containers, s.Networks, b.removing)

	b.selectImages()
	b.selectVolumes()
	b.selectNetworks()
	b.selectCache()

	sort.SliceStable(b.plan.Kept, func(i, j int) bool {
		if b.plan.Kept[i].Kind != b.plan.Kept[j].Kind {
			return b.plan.Kept[i].Kind < b.plan.Kept[j].Kind
		}
		return b.plan.Kept[i].Name < b.plan.Kept[j].Name
	})
	return b.plan
}

func dirsWalked(d *compose.Discovery) int {
	if d == nil {
		return 0
	}
	return d.DirsWalked
}

func (b *builder) keep(kind Kind, name string, reason Reason, detail string) {
	b.plan.Kept = append(b.plan.Kept, Kept{Kind: kind, Name: name, Reason: reason, Detail: detail})
}

func (b *builder) claims() *compose.Claims {
	if b.disco == nil {
		return &compose.Claims{}
	}
	return b.disco.Claims
}

// composeUnresolved decides what to do with a resource labelled for a project
// no compose file explains.
//
// When the search was exhaustive, no file means the project was deleted and
// the resource is collectible: deleting the compose file is how a project is
// retired. When the search could not be exhaustive, the same absence means
// nothing at all, so the resource is kept. Not looking is never evidence.
func (b *builder) composeUnresolved(labels map[string]string) (Reason, string, bool) {
	project := labels[compose.LabelProject]
	if project == "" || b.disco == nil {
		return "", "", false
	}
	if b.disco.Resolve(project) == compose.Unknown {
		return ReasonScanIncomplete, "project " + project, true
	}
	return "", "", false
}

func (b *builder) composeImageProject(img dockercli.Image) (string, bool) {
	for _, ref := range img.RepoTags {
		if project, ok := b.claims().ImageProject(ref); ok {
			return project, true
		}
	}
	for _, ref := range img.RepoDigests {
		if project, ok := b.claims().ImageProject(ref); ok {
			return project, true
		}
	}
	return "", false
}

// composeUnresolvedImage keeps an image while any compose file failed to
// render. An unreadable file may well name this image, and a missing variable
// must never be the reason an image disappears.
func (b *builder) composeUnresolvedImage(dockercli.Image) (Reason, string, bool) {
	if b.disco == nil || b.disco.Complete {
		return "", "", false
	}
	return ReasonScanIncomplete, "the compose search was incomplete", true
}
