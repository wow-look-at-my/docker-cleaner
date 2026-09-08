// Package compose answers a question: would a compose project still on disk
// attach this resource on its next `up`? Docker cannot answer it, because the
// labels naming a project's files live only on containers and `down` deletes
// them. So the index below remembers what docker forgets.
package compose

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultIndexPath is where the project index lives.
const DefaultIndexPath = "/var/lib/docker-cleaner/projects.json"

// indexSchema rises with the on-disk shape; a newer file is discarded.
const indexSchema = 1

// Entry is a project's compose files. Sets keeps each invocation apart, because
// separate stacks share a project name. Files is the union.
type Entry struct {
	Files []string   `json:"files"`
	Sets  [][]string `json:"sets,omitempty"`
	Seen  time.Time  `json:"seen"`
}

// Index maps project name to the compose files that declare it.
type Index struct {
	Schema   int              `json:"schema"`
	Projects map[string]Entry `json:"projects"`

	path     string
	readOnly bool
	// Warning says why the index could not persist.
	Warning string
}

// LoadIndex reads the index. Every failure mode yields a usable empty index
// rather than an error: a missing or damaged cache must never stop a cleanup,
// and it must never be parsed leniently into a half-populated map, which would
// read as "those projects are gone".
func LoadIndex(path string) *Index {
	idx := &Index{Schema: indexSchema, Projects: map[string]Entry{}, path: path}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return idx
	case err != nil:
		idx.Warning = fmt.Sprintf("cannot read %s (%v); continuing without a cached project index", path, err)
		return idx
	}

	var onDisk Index
	if err := json.Unmarshal(data, &onDisk); err != nil {
		idx.Warning = fmt.Sprintf("%s is corrupt (%v); rebuilding it", path, err)
		return idx
	}
	if onDisk.Schema > indexSchema {
		idx.Warning = fmt.Sprintf("%s was written by a newer version (schema %d); rebuilding it", path, onDisk.Schema)
		return idx
	}
	if onDisk.Projects != nil {
		idx.Projects = onDisk.Projects
	}
	return idx
}

// Record adds files for a project, keeping the union.
//
// The caller passes the whole set compose named, in compose's order, and that
// order decides what wins, so it leads. A path only the older record knew
// follows it rather than being dropped. Ordering by anything else puts an
// override ahead of the base file it overrides, and an index written that way
// is repaired by the next run.
func (i *Index) Record(project string, files []string, when time.Time) {
	if project == "" || len(files) == 0 {
		return
	}
	e := i.Projects[project]
	named := set.New[string]()
	var ordered []string
	for _, f := range files {
		if f == "" || named.Contains(f) {
			continue
		}
		named.Add(f)
		ordered = append(ordered, f)
	}
	e.Sets = withSet(e.Sets, ordered)
	for _, f := range e.Files {
		if !named.Contains(f) {
			named.Add(f)
			ordered = append(ordered, f)
		}
	}
	e.Files = ordered
	e.Seen = when
	i.Projects[project] = e
}

// withSet adds a file set, leading, and drops a repeat of it.
func withSet(sets [][]string, add []string) [][]string {
	out := [][]string{add}
	key := strings.Join(add, "\x00")
	for _, s := range sets {
		if strings.Join(s, "\x00") != key {
			out = append(out, s)
		}
	}
	return out
}

// Sets returns the file sets to render for a project, each holding only paths
// that still exist. A set compose named is rendered as compose named it: the
// files of separate stacks that share a project name never render together.
func (i *Index) Sets(project string) [][]string {
	e := i.Projects[project]
	recorded := e.Sets
	if len(recorded) == 0 && len(e.Files) > 0 {
		// An index written before sets existed holds the union alone.
		recorded = [][]string{e.Files}
	}

	var out [][]string
	seen := set.New[string]()
	for _, s := range recorded {
		var live []string
		for _, f := range s {
			if st, err := os.Stat(f); err == nil && !st.IsDir() {
				live = append(live, f)
			}
		}
		key := strings.Join(live, "\x00")
		if len(live) > 0 && !seen.Contains(key) {
			seen.Add(key)
			out = append(out, live)
		}
	}
	return out
}

// Files returns the recorded paths for a project that still exist on disk.
// A recorded path is always checked before it is believed: the index is a
// cache of a fact, never the fact itself.
func (i *Index) Files(project string) []string {
	var live []string
	for _, f := range i.Projects[project].Files {
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			live = append(live, f)
		}
	}
	return live
}

// Recorded reports whether the index holds a path for a project, existing or
// not. A recorded path that is gone retires the project.
func (i *Index) Recorded(project string) bool {
	return len(i.Projects[project].Files) > 0
}

// Forget drops a project whose files have all disappeared.
func (i *Index) Forget(project string) { delete(i.Projects, project) }

// Save writes the index atomically. An unwritable directory is a warning, not
// a failure: the run is still correct, it just has to work harder next time.
func (i *Index) Save() {
	if i.readOnly || i.path == "" {
		return
	}
	dir := filepath.Dir(i.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		i.note(dir, err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".projects-*.json")
	if err != nil {
		i.note(dir, err)
		return
	}
	defer os.Remove(tmp.Name())

	i.Schema = indexSchema
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(i); err != nil {
		tmp.Close()
		i.note(dir, err)
		return
	}
	if err := tmp.Close(); err != nil {
		i.note(dir, err)
		return
	}
	if err := os.Rename(tmp.Name(), i.path); err != nil {
		i.note(dir, err)
	}
}

func (i *Index) note(dir string, err error) {
	i.readOnly = true
	if i.Warning == "" {
		i.Warning = fmt.Sprintf("cannot write the project index under %s (%v); every run will rescan the disk until this is fixed", dir, err)
	}
}
