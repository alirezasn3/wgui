//go:build updatee2e

package main

// Built only into the end-to-end test of updating in place, never into a
// release. Releases are Linux binaries and the updater refuses anything else;
// this lets a development machine stand in for a Linux server, so the real
// download, swap, shutdown and restart can be exercised without one. Nothing
// about those steps differs between the two.
func init() { updatePlatform = "linux" }
