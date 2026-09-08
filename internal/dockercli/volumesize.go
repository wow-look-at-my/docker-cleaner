package dockercli

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
)

// sizeWorkers read separate directories at the same time. The walk waits on
// the disk, not the CPU, so a small machine still reads several.
func sizeWorkers() int {
	if n := runtime.NumCPU(); n > 4 {
		return n
	}
	return 4
}

// Measurable is how many of these volumes have a directory to walk.
func Measurable(volumes []Volume) int {
	n := 0
	for _, v := range volumes {
		if v.Mountpoint != "" {
			n++
		}
	}
	return n
}

// MeasureVolumes returns the bytes each volume holds, by walking what its
// driver mounts. `docker system df -v` reports nothing until it has measured
// every volume, so on a full host it outlasts the timeout.
//
// The volumes go in turn, and every worker reads inside the volume in hand: a
// nested docker root occupies a single reader for minutes.
func MeasureVolumes(ctx context.Context, volumes []Volume, p *progress.Reporter) map[string]int64 {
	sizes := map[string]int64{}
	for _, v := range volumes {
		if v.Mountpoint == "" {
			continue
		}
		if ctx.Err() != nil {
			return sizes
		}
		did := p.Step("measuring volume %s", v.Name)
		n, ok := treeSize(ctx, v.Mountpoint)
		did()
		if ok {
			sizes[v.Name] = n
		}
	}
	return sizes
}

// treeSize adds up the bytes under a directory, reading several directories at
// a time. It reports false when the walk could not finish, so a partial number
// never passes for a measurement.
func treeSize(ctx context.Context, root string) (int64, bool) {
	// A missing root is a volume this cannot read. Inside the tree a vanished
	// entry is normal, so only the root decides that.
	if _, err := os.Stat(root); err != nil {
		return 0, false
	}

	w := &treeWalk{stack: []string{root}}
	w.ready = sync.NewCond(&w.mu)

	var wg sync.WaitGroup
	for range sizeWorkers() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(ctx)
		}()
	}
	wg.Wait()

	if w.failed {
		return 0, false
	}
	return w.total, true
}

// treeWalk is the queue of directories that the workers of a measurement
// share, so every worker reads inside the volume being measured.
type treeWalk struct {
	mu    sync.Mutex
	ready *sync.Cond

	stack  []string
	active int // directories being read right now
	total  int64
	failed bool
}

// run takes directories until the queue is empty and nothing can refill it.
func (w *treeWalk) run(ctx context.Context) {
	for {
		w.mu.Lock()
		for len(w.stack) == 0 && w.active > 0 {
			w.ready.Wait()
		}
		if len(w.stack) == 0 {
			w.mu.Unlock()
			// The last worker out wakes the ones still waiting on it.
			w.ready.Broadcast()
			return
		}
		dir := w.stack[len(w.stack)-1]
		w.stack = w.stack[:len(w.stack)-1]
		w.active++
		w.mu.Unlock()

		var (
			subdirs []string
			bytes   int64
			err     error
		)
		if err = ctx.Err(); err == nil {
			subdirs, bytes, err = readDirSize(dir)
		}

		w.mu.Lock()
		w.total += bytes
		if err != nil {
			w.failed = true
			// A failed measurement answers nothing, so stop reading for it.
			w.stack = nil
		} else {
			w.stack = append(w.stack, subdirs...)
		}
		w.active--
		w.mu.Unlock()
		w.ready.Broadcast()
	}
}

// readDirSize reads a directory: the bytes its files occupy, and the
// directories under it. A symlink is left alone, because following it reads a
// tree again or leaves the volume altogether.
func readDirSize(dir string) ([]string, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A directory that went away mid-walk is normal on a live machine.
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	var subdirs []string
	var total int64
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			subdirs = append(subdirs, full)
			continue
		}
		info, err := e.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, err
		}
		total += onDisk(info)
	}
	return subdirs, total, nil
}
