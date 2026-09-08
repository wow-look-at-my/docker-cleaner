// Package dockercli reads and mutates docker state through the docker CLI.
// Every field here is optional: docker's JSON key casing has drifted across
// versions, so a missing key must decode to an empty value, never fail the run.
package dockercli

import "time"

// Version is `docker version --format json`. Server is nil when the daemon is
// unreachable, which is the only reliable check the CLI offers.
type Version struct {
	Client struct {
		Version    string `json:"Version"`
		APIVersion string `json:"ApiVersion"`
	} `json:"Client"`
	Server *struct {
		Version string `json:"Version"`
	} `json:"Server"`
}

// Container is an element of `docker container inspect`.
type Container struct {
	ID      string `json:"Id"`
	Created string `json:"Created"`
	Name    string `json:"Name"`
	// Image is the resolved id; a retagged Config.Image names another.
	Image string `json:"Image"`
	State struct {
		Status     string `json:"Status"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	// SizeRw is the writable layer, which `container inspect --size` fills in.
	SizeRw int64 `json:"SizeRw"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			NetworkID string `json:"NetworkID"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Image is an element of `docker image inspect`.
type Image struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	// Parent is empty on the containerd image store.
	Parent  string `json:"Parent"`
	Created string `json:"Created"`
	Size    int64  `json:"Size"`
}

// Volume is an element of `docker volume inspect`.
type Volume struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	// Mountpoint is where the driver keeps the data, and what gets measured.
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
	Scope      string            `json:"Scope"`
}

// Network is an element of `docker network inspect`.
type Network struct {
	ID         string `json:"Id"`
	Name       string `json:"Name"`
	Scope      string `json:"Scope"`
	Driver     string `json:"Driver"`
	Ingress    bool   `json:"Ingress"`
	ConfigOnly bool   `json:"ConfigOnly"`
	ConfigFrom struct {
		Network string `json:"Network"`
	} `json:"ConfigFrom"`
	// Containers holds live endpoints only. A stopped container's membership
	// appears solely in its own NetworkSettings.Networks.
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
	Labels map[string]string `json:"Labels"`
}

// DiskUsage supplies sizes, and nothing else. See volumesize.go for why
// `docker system df -v` does not fill it.
type DiskUsage struct {
	LayersSize int64
	Images     []ImageUsage
	Containers []ContainerUsage
	Volumes    []VolumeUsage
	BuildCache []CacheRecord
}

// ImageUsage is what an image holds, from `docker image inspect`.
type ImageUsage struct {
	ID   string
	Size int64
}

// ContainerUsage is a container's writable layer, from `container inspect -s`.
type ContainerUsage struct {
	ID     string
	SizeRw int64
}

// VolumeUsage is what a volume holds. Measured says the walk finished, which
// separates an empty volume from a volume nothing could read.
type VolumeUsage struct {
	Name     string
	Size     int64
	Measured bool
}

// CacheRecord is a build cache entry.
type CacheRecord struct {
	ID         string  `json:"ID"`
	Type       string  `json:"Type"`
	Size       int64   `json:"Size"`
	InUse      bool    `json:"InUse"`
	Shared     bool    `json:"Shared"`
	CreatedAt  string  `json:"CreatedAt"`
	LastUsedAt *string `json:"LastUsedAt"`
	UsageCount int     `json:"UsageCount"`
}

// Builder is an element of `docker buildx ls --format json`.
type Builder struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Nodes  []struct {
		Name string `json:"Name"`
	} `json:"Nodes"`
}

// Cache pairs a builder with the records it holds.
type Cache struct {
	Builder string
	Records []CacheRecord
}

// Snapshot is everything the read phase gathered.
type Snapshot struct {
	Version    Version
	DiskUsage  DiskUsage
	Containers []Container
	Images     []Image
	Volumes    []Volume
	Networks   []Network
	Caches     []Cache
	// CacheUnavailable says why no builder could be read.
	CacheUnavailable string
}

// ParseTime reads docker's RFC3339Nano timestamps, rejecting the empty string
// and the unset FinishedAt a container that never ran carries. Both would
// otherwise compare as older than every cutoff.
func ParseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.IsZero() || t.Year() <= 1 {
		return time.Time{}, false
	}
	return t, true
}
