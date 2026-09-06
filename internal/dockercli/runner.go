package dockercli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// inspectChunk keeps a host with thousands of objects under ARG_MAX.
const inspectChunk = 100

// Runner executes a docker invocation. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout []byte, stderr []byte, err error)
}

// Exec runs the real docker binary directly, never through a shell, so a path
// with spaces works and no argument can be read as shell syntax.
type Exec struct {
	Bin     string
	Timeout time.Duration
}

// NewExec builds a Runner for the given binary.
func NewExec(bin string, timeout time.Duration) *Exec {
	return &Exec{Bin: bin, Timeout: timeout}
}

// Run implements Runner.
func (e *Exec) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// Argv renders an invocation for display. The plan shows exactly what runs.
func Argv(bin string, args []string) string {
	return strings.Join(append([]string{bin}, args...), " ")
}

// missingObject reports whether stderr is only docker complaining that some id
// vanished between listing and inspecting. Inspect still prints the objects it
// did find, so that case is survivable; anything else is not.
func missingObject(stderr []byte) bool {
	s := strings.TrimSpace(string(stderr))
	if s == "" {
		return false
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "No such object") &&
			!strings.Contains(line, "No such container") &&
			!strings.Contains(line, "No such image") &&
			!strings.Contains(line, "No such volume") &&
			!strings.Contains(line, "No such network") {
			return false
		}
	}
	return true
}

// runJSON runs docker and decodes a single JSON document.
func runJSON(ctx context.Context, r Runner, v any, args ...string) error {
	out, errb, err := r.Run(ctx, args...)
	if err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(errb)))
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("docker %s: parsing output: %w", strings.Join(args, " "), err)
	}
	return nil
}

// runNDJSON decodes docker's line-per-record listing format. Some versions
// wrap the same data in a single array, so both shapes are accepted.
func runNDJSON[T any](ctx context.Context, r Runner, args ...string) ([]T, error) {
	out, errb, err := r.Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(errb)))
	}
	return decodeNDJSON[T](out, strings.Join(args, " "))
}

func decodeNDJSON[T any](out []byte, what string) ([]T, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var all []T
		if err := json.Unmarshal(trimmed, &all); err != nil {
			return nil, fmt.Errorf("docker %s: parsing output: %w", what, err)
		}
		return all, nil
	}
	var all []T
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var one T
		if err := json.Unmarshal(line, &one); err != nil {
			return nil, fmt.Errorf("docker %s: parsing output: %w", what, err)
		}
		all = append(all, one)
	}
	return all, nil
}

// inspect batches ids through `docker <kind> inspect`. An empty id list issues
// no call at all, because docker treats a bare inspect as a usage error.
func inspect[T any](ctx context.Context, r Runner, kind string, ids []string) ([]T, error) {
	var all []T
	for start := 0; start < len(ids); start += inspectChunk {
		end := min(start+inspectChunk, len(ids))
		args := append([]string{kind, "inspect"}, ids[start:end]...)
		out, errb, err := r.Run(ctx, args...)
		if err != nil && !missingObject(errb) {
			return nil, fmt.Errorf("docker %s inspect: %w: %s", kind, err, strings.TrimSpace(string(errb)))
		}
		batch, derr := decodeNDJSON[T](out, kind+" inspect")
		if derr != nil {
			return nil, derr
		}
		all = append(all, batch...)
	}
	return all, nil
}

// lines splits command output into non-empty trimmed lines.
func lines(out []byte) []string {
	var ids []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ids = append(ids, l)
		}
	}
	return ids
}
