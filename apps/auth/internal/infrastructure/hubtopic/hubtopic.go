// Package hubtopic cài application.HubTopics bằng kho topic của pkg/fhircast: Redis (dùng chung nhiều instance) hoặc bộ nhớ (một instance).
// Topic của một chủ thể ổn định chừng nào còn xin token ít nhất mỗi lease (1 giờ, như Medplum): mỗi lần hỏi gia hạn hạn của khóa.
package hubtopic

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"trivita-fhir/pkg/fhircast"
)

// callTimeout là hạn mỗi lần hỏi Redis: lấy token không được treo khi Redis chậm.
const callTimeout = 2 * time.Second

// Topics là application.HubTopics.
type Topics struct {
	store  fhircast.Store
	client *redis.Client
}

// NewMemory dựng kho trong bộ nhớ (CHỈ đúng với một instance: nhiều instance sẽ cấp topic khác nhau cho cùng một chủ thể).
func NewMemory() *Topics {
	return &Topics{store: fhircast.NewMemoryStore(fhircast.StoreOptions{})}
}

// NewRedis dựng kho trên Redis (redis:// hoặc rediss://). Không kết nối ngay: lỗi kết nối hiện ra ở lần cấp token đầu tiên.
func NewRedis(rawURL string) (*Topics, error) {
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("hubtopic: URL Redis không hợp lệ: %w", err)
	}
	// Hạn phải đặt NGAY trên client: deadline của context không áp cho bước khởi tạo kết nối của go-redis (đo được: cuộc gọi tới một Redis
	// treo mất 5 giây dù context 2 giây). Không thử lại: việc cấp token không được chờ lâu vì một tính năng phụ.
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = callTimeout, callTimeout, callTimeout
	opt.ContextTimeoutEnabled = true
	opt.MaxRetries = -1
	c := redis.NewClient(opt)
	return &Topics{store: fhircast.NewRedisStore(c, "", fhircast.StoreOptions{}), client: c}, nil
}

// TopicFor trả topic ổn định của chủ thể (tạo nếu chưa có) và gia hạn nó.
func (t *Topics) TopicFor(ctx context.Context, subject string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return t.store.UserTopic(ctx, subject)
}

// Close đóng kết nối Redis (nếu có).
func (t *Topics) Close() error {
	if t.client != nil {
		return t.client.Close()
	}
	return nil
}
