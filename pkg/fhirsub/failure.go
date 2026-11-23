package fhirsub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/core"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Trigger là một ngưỡng tự tắt Subscription: đủ MaxConsecutiveFailures sự kiện giao thất bại (đã hết lượt thử) trong
// TimeWindowSeconds giây gần nhất, kể từ lần giao thành công cuối, thì tắt. Cùng dạng với subscriptionAutoDisable của
// Medplum (S/config/types.ts).
type Trigger struct {
	MaxConsecutiveFailures int `json:"maxConsecutiveFailures"`
	TimeWindowSeconds      int `json:"timeWindowSeconds"`
}

func (t Trigger) window() time.Duration { return time.Duration(t.TimeWindowSeconds) * time.Second }

// FailureTracker đếm các sự kiện giao thất bại của từng Subscription.
type FailureTracker interface {
	// Record ghi một thất bại và trả về trigger đầu tiên (theo thứ tự trong triggers) đã đạt ngưỡng cùng số thất bại trong
	// cửa sổ của nó; nil nếu chưa trigger nào đạt.
	Record(ctx context.Context, subID string, triggers []Trigger) (fired *Trigger, count int, err error)
	// Clear xóa bộ đếm (sau một lần giao thành công hoặc sau khi tắt).
	Clear(ctx context.Context, subID string) error
}

// MemoryFailureTracker là FailureTracker trong bộ nhớ của một tiến trình: nhiều replica thì mỗi replica đếm riêng.
type MemoryFailureTracker struct {
	mu  sync.Mutex
	now func() time.Time
	ts  map[string][]time.Time
}

// NewMemoryFailureTracker tạo tracker bộ nhớ; now nil thì dùng time.Now.
func NewMemoryFailureTracker(now func() time.Time) *MemoryFailureTracker {
	if now == nil {
		now = time.Now
	}
	return &MemoryFailureTracker{now: now, ts: map[string][]time.Time{}}
}

func (t *MemoryFailureTracker) Record(_ context.Context, subID string, triggers []Trigger) (*Trigger, int, error) {
	if len(triggers) == 0 {
		return nil, 0, nil
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	// Tỉa theo cửa sổ lớn nhất, như ZREMRANGEBYSCORE của Medplum.
	cutoff := now.Add(-maxWindow(triggers))
	kept := t.ts[subID][:0]
	for _, x := range t.ts[subID] {
		if x.After(cutoff) {
			kept = append(kept, x)
		}
	}
	kept = append(kept, now)
	t.ts[subID] = kept
	for i := range triggers {
		start := now.Add(-triggers[i].window())
		n := 0
		for _, x := range kept {
			if !x.Before(start) {
				n++
			}
		}
		if n >= triggers[i].MaxConsecutiveFailures {
			return &triggers[i], n, nil
		}
	}
	return nil, 0, nil
}

func (t *MemoryFailureTracker) Clear(_ context.Context, subID string) error {
	t.mu.Lock()
	delete(t.ts, subID)
	t.mu.Unlock()
	return nil
}

func maxWindow(triggers []Trigger) time.Duration {
	var w time.Duration
	for _, t := range triggers {
		w = max(w, t.window())
	}
	return w
}

// recordFailure ghi thất bại của một lần giao đã bỏ cuộc và tắt Subscription nếu đạt ngưỡng. Lỗi chỉ được log.
func (m *Manager) recordFailure(d *delivery) {
	if len(m.cfg.AutoDisable) == 0 {
		return
	}
	fired, count, err := m.cfg.Failures.Record(m.ctx, d.subID, m.cfg.AutoDisable)
	if err != nil {
		slog.Error("fhirsub: không ghi được thất bại để tự tắt Subscription", "subscription", d.subID, "error", err)
		return
	}
	if fired != nil {
		m.autoDisable(d.subID, *fired, count)
	}
}

// clearFailures xóa bộ đếm sau một lần giao thành công.
func (m *Manager) clearFailures(subID string) {
	if len(m.cfg.AutoDisable) == 0 {
		return
	}
	if err := m.cfg.Failures.Clear(m.ctx, subID); err != nil {
		slog.Error("fhirsub: không xóa được bộ đếm thất bại", "subscription", subID, "error", err)
	}
}

// autoDisable chuyển Subscription đang active sang off kèm error, tương đương PATCH test/replace/add của Medplum
// (S/workers/subscription.ts autoDisableSubscription): đọc rồi ghi có If-Match, nên Subscription đã bị đổi trạng thái
// hoặc bị sửa song song thì không bị ghi đè. Chưa tạo AuditEvent (Go chưa lưu AuditEvent vào kho).
func (m *Manager) autoDisable(subID string, t Trigger, count int) {
	ctx := m.ctx
	msg := fmt.Sprintf("Automatically disabled after %d consecutive failed events in the last %d seconds", count, t.TimeWindowSeconds)
	res, err := m.repo.ReadResource(ctx, "Subscription", subID)
	if err != nil {
		slog.Error("fhirsub: không đọc được Subscription để tự tắt", "subscription", subID, "error", err)
		return
	}
	sub, ok := res.(*fhir.Subscription)
	if !ok || sub.Status != "active" {
		return // đã bị tắt hoặc đổi trạng thái: không ghi đè
	}
	var version, project string
	if sub.Meta != nil {
		version, project = sub.Meta.VersionId, sub.Meta.Project
	}
	sub.Status, sub.Error = "off", msg
	if _, err := m.repo.UpdateResource(ctx, sub, fhirrouter.UpdateOptions{IfMatch: version}); err != nil {
		var fe *fhirrouter.Error
		if errors.As(err, &fe) && fe.Status == http.StatusPreconditionFailed {
			return // vừa bị sửa song song: lần thất bại sau sẽ xét lại
		}
		slog.Error("fhirsub: không tắt được Subscription", "subscription", subID, "error", err)
		return
	}
	m.clearFailures(subID)
	m.Invalidate()
	slog.Warn("fhirsub: tự tắt Subscription vì giao thất bại lặp lại", "subscription", subID, "project", project,
		"failureCount", count, "timeWindowSeconds", t.TimeWindowSeconds)
}

// RedisFailureTracker là FailureTracker dùng chung giữa các replica: mỗi Subscription một sorted set
// `{prefix}:<id>` với điểm là thời điểm thất bại (mili giây), cùng thuật toán với Medplum
// (S/workers/subscription-failure-tracker.ts).
type RedisFailureTracker struct {
	c      redis.UniversalClient
	prefix string
	now    func() time.Time
}

// NewRedisFailureTracker tạo tracker Redis; prefix rỗng thì dùng "trivita:sub:failures", now nil thì dùng time.Now.
func NewRedisFailureTracker(c redis.UniversalClient, prefix string, now func() time.Time) *RedisFailureTracker {
	if prefix == "" {
		prefix = "trivita:sub:failures"
	}
	if now == nil {
		now = time.Now
	}
	return &RedisFailureTracker{c: c, prefix: prefix, now: now}
}

func (t *RedisFailureTracker) key(subID string) string { return t.prefix + ":" + subID }

func (t *RedisFailureTracker) Record(ctx context.Context, subID string, triggers []Trigger) (*Trigger, int, error) {
	if len(triggers) == 0 {
		return nil, 0, nil
	}
	now := t.now()
	ms := now.UnixMilli()
	key := t.key(subID)
	maxW := maxWindow(triggers)
	// Tỉa, thêm, đếm từng cửa sổ và đặt hạn trong một pipeline (MULTI) như Medplum.
	pipe := t.c.TxPipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.Add(-maxW).UnixMilli(), 10))
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(ms), Member: fmt.Sprintf("%d:%s", ms, core.NewID())})
	counts := make([]*redis.IntCmd, len(triggers))
	for i, tr := range triggers {
		counts[i] = pipe.ZCount(ctx, key, strconv.FormatInt(now.Add(-tr.window()).UnixMilli(), 10), "+inf")
	}
	pipe.Expire(ctx, key, maxW)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, 0, err
	}
	for i := range triggers {
		if n := int(counts[i].Val()); n >= triggers[i].MaxConsecutiveFailures {
			return &triggers[i], n, nil
		}
	}
	return nil, 0, nil
}

func (t *RedisFailureTracker) Clear(ctx context.Context, subID string) error {
	return t.c.Unlink(ctx, t.key(subID)).Err()
}
