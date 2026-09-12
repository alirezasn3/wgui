package api

import (
	"errors"
	"net/http"

	"wgui/internal/system"

	"github.com/labstack/echo/v4"
)

// getSystem reports the kernel settings the panel can change, so the settings
// page can show what is on and whether it is allowed to touch it.
func (s *Server) getSystem(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	return c.JSON(http.StatusOK, system.Read())
}

// enableIPForwarding turns on routing for peer traffic. Without it the tunnel
// comes up but nothing reaches the internet.
func (s *Server) enableIPForwarding(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}

	if err := system.EnableIPForwarding(); err != nil {
		return s.systemError(c, "enabling IP forwarding", err)
	}
	s.log.Info("IP forwarding enabled", "by", cl.Name)
	return c.JSON(http.StatusOK, system.Read())
}

// getNetworkRules dumps the machine's firewall and routing configuration. It is
// admin-only and read-only: it reveals how the server is protected, which is not
// something a distributor or a plain user has any business seeing.
func (s *Server) getNetworkRules(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"rules": system.NetworkRules(c.Request().Context()),
	})
}

type congestionRequest struct {
	Algorithm string `json:"algorithm"`
}

// setCongestion switches the congestion control algorithm, normally to BBR.
func (s *Server) setCongestion(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}

	var req congestionRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	if req.Algorithm == "" {
		req.Algorithm = "bbr"
	}

	if err := system.SetCongestionControl(req.Algorithm); err != nil {
		return s.systemError(c, "changing congestion control", err)
	}
	s.log.Info("congestion control changed", "algorithm", req.Algorithm, "by", cl.Name)
	return c.JSON(http.StatusOK, system.Read())
}

// systemError turns the package's sentinel errors into statuses the UI can act
// on, rather than a blanket 500 for what are mostly environment problems.
func (s *Server) systemError(c echo.Context, op string, err error) error {
	switch {
	case errors.Is(err, system.ErrUnsupported):
		return fail(c, http.StatusNotImplemented, "%s", err.Error())
	case errors.Is(err, system.ErrPermission):
		return fail(c, http.StatusForbidden, "%s", err.Error())
	case errors.Is(err, system.ErrBBRUnavailable):
		return fail(c, http.StatusUnprocessableEntity, "%s", err.Error())
	default:
		return s.internalError(c, op, err)
	}
}
