package main

import (
	"net"
	"strings"

	"github.com/labstack/echo/v4"
)

func Auth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		ip := strings.Split(ctx.Request().RemoteAddr, ":")[0]
		// check if request is from peer
		if !deviceCIDR.Contains(net.ParseIP(ip)) {
			logger.Warn("Unauthorized request from " + ctx.Request().RemoteAddr)
			return ctx.NoContent(403)
		}
		ctx.Set("peerIP", ip)
		return next(ctx)
	}
}
