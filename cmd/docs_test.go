package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const docsPath = "../docs/cmdline_args.txt"

// The flag reference is generated, never typed: a hand-written copy drifts the
// first time a flag's help changes, and nothing notices.
func TestCommittedFlagReferenceIsCurrent(t *testing.T) {
	got, err := HelpDump()
	require.NoError(t, err)

	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(docsPath), 0o755))
		require.NoError(t, os.WriteFile(docsPath, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(docsPath)
	require.NoError(t, err)
	assert.Equal(t, string(want), got,
		"docs/cmdline_args.txt is stale; regenerate it with UPDATE_GOLDEN=1 go-toolchain")
}

func TestEveryFlagIsDocumented(t *testing.T) {
	got, err := HelpDump()
	require.NoError(t, err)

	for _, flag := range []string{
		"--dry-run", "--age", "--build-cache-age", "--yes", "--no-containers",
		"--no-images", "--no-volumes", "--no-networks", "--no-build-cache",
		"--keep", "--json", "--show-kept", "--rescan", "--docker-bin",
		"--timeout", "--index", "--mountinfo", "--docker-root",
	} {
		assert.Contains(t, got, flag)
	}
	assert.Contains(t, got, "docker-cleaner watch", "the watch subcommand is part of the interface")
}

// An interactive prompt inside a JSON document is unparseable, so the flag
// combination is refused rather than silently producing junk.
func TestJSONWithoutDryRunOrYesIsRefused(t *testing.T) {
	err := runRoot(t, "--json")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--json needs --dry-run or --yes")
}

func TestABadAgeIsRejectedBeforeDockerIsTouched(t *testing.T) {
	for flag, want := range map[string]string{
		"--age=tuesday":            "--age",
		"--build-cache-age=-1h":    "--build-cache-age",
		"--age=":                   "--age",
		"--build-cache-age=7 days": "--build-cache-age",
	} {
		err := runRoot(t, flag, "--dry-run")

		require.Error(t, err, flag)
		assert.Contains(t, err.Error(), want, flag)
	}
}

func TestExtraArgumentsAreRejected(t *testing.T) {
	err := runRoot(t, "everything")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}

// runRoot parses one invocation. Every case here fails before RunE reaches
// docker, so nothing runs and nothing exits.
func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	// Flags keep their last parsed value, so start from the defaults.
	opts.age, opts.cacheAge, opts.json, opts.dryRun = "30d", "7d", false, false

	var out strings.Builder
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		opts.age, opts.cacheAge, opts.json, opts.dryRun = "30d", "7d", false, false
	})
	return rootCmd.Execute()
}
