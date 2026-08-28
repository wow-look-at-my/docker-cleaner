package cmd

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// newRunner is the seam tests replace. It also refuses a docker binary that
// does not resolve, so a typo fails before anything is planned.
var newRunner = func(bin string, timeout time.Duration) (dockercli.Runner, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("cannot run %q: %w", bin, err)
	}
	return dockercli.NewExec(path, timeout), nil
}
