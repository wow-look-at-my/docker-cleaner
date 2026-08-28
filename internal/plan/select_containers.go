package plan

import (
	"fmt"
	"sort"
	"time"

	"github.com/wow-look-at-my/docker-cleaner/internal/compose"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// selectContainers picks the containers that have been offline longer than the
// threshold.
//
// The age comes from State.FinishedAt, never from a prune filter: docker's
// container prune measures creation time, which says nothing about whether a
// container is still wanted.
func (b *builder) selectContainers() {
	sizes := map[string]int64{}
	for _, c := range b.snap.DiskUsage.Containers {
		sizes[c.ID] = c.SizeRw
	}

	containers := append([]dockercli.Container(nil), b.snap.Containers...)
	sort.Slice(containers, func(i, j int) bool { return containers[i].ID < containers[j].ID })

	for _, c := range containers {
		name := containerName(c)
		if reason, ok := aliveState(c.State.Status); ok {
			b.keep(KindContainer, name, reason, c.State.Status)
			continue
		}

		when, basis, ok := containerAge(c)
		if !ok {
			b.keep(KindContainer, name, ReasonNeverRan, "state "+c.State.Status)
			continue
		}
		age := b.now.Sub(when)
		if when.After(b.cutoff) {
			b.keep(KindContainer, name, ReasonTooRecent, basis+" "+HumanAge(age)+" ago")
			continue
		}
		if b.opt.SkipContainers {
			b.keep(KindContainer, name, ReasonStepDisabled, "")
			continue
		}

		t := Target{
			Kind:     KindContainer,
			ID:       c.ID,
			Name:     name,
			Detail:   fmt.Sprintf("%s %s ago, exit %d", basis, HumanAge(age), c.State.ExitCode),
			Size:     sizes[c.ID],
			Commands: [][]string{dockercli.RemoveContainer(c.ID)},
			Note:     containerNote(c),
		}
		if basis == "created" {
			t.Detail = fmt.Sprintf("created %s ago, never started", HumanAge(age))
		}
		b.plan.Containers = append(b.plan.Containers, t)
		b.removing[c.ID] = true
	}
}

// aliveState reports the states that hold their resources open. Each gets its
// own reason: a paused container is not the same finding as a running one, and
// a report that says so saves the next person a look.
func aliveState(status string) (Reason, bool) {
	switch status {
	case "running":
		return ReasonRunning, true
	case "paused":
		return ReasonPaused, true
	case "restarting":
		return ReasonRestarting, true
	case "removing":
		return ReasonRemoving, true
	case "exited", "dead", "created":
		return "", false
	default:
		return ReasonUnknownState, true
	}
}

// containerAge picks the timestamp that means "offline since".
//
// A container that never ran has FinishedAt set to the zero time, which parses
// to year one and is therefore older than every cutoff. Comparing it naively
// deletes every such container, including one made a minute ago. So a created
// container ages by its creation instead, and any other state without a usable
// FinishedAt is kept and reported.
func containerAge(c dockercli.Container) (time.Time, string, bool) {
	if c.State.Status == "created" {
		if t, ok := dockercli.ParseTime(c.Created); ok {
			return t, "created", true
		}
		return time.Time{}, "", false
	}
	if t, ok := dockercli.ParseTime(c.State.FinishedAt); ok {
		return t, "exited", true
	}
	return time.Time{}, "", false
}

// containerNote flags a container worth a second look before confirming. It
// never changes the verdict.
func containerNote(c dockercli.Container) string {
	switch p := c.HostConfig.RestartPolicy.Name; p {
	case "always", "unless-stopped":
		return "restart policy " + p + ": someone stopped this deliberately"
	}
	if project := c.Config.Labels[compose.LabelProject]; project != "" {
		return "compose project " + project
	}
	return ""
}
