// Package recaptcha cài cổng application.Recaptcha bằng siteverify của Google.
package recaptcha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultURL là điểm xác minh chính thức.
const DefaultURL = "https://www.google.com/recaptcha/api/siteverify"

// maxResponse chặn phản hồi lớn bất thường.
const maxResponse = 64 << 10

// Google gọi siteverify. Secret và token đi trong BODY của POST (Medplum đặt trong query string, vào log của proxy).
type Google struct {
	URL    string
	Client *http.Client
}

// New dựng bộ xác minh; endpoint rỗng dùng DefaultURL, timeout <= 0 là 3 giây. Không theo chuyển hướng: secret không được gửi sang nơi khác.
func New(endpoint string, timeout time.Duration) *Google {
	if endpoint == "" {
		endpoint = DefaultURL
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Google{URL: endpoint, Client: &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Verify trả true khi Google xác nhận token. Mọi lỗi (mạng, mã khác 200, JSON hỏng) là lỗi, không phải false: fail đóng.
func (g *Google) Verify(ctx context.Context, secret, token string) (bool, error) {
	form := url.Values{"secret": {secret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := g.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("recaptcha: %w", unwrapURLError(err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false, fmt.Errorf("recaptcha: mã trạng thái %d", res.StatusCode)
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponse)).Decode(&out); err != nil {
		return false, fmt.Errorf("recaptcha: phản hồi không đọc được: %w", err)
	}
	return out.Success, nil
}

// unwrapURLError bỏ URL khỏi lỗi của net/http để không đưa URL vào log.
func unwrapURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}
