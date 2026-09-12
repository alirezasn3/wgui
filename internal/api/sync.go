package api

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"wgui/internal/config"
	"wgui/internal/store"
	"wgui/internal/version"

	"github.com/labstack/echo/v4"
)

// SyncRequest is what a node sends its master on every tick: who it is and
// everything it has counted since it was last reset.
//
// The counts are absolute rather than a delta, so a request that is late,
// repeated or lost entirely costs nothing — the next one carries the whole
// truth again.
type SyncRequest struct {
	ServerID string `json:"serverId"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	// PeerCount is how many peers the node is serving, which it knows and the
	// master would otherwise have to guess at from the usage rows — a number
	// that counts only the peers with traffic.
	PeerCount int64 `json:"peerCount"`
	// What the node is doing right now, so the master's dashboard can show the
	// fleet without asking each server in turn.
	OnlinePeers int64              `json:"onlinePeers"`
	TXSpeed     int64              `json:"txSpeed"`
	RXSpeed     int64              `json:"rxSpeed"`
	Recent      store.UsageWindows `json:"recent"`
	// TrafficResetApplied is the master's last request to reset the node's
	// traffic counters that the node has carried out. Until it catches up
	// with the request, the figures in Recent predate the reset.
	TrafficResetApplied int64               `json:"trafficResetApplied"`
	TakenAt             int64               `json:"takenAt"`
	Usage               []store.ServerUsage `json:"usage"`
}

// SyncResponse is the master's reply: the peers to serve, and what every other
// server has counted, so the node measures a peer against the same total the
// master does.
type SyncResponse struct {
	ServerID string `json:"serverId"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	// What the master is carrying right now, so a node's dashboard can show
	// the server it follows as fully as the master shows its nodes.
	PeerCount   int64              `json:"peerCount"`
	OnlinePeers int64              `json:"onlinePeers"`
	TXSpeed     int64              `json:"txSpeed"`
	RXSpeed     int64              `json:"rxSpeed"`
	Recent      store.UsageWindows `json:"recent"`
	TakenAt     int64              `json:"takenAt"`
	Peers       []*store.Peer      `json:"peers"`
	Groups      []*store.Group     `json:"groups"`
	Visibility  []store.Grant      `json:"visibility"`
	Scripts     []*store.Script    `json:"scripts"`
	Settings    *config.Settings   `json:"settings,omitempty"`
	Usage       []store.UsageRow   `json:"usage"`
	// TrafficResetAt asks the node to reset its traffic counters. It is sent
	// with every sync until the node reports it done, and the node acts on each
	// distinct request once.
	TrafficResetAt int64 `json:"trafficResetAt,omitempty"`
}

// sync answers a node. It is the one endpoint that is not authenticated by the
// caller's tunnel address: a node is another server, not a peer, so it proves
// itself with the master's sync secret instead.
func (s *Server) sync(c echo.Context) error {
	secret, err := s.store.SyncSecret()
	if err != nil {
		return err
	}
	presented := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
	// Constant time, so a wrong secret cannot be found a character at a time.
	if subtle.ConstantTimeCompare([]byte(presented), []byte(secret)) != 1 {
		return fail(c, http.StatusUnauthorized, "the sync secret is missing or wrong")
	}

	var req SyncRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed sync request")
	}
	if req.ServerID == "" {
		return badRequest(c, "a node must say which server it is")
	}
	if req.ServerID == s.store.ServerID() {
		return badRequest(c, "a server cannot be its own node; the node is using a copied database")
	}

	if err := s.store.ReplaceServerUsage(req.ServerID, req.TakenAt, req.Usage); err != nil {
		return err
	}
	// A node that has not yet carried out a reset asked of it is still
	// reporting the figures from before it; showing them would undo the reset
	// on this dashboard until the node catches up.
	trafficReset, err := s.store.PendingTrafficReset(req.ServerID)
	if err != nil {
		return err
	}
	if trafficReset > req.TrafficResetApplied {
		req.Recent = store.UsageWindows{}
	}
	if err := s.store.SeeNode(store.Node{
		ID:          req.ServerID,
		Role:        "node",
		Name:        req.Name,
		Version:     req.Version,
		Address:     c.RealIP(),
		PeerCount:   req.PeerCount,
		OnlinePeers: req.OnlinePeers,
		TXSpeed:     req.TXSpeed,
		RXSpeed:     req.RXSpeed,
		Recent:      req.Recent,
	}); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	defs, err := s.store.AllDefinitions(now)
	if err != nil {
		return err
	}
	usage, err := s.store.UsageExcept(req.ServerID)
	if err != nil {
		return err
	}
	counts, err := s.store.CountPeers(store.Scope{Role: "admin"}, now)
	if err != nil {
		return err
	}
	live := s.engine.Totals()
	return c.JSON(http.StatusOK, SyncResponse{
		ServerID:    s.store.ServerID(),
		Name:        s.LocalName(),
		Version:     version.String(),
		PeerCount:   int64(counts.Total),
		OnlinePeers: int64(counts.Online),
		TXSpeed:     live.TXSpeed,
		RXSpeed:     live.RXSpeed,
		Recent:      s.recentUsage(),
		TakenAt:     now,
		Peers:       defs.Peers,
		Groups:      defs.Groups,
		Visibility:  defs.Visibility,
		Scripts:     defs.Scripts,
		Settings:    defs.Settings,
		Usage:       usage,

		TrafficResetAt: trafficReset,
	})
}

// listNodes shows every other wgui this one knows about, and this one itself,
// so a dashboard can put the fleet side by side.
func (s *Server) listNodes(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	nodes, err := s.store.Nodes()
	if err != nil {
		return err
	}

	// This server is not in the table — nothing syncs it to itself — so its own
	// row is built fresh, which also keeps its counts live rather than as of
	// the last time anything was written.
	counts, err := s.store.CountPeers(store.Scope{Role: "admin"}, nowMS())
	if err != nil {
		return err
	}
	live := s.engine.Totals()
	local := store.Node{
		ID:          s.store.ServerID(),
		Role:        "local",
		Name:        s.LocalName(),
		Version:     version.String(),
		PeerCount:   int64(counts.Total),
		Online:      true,
		LastSeenAt:  nowMS(),
		TXSpeed:     live.TXSpeed,
		RXSpeed:     live.RXSpeed,
		OnlinePeers: int64(counts.Online),
		Recent:      s.recentUsage(),
	}
	return c.JSON(http.StatusOK, echo.Map{"nodes": append([]store.Node{local}, nodes...)})
}

// forgetNode drops a node and the usage it had contributed.
func (s *Server) forgetNode(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	if err := s.store.ForgetNode(c.Param("id")); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// getSyncSecret hands the master's sync key to an admin, who needs it to set up
// a node and can read it out of the database anyway.
func (s *Server) getSyncSecret(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	secret, err := s.store.SyncSecret()
	if err != nil {
		return err
	}
	// The fingerprint travels with the secret because they are needed together:
	// a node has to trust this certificate as well as authenticate to it, and a
	// self-signed certificate is one no authority will vouch for.
	return c.JSON(http.StatusOK, echo.Map{
		"secret":      secret,
		"fingerprint": s.certFingerprintValue(),
	})
}

// rotateSyncSecret replaces the sync key. Every node is locked out until it is
// given the new one, which is the point.
func (s *Server) rotateSyncSecret(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	secret, err := s.store.RotateSyncSecret()
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, echo.Map{
		"secret":      secret,
		"fingerprint": s.certFingerprintValue(),
	})
}

// peerPresence reports every server that has carried this peer, so an operator
// looking at one panel can tell which server the peer is actually using.
func (s *Server) peerPresence(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if peer.ID != cl.PeerID && !s.canModifyPeer(cl, peer) && !cl.Bypass {
		return forbidden(c)
	}

	rows, err := s.store.PeerPresence(peer.ID, nowMS())
	if err != nil {
		return err
	}
	// The local server's live speed is known; a remote one's is not, and saying
	// so is better than showing a zero that looks like silence.
	local := s.store.ServerID()
	for i := range rows {
		if rows[i].ServerID == local {
			rows[i].ServerName = s.LocalName()
		} else if rows[i].ServerName == "" {
			rows[i].ServerName = rows[i].ServerID[:min(8, len(rows[i].ServerID))]
		}
	}
	return c.JSON(http.StatusOK, echo.Map{"presence": rows})
}

// LocalName is what this server calls itself on another server's dashboard.
//
// The public address is the right answer when one is set: it is the name peers
// actually reach this box at. Failing that the machine's hostname, which is what
// an operator calls it. The tunnel address is the last resort and a poor one —
// peers carry fixed tunnel addresses, so every server in a fleet necessarily
// shares the same one, and a fleet of servers all called 10.0.0.1 tells you
// nothing.
func (s *Server) LocalName() string {
	if a := s.Settings().PublicAddress; a != "" {
		return a
	}
	if h := hostname(); h != "" {
		return h
	}
	return s.cfg.InterfaceAddress
}

// hostname is read once: it cannot change under a running process in any way
// that matters here, and this is called on every sync.
var hostname = sync.OnceValue(func() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	// Some hosts report a trailing dot or a bare "localhost", neither of which
	// distinguishes one server from another.
	h = strings.TrimSuffix(strings.TrimSpace(h), ".")
	if h == "localhost" || h == "localhost.localdomain" {
		return ""
	}
	return h
})

// recentUsage is what this server carried over the recent past, reported to the
// other servers so a dashboard can show the fleet without asking each in turn.
func (s *Server) recentUsage() store.UsageWindows {
	w, err := s.store.TrafficWindows(nowMS())
	if err != nil {
		s.log.Warn("reading recent traffic failed", "error", err)
	}
	return w
}

type trafficResetRequest struct {
	// ServerIDs picks whose counters to reset: this server's own id and, on a
	// master, any of its nodes. Empty means this server alone.
	ServerIDs []string `json:"serverIds"`
}

// resetTraffic starts traffic counters again from zero. This server's are
// cleared at once; a node's are cleared when it next syncs, which is the only
// time a master can reach it.
//
// It is never forwarded: a node's counters are its own record of what crossed
// its device, so a node resets them itself and leaves the rest of the fleet to
// the master.
func (s *Server) resetTraffic(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}
	var req trafficResetRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}

	self := s.store.ServerID()
	local := len(req.ServerIDs) == 0
	var nodes []string
	for _, id := range req.ServerIDs {
		if id == self {
			local = true
			continue
		}
		nodes = append(nodes, id)
	}

	if len(nodes) > 0 {
		if s.cfg.Master.IsNode() {
			return badRequest(c, "a node can only reset its own counters; reset the other servers from the master")
		}
		if err := s.store.RequestTrafficReset(nodes, nowMS()); err != nil {
			return s.internalError(c, "requesting a traffic reset", err)
		}
	}
	if local {
		if err := s.store.ResetTraffic(); err != nil {
			return s.internalError(c, "resetting traffic", err)
		}
	}
	s.log.Info("traffic counters reset", "thisServer", local, "nodes", len(nodes), "by", cl.Name)
	return c.NoContent(http.StatusNoContent)
}
