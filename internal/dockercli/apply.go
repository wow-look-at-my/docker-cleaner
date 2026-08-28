package dockercli

import (
	"context"
	"strings"
)

// RemoveContainer removes one container. Never -f (kills running) or -v.
func RemoveContainer(id string) []string { return []string{"rm", id} }

// RemoveImageRef removes one reference; an id with several tags needs each.
func RemoveImageRef(ref string) []string { return []string{"rmi", ref} }

// RemoveVolume removes one volume, one call each so failures stay visible.
func RemoveVolume(name string) []string { return []string{"volume", "rm", name} }

// RemoveNetwork removes one network.
func RemoveNetwork(id string) []string { return []string{"network", "rm", id} }

// PruneBuildCache prunes one builder. Here until= means "not used in", the
// only ageing docker offers that tracks whether a thing is still wanted. It
// takes a Go duration: 168h, never 7d.
func PruneBuildCache(builder, until string) []string {
	args := []string{"buildx", "prune"}
	if builder != "" {
		args = append(args, "--builder", builder)
	}
	return append(args, "--force", "--filter", "until="+until)
}

// Do runs one mutating command, reporting docker's first stderr line.
func Do(ctx context.Context, r Runner, args []string) error {
	_, errb, err := r.Run(ctx, args...)
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(errb))
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if msg == "" {
		return err
	}
	return &Failure{Args: args, Msg: msg}
}

// Failure carries what docker said about a rejected command.
type Failure struct {
	Args []string
	Msg  string
}

func (f *Failure) Error() string { return f.Msg }
