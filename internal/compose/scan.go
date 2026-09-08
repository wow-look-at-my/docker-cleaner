package compose

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"

	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
	"github.com/wow-look-at-my/go-containers/set"
)

// composeNames are the filenames docker compose itself looks for.
var composeNames = set.Of[string]("compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml")

// defaultWorkers walks several mounts at a time. The disk dominates the work.
const defaultWorkers = 16

// defaultMaxDepth ends a path that grows without end. Reaching it is reported.
const defaultMaxDepth = 128

// fileID names a directory for the visited set and for the mount-boundary
// check. identify says what fills it on each platform.
type fileID struct {
	dev, ino uint64
	path     string
}

// ScanResult is what a walk found and what it could not reach.
type ScanResult struct {
	Files []string
	// Failures are places the walk could not read; any means not exhaustive.
	Failures []string
	Skipped  []Mount
	Dirs     int
}

// Complete reports whether the walk reached everything it set out to reach.
func (r ScanResult) Complete() bool { return len(r.Failures) == 0 }

// Scanner finds compose files. The interface exists so tests can prove a run
// resolved everything from the index without touching the disk.
type Scanner interface {
	Scan(ctx context.Context) (ScanResult, error)
}

// FSScanner walks local filesystems for compose files.
type FSScanner struct {
	Mounts   []Mount
	Workers  int
	MaxDepth int
	Progress *progress.Reporter
}

// Scan walks every mount that was not skipped. It searches by filename, so a
// project directory that moved since it was last seen is still found.
//
// The context bounds the whole search. A read of a directory on a wedged
// network mount never returns, so results travel by channel and the collector
// abandons such a walker instead of waiting for it. The caller then gets an
// incomplete result, which keeps every unresolved project.
func (s *FSScanner) Scan(ctx context.Context) (ScanResult, error) {
	var result ScanResult
	var roots []string
	for _, m := range s.Mounts {
		if m.Skip != "" {
			result.Skipped = append(result.Skipped, m)
			continue
		}
		roots = append(roots, m.Point)
	}
	if len(roots) == 0 {
		return result, nil
	}

	var dirs, files atomic.Int64
	s.Progress.Detail(func() string {
		return progress.Join(
			progress.Count("directory", "directories", int(dirs.Load())),
			progress.Count("compose file", "compose files", int(files.Load())))
	})
	defer s.Progress.Detail(nil)

	work := make(chan string)
	// The buffer lets an abandoned walker finish its send instead of leaking.
	out := make(chan ScanResult, len(roots))
	for range min(s.workers(), len(roots)) {
		go func() {
			for root := range work {
				out <- walk(ctx, root, s.maxDepth(), &dirs, &files)
			}
		}()
	}
	go func() {
		defer close(work)
		for _, root := range roots {
			select {
			case work <- root:
			case <-ctx.Done():
				return
			}
		}
	}()

	for range roots {
		select {
		case r := <-out:
			result.Files = append(result.Files, r.Files...)
			result.Failures = append(result.Failures, r.Failures...)
			result.Dirs += r.Dirs
		case <-ctx.Done():
			result.Failures = append(result.Failures,
				"the search for compose files stopped early: "+ctx.Err().Error())
			sortResult(&result)
			return result, nil
		}
	}

	sortResult(&result)
	return result, nil
}

func (s *FSScanner) workers() int {
	if s.Workers <= 0 {
		return defaultWorkers
	}
	return s.Workers
}

func (s *FSScanner) maxDepth() int {
	if s.MaxDepth <= 0 {
		return defaultMaxDepth
	}
	return s.MaxDepth
}

func sortResult(r *ScanResult) {
	sort.Strings(r.Files)
	sort.Strings(r.Failures)
}

// pending is a directory the walk has yet to read, and how far under the mount
// point it sits.
type pending struct {
	path  string
	id    fileID
	depth int
}

// walk descends a mount without crossing into another and without following
// symlinks, which would otherwise revisit whole trees. It recognises a
// directory it has already read by identity rather than by path, so a route
// that leads back into the tree ends there instead of repeating it.
func walk(ctx context.Context, root string, maxDepth int, dirs, files *atomic.Int64) ScanResult {
	var res ScanResult

	rootID, ok := identify(root)
	if !ok {
		return ScanResult{Failures: []string{root + ": cannot stat"}}
	}

	seen := set.New[fileID]()
	stack := []pending{{path: root, id: rootID}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			res.Failures = append(res.Failures, root+": "+err.Error())
			return res
		}

		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen.Contains(dir.id) {
			continue
		}
		seen.Add(dir.id)
		if dir.depth >= maxDepth {
			res.Failures = append(res.Failures,
				fmt.Sprintf("%s: deeper than %d directories, so it was not searched", dir.path, maxDepth))
			continue
		}
		res.Dirs++
		dirs.Add(1)

		entries, err := os.ReadDir(dir.path)
		if err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("%s: %v", dir.path, err))
			continue
		}
		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(dir.path, name)
			switch {
			case e.Type()&fs.ModeSymlink != 0:
				// A symlink is either another route to something already
				// walked or a route off this filesystem. Neither is wanted.
			case e.IsDir():
				if name == ".git" {
					continue
				}
				id, ok := identify(full)
				if !ok || id.dev != rootID.dev {
					continue
				}
				stack = append(stack, pending{path: full, id: id, depth: dir.depth + 1})
			case composeNames.Contains(name):
				res.Files = append(res.Files, full)
				files.Add(1)
			}
		}
	}
	return res
}
