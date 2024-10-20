package main

import (
	"net"
	"strings"

	"github.com/labstack/echo/v4"
)

func Auth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		// check for admin bypass
		if ctx.Request().Header.Get("bypass_key") == config.BypassKey {
			ctx.Set("bypass", true)
			return next(ctx)
		}

		ip := strings.Split(ctx.Request().RemoteAddr, ":")[0]
		// check if request is from peer
		if !deviceCIDR.Contains(net.ParseIP(ip)) {
			logger.Warn("Unauthorized request from " + ctx.Request().RemoteAddr)
			return ctx.NoContent(403)
		}
		ctx.Set("peerIP", ip)
		ctx.Set("bypass", false)
		return next(ctx)
	}
}
