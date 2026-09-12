package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"wgui/web"

	"github.com/labstack/echo/v4"
)

// frontendHandler serves the embedded SvelteKit build.
//
// The static adapter writes a directory per prerendered route, so a request for
// /peers has to fall back to /peers/index.html, and anything it cannot resolve
// falls back to the SPA entry point so client-side routing keeps working after a
// hard refresh.
func frontendHandler() echo.HandlerFunc {
	assets, err := web.Assets()
	if err != nil {
		return func(c echo.Context) error {
			return fail(c, http.StatusNotFound,
				"the web interface was not built into this binary; run `npm run build` in web/ and rebuild")
		}
	}
	fileServer := http.FileServer(http.FS(assets))

	return func(c echo.Context) error {
		req := c.Request()
		clean := path.Clean("/" + strings.TrimPrefix(req.URL.Path, "/"))

		resolved, ok := resolve(assets, clean)
		if !ok {
			// Unknown route: hand the SPA entry point to the client router.
			for _, fallback := range []string{"200.html", "index.html"} {
				if _, err := fs.Stat(assets, fallback); err == nil {
					resolved = "/" + fallback
					ok = true
					break
				}
			}
		}
		if !ok {
			return notFound(c)
		}

		r := req.Clone(req.Context())
		r.URL.Path = resolved
		fileServer.ServeHTTP(c.Response(), r)
		return nil
	}
}

// resolve finds the file that should answer a request path.
func resolve(assets fs.FS, urlPath string) (string, bool) {
	if urlPath == "/" {
		if _, err := fs.Stat(assets, "index.html"); err == nil {
			return "/index.html", true
		}
		return "", false
	}

	name := strings.TrimPrefix(urlPath, "/")
	if info, err := fs.Stat(assets, name); err == nil && !info.IsDir() {
		return urlPath, true
	}
	indexed := path.Join(name, "index.html")
	if _, err := fs.Stat(assets, indexed); err == nil {
		return "/" + indexed, true
	}
	return "", false
}
