// Package progress says what a long run is doing while it does it. A cleanup
// reads docker, walks the disk and renders compose files before it can print
// anything, and on a large machine that takes minutes. Without this the tool
// looks hung.
package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ttyInterval redraws often enough to look alive without costing anything.
const ttyInterval = 100 * time.Millisecond

// logInterval paces a stream that scrolls: a log wants news, not a frame rate.
const logInterval = 2 * time.Second

// stagePatience is how long a stage runs before its line carries a clock.
const stagePatience = 2 * time.Second

// logHeartbeat repeats an unchanged stage on a stream that scrolls.
const logHeartbeat = 5 * time.Second

// Reporter shows the current stage. A nil Reporter is a working no-op, so a
// caller that wants silence passes nil and changes nothing else.
type Reporter struct {
	w     io.Writer
	tty   bool
	width int

	// now reads the clock. A test replaces it.
	now func() time.Time

	mu      sync.Mutex
	stage   string
	detail  func() string
	drawn   string
	started time.Time
	logged  time.Time
	dirty   bool

	total    int // steps this phase takes
	finished int // steps behind it

	running map[int]step // work in flight, which is what the line names
	nextID  int

	stop chan struct{}
	done chan struct{}
}

// step is work that has started and has not come back.
type step struct {
	label string
	at    time.Time
}

// New starts a reporter. On a terminal it redraws in place, and elsewhere it
// prints a line as the text changes.
func New(w io.Writer, tty bool, width int) *Reporter {
	return newClocked(w, tty, width, time.Now)
}

// newClocked is New over a given clock, which the loop reads as it starts.
func newClocked(w io.Writer, tty bool, width int, now func() time.Time) *Reporter {
	if width <= 0 {
		width = 100
	}
	r := &Reporter{
		w:       w,
		tty:     tty,
		width:   width,
		now:     now,
		running: map[int]step{},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go r.loop()
	return r
}

func (r *Reporter) loop() {
	defer close(r.done)
	interval := logInterval
	if r.tty {
		interval = ttyInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-tick.C:
			r.render()
		}
	}
}

// Stage names what the run is doing now. It draws immediately, because the
// reader waits for exactly this.
func (r *Reporter) Stage(format string, args ...any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stage = fmt.Sprintf(format, args...)
	r.detail = nil
	r.started = r.now()
	r.total, r.finished = 0, 0
	r.restart()
	r.mu.Unlock()
	r.render()
}

// Steps opens a counted phase. Each step is a call the run has to make, so the
// fraction on screen is the work itself.
func (r *Reporter) Steps(total int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.total, r.finished = total, 0
	r.restart()
	r.mu.Unlock()
}

// Step names work that starts now and returns the call that marks it finished.
// The fraction counts only what has come back.
func (r *Reporter) Step(format string, args ...any) func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	r.running[id] = step{label: fmt.Sprintf(format, args...), at: r.now()}
	r.mu.Unlock()
	r.render()

	return func() {
		r.mu.Lock()
		delete(r.running, id)
		r.finished++
		r.mu.Unlock()
		r.render()
	}
}

// Detail attaches a polled suffix to the current stage, so a caller that counts
// directories updates a number rather than a string.
func (r *Reporter) Detail(f func() string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.detail = f
	r.mu.Unlock()
}

// Stop clears the line and ends the reporter. A repeat call does nothing, so a
// deferred Stop is safe beside an explicit call. A later Stage starts it again,
// because a run still has slow work to narrate after it prints its report.
func (r *Reporter) Stop() {
	if r == nil {
		return
	}
	select {
	case <-r.done:
	default:
		close(r.stop)
		<-r.done
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dirty {
		fmt.Fprint(r.w, "\r\033[K")
		r.dirty = false
	}
	r.stage, r.drawn = "", ""
	r.running = map[int]step{}
	r.total, r.finished = 0, 0
}

// Log writes a line that shares the screen with the reporter. A drawn line
// carries no newline, so a write straight to the log lands on the end of it.
// This clears the drawn line, and the next tick draws it below the new text.
func (r *Reporter) Log(w io.Writer, format string, args ...any) {
	if r == nil {
		fmt.Fprintf(w, format, args...)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dirty {
		fmt.Fprint(r.w, "\r\033[K")
		r.dirty, r.drawn = false, ""
	}
	fmt.Fprintf(w, format, args...)
}

// restart brings the draw loop back after a Stop. The caller holds the lock.
func (r *Reporter) restart() {
	select {
	case <-r.done:
	default:
		return // still running
	}
	r.stop, r.done = make(chan struct{}), make(chan struct{})
	go r.loop()
}

func (r *Reporter) render() {
	r.mu.Lock()
	defer r.mu.Unlock()

	base, since := r.stage, r.now().Sub(r.started)
	if oldest, ok := r.longest(); ok {
		// The step waiting longest is what holds the run up.
		base, since = oldest.label, r.now().Sub(oldest.at)
	} else if r.total > 0 {
		// Between steps. The stage that opened the phase is long finished.
		return
	}
	if base == "" {
		return
	}
	if r.total > 0 {
		base = fmt.Sprintf("%3d%% (%d of %d) %s", r.finished*100/r.total, r.finished, r.total, base)
	}
	if r.detail != nil {
		if d := r.detail(); d != "" {
			base += " " + d
		}
	}

	// The clock tells a held fraction from a wedged run.
	now := r.now()
	text := base
	if since >= stagePatience {
		text += " " + clock(since)
	}

	if !r.tty {
		if base == r.drawn && now.Sub(r.logged) < logHeartbeat {
			return
		}
		r.drawn, r.logged = base, now
		fmt.Fprintln(r.w, "docker-cleaner: "+text)
		return
	}
	if text == r.drawn {
		return
	}
	r.drawn = text
	fmt.Fprint(r.w, "\r\033[K"+truncate(text, r.width-1))
	r.dirty = true
}

// longest is the step that has waited longest, and whether any step is running.
func (r *Reporter) longest() (step, bool) {
	var oldest step
	found := false
	for _, s := range r.running {
		if !found || s.at.Before(oldest.at) {
			oldest, found = s, true
		}
	}
	return oldest, found
}

// clock renders how long the current step has run.
func clock(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("[%ds]", int(d.Seconds()))
	}
	return fmt.Sprintf("[%dm%02ds]", int(d/time.Minute), int(d%time.Minute/time.Second))
}

// truncate keeps the line inside the terminal, since a wrap leaves a stale row.
func truncate(s string, max int) string {
	if max < 8 {
		max = 8
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}

// Count renders a labelled tally, so every counter reads the same way.
func Count(singular, plural string, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// Join renders several tallies as the detail of a stage.
func Join(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return "(" + strings.Join(kept, ", ") + ")"
}
