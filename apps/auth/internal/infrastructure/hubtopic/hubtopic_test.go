package hubtopic_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"trivita-fhir/apps/auth/internal/infrastructure/hubtopic"
)

func TestMemoryTopicsAreStablePerSubject(t *testing.T) {
	tp := hubtopic.NewMemory()
	ctx := context.Background()
	a1, err := tp.TopicFor(ctx, "user-a")
	a2, _ := tp.TopicFor(ctx, "user-a")
	b, _ := tp.TopicFor(ctx, "user-b")
	if err != nil || a1 == "" || a1 != a2 || a1 == b {
		t.Errorf("a1=%q a2=%q b=%q err=%v", a1, a2, b, err)
	}
	if len(a1) != 36 || strings.Count(a1, "-") != 4 {
		t.Errorf("topic phải là UUID: %q", a1)
	}
	if err := tp.Close(); err != nil {
		t.Error(err)
	}
}

// Nhiều instance auth chung Redis phải cấp CÙNG topic cho một chủ thể (nếu không hai ứng dụng của một người không tìm thấy nhau).
func TestRedisTopicsAreSharedAcrossInstancesAndExpire(t *testing.T) {
	mr := miniredis.RunT(t)
	a, err := hubtopic.NewRedis("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := hubtopic.NewRedis("redis://" + mr.Addr())
	defer a.Close()
	defer b.Close()
	ctx := context.Background()
	ta, err := a.TopicFor(ctx, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.TopicFor(ctx, "user-1")
	if err != nil || ta != tb {
		t.Fatalf("hai instance cho hai topic khác nhau: %q %q %v", ta, tb, err)
	}
	if other, _ := b.TopicFor(ctx, "user-2"); other == ta {
		t.Error("hai chủ thể không được chung topic")
	}
	// mọi khóa có hạn (không để dữ liệu tồn vô hạn) và mỗi lần hỏi gia hạn
	keys := mr.Keys()
	if len(keys) != 2 {
		t.Fatalf("khóa: %v", keys)
	}
	for _, k := range keys {
		if mr.TTL(k) <= 0 {
			t.Errorf("khóa %s không có hạn", k)
		}
	}
	mr.FastForward(59 * time.Minute)
	if again, _ := a.TopicFor(ctx, "user-1"); again != ta {
		t.Error("còn trong lease phải ổn định")
	}
	mr.FastForward(59 * time.Minute) // lần hỏi trước đã gia hạn
	if again, _ := a.TopicFor(ctx, "user-1"); again != ta {
		t.Error("mỗi lần hỏi phải gia hạn lease")
	}
	mr.FastForward(61 * time.Minute) // quá hạn không ai hỏi
	if again, _ := a.TopicFor(ctx, "user-1"); again == ta {
		t.Error("quá lease không xin token thì phải sinh topic mới")
	}
}

func TestRedisTopicsFailWithinTimeoutWhenRedisIsDown(t *testing.T) {
	mr := miniredis.RunT(t)
	tp, _ := hubtopic.NewRedis("redis://" + mr.Addr())
	defer tp.Close()
	mr.Close()
	start := time.Now()
	if topic, err := tp.TopicFor(context.Background(), "u"); err == nil || topic != "" {
		t.Errorf("Redis chết phải lỗi: %q %v", topic, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("lấy topic treo %v khi Redis chết", time.Since(start))
	}
}

func TestNewRedisRejectsBadURL(t *testing.T) {
	if _, err := hubtopic.NewRedis("http://nope"); err == nil {
		t.Error("URL không phải redis:// phải bị từ chối")
	}
}

// Redis nhận kết nối nhưng không bao giờ trả lời (mạng treo): việc lấy topic phải bỏ cuộc sau hạn của nó, không treo việc cấp token.
func TestRedisTopicsTimeOutWhenRedisHangs(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // giữ mở, không đọc, không ghi
		}
	}()
	tp, _ := hubtopic.NewRedis("redis://" + ln.Addr().String())
	defer tp.Close()
	start := time.Now()
	if _, err := tp.TopicFor(context.Background(), "u"); err == nil {
		t.Fatal("Redis treo phải trả lỗi")
	}
	if d := time.Since(start); d < time.Second || d > 3*time.Second {
		t.Errorf("hạn gọi Redis = %v, muốn khoảng 2 giây (callTimeout)", d)
	}
}
