package fhirsub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
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

// outcome là kết quả của một lần thử giao.
type outcome int

const (
	delivered outcome = iota // 2xx
	retry                    // lỗi mạng, 408, 429, 5xx: đáng thử lại
	giveUp                   // lỗi không thử lại được (4xx khác, địa chỉ bị chặn, request hỏng)
	canceled                 // ctx bị hủy giữa chừng (tắt máy): chưa biết kết quả
)

// try thực hiện MỘT lần giao tới endpoint (tăng d.attempt) và phân loại kết quả.
func (m *Manager) try(ctx context.Context, d *delivery) (outcome, int, error) {
	d.attempt++
	req, err := d.request()
	if err != nil {
		slog.Error("fhirsub: không dựng được request", "subscription", d.subID, "error", err)
		return giveUp, 0, err
	}
	resp, err := m.client.Do(req.WithContext(ctx))
	status := 0
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		status = resp.StatusCode
		if status >= 200 && status < 300 {
			return delivered, status, nil
		}
	}
	if ctx.Err() != nil {
		return canceled, status, err
	}
	if errIsBlocked(err) {
		return giveUp, status, err // địa chỉ bị chặn sẽ không tự hết bị chặn
	}
	if err != nil || retryable(status) {
		return retry, status, err
	}
	return giveUp, status, err
}

// attempt thực hiện một lần giao (chế độ hàng đợi); thất bại thì hẹn thử lại (không chặn worker) hoặc bỏ cuộc.
func (m *Manager) attempt(d *delivery) {
	if m.ctx.Err() != nil {
		return
	}
	out, status, err := m.try(m.ctx, d)
	switch out {
	case delivered:
		atomic.AddInt64(&m.stats.Delivered, 1)
		m.clearFailures(d.subID)
		return
	case canceled:
		return
	}
	if out == giveUp || d.attempt >= m.cfg.MaxAttempts {
		m.giveUp(d, status, err)
		return
	}
	atomic.AddInt64(&m.stats.Retried, 1)
	time.AfterFunc(m.cfg.backoff(d.attempt), func() {
		if m.ctx.Err() == nil {
			m.enqueue(d)
		}
	})
}

func (m *Manager) giveUp(d *delivery, status int, err error) {
	atomic.AddInt64(&m.stats.Failed, 1)
	slog.Warn("fhirsub: giao thất bại, bỏ cuộc", "subscription", d.subID, "resource", d.resource,
		"attempts", d.attempt, "status", status, "error", err)
	m.recordFailure(d)
}

// deliverSync giao một thông báo tại chỗ, thử lại tới MaxAttempts (chờ theo backoff). Trả true khi đã xong (giao được hoặc bỏ cuộc), false khi ctx bị hủy giữa chừng
// (tắt máy: thông báo chưa xong, message phải ở lại PEL để nơi khác giao).
func (m *Manager) deliverSync(ctx context.Context, d *delivery) bool {
	for {
		out, status, err := m.try(ctx, d)
		switch out {
		case delivered:
			atomic.AddInt64(&m.stats.Delivered, 1)
			m.clearFailures(d.subID)
			return true
		case canceled:
			return false
		}
		if out == giveUp || d.attempt >= m.cfg.MaxAttempts {
			m.giveUp(d, status, err)
			return true
		}
		atomic.AddInt64(&m.stats.Retried, 1)
		select {
		case <-time.After(m.cfg.backoff(d.attempt)):
		case <-ctx.Done():
			return false
		}
	}
}

// deliverAll giao song song (tối đa Workers) mọi thông báo của một sự kiện và chờ xong. Trả lỗi nếu còn thông báo dở dang vì tắt máy, để broker không ACK.
func (m *Manager) deliverAll(ctx context.Context, ds []*delivery) error {
	if len(ds) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel) // Manager.Stop hủy các lần giao đang chạy
	defer stop()
	sem := make(chan struct{}, m.cfg.Workers)
	var wg sync.WaitGroup
	var incomplete atomic.Bool
	for _, d := range ds {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			incomplete.Store(true)
			continue
		}
		wg.Add(1)
		go func(d *delivery) {
			defer wg.Done()
			defer func() { <-sem }()
			if !m.deliverSync(ctx, d) {
				incomplete.Store(true)
			}
		}(d)
	}
	wg.Wait()
	if incomplete.Load() {
		return errors.New("fhirsub: dừng giữa lúc giao rest-hook, sự kiện sẽ được giao lại")
	}
	return nil
}

func errIsBlocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), ErrBlockedAddress.Error())
}
