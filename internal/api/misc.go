package api

import (
	"net/http"

	"wgui/internal/config"
	"wgui/internal/engine"
	"wgui/internal/store"
	"wgui/internal/version"

	"github.com/labstack/echo/v4"
)

type sessionResponse struct {
	PeerID      string              `json:"peerId"`
	Name        string              `json:"name"`
	Role        string              `json:"role"`
	CanWrite    bool                `json:"canWrite"`
	IsAdmin     bool                `json:"isAdmin"`
	Endpoints   []string            `json:"endpoints"`
	SortFields  []string            `json:"sortFields"`
	ServerKey   string              `json:"serverPublicKey"`
	Version     string              `json:"version"`
	PeerDefault config.PeerDefaults `json:"peerDefaults"`
	// QR travels with the session because every peer, admin or not, renders its
	// own code and needs to know how it should look.
	QR config.QRSettings `json:"qr"`
	// IsNode says this server takes its peers from a master, so anything edited
	// here is replaced at the next sync. The panel says so rather than letting
	// an operator wonder where their change went.
	IsNode    bool   `json:"isNode"`
	MasterURL string `json:"masterUrl"`
}

// getSession tells the frontend who it is talking to and what it may do, so the
// UI can hide what the API would refuse anyway.
func (s *Server) getSession(c echo.Context) error {
	cl := caller(c)
	settings := s.Settings()

	return c.JSON(http.StatusOK, sessionResponse{
		PeerID:      cl.PeerID,
		Name:        cl.Name,
		Role:        cl.Role,
		CanWrite:    cl.CanWrite(),
		IsAdmin:     cl.IsAdmin(),
		Endpoints:   settings.Endpoints,
		SortFields:  store.SortableFields(),
		ServerKey:   s.serverPublicKey(),
		Version:     version.Short(),
		PeerDefault: settings.PeerDefaults,
		QR:          settings.QR,
		IsNode:      s.cfg.Master.IsNode(),
		MasterURL:   s.cfg.Master.URL,
	})
}

type statusResponse struct {
	Interface  string        `json:"interface"`
	ListenPort int           `json:"listenPort"`
	PublicKey  string        `json:"publicKey"`
	Peers      statusCounts  `json:"peers"`
	Live       engine.Totals `json:"live"`
	Usage      int64         `json:"usage"`
	Groups     int           `json:"groups"`
	// Recent is what this server carried over the last hour, day, week and
	// month.
	Recent store.UsageWindows `json:"recent"`
}

type statusCounts struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Disabled int `json:"disabled"`
	Expired  int `json:"expired"`
	Quota    int `json:"quota"`
	// Online counts peers connected to this server. OnlineAnywhere counts those
	// connected to any server in the fleet — on a node the two differ, and a
	// node's dashboard should say how busy the node is.
	Online         int `json:"online"`
	OnlineAnywhere int `json:"onlineAnywhere"`
}

// getStatus feeds the dashboard cards. It counts within the caller's scope, so a
// distributor sees totals for its own peers rather than the whole server.
func (s *Server) getStatus(c echo.Context) error {
	cl := caller(c)

	counts, err := s.store.CountPeers(cl.Scope(), nowMS())
	if err != nil {
		return s.internalError(c, "loading status", err)
	}

	res := statusResponse{
		Peers: statusCounts{
			Total:          counts.Total,
			Active:         counts.Active,
			Disabled:       counts.Disabled,
			Expired:        counts.Expired,
			Quota:          counts.Quota,
			Online:         counts.Online,
			OnlineAnywhere: counts.OnlineAnywhere,
		},
		Usage: counts.Usage,
		Live:  s.engine.Totals(),
	}
	// Live totals come from the whole device; scope the online count to what
	// this caller can actually see so the two numbers agree.
	res.Live.Online = counts.Online

	if info := s.engine.Info(); info != nil {
		res.Interface, res.ListenPort, res.PublicKey = info.Name, info.ListenPort, info.PublicKey
	}
	if groups, err := s.store.CountGroups(cl.Scope()); err == nil {
		res.Groups = groups
	}
	res.Recent = s.recentUsage()

	return c.JSON(http.StatusOK, res)
}

func (s *Server) getSettings(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	return c.JSON(http.StatusOK, s.Settings())
}

func (s *Server) putSettings(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}

	var incoming config.Settings
	if err := c.Bind(&incoming); err != nil {
		return badRequest(c, "malformed request body")
	}
	incoming.Normalize()

	// Settings are split. Two of them describe the box a server is — its public
	// address and which endpoint its own configurations point at — and belong
	// to whichever server is being edited. The rest is policy that every server
	// in a fleet should agree on, and the master holds it.
	current, err := s.store.LoadSettings()
	if err != nil {
		return s.internalError(c, "loading settings", err)
	}

	switch {
	case s.cfg.Master.IsNode():
		// Keep what is this node's own, and send the shared half upstream:
		// written here it would only stand until the next sync replaced it.
		shared := incoming
		if err := s.store.SaveSettings(incoming.AdoptShared(current)); err != nil {
			return s.internalError(c, "saving settings", err)
		}
		if err := s.forwardSettings(c, shared); err != nil {
			return err
		}

	case forwarded(c):
		// A node's operator editing the shared half. Its idea of a public
		// address is its own, not this server's.
		if err := s.store.SaveSettings(current.AdoptShared(incoming)); err != nil {
			return s.internalError(c, "saving settings", err)
		}

	default:
		if err := s.store.SaveSettings(incoming); err != nil {
			return s.internalError(c, "saving settings", err)
		}
	}

	// Reload rather than trusting the request body, so what the UI shows is what
	// was actually stored.
	saved, err := s.store.LoadSettings()
	if err != nil {
		return s.internalError(c, "reloading settings", err)
	}
	s.setSettings(saved)

	s.log.Info("settings updated", "by", cl.Name)
	return c.JSON(http.StatusOK, saved)
}
