# WGUI

A web panel for managing WireGuard peers: quotas, expiry, groups, and live
traffic, served by a single Go binary with an embedded SQLite database.

The panel has no login screen. It answers only requests that arrive **through
the tunnel**, and the source address identifies the peer making them — so being
able to reach the panel at all already proves you hold a valid key.

## Features

- **Peers** — create, edit, disable and delete; each gets a key pair and tunnel
  address automatically, with a labelled QR code and a `.conf` to download or
  share.
- **Quotas and expiry** — per peer or shared across a group, with unlimited as a
  first-class option on both.
- **Groups** — one allowance and expiry covering several peers. Usage is summed
  from the members, so the two can never drift apart.
- **Bulk editing** — select any number of peers and set expiry, set allowance,
  enable, disable, reset usage, move between groups, change role, or delete.
  Each action is all-or-nothing.
- **Live traffic** — current up/down rate per peer, read from the interface each
  second and never written to the database. The panel refreshes once a second;
  responses are compressed, so a page of fifty peers costs about 7 KB.
- **Search, filter, sort** — by name, address or public key; by status, group,
  role or owner; sorted on any column including live speed.
- **Endpoint lookups** — ISP, organisation and ASN behind a peer's address, on
  demand and cached, through any service that answers on a URL, with an optional
  API key.
- **Roles** — admins see everything, distributors manage the peers they own, and
  users see only themselves plus whatever has been shared with them.
- **Server tuning and inspection** — IP forwarding and BBR are buttons; the
  dashboard shows the machine's firewall and routing rules.
- **Automation** — save scripts and run them from the panel, and have a monitor
  run one when a destination stops answering.
- **Several servers** — nodes take their peers from a master over one HTTPS
  exchange and count usage toward the same allowances, and any panel can show
  where each peer is connected.
- **Server traffic** — what each server carried over the last hour, day, week
  and month, with a reset.

## Installing

wgui runs on Linux and ships as a single binary with everything inside it — the
web interface, the database engine, the TLS certificate generator. There is
nothing else to install: no Go, no Node, no database server, no web server.

### From a release

Download the latest release for this machine's architecture, check it against
the published checksums, and put it in place:

```bash
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
BASE=https://github.com/alirezasn3/wgui/releases/latest/download

curl -fsSLO $BASE/wgui-linux-$ARCH
curl -fsSLO $BASE/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS

sudo install -Dm755 wgui-linux-$ARCH /opt/wgui/wgui
```

Binaries are built for `amd64` and `arm64`. To pin a particular version instead
of the latest, use `BASE=https://github.com/alirezasn3/wgui/releases/download/v2.0.0`;
every release is listed on the
[releases page](https://github.com/alirezasn3/wgui/releases).

Write `/opt/wgui/config.json` next to it — at minimum the interface name and the
subnet it hands addresses out of:

```json
{
  "dbPath": "wgui.db",
  "interfaceName": "wg0",
  "interfaceAddress": "10.0.0.1",
  "interfaceAddressCIDR": "10.0.0.0/24",
  "listenAddress": "0.0.0.0:443"
}
```

Then install the service and start it:

```bash
cd /opt/wgui
sudo ./wgui --install
sudo journalctl -u wgui -f
```

`--install` writes `/etc/systemd/system/wgui.service`, reloads systemd, enables
the unit so it comes back after a reboot, and starts it. Run it again whenever
you want: an existing unit is replaced rather than left alone, which is how you
repair an installation made by an earlier version or point it at a binary that
has moved.

wgui runs as root because it configures the WireGuard interface and can change
kernel settings from the panel.

### First run

Starting on an empty database, wgui creates the database, generates a TLS
certificate into it, creates an admin peer, and writes that peer's configuration
to `/opt/wgui/admin.conf`.

Copy `admin.conf` to your own machine, import it into any WireGuard client,
bring the tunnel up, and open the panel at the server's tunnel address —
`https://10.0.0.1` for the config above. The certificate is self-signed, so the
browser warns once.

That file is the only way in, so keep a copy. Then set the public address and
endpoints on the Settings page, and download a fresh configuration for yourself
from the panel.

### Upgrading

Download the new binary the same way, replace the old one and restart. The
database, certificate and settings are left alone, and the schema migrates
itself on start.

```bash
sudo install -Dm755 wgui-linux-$ARCH /opt/wgui/wgui
sudo systemctl restart wgui
```

Upgrade a master and its nodes together: servers exchange usage, resets and
traffic figures with each other, and an older server on one end does not
understand the newer parts of that exchange.

The only files wgui puts on disk are the database and `admin.conf` on first run.

`./wgui --version` says what is installed. `./wgui --uninstall` stops the
service, unlinks it from its boot target and removes the unit; it does not touch
the database.

If you installed with a version before this one, run `sudo ./wgui --install`
once. The unit it wrote had an empty `WantedBy`, so `systemctl enable` had no
target to link it into and the panel did not start after a reboot.

## Building from source

Needs Go 1.26+ and Node 20+. One command sets everything up and produces a
Linux binary:

```bash
make setup
```

That installs the frontend dependencies, builds the web interface, and links it
into `wgui-linux-amd64`. Afterwards `make linux` rebuilds, and
`make linux ARCH=arm64` targets 64-bit ARM. `make dist` builds every
architecture into `dist/` with checksums, which is what a release contains.

### Developing

`--fake-wg` swaps the WireGuard interface for an in-memory one that fabricates
peers and traffic, so the whole panel runs on a machine that has no WireGuard:

```bash
make dev
```

Authentication is based on the source address of the request, so point the
subnet at loopback to make your own browser count as a peer:

```json
{
  "interfaceAddress": "127.0.0.254",
  "interfaceAddressCIDR": "127.0.0.0/24",
  "listenAddress": "127.0.0.1:8080"
}
```

The first peer is then created at `127.0.0.1/32`, which is where the browser
connects from. `make dev` also passes `--dev-http`, so there is no certificate
warning in the way.

For frontend work, `npm run dev` in `web/` serves the UI with hot reload and
proxies `/api` to `http://127.0.0.1:8080` (override with `WGUI_API`).

```bash
make test   # Go tests and the frontend type check
```

### Releasing

Versions are `vMAJOR.MINOR.PATCH` and live in git tags; the version, commit and
build date are linked into the binary and shown by `--version` and in the
panel's sidebar. Tagging is what publishes a release:

```bash
make release TAG=v2.1.0
```

That checks the tag looks like a version and the working tree is clean, then
tags and pushes. Pushing the tag runs `.github/workflows/release.yml`, which
tests, builds both architectures, and attaches them to a GitHub release along
with `SHA256SUMS`. Builds from an untagged commit are versioned by
`git describe`, so a binary can always be traced back to its source.

## Configuration

`config.json` sits next to the binary and holds only what has to be known before
the database opens. Everything else is edited in the panel's Settings page and
takes effect without a restart.

```json
{
  "dbPath": "wgui.db",
  "interfaceName": "wg0",
  "interfaceAddress": "10.0.0.1",
  "interfaceAddressCIDR": "10.0.0.0/24",
  "listenAddress": "0.0.0.0:443",
  "tls": { "cert": "", "key": "", "ephemeral": false },
  "bypassKey": "a-long-random-string"
}
```

| Key | Meaning |
| --- | --- |
| `dbPath` | SQLite file, relative to the binary |
| `interfaceName` | The WireGuard interface to manage |
| `interfaceAddress` | The server's own address inside the tunnel |
| `interfaceAddressCIDR` | The subnet peers are allocated from |
| `listenAddress` | Where the panel listens |
| `tls` | Your certificate and key. Leave empty and wgui generates a self-signed one. Nothing is written to disk either way — see below. |
| `bypassKey` | Optional. Sent as a `bypass_key` header to reach the API from outside the tunnel, for scripts and recovery. Leave it out to disable that path entirely. |

Copy `config.example.json` to `config.json` to start from.

### Certificates

The panel always serves HTTPS, and **no certificate or key is ever written to
disk**.

Point `tls.cert` and `tls.key` at your own certificate if you have one; those
files are read into memory at startup and nothing else is touched. With both
left empty, wgui generates a long-lived self-signed certificate covering the
interface address and the public address, and keeps it in the database next to
the peer keys that are already there. Browsers warn about it the first time;
after that the exception sticks, because the certificate is reused on every
start. Its SHA-256 fingerprint is logged at startup so you can check what your
browser is being shown.

Set `tls.ephemeral` to `true` to generate a throwaway certificate on every start
instead, keeping nothing at all — switching to it also discards any certificate
already stored. The cost is that the fingerprint changes at every restart, so
every browser warns again after each upgrade or crash-restart, and any exception
that was accepted is void.

### Settings (edited in the panel)

Public address and the list of endpoints; the default endpoint; defaults for new
peers (allowance, expiry, role, DNS, allowed IPs, MTU, keepalive); defaults for
new groups; the endpoint-lookup service and cache lifetime; and how often usage
is written to disk.

The settings page also has buttons for the two kernel settings a WireGuard
server usually needs:

- **IP forwarding** — without it the tunnel comes up but no traffic reaches the
  internet.
- **BBR congestion control** — usually improves throughput over long-distance
  links, and selects the `fq` queueing discipline that BBR expects.

Both are written to the running kernel and to `/etc/sysctl.d`, so they survive a
reboot. They need wgui to be running as root; the buttons say so when it is not.

### QR codes and configurations

A peer's page has its configuration as a QR code and as a file, each with a
download and — where the browser supports it — a share button that hands the
image or the `.conf` to the operating system's share sheet.

The code is drawn with the peer's name above it and its tunnel address below,
so a screenshot still says who it belongs to, plus an optional caption line for
a support address. The image is always square, so it sits well wherever it is
shared. The colour is configurable; the default is the deep green the panel has
always used. Codes are always drawn on white whatever the panel's theme, because
a code on a dark background will not scan.

**Filenames are kept within what WireGuard accepts.** A tunnel is named after
the file it was imported from, and that name has to fit a network interface: at
most 15 characters from `[a-zA-Z0-9_=+.-]`. The clients reject anything else, so
a longer peer name is shortened for the download and the panel says what the
file was called. A shortened name carries a short digest of the original, so
`Johnson-Family-Mom` and `Johnson-Family-Dad` do not both become the same file.
Peer names themselves stay unrestricted.

### Usage, allowances and groups

A peer's usage bar shows what has been used, what the allowance is, the
percentage spent and what is left.

**A group governs its members outright.** Joining one hands the group the whole
say over data and time: its allowance and expiry *replace* the member's own
rather than competing with them, so a generous group is not held back by an old
quota on one of its peers, and a member whose own date has passed is not cut off
inside a live group. The member's own figures stay in the row untouched and
apply again the moment it leaves, which is why the two fields are read-only in
the editor while a group is selected. Disabling a peer by hand is not a limit,
so that switch still cuts it off on its own.

The bar then reads as **this peer's** share of the group's allowance — the
figure a customer asks about — while what is *left* counts the whole group's
spending, since that is what will cut the peer off. The rest of the group's
consumption is drawn behind the bar and named on the line below it, so a peer
that has used almost nothing still shows when the shared pot is nearly empty.
The expiry column names the group it came from in the same way.

Usage is changed in exactly two ways: **reset it**, which puts the counters back
to zero, or **change the allowance**, which is the total the peer or group is
entitled to. Remaining usage is shown, never edited — it is the difference
between the two. Resetting is available per peer, per group, and in bulk.

Expiry is entered as a number of **days from now**, and an allowance in **GiB**.
Neither is written back unless you actually change it. That matters because
neither field can round-trip its stored value: a day count cannot express a
deadline that has already passed, and bytes lose precision through a field with
two decimal places. Saving a dialog you only opened to rename something used to
zero an expired peer's expiry — zero is what the panel stores for "never" — and
shave a couple of hundred megabytes off its allowance. The line under the expiry
always names the deadline in force, so an expired peer can never be mistaken for
one that never expires, and tells you what a change will set it to.

Usage is counted **per server**. Each wgui writes only its own row in
`peer_usage`, and a peer's usage is the sum across every server carrying it, so
the same peer can be served by more than one box and still be held to one
allowance. Rows from other servers are absolute snapshots rather than deltas: a
sync that is late, repeated or missed entirely costs nothing. Resetting clears
every server's row and records when, so a server still reporting counters it
read before the reset cannot quietly restore them. Each server reports, with
its counts, the latest reset it had heard of when it read them; counts measured
from before a peer's latest reset are refused, and a server clears its own count
when it learns of a newer reset. A server that keeps counting right up to the
sync that tells it about the reset therefore cannot put the old number back.

Up and down are written from the **peer's** point of view, not the interface's:
what the server transmits is what the peer downloads.

Live figures — speed, usage, handshake age — change on every poll, so the peers
table uses a fixed column layout: the widths come from the widest value each
column can hold and nothing a peer does can reflow the rows around it. Any
traffic at all is shown, down to a single byte per second; only a genuinely idle
peer reads as a dash.

A row's group name opens that group, and so does the tag in the peer's own
dialog. A user that has been granted sight of other peers carries a small badge
with how many, next to its name. Admins also get an **Owner** column; clicking a
name there filters the list down to everything that distributor is responsible
for.

### If the database is damaged

wgui checks the database's indexes against its contents every time it starts,
and refuses to open one where they disagree. That state does not announce
itself — a stale index simply answers questions wrongly, and one of the
questions the panel asks is which tunnel addresses are still free, so the damage
compounds silently until two peers are handed the same address.

```bash
sudo systemctl stop wgui
sudo ./wgui --repair
sudo systemctl start wgui
```

`--repair` rebuilds every index from the table, which fixes the damage outright
when the table itself is sound. When it cannot, it says why: a unique index that
stopped rejecting duplicates leaves two real peers sharing a name or an address,
and which one to change is not a decision wgui can make. It names both and waits.

The usual cause is copying `wgui.db` while wgui is running. SQLite keeps recent
writes in a `wgui.db-wal` alongside it, so a copy of the one file is a mix of
pages from different moments. Stop wgui before copying, copy `-wal` and `-shm`
too, or use `sqlite3 wgui.db ".backup out.db"`, which is safe against a live
database. Better still, let a node sync instead of copying anything.

### Restarting

Restarting wgui does not disturb anyone who is connected. On start it reads what
the interface is already holding and leaves alone every peer the kernel has
exactly as the database wants it — writing a peer is what tears its handshake
down, so only what is actually wrong is written, and what the database no longer
knows about is removed. The log says how many of each.

### Several servers

One wgui can take its peers from another, so the same customers can be served
from more than one box and still be held to one allowance. The server that is
edited is the **master**; a server with a `master` block in its `config.json` is
a **node**.

```json
"master": {
  "url": "https://panel.example.com",
  "secret": "<the master's sync secret>",
  "fingerprint": "AB:CD:...",
  "intervalSeconds": 15
}
```

The node does all the talking. Every interval it sends the master everything it
has counted and receives the peers to serve plus what every other server has
counted, in one exchange. Nothing has to reach the node from outside: no inbound
port, no keepalive holding a path open, and a node behind NAT works like one
that is not.

The **Nodes** page shows this panel's certificate fingerprint next to the secret,
and puts both into the snippet, because a node needs them together: it has to
trust the certificate as well as authenticate to it.

The secret is generated on the master's first start and lives in `secrets`,
which no API returns and which is deliberately not replicated — a node's copy of
the database must not let it impersonate the master. Read it from the master
with `sqlite3 wgui.db "select cast(value as text) from secrets where key='sync_secret'"`.

A master usually serves the self-signed certificate wgui generated for it, which
no authority will vouch for. Set `fingerprint` to its SHA-256 and the node
verifies that instead, which also makes the name in the URL irrelevant — useful,
because a browser only warns about a name the certificate does not carry while a
node refuses outright. `"insecure": true` accepts any certificate and is a last
resort.

The generated certificate is named after every address the panel answers on: its
tunnel address, its public address, and each configured endpoint. If that set
changes, the stored certificate no longer covers it, so wgui reissues on the
next start rather than serving one that cannot match. The fingerprint changes
with it — a pinned node needs the new one.

A node creates no admin peer of its own and writes no `admin.conf`: it is given
the master's peers, the admin among them, within seconds of starting. Use the
master's `admin.conf` to reach either panel — the peer definitions are identical,
so the same tunnel works against both.

**A server that already had a database of its own keeps nothing.** The first
sync deletes whatever the master does not list, which for a server that had been
running alone is everything it had, so wgui moves the old file aside as
`wgui-standalone-<timestamp>.db` and starts from nothing. The data is still
there to look at; it is simply no longer in use. This happens once — a database
that has synced remembers which master it belongs to and is left alone from then
on.

Peers, groups, sharing grants, scripts and settings all come from the master.
A server names itself by its **public address** where one is set, since that is
what peers actually reach it at. Failing that it uses the machine's hostname.
The tunnel address is only a last resort and a poor one — peers carry fixed
tunnel addresses, so every server in a fleet necessarily has the same one, and a
list of servers all called `10.0.0.1` says nothing. Set the public address on
each server: it names it, and it is what its generated configurations point at.

Settings are the one thing that splits: `publicAddress` and `defaultEndpoint`
describe the box being edited and stay where they are written — copying them
would make every configuration downloaded from a node point at the master
instead — while the rest is policy and travels to the master like everything
else. Editing them on a node does both at once. The endpoint list itself is
shared, so a peer can be pointed at any server from any panel.

**Monitors do not replicate.** A monitor watches something from where it is
standing and runs a script when it stops answering, so it belongs to the server
it runs on; replicating one would have every node react to the master's view of
the network.

**The master is authoritative**, but a node's panel is not read-only. Anything
the master does not list is removed from the node, including peers a node
created before it was enrolled — so a write applied locally would only stand
until the next sync. Instead a node **forwards** it: creating, editing or
deleting a peer, a group or a script on a node sends the request to the master,
returns the master's answer, and pulls the change straight back down, so the
page you refresh is already showing what you just did.

Two things follow. The master must be reachable to edit anything on a node; when
it is not, the change is refused with the reason rather than accepted and lost.
And the master stays the only allocator of tunnel addresses, which is what keeps
two servers from ever handing out the same one.

A forwarded request travels as **the operator who made it**, not as the node, so
the master applies exactly the permissions that peer holds there — a distributor
editing on a node can change what it could change on the master, and no more.

What survives a sync is usage: peers are updated in place rather than recreated,
so a node's own counts are never lost.

Counts are absolute rather than deltas, so a sync that is late, repeated or
missed for an hour costs nothing; the next one carries the whole truth again. A
peer can overshoot its allowance by at most one interval's traffic, since both
servers only learn the combined total when they next speak.

The **Nodes** page on the master lists every node it has heard from, whether each
is still syncing, how many peers it serves, and the sync secret with the snippet
to paste into a new node's `config.json`. Forgetting a node there drops the usage
it contributed; it reappears on its own if it syncs again.

### Seeing the whole fleet

Usage is not the only thing servers exchange. Each one also reports when and
from where it last carried every peer, so any panel can answer where a peer
actually is rather than only that it is somewhere.

- The **Dashboard** gains a *Servers* section as soon as there is more than one:
  each server, its peer and online counts, how much it carried over the last
  hour, day, week and 30 days, and when it last synced. It appears on a node
  too, showing the master it follows.
- **Online now** counts peers connected to *this* server. Where the fleet has
  more connected than this server does, the card says so — a node should report
  how busy the node is.
- The status filter separates **online anywhere**, **online on this server** and
  **online on another server**, and a peer connected elsewhere says which server
  next to its status.
- The **peers table** names the server under the handshake when the peer was
  last seen somewhere other than the panel you are looking at.
- A **peer's dialog** lists every server that has carried it — which one is
  online with it now, when each last saw it, the address it connected from
  there, and how much each carried toward the one allowance.

A remote server's line is as fresh as its last sync, not as fresh as the panel's
own poll, and it says so. The peer row's "last handshake" means last seen
*anywhere*, which is what sorting on it should answer for a fleet.

### Server traffic

The dashboard shows what this server carried over the **last hour, 24 hours, 7
days and 30 days**. Each time usage is written, the bytes the interface carried
are also added to a bucket for the current minute, and a window is the sum of
the buckets inside it — exact to within a minute. Buckets older than 31 days are
dropped.

This is deliberately not worked out from the peers' usage. That total goes
*down* whenever a peer or group is reset or deleted and includes usage imported
from the old panel, so a difference between two readings of it measures neither.
Traffic is recorded as it crosses the interface and nothing done to the peers
afterwards changes it.

**Reset** starts the counters again from zero; peers' usage and quotas are not
affected. The traffic panel resets this server, each row of the *Servers* table
resets that server, and a master's **Reset all traffic** resets every server. A
master cannot reach its nodes, so a node resets when it next syncs, and until it
confirms, the master shows zero for it rather than the figures from before.

### Automation

The **Automation** page, admin-only, holds two related things.

**Scripts** are shell scripts saved in the database and run with a button. They
run as root, like everything else wgui does, with a fixed `PATH`, a clean
environment apart from `WGUI=1`, and nothing written to disk — the script is fed
to bash on standard input. Each has its own timeout, output is captured and
capped, the last twenty runs are kept, and a script cannot be started again
while the previous run is still going. A run outlives the request that started
it, so closing the browser does not kill it; a running script can be stopped.

**Monitors** watch a destination and run a script when it stops answering. A
target with a port (`example.com:443`) is checked by opening a TCP connection,
which tells you the service is up; anything else is checked with an ICMP ping.
Set how often to check, how long to wait, and how many failures in a row should
set the script off. By default it fires once per outage and stays quiet until
the target answers again, so a long outage does not run the script on every
tick.

ICMP needs the raw socket that running as root provides; failing that, wgui
falls back to the unprivileged datagram socket, which needs
`net.ipv4.ping_group_range` to include its group. Prefer a `host:port` target
where you can: plenty of hosts drop ping, and a monitor cannot tell that apart
from an outage.

### Network rules on the dashboard

The dashboard has a collapsible **Network rules** section showing what the
machine's firewall and routing look like: `iptables-save`, `ip6tables-save`,
`ip rule show`, `ip route show` and `ufw status verbose`, each in its own tab
with the command that produced it.

It is read-only — nothing there changes the system — and admin-only, since it
describes how the server is protected. Nothing is run until the section is
opened, and it is not part of the dashboard's poll, so an expensive
`iptables-save` happens only when somebody asks for it. Tools that are not
installed are reported as such rather than failing the rest. Each command is
given five seconds and its output capped, so neither a wedged tool nor an
enormous ruleset can tie up the panel.

## Migrating from the MongoDB version

wgui no longer speaks MongoDB at all. Export the two collections with
`mongoexport` and import the files:

```bash
mongoexport --uri mongodb://localhost:27017 --db wgui --collection peers  --out peers.json
mongoexport --uri mongodb://localhost:27017 --db wgui --collection groups --out groups.json

./wgui --import-peers peers.json --import-groups groups.json
```

Both the line-delimited default and `--jsonArray` output are accepted, as are
the Extended JSON wrappers (`$oid`, `$numberLong`, `$date`). `--import-groups`
can be left out if the old deployment never used groups. The import refuses to
run against a database that already holds peers unless you pass `--force`.

It carries over peers (keys, names, addresses, roles, allowances, expiry, usage
totals and pinned endpoints) and groups (names, allowances, expiry, membership).
Three things change on the way:

- **Ownership** is read once from the old `Prefix-name` convention and written
  to a real column. After that, renaming a peer no longer moves it between
  distributors. Only plain users are handed over: a prefix is shared by whoever
  is named that way, so an admin sitting under a distributor's prefix would
  otherwise end up owned — and therefore editable — by that distributor.
- **Group members lose their individual allowance.** The old code copied a
  group's allowance onto every member row; the new model derives it from the
  group, so keeping the copies would limit them twice over.
- **Disabled peers are sorted out.** The old schema used one flag for both
  "switched off by hand" and "out of quota". A peer that was disabled while
  still inside its limits is imported as manually disabled; one that had run out
  is not, so raising its allowance brings it straight back.

Logs and per-server info are not migrated.

### Warnings you may see

Neither of these loses data, and both are quick to resolve in the panel.

**`group owner was not imported, leaving the group to the admins`**

The group records an `ownerID` that no peer in the export has — usually an admin
or distributor that was deleted while their groups lived on. MongoDB let that
reference dangle; the new schema has a real foreign key, so it cannot store a
pointer to a peer that does not exist. Rather than refuse the whole import, wgui
drops the owner and says which group it was. The group, its allowance and all
its members come across intact; it simply has no owner, which means only admins
can manage it. Set an owner on the group in the panel if you want a distributor
to have it.

**`some name prefixes are claimed by more than one distributor`**

Two or more distributors are named with the same prefix, so "who owns `X-...`"
has no single answer. Guessing would hand one reseller's customers to another,
so those peers are imported with no owner instead. Set the owner on the affected
peers in the panel — the message lists the prefixes involved.

## How it works

One goroutine reconciles the database against the interface every second. It
reads WireGuard's cumulative counters, turns them into per-tick deltas, and uses
them for two things: live speed, kept in memory only, and usage totals,
accumulated and written to SQLite in one transaction every few seconds.

Each time usage is written it works out who should be reachable — a peer is cut
off when it is disabled by hand, past its expiry, or over its allowance, or when
its group is — and pushes only the peers whose state actually changed. On a node
this also happens the moment a sync arrives, so a peer switched off on the
master, or taken over its allowance by traffic on another server, is cut off
without waiting for the next write. Enforcement does not depend on the write
succeeding: a usage write that fails costs the traffic it could not record, but
expiry and the rest are still applied.

Cutting a peer off means giving the interface a preshared key the client does
not have, so the next handshake fails without losing the peer's address. A new
key alone is not enough, though: the kernel keeps honouring the session already
open until its keys age out, up to three minutes later. So a peer being switched
off is first removed from the interface, which ends its session, and then added
back with the new key. It is cut off within one usage write — ten seconds by
default — rather than minutes later.

Counters are seeded from the interface at startup, so restarting wgui does not
fold the existing totals into everyone's usage, and a counter that goes backwards
is treated as an interface restart rather than negative traffic.
