// Package pwned kiểm tra mật khẩu có trong cơ sở dữ liệu rò rỉ qua API range của Have I Been Pwned (k-anonymity: chỉ
// gửi 5 ký tự đầu của SHA-1, mật khẩu không bao giờ rời máy).
package pwned

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // HIBP yêu cầu SHA-1; không dùng cho mục đích bảo mật khác
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultURL là địa chỉ API range chính thức.
const DefaultURL = "https://api.pwnedpasswords.com/range/"

// HIBP cài application.PwnedChecker.
type HIBP struct {
	URL    string
	Client *http.Client
}

// NewHIBP dựng checker với URL (rỗng = mặc định) và timeout mỗi lần gọi.
func NewHIBP(url string, timeout time.Duration) *HIBP {
	if url == "" {
		url = DefaultURL
	}
	if !strings.HasSuffix(url, "/") {
		url += "/"
	}
	return &HIBP{URL: url, Client: &http.Client{Timeout: timeout}}
}

// Count trả số lần mật khẩu xuất hiện trong dữ liệu rò rỉ. Lỗi mạng hoặc phản hồi lạ là lỗi (fail đóng).
func (h *HIBP) Count(ctx context.Context, password string) (int, error) {
	sum := sha1.Sum([]byte(password)) //nolint:gosec
	full := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := full[:5], full[5:]
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL+prefix, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Add-Padding", "true") // thêm dòng giả để độ dài phản hồi không lộ số kết quả thật
	resp, err := h.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("pwned: dịch vụ trả %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		sfx, cnt, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(sfx, suffix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(cnt))
		if err != nil {
			return 0, fmt.Errorf("pwned: số đếm không hợp lệ %q", cnt)
		}
		return n, nil // dòng padding có số đếm 0
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, nil
}

// Disabled là checker luôn trả 0: chỉ dùng ở local và test (cấu hình cấm tắt ở staging/production).
type Disabled struct{}

// Count luôn trả 0.
func (Disabled) Count(context.Context, string) (int, error) { return 0, nil }
