# Changelog

Each version's section becomes its release notes on GitHub, and is what a
running panel shows an admin before installing the update. Newest first;
`make release` refuses a tag that has no section here.

## v2.1.0

### Added

- **Updates from the panel.** Settings has an Updates section listing every
  release newer than the one running, with its changelog, and an Install button.
  The new binary is checked against the release's published checksums and run
  once to prove it works on this machine before anything changes; then the
  database is backed up to `wgui.db.before-<version>`, the running binary is kept
  as `wgui.previous`, and wgui restarts into the new version. Connected peers
  stay connected.
- **Update notices.** Each server checks for new releases shortly after starting
  and every six hours after that. Admins see a banner when one is out, which can
  be dismissed until the next release.

### Upgrading

This is the first version that can update itself, so getting to it is still
done by hand: replace the binary and restart, as described in the README. Every
update after this one can be installed from the panel. Each server updates
itself, so update the master and each node from its own panel.

## v2.0.0

The rewrite: one Go binary with an embedded SQLite database replaces the
MongoDB-backed version. See the README for everything it does, and for
migrating from the old version with `--import-peers` and `--import-groups`.
