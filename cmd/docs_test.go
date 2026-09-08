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

// The flag reference is generated, never typed: a hand-written copy drifts as
// soon as a flag's help changes, and nothing notices.
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
	t.Parallel()

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
	t.Parallel()

	err := runRoot(t, "--json")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--json needs --dry-run or --yes")
}

func TestABadAgeIsRejectedBeforeDockerIsTouched(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	err := runRoot(t, "everything")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}

// A parsed flag must not reach the next invocation. A leaked --dry-run carries
// --json past the guard above and into a real run, which then reads docker and
// walks the disk.
func TestAnInvocationDoesNotLeakItsFlags(t *testing.T) {
	t.Parallel()

	require.Error(t, runRoot(t, "--age=tuesday", "--dry-run"))

	err := runRoot(t, "--json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--json needs --dry-run or --yes")
}

// runRoot parses an invocation on a command tree of its own, so a test never
// sees another test's flags. Every case here fails before RunE reaches docker.
func runRoot(t *testing.T, args ...string) error {
	t.Helper()

	var out strings.Builder
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	return root.Execute()
}
