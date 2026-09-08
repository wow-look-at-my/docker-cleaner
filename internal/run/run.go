// Package run wires the phases together: read docker, resolve compose, decide,
// show, confirm, apply, report.
package run

import (
	"bufio"
	"context"
	"encoding/json"
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
	Options    plan.Options
	DryRun     bool
	Yes        bool
	JSON       bool
	ShowKept   bool
	Rescan     bool
	IndexPath  string
	MountInfo  string
	DockerRoot string
	// ScanTimeout bounds the compose search. Past it the search is incomplete.
	ScanTimeout time.Duration

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

	if err := report.Text(c.Stdout, p, snap.BeforeText, c.DryRun, c.ShowKept); err != nil {
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

	_, failed := apply(ctx, c, p, c.Stdout)

	// Measuring again walks every volume, so the run says so.
	fmt.Fprintln(c.Stderr, "docker-cleaner: measuring disk usage again")
	var after dockercli.DiskUsage
	if out, _, err := c.Runner.Run(ctx, "system", "df", "-v", "--format", "json"); err == nil {
		if json.Unmarshal(out, &after) == nil {
			fmt.Fprintln(c.Stdout, "\nAFTER")
			fmt.Fprintln(c.Stdout, strings.TrimRight(dockercli.Summary(after), "\n"))
		}
	}
	if failed > 0 {
		return ExitApplyFailed
	}
	return ExitOK
}

// discover resolves compose projects. The scanner is handed the mount table
// but only runs if some project the index cannot explain remains.
//
// The search gets its own deadline. A walk of a whole machine, or a read of a
// directory on a mount whose server is gone, can outlast anybody's patience,
// and the answer it owes the plan already has a safe shape: an incomplete
// search keeps every project it could not resolve.
func discover(ctx context.Context, c Config, snap dockercli.Snapshot) *compose.Discovery {
	c.Progress.Stage("looking for compose projects")
	idx := compose.LoadIndex(c.IndexPath)

	if c.ScanTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.ScanTimeout)
		defer cancel()
	}

	var scanner compose.Scanner
	mounts, err := compose.ReadMounts(c.MountInfo, c.DockerRoot)
	if err != nil {
		// Without a mount table the search cannot be exhaustive, so say so
		// rather than letting a missing file read as "no projects exist".
		return &compose.Discovery{
			Claims:   compose.Resolve(ctx, c.Runner, nil, c.Progress),
			Complete: false,
			Failures: []string{"cannot read " + c.MountInfo + ": " + err.Error()},
		}
	}
	scanner = &compose.FSScanner{Mounts: mounts, Progress: c.Progress}

	return compose.Discover(ctx, c.Runner, snap.Containers, plan.ComposeProjects(snap), compose.Options{
		Index:    idx,
		Scanner:  scanner,
		Rescan:   c.Rescan,
		Now:      c.Now,
		Progress: c.Progress,
	})
}

// apply runs the plan. A failure is reported and the run continues, because a
// world that changed under us is normal, not exceptional.
func apply(ctx context.Context, c Config, p plan.Plan, log io.Writer) ([]report.Operation, int) {
	fmt.Fprintln(log, "APPLY")
	notRemoved := map[string]bool{}
	var ops []report.Operation
	failures := 0

	for _, t := range p.Containers {
		done, ok := runTarget(ctx, c, t, log)
		ops = append(ops, done...)
		if !ok {
			notRemoved[t.Name] = true
			failures++
		}
	}

	// A container that would not go still holds its image, volume and network.
	rest := append(append(append([]plan.Target{}, p.Images...), p.Volumes...), p.Networks...)
	for _, t := range rest {
		if blocked, by := blockedBy(t, notRemoved); blocked {
			fmt.Fprintf(log, "  skipped  %s: %s was not removed\n", t.Name, by)
			continue
		}
		done, ok := runTarget(ctx, c, t, log)
		ops = append(ops, done...)
		if !ok {
			failures++
		}
	}

	for _, cp := range p.Caches {
		op := doOne(ctx, c, cp.Command, log)
		ops = append(ops, op)
		if !op.OK {
			failures++
		}
	}
	return ops, failures
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

func doOne(ctx context.Context, c Config, args []string, log io.Writer) report.Operation {
	if err := dockercli.Do(ctx, c.Runner, args); err != nil {
		fmt.Fprintf(log, "  FAILED   docker %s: %v\n", strings.Join(args, " "), err)
		return report.Operation{Argv: args, OK: false, Err: err.Error()}
	}
	fmt.Fprintf(log, "  ok       docker %s\n", strings.Join(args, " "))
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
		ops, failures := apply(ctx, c, p, c.Stderr)
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
