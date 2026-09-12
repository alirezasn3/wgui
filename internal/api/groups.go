package api

import (
	"errors"
	"net/http"
	"strings"

	"wgui/internal/config"
	"wgui/internal/store"

	"github.com/labstack/echo/v4"
)

type groupListResponse struct {
	Groups   []*store.Group `json:"groups"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}

func (s *Server) listGroups(c echo.Context) error {
	cl := caller(c)

	filter := store.GroupFilter{
		Scope:    cl.Scope(),
		Search:   c.QueryParam("search"),
		Status:   c.QueryParam("status"),
		Sort:     c.QueryParam("sort"),
		Desc:     c.QueryParam("order") == "desc",
		Page:     queryInt(c, "page", 1),
		PageSize: queryInt(c, "pageSize", 50),
		Now:      nowMS(),
	}
	if filter.PageSize > 500 {
		filter.PageSize = 500
	}

	groups, total, err := s.store.ListGroups(filter)
	if err != nil {
		return s.internalError(c, "listing groups", err)
	}
	return c.JSON(http.StatusOK, groupListResponse{
		Groups: groups, Total: total, Page: filter.Page, PageSize: filter.PageSize,
	})
}

// loadGroup fetches a group and checks the caller may see it.
func (s *Server) loadGroup(c echo.Context) (*store.Group, *Caller, error) {
	cl := caller(c)
	id, err := pathInt64(c)
	if err != nil {
		return nil, cl, badRequest(c, "malformed group id")
	}

	group, err := s.store.GetGroup(id, nowMS())
	if errors.Is(err, store.ErrNotFound) {
		return nil, cl, notFound(c)
	}
	if err != nil {
		return nil, cl, s.internalError(c, "loading group", err)
	}

	if !cl.IsAdmin() && group.OwnerID != cl.PeerID && !cl.Bypass {
		return nil, cl, notFound(c)
	}
	return group, cl, nil
}

// assertGroupWritable checks a group exists and the caller may put peers into it.
func (s *Server) assertGroupWritable(cl *Caller, id int64) *httpError {
	group, err := s.store.GetGroup(id, nowMS())
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("group %d does not exist", id)
	}
	if err != nil {
		return errInternal("loading group failed")
	}
	if !s.canModifyGroup(cl, group) {
		return errForbidden("you are not allowed to use group %q", group.Name)
	}
	return nil
}

func (s *Server) getGroup(c echo.Context) error {
	group, _, err := s.loadGroup(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, group)
}

type createGroupRequest struct {
	Name         string  `json:"name"`
	OwnerID      *string `json:"ownerId"`
	AllowedUsage *int64  `json:"allowedUsage"`
	ExpiresAt    *int64  `json:"expiresAt"`
	ExpiryDays   *int    `json:"expiryDays"`
	Note         *string `json:"note"`
}

func (s *Server) createGroup(c echo.Context) error {
	cl := caller(c)
	if !cl.CanWrite() {
		return forbidden(c)
	}

	var req createGroupRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return badRequest(c, "name is required")
	}

	defaults := s.Settings().GroupDefaults
	group := &store.Group{
		Name:         req.Name,
		OwnerID:      cl.PeerID,
		AllowedUsage: defaults.AllowedUsageBytes,
		ExpiresAt:    expiryFromDays(defaults.ExpiryDays),
	}

	if req.AllowedUsage != nil {
		group.AllowedUsage = max(*req.AllowedUsage, 0)
	}
	switch {
	case req.ExpiryDays != nil:
		group.ExpiresAt = expiryFromDays(*req.ExpiryDays)
	case req.ExpiresAt != nil:
		group.ExpiresAt = max(*req.ExpiresAt, 0)
	}
	if req.Note != nil {
		group.Note = *req.Note
	}
	if cl.IsAdmin() && req.OwnerID != nil {
		group.OwnerID = *req.OwnerID
	}

	if err := s.store.CreateGroup(group); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "a group named %q already exists", group.Name)
		}
		return s.internalError(c, "creating group", err)
	}

	s.log.Info("group created", "group", group.Name, "by", cl.Name)

	created, err := s.store.GetGroup(group.ID, nowMS())
	if err != nil {
		return s.internalError(c, "reloading group", err)
	}
	return c.JSON(http.StatusCreated, created)
}

type patchGroupRequest struct {
	Name             *string `json:"name"`
	OwnerID          *string `json:"ownerId"`
	AllowedUsage     *int64  `json:"allowedUsage"`
	ExpiresAt        *int64  `json:"expiresAt"`
	ExpiryDays       *int    `json:"expiryDays"`
	ManuallyDisabled *bool   `json:"manuallyDisabled"`
	Note             *string `json:"note"`
	ResetUsage       bool    `json:"resetUsage"`
}

func (s *Server) toGroupPatch(req patchGroupRequest, cl *Caller) (store.GroupPatch, *httpError) {
	var patch store.GroupPatch

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return patch, errBadRequest("name must not be empty")
		}
		patch.Name = &name
	}
	if req.OwnerID != nil {
		if !cl.IsAdmin() {
			return patch, errForbidden("only an admin can reassign ownership")
		}
		patch.OwnerID = req.OwnerID
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
	patch.ManuallyDisabled = req.ManuallyDisabled
	patch.Note = req.Note
	patch.ResetUsage = req.ResetUsage
	return patch, nil
}

func (s *Server) patchGroup(c echo.Context) error {
	group, cl, err := s.loadGroup(c)
	if err != nil {
		return err
	}
	if !s.canModifyGroup(cl, group) {
		return forbidden(c)
	}

	var req patchGroupRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	patch, herr := s.toGroupPatch(req, cl)
	if herr != nil {
		return herr.render(c)
	}

	if _, err := s.store.UpdateGroups([]int64{group.ID}, patch); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "that name is already taken")
		}
		return s.internalError(c, "updating group", err)
	}

	s.log.Info("group updated", "group", group.Name, "by", cl.Name)
	s.engine.Kick()

	updated, err := s.store.GetGroup(group.ID, nowMS())
	if err != nil {
		return s.internalError(c, "reloading group", err)
	}
	return c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteGroup(c echo.Context) error {
	group, cl, err := s.loadGroup(c)
	if err != nil {
		return err
	}
	if !s.canModifyGroup(cl, group) {
		return forbidden(c)
	}

	if _, err := s.store.DeleteGroups([]int64{group.ID}); err != nil {
		return s.internalError(c, "deleting group", err)
	}

	s.log.Info("group deleted", "group", group.Name, "by", cl.Name)
	s.engine.Kick()
	return c.NoContent(http.StatusNoContent)
}

type bulkGroupRequest struct {
	IDs    []int64 `json:"ids"`
	Action string  `json:"action"`

	AllowedUsage *int64 `json:"allowedUsage"`
	ExpiresAt    *int64 `json:"expiresAt"`
	ExpiryDays   *int   `json:"expiryDays"`
}

func (s *Server) bulkGroups(c echo.Context) error {
	cl := caller(c)
	if !cl.CanWrite() {
		return forbidden(c)
	}

	var req bulkGroupRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}
	if len(req.IDs) == 0 {
		return badRequest(c, "select at least one group")
	}

	for _, id := range req.IDs {
		group, err := s.store.GetGroup(id, nowMS())
		if errors.Is(err, store.ErrNotFound) {
			return fail(c, http.StatusNotFound, "group %d no longer exists", id)
		}
		if err != nil {
			return s.internalError(c, "loading groups", err)
		}
		if !s.canModifyGroup(cl, group) {
			return fail(c, http.StatusForbidden, "you are not allowed to change %q", group.Name)
		}
	}

	if req.Action == "delete" {
		n, err := s.store.DeleteGroups(req.IDs)
		if err != nil {
			return s.internalError(c, "deleting groups", err)
		}
		s.log.Info("groups deleted in bulk", "count", n, "by", cl.Name)
		s.engine.Kick()
		return c.JSON(http.StatusOK, map[string]any{"affected": n})
	}

	var patch store.GroupPatch
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
			return badRequest(c, "allowedUsage is required for setUsage")
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
			return badRequest(c, "expiresAt or expiryDays is required for setExpiry")
		}
	default:
		return badRequest(c, "unknown action %q", req.Action)
	}

	n, err := s.store.UpdateGroups(req.IDs, patch)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return fail(c, http.StatusConflict, "that change would give two groups the same name")
		}
		return s.internalError(c, "updating groups", err)
	}

	s.log.Info("groups updated in bulk", "action", req.Action, "count", n, "by", cl.Name)
	s.engine.Kick()
	return c.JSON(http.StatusOK, map[string]any{"affected": n})
}

// ownerOptions lists the peers that can own something, for the pickers in the
// UI. Only an admin ever sees more than itself.
func (s *Server) ownerOptions(cl *Caller) ([]*store.Peer, error) {
	if !cl.IsAdmin() {
		return nil, nil
	}
	peers, _, err := s.store.ListPeers(store.PeerFilter{
		Scope: store.Scope{Role: config.RoleAdmin},
		Role:  config.RoleDistributor,
		Sort:  "name",
		Now:   nowMS(),
	})
	return peers, err
}
