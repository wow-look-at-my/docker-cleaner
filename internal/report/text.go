package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
)

// Text renders the plan a person confirms. Every target shows its size, and a
// target freed by removing a container says which one, so a cascade is never a
// surprise.
func Text(w io.Writer, p plan.Plan, before string, dryRun, showKept bool) {
	header(w, p, dryRun)
	if before != "" {
		fmt.Fprintln(w, "BEFORE")
		fmt.Fprintln(w, strings.TrimRight(before, "\n"))
		fmt.Fprintln(w)
	}

	section(w, "CONTAINERS TO REMOVE", p.Containers)
	section(w, "IMAGES TO REMOVE", p.Images)
	section(w, "VOLUMES TO REMOVE", p.Volumes)
	section(w, "NETWORKS TO REMOVE", p.Networks)
	cacheSection(w, p)
	keptSection(w, p, showKept)
	warnings(w, p)

	if p.Empty() {
		fmt.Fprintln(w, "Nothing to remove.")
		return
	}
	fmt.Fprintf(w, "TOTAL  %s reclaimable\n", Bytes(p.Reclaimable()))
	if dryRun {
		fmt.Fprintln(w, "\nDry run: nothing was removed.")
	}
}

func header(w io.Writer, p plan.Plan, dryRun bool) {
	mode := ""
	if dryRun {
		mode = "   DRY RUN"
	}
	fmt.Fprintf(w, "docker-cleaner%s\n", mode)
	fmt.Fprintf(w, "containers offline over %s (before %s)\n",
		plan.HumanAge(p.Age), p.Cutoff.UTC().Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(w, "build cache unused over %s (before %s)\n",
		plan.HumanAge(p.BuildCacheAge), p.CacheCutoff.UTC().Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(w, "compose projects: %s, %d directories walked\n\n",
		completeness(p), p.DirsWalked)
}

// completeness is load-bearing, not decoration: it says whether "no compose
// file found" was allowed to mean "the project was deleted".
func completeness(p plan.Plan) string {
	if p.ComposeComplete {
		return "search complete"
	}
	return "SEARCH INCOMPLETE, so unresolved projects were kept"
}

func section(w io.Writer, title string, targets []plan.Target) {
	if len(targets) == 0 {
		return
	}
	var total int64
	for _, t := range targets {
		total += t.Size
	}
	fmt.Fprintf(w, "%s  (%d, %s)\n", title, len(targets), Bytes(total))
	for _, t := range targets {
		fmt.Fprintf(w, "  %-40s %10s  %s\n", t.Name, Bytes(t.Size), t.Detail)
		if len(t.FreedBy) > 0 {
			fmt.Fprintf(w, "      freed by removing %s\n", strings.Join(t.FreedBy, ", "))
		}
		if t.Note != "" {
			fmt.Fprintf(w, "      note: %s\n", t.Note)
		}
	}
	fmt.Fprintln(w)
}

func cacheSection(w io.Writer, p plan.Plan) {
	if len(p.Caches) == 0 {
		return
	}
	fmt.Fprintln(w, "BUILD CACHE")
	for _, c := range p.Caches {
		name := c.Builder
		if name == "" {
			name = "default"
		}
		fmt.Fprintf(w, "  %-40s %10s  %d records unused in %s\n", name, Bytes(c.Size), c.Records, c.Until)
		fmt.Fprintf(w, "      will run: docker %s\n", strings.Join(c.Command, " "))
	}
	fmt.Fprintln(w)
}

// keptSection groups by reason so the common cases read as one line each,
// while --show-kept names every resource.
func keptSection(w io.Writer, p plan.Plan, showKept bool) {
	if len(p.Kept) == 0 {
		return
	}
	fmt.Fprintln(w, "KEPT")
	if showKept {
		for _, k := range p.Kept {
			detail := ""
			if k.Detail != "" {
				detail = " (" + k.Detail + ")"
			}
			fmt.Fprintf(w, "  %-40s %-12s %s%s\n", k.Name, k.Kind, k.Reason, detail)
		}
		fmt.Fprintln(w)
		return
	}

	counts := map[string]int{}
	for _, k := range p.Kept {
		counts[string(k.Kind)+"\x00"+string(k.Reason)]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		fmt.Fprintf(w, "  %-30s %s\n", pluralKind(parts[0], counts[key]), parts[1])
	}
	fmt.Fprintf(w, "  (--show-kept lists all %d)\n\n", len(p.Kept))
}

func warnings(w io.Writer, p plan.Plan) {
	for _, f := range p.ComposeFailures {
		fmt.Fprintf(w, "COULD NOT SEARCH: %s\n", f)
	}
	for _, warn := range p.Warnings {
		fmt.Fprintf(w, "WARNING: %s\n", warn)
	}
	if len(p.ComposeFailures) > 0 || len(p.Warnings) > 0 {
		fmt.Fprintln(w)
	}
}

func pluralKind(kind string, n int) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", kind)
	}
	return fmt.Sprintf("%d %ss", n, kind)
}
