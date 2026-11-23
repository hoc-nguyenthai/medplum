// Package safehttp là HTTP client ra ngoài an toàn trước SSRF, dùng chung cho mọi nơi server gọi một URL do người dùng cung cấp
// (webhook của Subscription, JWKS của client OAuth...). Việc chặn dải nội bộ làm ở bước KẾT NỐI (Dialer.Control) trên địa chỉ IP thật đã
// phân giải nên chống được cả DNS rebinding; không đi qua proxy từ biến môi trường, không theo redirect.
package safehttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedAddress báo địa chỉ đích thuộc dải nội bộ (loopback, mạng riêng, link-local, metadata đám mây, dải dành riêng IANA...).
var ErrBlockedAddress = errors.New("safehttp: địa chỉ đích bị chặn (dải nội bộ)")

// blockedPrefixes là các dải không thuộc Internet công cộng mà netip không có hàm kiểm riêng (các dải IsPrivate, IsLoopback, IsLinkLocal*,
// IsMulticast, IsUnspecified đã được kiểm bằng hàm tương ứng).
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"100.64.0.0/10",   // CGNAT (RFC 6598)
		"192.0.0.0/24",    // IETF protocol assignments (RFC 6890)
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking (RFC 2544)
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // dành riêng (gồm cả 255.255.255.255)
		"64:ff9b::/96",    // NAT64: nhúng được IPv4 nội bộ
		"64:ff9b:1::/48",  // NAT64 cục bộ (RFC 8215)
		"100::/64",        // discard-only (RFC 6666)
		"2001:db8::/32",   // tài liệu
		"2002::/16",       // 6to4: nhúng được IPv4 nội bộ
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Blocked cho biết ip không được phép làm đích (IPv4 nhúng trong IPv6 được giải ra trước khi kiểm).
func Blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// HostBlocked cho biết host là địa chỉ IP literal bị chặn, hoặc tên chắc chắn là nội bộ (localhost và *.localhost). Tên miền thường chỉ kiểm được sau khi resolve (lúc kết nối).
func HostBlocked(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || h == "localhost.localdomain" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(strings.Trim(h, "[]"))
	return err == nil && Blocked(ip)
}

// CheckURL kiểm tra URL đích: http(s) (chỉ https nếu httpsOnly), có host, không chứa userinfo.
func CheckURL(raw string, httpsOnly bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL không hợp lệ: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, errors.New("phải là URL http(s) đầy đủ")
	}
	if httpsOnly && u.Scheme != "https" {
		return nil, errors.New("phải là https")
	}
	if u.User != nil {
		return nil, errors.New("không được chứa thông tin đăng nhập")
	}
	return u, nil
}

// NewClient dựng HTTP client chặn SSRF. allowPrivate=true tắt việc chặn (chỉ cho dev/test).
func NewClient(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return ErrBlockedAddress
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || Blocked(ip) {
				return ErrBlockedAddress
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          50,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
