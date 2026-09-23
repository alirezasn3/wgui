package update

import (
	"regexp"
	"strconv"
	"strings"
)

// semver is a parsed release tag: vMAJOR.MINOR.PATCH with an optional
// pre-release suffix, the shape `make release` insists on.
type semver struct {
	major, minor, patch int
	pre                 string
}

var tagPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

// describeSuffix is what `git describe` adds to a build made after a tag
// (v2.0.0-3-g1a2b3c4) or from a tree with uncommitted changes (-dirty). Such a
// build is not a release, whatever its version string starts with.
var describeSuffix = regexp.MustCompile(`(-\d+-g[0-9a-f]{7,})?(-dirty)?$`)

func parseVersion(v string) (semver, bool) {
	m := tagPattern.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return semver{}, false
	}
	var s semver
	s.major, _ = strconv.Atoi(m[1])
	s.minor, _ = strconv.Atoi(m[2])
	s.patch, _ = strconv.Atoi(m[3])
	s.pre = m[4]
	return s, true
}

// isRelease reports whether a build's version is a published tag rather than a
// development build. Only a release is offered updates: a binary built from a
// working tree is somebody's work in progress, and replacing it with the last
// release would throw that work away.
func isRelease(v string) bool {
	if _, ok := parseVersion(v); !ok {
		return false
	}
	return describeSuffix.FindString(v) == ""
}

// compare orders two versions: negative when a is older than b. A pre-release
// comes before the release it leads up to.
func compare(a, b semver) int {
	for _, d := range []int{a.major - b.major, a.minor - b.minor, a.patch - b.patch} {
		if d != 0 {
			return d
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	}
	return strings.Compare(a.pre, b.pre)
}

// newer reports whether version a is newer than version b. Anything that does
// not parse is never newer.
func newer(a, b string) bool {
	va, ok := parseVersion(a)
	if !ok {
		return false
	}
	vb, ok := parseVersion(b)
	if !ok {
		return false
	}
	return compare(va, vb) > 0
}
