package compose

import (
	"github.com/wow-look-at-my/go-containers/set"
	"os"
	"sort"
	"strings"
)

// DefaultMountInfo is the kernel's view of what is mounted.
const DefaultMountInfo = "/proc/self/mountinfo"

// Mount is one filesystem the scan may walk.
type Mount struct {
	Point  string
	FSType string
	Device string
	// Skip is empty when the mount is walked, otherwise it says why not. Every
	// skip is reported: choosing not to look somewhere is a judgement, and it
	// should be visible rather than assumed.
	Skip string
}

// pseudoFS are kernel interfaces holding no compose projects. tmpfs is
// deliberately absent: /tmp holds real ones.
var pseudoFS = set.Of[string]("proc", "sysfs", "devtmpfs", "devpts",
	"securityfs", "debugfs", "tracefs", "bpf",
	"pstore", "configfs", "fusectl", "nsfs",
	"binfmt_misc", "autofs", "mqueue", "hugetlbfs",
	"cgroup", "cgroup2", "rpc_pipefs", "efivarfs",
	"selinuxfs", "ramfs", "fuse.portal")

// ReadMounts parses mountinfo and decides what to walk. Docker's own storage
// root is skipped: it holds container layers, not projects.
func ReadMounts(path, dockerRoot string) ([]Mount, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseMounts(string(data), dockerRoot), nil
}

func parseMounts(text, dockerRoot string) []Mount {
	var mounts []Mount
	for _, line := range strings.Split(text, "\n") {
		m, ok := parseMountLine(line)
		if !ok {
			continue
		}
		mounts = append(mounts, m)
	}

	sort.Slice(mounts, func(a, b int) bool { return mounts[a].Point < mounts[b].Point })

	seenDevice := map[string]string{}
	for i := range mounts {
		m := &mounts[i]
		switch {
		case pseudoFS.Contains(m.FSType) || strings.HasPrefix(m.FSType, "cgroup"):
			m.Skip = "kernel filesystem (" + m.FSType + ")"
		case dockerRoot != "" && underPath(m.Point, dockerRoot):
			m.Skip = "docker storage root"
		default:
			if first, dup := seenDevice[m.Device]; dup {
				m.Skip = "same filesystem as " + first
			} else {
				seenDevice[m.Device] = m.Point
			}
		}
	}
	return mounts
}

// parseMountLine reads one mountinfo record. The optional fields between the
// root field and the separator are variable in number, so the separator is
// what the format is anchored on.
func parseMountLine(line string) (Mount, bool) {
	fields := strings.Fields(line)
	sep := -1
	for i, f := range fields {
		if f == "-" {
			sep = i
			break
		}
	}
	if sep < 5 || sep+2 >= len(fields) {
		return Mount{}, false
	}
	return Mount{
		Point:  unescapeOctal(fields[4]),
		Device: fields[2],
		FSType: fields[sep+1],
	}, true
}

// unescapeOctal decodes the \040-style escapes mountinfo uses for spaces,
// tabs, newlines and backslashes in paths.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			v := 0
			ok := true
			for _, c := range []byte(s[i+1 : i+4]) {
				if c < '0' || c > '7' {
					ok = false
					break
				}
				v = v*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func underPath(p, root string) bool {
	root = strings.TrimSuffix(root, "/")
	return p == root || strings.HasPrefix(p, root+"/")
}
