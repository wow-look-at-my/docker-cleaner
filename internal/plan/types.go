// Package plan decides what to remove. Compute is pure: it takes a snapshot,
// the options and a single clock reading, and returns the plan. Everything the
// dry run prints and everything the apply phase executes comes from here, so
// they can never disagree.
package plan

import "time"

// Kind names a class of docker resource.
type Kind string

// The resource kinds this tool touches.
const (
	KindContainer  Kind = "container"
	KindImage      Kind = "image"
	KindVolume     Kind = "volume"
	KindNetwork    Kind = "network"
	KindBuildCache Kind = "build cache"
)

// Reason says why a resource was kept. Every non-target carries a reason.
type Reason string

// Why a container was kept.
const (
	ReasonRunning      Reason = "running"
	ReasonPaused       Reason = "paused"
	ReasonRestarting   Reason = "restarting"
	ReasonRemoving     Reason = "being removed"
	ReasonUnknownState Reason = "unknown state"
	ReasonTooRecent    Reason = "too recent"
	ReasonNeverRan     Reason = "never finished, so it has no age"
)

// Why an image was kept.
const (
	ReasonReferenced     Reason = "in use by a container"
	ReasonReferencedName Reason = "in use by a container, by name"
	ReasonNewestInRepo   Reason = "newest in its repository"
	ReasonTaggedLatest   Reason = "tagged latest"
	ReasonNoTimestamps   Reason = "its repository has no usable timestamps"
	ReasonKeepPattern    Reason = "matches --keep"
	ReasonParentOfKept   Reason = "parent of a kept image"
)

// Why a volume or network was kept.
const (
	ReasonInUse           Reason = "attached to a container"
	ReasonPredefined      Reason = "predefined by docker"
	ReasonSwarmScope      Reason = "not local scope"
	ReasonIngress         Reason = "swarm ingress"
	ReasonConfigOnly      Reason = "a config-only network"
	ReasonNonLocalDriver  Reason = "not the local driver"
	ReasonClaimedByCompos Reason = "claimed by a compose project still on disk"
	ReasonScanIncomplete  Reason = "nothing has named a compose file for its project"
	ReasonComposeBadFile  Reason = "its compose file could not be read"
)

// Why build cache was kept.
const (
	ReasonCacheInUse    Reason = "in use"
	ReasonCacheRecent   Reason = "used recently"
	ReasonCacheUnusable Reason = "the build cache could not be enumerated"
)

// Why anything was kept.
const ReasonStepDisabled Reason = "that step is turned off"

// Target is a resource this run will remove.
type Target struct {
	Kind   Kind
	ID     string
	Name   string
	Detail string
	// Note flags a target worth another look. It never changes the verdict.
	Note string
	Size int64
	// FreedBy names the containers whose removal made this collectible.
	FreedBy []string
	// Commands are the exact invocations, in order.
	Commands [][]string
}

// Kept is a resource this run deliberately left alone.
type Kept struct {
	Kind   Kind
	Name   string
	Reason Reason
	Detail string
}

// CachePlan is the build cache step for a builder.
type CachePlan struct {
	Builder string
	Records int
	Size    int64
	Until   string
	Command []string
}

// Plan is the whole decision.
type Plan struct {
	Now           time.Time
	Age           time.Duration
	BuildCacheAge time.Duration
	Cutoff        time.Time
	CacheCutoff   time.Time

	Containers []Target
	Images     []Target
	Volumes    []Target
	Networks   []Target
	Caches     []CachePlan
	Kept       []Kept

	// ComposeComplete false changes what "not found" means, so it is reported.
	ComposeComplete bool
	ComposeFailures []string
	Warnings        []string
}

// Targets returns every removal in apply order.
func (p Plan) Targets() []Target {
	out := make([]Target, 0, len(p.Containers)+len(p.Images)+len(p.Volumes)+len(p.Networks))
	out = append(out, p.Containers...)
	out = append(out, p.Images...)
	out = append(out, p.Volumes...)
	out = append(out, p.Networks...)
	return out
}

// Empty reports whether there is nothing at all to do.
func (p Plan) Empty() bool { return len(p.Targets()) == 0 && len(p.Caches) == 0 }

// Reclaimable totals the bytes the plan expects to free.
func (p Plan) Reclaimable() int64 {
	var n int64
	for _, t := range p.Targets() {
		n += t.Size
	}
	for _, c := range p.Caches {
		n += c.Size
	}
	return n
}

// Options are the knobs Compute reads.
type Options struct {
	Age           time.Duration
	BuildCacheAge time.Duration
	Keep          []string

	SkipContainers bool
	SkipImages     bool
	SkipVolumes    bool
	SkipNetworks   bool
	SkipCache      bool
}
