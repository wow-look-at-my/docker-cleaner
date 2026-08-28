// Package cmd is the command line surface.
package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
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
	indexPath    string
	mountInfo    string
	dockerRoot   string
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

		runner, err := newRunner(opts.dockerBin, opts.timeout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "docker-cleaner:", err)
			os.Exit(run.ExitEnvironment)
		}

		code := run.Do(context.Background(), run.Config{
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
			Runner:      runner,
			Stdout:      cmd.OutOrStdout(),
			Stderr:      cmd.ErrOrStderr(),
			Stdin:       os.Stdin,
			Interactive: term.IsTerminal(int(os.Stdin.Fd())),
			Now:         time.Now(),
		})
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
	f.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "timeout for one docker invocation")
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
