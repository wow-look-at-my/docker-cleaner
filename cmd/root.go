// Package cmd is the command line surface.
package cmd

import (
	"context"
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

var opts struct {
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
	rescan       bool
	dockerBin    string
	timeout      time.Duration
	scanTimeout  time.Duration
	indexPath    string
	mountInfo    string
	dockerRoot   string
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

var rootCmd = &cobra.Command{
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
			fmt.Fprintln(os.Stderr, "docker-cleaner:", err)
			os.Exit(run.ExitEnvironment)
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
			Rescan:      opts.rescan,
			IndexPath:   opts.indexPath,
			MountInfo:   opts.mountInfo,
			DockerRoot:  opts.dockerRoot,
			ScanTimeout: opts.scanTimeout,
			Runner:      runner,
			Stdout:      cmd.OutOrStdout(),
			Stderr:      cmd.ErrOrStderr(),
			Stdin:       os.Stdin,
			Progress:    bar,
			Interactive: term.IsTerminal(int(os.Stdin.Fd())),
			Now:         time.Now(),
		})
		bar.Stop()
		os.Exit(code)
		return nil
	},
}

func init() {
	f := rootCmd.Flags()
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
	f.BoolVar(&opts.rescan, "rescan", false, "walk the disk for compose files even if the index answers")
	f.StringVar(&opts.dockerBin, "docker-bin", "docker", "docker executable to run")
	f.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "timeout for a docker invocation")
	f.DurationVar(&opts.scanTimeout, "scan-timeout", 5*time.Minute,
		"give up searching the disk for compose files after this, keeping every project the search could not resolve (0 waits forever)")
	f.StringVar(&opts.progress, "progress", "auto",
		"show what the run is doing on stderr: "+strings.Join(progressModes, ", "))
	f.StringVar(&opts.indexPath, "index", compose.DefaultIndexPath, "where the compose project index lives")
	f.StringVar(&opts.mountInfo, "mountinfo", compose.DefaultMountInfo, "mount table naming the filesystems to search")
	f.StringVar(&opts.dockerRoot, "docker-root", "/var/lib/docker", "docker's storage root, excluded from the search")
	rootCmd.Version = Version
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

// Execute runs the command line and returns the process exit code.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "docker-cleaner:", err)
		return run.ExitUsage
	}
	return run.ExitOK
}
