package testkit

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// BuildTimeout là hạn một lần `docker build`.
const BuildTimeout = 10 * time.Minute

// BuildSpec mô tả cách dựng image của một service.
type BuildSpec struct {
	Name       string            // tên ngắn (thành tag testkit-<Name>:latest); dựng một lần cho mỗi tiến trình test
	Context    string            // thư mục context (đường dẫn tuyệt đối hoặc tương đối với thư mục chạy)
	Dockerfile string            // đường dẫn Dockerfile (tương đối với Context hoặc tuyệt đối)
	Args       map[string]string // --build-arg
	// NamedContexts là các build context có tên (--build-context tên=đường-dẫn), ví dụ thư viện anh em mà Dockerfile lấy bằng `COPY --from=tên`.
	NamedContexts map[string]string
}

var (
	buildMu   sync.Mutex
	buildDone = map[string]error{}
)

// BuildImage dựng image bằng `docker build` (không dùng bộ dựng trong thư viện: cần BuildKit và <Dockerfile>.dockerignore) và
// trả tag. Dựng một lần cho mỗi (Name, Context, Dockerfile, Args) trong cả tiến trình; lớp cache của Docker giữ cho lần sau.
// Image không bị xóa khi test xong (để tái dùng cache); dọn bằng `docker image prune`.
func BuildImage(t testing.TB, s BuildSpec) string {
	t.Helper()
	RequireDocker(t)
	tag := "testkit-" + s.Name + ":latest"
	key := fmt.Sprintf("%s|%s|%s|%v|%v", tag, s.Context, s.Dockerfile, s.Args, s.NamedContexts)
	buildMu.Lock()
	defer buildMu.Unlock()
	if err, ok := buildDone[key]; ok {
		if err != nil {
			t.Fatalf("testkit: dựng image %s đã thất bại trước đó: %v", tag, err)
		}
		return tag
	}
	args := []string{"build", "-t", tag}
	if s.Dockerfile != "" {
		args = append(args, "-f", s.Dockerfile)
	}
	for k, v := range s.Args {
		args = append(args, "--build-arg", k+"="+v)
	}
	for k, v := range s.NamedContexts {
		args = append(args, "--build-context", k+"="+v)
	}
	args = append(args, s.Context)
	ctx, cancel := context.WithTimeout(context.Background(), BuildTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 40 {
			lines = lines[len(lines)-40:]
		}
		err = fmt.Errorf("docker %s: %w\n%s", strings.Join(args, " "), err, strings.Join(lines, "\n"))
		buildDone[key] = err
		t.Fatalf("testkit: dựng image %s thất bại: %v", tag, err)
	}
	buildDone[key] = nil
	return tag
}

// ServiceSpec mô tả cách chạy một service từ image.
type ServiceSpec struct {
	Image   string
	Env     map[string]string
	Port    string   // cổng HTTP trong container, ví dụ "8000/tcp"
	Health  string   // đường dẫn probe, ví dụ "/health/ready"; rỗng thì chỉ chờ cổng mở
	Cmd     []string // ghi đè CMD/args (rỗng: giữ mặc định của image)
	Network *Network
	Alias   string // tên trong Network
	Ready   time.Duration
	// ReadOnly chạy với root filesystem CHỈ ĐỌC (như `readOnlyRootFilesystem: true` của K8s): service ghi đĩa ở đâu đó ngoài volume thì không lên được.
	ReadOnly bool
}

// Service là một service đang chạy trong container.
type Service struct {
	URL  string // http://host:port, dùng từ máy chạy test
	Host string
	Port int
	c    testcontainers.Container
}

// RunService chạy image và chờ sẵn sàng (cổng mở và, nếu có, probe Health trả 200). Dọn khi test xong. Khi khởi động thất bại,
// lỗi kèm đoạn cuối log của container.
func RunService(t testing.TB, s ServiceSpec) *Service {
	t.Helper()
	RequireDocker(t)
	ready := s.Ready
	if ready <= 0 {
		ready = 60 * time.Second
	}
	var strategy wait.Strategy = wait.ForListeningPort(s.Port).WithStartupTimeout(ready)
	if s.Health != "" {
		strategy = wait.ForHTTP(s.Health).WithPort(s.Port).WithStartupTimeout(ready)
	}
	req := testcontainers.ContainerRequest{
		Image: s.Image, Env: s.Env, ExposedPorts: []string{s.Port}, Cmd: s.Cmd, WaitingFor: strategy,
	}
	if s.ReadOnly {
		req.HostConfigModifier = func(hc *container.HostConfig) { hc.ReadonlyRootfs = true }
	}
	if s.Network != nil {
		req.Networks = []string{s.Network.Name}
		req.NetworkAliases = map[string][]string{s.Network.Name: {s.Alias}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), StartTimeout)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if c != nil {
		testcontainers.CleanupContainer(t, c)
	}
	if err != nil {
		t.Fatalf("testkit: chạy %s: %s", s.Image, withLogs(context.Background(), c, err))
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("testkit: host: %v", err)
	}
	mp, err := c.MappedPort(ctx, s.Port)
	if err != nil {
		t.Fatalf("testkit: cổng: %v", err)
	}
	n, _ := strconv.Atoi(mp.Port())
	return &Service{URL: fmt.Sprintf("http://%s:%s", host, mp.Port()), Host: host, Port: n, c: c}
}

// WSURL là địa chỉ websocket của service (ws://host:port).
func (s *Service) WSURL() string { return "ws" + strings.TrimPrefix(s.URL, "http") }

// Logs trả log của container (để in khi test thất bại).
func (s *Service) Logs(t testing.TB) string {
	t.Helper()
	rd, err := s.c.Logs(context.Background())
	if err != nil {
		return err.Error()
	}
	defer rd.Close()
	var b strings.Builder
	buf := make([]byte, 8192)
	for {
		n, err := rd.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}

// Terminate gửi SIGTERM và chờ tối đa grace rồi mới ép dừng (kiểm tắt êm). Trả mã thoát của tiến trình.
func (s *Service) Terminate(t testing.TB, grace time.Duration) int {
	t.Helper()
	if err := s.c.Stop(context.Background(), &grace); err != nil {
		t.Fatalf("testkit: dừng service: %v", err)
	}
	st, err := s.c.State(context.Background())
	if err != nil {
		t.Fatalf("testkit: trạng thái: %v", err)
	}
	return st.ExitCode
}

// Exec chạy một lệnh trong container đang chạy (ví dụ `/app healthcheck`) và trả mã thoát cùng đầu ra.
func (s *Service) Exec(t testing.TB, cmd ...string) (int, string) {
	t.Helper()
	return s.ExecEnv(t, nil, cmd...)
}

// ExecEnv như Exec nhưng thêm biến môi trường cho riêng lệnh đó (ví dụ mật khẩu cho `auth seed project`).
func (s *Service) ExecEnv(t testing.TB, env map[string]string, cmd ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := []tcexec.ProcessOption{tcexec.Multiplexed()}
	if len(env) > 0 {
		var kv []string
		for k, v := range env {
			kv = append(kv, k+"="+v)
		}
		opts = append(opts, tcexec.WithEnv(kv))
	}
	code, rd, err := s.c.Exec(ctx, cmd, opts...)
	if err != nil {
		t.Fatalf("testkit: exec %v: %v", cmd, err)
	}
	b, _ := io.ReadAll(rd)
	return code, string(b)
}

// RunToExit chạy image tới khi tiến trình thoát (kiểm service từ chối khởi động khi cấu hình sai) và trả mã thoát cùng log.
// Quá `within` mà chưa thoát thì test thất bại.
func RunToExit(t testing.TB, s ServiceSpec, within time.Duration) (int, string) {
	t.Helper()
	RequireDocker(t)
	req := testcontainers.ContainerRequest{Image: s.Image, Env: s.Env, Cmd: s.Cmd, WaitingFor: wait.ForExit().WithExitTimeout(within)}
	ctx, cancel := context.WithTimeout(context.Background(), StartTimeout)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if c != nil {
		testcontainers.CleanupContainer(t, c)
	}
	if err != nil {
		t.Fatalf("testkit: %s không thoát trong %v: %s", s.Image, within, withLogs(context.Background(), c, err))
	}
	st, err := c.State(ctx)
	if err != nil {
		t.Fatalf("testkit: trạng thái: %v", err)
	}
	rd, err := c.Logs(ctx)
	if err != nil {
		t.Fatalf("testkit: log: %v", err)
	}
	defer rd.Close()
	b, _ := io.ReadAll(rd)
	return st.ExitCode, string(b)
}
