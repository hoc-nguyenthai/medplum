package fhirsub

import (
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"trivita-fhir/pkg/safehttp"
)

// ErrBlockedAddress báo địa chỉ đích thuộc dải nội bộ (xem pkg/safehttp).
var ErrBlockedAddress = safehttp.ErrBlockedAddress

// blocked cho biết ip thuộc dải không được phép làm đích webhook.
func blocked(ip netip.Addr) bool { return safehttp.Blocked(ip) }

// checkEndpoint kiểm tra URL của kênh: chỉ http/https, có host, không chứa userinfo.
func checkEndpoint(raw string) (*url.URL, error) { return safehttp.CheckURL(raw, false) }

// newClient dựng HTTP client giao webhook, chặn SSRF ở bước kết nối.
func newClient(timeout time.Duration, allowPrivate bool) *http.Client {
	return safehttp.NewClient(timeout, allowPrivate)
}
