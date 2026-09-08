package plan

import (
	"sort"
	"strings"

	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
	"github.com/wow-look-at-my/go-containers/set"
)

// predefinedNetworks cannot be removed, so they never belong in a plan.
var predefinedNetworks = set.Of[string]("bridge", "host", "none")

// selectVolumes picks volumes nothing will attach.
//
// "Nothing will attach" is the whole difficulty: a stack that is merely `down`
// has no containers either, and docker keeps no record telling them apart.
// So a volume a compose file on disk still declares is in use, whatever the
// container list says.
func (b *builder) selectVolumes() {
	sizes := map[string]int64{}
	for _, v := range b.snap.DiskUsage.Volumes {
		sizes[v.Name] = v.Size
	}

	volumes := append([]dockercli.Volume(nil), b.snap.Volumes...)
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Name < volumes[j].Name })

	for _, v := range volumes {
		if v.Driver != "" && v.Driver != "local" {
			b.keep(KindVolume, v.Name, ReasonNonLocalDriver, v.Driver)
			continue
		}
		if v.Scope != "" && v.Scope != "local" {
			b.keep(KindVolume, v.Name, ReasonSwarmScope, v.Scope)
			continue
		}
		if st := b.refs.volume(v.Name); st.held {
			b.keep(KindVolume, v.Name, ReasonInUse, strings.Join(st.holders, ", "))
			continue
		}
		if project, ok := b.claims().VolumeProject(v.Name); ok {
			b.keep(KindVolume, v.Name, ReasonClaimedByCompos, project)
			continue
		}
		if reason, detail, ok := b.composeUnresolved(v.Labels); ok {
			b.keep(KindVolume, v.Name, reason, detail)
			continue
		}
		if b.opt.SkipVolumes {
			b.keep(KindVolume, v.Name, ReasonStepDisabled, "")
			continue
		}

		b.plan.Volumes = append(b.plan.Volumes, Target{
			Kind:     KindVolume,
			ID:       v.Name,
			Name:     volumeName(v),
			Detail:   volumeDetail(v),
			Note:     volumeNote(v),
			Size:     sizes[v.Name],
			FreedBy:  b.refs.volume(v.Name).by,
			Commands: [][]string{dockercli.RemoveVolume(v.Name)},
		})
	}
}

// selectNetworks picks custom networks nothing attaches. Compose recreates a
// network on the next `up`, so this is the cheapest thing here to get wrong,
// but a stopped container still belonging to a project is a real reference.
func (b *builder) selectNetworks() {
	networks := append([]dockercli.Network(nil), b.snap.Networks...)
	sort.Slice(networks, func(i, j int) bool { return networks[i].Name < networks[j].Name })

	configFrom := set.New[string]()
	for _, n := range networks {
		if n.ConfigFrom.Network != "" {
			configFrom.Add(n.ConfigFrom.Network)
		}
	}

	for _, n := range networks {
		switch {
		case predefinedNetworks.Contains(n.Name), n.Driver == "null", n.Driver == "host":
			b.keep(KindNetwork, n.Name, ReasonPredefined, n.Driver)
			continue
		case n.Scope != "" && n.Scope != "local":
			b.keep(KindNetwork, n.Name, ReasonSwarmScope, n.Scope)
			continue
		case n.Ingress:
			b.keep(KindNetwork, n.Name, ReasonIngress, "")
			continue
		case n.ConfigOnly, configFrom.Contains(n.Name):
			b.keep(KindNetwork, n.Name, ReasonConfigOnly, "")
			continue
		}
		if st := b.refs.network(n.ID); st.held {
			b.keep(KindNetwork, n.Name, ReasonInUse, strings.Join(st.holders, ", "))
			continue
		}
		if project, ok := b.claims().NetworkProject(n.Name); ok {
			b.keep(KindNetwork, n.Name, ReasonClaimedByCompos, project)
			continue
		}
		if reason, detail, ok := b.composeUnresolved(n.Labels); ok {
			b.keep(KindNetwork, n.Name, reason, detail)
			continue
		}
		if b.opt.SkipNetworks {
			b.keep(KindNetwork, n.Name, ReasonStepDisabled, "")
			continue
		}

		b.plan.Networks = append(b.plan.Networks, Target{
			Kind:     KindNetwork,
			ID:       n.ID,
			Name:     n.Name,
			Detail:   n.Driver,
			FreedBy:  b.refs.network(n.ID).by,
			Commands: [][]string{dockercli.RemoveNetwork(n.ID)},
		})
	}
}

// selectCache ages build cache by last use, which is the only measure docker
// offers that says whether a thing is still wanted. Every builder is pruned:
// buildx acts on a builder at a time, and a docker-container builder holds
// its cache in a volume a plain prune never reaches.
func (b *builder) selectCache() {
	if b.snap.CacheUnavailable != "" {
		b.keep(KindBuildCache, "build cache", ReasonCacheUnusable, b.snap.CacheUnavailable)
	}
	if b.opt.SkipCache {
		if len(b.snap.Caches) > 0 {
			b.keep(KindBuildCache, "build cache", ReasonStepDisabled, "")
		}
		return
	}

	until := GoDuration(b.opt.BuildCacheAge)
	for _, c := range b.snap.Caches {
		var stale, size int64
		var inUse, recent int
		for _, rec := range c.Records {
			if rec.InUse {
				inUse++
				continue
			}
			when := rec.CreatedAt
			if rec.LastUsedAt != nil && *rec.LastUsedAt != "" {
				when = *rec.LastUsedAt
			}
			if t, ok := dockercli.ParseTime(when); ok && t.After(b.cacheCutoff) {
				recent++
				continue
			}
			stale++
			size += rec.Size
		}

		name := c.Builder
		if name == "" {
			name = "default"
		}
		if inUse > 0 {
			b.keep(KindBuildCache, name, ReasonCacheInUse, plural(inUse, "record"))
		}
		if recent > 0 {
			b.keep(KindBuildCache, name, ReasonCacheRecent, plural(recent, "record"))
		}
		if stale == 0 {
			continue
		}
		b.plan.Caches = append(b.plan.Caches, CachePlan{
			Builder: c.Builder,
			Records: int(stale),
			Size:    size,
			Until:   until,
			Command: dockercli.PruneBuildCache(c.Builder, until),
		})
	}
}

func volumeName(v dockercli.Volume) string {
	if IsAnonymousVolume(v.Name) {
		return v.Name[:12] + " <anonymous>"
	}
	return v.Name
}

func volumeDetail(v dockercli.Volume) string {
	if project := v.Labels[compose.LabelProject]; project != "" {
		return "compose project " + project
	}
	if IsAnonymousVolume(v.Name) {
		return "anonymous"
	}
	return "named, no compose project"
}

// volumeNote flags a buildx builder's state volume: removing it discards that
// builder's whole cache immediately instead of ageing it out.
func volumeNote(v dockercli.Volume) string {
	if strings.HasPrefix(v.Name, "buildx_buildkit_") {
		return "buildx builder state: this is that builder's entire cache"
	}
	return ""
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
