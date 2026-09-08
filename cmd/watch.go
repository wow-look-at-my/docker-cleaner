package cmd

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
)

func init() {
	subcommands = append(subcommands, newWatchCmd)
}

func newWatchCmd(opts *options) *cobra.Command {
	watch := &cobra.Command{
		Use:   "watch",
		Short: "Record compose projects as docker creates their containers",
		Long: `Record compose projects as docker creates their containers.

Docker names a project's compose files on the containers it creates and forgets
them when those containers go. Running this keeps the record, so a stack that
came up and went down between two cleanups is still recognised.

It is an optimisation, never a requirement: a project this misses is kept
rather than removed.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return compose.Watch(context.Background(), opts.dockerBin, opts.indexPath, cmd.OutOrStdout())
		},
	}

	f := watch.Flags()
	f.StringVar(&opts.dockerBin, "docker-bin", "docker", "docker executable to run")
	f.StringVar(&opts.indexPath, "index", compose.DefaultIndexPath, "where the compose project index lives")
	return watch
}
