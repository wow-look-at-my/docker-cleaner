package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
	"github.com/wow-look-at-my/docker-cleaner/internal/plan"
)

var now = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

// fixedPlan is a plan covering every section the report can print.
func fixedPlan() plan.Plan {
	return plan.Plan{
		Now:           now,
		Age:           30 * 24 * time.Hour,
		BuildCacheAge: 7 * 24 * time.Hour,
		Cutoff:        now.Add(-30 * 24 * time.Hour),
		CacheCutoff:   now.Add(-7 * 24 * time.Hour),
		Containers: []plan.Target{{
			Kind: plan.KindContainer, ID: "c1", Name: "web-old", Size: 4096,
			Detail: "exited 47d ago", Note: "restart policy unless-stopped",
			Commands: [][]string{{"rm", "c1"}},
		}},
		Images: []plan.Target{{
			Kind: plan.KindImage, ID: "sha256:v1", Name: "myapp:v1", Size: 268435456,
			Detail: "created 200d ago", FreedBy: []string{"web-old"},
			Commands: [][]string{{"rmi", "myapp:v1"}},
		}},
		Volumes: []plan.Target{{
			Kind: plan.KindVolume, ID: "scratch", Name: "scratch", Size: 1024,
			Detail: "no compose project", Commands: [][]string{{"volume", "rm", "scratch"}},
		}},
		Networks: []plan.Target{{
			Kind: plan.KindNetwork, ID: "n1", Name: "orphan_default",
			Detail: "no attached containers", Commands: [][]string{{"network", "rm", "n1"}},
		}},
		Caches: []plan.CachePlan{{
			Builder: "", Records: 3, Size: 1048576, Until: "168h",
			Command: dockercli.PruneBuildCache("", "168h"),
		}, {
			Builder: "ci-builder", Records: 1, Size: 4194304, Until: "168h",
			Command: dockercli.PruneBuildCache("ci-builder", "168h"),
		}},
		Kept: []plan.Kept{
			{Kind: plan.KindContainer, Name: "webapp-db-1", Reason: plan.ReasonRunning},
			{Kind: plan.KindImage, Name: "myapp:v2", Reason: plan.ReasonNewestInRepo},
			{Kind: plan.KindImage, Name: "nginx:latest", Reason: plan.ReasonTaggedLatest},
			{Kind: plan.KindVolume, Name: "webapp_pgdata", Reason: plan.ReasonClaimedByCompos, Detail: "webapp"},
		},
		ComposeComplete: true,
	}
}

const beforeTable = "TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE\n" +
	"Images          2         1         384MB     134.2MB (34%)\n"

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func render(t *testing.T, p plan.Plan, before string, dryRun, showKept bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, Text(&buf, p, before, dryRun, showKept))
	return buf.Bytes()
}

func TestDryRunReport(t *testing.T) {
	golden(t, "dry-run.golden", render(t, fixedPlan(), beforeTable, true, false))
}

func TestShowKeptItemisesEveryResource(t *testing.T) {
	golden(t, "show-kept.golden", render(t, fixedPlan(), "", true, true))
}

func TestNothingToRemoveReport(t *testing.T) {
	p := plan.Plan{
		Age: 30 * 24 * time.Hour, BuildCacheAge: 7 * 24 * time.Hour,
		Cutoff: now.Add(-30 * 24 * time.Hour), CacheCutoff: now.Add(-7 * 24 * time.Hour),
		ComposeComplete: true,
	}

	golden(t, "empty.golden", render(t, p, "", false, false))
}

// An unresolved project changes what "not found" means, so the report says so
// in its header rather than burying it.
func TestAnUnresolvedProjectIsAnnouncedAndItsFailuresListed(t *testing.T) {
	p := fixedPlan()
	p.ComposeComplete = false
	p.ComposeFailures = []string{"/srv: permission denied"}
	p.Warnings = []string{"cannot write the project index under /var/lib/docker-cleaner"}

	out := string(render(t, p, "", true, false))

	assert.Contains(t, out, "NOT FULLY RESOLVED")
	assert.Contains(t, out, "COULD NOT SEARCH: /srv: permission denied")
	assert.Contains(t, out, "WARNING: cannot write the project index")
}

// The exact prune argv is printed because it is the command whose effect
// cannot be undone by re-running the tool.
func TestBuildCachePrintsTheCommandItWillRun(t *testing.T) {
	out := string(render(t, fixedPlan(), "", true, false))

	assert.Contains(t, out, "docker buildx prune --force --filter until=168h")
	assert.Contains(t, out, "docker buildx prune --builder ci-builder --force --filter until=168h")
}

func TestBytesMatchesDockersUnits(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0B", 999: "999B", 1000: "1.0kB", 1500: "1.5kB",
		1048576: "1.0MB", 268435456: "268.4MB", 1073741824: "1.1GB",
	} {
		assert.Equal(t, want, Bytes(n))
	}
}

// The operations array is what makes a dry run auditable: it is the literal
// argv, not a summary of it.
func TestJSONCarriesEveryCommand(t *testing.T) {
	var buf bytes.Buffer
	d := Build(fixedPlan(), true)
	d.Operations = []Operation{
		{Argv: []string{"rm", "c1"}, OK: true},
		{Argv: []string{"rmi", "myapp:v1"}, OK: false, Err: "image is being used"},
	}
	require.NoError(t, Write(&buf, d))

	var back Doc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &back))
	assert.True(t, back.DryRun)
	assert.Equal(t, [][]string{{"rm", "c1"}}, back.Containers[0].Commands)
	assert.Equal(t, []string{"web-old"}, back.Images[0].FreedBy)
	assert.Equal(t, []string{"buildx", "prune", "--force", "--filter", "until=168h"}, back.BuildCache[0].Command)
	assert.Equal(t, int64(273683456), back.Reclaimable)
	assert.Equal(t, "image is being used", back.Operations[1].Err)
	assert.Len(t, back.Kept, 4)
}

// Empty lists must serialise as [], because null makes a consumer special-case
// the quiet run.
func TestJSONEmptyPlanHasEmptyArraysNotNulls(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, Write(&buf, Build(plan.Plan{ComposeComplete: true}, true)))

	body := buf.String()
	for _, key := range []string{"containers", "images", "volumes", "networks", "build_cache", "kept"} {
		assert.Contains(t, body, fmt.Sprintf("%q: []", key))
	}
	assert.NotContains(t, body, "null")
}

func TestJSONIsIndentedForReading(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, Write(&buf, Build(fixedPlan(), true)))

	assert.True(t, strings.Contains(buf.String(), "\n  \"dry_run\": true"))
}
