// Package api exposes the panel's HTTP interface: a JSON API under /api and the
// embedded frontend on everything else.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"wgui/internal/config"
	"wgui/internal/engine"
	"wgui/internal/ipinfo"
	"wgui/internal/monitor"
	"wgui/internal/scripts"
	"wgui/internal/store"
	"wgui/internal/update"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Server struct {
	store  *store.Store
	engine *engine.Engine
	runner *scripts.Runner
	cfg    *config.Config
	log    *slog.Logger
	ipinfo *ipinfo.Provider

	mu              sync.RWMutex
	settings        config.Settings
	certFingerprint string
	forwarder       Forwarder
	updater         *update.Updater
}

func New(st *store.Store, eng *engine.Engine, runner *scripts.Runner, cfg *config.Config, settings config.Settings, log *slog.Logger) *Server {
	s := &Server{store: st, engine: eng, runner: runner, cfg: cfg, settings: settings, log: log}
	s.ipinfo = ipinfo.New(st, func() config.IPInfoSettings { return s.Settings().IPInfo })
	return s
}

// Settings returns the current runtime settings.
func (s *Server) Settings() config.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Server) setSettings(v config.Settings) {
	s.mu.Lock()
	s.settings = v
	s.mu.Unlock()
}

// ReloadSettings re-reads the settings from the database. The panel keeps them
// in memory, so a change made by anything other than the settings endpoint —
// a sync from a master, for one — has to say so or the panel serves the copy it
// started with until it is restarted.
func (s *Server) ReloadSettings() error {
	v, err := s.store.LoadSettings()
	if err != nil {
		return err
	}
	s.setSettings(v)
	return nil
}

// FlushInterval is handed to the engine so a settings change takes effect
// without a restart.
func (s *Server) FlushInterval() time.Duration {
	return time.Duration(s.Settings().UsageFlushSeconds) * time.Second
}

// Handler builds the echo instance serving both the API and the frontend.
func (s *Server) Handler() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// A refusal has already written its own response; anything else is a
	// genuine failure and is left to echo to render.
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		if errors.Is(err, errRefused) || c.Response().Committed {
			return
		}
		e.DefaultHTTPErrorHandler(err, c)
	}

	e.Use(middleware.Recover())
	// The panel polls, and it does so over the tunnel. JSON compresses to a
	// fraction of its size, which matters far more than the CPU it costs.
	e.Use(middleware.GzipWithConfig(middleware.GzipConfig{
		Level:     5,
		MinLength: 1024,
	}))
	// The dev server runs on a different origin; credentials are not used, the
	// caller is identified by its tunnel address.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"http://localhost:5173"},
	}))

	// A node is another server, not a peer, so it proves itself with the sync
	// secret. That is why it is registered outside the group below, which
	// identifies callers by their tunnel address.
	e.POST("/api/sync", s.sync)
	// Anything but a POST here is a browser or a curl, not a node. It gets a
	// route of its own so it is told that: left to fall through, it would be
	// caught by the group below and refused for coming from outside the
	// tunnel, which is true of every node too and sends whoever is looking
	// after the wrong problem.
	e.Match([]string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions}, "/api/sync", syncWrongMethod)

	api := e.Group("/api", s.authenticate)

	api.GET("/session", s.getSession)
	api.GET("/peers/:id/presence", s.peerPresence)
	api.GET("/nodes", s.listNodes)
	api.DELETE("/nodes/:id", s.forgetNode)
	api.GET("/nodes/secret", s.getSyncSecret)
	api.POST("/nodes/secret", s.rotateSyncSecret)
	api.GET("/status", s.getStatus)
	api.POST("/traffic/reset", s.resetTraffic)
	// Each server updates itself: these are never forwarded to a master.
	api.GET("/update", s.getUpdate)
	api.POST("/update/check", s.checkUpdate)
	api.POST("/update/install", s.installUpdate)

	api.GET("/settings", s.getSettings)
	api.PUT("/settings", s.putSettings)

	api.GET("/system", s.getSystem)
	api.POST("/system/ip-forwarding", s.enableIPForwarding)
	api.POST("/system/congestion", s.setCongestion)
	api.GET("/system/network", s.getNetworkRules)

	api.GET("/scripts", s.listScripts)
	api.POST("/scripts", s.createScript, s.forwardToMaster)
	api.GET("/scripts/:id", s.getScript)
	api.PATCH("/scripts/:id", s.patchScript, s.forwardToMaster)
	api.DELETE("/scripts/:id", s.deleteScript, s.forwardToMaster)
	api.POST("/scripts/:id/run", s.runScript)
	api.POST("/scripts/:id/stop", s.stopScript)
	api.GET("/scripts/:id/runs", s.scriptRuns)

	api.GET("/monitors", s.listMonitors)
	api.POST("/monitors", s.createMonitor)
	api.GET("/monitors/:id", s.getMonitor)
	api.PATCH("/monitors/:id", s.patchMonitor)
	api.DELETE("/monitors/:id", s.deleteMonitor)
	api.POST("/monitors/:id/check", s.checkMonitor)

	api.GET("/peers", s.listPeers)
	api.POST("/peers", s.createPeer, s.forwardToMaster)
	api.POST("/peers/bulk", s.bulkPeers, s.forwardToMaster)
	api.GET("/peers/:id", s.getPeer)
	api.PATCH("/peers/:id", s.patchPeer, s.forwardToMaster)
	api.DELETE("/peers/:id", s.deletePeer, s.forwardToMaster)
	api.GET("/peers/:id/config", s.getPeerConfig)
	api.GET("/peers/:id/ipinfo", s.getPeerIPInfo)
	api.GET("/peers/:id/visibility", s.getVisibility)
	api.PUT("/peers/:id/visibility", s.putVisibility, s.forwardToMaster)

	api.GET("/groups", s.listGroups)
	api.POST("/groups", s.createGroup, s.forwardToMaster)
	api.POST("/groups/bulk", s.bulkGroups, s.forwardToMaster)
	api.GET("/groups/:id", s.getGroup)
	api.PATCH("/groups/:id", s.patchGroup, s.forwardToMaster)
	api.DELETE("/groups/:id", s.deleteGroup, s.forwardToMaster)

	e.GET("/*", frontendHandler())

	return e
}

// apiError is the body returned for every failed request.
type apiError struct {
	Error string `json:"error"`
}

// errRefused is what fail returns once it has written the refusal.
//
// Every caller already writes `if err != nil { return err }`, but c.JSON
// answers nil when it succeeds, so returning it meant a refusal read as
// success — helpers like loadPeer handed back a nil peer with a nil error and
// the handler carried on and dereferenced it. Returning something non-nil is
// what makes that convention actually stop them; the response is already
// written, so the error only carries the decision.
var errRefused = errors.New("request refused")

// syncWrongMethod answers the requests that are not a node syncing.
func syncWrongMethod(c echo.Context) error {
	return fail(c, http.StatusMethodNotAllowed,
		"this endpoint is how a node syncs with its master: POST to it with the sync secret as a bearer token")
}

func fail(c echo.Context, code int, format string, args ...any) error {
	msg := format
	if len(args) > 0 {
		msg = sprintf(format, args...)
	}
	if err := c.JSON(code, apiError{Error: msg}); err != nil {
		return err
	}
	return errRefused
}

func badRequest(c echo.Context, format string, args ...any) error {
	return fail(c, http.StatusBadRequest, format, args...)
}

func forbidden(c echo.Context) error {
	return fail(c, http.StatusForbidden, "you are not allowed to do that")
}

func notFound(c echo.Context) error {
	return fail(c, http.StatusNotFound, "not found")
}

// internalError logs the underlying cause and returns a generic message, so
// database details never reach the browser.
func (s *Server) internalError(c echo.Context, op string, err error) error {
	s.log.Error(op+" failed", "error", err, "path", c.Request().URL.Path)
	return fail(c, http.StatusInternalServerError, "%s failed", op)
}

// probe checks a monitor's target immediately, on behalf of the panel.
func (s *Server) probe(ctx context.Context, m *store.Monitor) monitor.Probe {
	return monitor.Check(ctx, m.Target, time.Duration(m.TimeoutSec)*time.Second)
}

// SetEngine wires the engine after construction: the two refer to each other,
// since the API asks the engine for live counters and the engine is configured
// from the settings the API owns.
func (s *Server) SetEngine(e *engine.Engine) { s.engine = e }

// SetCertFingerprint records the SHA-256 of the certificate this panel serves,
// so an admin setting up a node can read it off the Nodes page instead of
// hunting through the logs for the line that printed it at startup.
func (s *Server) SetCertFingerprint(v string) {
	s.mu.Lock()
	s.certFingerprint = v
	s.mu.Unlock()
}

func (s *Server) certFingerprintValue() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.certFingerprint
}
