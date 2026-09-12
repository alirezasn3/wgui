package api

import (
	"errors"
	"net/http"
	"strings"

	"wgui/internal/store"

	"github.com/labstack/echo/v4"
)

func (s *Server) listMonitors(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	list, err := s.store.ListMonitors()
	if err != nil {
		return s.internalError(c, "listing monitors", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"monitors": list})
}

func (s *Server) loadMonitor(c echo.Context) (*store.Monitor, error) {
	if !caller(c).IsAdmin() {
		return nil, forbidden(c)
	}
	id, err := pathInt64(c)
	if err != nil {
		return nil, badRequest(c, "malformed monitor id")
	}

	m, err := s.store.GetMonitor(id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(c)
	}
	if err != nil {
		return nil, s.internalError(c, "loading monitor", err)
	}
	return m, nil
}

func (s *Server) getMonitor(c echo.Context) error {
	m, err := s.loadMonitor(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, m)
}

type monitorRequest struct {
	Name            *string `json:"name"`
	Target          *string `json:"target"`
	IntervalSec     *int    `json:"intervalSec"`
	TimeoutSec      *int    `json:"timeoutSec"`
	FailuresBefore  *int    `json:"failuresBefore"`
	ScriptID        *int64  `json:"scriptId"`
	RearmOnRecovery *bool   `json:"rearmOnRecovery"`
	Enabled         *bool   `json:"enabled"`
	ResetState      bool    `json:"resetState"`
}

func (s *Server) createMonitor(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}

	var req monitorRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		return badRequest(c, "name is required")
	}
	if req.Target == nil || strings.TrimSpace(*req.Target) == "" {
		return badRequest(c, "a target is required, as a host, an address, or host:port")
	}

	m := &store.Monitor{
		Name:            strings.TrimSpace(*req.Name),
		Target:          strings.TrimSpace(*req.Target),
		IntervalSec:     30,
		TimeoutSec:      5,
		FailuresBefore:  3,
		RearmOnRecovery: true,
		Enabled:         true,
	}
	applyMonitorDefaults(m, req)

	if m.ScriptID != 0 {
		if _, err := s.store.GetScript(m.ScriptID); err != nil {
			return badRequest(c, "that script does not exist")
		}
	}

	if err := s.store.CreateMonitor(m); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "a monitor named %q already exists", m.Name)
		}
		return s.internalError(c, "creating monitor", err)
	}

	s.log.Info("monitor created", "monitor", m.Name, "target", m.Target, "by", cl.Name)
	created, err := s.store.GetMonitor(m.ID)
	if err != nil {
		return s.internalError(c, "reloading monitor", err)
	}
	return c.JSON(http.StatusCreated, created)
}

func applyMonitorDefaults(m *store.Monitor, req monitorRequest) {
	if req.IntervalSec != nil {
		m.IntervalSec = clampInterval(*req.IntervalSec)
	}
	if req.TimeoutSec != nil {
		m.TimeoutSec = clampProbeTimeout(*req.TimeoutSec)
	}
	if req.FailuresBefore != nil {
		m.FailuresBefore = max(*req.FailuresBefore, 1)
	}
	if req.ScriptID != nil {
		m.ScriptID = *req.ScriptID
	}
	if req.RearmOnRecovery != nil {
		m.RearmOnRecovery = *req.RearmOnRecovery
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
}

func (s *Server) patchMonitor(c echo.Context) error {
	m, err := s.loadMonitor(c)
	if err != nil {
		return err
	}

	var req monitorRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}

	patch := store.MonitorPatch{
		ScriptID:        req.ScriptID,
		RearmOnRecovery: req.RearmOnRecovery,
		Enabled:         req.Enabled,
		ResetState:      req.ResetState,
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return badRequest(c, "name must not be empty")
		}
		patch.Name = &name
	}
	if req.Target != nil {
		target := strings.TrimSpace(*req.Target)
		if target == "" {
			return badRequest(c, "target must not be empty")
		}
		patch.Target = &target
	}
	if req.IntervalSec != nil {
		v := clampInterval(*req.IntervalSec)
		patch.IntervalSec = &v
	}
	if req.TimeoutSec != nil {
		v := clampProbeTimeout(*req.TimeoutSec)
		patch.TimeoutSec = &v
	}
	if req.FailuresBefore != nil {
		v := max(*req.FailuresBefore, 1)
		patch.FailuresBefore = &v
	}
	if patch.ScriptID != nil && *patch.ScriptID != 0 {
		if _, err := s.store.GetScript(*patch.ScriptID); err != nil {
			return badRequest(c, "that script does not exist")
		}
	}

	if err := s.store.UpdateMonitor(m.ID, patch); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "that name is already taken")
		}
		return s.internalError(c, "updating monitor", err)
	}

	s.log.Info("monitor updated", "monitor", m.Name, "by", caller(c).Name)
	updated, err := s.store.GetMonitor(m.ID)
	if err != nil {
		return s.internalError(c, "reloading monitor", err)
	}
	return c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteMonitor(c echo.Context) error {
	m, err := s.loadMonitor(c)
	if err != nil {
		return err
	}
	if err := s.store.DeleteMonitor(m.ID); err != nil {
		return s.internalError(c, "deleting monitor", err)
	}
	s.log.Info("monitor deleted", "monitor", m.Name, "by", caller(c).Name)
	return c.NoContent(http.StatusNoContent)
}

// checkMonitor probes the target right now, so an operator can see whether the
// settings work without waiting for the next round.
func (s *Server) checkMonitor(c echo.Context) error {
	m, err := s.loadMonitor(c)
	if err != nil {
		return err
	}

	probe := s.probe(c.Request().Context(), m)
	res := map[string]any{"ok": probe.OK, "rttMs": probe.RTT.Milliseconds()}
	if probe.Err != nil {
		res["error"] = probe.Err.Error()
	}
	return c.JSON(http.StatusOK, res)
}

func clampInterval(v int) int {
	switch {
	case v < 5:
		return 5
	case v > 86400:
		return 86400
	default:
		return v
	}
}

func clampProbeTimeout(v int) int {
	switch {
	case v < 1:
		return 1
	case v > 60:
		return 60
	default:
		return v
	}
}
