package main

import (
	"log"
	"net"
	"strings"

	"github.com/labstack/echo/v4"
)

func Auth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		// check for admin bypass
		if config.BypassKey != "" {
			if ctx.Request().Header.Get("bypass_key") == config.BypassKey {
				ctx.Set("bypass", true)
				return next(ctx)
			}
		}

		ip := strings.Split(ctx.Request().RemoteAddr, ":")[0]
		// check if request is from peer
		if !deviceCIDR.Contains(net.ParseIP(ip)) {
			log.Println("Unauthorized request from " + ctx.Request().RemoteAddr)
			return ctx.NoContent(403)
		}

		peer, err := peersDB.GetPeerByAllowedIPs(ip + "/32")
		if err != nil {
			if err.Error() == "peer not found" {
				log.Println("Unauthorized request from " + ctx.Request().RemoteAddr)
				return ctx.NoContent(403)
			}
			return ctx.String(500, err.Error())
		}
		ctx.Set("peerName", peer.Name)
		ctx.Set("peerRole", peer.Role)
		ctx.Set("bypass", false)
		return next(ctx)
	}
}
