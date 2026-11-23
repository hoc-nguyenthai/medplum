// Package client là HTTP client tối giản cho API FHIR R4 của fhir-server: gắn bearer token, header chọn project của
// super-admin, đọc lỗi OperationOutcome và chỉ đi theo link (phân trang, Content-Location) trên chính máy chủ của mình
// để không bao giờ gửi token tới host khác.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client gọi một fhir-server.
type Client struct {
	Base    *url.URL // gốc API, ví dụ http://localhost:8005/fhir/R4
	Token   string   // bearer token (rỗng: không gửi)
	Project string   // header X-Fhir-Project (super-admin), rỗng: không gửi
	HTTP    *http.Client
}

// New dựng Client từ URL gốc.
func New(base, token, project string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("URL gốc không hợp lệ: %q (cần dạng http://host:port/fhir/R4)", base)
	}
	return &Client{Base: u, Token: token, Project: project, HTTP: &http.Client{Timeout: timeout}}, nil
}

// Result là một phản hồi thành công (2xx).
type Result struct {
	Status int
	Header http.Header
	Body   []byte
}

// APIError là phản hồi lỗi của máy chủ.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }

// Resolve đổi đường dẫn tương đối ("Patient/1?x=y") hoặc URL tuyệt đối do máy chủ trả về thành URL trên máy chủ của
// Client. URL tuyệt đối chỉ lấy phần path và query (host do máy chủ khai báo có thể khác địa chỉ client dùng) và phải
// nằm dưới gốc API.
func (c *Client) Resolve(ref string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimRight(c.Base.Path, "/")
	switch {
	case u.IsAbs() || strings.HasPrefix(ref, "/"):
		if p := strings.TrimRight(u.Path, "/"); p != basePath && !strings.HasPrefix(u.Path, basePath+"/") {
			return "", fmt.Errorf("link %q nằm ngoài %s", ref, basePath)
		}
		out := *c.Base
		out.Path, out.RawPath, out.RawQuery = u.Path, "", u.RawQuery
		return out.String(), nil
	}
	rel := strings.TrimLeft(ref, "/")
	out := *c.Base
	out.Path = basePath + "/" + rel
	out.RawQuery = ""
	if i := strings.IndexByte(rel, '?'); i >= 0 {
		out.Path, out.RawQuery = basePath+"/"+rel[:i], rel[i+1:]
	}
	return out.String(), nil
}

// Do gửi một yêu cầu; ref là đường dẫn tương đối hoặc link của máy chủ. Mã không phải 2xx trả *APIError.
func (c *Client) Do(ctx context.Context, method, ref string, body []byte, headers ...string) (*Result, error) {
	target, err := c.Resolve(ref)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/fhir+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/fhir+json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Project != "" {
		req.Header.Set("X-Fhir-Project", c.Project)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<30))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, &APIError{Status: res.StatusCode, Message: errorMessage(data, res.Status)}
	}
	return &Result{Status: res.StatusCode, Header: res.Header, Body: data}, nil
}

// errorMessage lấy chẩn đoán từ OperationOutcome (hoặc JSON {"error":...}), không thì dùng nguyên văn.
func errorMessage(body []byte, fallback string) string {
	var oc struct {
		Issue []struct {
			Diagnostics string `json:"diagnostics"`
			Details     struct {
				Text string `json:"text"`
			} `json:"details"`
		} `json:"issue"`
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &oc) == nil {
		var parts []string
		for _, i := range oc.Issue {
			m := i.Diagnostics
			if m == "" {
				m = i.Details.Text
			}
			if m != "" {
				parts = append(parts, m)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
		if oc.Error != "" {
			return oc.Error
		}
	}
	if s := strings.TrimSpace(string(body)); s != "" && len(s) < 500 {
		return s
	}
	return fallback
}

// IsStatus cho biết err là APIError với mã status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}
