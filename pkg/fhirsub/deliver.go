package fhirsub

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// delivery là một lần giao thông báo (có thể thử nhiều lần).
type delivery struct {
	subID       string
	endpoint    string
	interaction string
	resource    string // Type/id
	body        []byte
	headers     [][2]string
	secret      string
	attempt     int
}

func newDelivery(e *entry, ev ChangeEvent) *delivery {
	body := []byte(ev.Resource)
	if ev.Interaction == Delete {
		body = []byte("{}") // như Medplum: xóa thì body rỗng, tên resource nằm ở header
	}
	return &delivery{
		subID: e.sub.ID, endpoint: e.sub.Channel.Endpoint, interaction: ev.Interaction,
		resource: ev.ResourceType + "/" + ev.ID, body: body, headers: e.headers, secret: e.secret,
	}
}

var skipHeaders = map[string]bool{"host": true, "content-length": true, "transfer-encoding": true, "connection": true}

func (d *delivery) request() (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, d.endpoint, bytes.NewReader(d.body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/fhir+json")
	req.Header.Set("X-Medplum-Subscription", d.subID)
	req.Header.Set("X-Medplum-Interaction", d.interaction)
	if d.interaction == Delete {
		req.Header.Set("X-Medplum-Deleted-Resource", d.resource)
	}
	for _, h := range d.headers {
		if !skipHeaders[strings.ToLower(h[0])] {
			req.Header.Set(h[0], h[1])
		}
	}
	if d.secret != "" {
		mac := hmac.New(sha256.New, []byte(d.secret))
		mac.Write(d.body)
		req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	return req, nil
}

// retryable: lỗi mạng, 408, 429 và 5xx đáng thử lại; các 4xx còn lại là lỗi của người nhận.
func retryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// attempt thực hiện một lần giao; thất bại thì hẹn thử lại (không chặn worker) hoặc bỏ cuộc.
func (m *Manager) attempt(d *delivery) {
	if m.ctx.Err() != nil {
		return
	}
	d.attempt++
	req, err := d.request()
	if err != nil {
		atomic.AddInt64(&m.stats.Failed, 1)
		slog.Error("fhirsub: không dựng được request", "subscription", d.subID, "error", err)
		return
	}
	req = req.WithContext(m.ctx)
	resp, err := m.client.Do(req)
	status := 0
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		status = resp.StatusCode
		if status >= 200 && status < 300 {
			atomic.AddInt64(&m.stats.Delivered, 1)
			m.clearFailures(d.subID)
			return
		}
	}
	retry := err != nil || retryable(status)
	if errIsBlocked(err) {
		retry = false // địa chỉ bị chặn sẽ không tự hết bị chặn
	}
	if !retry || d.attempt >= m.cfg.MaxAttempts {
		atomic.AddInt64(&m.stats.Failed, 1)
		slog.Warn("fhirsub: giao thất bại, bỏ cuộc", "subscription", d.subID, "resource", d.resource,
			"attempts", d.attempt, "status", status, "error", err)
		m.recordFailure(d)
		return
	}
	delay := m.cfg.BaseDelay << (d.attempt - 1)
	if delay > time.Minute || delay <= 0 {
		delay = time.Minute
	}
	atomic.AddInt64(&m.stats.Retried, 1)
	time.AfterFunc(delay, func() {
		if m.ctx.Err() == nil {
			m.enqueue(d)
		}
	})
}

func errIsBlocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrBlockedAddress.Error())
}
