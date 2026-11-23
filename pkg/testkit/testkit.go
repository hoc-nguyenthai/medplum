// Package testkit dựng các dịch vụ hạ tầng thật (PostgreSQL, Redis) và image của chính service bằng testcontainers, dùng cho
// test tích hợp và e2e. Chỉ được import từ file _test.go hoặc từ thư mục tests/ (xem architecture_test.go).
//
//   - Postgres: MỘT container cho cả tiến trình test; mỗi test xin một schema riêng (NewSchema) và, khi cần thử quyền, một
//     role riêng chỉ có quyền trên schema đó (NewServiceRole), khớp mô hình "một database nhiều schema" của docs/plans/microservices-shared-db.md.
//   - Redis: mỗi test một container (cô lập, an toàn khi chạy song song).
//   - BuildImage + RunService: dựng image từ Dockerfile của service rồi chạy, để e2e kiểm cả Dockerfile.
//
// Không có Docker thì test bị BỎ QUA có thông báo rõ; đặt TRIVITA_REQUIRE_DOCKER=1 (CI) thì test THẤT BẠI thay vì bỏ qua.
package testkit

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// RequireDockerEnv là biến môi trường đòi Docker: đặt thì thiếu Docker làm test thất bại thay vì bỏ qua.
const RequireDockerEnv = "TRIVITA_REQUIRE_DOCKER"

// StartTimeout là hạn khởi động một container (kéo image có thể lâu lần đầu).
const StartTimeout = 3 * time.Minute

var (
	dockerOnce sync.Once
	dockerErr  error
)

// RequireDocker kiểm tra Docker dùng được (`docker info`, kiểm một lần cho cả tiến trình). Không dùng được thì: bỏ qua test
// kèm lý do, hoặc thất bại nếu TRIVITA_REQUIRE_DOCKER=1. `go test -short` luôn bỏ qua (test đơn vị không cần Docker).
func RequireDocker(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("testkit: -short bỏ qua test cần Docker")
	}
	dockerOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
			dockerErr = &dockerError{string(out), err}
		}
	})
	switch decide(dockerErr, os.Getenv(RequireDockerEnv) == "1") {
	case outcomeFail:
		t.Fatalf("testkit: cần Docker (%s=1) nhưng không dùng được: %v", RequireDockerEnv, dockerErr)
	case outcomeSkip:
		t.Skipf("testkit: bỏ qua vì Docker không dùng được (đặt %s=1 để coi là lỗi): %v", RequireDockerEnv, dockerErr)
	}
}

type outcome int

const (
	outcomeRun outcome = iota
	outcomeSkip
	outcomeFail
)

// decide: Docker dùng được thì chạy; không thì bỏ qua, trừ khi môi trường đòi Docker (CI) thì thất bại.
func decide(dockerErr error, require bool) outcome {
	switch {
	case dockerErr == nil:
		return outcomeRun
	case require:
		return outcomeFail
	}
	return outcomeSkip
}

type dockerError struct {
	out string
	err error
}

func (e *dockerError) Error() string {
	if len(e.out) > 200 {
		e.out = e.out[:200]
	}
	return e.err.Error() + ": " + e.out
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
