package progress

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// safeBuffer is written by the reporter's own goroutine and read by the test.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A stage draws as soon as it is named. Waiting for the next tick is what makes
// a tool look wedged, which is the whole failure this package exists for.
func TestAStageAppearsAtOnce(t *testing.T) {
	var out safeBuffer
	r := New(&out, false, 0)
	defer r.Stop()

	r.Stage("reading containers")

	assert.Contains(t, out.String(), "reading containers")
}

// Off a terminal the output is plain lines, because a log or a pipe keeps every
// byte it is given.
func TestOffATerminalNothingRedraws(t *testing.T) {
	var out safeBuffer
	r := New(&out, false, 0)
	defer r.Stop()

	r.Stage("reading images")
	r.Stop()

	assert.NotContains(t, out.String(), "\033")
	assert.True(t, strings.HasSuffix(out.String(), "\n"))
}

// On a terminal the reporter owns a single line and leaves it clean, so the
// report that follows starts at the left margin.
func TestATerminalLineIsRedrawnAndCleared(t *testing.T) {
	var out safeBuffer
	r := New(&out, true, 40)

	r.Stage("reading volumes")
	r.Stop()

	body := out.String()
	assert.Contains(t, body, "\r\033[K")
	assert.Contains(t, body, "reading volumes")
	assert.True(t, strings.HasSuffix(body, "\r\033[K"), "the line is left clear for the report")
}

// A long path must not wrap: a wrapped line leaves a stale row behind that no
// redraw can reach.
func TestALongLineIsTruncatedToTheTerminal(t *testing.T) {
	var out safeBuffer
	r := New(&out, true, 30)
	defer r.Stop()

	r.Stage("searching %s", strings.Repeat("deep/", 40))

	for _, line := range strings.Split(out.String(), "\r\033[K") {
		assert.LessOrEqual(t, len(line), 30)
	}
}

// A detail is polled, so a caller that counts directories updates a number
// rather than formatting a string per directory.
func TestTheDetailIsPolledAndFollowsTheStage(t *testing.T) {
	var out safeBuffer
	r := New(&out, false, 0)
	defer r.Stop()

	var count int
	r.Stage("searching the disk")
	r.Detail(func() string {
		count++
		return Join(Count("directory", "directories", count))
	})
	require.Eventually(t, func() bool {
		return strings.Contains(out.String(), "(1 directory)")
	}, 10*time.Second, 20*time.Millisecond)

	r.Stage("reading compose files")
	before := out.String()
	time.Sleep(50 * time.Millisecond)

	assert.NotContains(t, strings.TrimPrefix(out.String(), before), "directories",
		"a new stage drops the previous stage's counter")
}

// A nil reporter is what "--progress never" produces, so every method must
// accept it.
func TestANilReporterIsSilentAndSafe(t *testing.T) {
	var r *Reporter

	r.Stage("reading containers")
	r.Detail(func() string { return "detail" })
	r.Stop()
}

// Stop is deferred next to explicit calls, so it must be safe more than the
// time it is meant to run.
func TestStopIsSafeToRepeat(t *testing.T) {
	var out safeBuffer
	r := New(&out, true, 40)

	r.Stage("reading networks")
	r.Stop()
	r.Stop()
}

func TestCountAgreesWithItsSubject(t *testing.T) {
	assert.Equal(t, "1 directory", Count("directory", "directories", 1))
	assert.Equal(t, "2 directories", Count("directory", "directories", 2))
	assert.Equal(t, "0 directories", Count("directory", "directories", 0))
	assert.Equal(t, "(3 files, 1 mount)", Join(Count("file", "files", 3), "", Count("mount", "mounts", 1)))
	assert.Empty(t, Join("", ""))
}
