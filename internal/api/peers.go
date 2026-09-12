package api

import (
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"wgui/internal/config"
	"wgui/internal/ipinfo"
	"wgui/internal/netutil"
	"wgui/internal/store"
	"wgui/internal/wgdev"

	"github.com/labstack/echo/v4"
)

// allocMu serialises address allocation so two simultaneous creates do not race
// for the same address and burn retries on a unique-constraint failure.
var allocMu sync.Mutex

type peerListResponse struct {
	Peers    []*store.Peer `json:"peers"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
}

// localiseSeenOn blanks the "seen on" name when the server that saw the peer
// last is this one. The panel should only point elsewhere when there is
// somewhere else to point, and on a single server there never is.
func (s *Server) localiseSeenOn(peers []*store.Peer) {
	local := s.store.ServerID()
	for _, p := range peers {
		if p.SeenOnID == local || p.SeenOnID == "" {
			p.SeenOn, p.SeenOnID = "", ""
		}
	}
}

func (s *Server) listPeers(c echo.Context) error {
	cl := caller(c)

	filter := store.PeerFilter{
		Scope:    cl.Scope(),
		Search:   c.QueryParam("search"),
		Status:   c.QueryParam("status"),
		Role:     c.QueryParam("role"),
		OwnerID:  c.QueryParam("owner"),
		Sort:     c.QueryParam("sort"),
		Desc:     c.QueryParam("order") == "desc",
		Page:     queryInt(c, "page", 1),
		PageSize: queryInt(c, "pageSize", 50),
		Now:      nowMS(),
	}
	if filter.PageSize > 500 {
		filter.PageSize = 500
	}
	if g := c.QueryParam("group"); g != "" {
		id, err := strconv.ParseInt(g, 10, 64)
		if err != nil {
			return badRequest(c, "group must be a number, or -1 for peers with no group")
		}
		filter.GroupID = &id
	}

	// Live speed is never stored, so it cannot be sorted in SQL. Fetch every
	// match instead and sort the page in memory.
	sortBySpeed := filter.Sort == "speed"
	page, pageSize := filter.Page, filter.PageSize
	if sortBySpeed {
		filter.PageSize = 0
	}

	peers, total, err := s.store.ListPeers(filter)
	if err != nil {
		return s.internalError(c, "listing peers", err)
	}
	s.engine.Decorate(peers)
	s.localiseSeenOn(peers)

	if sortBySpeed {
		sort.SliceStable(peers, func(i, j int) bool {
			a := peers[i].TXSpeed + peers[i].RXSpeed
			b := peers[j].TXSpeed + peers[j].RXSpeed
			if a == b {
				return peers[i].Name < peers[j].Name
			}
			if filter.Desc {
				return a > b
			}
			return a < b
		})
		peers = paginate(peers, page, pageSize)
	}

	for _, p := range peers {
		redactPeer(p, cl, s.canModifyPeer(cl, p))
	}

	return c.JSON(http.StatusOK, peerListResponse{
		Peers: peers, Total: total, Page: page, PageSize: pageSize,
	})
}

func paginate(peers []*store.Peer, page, pageSize int) []*store.Peer {
	if pageSize <= 0 {
		return peers
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start >= len(peers) {
		return []*store.Peer{}
	}
	end := min(start+pageSize, len(peers))
	return peers[start:end]
}

// loadPeer fetches a peer and checks the caller is allowed to see it.
func (s *Server) loadPeer(c echo.Context) (*store.Peer, *Caller, error) {
	cl := caller(c)
	id, err := pathID(c)
	if err != nil {
		return nil, cl, badRequest(c, "malformed peer id")
	}

	peer, err := s.store.GetPeer(id, nowMS())
	if errors.Is(err, store.ErrNotFound) {
		return nil, cl, notFound(c)
	}
	if err != nil {
		return nil, cl, s.internalError(c, "loading peer", err)
	}

	visible, err := s.store.CanSee(cl.Scope(), peer.ID)
	if err != nil {
		return nil, cl, s.internalError(c, "checking access", err)
	}
	// Report an invisible peer as missing rather than forbidden, so the panel
	// does not confirm the existence of peers the caller may not see.
	if !visible && !cl.Bypass {
		return nil, cl, notFound(c)
	}
	return peer, cl, nil
}

func (s *Server) getPeer(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	s.engine.Decorate([]*store.Peer{peer})
	s.localiseSeenOn([]*store.Peer{peer})
	redactPeer(peer, cl, s.canModifyPeer(cl, peer))
	return c.JSON(http.StatusOK, peer)
}

func (s *Server) getPeerConfig(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	// The configuration contains the private key, so it follows the same rule.
	if peer.ID != cl.PeerID && !s.canModifyPeer(cl, peer) && !cl.Bypass {
		return forbidden(c)
	}
	return c.String(http.StatusOK, renderConfig(peer, s.Settings(), s.serverPublicKey()))
}

func (s *Server) getPeerIPInfo(c echo.Context) error {
	peer, _, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	s.engine.Decorate([]*store.Peer{peer})
	s.localiseSeenOn([]*store.Peer{peer})

	host := ipinfo.HostOf(peer.LastEndpoint)
	if host == "" {
		return fail(c, http.StatusNotFound, "this peer has never connected, so it has no endpoint to look up")
	}

	info, err := s.ipinfo.Lookup(c.Request().Context(), host)
	switch {
	case errors.Is(err, ipinfo.ErrDisabled):
		return fail(c, http.StatusServiceUnavailable, "ip information lookups are turned off in settings")
	case errors.Is(err, ipinfo.ErrNotPublic):
		return fail(c, http.StatusUnprocessableEntity, "%s is not a public address", host)
	case err != nil:
		s.log.Warn("ip lookup failed", "ip", host, "error", err)
		return fail(c, http.StatusBadGateway, "could not reach the ip information service")
	}
	return c.JSON(http.StatusOK, info)
}

type createPeerRequest struct {
	Name              string  `json:"name"`
	Role              *string `json:"role"`
	OwnerID           *string `json:"ownerId"`
	GroupID           *int64  `json:"groupId"`
	AllowedUsage      *int64  `json:"allowedUsage"` // bytes, 0 = unlimited
	ExpiresAt         *int64  `json:"expiresAt"`    // unix ms, 0 = never
	ExpiryDays        *int    `json:"expiryDays"`   // convenience, wins over ExpiresAt
	ClientEndpoint    *string `json:"clientEndpoint"`
	PreferredEndpoint *string `json:"preferredEndpoint"`
	Note              *string `json:"note"`
}

func (s *Server) createPeer(c echo.Context) error {
	cl := caller(c)
	if !cl.CanWrite() {
		return forbidden(c)
	}

	var req createPeerRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return badRequest(c, "name is required")
	}

	settings := s.Settings()
	defaults := settings.PeerDefaults

	peer := &store.Peer{
		Name:         req.Name,
		Role:         defaults.Role,
		AllowedUsage: defaults.AllowedUsageBytes,
		ExpiresAt:    expiryFromDays(defaults.ExpiryDays),
	}

	if req.AllowedUsage != nil {
		peer.AllowedUsage = max(*req.AllowedUsage, 0)
	}
	switch {
	case req.ExpiryDays != nil:
		peer.ExpiresAt = expiryFromDays(*req.ExpiryDays)
	case req.ExpiresAt != nil:
		peer.ExpiresAt = max(*req.ExpiresAt, 0)
	}
	if req.ClientEndpoint != nil {
		peer.ClientEndpoint = strings.TrimSpace(*req.ClientEndpoint)
	}
	if req.PreferredEndpoint != nil {
		peer.PreferredEndpoint = strings.TrimSpace(*req.PreferredEndpoint)
	}
	if req.Note != nil {
		peer.Note = *req.Note
	}
	if req.GroupID != nil {
		peer.GroupID = *req.GroupID
	}

	// Only an admin decides roles and ownership. Anything a distributor creates
	// is a plain user that belongs to that distributor.
	if cl.IsAdmin() {
		if req.Role != nil {
			if !config.ValidRole(*req.Role) {
				return badRequest(c, "role must be admin, distributor or user")
			}
			peer.Role = *req.Role
		}
		if req.OwnerID != nil {
			peer.OwnerID = *req.OwnerID
		}
	} else {
		peer.Role = config.RoleUser
		peer.OwnerID = cl.PeerID
	}

	if peer.GroupID != 0 {
		if err := s.assertGroupWritable(cl, peer.GroupID); err != nil {
			return err.render(c)
		}
	}

	privateKey, publicKey, err := wgdev.GenerateKeypair()
	if err != nil {
		return s.internalError(c, "generating keys", err)
	}
	peer.PrivateKey, peer.PublicKey, peer.ID = privateKey, publicKey, publicKey

	if err := s.allocateAndInsert(peer); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "a peer named %q already exists", peer.Name)
		}
		if errors.Is(err, netutil.ErrPoolExhausted) {
			return fail(c, http.StatusConflict, "the interface subnet has no free addresses left")
		}
		return s.internalError(c, "creating peer", err)
	}

	s.log.Info("peer created", "peer", peer.Name, "by", cl.Name)
	s.engine.Kick()

	created, err := s.store.GetPeer(peer.ID, nowMS())
	if err != nil {
		return s.internalError(c, "reloading peer", err)
	}
	return c.JSON(http.StatusCreated, created)
}

// allocateAndInsert picks the next free tunnel address and inserts the peer,
// retrying if another request took the address first.
func (s *Server) allocateAndInsert(peer *store.Peer) error {
	subnet, err := netip.ParsePrefix(s.cfg.InterfaceAddressCIDR)
	if err != nil {
		return err
	}
	server, err := netip.ParseAddr(s.cfg.InterfaceAddress)
	if err != nil {
		return err
	}

	allocMu.Lock()
	defer allocMu.Unlock()

	for attempt := 0; attempt < 5; attempt++ {
		used, err := s.store.UsedAllowedIPs()
		if err != nil {
			return err
		}
		addr, err := netutil.NextFreeAddress(subnet, server, used)
		if err != nil {
			return err
		}
		peer.AllowedIPs = addr

		err = s.store.CreatePeer(peer)
		if err == nil {
			return nil
		}
		if !errors.Is(err, store.ErrDuplicate) {
			return err
		}
		// The name is the other unique column; if it is the clash, retrying
		// with a new address will not help.
		if taken, lookupErr := s.nameTaken(peer.Name); lookupErr == nil && taken {
			return store.ErrDuplicate
		}
	}
	return store.ErrDuplicate
}

func (s *Server) nameTaken(name string) (bool, error) {
	peers, _, err := s.store.ListPeers(store.PeerFilter{
		Scope: store.Scope{Role: config.RoleAdmin}, Search: name, Now: nowMS(),
	})
	if err != nil {
		return false, err
	}
	for _, p := range peers {
		if p.Name == name {
			return true, nil
		}
	}
	return false, nil
}

type patchPeerRequest struct {
	Name              *string `json:"name"`
	Role              *string `json:"role"`
	OwnerID           *string `json:"ownerId"`
	GroupID           *int64  `json:"groupId"`
	AllowedUsage      *int64  `json:"allowedUsage"`
	ExpiresAt         *int64  `json:"expiresAt"`
	ExpiryDays        *int    `json:"expiryDays"`
	ClientEndpoint    *string `json:"clientEndpoint"`
	PreferredEndpoint *string `json:"preferredEndpoint"`
	ManuallyDisabled  *bool   `json:"manuallyDisabled"`
	Note              *string `json:"note"`
	ResetUsage        bool    `json:"resetUsage"`
}

// toPatch validates a request against what the caller is allowed to change.
func (s *Server) toPatch(req patchPeerRequest, cl *Caller) (store.PeerPatch, *httpError) {
	var patch store.PeerPatch

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return patch, errBadRequest("name must not be empty")
		}
		patch.Name = &name
	}
	if req.Role != nil {
		if !cl.IsAdmin() {
			return patch, errForbidden("only an admin can change roles")
		}
		if !config.ValidRole(*req.Role) {
			return patch, errBadRequest("role must be admin, distributor or user")
		}
		patch.Role = req.Role
	}
	if req.OwnerID != nil {
		if !cl.IsAdmin() {
			return patch, errForbidden("only an admin can reassign ownership")
		}
		patch.OwnerID = req.OwnerID
	}
	if req.GroupID != nil {
		patch.GroupID = req.GroupID
	}
	if req.AllowedUsage != nil {
		v := max(*req.AllowedUsage, 0)
		patch.AllowedUsage = &v
	}
	switch {
	case req.ExpiryDays != nil:
		v := expiryFromDays(*req.ExpiryDays)
		patch.ExpiresAt = &v
	case req.ExpiresAt != nil:
		v := max(*req.ExpiresAt, 0)
		patch.ExpiresAt = &v
	}
	if req.ClientEndpoint != nil {
		v := strings.TrimSpace(*req.ClientEndpoint)
		patch.ClientEndpoint = &v
	}
	if req.PreferredEndpoint != nil {
		v := strings.TrimSpace(*req.PreferredEndpoint)
		patch.PreferredEndpoint = &v
	}
	patch.ManuallyDisabled = req.ManuallyDisabled
	patch.Note = req.Note
	patch.ResetUsage = req.ResetUsage
	return patch, nil
}

func (s *Server) patchPeer(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if !s.canModifyPeer(cl, peer) {
		return forbidden(c)
	}

	var req patchPeerRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}

	patch, herr := s.toPatch(req, cl)
	if herr != nil {
		return herr.render(c)
	}
	// Switching off the peer you are connected through would drop the tunnel
	// carrying this request, and the panel only answers from inside it.
	if peer.ID == cl.PeerID && patch.ManuallyDisabled != nil && *patch.ManuallyDisabled {
		return badRequest(c, "you cannot disable the peer you are connected as")
	}
	if patch.GroupID != nil && *patch.GroupID != 0 {
		if herr := s.assertGroupWritable(cl, *patch.GroupID); herr != nil {
			return herr.render(c)
		}
	}

	if _, err := s.store.UpdatePeers([]string{peer.ID}, patch); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "that name is already taken")
		}
		return s.internalError(c, "updating peer", err)
	}

	s.log.Info("peer updated", "peer", peer.Name, "by", cl.Name)
	s.engine.Kick()

	updated, err := s.store.GetPeer(peer.ID, nowMS())
	if err != nil {
		return s.internalError(c, "reloading peer", err)
	}
	s.engine.Decorate([]*store.Peer{updated})
	return c.JSON(http.StatusOK, updated)
}

func (s *Server) deletePeer(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if !s.canModifyPeer(cl, peer) {
		return forbidden(c)
	}
	if peer.ID == cl.PeerID {
		return badRequest(c, "you cannot delete the peer you are connected as")
	}

	if _, err := s.store.DeletePeers([]string{peer.ID}); err != nil {
		return s.internalError(c, "deleting peer", err)
	}

	s.log.Info("peer deleted", "peer", peer.Name, "by", cl.Name)
	s.engine.Kick()
	return c.NoContent(http.StatusNoContent)
}

// -- bulk -------------------------------------------------------------------

type bulkPeerRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"`

	AllowedUsage *int64  `json:"allowedUsage"`
	ExpiresAt    *int64  `json:"expiresAt"`
	ExpiryDays   *int    `json:"expiryDays"`
	GroupID      *int64  `json:"groupId"`
	Role         *string `json:"role"`
}

func (s *Server) bulkPeers(c echo.Context) error {
	cl := caller(c)
	if !cl.CanWrite() {
		return forbidden(c)
	}

	var req bulkPeerRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	if len(req.IDs) == 0 {
		return badRequest(c, "select at least one peer")
	}

	// Verify every target up front so a bulk action is all-or-nothing rather
	// than applying to the prefix of the selection the caller happens to own.
	targets := make([]*store.Peer, 0, len(req.IDs))
	for _, id := range req.IDs {
		peer, err := s.store.GetPeer(id, nowMS())
		if errors.Is(err, store.ErrNotFound) {
			return fail(c, http.StatusNotFound, "peer %s no longer exists", id)
		}
		if err != nil {
			return s.internalError(c, "loading peers", err)
		}
		if !s.canModifyPeer(cl, peer) {
			return fail(c, http.StatusForbidden, "you are not allowed to change %q", peer.Name)
		}
		targets = append(targets, peer)
	}

	if req.Action == "delete" {
		for _, p := range targets {
			if p.ID == cl.PeerID {
				return badRequest(c, "you cannot delete the peer you are connected as")
			}
		}
		n, err := s.store.DeletePeers(req.IDs)
		if err != nil {
			return s.internalError(c, "deleting peers", err)
		}
		s.log.Info("peers deleted in bulk", "count", n, "by", cl.Name)
		s.engine.Kick()
		return c.JSON(http.StatusOK, map[string]any{"affected": n})
	}

	patch, herr := s.bulkPatch(req, cl)
	if herr != nil {
		return herr.render(c)
	}
	if patch.ManuallyDisabled != nil && *patch.ManuallyDisabled {
		for _, p := range targets {
			if p.ID == cl.PeerID {
				return badRequest(c, "you cannot disable the peer you are connected as")
			}
		}
	}
	if patch.GroupID != nil && *patch.GroupID != 0 {
		if herr := s.assertGroupWritable(cl, *patch.GroupID); herr != nil {
			return herr.render(c)
		}
	}

	n, err := s.store.UpdatePeers(req.IDs, patch)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "that change would give two peers the same name")
		}
		return s.internalError(c, "updating peers", err)
	}

	s.log.Info("peers updated in bulk", "action", req.Action, "count", n, "by", cl.Name)
	s.engine.Kick()
	return c.JSON(http.StatusOK, map[string]any{"affected": n})
}

// bulkPatch translates a bulk action into the patch it stands for.
func (s *Server) bulkPatch(req bulkPeerRequest, cl *Caller) (store.PeerPatch, *httpError) {
	var patch store.PeerPatch
	yes, no := true, false

	switch req.Action {
	case "enable":
		patch.ManuallyDisabled = &no
	case "disable":
		patch.ManuallyDisabled = &yes
	case "resetUsage":
		patch.ResetUsage = true
	case "setUsage":
		if req.AllowedUsage == nil {
			return patch, errBadRequest("allowedUsage is required for setUsage")
		}
		v := max(*req.AllowedUsage, 0)
		patch.AllowedUsage = &v
	case "setExpiry":
		switch {
		case req.ExpiryDays != nil:
			v := expiryFromDays(*req.ExpiryDays)
			patch.ExpiresAt = &v
		case req.ExpiresAt != nil:
			v := max(*req.ExpiresAt, 0)
			patch.ExpiresAt = &v
		default:
			return patch, errBadRequest("expiresAt or expiryDays is required for setExpiry")
		}
	case "setGroup":
		if req.GroupID == nil {
			return patch, errBadRequest("groupId is required for setGroup, use 0 to remove the group")
		}
		patch.GroupID = req.GroupID
	case "setRole":
		if !cl.IsAdmin() {
			return patch, errForbidden("only an admin can change roles")
		}
		if req.Role == nil || !config.ValidRole(*req.Role) {
			return patch, errBadRequest("role must be admin, distributor or user")
		}
		patch.Role = req.Role
	default:
		return patch, errBadRequest("unknown action %q", req.Action)
	}
	return patch, nil
}

// -- sharing ----------------------------------------------------------------

func (s *Server) getVisibility(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if peer.ID != cl.PeerID && !s.canModifyPeer(cl, peer) {
		return forbidden(c)
	}

	ids, err := s.store.Visibility(peer.ID)
	if err != nil {
		return s.internalError(c, "loading shared peers", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"targets": ids})
}

type visibilityRequest struct {
	Targets []string `json:"targets"`
}

// putVisibility replaces which peers a user is allowed to see. Only an admin or
// the user's own distributor may change it, and they may only share peers they
// can see themselves.
func (s *Server) putVisibility(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if !s.canModifyPeer(cl, peer) {
		return forbidden(c)
	}

	var req visibilityRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}

	for _, target := range req.Targets {
		ok, err := s.store.CanSee(cl.Scope(), target)
		if err != nil {
			return s.internalError(c, "checking access", err)
		}
		if !ok {
			return fail(c, http.StatusForbidden, "you cannot share a peer you cannot see")
		}
	}

	if err := s.store.SetVisibility(peer.ID, req.Targets); err != nil {
		return s.internalError(c, "saving shared peers", err)
	}
	s.log.Info("sharing updated", "peer", peer.Name, "targets", len(req.Targets), "by", cl.Name)
	return c.NoContent(http.StatusNoContent)
}
