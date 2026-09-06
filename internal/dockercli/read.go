package dockercli

import (
	"context"
	"errors"
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"strings"
)

// Read gathers every fact the selector needs. A read that cannot complete is
// fatal: a partial read produces a confident, wrong plan.
func Read(ctx context.Context, r Runner) (Snapshot, error) {
	var s Snapshot

	if err := runJSON(ctx, r, &s.Version, "version", "--format", "json"); err != nil {
		return s, err
	}
	if s.Version.Server == nil {
		return s, errors.New("docker daemon is not reachable")
	}

	before, errb, err := r.Run(ctx, "system", "df")
	if err != nil {
		return s, fmt.Errorf("docker system df: %w: %s", err, strings.TrimSpace(string(errb)))
	}
	s.BeforeText = string(before)

	if err := runJSON(ctx, r, &s.DiskUsage, "system", "df", "-v", "--format", "json"); err != nil {
		return s, err
	}

	if s.Containers, err = readContainers(ctx, r); err != nil {
		return s, err
	}
	if s.Images, err = readImages(ctx, r); err != nil {
		return s, err
	}
	if s.Volumes, err = readVolumes(ctx, r); err != nil {
		return s, err
	}
	if s.Networks, err = readNetworks(ctx, r); err != nil {
		return s, err
	}
	s.Caches, s.CacheUnavailable = readCaches(ctx, r, s.DiskUsage)
	return s, nil
}

func readContainers(ctx context.Context, r Runner) ([]Container, error) {
	out, errb, err := r.Run(ctx, "ps", "-aq", "--no-trunc")
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w: %s", err, strings.TrimSpace(string(errb)))
	}
	return inspect[Container](ctx, r, "container", lines(out))
}

// readImages lists without -a on purpose: -a surfaces intermediate images,
// whose removal docker refuses and which no user ever asked to reclaim.
func readImages(ctx context.Context, r Runner) ([]Image, error) {
	type row struct {
		ID string `json:"ID"`
	}
	rows, err := runNDJSON[row](ctx, r, "image", "ls", "--no-trunc", "--format", "json")
	if err != nil {
		return nil, err
	}
	seen := set.New[string]()
	var ids []string
	for _, x := range rows {
		if x.ID != "" && !seen.Contains(x.ID) {
			seen.Add(x.ID)
			ids = append(ids, x.ID)
		}
	}
	return inspect[Image](ctx, r, "image", ids)
}

func readVolumes(ctx context.Context, r Runner) ([]Volume, error) {
	type row struct {
		Name string `json:"Name"`
	}
	rows, err := runNDJSON[row](ctx, r, "volume", "ls", "--format", "json")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, x := range rows {
		if x.Name != "" {
			names = append(names, x.Name)
		}
	}
	return inspect[Volume](ctx, r, "volume", names)
}

func readNetworks(ctx context.Context, r Runner) ([]Network, error) {
	type row struct {
		ID string `json:"ID"`
	}
	rows, err := runNDJSON[row](ctx, r, "network", "ls", "--no-trunc", "--format", "json")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, x := range rows {
		if x.ID != "" {
			ids = append(ids, x.ID)
		}
	}
	return inspect[Network](ctx, r, "network", ids)
}

// readCaches enumerates every builder, because `docker buildx du` and `docker
// buildx prune` both act on a single builder and a docker-container builder
// keeps its cache in its own volume. A failure here is reported, not fatal:
// build cache is a step of the read, and losing it must not block the rest of the run.
func readCaches(ctx context.Context, r Runner, du DiskUsage) ([]Cache, string) {
	builders, err := runNDJSON[Builder](ctx, r, "buildx", "ls", "--format", "json")
	if err != nil || len(builders) == 0 {
		if len(du.BuildCache) > 0 {
			return []Cache{{Builder: "", Records: du.BuildCache}}, ""
		}
		if err != nil {
			return nil, "cannot enumerate builders: " + err.Error()
		}
		return nil, ""
	}

	var caches []Cache
	var failures []string
	for _, b := range builders {
		if b.Name == "" {
			continue
		}
		recs, err := runNDJSON[CacheRecord](ctx, r, "buildx", "du", "--builder", b.Name, "--format", "json")
		if err != nil {
			failures = append(failures, b.Name+": "+err.Error())
			continue
		}
		caches = append(caches, Cache{Builder: b.Name, Records: recs})
	}
	if len(caches) == 0 && len(failures) > 0 {
		return nil, "cannot read build cache: " + strings.Join(failures, "; ")
	}
	return caches, strings.Join(failures, "; ")
}
