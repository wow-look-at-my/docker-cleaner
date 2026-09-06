package compose

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mountsFromFixture(t *testing.T) []Mount {
	t.Helper()
	m, err := ReadMounts(filepath.Join("testdata", "mountinfo"), "/var/lib/docker")
	require.NoError(t, err)
	return m
}

func find(t *testing.T, mounts []Mount, point string) Mount {
	t.Helper()
	for _, m := range mounts {
		if m.Point == point {
			return m
		}
	}
	t.Fatalf("no mount at %s", point)
	return Mount{}
}

func TestKernelFilesystemsAreSkipped(t *testing.T) {
	mounts := mountsFromFixture(t)

	for _, point := range []string{"/proc", "/sys", "/dev", "/dev/pts", "/sys/fs/cgroup", "/sys/kernel/debug"} {
		assert.Contains(t, find(t, mounts, point).Skip, "kernel filesystem", point)
	}
}

// tmpfs is walked on purpose: /tmp holds real projects.
func TestRealFilesystemsIncludingTmpfsAreWalked(t *testing.T) {
	mounts := mountsFromFixture(t)

	for _, point := range []string{"/", "/home", "/tmp", "/run", "/mnt/backup drive"} {
		assert.Empty(t, find(t, mounts, point).Skip, point)
	}
}

// Walking a bind mount again would re-read a whole filesystem for nothing.
func TestBindMountOfAWalkedFilesystemIsSkipped(t *testing.T) {
	mounts := mountsFromFixture(t)

	assert.Equal(t, "same filesystem as /home", find(t, mounts, "/srv/mirror").Skip)
}

// Docker's storage root holds layers, not projects.
func TestDockerStorageRootIsSkipped(t *testing.T) {
	mounts := mountsFromFixture(t)

	assert.Equal(t, "docker storage root", find(t, mounts, "/var/lib/docker").Skip)
}

// mountinfo escapes a space as an octal code, and a project can live under
// such a path.
func TestMountPointEscapesAreDecoded(t *testing.T) {
	mounts := mountsFromFixture(t)

	assert.Equal(t, "/mnt/backup drive", find(t, mounts, "/mnt/backup drive").Point)
	assert.Equal(t, "xfs", find(t, mounts, "/mnt/backup drive").FSType)
}

func TestUnescapeOctalLeavesOtherBackslashesAlone(t *testing.T) {
	assert.Equal(t, "/a b", unescapeOctal(`/a\040b`))
	assert.Equal(t, "/a\tb", unescapeOctal(`/a\011b`))
	assert.Equal(t, `/a\zb`, unescapeOctal(`/a\zb`))
	assert.Equal(t, "/plain", unescapeOctal("/plain"))
}

func TestGarbageLinesAreIgnored(t *testing.T) {
	mounts := mountsFromFixture(t)

	assert.Len(t, mounts, 13)
	_, ok := parseMountLine("this line is not a mountinfo record")
	assert.False(t, ok)
	_, ok = parseMountLine("")
	assert.False(t, ok)
}

func TestMissingMountInfoIsAnError(t *testing.T) {
	_, err := ReadMounts(filepath.Join("testdata", "no-such-file"), "")

	require.Error(t, err)
}
