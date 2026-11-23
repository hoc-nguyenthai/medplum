// Package echo4obs instruments Echo v4 (github.com/labstack/echo/v4) applications.
//
//	e := echo.New()
//	if err := echo4obs.Setup(ctx, e, observability.FromEnv()); err != nil {
//		e.Logger.Error(err)
//	}
//	defer observability.Shutdown(0)
package echo4obs

import (
	"context"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	observability "trivita-fhir/pkg/observability"
)

// Setup runs observability.Setup, replaces e.Logger with an adapter writing through the
// library's handler (JSON on stdout, OTLP when enabled) and registers Middleware on e. Call
// it before adding other middlewares, so the server span covers them.
func Setup(ctx context.Context, e *echo.Echo, s observability.Settings, opts ...observability.Option) error {
	err := observability.Setup(ctx, s, opts...)
	e.Logger = NewLogger(observability.Logger("echo"))
	e.Use(Middleware())
	return err
}

// Middleware creates one SERVER span per request named "<METHOD> <route>" (for example
// "GET /orders/:id"), records HTTP server metrics and writes an access log line. Requests
// matching Settings.ExcludedURLs are passed through untouched.
//
// Handler errors go through Echo's HTTPErrorHandler here (c.Error), so the recorded status
// code is the one the client receives; the error is still returned to outer middlewares.
func Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			req := c.Request()
			sr := observability.StartServerRequest(req, c.Path(), c.RealIP())
			if sr == nil {
				return next(c)
			}
			c.SetRequest(req.WithContext(sr.Context()))
			defer func() {
				if p := recover(); p != nil {
					sr.End(http.StatusInternalServerError, 0, fmt.Errorf("panic: %v", p))
					panic(p)
				}
			}()

			if err = next(c); err != nil {
				c.Error(err)
			}
			// The router answers unmatched requests with these exact errors; their c.Path() is
			// some partial route, which must not name the span.
			if err == echo.ErrNotFound || err == echo.ErrMethodNotAllowed {
				sr.SetRoute("")
			}
			resp := c.Response()
			sr.End(resp.Status, resp.Size, err)
			return err
		}
	}
}
