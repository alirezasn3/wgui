// Package web carries the built frontend so that wgui ships as a single binary.
package web

import (
	"embed"
	"io/fs"
)

// build holds the output of `npm run build`. The directory is kept in the
// repository with a placeholder so the Go build works before the frontend has
// been built.
//
//go:embed all:build
var build embed.FS

// Assets returns the build directory rooted at its top level.
func Assets() (fs.FS, error) {
	return fs.Sub(build, "build")
}
