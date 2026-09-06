package compose

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowRunner answers a compose render after a wait, and records how many
// renders were in flight together.
type slowRunner struct {
	delay time.Duration

	mu      sync.Mutex
	running int
	peak    int
	calls   atomic.Int64
}

func (s *slowRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	s.calls.Add(1)
	s.mu.Lock()
	s.running++
	if s.running > s.peak {
		s.peak = s.running
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running--
		s.mu.Unlock()
	}()

	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, []byte("cancelled"), ctx.Err()
	}
	return []byte(project(args[2])), nil, nil
}

func manyFiles(n int) []string {
	var files []string
	for i := range n {
		files = append(files, "/srv/p"+strconv.Itoa(i)+"/compose.yaml")
	}
	return files
}

// Each file costs a docker invocation. In order, a machine full of projects
// spends the whole run here, in silence.
func TestFilesRenderTogether(t *testing.T) {
	r := &slowRunner{delay: 20 * time.Millisecond}

	c := Resolve(context.Background(), r, manyFiles(16), nil)

	assert.Len(t, c.Projects, 16)
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Greater(t, r.peak, 1, "renders ran in order, so nothing was gained")
	assert.LessOrEqual(t, r.peak, renderWorkers, "a machine full of projects must not fork without bound")
}

// The claims decide what gets deleted, so they must not depend on the order the
// renders finished in.
func TestRenderOrderDoesNotChangeTheClaims(t *testing.T) {
	files := manyFiles(12)

	want := Resolve(context.Background(), &slowRunner{}, files, nil)
	for range 5 {
		got := Resolve(context.Background(), &slowRunner{delay: time.Millisecond}, files, nil)
		assert.Equal(t, want.Projects, got.Projects)
	}
}

// A file nothing reached is unreadable, never "this project declares nothing".
// The other reading retires a live project.
func TestAFileTheRunNeverReachedIsUnreadable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := Resolve(ctx, &slowRunner{delay: time.Hour}, manyFiles(50), nil)

	require.NotEmpty(t, c.Unreadable)
	assert.Empty(t, c.Projects)
	for _, why := range c.Unreadable {
		assert.NotEmpty(t, why)
	}
}
