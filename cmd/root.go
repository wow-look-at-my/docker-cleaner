// Package cmd is the command line surface.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
	"github.com/wow-look-at-my/docker-cleaner/internal/progress"
	"github.com/wow-look-at-my/docker-cleaner/internal/run"
	"golang.org/x/term"
)

// Version is set at build time.
var Version = "dev"

// options holds the flags of an invocation. Each invocation builds its own, so
// commands that run together never share a value.
type options struct {
	dryRun       bool
	age          string
	cacheAge     string
	yes          bool
	noContainers bool
	noImages     bool
	noVolumes    bool
	noNetworks   bool
	noCache      bool
	keep         []string
	json         bool
	showKept     bool
	dockerBin    string
	timeout      time.Duration
	indexPath    string
	progress     string
}

// progressModes are the accepted --progress values.
var progressModes = []string{"auto", "always", "never"}

// reporter builds the progress reporter for this invocation. It draws on
// stderr, so stdout carries only the report and --json stays parseable.
func reporter(mode string) (*progress.Reporter, error) {
	fd := int(os.Stderr.Fd())
	tty := term.IsTerminal(fd)
	switch mode {
	case "never":
		return nil, nil
	// Only a terminal gets the escape codes that redraw a line.
	case "always", "auto":
	default:
		return nil, fmt.Errorf("--progress: %q is not %s", mode, strings.Join(progressModes, ", "))
	}
	width := 0
	if tty {
		if w, _, err := term.GetSize(fd); err == nil {
			width = w
		}
	}
	return progress.New(os.Stderr, tty, width), nil
}

// Each subcommand file appends its own constructor here in an init.
var subcommands []func(*options) *cobra.Command

// exitError carries a finished run's exit code out to Execute, so the
// command tree never ends the process itself.
type exitError struct {
	code int
	// err is nil when the run has already reported itself.
	err error
}

func (e exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit status %d", e.code)
}

// newRootCmd builds a command tree over its own options.
func newRootCmd() *cobra.Command {
	opts := &options{}
	root := rootCommand(opts)
	bindRootFlags(root, opts)
	for _, sub := range subcommands {
		root.AddCommand(sub(opts))
	}
	root.Version = Version
	root.CompletionOptions.DisableDefaultCmd = true
	return root
}

func rootCommand(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "docker-cleaner",
		Short: "Remove docker resources nothing will use again",
		Long: `Remove docker resources nothing will use again.

Deletes containers offline past a threshold, images no longer reachable except
the newest of each repository, volumes and networks nothing will attach, and
build cache nothing has touched lately.

A compose project still on disk keeps everything it would attach on its next
up, so a stack that is merely down is safe. Deleting its compose file is what
retires a project.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clean(cmd, opts)
		},
	}
}

// clean validates the invocation, then runs it. Every flag it rejects is
// rejected before docker or the disk is touched.
func clean(cmd *cobra.Command, opts *options) error {
	age, err := plan.ParseAge(opts.age)
	if err != nil {
		return fmt.Errorf("--age: %w", err)
	}
	cacheAge, err := plan.ParseAge(opts.cacheAge)
	if err != nil {
		return fmt.Errorf("--build-cache-age: %w", err)
	}
	if opts.json && !opts.dryRun && !opts.yes {
		return fmt.Errorf("--json needs --dry-run or --yes: a prompt would corrupt the output")
	}

	bar, err := reporter(opts.progress)
	if err != nil {
		return err
	}

	runner, err := newRunner(opts.dockerBin, opts.timeout)
	if err != nil {
		bar.Stop()
		return exitError{code: run.ExitEnvironment, err: err}
	}

	// The interrupt reaches the docker child and the disk walk.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := run.Do(ctx, run.Config{
		Options: plan.Options{
			Age:            age,
			BuildCacheAge:  cacheAge,
			Keep:           opts.keep,
			SkipContainers: opts.noContainers,
			SkipImages:     opts.noImages,
			SkipVolumes:    opts.noVolumes,
			SkipNetworks:   opts.noNetworks,
			SkipCache:      opts.noCache,
		},
		DryRun:      opts.dryRun,
		Yes:         opts.yes,
		JSON:        opts.json,
		ShowKept:    opts.showKept,
		IndexPath:   opts.indexPath,
		Runner:      runner,
		Stdout:      cmd.OutOrStdout(),
		Stderr:      cmd.ErrOrStderr(),
		Stdin:       os.Stdin,
		Progress:    bar,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Now:         time.Now(),
	})
	bar.Stop()
	// The run has already said everything it has to say.
	return exitError{code: code}
}

func bindRootFlags(root *cobra.Command, opts *options) {
	f := root.Flags()
	f.BoolVar(&opts.dryRun, "dry-run", false, "show the plan and remove nothing")
	f.StringVar(&opts.age, "age", "30d", "remove containers offline longer than this (30d, 4w, 720h, 1h30m)")
	f.StringVar(&opts.cacheAge, "build-cache-age", "7d", "remove build cache unused longer than this")
	f.BoolVarP(&opts.yes, "yes", "y", false, "apply without confirming")
	f.BoolVar(&opts.noContainers, "no-containers", false, "leave containers alone")
	f.BoolVar(&opts.noImages, "no-images", false, "leave images alone")
	f.BoolVar(&opts.noVolumes, "no-volumes", false, "leave volumes alone")
	f.BoolVar(&opts.noNetworks, "no-networks", false, "leave networks alone")
	f.BoolVar(&opts.noCache, "no-build-cache", false, "leave build cache alone")
	f.StringArrayVar(&opts.keep, "keep", nil, "keep images matching this glob (repeatable)")
	f.BoolVar(&opts.json, "json", false, "emit the plan as JSON, including every command's argv")
	f.BoolVar(&opts.showKept, "show-kept", false, "list every kept resource instead of counts")
	f.StringVar(&opts.dockerBin, "docker-bin", "docker", "docker executable to run")
	f.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "timeout for a docker invocation")
	f.StringVar(&opts.progress, "progress", "auto",
		"show what the run is doing on stderr: "+strings.Join(progressModes, ", "))
	f.StringVar(&opts.indexPath, "index", compose.DefaultIndexPath, "where the compose project index lives")
}

// Execute runs the command line and returns the process exit code.
func Execute() int {
	err := newRootCmd().Execute()

	var exit exitError
	if errors.As(err, &exit) {
		if exit.err != nil {
			fmt.Fprintln(os.Stderr, "docker-cleaner:", exit.err)
		}
		return exit.code
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "docker-cleaner:", err)
		return run.ExitUsage
	}
	return run.ExitOK
}
