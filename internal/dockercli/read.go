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

// steps is how many calls the counted part of the read still has to make. The
// trailing call is the disk-usage pass.
func (l listing) steps() int {
	return batches(len(l.containers)) + batches(len(l.images)) +
		batches(len(l.volumes)) + batches(len(l.networks)) +
		len(l.builders) + 1
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
	go2(func() error {
		// The daemon measures every volume in a single pass, so this step
		// cannot be split. Naming the count says how big the job is.
		did := p.Step("measuring disk usage: %s, in a single docker pass",
			progress.Count("volume", "volumes", len(found.volumes)))
		defer did()
		return runJSON(ctx, r, &s.DiskUsage, "system", "df", "-v", "--format", "json")
	})
	wg.Wait()
	if failed != nil {
		return s, failed
	}
	s.BeforeText = Summary(s.DiskUsage)

	s.Caches, s.CacheUnavailable = readCaches(ctx, r, p, found.builders, s.DiskUsage)
	return s, nil
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
func readCaches(ctx context.Context, r Runner, p *progress.Reporter, builders []Builder, du DiskUsage) ([]Cache, string) {
	if len(builders) == 0 {
		if len(du.BuildCache) > 0 {
			return []Cache{{Builder: "", Records: du.BuildCache}}, ""
		}
		return nil, ""
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
