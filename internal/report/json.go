package report

import (
	"encoding/json"
	"io"

	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
)

// Doc is the machine-readable plan. It carries the literal argv of every
// command, which is what makes a dry run auditable rather than a summary.
type Doc struct {
	DryRun          bool        `json:"dry_run"`
	Aborted         bool        `json:"aborted"`
	AgeSeconds      float64     `json:"age_seconds"`
	CacheAgeSeconds float64     `json:"build_cache_age_seconds"`
	Cutoff          string      `json:"cutoff"`
	CacheCutoff     string      `json:"build_cache_cutoff"`
	ComposeComplete bool        `json:"compose_search_complete"`
	ComposeFailures []string    `json:"compose_search_failures,omitempty"`
	SkippedMounts   []string    `json:"skipped_mounts,omitempty"`
	DirsWalked      int         `json:"directories_walked"`
	Warnings        []string    `json:"warnings,omitempty"`
	Containers      []Item      `json:"containers"`
	Images          []Item      `json:"images"`
	Volumes         []Item      `json:"volumes"`
	Networks        []Item      `json:"networks"`
	BuildCache      []CacheItem `json:"build_cache"`
	Kept            []KeptItem  `json:"kept"`
	Operations      []Operation `json:"operations,omitempty"`
	Reclaimable     int64       `json:"reclaimable_bytes"`
	ExitCode        int         `json:"exit_code"`
}

// Item is one planned removal.
type Item struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Detail   string     `json:"detail,omitempty"`
	Note     string     `json:"note,omitempty"`
	Size     int64      `json:"size_bytes"`
	FreedBy  []string   `json:"freed_by,omitempty"`
	Commands [][]string `json:"commands"`
}

// CacheItem is one builder's prune.
type CacheItem struct {
	Builder string   `json:"builder"`
	Records int      `json:"records"`
	Size    int64    `json:"size_bytes"`
	Until   string   `json:"until"`
	Command []string `json:"command"`
}

// KeptItem is one resource left alone, and why.
type KeptItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// Operation is one command that actually ran.
type Operation struct {
	Argv []string `json:"argv"`
	OK   bool     `json:"ok"`
	Err  string   `json:"error,omitempty"`
}

// Build assembles the document from a plan.
func Build(p plan.Plan, dryRun bool) Doc {
	d := Doc{
		DryRun:          dryRun,
		AgeSeconds:      p.Age.Seconds(),
		CacheAgeSeconds: p.BuildCacheAge.Seconds(),
		Cutoff:          p.Cutoff.UTC().Format("2006-01-02T15:04:05Z"),
		CacheCutoff:     p.CacheCutoff.UTC().Format("2006-01-02T15:04:05Z"),
		ComposeComplete: p.ComposeComplete,
		ComposeFailures: p.ComposeFailures,
		SkippedMounts:   p.SkippedMounts,
		DirsWalked:      p.DirsWalked,
		Warnings:        p.Warnings,
		Containers:      items(p.Containers),
		Images:          items(p.Images),
		Volumes:         items(p.Volumes),
		Networks:        items(p.Networks),
		BuildCache:      []CacheItem{},
		Kept:            []KeptItem{},
		Reclaimable:     p.Reclaimable(),
	}
	for _, c := range p.Caches {
		d.BuildCache = append(d.BuildCache, CacheItem{c.Builder, c.Records, c.Size, c.Until, c.Command})
	}
	for _, k := range p.Kept {
		d.Kept = append(d.Kept, KeptItem{string(k.Kind), k.Name, string(k.Reason), k.Detail})
	}
	return d
}

// Write emits the document.
func Write(w io.Writer, d Doc) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}

func items(targets []plan.Target) []Item {
	out := make([]Item, 0, len(targets))
	for _, t := range targets {
		out = append(out, Item{t.ID, t.Name, t.Detail, t.Note, t.Size, t.FreedBy, t.Commands})
	}
	return out
}
