package compose

import (
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// composeNames are the filenames docker compose itself looks for.
var composeNames = set.Of[string]("compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml")

// ScanResult is what a walk found and what it could not reach.
type ScanResult struct {
	Files []string
	// Failures are places the walk could not read. While any exist the search
	// was not exhaustive, so "no compose file found" proves nothing.
	Failures []string
	Skipped  []Mount
	Dirs     int
}

// Complete reports whether the walk reached everything it set out to reach.
func (r ScanResult) Complete() bool { return len(r.Failures) == 0 }

// Scanner finds compose files. The interface exists so tests can prove a run
// resolved everything from the index without touching the disk.
type Scanner interface {
	Scan() (ScanResult, error)
}

// FSScanner walks local filesystems for compose files.
type FSScanner struct {
	Mounts  []Mount
	Workers int
}

// Scan walks every mount that was not skipped. It searches by filename, so a
// project directory that moved since it was last seen is still found.
func (s *FSScanner) Scan() (ScanResult, error) {
	workers := s.Workers
	if workers <= 0 {
		workers = 16
	}

	var (
		mu     sync.Mutex
		result ScanResult
		wg     sync.WaitGroup
	)
	roots := make(chan string)

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for root := range roots {
				files, failures, dirs := walk(root)
				mu.Lock()
				result.Files = append(result.Files, files...)
				result.Failures = append(result.Failures, failures...)
				result.Dirs += dirs
				mu.Unlock()
			}
		}()
	}

	for _, m := range s.Mounts {
		if m.Skip != "" {
			result.Skipped = append(result.Skipped, m)
			continue
		}
		roots <- m.Point
	}
	close(roots)
	wg.Wait()

	sort.Strings(result.Files)
	sort.Strings(result.Failures)
	return result, nil
}

// walk descends one mount without crossing into another and without following
// symlinks, which would otherwise revisit whole trees.
func walk(root string) (files, failures []string, dirs int) {
	rootDev, ok := deviceOf(root)
	if !ok {
		return nil, []string{root + ": cannot stat"}, 0
	}

	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		dirs++

		entries, err := os.ReadDir(dir)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(dir, name)
			switch {
			case e.Type()&fs.ModeSymlink != 0:
				// A symlink is either a second route to something already
				// walked or a route off this filesystem. Neither is wanted.
			case e.IsDir():
				if name == ".git" {
					continue
				}
				if dev, ok := deviceOf(full); !ok || dev != rootDev {
					continue
				}
				stack = append(stack, full)
			case composeNames.Contains(name):
				files = append(files, full)
			}
		}
	}
	return files, failures, dirs
}
