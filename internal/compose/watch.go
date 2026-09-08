package compose

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// event is a line of `docker events --format json`.
type event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// Watch records every compose project docker creates a container for.
//
// It narrows a window and nothing more: an `up` followed by a `down` between
// cleanup runs leaves no container to read labels from. It is never a
// correctness dependency, because a project it misses is unresolved, and an
// unresolved project keeps everything it claims.
func Watch(ctx context.Context, dockerBin, indexPath string, log io.Writer) error {
	cmd := exec.CommandContext(ctx, dockerBin,
		"events", "--filter", "type=container", "--filter", "event=create", "--format", "json")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		project, files, ok := ProjectFromEvent(scanner.Bytes())
		if !ok {
			continue
		}
		idx := LoadIndex(indexPath)
		idx.Record(project, files, time.Now())
		idx.Save()
		if idx.Warning != "" {
			fmt.Fprintln(log, "docker-cleaner watch:", idx.Warning)
		} else {
			fmt.Fprintf(log, "docker-cleaner watch: recorded project %q -> %v\n", project, files)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return cmd.Wait()
}

// ProjectFromEvent extracts a project and its compose files from an event.
// A throwaway `compose run` container carries the same labels and is just as
// good a source: oneoff=True does not make its config_files less true.
func ProjectFromEvent(line []byte) (project string, files []string, ok bool) {
	var e event
	if err := json.Unmarshal(line, &e); err != nil {
		return "", nil, false
	}
	project = e.Actor.Attributes[LabelProject]
	files = splitList(e.Actor.Attributes[LabelConfigFiles])
	if project == "" || len(files) == 0 {
		return "", nil, false
	}
	return project, files, true
}
