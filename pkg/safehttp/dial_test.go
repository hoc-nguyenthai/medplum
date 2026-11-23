package safehttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestDialContextBlocksInternalTargets(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	addr := ln.Addr().String()
	dial := DialContext(false, 2*time.Second)
	for _, target := range []string{addr, "localhost:25", "[::1]:25", "10.0.0.1:25", "169.254.169.254:25", "[::ffff:10.0.0.1]:25", "198.18.0.1:25", "0.0.0.0:25", "foo.localhost:25"} {
		if c, err := dial(context.Background(), "tcp", target); !errors.Is(err, ErrBlockedAddress) {
			if c != nil {
				c.Close()
			}
			t.Errorf("%s: phải bị chặn, được %v", target, err)
		}
	}
	// allowPrivate: nối được tới loopback (dev/test)
	c, err := DialContext(true, 2*time.Second)(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("allowPrivate: %v", err)
	}
	c.Close()
	// địa chỉ sai dạng
	if _, err := dial(context.Background(), "tcp", "khong-co-cong"); err == nil {
		t.Error("địa chỉ không có cổng phải lỗi")
	}
	// context đã hủy
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DialContext(true, time.Second)(ctx, "tcp", addr); err == nil {
		t.Error("context hủy phải lỗi")
	}
}

// Một tên trả cả địa chỉ công cộng lẫn nội bộ phải bị từ chối TOÀN BỘ, không được nối thử địa chỉ công cộng (dù nội bộ đứng trước).
func TestDialContextRejectsNamesWithAnyInternalAddress(t *testing.T) {
	old := lookupNetIP
	defer func() { lookupNetIP = old }()
	for name, addrs := range map[string][]netip.Addr{
		"nội bộ trước":    {netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("8.8.8.8")},
		"công cộng trước": {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")},
	} {
		lookupNetIP = func(context.Context, string) ([]netip.Addr, error) { return addrs, nil }
		_, err := DialContext(false, 200*time.Millisecond)(context.Background(), "tcp", "mixed.example.org:25")
		if !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: phải bị chặn hoàn toàn, được %v", name, err)
		}
	}
	// không phân giải được địa chỉ nào
	lookupNetIP = func(context.Context, string) ([]netip.Addr, error) { return nil, nil }
	if _, err := DialContext(false, time.Second)(context.Background(), "tcp", "trong.example.org:25"); err == nil || errors.Is(err, ErrBlockedAddress) {
		t.Errorf("rỗng: %v", err)
	}
	// lỗi phân giải đi nguyên
	boom := errors.New("không phân giải được")
	lookupNetIP = func(context.Context, string) ([]netip.Addr, error) { return nil, boom }
	if _, err := DialContext(false, time.Second)(context.Background(), "tcp", "loi.example.org:25"); !errors.Is(err, boom) {
		t.Errorf("lỗi phân giải: %v", err)
	}
	// tên nội bộ rõ ràng bị chặn TRƯỚC khi phân giải
	called := false
	lookupNetIP = func(context.Context, string) ([]netip.Addr, error) { called = true; return nil, nil }
	if _, err := DialContext(false, time.Second)(context.Background(), "tcp", "db.localhost:25"); !errors.Is(err, ErrBlockedAddress) || called {
		t.Errorf("*.localhost phải bị chặn trước khi phân giải: %v called=%v", err, called)
	}
}
