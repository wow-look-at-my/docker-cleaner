package compose

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// composeNames are the filenames docker compose itself looks for.
var composeNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// ProbeResult is what a look around the known project directories found.
type ProbeResult struct {
	Files []string
	// Failures are directories the probe could not read.
	Failures []string
}

// probe looks for a project beside the projects docker already named.
//
// Compose names a project after the directory that holds its file, and a
// machine keeps its stacks together. So the parent of a known project
// directory is where an unnamed project lives. Reading those parents costs a
// call each, against a walk of every file on the machine.
func probe(ctx context.Context, parents, wanted []string) ProbeResult {
	var res ProbeResult
	if len(parents) == 0 || len(wanted) == 0 {
		return res
	}

	want := set.New[string]()
	for _, p := range wanted {
		want.Add(p)
	}
	found := set.New[string]()

	for _, parent := range dedup(parents) {
		if ctx.Err() != nil {
			res.Failures = append(res.Failures, "the compose probe ran out of time: "+ctx.Err().Error())
			break
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			// The projects under an unreadable parent stay unresolved.
			res.Failures = append(res.Failures, parent+": "+err.Error())
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !want.Contains(ProjectName(e.Name())) {
				continue
			}
			for _, name := range composeNames {
				file := filepath.Join(parent, e.Name(), name)
				if st, err := os.Stat(file); err == nil && !st.IsDir() && !found.Contains(file) {
					found.Add(file)
					res.Files = append(res.Files, file)
				}
			}
		}
	}

	sort.Strings(res.Files)
	sort.Strings(res.Failures)
	return res
}

// ProjectName is the name compose gives a project in the named directory. It
// lowercases and drops what a project name may not carry.
func ProjectName(dir string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(dir) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(b.String(), "_-")
}

func dedup(in []string) []string {
	seen := set.New[string]()
	var out []string
	for _, s := range in {
		if s == "" || seen.Contains(s) {
			continue
		}
		seen.Add(s)
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
