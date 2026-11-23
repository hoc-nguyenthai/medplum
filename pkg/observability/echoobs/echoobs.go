// Package echoobs instruments Echo v5 (github.com/labstack/echo/v5) applications.
//
//	e := echo.New()
//	if err := echoobs.Setup(ctx, e, observability.FromEnv()); err != nil {
//		e.Logger.Error("observability setup failed", "error", err)
//	}
//	defer observability.Shutdown(0)
//
// For Echo v4 use package echo4obs.
package echoobs

import (
	"context"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"

	observability "trivita-fhir/pkg/observability"
)

// Setup runs observability.Setup, routes Echo's own logger through the library's handler
// (JSON on stdout, OTLP when enabled) and registers Middleware on e. Call it before adding
// other middlewares, so the server span covers them.
func Setup(ctx context.Context, e *echo.Echo, s observability.Settings, opts ...observability.Option) error {
	err := observability.Setup(ctx, s, opts...)
	e.Logger = observability.Logger("echo")
	e.Use(Middleware())
	return err
}

// Middleware creates one SERVER span per request named "<METHOD> <route>" (for example
// "GET /orders/:id"), records HTTP server metrics and writes an access log line. Requests
// matching Settings.ExcludedURLs are passed through untouched.
//
// Handler errors go through Echo's HTTPErrorHandler here, so the recorded status code is the
// one the client receives; the error is still returned to outer middlewares.
func Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			req := c.Request()
			sr := observability.StartServerRequest(req, route(c), c.RealIP())
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
				c.Echo().HTTPErrorHandler(c, err)
			}
			resp, status := echo.ResolveResponseStatus(c.Response(), err)
			var size int64
			if resp != nil {
				size = resp.Size
			}
			sr.End(status, size, err)
			return err
		}
	}
}

// route returns the matched route template, or "" for 404/405, so unmatched URLs never
// become span names or metric attributes.
func route(c *echo.Context) string {
	switch c.RouteInfo().Name {
	case echo.NotFoundRouteName, echo.MethodNotAllowedRouteName:
		return ""
	}
	return c.Path()
}
