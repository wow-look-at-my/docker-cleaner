package dockercli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
	"github.com/wow-look-at-my/go-containers/set"
)

// listing is what the cheap enumerations found. The rest of the read is a call
// per batch of these, which is where the step count comes from.
type listing struct {
	containers []string
	images     []string
	volumes    []string
	networks   []string
	builders   []Builder
}

// steps is how many calls the counted part of the read still has to make.
func (l listing) steps() int {
	return batches(len(l.containers)) + batches(len(l.images)) +
		batches(len(l.volumes)) + batches(len(l.networks))
}

// batches is how many inspect calls a set of ids takes.
func batches(n int) int {
	return (n + inspectChunk - 1) / inspectChunk
}

// Read gathers every fact the selector needs. A read that cannot complete is
// fatal: a partial read produces a confident, wrong plan.
//
// The order is deliberate. Every enumeration is cheap and says how much work
// the rest of the read is, so the run reports a true fraction rather than a
// clock. The slow disk-usage pass goes last.
func Read(ctx context.Context, r Runner, p *progress.Reporter) (Snapshot, error) {
	var s Snapshot

	p.Stage("asking docker what it holds")
	if err := runJSON(ctx, r, &s.Version, "version", "--format", "json"); err != nil {
		return s, err
	}
	if s.Version.Server == nil {
		return s, errors.New("docker daemon is not reachable")
	}

	found, err := list(ctx, r, p)
	if err != nil {
		return s, err
	}

	// In parallel the read costs the slowest call, never the sum.
	p.Steps(found.steps())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var failed error
	var mu sync.Mutex
	go2 := func(f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				mu.Lock()
				if failed == nil {
					// The read is fatal, so the rest answers nothing.
					failed = err
					cancel()
				}
				mu.Unlock()
			}
		}()
	}

	go2(func() (err error) {
		s.Containers, err = inspect[Container](ctx, r, "container", found.containers, p)
		return err
	})
	go2(func() (err error) {
		s.Images, err = inspect[Image](ctx, r, "image", found.images, p)
		return err
	})
	go2(func() (err error) {
		s.Volumes, err = inspect[Volume](ctx, r, "volume", found.volumes, p)
		return err
	})
	go2(func() (err error) {
		s.Networks, err = inspect[Network](ctx, r, "network", found.networks, p)
		return err
	})
	wg.Wait()
	if failed != nil {
		return s, failed
	}

	s.Caches, s.CacheUnavailable = readCaches(ctx, r, p, found.builders)
	s.DiskUsage = measure(ctx, s, p)
	return s, nil
}

// measure fills in the sizes the report shows. Nothing the selector decides
// depends on a size, so a volume this cannot read is left out rather than
// counted as empty.
func measure(ctx context.Context, s Snapshot, p *progress.Reporter) DiskUsage {
	var du DiskUsage
	for _, i := range s.Images {
		du.Images = append(du.Images, ImageUsage{ID: i.ID, Size: i.Size})
		du.LayersSize += i.Size
	}
	for _, c := range s.Containers {
		du.Containers = append(du.Containers, ContainerUsage{ID: c.ID, SizeRw: c.SizeRw})
	}
	for _, cache := range s.Caches {
		du.BuildCache = append(du.BuildCache, cache.Records...)
	}

	// Counting a volume that is never walked leaves the fraction short.
	p.Steps(Measurable(s.Volumes))
	sizes := MeasureVolumes(ctx, s.Volumes, p)
	for _, v := range s.Volumes {
		n, ok := sizes[v.Name]
		du.Volumes = append(du.Volumes, VolumeUsage{Name: v.Name, Size: n, Measured: ok})
	}
	return du
}

// list enumerates what the machine holds. Every call here returns names only,
// so the daemon measures nothing and answers straight away.
func list(ctx context.Context, r Runner, p *progress.Reporter) (listing, error) {
	var l listing

	// Each listing names itself: a huge image set, or a gone builder, is slow.
	p.Stage("listing containers")
	out, errb, err := r.Run(ctx, "ps", "-aq", "--no-trunc")
	if err != nil {
		return l, fmt.Errorf("docker ps: %w: %s", err, strings.TrimSpace(string(errb)))
	}
	l.containers = lines(out)

	// No -a on purpose: it surfaces intermediate images docker refuses to remove.
	p.Stage("listing images")
	if l.images, err = listField(ctx, r, "ID", "image", "ls", "--no-trunc", "--format", "json"); err != nil {
		return l, err
	}
	p.Stage("listing volumes")
	if l.volumes, err = listField(ctx, r, "Name", "volume", "ls", "--format", "json"); err != nil {
		return l, err
	}
	p.Stage("listing networks")
	if l.networks, err = listField(ctx, r, "ID", "network", "ls", "--no-trunc", "--format", "json"); err != nil {
		return l, err
	}

	// No buildx is not a failure: the disk-usage pass still reports the cache.
	p.Stage("listing builders")
	l.builders, _ = runNDJSON[Builder](ctx, r, "buildx", "ls", "--format", "json")
	return l, nil
}

// listField reads the named column of an `ls` listing, discarding a repeat.
// Docker lists an image per tag, and a repeat would be inspected again.
func listField(ctx context.Context, r Runner, field string, args ...string) ([]string, error) {
	rows, err := runNDJSON[map[string]any](ctx, r, args...)
	if err != nil {
		return nil, err
	}
	seen := set.New[string]()
	var out []string
	for _, row := range rows {
		v, _ := row[field].(string)
		if v == "" || seen.Contains(v) {
			continue
		}
		seen.Add(v)
		out = append(out, v)
	}
	return out, nil
}

// readCaches reads every builder, because `docker buildx du` and `docker buildx
// prune` act on a single builder, and a docker-container builder keeps its
// cache in its own volume. A failure here is reported, not fatal: losing the
// build cache must not block the rest of the run.
func readCaches(ctx context.Context, r Runner, p *progress.Reporter, builders []Builder) ([]Cache, string) {
	// With no builder named, the default builder still answers.
	if len(builders) == 0 {
		did := p.Step("reading the build cache")
		recs, err := runNDJSON[CacheRecord](ctx, r, "buildx", "du", "--format", "json")
		did()
		if err != nil {
			return nil, "cannot read build cache: " + err.Error()
		}
		if len(recs) == 0 {
			return nil, ""
		}
		return []Cache{{Builder: "", Records: recs}}, ""
	}

	var caches []Cache
	var failures []string
	for _, b := range builders {
		if b.Name == "" {
			continue
		}
		did := p.Step("reading the build cache of %s", b.Name)
		recs, err := runNDJSON[CacheRecord](ctx, r, "buildx", "du", "--builder", b.Name, "--format", "json")
		did()
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
