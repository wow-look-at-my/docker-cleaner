package dockercli

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
)

// sizeWorkers walk separate volumes at the same time.
const sizeWorkers = 8

// MeasureVolumes returns the bytes each volume holds, by walking the directory
// its driver mounts.
//
// The daemon offers this only through `docker system df -v`, which measures
// every volume in a call that reports nothing until it ends. On a full host
// that call outlasts the timeout and takes the run with it. Walking the same
// directories here reports per volume, stops when the context ends, and runs
// several at a time.
//
// A volume this cannot read is absent from the result. Reporting it as empty
// would be a measurement nobody made.
func MeasureVolumes(ctx context.Context, volumes []Volume, p *progress.Reporter) map[string]int64 {
	sizes := map[string]int64{}
	var mu sync.Mutex

	work := make(chan Volume)
	var wg sync.WaitGroup
	for range sizeWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range work {
				did := p.Step("measuring volume %s", v.Name)
				n, ok := treeSize(ctx, v.Mountpoint)
				did()
				if !ok {
					continue
				}
				mu.Lock()
				sizes[v.Name] = n
				mu.Unlock()
			}
		}()
	}

	for _, v := range volumes {
		if v.Mountpoint == "" {
			continue
		}
		select {
		case work <- v:
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return sizes
		}
	}
	close(work)
	wg.Wait()
	return sizes
}

// treeSize adds up the bytes under a directory. It reports false when the walk
// could not finish, so a partial number never passes for a measurement.
func treeSize(ctx context.Context, root string) (int64, bool) {
	// A missing root is a volume this cannot read. Inside the tree a vanished
	// entry is normal, so only the root decides that.
	if _, err := os.Stat(root); err != nil {
		return 0, false
	}

	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			// A file that went away mid-walk is normal on a live machine.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		total += onDisk(info)
		return nil
	})
	if err != nil {
		return 0, false
	}
	return total, true
}
