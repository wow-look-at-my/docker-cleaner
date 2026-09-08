// Package run wires the phases together: read docker, resolve compose, decide,
// show, confirm, apply, report.
package run

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
	"github.com/wow-look-at-my/docker-cleaner/internal/report"
)

// Config describes an invocation.
type Config struct {
	Options   plan.Options
	DryRun    bool
	Yes       bool
	JSON      bool
	ShowKept  bool
	IndexPath string

	Runner dockercli.Runner
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// Progress says what the run is doing. A nil reporter says nothing.
	Progress *progress.Reporter
	// Interactive false means nothing can answer a prompt, so the run refuses.
	Interactive bool
	Now         time.Time
}

// Do performs a run and returns the process exit code.
func Do(ctx context.Context, c Config) int {
	snap, err := dockercli.Read(ctx, c.Runner, c.Progress)
	if err != nil {
		c.Progress.Stop()
		fmt.Fprintln(c.Stderr, "docker-cleaner:", err)
		return ExitEnvironment
	}

	disco := discover(ctx, c, snap)
	c.Progress.Stage("deciding what to remove")
	p := plan.Compute(snap, disco, c.Options, c.Now)
	defer saveIndex(disco)
	// The report owns the screen from here on.
	c.Progress.Stop()

	if c.JSON {
		return emitJSON(ctx, c, p)
	}

	before := report.Summary(snap.DiskUsage, snap.CacheUnavailable)
	if err := report.Text(c.Stdout, p, before, c.DryRun, c.ShowKept); err != nil {
		fmt.Fprintln(c.Stderr, "docker-cleaner:", err)
		return ExitEnvironment
	}
	if p.Empty() || c.DryRun {
		return ExitOK
	}
	if !c.Yes {
		if !c.Interactive {
			fmt.Fprintln(c.Stderr, "docker-cleaner: refusing to prompt with no terminal; pass --yes to apply")
			return ExitUsage
		}
		if !confirm(c) {
			fmt.Fprintln(c.Stdout, "Aborted: nothing was removed.")
			return ExitOK
		}
	}

	_, freed, failed := apply(ctx, c, p, c.Stdout)

	// Measured before the removals, because measuring again walks every volume.
	fmt.Fprintf(c.Stdout, "\nFREED  %s\n", report.Bytes(freed))
	if failed > 0 {
		return ExitApplyFailed
	}
	return ExitOK
}

// discover resolves compose projects from what docker already holds: the
// labels on every container, and the index that remembers them after `down`
// removes the containers that carried them.
func discover(ctx context.Context, c Config, snap dockercli.Snapshot) *compose.Discovery {
	c.Progress.Stage("looking for compose projects")
	return compose.Discover(ctx, c.Runner, snap.Containers, plan.ComposeProjects(snap), compose.Options{
		Index:    compose.LoadIndex(c.IndexPath),
		Now:      c.Now,
		Progress: c.Progress,
	})
}

// apply runs the plan, and reports the bytes behind everything that went. A
// failure is reported and the run continues, because a world that changed under
// us is normal, not exceptional.
func apply(ctx context.Context, c Config, p plan.Plan, log io.Writer) ([]report.Operation, int64, int) {
	fmt.Fprintln(log, "APPLY")
	c.Progress.Steps(commandCount(p))
	defer c.Progress.Stop()
	notRemoved := map[string]bool{}
	var ops []report.Operation
	var freed int64
	failures := 0

	for _, t := range p.Containers {
		done, ok := runTarget(ctx, c, t, log)
		ops = append(ops, done...)
		if !ok {
			notRemoved[t.Name] = true
			failures++
			continue
		}
		freed += t.Size
	}

	// A container that would not go still holds its image, volume and network.
	rest := append(append(append([]plan.Target{}, p.Images...), p.Volumes...), p.Networks...)
	for _, t := range rest {
		if blocked, by := blockedBy(t, notRemoved); blocked {
			c.Progress.Log(log, "  skipped  %s: %s was not removed\n", t.Name, by)
			continue
		}
		done, ok := runTarget(ctx, c, t, log)
		ops = append(ops, done...)
		if !ok {
			failures++
			continue
		}
		freed += t.Size
	}

	for _, cp := range p.Caches {
		op := doOne(ctx, c, cp.Command, log)
		ops = append(ops, op)
		if !op.OK {
			failures++
			continue
		}
		freed += cp.Size
	}
	return ops, freed, failures
}

// commandCount is how many docker calls the apply will make.
func commandCount(p plan.Plan) int {
	n := len(p.Caches)
	for _, group := range [][]plan.Target{p.Containers, p.Images, p.Volumes, p.Networks} {
		for _, t := range group {
			n += len(t.Commands)
		}
	}
	return n
}

func runTarget(ctx context.Context, c Config, t plan.Target, log io.Writer) ([]report.Operation, bool) {
	ok := true
	var ops []report.Operation
	for _, args := range t.Commands {
		op := doOne(ctx, c, args, log)
		ops = append(ops, op)
		if !op.OK {
			ok = false
		}
	}
	return ops, ok
}

// doOne names the removal before it runs it. A large image or a build cache
// takes its time, and the log speaks only after the call comes back.
func doOne(ctx context.Context, c Config, args []string, log io.Writer) report.Operation {
	did := c.Progress.Step("docker %s", strings.Join(args, " "))
	err := dockercli.Do(ctx, c.Runner, args)
	did()

	if err != nil {
		c.Progress.Log(log, "  FAILED   docker %s: %v\n", strings.Join(args, " "), err)
		return report.Operation{Argv: args, OK: false, Err: err.Error()}
	}
	c.Progress.Log(log, "  ok       docker %s\n", strings.Join(args, " "))
	return report.Operation{Argv: args, OK: true}
}

// blockedBy reports whether every container that would have freed this target
// failed to go.
func blockedBy(t plan.Target, notRemoved map[string]bool) (bool, string) {
	if len(t.FreedBy) == 0 {
		return false, ""
	}
	for _, name := range t.FreedBy {
		if !notRemoved[name] {
			return false, ""
		}
	}
	return true, strings.Join(t.FreedBy, ", ")
}

// emitJSON prints a document. With --yes it applies before printing and records
// what actually ran, so the operations array is history rather than intent. The
// apply log goes to stderr, which keeps stdout parseable.
func emitJSON(ctx context.Context, c Config, p plan.Plan) int {
	doc := report.Build(p, c.DryRun)
	code := ExitOK
	if !c.DryRun && !p.Empty() {
		ops, _, failures := apply(ctx, c, p, c.Stderr)
		doc.Operations = ops
		if failures > 0 {
			code = ExitApplyFailed
		}
	}
	doc.ExitCode = code
	if err := report.Write(c.Stdout, doc); err != nil {
		fmt.Fprintln(c.Stderr, "docker-cleaner:", err)
		return ExitEnvironment
	}
	return code
}

func confirm(c Config) bool {
	fmt.Fprint(c.Stdout, "\nRemove these? [y/N] ")
	line, err := bufio.NewReader(c.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func saveIndex(d *compose.Discovery) {
	if d != nil && d.Index != nil {
		d.Index.Save()
	}
}
