package api

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"wgui/internal/config"
	"wgui/internal/store"

	"github.com/labstack/echo/v4"
)

// callerKey is where the authenticated peer is stashed on the request context.
const callerKey = "caller"

// Caller is whoever made the request. The panel has no passwords: a request is
// trusted because it arrived over the tunnel from an address that belongs to a
// known peer.
type Caller struct {
	PeerID string
	Name   string
	Role   string
	Bypass bool // authenticated by the out-of-band bypass key instead
}

func (c *Caller) IsAdmin() bool { return c.Role == config.RoleAdmin }

// CanWrite reports whether the caller may create or change anything at all.
func (c *Caller) CanWrite() bool {
	return c.Role == config.RoleAdmin || c.Role == config.RoleDistributor
}

func (c *Caller) Scope() store.Scope {
	return store.Scope{PeerID: c.PeerID, Role: c.Role}
}

func caller(c echo.Context) *Caller {
	v, _ := c.Get(callerKey).(*Caller)
	return v
}

// authenticate resolves the requesting peer from the source address of the
// request, or from the configured bypass key.
func (s *Server) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()

		// The bypass key is for out-of-band administration (scripts, recovery)
		// and is only honoured when one has actually been configured.
		if key := req.Header.Get("bypass_key"); key != "" && s.cfg.BypassKey != "" && key == s.cfg.BypassKey {
			c.Set(callerKey, &Caller{Name: "bypass", Role: config.RoleAdmin, Bypass: true})
			return next(c)
		}

		// A node forwarding one of its operators' writes. The node proves itself
		// with the sync secret and names whose request it is carrying; the role
		// that peer actually holds is read here, so a node cannot hand its
		// operators authority they do not have on the master.
		if onBehalf := req.Header.Get(OnBehalfHeader); onBehalf != "" {
			caller, err := s.callerForForwarded(c, onBehalf)
			if err != nil {
				return err
			}
			c.Set(callerKey, caller)
			return next(c)
		}

		host, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			host = req.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !s.cfg.DeviceCIDR().Contains(ip) {
			s.log.Warn("rejected request from outside the tunnel", "remote", req.RemoteAddr, "path", req.URL.Path)
			return fail(c, http.StatusForbidden, "requests must come from inside the tunnel")
		}

		peer, err := s.store.GetPeerByAllowedIP(ip.String()+"/32", nowMS())
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("no peer owns the requesting address", "remote", ip.String())
			return fail(c, http.StatusForbidden, "no peer is registered for your address")
		}
		if err != nil {
			return s.internalError(c, "authentication", err)
		}

		c.Set(callerKey, &Caller{PeerID: peer.ID, Name: peer.Name, Role: peer.Role})
		return next(c)
	}
}

// canModifyPeer reports whether the caller may change the given peer. Seeing a
// peer is not enough: a distributor may only change what it owns.
func (s *Server) canModifyPeer(cl *Caller, target *store.Peer) bool {
	switch cl.Role {
	case config.RoleAdmin:
		return true
	case config.RoleDistributor:
		// A distributor manages the plain users it owns. Ownership of an admin
		// or of another distributor — which an imported ownership row can imply
		// — must never grant power over them.
		return target.OwnerID == cl.PeerID && target.Role == config.RoleUser
	default:
		return false
	}
}

func (s *Server) canModifyGroup(cl *Caller, target *store.Group) bool {
	switch cl.Role {
	case config.RoleAdmin:
		return true
	case config.RoleDistributor:
		return target.OwnerID == cl.PeerID
	default:
		return false
	}
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// OnBehalfHeader names the peer whose request a node is forwarding to its
// master. It is only honoured alongside the sync secret.
const OnBehalfHeader = "X-WGUI-On-Behalf-Of"

// forwardedKey marks a request that reached this server through a node, so a
// handler can tell the difference where it matters.
const forwardedKey = "wgui.forwarded"

// callerForForwarded authenticates a node's forwarded request and resolves it to
// the peer it was made by.
func (s *Server) callerForForwarded(c echo.Context, peerID string) (*Caller, error) {
	secret, err := s.store.SyncSecret()
	if err != nil {
		return nil, err
	}
	presented := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(presented), []byte(secret)) != 1 {
		return nil, fail(c, http.StatusUnauthorized, "the sync secret is missing or wrong")
	}

	peer, err := s.store.GetPeer(peerID, nowMS())
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(c, http.StatusForbidden, "the peer this request was made by is not known here")
	}
	if err != nil {
		return nil, s.internalError(c, "authentication", err)
	}
	c.Set(forwardedKey, true)
	return &Caller{PeerID: peer.ID, Name: peer.Name, Role: peer.Role}, nil
}

// forwarded reports whether this request reached the server through a node.
func forwarded(c echo.Context) bool {
	v, _ := c.Get(forwardedKey).(bool)
	return v
}
