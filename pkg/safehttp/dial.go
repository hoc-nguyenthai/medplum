package safehttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// lookupNetIP là bộ phân giải (biến để test tiêm kết quả).
var lookupNetIP = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// DialContext trả hàm nối TCP chặn SSRF cho giao thức không phải HTTP (ví dụ SMTP). allowPrivate=true tắt việc chặn (chỉ dev/test).
//
//   - host là IP literal bị chặn: từ chối ngay;
//   - host là tên: phân giải MỌI địa chỉ; rỗng, hoặc BẤT KỲ địa chỉ nào bị chặn thì từ chối cả tên (một tên trả cả IP công cộng lẫn nội bộ không được dùng);
//   - thử lần lượt các địa chỉ còn lại; Dialer.Control kiểm lại địa chỉ thật ngay lúc nối (lưới thứ hai chống DNS rebinding);
//   - tổng thời gian phân giải và nối bị chặn bởi timeout.
func DialContext(allowPrivate bool, timeout time.Duration) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var addrs []netip.Addr
		if ip, perr := netip.ParseAddr(strings.Trim(host, "[]")); perr == nil {
			addrs = []netip.Addr{ip}
		} else {
			if !allowPrivate && HostBlocked(host) {
				return nil, ErrBlockedAddress
			}
			if addrs, err = lookupNetIP(ctx, host); err != nil {
				return nil, err
			}
		}
		if len(addrs) == 0 {
			return nil, errors.New("safehttp: không phân giải được địa chỉ nào")
		}
		if !allowPrivate {
			for _, a := range addrs {
				if Blocked(a) {
					return nil, ErrBlockedAddress
				}
			}
		}
		d := &net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			h, _, err := net.SplitHostPort(address)
			if err != nil {
				return ErrBlockedAddress
			}
			ip, err := netip.ParseAddr(h)
			if err != nil || Blocked(ip) {
				return ErrBlockedAddress
			}
			return nil
		}}
		var last error
		for _, a := range addrs {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}
}
