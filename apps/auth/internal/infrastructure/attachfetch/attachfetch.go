// Package attachfetch tải tệp đính kèm thư theo URL bằng client chặn SSRF (pkg/safehttp): chỉ https, không userinfo, chặn host và địa chỉ nội bộ lúc nối,
// không theo redirect, timeout và giới hạn byte. Thông điệp lỗi an toàn để trả cho client (không có chi tiết mạng).
package attachfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/pkg/safehttp"
)

var _ application.AttachmentFetcher = (*Fetcher)(nil)

// Fetcher cài application.AttachmentFetcher.
type Fetcher struct {
	client     *http.Client
	httpsOnly  bool
	allowPriv  bool
	timeoutMax time.Duration
}

// New dựng Fetcher; allowPrivate=true tắt chặn địa chỉ nội bộ và cho phép http (chỉ dev/test).
func New(timeout time.Duration, allowPrivate bool) *Fetcher {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Fetcher{client: safehttp.NewClient(timeout, allowPrivate), httpsOnly: !allowPrivate, allowPriv: allowPrivate, timeoutMax: timeout}
}

// Fetch tải URL. Chỉ trả nội dung khi HTTP 2xx và không quá maxBytes.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := safehttp.CheckURL(rawURL, f.httpsOnly)
	if err != nil {
		return nil, errors.New("invalid URL (https is required)")
	}
	if !f.allowPriv && safehttp.HostBlocked(u.Hostname()) {
		return nil, fmt.Errorf("Outbound request to unsafe hostname %s is blocked", u.Hostname())
	}
	ctx, cancel := context.WithTimeout(ctx, f.timeoutMax)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	res, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, safehttp.ErrBlockedAddress) {
			return nil, fmt.Errorf("Outbound request to unsafe address %s is blocked", u.Hostname())
		}
		var ue *url.Error
		if errors.As(err, &ue) && errors.Is(ue.Err, context.DeadlineExceeded) {
			return nil, errors.New("request timed out")
		}
		return nil, errors.New("request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("request failed")
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("body too large")
	}
	return data, nil
}
