// Package mongomig imports peers and groups exported from the MongoDB database
// used by the previous version of wgui. It runs once, from the command line, and
// is the only code left that knows the old schema.
//
// It reads mongoexport output rather than connecting to MongoDB, so migrating
// does not require the old database to still be running, and wgui does not carry
// a MongoDB driver it would otherwise never use.
package mongomig

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"wgui/internal/config"
	"wgui/internal/store"
)

type Options struct {
	Store *store.Store
	// PeersFile and GroupsFile are mongoexport output. GroupsFile may be empty
	// when the old deployment never used groups.
	PeersFile  string
	GroupsFile string
	Force      bool
	Log        *slog.Logger
}

// srcPeer is the old peers document. Server-specific info and the Telegram chat
// id are present in the export but not carried over.
type srcPeer struct {
	ID                extString `json:"_id"`
	Role              string    `json:"role"`
	Name              string    `json:"name"`
	PreferredEndpoint string    `json:"preferredEndpoint"`
	AllowedIPs        string    `json:"allowedIPs"`
	PublicKey         string    `json:"publicKey"`
	PrivateKey        string    `json:"privateKey"`
	Disabled          extBool   `json:"disabled"`
	AllowedUsage      extInt64  `json:"allowedUsage"`
	ExpiresAt         extInt64  `json:"expiresAt"`
	TotalTX           extInt64  `json:"totalTX"`
	TotalRX           extInt64  `json:"totalRX"`
	GroupID           extString `json:"groupID"`
}

// srcGroup is the old groups document. Its totalTX/totalRX are ignored, because
// group usage is now summed from the members instead of being stored.
type srcGroup struct {
	ID           extString `json:"_id"`
	Name         string    `json:"name"`
	PeerIDs      []string  `json:"peerIDs"`
	AllowedUsage extInt64  `json:"allowedUsage"`
	ExpiresAt    extInt64  `json:"expiresAt"`
	OwnerID      string    `json:"ownerID"`
}

// zeroObjectID is what the old code stored for "no group".
const zeroObjectID = "000000000000000000000000"

func Run(opt Options) error {
	if opt.PeersFile == "" {
		return errors.New("no peers export given: pass --peers <file> (see mongoexport in the README)")
	}

	empty, err := opt.Store.IsEmpty()
	if err != nil {
		return err
	}
	if !empty && !opt.Force {
		return errors.New("the SQLite database already has peers; re-run with --force to import anyway")
	}

	peers, err := readFile[srcPeer](opt.PeersFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", opt.PeersFile, err)
	}

	var groups []srcGroup
	if opt.GroupsFile != "" {
		if groups, err = readFile[srcGroup](opt.GroupsFile); err != nil {
			return fmt.Errorf("read %s: %w", opt.GroupsFile, err)
		}
	}
	opt.Log.Info("read export", "peers", len(peers), "groups", len(groups))

	// Membership used to live on the group document as a list of peer ids; the
	// new schema keeps it on the peer. The peer's own groupID is used as a
	// fallback for exports where the two disagree.
	groupOf := map[string]string{}
	for _, g := range groups {
		for _, id := range g.PeerIDs {
			groupOf[id] = g.ID.String()
		}
	}
	for _, p := range peers {
		id := p.GroupID.String()
		if id == "" || id == zeroObjectID {
			continue
		}
		if _, ok := groupOf[p.ID.String()]; !ok {
			groupOf[p.ID.String()] = id
		}
	}

	// Order matters, because the new schema has real foreign keys where the old
	// one had conventions: a group points at the peer that owns it, a peer
	// points at its owner and its group. So every peer is inserted first with
	// neither link set, then the groups, then the links are filled in.
	imported, err := importPeers(opt, peers)
	if err != nil {
		return err
	}
	newGroupIDs, err := importGroups(opt, groups, peers)
	if err != nil {
		return err
	}
	if err := linkPeers(opt, peers, imported, groupOf, newGroupIDs); err != nil {
		return err
	}

	opt.Log.Info("import finished", "peers", len(peers), "groups", len(newGroupIDs))
	if len(newGroupIDs) > 0 {
		opt.Log.Info("group members were given an unlimited individual allowance, because a group's allowance now applies to the group as a whole")
	}
	return nil
}

// readFile decodes a mongoexport file, which is either a JSON array or one
// document per line.
func readFile[T any](path string) ([]T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var out []T
	err = decodeDocuments(data, func(raw json.RawMessage) error {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	return out, err
}

// importGroups writes the groups and returns the mapping from the old ObjectID
// to the new integer primary key.
func importGroups(opt Options, groups []srcGroup, peers []srcPeer) (map[string]int64, error) {
	owners, _ := ownerByPrefix(peers)
	ids := map[string]int64{}

	// Which peers exist, so a group never points at one that was skipped.
	known := map[string]bool{}
	for _, p := range peers {
		if p.PublicKey != "" {
			known[p.PublicKey] = true
		}
	}

	for _, g := range groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			opt.Log.Warn("skipped a group with no name", "id", g.ID.String())
			continue
		}
		row := &store.Group{
			Name:         name,
			OwnerID:      g.OwnerID,
			AllowedUsage: max(g.AllowedUsage.Int64(), 0),
			ExpiresAt:    max(g.ExpiresAt.Int64(), 0),
		}
		// An owner recorded on the old document wins; otherwise fall back to
		// the naming convention the old panel used for scoping. Either way the
		// owner has to be a peer that actually made it in, or the foreign key
		// would reject the whole group.
		if row.OwnerID == "" {
			row.OwnerID = owners[prefixOf(name)]
		}
		if row.OwnerID != "" && !known[row.OwnerID] {
			opt.Log.Warn("group owner was not imported, leaving the group to the admins",
				"group", name, "owner", row.OwnerID)
			row.OwnerID = ""
		}
		if err := opt.Store.CreateGroup(row); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				opt.Log.Warn("skipped a duplicate group name", "name", name)
				continue
			}
			return nil, fmt.Errorf("import group %q: %w", name, err)
		}
		ids[g.ID.String()] = row.ID
	}
	return ids, nil
}

// importPeers inserts every peer with no owner and no group, and reports which
// ones made it in so the linking pass can skip the rest.
func importPeers(opt Options, peers []srcPeer) (map[string]bool, error) {
	imported := map[string]bool{}

	for _, p := range peers {
		name := strings.TrimSpace(p.Name)
		if name == "" || p.PublicKey == "" || p.AllowedIPs == "" {
			opt.Log.Warn("skipped an incomplete peer", "id", p.ID.String(), "name", p.Name)
			continue
		}

		role := p.Role
		if !config.ValidRole(role) {
			role = config.RoleUser
		}

		row := &store.Peer{
			ID:                p.PublicKey,
			Name:              name,
			Role:              role,
			PublicKey:         p.PublicKey,
			PrivateKey:        p.PrivateKey,
			AllowedIPs:        p.AllowedIPs,
			PreferredEndpoint: p.PreferredEndpoint,
			AllowedUsage:      max(p.AllowedUsage.Int64(), 0),
			ExpiresAt:         max(p.ExpiresAt.Int64(), 0),
		}

		// The old schema conflated "switched off by hand" with "out of quota or
		// expired". Only the manual flag is stored now, and it is inferred: a
		// peer that was disabled while still inside its limits must have been
		// switched off deliberately.
		if bool(p.Disabled) && !exhausted(p) {
			row.ManuallyDisabled = true
		}

		if err := opt.Store.CreatePeer(row); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				opt.Log.Warn("skipped a peer whose name or address is already taken",
					"name", name, "address", p.AllowedIPs)
				continue
			}
			return nil, fmt.Errorf("import peer %q: %w", name, err)
		}
		imported[p.PublicKey] = true

		if p.TotalTX > 0 || p.TotalRX > 0 {
			if err := opt.Store.ApplyUsageDeltas([]store.UsageDelta{
				{PeerID: row.ID, TX: p.TotalTX.Int64(), RX: p.TotalRX.Int64()},
			}); err != nil {
				return nil, fmt.Errorf("import usage for %q: %w", name, err)
			}
		}
	}
	return imported, nil
}

// linkPeers fills in ownership and group membership, now that every row both
// sides of those references exists.
func linkPeers(opt Options, peers []srcPeer, imported map[string]bool, groupOf map[string]string, newGroupIDs map[string]int64) error {
	owners, ambiguous := ownerByPrefix(peers)
	if len(ambiguous) > 0 {
		opt.Log.Warn("some name prefixes are claimed by more than one distributor, so their peers were left without an owner; set it on the peer in the panel",
			"prefixes", strings.Join(ambiguous, ", "))
	}

	for _, p := range peers {
		if !imported[p.PublicKey] {
			continue
		}

		role := p.Role
		if !config.ValidRole(role) {
			role = config.RoleUser
		}

		var patch store.PeerPatch

		// Ownership used to be implied by the "prefix-name" convention. This is
		// the last time that convention is consulted; from here on it is a real
		// column.
		//
		// Only plain users are handed over. A name prefix is shared by whoever
		// happens to be named that way, so an admin or a second distributor can
		// sit under a distributor's prefix, and handing one of those over would
		// quietly give the distributor power over an account above it.
		if role == config.RoleUser {
			if owner, ok := owners[prefixOf(strings.TrimSpace(p.Name))]; ok && owner != p.PublicKey && imported[owner] {
				patch.OwnerID = &owner
			}
		}

		if oldGroup, ok := groupOf[p.ID.String()]; ok {
			if newID, ok := newGroupIDs[oldGroup]; ok {
				patch.GroupID = &newID
				// The old code copied the group's allowance onto every member,
				// so keeping it would limit them twice over.
				zero := int64(0)
				patch.AllowedUsage = &zero
			}
		}

		if patch.OwnerID == nil && patch.GroupID == nil {
			continue
		}
		if _, err := opt.Store.UpdatePeers([]string{p.PublicKey}, patch); err != nil {
			return fmt.Errorf("link peer %q: %w", p.Name, err)
		}
	}
	return nil
}

// exhausted reports whether the old peer was out of quota or past its expiry, in
// which case its disabled flag was set by the enforcement loop rather than by
// hand.
func exhausted(p srcPeer) bool {
	usage := p.TotalTX.Int64() + p.TotalRX.Int64()
	if quota := p.AllowedUsage.Int64(); quota > 0 && usage >= quota {
		return true
	}
	if expiry := p.ExpiresAt.Int64(); expiry > 0 && expiry <= nowMS() {
		return true
	}
	return false
}

// ownerByPrefix maps the old "Prefix-name" convention to the distributor that
// owned that prefix.
//
// A prefix claimed by more than one distributor is dropped rather than resolved
// arbitrarily: picking one would hand somebody else's customers to the wrong
// reseller, and ownership is now an explicit field an admin can set in a moment.
// The ambiguous prefixes are returned so they can be reported.
func ownerByPrefix(peers []srcPeer) (owners map[string]string, ambiguous []string) {
	claims := map[string][]string{}
	for _, p := range peers {
		if p.Role != config.RoleDistributor || p.PublicKey == "" {
			continue
		}
		if prefix := prefixOf(strings.TrimSpace(p.Name)); prefix != "" {
			claims[prefix] = append(claims[prefix], p.PublicKey)
		}
	}

	owners = map[string]string{}
	for prefix, keys := range claims {
		if len(keys) > 1 {
			ambiguous = append(ambiguous, prefix)
			continue
		}
		owners[prefix] = keys[0]
	}
	sort.Strings(ambiguous)
	return owners, ambiguous
}

func nowMS() int64 { return time.Now().UnixMilli() }

func prefixOf(name string) string {
	prefix, _, found := strings.Cut(name, "-")
	if !found {
		return ""
	}
	return prefix
}
