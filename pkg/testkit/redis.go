package testkit

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
)

// Redis 7.4 đã được kiểm với toàn bộ test messaging/fhirsub/fhircast; đổi bằng TESTKIT_REDIS_IMAGE.
const defaultRedisImage = "redis:7.4-alpine"

// RedisInstance là một Redis riêng của test.
type RedisInstance struct {
	Addr string // host:port, dùng từ máy chạy test
	URL  string // redis://host:port
	// InternalAddr là host:port dùng từ container khác trong cùng Network (rỗng nếu không gắn Network).
	InternalAddr string
	c            *redis.RedisContainer
}

// Network là mạng Docker để các container nói chuyện với nhau bằng tên (ví dụ service gọi redis:6379).
type Network struct {
	Name string
	net  *testcontainers.DockerNetwork
}

// NewNetwork tạo mạng riêng cho test, xóa khi test xong.
func NewNetwork(t testing.TB) *Network {
	t.Helper()
	RequireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), StartTimeout)
	defer cancel()
	n, err := tcnetwork.New(ctx)
	if err != nil {
		t.Fatalf("testkit: tạo network: %v", err)
	}
	t.Cleanup(func() { _ = n.Remove(context.Background()) })
	return &Network{Name: n.Name, net: n}
}

// RedisOption tùy chỉnh Redis.
type RedisOption func(*redisCfg)

type redisCfg struct {
	net   *Network
	alias string
}

// OnNetwork gắn Redis vào network với tên alias (service khác dùng alias:6379).
func OnNetwork(n *Network, alias string) RedisOption {
	return func(c *redisCfg) { c.net, c.alias = n, alias }
}

// StartRedis khởi động một Redis riêng cho test và dọn khi test xong. Lỗi khởi động in kèm log của container.
func StartRedis(t testing.TB, opts ...RedisOption) *RedisInstance {
	t.Helper()
	RequireDocker(t)
	var cfg redisCfg
	for _, o := range opts {
		o(&cfg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), StartTimeout)
	defer cancel()
	cust := []testcontainers.ContainerCustomizer{}
	if cfg.net != nil {
		cust = append(cust, tcnetwork.WithNetwork([]string{cfg.alias}, cfg.net.net))
	}
	c, err := redis.Run(ctx, env("TESTKIT_REDIS_IMAGE", defaultRedisImage), cust...)
	if c != nil {
		testcontainers.CleanupContainer(t, c)
	}
	if err != nil {
		t.Fatalf("testkit: khởi động Redis: %s", withLogs(ctx, c, err))
	}
	u, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("testkit: địa chỉ Redis: %v", err)
	}
	ri := &RedisInstance{URL: u, Addr: strings.TrimPrefix(u, "redis://"), c: c}
	if cfg.net != nil {
		ri.InternalAddr = cfg.alias + ":6379"
	}
	return ri
}

// Stop dừng container Redis (mô phỏng Redis chết); Start bật lại cùng container (cổng host có thể đổi: dùng lại Addr()).
func (r *RedisInstance) Stop(t testing.TB) {
	t.Helper()
	if err := r.c.Stop(context.Background(), nil); err != nil {
		t.Fatalf("testkit: dừng Redis: %v", err)
	}
}

// Start bật lại Redis đã Stop và cập nhật Addr, URL (cổng host được ánh xạ lại có thể khác).
func (r *RedisInstance) Start(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), StartTimeout)
	defer cancel()
	if err := r.c.Start(ctx); err != nil {
		t.Fatalf("testkit: bật lại Redis: %v", err)
	}
	u, err := r.c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("testkit: địa chỉ Redis: %v", err)
	}
	r.URL, r.Addr = u, strings.TrimPrefix(u, "redis://")
}

// withLogs gắn đoạn cuối log container vào lỗi để chẩn đoán khi khởi động thất bại.
func withLogs(ctx context.Context, c testcontainers.Container, err error) string {
	msg := err.Error()
	if c == nil {
		return msg
	}
	rd, lerr := c.Logs(ctx)
	if lerr != nil {
		return msg
	}
	defer rd.Close()
	buf := make([]byte, 4096)
	n, _ := rd.Read(buf)
	return fmt.Sprintf("%s\n--- log container ---\n%s", msg, buf[:n])
}
