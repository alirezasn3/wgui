package api

import (
	"context"
	"errors"
	"net/http"

	"wgui/internal/telegram"

	"github.com/labstack/echo/v4"
)

// TelegramBot is the warning bot, as far as the API is concerned.
type TelegramBot interface {
	Status() telegram.Status
	SetToken(ctx context.Context, token string) (telegram.Status, error)
	NewLink(peerID string) (link string, expiresAt int64, err error)
}

// SetTelegram gives the API the bot. Only the master has one.
func (s *Server) SetTelegram(b TelegramBot) { s.telegram = b }

func (s *Server) noBot(c echo.Context) error {
	return fail(c, http.StatusServiceUnavailable, "this server does not run the Telegram bot")
}

// getTelegram reports whether the bot is set up and in touch with Telegram.
func (s *Server) getTelegram(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	if s.telegram == nil {
		return s.noBot(c)
	}
	return c.JSON(http.StatusOK, s.telegram.Status())
}

type telegramTokenRequest struct {
	Token string `json:"token"`
}

// putTelegramToken sets the bot's token, or removes the bot with an empty one.
// The token is only ever written: nothing here, or anywhere, reads it back out.
func (s *Server) putTelegramToken(c echo.Context) error {
	cl := caller(c)
	if !cl.IsAdmin() {
		return forbidden(c)
	}
	if s.telegram == nil {
		return s.noBot(c)
	}
	var req telegramTokenRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "malformed request body")
	}

	status, err := s.telegram.SetToken(c.Request().Context(), req.Token)
	var remote *telegram.RemoteError
	switch {
	case errors.Is(err, telegram.ErrBadToken):
		return badRequest(c, "%s", err.Error())
	case errors.As(err, &remote):
		return fail(c, http.StatusBadGateway, "%s", err.Error())
	case err != nil:
		return s.internalError(c, "saving the Telegram token", err)
	}
	if req.Token == "" {
		s.log.Info("telegram bot removed", "by", cl.Name)
	} else {
		s.log.Info("telegram bot set", "bot", status.Username, "by", cl.Name)
	}
	return c.JSON(http.StatusOK, status)
}

// getPeerTelegram says whether the bot can be connected to this peer, and
// whether it is.
func (s *Server) getPeerTelegram(c echo.Context) error {
	peer, _, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if s.telegram == nil {
		return s.noBot(c)
	}
	st := s.telegram.Status()
	linked, err := s.store.TelegramLinked(peer.ID)
	if err != nil {
		return s.internalError(c, "reading the Telegram link", err)
	}
	return c.JSON(http.StatusOK, echo.Map{
		"available": st.Configured && st.Username != "",
		"linked":    linked,
	})
}

// linkPeerTelegram makes the link a customer opens to connect their chat.
//
// Only an admin may. A link decides whose phone a subscription's warnings reach,
// and the admin is the one who hands them out; a distributor or a customer can
// see whether a peer is connected, and can stop it, but not start it. The check
// comes before the peer is looked up, so a refusal says nothing about which
// peers exist.
func (s *Server) linkPeerTelegram(c echo.Context) error {
	if !caller(c).IsAdmin() {
		return forbidden(c)
	}
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if s.telegram == nil {
		return s.noBot(c)
	}

	link, expiresAt, err := s.telegram.NewLink(peer.ID)
	switch {
	case errors.Is(err, telegram.ErrNotConfigured), errors.Is(err, telegram.ErrNoUsername):
		return fail(c, http.StatusConflict, "%s", err.Error())
	case err != nil:
		return s.internalError(c, "making a Telegram link", err)
	}
	s.log.Info("telegram link made", "peer", peer.Name, "by", cl.Name)
	return c.JSON(http.StatusOK, echo.Map{"link": link, "expiresAt": expiresAt})
}

// unlinkPeerTelegram stops a peer's warnings. Whoever may change the peer can,
// and so can the peer itself: being able to stop messages to your own phone is
// not something to withhold.
func (s *Server) unlinkPeerTelegram(c echo.Context) error {
	peer, cl, err := s.loadPeer(c)
	if err != nil {
		return err
	}
	if !s.canModifyPeer(cl, peer) && peer.ID != cl.PeerID {
		return forbidden(c)
	}
	if err := s.store.UnlinkPeer(peer.ID); err != nil {
		return s.internalError(c, "disconnecting Telegram", err)
	}
	s.log.Info("telegram disconnected", "peer", peer.Name, "by", cl.Name)
	return c.NoContent(http.StatusNoContent)
}
