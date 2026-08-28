package plan

import (
	"sort"
	"strings"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// refs maps each resource to the containers referencing it, across every
// container regardless of state.
type refs struct {
	images   map[string][]string
	byName   map[string][]string
	volumes  map[string][]string
	networks map[string][]string
	names    map[string]string
	removing map[string]bool
}

// buildRefs walks every container once. Networks come from the containers
// themselves as well as from `network inspect`, because inspect lists live
// endpoints only: a stopped container's membership appears nowhere else, and
// missing it deletes the network a stopped stack needs to start again.
func buildRefs(containers []dockercli.Container, networks []dockercli.Network, removing map[string]bool) *refs {
	r := &refs{
		images:   map[string][]string{},
		byName:   map[string][]string{},
		volumes:  map[string][]string{},
		networks: map[string][]string{},
		names:    map[string]string{},
		removing: removing,
	}

	for _, c := range containers {
		name := containerName(c)
		r.names[c.ID] = name

		if c.Image != "" {
			r.images[c.Image] = append(r.images[c.Image], c.ID)
		}
		if c.Config.Image != "" {
			r.byName[c.Config.Image] = append(r.byName[c.Config.Image], c.ID)
		}
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				r.volumes[m.Name] = append(r.volumes[m.Name], c.ID)
			}
		}
		for _, n := range c.NetworkSettings.Networks {
			if n.NetworkID != "" {
				r.networks[n.NetworkID] = append(r.networks[n.NetworkID], c.ID)
			}
		}
	}

	for _, n := range networks {
		for id := range n.Containers {
			r.networks[n.ID] = append(r.networks[n.ID], id)
		}
	}
	return r
}

// state is what the reference graph concludes about one resource.
type state struct {
	// held is true while a container that survives this run still references
	// the resource.
	held bool
	// by names the containers whose removal frees it, empty when nothing
	// referenced it in the first place.
	by []string
	// holders names the survivors keeping it, for the report.
	holders []string
}

func (r *refs) status(ids []string) state {
	if len(ids) == 0 {
		return state{}
	}
	var freed, held []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		name := r.names[id]
		if name == "" {
			name = ShortID(id)
		}
		if r.removing[id] {
			freed = append(freed, name)
		} else {
			held = append(held, name)
		}
	}
	sort.Strings(freed)
	sort.Strings(held)
	if len(held) > 0 {
		return state{held: true, holders: held}
	}
	return state{by: freed}
}

func (r *refs) image(id string) state     { return r.status(r.images[id]) }
func (r *refs) volume(name string) state  { return r.status(r.volumes[name]) }
func (r *refs) network(id string) state   { return r.status(r.networks[id]) }
func (r *refs) imageByName(ref string) state { return r.status(r.byName[ref]) }

func containerName(c dockercli.Container) string {
	if c.Name != "" {
		return strings.TrimPrefix(c.Name, "/")
	}
	return ShortID(c.ID)
}
