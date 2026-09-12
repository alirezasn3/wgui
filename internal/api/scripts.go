package api

import (
	"errors"
	"net/http"
	"strings"

	"wgui/internal/scripts"
	"wgui/internal/store"

	"github.com/labstack/echo/v4"
)

// Scripts run as root and can do anything to the server, so every route here is
// admin-only.
func (s *Server) listScripts(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}

	list, err := s.store.ListScripts()
	if err != nil {
		return s.internalError(c, "listing scripts", err)
	}
	running := s.runner.Running()
	for _, script := range list {
		script.Running = running[script.ID]
	}
	return c.JSON(http.StatusOK, map[string]any{"scripts": list})
}

func (s *Server) loadScript(c echo.Context) (*store.Script, error) {
	if !caller(c).IsAdmin() {
		return nil, forbidden(c)
	}
	id, err := pathInt64(c)
	if err != nil {
		return nil, badRequest(c, "malformed script id")
	}

	script, err := s.store.GetScript(id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(c)
	}
	if err != nil {
		return nil, s.internalError(c, "loading script", err)
	}
	script.Running = s.runner.Running()[script.ID]
	return script, nil
}

func (s *Server) getScript(c echo.Context) error {
	script, err := s.loadScript(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, script)
}

type scriptRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Body        *string `json:"body"`
	TimeoutSec  *int    `json:"timeoutSec"`
}

func (s *Server) createScript(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}

	var req scriptRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		return badRequest(c, "name is required")
	}

	script := &store.Script{Name: strings.TrimSpace(*req.Name), TimeoutSec: 60}
	if req.Description != nil {
		script.Description = *req.Description
	}
	if req.Body != nil {
		script.Body = *req.Body
	}
	if req.TimeoutSec != nil {
		script.TimeoutSec = clampTimeout(*req.TimeoutSec)
	}

	if err := s.store.CreateScript(script); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "a script named %q already exists", script.Name)
		}
		return s.internalError(c, "creating script", err)
	}

	s.log.Info("script created", "script", script.Name, "by", cl.Name)
	return c.JSON(http.StatusCreated, script)
}

func (s *Server) patchScript(c echo.Context) error {
	script, err := s.loadScript(c)
	if err != nil {
		return err
	}
	cl := caller(c)

	var req scriptRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}

	patch := store.ScriptPatch{Description: req.Description, Body: req.Body}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return badRequest(c, "name must not be empty")
		}
		patch.Name = &name
	}
	if req.TimeoutSec != nil {
		v := clampTimeout(*req.TimeoutSec)
		patch.TimeoutSec = &v
	}

	if err := s.store.UpdateScript(script.ID, patch); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "that name is already taken")
		}
		return s.internalError(c, "updating script", err)
	}

	s.log.Info("script updated", "script", script.Name, "by", cl.Name)
	updated, err := s.store.GetScript(script.ID)
	if err != nil {
		return s.internalError(c, "reloading script", err)
	}
	return c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteScript(c echo.Context) error {
	script, err := s.loadScript(c)
	if err != nil {
		return err
	}
	if script.Running {
		return badRequest(c, "this script is still running; stop it first")
	}

	if err := s.store.DeleteScript(script.ID); err != nil {
		return s.internalError(c, "deleting script", err)
	}
	s.log.Info("script deleted", "script", script.Name, "by", caller(c).Name)
	return c.NoContent(http.StatusNoContent)
}

// runScript executes a script and returns the result once it has finished.
func (s *Server) runScript(c echo.Context) error {
	script, err := s.loadScript(c)
	if err != nil {
		return err
	}
	cl := caller(c)

	run, err := s.runner.Run(c.Request().Context(), script, scripts.SourceManual, cl.Name)
	if errors.Is(err, scripts.ErrAlreadyRunning) {
		return fail(c, http.StatusConflict, "%s", err.Error())
	}
	if err != nil {
		return s.internalError(c, "running script", err)
	}
	return c.JSON(http.StatusOK, run)
}

func (s *Server) stopScript(c echo.Context) error {
	script, err := s.loadScript(c)
	if err != nil {
		return err
	}
	if !s.runner.Cancel(script.ID) {
		return badRequest(c, "this script is not running")
	}
	s.log.Info("script stopped", "script", script.Name, "by", caller(c).Name)
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) scriptRuns(c echo.Context) error {
	script, err := s.loadScript(c)
	if err != nil {
		return err
	}
	runs, err := s.store.ScriptRuns(script.ID, queryInt(c, "limit", 20))
	if err != nil {
		return s.internalError(c, "loading script history", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"runs": runs})
}

// clampTimeout keeps a mistyped timeout from pinning a process open, or from
// being so short that nothing can finish.
func clampTimeout(v int) int {
	switch {
	case v < 1:
		return 1
	case v > 1800:
		return 1800
	default:
		return v
	}
}
