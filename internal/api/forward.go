package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"wgui/internal/config"

	"github.com/labstack/echo/v4"
)

// forwardToMaster sends a write to the master instead of applying it here.
//
// A node holds its master's peers, so a change written locally is replaced at
// the next sync. Rather than refuse the edit — which leaves an operator with a
// panel that shows everything and can change nothing — the request is carried
// upstream, answered by the master, and pulled straight back down, so the page
// the operator refreshes is already showing what they just did.
//
// The request travels as the operator who made it, not as the node, so the
// master applies exactly the permissions that peer has there.
func (s *Server) forwardToMaster(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !s.cfg.Master.IsNode() {
			return next(c)
		}
		if s.forwarder == nil {
			return fail(c, http.StatusServiceUnavailable,
				"this server is a node but has no way to reach its master yet")
		}

		cl := caller(c)
		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return badRequest(c, "could not read the request")
		}

		res, err := s.forwarder.Forward(c.Request().Context(), Forwarded{
			Method:  c.Request().Method,
			Path:    c.Request().URL.RequestURI(),
			Body:    body,
			PeerID:  cl.PeerID,
			Headers: http.Header{"Content-Type": {c.Request().Header.Get("Content-Type")}},
		})
		if err != nil {
			return fail(c, http.StatusBadGateway,
				"could not reach the master at %s: %v", s.cfg.Master.URL, err)
		}

		// Pull the change back down before answering, so an operator who
		// refreshes immediately sees what they just did rather than the state
		// from before it.
		s.forwarder.SyncNow(c.Request().Context())

		for k, vs := range res.Headers {
			for _, v := range vs {
				c.Response().Header().Add(k, v)
			}
		}
		return c.Blob(res.Status, res.Headers.Get("Content-Type"), res.Body)
	}
}

// Forwarded is one request being carried to a master on an operator's behalf.
type Forwarded struct {
	Method  string
	Path    string
	Body    []byte
	PeerID  string
	Headers http.Header
}

// ForwardedResponse is what the master answered.
type ForwardedResponse struct {
	Status  int
	Headers http.Header
	Body    []byte
}

// Forwarder carries a node's writes to its master. The sync client implements
// it; the API only needs to know that something can.
type Forwarder interface {
	Forward(ctx context.Context, req Forwarded) (*ForwardedResponse, error)
	// SyncNow pulls the master's state immediately rather than waiting for the
	// next tick.
	SyncNow(ctx context.Context)
}

// SetForwarder gives a node somewhere to send the writes made on its panel.
func (s *Server) SetForwarder(f Forwarder) { s.forwarder = f }

// forwardSettings sends the shared half of a settings change to the master. A
// master that cannot be reached is reported rather than swallowed: the local
// half has already been saved, and an operator should know the rest has not.
func (s *Server) forwardSettings(c echo.Context, shared config.Settings) error {
	if s.forwarder == nil {
		return nil
	}
	body, err := json.Marshal(shared)
	if err != nil {
		return err
	}
	res, err := s.forwarder.Forward(c.Request().Context(), Forwarded{
		Method:  http.MethodPut,
		Path:    "/api/settings",
		Body:    body,
		PeerID:  caller(c).PeerID,
		Headers: http.Header{"Content-Type": {echo.MIMEApplicationJSON}},
	})
	if err != nil {
		return fail(c, http.StatusBadGateway,
			"your own address was saved, but the shared settings could not reach the master at %s: %v",
			s.cfg.Master.URL, err)
	}
	if res.Status >= 300 {
		return c.Blob(res.Status, res.Headers.Get("Content-Type"), res.Body)
	}
	s.forwarder.SyncNow(c.Request().Context())
	return nil
}
