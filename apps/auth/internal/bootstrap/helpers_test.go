package bootstrap

import (
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func httpTestServer(t *testing.T, e *echo.Echo) string {
	t.Helper()
	ts := httptest.NewServer(e)
	t.Cleanup(ts.Close)
	return ts.URL
}
