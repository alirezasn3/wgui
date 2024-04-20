package main

import (
	"net"
	"strings"

	"github.com/labstack/echo/v4"
)

var deviceCIDR *net.IPNet

func init() {
	var e error
	// used to check if requests is from peers
	_, deviceCIDR, e = net.ParseCIDR(config.InterfaceAddressCIDR)
	if e != nil {
		panic(e)
	}
}

func Auth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		// check if request is from peer
		if !deviceCIDR.Contains(net.ParseIP(strings.Split(ctx.Request().RemoteAddr, ":")[0])) {
			logger.Warn("Unauthorized request from " + ctx.Request().RemoteAddr)
			return ctx.NoContent(403)
		}

		// check if request is to api
		if !strings.Contains(ctx.Request().URL.Path, "/api/") || ctx.Request().URL.Path == "/api/auth" {
			return next(ctx)
		}

		// get cookie
		cookie, err := ctx.Cookie("id")
		if err == nil {
			if p, ok := peers.peers[cookie.Value]; ok {
				ctx.Set("role", p.Role)
				ctx.Set("name", p.Name)
				ctx.Set("id", p.ID)
				ctx.Set("group", strings.Split(p.Name, "-")[0]+"-")
				return next(ctx)
			}
		}

		logger.Warn("Unauthenticated request from " + ctx.Request().RemoteAddr)
		return ctx.NoContent(401)
	}
}
