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

// Reporter shows the current stage. A nil Reporter is a working no-op, so a
// caller that wants silence passes nil and changes nothing else.
type Reporter struct {
	w     io.Writer
	tty   bool
	width int

	mu     sync.Mutex
	stage  string
	detail func() string
	drawn  string
	dirty  bool

	stop chan struct{}
	done chan struct{}
}

// New starts a reporter. On a terminal it redraws in place. Elsewhere it
// prints a line whenever the text changes, which keeps a log readable and a
// redirected stream free of escape codes.
func New(w io.Writer, tty bool, width int) *Reporter {
	if width <= 0 {
		width = 100
	}
	r := &Reporter{w: w, tty: tty, width: width, stop: make(chan struct{}), done: make(chan struct{})}
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
	r.mu.Unlock()
	r.render()
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
// deferred Stop is safe beside an explicit call.
func (r *Reporter) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.stage == "" && !r.dirty {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()

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
	r.stage = ""
	r.drawn = ""
}

func (r *Reporter) render() {
	r.mu.Lock()
	defer r.mu.Unlock()

	text := r.stage
	if text == "" {
		return
	}
	if r.detail != nil {
		if d := r.detail(); d != "" {
			text += " " + d
		}
	}
	if text == r.drawn {
		return
	}
	r.drawn = text

	if !r.tty {
		fmt.Fprintln(r.w, "docker-cleaner: "+text)
		return
	}
	fmt.Fprint(r.w, "\r\033[K"+truncate(text, r.width-1))
	r.dirty = true
}

// truncate keeps the line inside the terminal, so a long path cannot wrap and
// leave a stale row behind.
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
