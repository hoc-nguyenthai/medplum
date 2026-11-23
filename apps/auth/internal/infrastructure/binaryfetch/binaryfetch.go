// Package binaryfetch đọc nội dung Binary từ dịch vụ FHIR (fhir-server) cho tệp đính kèm thư (E5), bằng chính Bearer token của người gọi
// để quyền, project và securityContext do fhir-server quyết định.
//
// Địa chỉ dịch vụ FHIR do vận hành viên cấu hình (có thể là địa chỉ nội bộ) nên KHÔNG áp chặn SSRF như attachfetch; thay vào đó: token chỉ
// được gửi tới đúng địa chỉ cấu hình, không theo redirect (token không bao giờ tới host khác), không dùng proxy từ môi trường, id đã kiểm
// ở use case, timeout và giới hạn byte. Lỗi trả ra không chứa token, URL hay thân phản hồi.
package binaryfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/application"
)

var _ application.BinarySource = (*Source)(nil)

// Source cài application.BinarySource.
type Source struct {
	base   string // gốc API FHIR, không có dấu / cuối, ví dụ https://fhir.internal/fhir/R4
	client *http.Client
}

// New dựng Source. base phải là URL http(s) tuyệt đối, không userinfo, không query/fragment (kiểm ở cấu hình, lặp lại ở đây).
func New(base string, timeout time.Duration) (*Source, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(base, "?#") {
		return nil, errors.New("binaryfetch: URL dịch vụ FHIR phải là http(s) tuyệt đối, không userinfo, query hay fragment")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // không đi qua proxy của môi trường: token chỉ tới đúng địa chỉ đã cấu hình
	return &Source{
		base: strings.TrimRight(base, "/"),
		client: &http.Client{
			Timeout: timeout, Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, // 3xx là lỗi, không bao giờ theo
		},
	}, nil
}

// Read gọi GET <base>/Binary/<id> với Accept: application/octet-stream (dịch vụ FHIR trả nội dung thô cho Accept không phải JSON, và cả khi
// chưa bật kho: Binary kiểu cũ có data base64 được giải mã).
func (s *Source) Read(ctx context.Context, bearerToken, id string, maxBytes int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/Binary/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, "", errors.New("binaryfetch: không dựng được yêu cầu")
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Accept", "application/octet-stream")
	res, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && errors.Is(ue.Err, context.DeadlineExceeded) {
			return nil, "", errors.New("binaryfetch: request timed out")
		}
		return nil, "", errors.New("binaryfetch: request failed") // url.Error chứa URL: không chuyển tiếp
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return nil, "", application.ErrBinaryNotFound
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, "", application.ErrBinaryForbidden
	case res.StatusCode < 200 || res.StatusCode > 299:
		return nil, "", fmt.Errorf("binaryfetch: HTTP %d", res.StatusCode)
	}
	if res.ContentLength > maxBytes {
		return nil, "", application.ErrBinaryTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return nil, "", errors.New("binaryfetch: request failed")
	}
	if int64(len(data)) > maxBytes {
		return nil, "", application.ErrBinaryTooLarge
	}
	return data, res.Header.Get("Content-Type"), nil
}
