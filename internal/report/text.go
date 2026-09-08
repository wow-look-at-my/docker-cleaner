package report

import (
	"io"
	"sort"
	"strings"
	"text/template"

	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
)

// doc is the whole terminal report. Empty sections disappear, so a quiet run
// stays short.
var doc = template.Must(template.New("report").Funcs(template.FuncMap{
	"bytes":       Bytes,
	"age":         plan.HumanAge,
	"join":        strings.Join,
	"total":       totalSize,
	"argv":        func(a []string) string { return "docker " + strings.Join(a, " ") },
	"trim":        func(s string) string { return strings.TrimRight(s, "\n") },
	"cutoff":      func(p plan.Plan) string { return p.Cutoff.UTC().Format("2006-01-02 15:04 MST") },
	"cacheCutoff": func(p plan.Plan) string { return p.CacheCutoff.UTC().Format("2006-01-02 15:04 MST") },
	"builder":     builderName,
}).Parse(`docker-cleaner{{if .DryRun}}   DRY RUN{{end}}
containers offline over {{age .Plan.Age}} (before {{cutoff .Plan}})
build cache unused over {{age .Plan.BuildCacheAge}} (before {{cacheCutoff .Plan}})
compose projects: {{.Completeness}}
{{- if .Before}}

BEFORE
{{trim .Before}}
{{- end}}
{{- range .Sections}}{{if .Targets}}

{{.Title}}  ({{len .Targets}}, {{bytes (total .Targets)}})
{{- range .Targets}}
  {{printf "%-40s" .Name}} {{printf "%10s" (bytes .Size)}}  {{.Detail}}
  {{- if .FreedBy}}
      freed by removing {{join .FreedBy ", "}}
  {{- end}}
  {{- if .Note}}
      note: {{.Note}}
  {{- end}}
{{- end}}
{{- end}}{{end}}
{{- if .Plan.Caches}}

BUILD CACHE
{{- range .Plan.Caches}}
  {{printf "%-40s" (builder .Builder)}} {{printf "%10s" (bytes .Size)}}  {{.Records}} record{{if ne .Records 1}}s{{end}} unused in {{.Until}}
      will run: {{argv .Command}}
{{- end}}
{{- end}}
{{- if .Kept}}

KEPT
{{- range .Kept}}
  {{.}}
{{- end}}
{{- end}}
{{- range .Plan.ComposeFailures}}
COULD NOT SEARCH: {{.}}
{{- end}}
{{- range .Plan.Warnings}}
WARNING: {{.}}
{{- end}}
{{- if .Plan.Empty}}

Nothing to remove.
{{- else}}

TOTAL  {{bytes .Plan.Reclaimable}} reclaimable
{{- if .DryRun}}

Dry run: nothing was removed.
{{- end}}
{{- end}}
`))

// section is a group of removals.
type section struct {
	Title   string
	Targets []plan.Target
}

// view is what the template renders.
type view struct {
	Plan         plan.Plan
	Before       string
	DryRun       bool
	Completeness string
	Sections     []section
	Kept         []string
}

// Text renders the plan a person confirms. A target the cascade freed names
// the container that freed it, so nothing disappears unexplained.
func Text(w io.Writer, p plan.Plan, before string, dryRun, showKept bool) error {
	return doc.Execute(w, view{
		Plan:         p,
		Before:       before,
		DryRun:       dryRun,
		Completeness: completeness(p),
		Sections: []section{
			{"CONTAINERS TO REMOVE", p.Containers},
			{"IMAGES TO REMOVE", p.Images},
			{"VOLUMES TO REMOVE", p.Volumes},
			{"NETWORKS TO REMOVE", p.Networks},
		},
		Kept: keptLines(p, showKept),
	})
}

// completeness is load-bearing, not decoration: it says whether "no compose
// file found" was allowed to mean "the project was deleted".
func completeness(p plan.Plan) string {
	if p.ComposeComplete {
		return "resolved from docker"
	}
	return "NOT FULLY RESOLVED, so unresolved projects were kept"
}

// keptLines groups by reason so common cases read as a line, while
// --show-kept names every resource.
func keptLines(p plan.Plan, showKept bool) []string {
	if len(p.Kept) == 0 {
		return nil
	}
	if showKept {
		out := make([]string, 0, len(p.Kept))
		for _, k := range p.Kept {
			line := pad(k.Name, 40) + " " + pad(string(k.Kind), 12) + " " + string(k.Reason)
			if k.Detail != "" {
				line += " (" + k.Detail + ")"
			}
			out = append(out, line)
		}
		return out
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

	out := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		out = append(out, pad(pluralKind(parts[0], counts[key]), 30)+" "+parts[1])
	}
	return append(out, "(--show-kept lists all "+itoa(len(p.Kept))+")")
}

func totalSize(targets []plan.Target) int64 {
	var n int64
	for _, t := range targets {
		n += t.Size
	}
	return n
}

func builderName(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

func pluralKind(kind string, n int) string {
	if n == 1 {
		return "1 " + kind
	}
	return itoa(n) + " " + kind + "s"
}

func pad(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
