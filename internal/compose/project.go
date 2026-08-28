package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
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

// Project is what one compose project would attach on its next `up`.
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

// Resolve renders every compose file into its claims. Files that name the same
// project are merged, because a directory holding both compose.yaml and
// docker-compose.yml is one project described twice.
func Resolve(ctx context.Context, r dockercli.Runner, files []string) *Claims {
	c := &Claims{
		Projects:   map[string]*Project{},
		Unreadable: map[string]string{},
		images:     map[string]string{},
		volumes:    map[string]string{},
		networks:   map[string]string{},
	}

	for _, file := range files {
		cfg, err := render(ctx, r, file)
		if err != nil {
			c.Unreadable[file] = err.Error()
			continue
		}
		c.add(file, cfg)
	}

	for _, p := range c.Projects {
		sort.Strings(p.Files)
		sort.Strings(p.Images)
		sort.Strings(p.Volumes)
		sort.Strings(p.Networks)
	}
	return c
}

func render(ctx context.Context, r dockercli.Runner, file string) (config, error) {
	var cfg config
	out, errb, err := r.Run(ctx, "compose", "-f", file, "config", "--format", "json")
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

func (c *Claims) add(file string, cfg config) {
	p := c.Projects[cfg.Name]
	if p == nil {
		p = &Project{Name: cfg.Name}
		c.Projects[cfg.Name] = p
	}
	p.Files = append(p.Files, file)

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
