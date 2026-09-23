package api

import (
	"errors"
	"net/http"

	"wgui/internal/update"

	"github.com/labstack/echo/v4"
)

// SetUpdater gives the panel a way to check for and install new releases.
func (s *Server) SetUpdater(u *update.Updater) { s.updater = u }

// getUpdate reports whether a newer release exists, with its changelog, and
// the progress of an install under way.
func (s *Server) getUpdate(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	if s.updater == nil {
		return fail(c, http.StatusServiceUnavailable, "updates are not available on this server")
	}
	return c.JSON(http.StatusOK, s.updater.Status())
}

// checkUpdate asks GitHub now rather than waiting for the next scheduled check.
// A failure to reach it is part of the status, not an error of this request:
// the panel shows it where the answer would have been.
func (s *Server) checkUpdate(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	if s.updater == nil {
		return fail(c, http.StatusServiceUnavailable, "updates are not available on this server")
	}
	if err := s.updater.Check(c.Request().Context()); err != nil {
		s.log.Warn("checking for updates failed", "error", err)
	}
	return c.JSON(http.StatusOK, s.updater.Status())
}

type installUpdateRequest struct {
	Version string `json:"version"`
}

// installUpdate starts installing a release. It answers at once; the panel
// follows the install through getUpdate and sees the server come back as the
// new version.
func (s *Server) installUpdate(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}
	if s.updater == nil {
		return fail(c, http.StatusServiceUnavailable, "updates are not available on this server")
	}
	var req installUpdateRequest
	if err := c.Bind(&req); err != nil || req.Version == "" {
		return badRequest(c, "say which version to install")
	}

	switch err := s.updater.Install(req.Version); {
	case errors.Is(err, update.ErrUnsupported), errors.Is(err, update.ErrBusy):
		return fail(c, http.StatusConflict, "%s", err.Error())
	case errors.Is(err, update.ErrUnknown):
		return badRequest(c, "%s", err.Error())
	case err != nil:
		return s.internalError(c, "starting the update", err)
	}
	s.log.Info("update started", "version", req.Version, "by", cl.Name)
	return c.JSON(http.StatusAccepted, s.updater.Status())
}
