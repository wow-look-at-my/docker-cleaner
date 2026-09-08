package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
)

// config is the part of `docker compose config --format json` needed here.
// Map keys are the compose file's keys; real docker names come from Name.
type config struct {
	Name     string `json:"name"`
	Services map[string]struct {
		Image string `json:"image"`
	} `json:"services"`
	Volumes map[string]struct {
		Name     string `json:"name"`
		External bool   `json:"external"`
	} `json:"volumes"`
	Networks map[string]struct {
		Name     string `json:"name"`
		External bool   `json:"external"`
	} `json:"networks"`
}

// Project is what a compose project would attach on its next `up`.
type Project struct {
	Name     string
	Files    []string
	Images   []string
	Volumes  []string
	Networks []string
}

// Claims is the resolved view of every project found on disk.
type Claims struct {
	Projects map[string]*Project
	// Unreadable maps a file to why it would not render: unknown, not absent.
	Unreadable map[string]string

	images   map[string]string
	volumes  map[string]string
	networks map[string]string
}

// renderWorkers bounds the `docker compose config` processes a render forks.
const renderWorkers = 8

// Resolve renders each project's whole file set into its claims. A set is
// every file compose itself used, in compose's own order.
//
// The set renders in a single call, because that is the only way to see the
// project compose builds. `-f` turns off the automatic merge of an override
// beside the base file, so a base file rendered alone declares none of what its
// override adds, and an override rendered alone is not a project at all.
//
// The sets render together, and the results are merged in set order, so the
// claims never depend on the order the renders finished in. A cancelled context
// leaves the rest unread, which the caller reports as unresolved.
func Resolve(ctx context.Context, r dockercli.Runner, sets [][]string, p *progress.Reporter) *Claims {
	c := &Claims{
		Projects:   map[string]*Project{},
		Unreadable: map[string]string{},
		images:     map[string]string{},
		volumes:    map[string]string{},
		networks:   map[string]string{},
	}

	for i, rendered := range renderAll(ctx, r, sets, p) {
		if rendered.err != nil {
			c.Unreadable[strings.Join(sets[i], " with ")] = rendered.err.Error()
			continue
		}
		c.add(sets[i], rendered.cfg)
	}

	for _, p := range c.Projects {
		// Files keep compose's order: a later file overrides what precedes it.
		sort.Strings(p.Images)
		sort.Strings(p.Volumes)
		sort.Strings(p.Networks)
	}
	return c
}

// rendered is what a file's `docker compose config` produced.
type rendered struct {
	cfg config
	err error
}

// renderAll renders the files together and returns a result per file, in the
// order the files were given. A file nothing reached carries an error, so a
// search cut short can never read as "this project declares nothing".
func renderAll(ctx context.Context, r dockercli.Runner, sets [][]string, p *progress.Reporter) []rendered {
	out := make([]rendered, len(sets))
	if len(sets) == 0 {
		return out
	}
	for i := range out {
		out[i].err = errors.New("the run stopped before this file was read")
	}

	var done atomic.Int64
	p.Detail(func() string {
		return fmt.Sprintf("(%d of %d)", done.Load(), len(sets))
	})
	defer p.Detail(nil)

	work := make(chan int)
	var wg sync.WaitGroup
	for range min(renderWorkers, len(sets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				out[i].cfg, out[i].err = render(ctx, r, sets[i])
				done.Add(1)
			}
		}()
	}
	for i := range sets {
		select {
		case work <- i:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return out
		}
	}
	close(work)
	wg.Wait()
	return out
}

func render(ctx context.Context, r dockercli.Runner, files []string) (config, error) {
	var cfg config
	args := []string{"compose"}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "config", "--format", "json")
	out, errb, err := r.Run(ctx, args...)
	if err != nil {
		msg := strings.TrimSpace(string(errb))
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return cfg, fmt.Errorf("%s", msg)
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		return cfg, fmt.Errorf("unparseable compose config: %v", err)
	}
	if cfg.Name == "" {
		return cfg, fmt.Errorf("compose config named no project")
	}
	return cfg, nil
}

func (c *Claims) add(files []string, cfg config) {
	p := c.Projects[cfg.Name]
	if p == nil {
		p = &Project{Name: cfg.Name}
		c.Projects[cfg.Name] = p
	}
	for _, f := range files {
		p.Files = appendNew(p.Files, f)
	}

	for _, svc := range cfg.Services {
		if svc.Image == "" {
			continue
		}
		p.Images = appendNew(p.Images, svc.Image)
		c.images[svc.Image] = cfg.Name
	}
	for key, v := range cfg.Volumes {
		name := realName(cfg.Name, key, v.Name, v.External)
		p.Volumes = appendNew(p.Volumes, name)
		c.volumes[name] = cfg.Name
	}
	for key, n := range cfg.Networks {
		name := realName(cfg.Name, key, n.Name, n.External)
		p.Networks = appendNew(p.Networks, name)
		c.networks[name] = cfg.Name
	}
}

// realName resolves a compose key to its docker name: Name when compose set
// it, the key when external, else project_key.
func realName(project, key, name string, external bool) string {
	if name != "" {
		return name
	}
	if external {
		return key
	}
	return project + "_" + key
}

func appendNew(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// ImageProject reports which live project names an image reference.
func (c *Claims) ImageProject(ref string) (string, bool) { return lookup(c.images, ref) }

// VolumeProject reports which live project declares a volume.
func (c *Claims) VolumeProject(name string) (string, bool) { return lookup(c.volumes, name) }

// NetworkProject reports which live project declares a network.
func (c *Claims) NetworkProject(name string) (string, bool) { return lookup(c.networks, name) }

// Known reports whether any compose file on disk declares this project.
func (c *Claims) Known(project string) bool {
	_, ok := c.Projects[project]
	return ok
}

func lookup(m map[string]string, k string) (string, bool) {
	v, ok := m[k]
	return v, ok
}
