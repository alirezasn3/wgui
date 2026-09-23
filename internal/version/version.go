// Package version carries the build identity, stamped in at link time.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// These are set with -ldflags at build time; see the Makefile. The defaults are
// what a plain `go build` or `go run` produces.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
	// Repo is the GitHub repository releases are published to, which is where
	// the panel looks for updates. A fork that publishes its own releases sets
	// it at link time.
	Repo = "alirezasn3/wgui"
	// ReleaseAPI is the GitHub API that Repo's releases are read from.
	ReleaseAPI = "https://api.github.com"
)

// String is the one-line identity printed by --version.
func String() string {
	return fmt.Sprintf("wgui %s (%s, %s/%s)", Version, commit(), runtime.GOOS, runtime.GOARCH)
}

// Short is just the version, for the panel's footer.
func Short() string { return Version }

// commit falls back to the VCS stamp the Go toolchain embeds, so a binary built
// without the Makefile still says where it came from.
func commit() string {
	if Commit != "" {
		return short(Commit)
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return short(s.Value)
		}
	}
	return "unknown"
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
