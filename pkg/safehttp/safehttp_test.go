package safehttp

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestBlocked(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.8.8.8", "10.0.0.1", "172.16.5.5", "172.31.255.255", "192.168.1.1", "169.254.169.254", "169.254.0.1",
		"100.64.0.1", "100.127.255.255", "0.0.0.0", "0.1.2.3", "0.250.250.254", "0.255.255.255", "::ffff:0.250.250.254", "224.0.0.1", "::1", "fe80::1", "fc00::1", "fd12:3456::1", "::", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
		// dải bổ sung so với bản gốc của fhirsub
		"198.18.0.1", "198.19.255.255", "192.0.0.1", "192.0.2.5", "198.51.100.7", "203.0.113.9", "240.0.0.1", "255.255.255.255",
		"64:ff9b::7f00:1", "64:ff9b::a00:1", "2002:7f00:1::1", "2001:db8::1", "100::1",
	} {
		if !Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s phải bị chặn", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.32.0.1", "100.128.0.1", "198.20.0.1", "198.17.255.255", "1.0.0.1", "2606:4700::1111", "::ffff:8.8.8.8", "2a00:1450::1"} {
		if Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s không được bị chặn", s)
		}
	}
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{"http://example.org/hook", "https://example.org:8443/a?b=c"} {
		if _, err := CheckURL(ok, false); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ftp://example.org/x", "file:///etc/passwd", "http://", "//example.org", "http://user:pw@example.org/", "javascript:alert(1)", "example.org/hook"} {
		if _, err := CheckURL(bad, false); err == nil {
			t.Errorf("%q phải bị từ chối", bad)
		}
	}
	if _, err := CheckURL("http://example.org/", true); err == nil {
		t.Error("httpsOnly phải từ chối http")
	}
	if _, err := CheckURL("https://example.org/", true); err != nil {
		t.Error(err)
	}
}

func TestClientBlocksLoopbackAtConnectTimeAndDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, "/x", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	_, err := NewClient(2*time.Second, false).Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), ErrBlockedAddress.Error()) {
		t.Fatalf("loopback phải bị chặn lúc kết nối: %v", err)
	}
	res, err := NewClient(2*time.Second, true).Get(srv.URL + "/redir")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Errorf("không được theo redirect: %d", res.StatusCode)
	}
	// tên miền trỏ về loopback cũng bị chặn: kiểm trên IP đã phân giải, không trên tên
	if ips, err := net.LookupIP("localhost"); err == nil && len(ips) > 0 {
		_, err := NewClient(2*time.Second, false).Get("http://localhost:" + strings.Split(srv.Listener.Addr().String(), ":")[1])
		if err == nil || !errors.Is(err, ErrBlockedAddress) && !strings.Contains(err.Error(), ErrBlockedAddress.Error()) {
			t.Errorf("localhost: %v", err)
		}
	}
}

func TestHostBlocked(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "192.168.0.1": true, "172.16.5.5": true, "169.254.169.254": true, "100.64.0.1": true, "0.0.0.0": true,
		"::1": true, "[::1]": true, "fe80::1": true, "fc00::1": true, "::ffff:127.0.0.1": true,
		"localhost": true, "LOCALHOST": true, "localhost.": true, "localhost.localdomain": true, "app.localhost": true, "x.y.LOCALHOST": true,
		"example.org": false, "idp.example.org": false, "8.8.8.8": false, "2606:4700::1111": false, "notlocalhost": false, "localhost.example.org": false, "": false,
	} {
		if got := HostBlocked(host); got != want {
			t.Errorf("HostBlocked(%q) = %v, mong %v", host, got, want)
		}
	}
}
