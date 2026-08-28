package dockercli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fixture reads one captured docker response.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	usedFixtures.Add(name)
	return b
}

// usedFixtures records what the suite reads, so a fixture nothing names shows
// up as a failure rather than as coverage.
var usedFixtures = &nameSet{m: map[string]bool{}}

type nameSet struct {
	mu sync.Mutex
	m  map[string]bool
}

func (s *nameSet) Add(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = true
}

func (s *nameSet) Has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[name]
}

// fake answers docker reads from fixtures and records every argv it was given.
type fake struct {
	t     *testing.T
	mu    sync.Mutex
	calls [][]string
	// override replaces the fixture answer for calls whose argv starts with
	// the key, which is how a test injects a failure.
	override map[string]func() ([]byte, []byte, error)
}

func newFake(t *testing.T) *fake {
	return &fake{t: t, override: map[string]func() ([]byte, []byte, error){}}
}

func (f *fake) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()

	joined := strings.Join(args, " ")
	for prefix, fn := range f.override {
		if strings.HasPrefix(joined, prefix) {
			return fn()
		}
	}
	return f.answer(args, joined)
}

func (f *fake) answer(args []string, joined string) ([]byte, []byte, error) {
	switch {
	case joined == "version --format json":
		return fixture(f.t, "version.json"), nil, nil
	case joined == "system df":
		return fixture(f.t, "system_df.txt"), nil, nil
	case joined == "system df -v --format json":
		return fixture(f.t, "system_df_v.json"), nil, nil
	case joined == "ps -aq --no-trunc":
		return []byte("c0ffee1\nc0ffee2\n"), nil, nil
	case joined == "image ls --no-trunc --format json":
		return fixture(f.t, "image_ls.ndjson"), nil, nil
	case joined == "volume ls --format json":
		return fixture(f.t, "volume_ls.ndjson"), nil, nil
	case joined == "network ls --no-trunc --format json":
		return fixture(f.t, "network_ls.ndjson"), nil, nil
	case joined == "buildx ls --format json":
		return fixture(f.t, "buildx_ls.ndjson"), nil, nil
	case joined == "buildx du --builder default --format json":
		return fixture(f.t, "buildx_du_default.ndjson"), nil, nil
	case joined == "buildx du --builder ci-builder --format json":
		return fixture(f.t, "buildx_du_ci.ndjson"), nil, nil
	}
	if len(args) >= 2 && args[1] == "inspect" {
		return fixture(f.t, args[0]+"_inspect.json"), nil, nil
	}
	f.t.Fatalf("fake docker got an unexpected call: docker %s", joined)
	return nil, nil, nil
}

// argvs returns every recorded call joined for easy matching.
func (f *fake) argvs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func (f *fake) callsWithPrefix(prefix string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			out = append(out, c)
		}
	}
	return out
}
