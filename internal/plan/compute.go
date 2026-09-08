package plan

import (
	"sort"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
	"github.com/wow-look-at-my/go-containers/set"
)

// builder carries the state a Compute call threads through its phases.
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
	seen := set.New[string]()
	add := func(labels map[string]string) {
		if p := labels[compose.LabelProject]; p != "" {
			seen.Add(p)
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

	out := make([]string, 0, seen.Len())
	for p := range seen.All() {
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
	}
	if d != nil {
		b.plan.ComposeFailures = d.Failures
		if d.Warning != "" {
			b.plan.Warnings = append(b.plan.Warnings, d.Warning)
		}
		for file, why := range d.Claims.Unreadable {
			b.plan.Warnings = append(b.plan.Warnings, "compose file "+file+" could not be read: "+why)
		}
		sort.Strings(b.plan.Warnings)
	}

	// Containers go before the rest, so the cascade sees what their removal frees.
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

func (b *builder) keep(kind Kind, name string, reason Reason, detail string) {
	b.plan.Kept = append(b.plan.Kept, Kept{Kind: kind, Name: name, Reason: reason, Detail: detail})
}

func (b *builder) claims() *compose.Claims {
	if b.disco == nil {
		return &compose.Claims{}
	}
	return b.disco.Claims
}

// composeUnresolved handles a resource whose project no compose file explains.
// After an exhaustive search that means the project was deleted, so the
// resource collects. After an incomplete search it means nothing, so it is kept:
// not looking is never evidence.
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

// composeUnresolvedImage keeps every image while any compose file failed to
// render: an unreadable file may name this image.
func (b *builder) composeUnresolvedImage(dockercli.Image) (Reason, string, bool) {
	if b.disco == nil || b.disco.Complete {
		return "", "", false
	}
	return ReasonScanIncomplete, "the compose search was incomplete", true
}
