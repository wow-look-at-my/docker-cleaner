package dockercli

import (
	"context"
	"strings"
)

// RemoveContainer removes one container. Never -f, which would kill a running
// container the plan never selected, and never -v, which would take anonymous
// volumes that are not in the plan and break dry-run/apply symmetry.
func RemoveContainer(id string) []string { return []string{"rm", id} }

// RemoveImageRef removes one reference. Removing by repo:tag is the precise
// operation: an id carrying several tags cannot be removed by id at all.
func RemoveImageRef(ref string) []string { return []string{"rmi", ref} }

// RemoveVolume removes one volume, one call each so a single failure does not
// hide the others.
func RemoveVolume(name string) []string { return []string{"volume", "rm", name} }

// RemoveNetwork removes one network.
func RemoveNetwork(id string) []string { return []string{"network", "rm", id} }

// PruneBuildCache builds the prune for one builder. until= here means "not
// used in", which is the only ageing docker measures that says anything about
// whether a thing is still wanted. The duration must be a Go duration: 7d is
// not one, 168h is.
func PruneBuildCache(builder, until string) []string {
	args := []string{"buildx", "prune"}
	if builder != "" {
		args = append(args, "--builder", builder)
	}
	return append(args, "--force", "--filter", "until="+until)
}

// Do runs one mutating command and returns the first line of stderr on
// failure, which is the part worth showing.
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
