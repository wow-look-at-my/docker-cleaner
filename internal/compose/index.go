// Package compose answers one question: would a compose project still on disk
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
	"sort"
	"time"
)

// DefaultIndexPath is where the project index lives.
const DefaultIndexPath = "/var/lib/docker-cleaner/projects.json"

// indexSchema is bumped when the on-disk shape changes. A file from a future
// version is discarded rather than half-read.
const indexSchema = 1

// Entry is one project's known compose files.
type Entry struct {
	Files []string  `json:"files"`
	Seen  time.Time `json:"seen"`
}

// Index maps project name to the compose files that declare it.
type Index struct {
	Schema   int              `json:"schema"`
	Projects map[string]Entry `json:"projects"`

	path     string
	readOnly bool
	// Warning explains why the index could not be persisted, so a run that
	// silently loses its speed advantage says so instead.
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

// Record adds files for a project, keeping the union. Paths accumulate because
// a project can legitimately be described by several files.
func (i *Index) Record(project string, files []string, when time.Time) {
	if project == "" || len(files) == 0 {
		return
	}
	e := i.Projects[project]
	have := set.New[string]()
	for _, f := range e.Files {
		have.Add(f)
	}
	for _, f := range files {
		if f == "" || have.Contains(f) {
			continue
		}
		have.Add(f)
		e.Files = append(e.Files, f)
	}
	sort.Strings(e.Files)
	e.Seen = when
	i.Projects[project] = e
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

// Forget drops a project whose files have all disappeared, so a deleted stack
// does not keep its entry forever.
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
