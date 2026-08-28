package plan

import (
	"path"
	"strings"
)

// SplitReference splits an image reference at the last colon, but only when
// the tail holds no slash. Without that guard localhost:5000/myapp splits into
// repository "localhost", collapsing a whole registry into one repository.
func SplitReference(ref string) (repo, version string) {
	i := strings.LastIndexByte(ref, ':')
	if i < 0 {
		return ref, ""
	}
	if strings.Contains(ref[i+1:], "/") {
		return ref, ""
	}
	return ref[:i], ref[i+1:]
}

// IsAnonymousVolume reports a docker-generated name: 64 hex characters.
func IsAnonymousVolume(name string) bool {
	if len(name) != 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// MatchKeep reports the first --keep pattern matching any of a resource's
// names. Patterns are globs, and * deliberately crosses /, so registry.local/*
// covers a whole registry.
func MatchKeep(patterns, names []string) string {
	for _, p := range patterns {
		for _, n := range names {
			if glob(p, n) {
				return p
			}
		}
	}
	return ""
}

// glob matches a pattern against a name with * spanning any run of characters,
// separators included. path.Match stops at /, which is the wrong behaviour for
// image references.
func glob(pattern, name string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == name || (path.Base(pattern) == pattern && strings.HasPrefix(name, pattern))
	}
	parts := strings.Split(pattern, "*")
	rest := name

	if parts[0] != "" {
		if !strings.HasPrefix(rest, parts[0]) {
			return false
		}
		rest = rest[len(parts[0]):]
	}
	last := parts[len(parts)-1]
	for _, mid := range parts[1 : len(parts)-1] {
		if mid == "" {
			continue
		}
		i := strings.Index(rest, mid)
		if i < 0 {
			return false
		}
		rest = rest[i+len(mid):]
	}
	if last == "" {
		return true
	}
	return strings.HasSuffix(rest, last)
}

// ShortID trims a sha256: prefixed id to something a person can read back.
func ShortID(id string) string {
	s := strings.TrimPrefix(id, "sha256:")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
