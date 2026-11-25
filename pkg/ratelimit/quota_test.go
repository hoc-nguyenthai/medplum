package ratelimit

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// quotaCases chạy cùng một bộ kiểm cho Memory và Redis để hai bản hành xử như nhau.
func quotaCases(t *testing.T, q Quota, advance func(time.Duration)) {
	t.Helper()
	w := time.Minute
	st, err := q.Consume("a", 3, 10, w)
	if err != nil || !st.Allowed || st.Remaining != 7 || st.Limit != 10 || st.Reset <= 0 || st.Reset > w {
		t.Fatalf("lần 1: %+v %v", st, err)
	}
	if st, _ = q.Consume("a", 7, 10, w); !st.Allowed || st.Remaining != 0 {
		t.Fatalf("vừa đủ: %+v", st)
	}
	if st, _ = q.Consume("a", 1, 10, w); st.Allowed || st.Remaining != 0 {
		t.Fatalf("hết điểm phải bị từ chối: %+v", st)
	}
	if st, _ = q.Peek("a", 10, w); !st.Allowed || st.Remaining != 0 {
		t.Fatalf("peek không đổi: %+v", st)
	}
	if st, _ = q.Consume("b", 1, 10, w); !st.Allowed || st.Remaining != 9 {
		t.Fatalf("khóa khác độc lập: %+v", st)
	}
	// bị từ chối không trừ điểm (cost 20 hạ về 10, nhưng b đã dùng 1)
	if st, _ = q.Consume("b", 20, 10, w); st.Allowed {
		t.Fatalf("vượt điểm còn lại phải bị từ chối: %+v", st)
	}
	if st, _ = q.Peek("b", 10, w); st.Remaining != 9 {
		t.Fatalf("từ chối không được trừ: %+v", st)
	}
	q.Consume("z", 3, 10, w)
	advance(w + time.Second)
	if st, _ = q.Peek("z", 10, w); st.Remaining != 10 || st.Reset != w {
		t.Fatalf("peek sau cửa sổ phải thấy đầy điểm: %+v", st)
	}
	if st, _ = q.Consume("a", 4, 10, w); !st.Allowed || st.Remaining != 6 {
		t.Fatalf("sau cửa sổ phải làm mới: %+v", st)
	}
	// thao tác nặng hơn cả hạn mức vẫn chạy được một lần mỗi cửa sổ trên khóa trống
	if st, _ = q.Consume("heavy", 50, 10, w); !st.Allowed || st.Remaining != 0 {
		t.Fatalf("cost > limit: %+v", st)
	}
	if st, _ = q.Consume("heavy", 1, 10, w); st.Allowed {
		t.Fatalf("lần hai trong cửa sổ phải bị từ chối: %+v", st)
	}
	if st, _ = q.Peek("chua-co", 10, w); !st.Allowed || st.Remaining != 10 || st.Reset != w {
		t.Fatalf("peek khóa chưa có: %+v", st)
	}
}

func TestMemoryQuota(t *testing.T) {
	q := NewMemoryQuota()
	now := time.Unix(1000, 0)
	q.now = func() time.Time { return now }
	quotaCases(t, q, func(d time.Duration) { now = now.Add(d) })
}

func TestRedisQuota(t *testing.T) {
	mr := miniredis.RunT(t)
	q := NewRedisQuota(redis.NewClient(&redis.Options{Addr: mr.Addr()}), "t:")
	quotaCases(t, q, func(d time.Duration) { mr.FastForward(d) })
	if q.Errors() != 0 {
		t.Fatalf("không được rơi về bộ nhớ: %d", q.Errors())
	}
}

func TestRedisQuotaSharedAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	a := NewRedisQuota(redis.NewClient(&redis.Options{Addr: mr.Addr()}), "t:")
	b := NewRedisQuota(redis.NewClient(&redis.Options{Addr: mr.Addr()}), "t:")
	a.Consume("k", 6, 10, time.Minute)
	if st, _ := b.Consume("k", 6, 10, time.Minute); st.Allowed {
		t.Fatalf("hai replica phải dùng chung điểm: %+v", st)
	}
	if st, _ := b.Peek("k", 10, time.Minute); st.Remaining != 4 {
		t.Fatalf("peek: %+v", st)
	}
}

func TestRedisQuotaFallsBackToMemoryWhenRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	q := NewRedisQuota(redis.NewClient(&redis.Options{Addr: mr.Addr()}), "t:")
	mr.Close()
	st, err := q.Consume("k", 4, 10, time.Minute)
	if err != nil || !st.Allowed || st.Remaining != 6 {
		t.Fatalf("fail-open dùng bộ nhớ: %+v %v", st, err)
	}
	if st, _ = q.Consume("k", 7, 10, time.Minute); st.Allowed {
		t.Fatalf("bộ nhớ vẫn phải giới hạn: %+v", st)
	}
	if st, _ = q.Peek("k", 10, time.Minute); st.Remaining != 6 {
		t.Fatalf("peek bộ nhớ: %+v", st)
	}
	if q.Errors() == 0 {
		t.Fatal("phải đếm lỗi Redis")
	}
}
