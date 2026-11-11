# Changelog

Each version's section becomes its release notes on GitHub, and is what a
running panel shows an admin before installing the update. Newest first;
`make release` refuses a tag that has no section here.

## v2.4.0

### Added

- **`/list` as a table.** Telegram added rich messages in June 2026, which can hold
  a table. Switch on *Send /list as a table* under Settings → Telegram bot and a
  group's members go in a two-column table (name, used) under the group's figures,
  with a divider between subscriptions, in one message instead of several. The
  column headings are two more messages you can write in your customers' language,
  and the message is sent right-to-left when your wording is.
- **It is off by default, and falls back safely.** What an older Telegram app shows
  for a rich message is not documented, so try it on your own phone first. If
  Telegram refuses one, the plain list is sent instead, and a list too large for
  one rich message goes as plain text from the start. Names are escaped, and not
  turned into mentions, hashtags or links; the cost is that a link in the `/list`
  message is not tappable when it is a table.

### Changed

- **`/list` shows a group's whole figures, and every member.** A member used to see
  how much its own phone had used next to how much the group had left. It now sees
  what the group has spent in all, what it has left and when it ends, and under that
  a line for each member of the group — including members on other chats — with
  what each has used, adding up to the group's total. Members of one group on the
  same chat are listed once, with their names together. A member now sees the names
  and use of everyone in its group.
- A group with more members than fit in one message is carried over between
  members, never through one.

### Upgrading

Nothing to do: the database is unchanged, and the new settings keep their defaults
on a server whose settings were saved by an earlier version, so tables stay off
until you turn them on. Only the master runs the bot, so update it first.

## v2.3.0

### Added

- **Telegram warnings.** A bot, run by wgui itself, that messages a customer
  before their data or time runs out. Make a bot with @BotFather and paste its
  token into Settings → Telegram bot; wgui checks it with Telegram before keeping
  it, and the token is never shown again or copied to nodes. An admin connects a
  subscription from its page with **Get link**: the customer opens the link in
  Telegram, once, within seven days.
- **Two warnings, each sent once per situation.** One when a subscription has
  this many days left (1 by default), one when it has this much data left (1 GiB
  by default); 0 turns either off. Renewing, resetting usage or reconnecting a
  chat arms them again, and a restart does not repeat what was sent. Peers that
  are already cut off or switched off by hand are not warned, and a group's
  members are warned about the group's limits.
- **`/list`.** A customer can ask the bot where each of their subscriptions
  stands: what they have used, what is left, and when it ends. For a group
  member, used is their own share and left is what the group has left.
- **Wording you control.** Everything the bot says — the confirmation, the
  answer to a bad link, its help, both warnings and both halves of `/list` — is
  plain text with `{placeholders}`, in whatever language your customers read.
- Disconnecting is open to whoever may change a peer, and to the customer for
  their own; a customer who blocks the bot is disconnected automatically.

### Changed

- **Distributors get Peers and Groups only.** The dashboard, and the server pages
  an admin uses, are no longer offered to them, and typing one of their addresses
  goes to Peers. Customers still get just their own page.
- Only admins can make a Telegram link, since it decides whose phone a
  subscription's warnings reach.

### Upgrading

The database upgrades itself on start. Nothing changes until a bot token is set,
and the bot runs on the master only, so update the master first; nodes need
nothing from it and carry Telegram requests to the master on their own. The
master must be able to reach `api.telegram.org` (`HTTPS_PROXY` is honoured).

If you used the old wgui Telegram bot with the same token, stop it first:
Telegram lets only one program read a bot's messages. Connections made through
the old bot are not carried over; each subscription needs a new link.

## v2.2.0

### Added

- **Peers by owner.** The Peers page has an *Owner* picker next to the status,
  group and role filters, listing everyone who owns a peer with how many, plus
  *No owner* for peers nobody is responsible for. Clicking a name in the Owner
  column picks that owner too, and the choice is kept in the link. Admins only;
  a distributor's list is already everything it owns.
- **Sort by owner.** Sorting on the Owner column groups each owner's peers
  together, owners in name order and unowned peers last.

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
