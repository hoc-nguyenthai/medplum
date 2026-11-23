package fhirsub

import (
	"net/netip"
	"testing"
)

func TestBlockedAddresses(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.8.8.8", "10.0.0.1", "172.16.5.5", "172.31.255.255", "192.168.1.1", "169.254.169.254", "169.254.0.1",
		"100.64.0.1", "100.127.255.255", "0.0.0.0", "224.0.0.1", "::1", "fe80::1", "fc00::1", "fd12:3456::1", "::", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
	} {
		if !blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s phải bị chặn", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.32.0.1", "100.128.0.1", "2606:4700::1111", "::ffff:8.8.8.8"} {
		if blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s không được bị chặn", s)
		}
	}
}

func TestCheckEndpoint(t *testing.T) {
	for _, ok := range []string{"http://example.org/hook", "https://example.org:8443/a?b=c"} {
		if _, err := checkEndpoint(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ftp://example.org/x", "file:///etc/passwd", "gopher://x", "http://", "//example.org", "http://user:pw@example.org/", "javascript:alert(1)", "example.org/hook"} {
		if _, err := checkEndpoint(bad); err == nil {
			t.Errorf("%q phải bị từ chối", bad)
		}
	}
}
