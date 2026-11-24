// Package integration_test chạy fhir-notifier TRONG tiến trình test nhưng trên PostgreSQL và Redis THẬT (testcontainers): kiểm điều mà miniredis và SQLite không kiểm
// được, như role DB hẹp quyền với DATABASE_REQUIRE_SCHEMA (mô hình D-1 của docs/service-principles.md), nhận lại message chưa ACK (XCLAIM) và hành vi khi Redis chết
// rồi sống lại. Phía fhir-server được mô phỏng bằng cách ghi resource qua pkg/fhirstore và pkg/fhirsub lên Redis (hợp đồng dây C-6).
package integration_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-notifier/internal/bootstrap"
	"trivita-fhir/apps/fhir-notifier/internal/config"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
	"trivita-fhir/pkg/testkit"
)

type rig struct {
	store    *fhirstore.Store
	dsn      string
	redis    *testkit.RedisInstance
	proxyURL string
	vars     map[string]string
}

// redisProxy chuyển tiếp TCP tới địa chỉ HIỆN TẠI của Redis. Cổng host của container Redis có thể đổi sau khi Stop/Start, trong khi service chỉ biết một REDIS_URL cố định
// (như production sau một địa chỉ ổn định), nên test nối service qua proxy này.
func redisProxy(t *testing.T, r *testkit.RedisInstance) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				up, err := net.DialTimeout("tcp", r.Addr, 2*time.Second)
				if err != nil {
					return
				}
				defer up.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	return "redis://" + l.Addr().String()
}

// newRig dựng schema riêng với role CHỈ có quyền trên schema đó (như fhir_app), migrate bằng chính role đó (như `fhir-server migrate up`).
func newRig(t *testing.T) *rig {
	t.Helper()
	testkit.RequireDocker(t)
	pg := testkit.Postgres(t)
	dsn, _, _ := pg.NewServiceRole(t)
	rds := testkit.StartRedis(t)
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: dsn, RequireSchema: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := fhirstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	proxy := redisProxy(t, rds)
	return &rig{store: st, dsn: dsn, redis: rds, proxyURL: proxy, vars: map[string]string{
		"DATABASE_URL": dsn, "DATABASE_REQUIRE_SCHEMA": "true", "REDIS_URL": proxy, "FHIR_WEBHOOK_ALLOW_PRIVATE": "true",
		"SHUTDOWN_DRAIN_DELAY": "0s", "FHIR_WEBHOOK_TIMEOUT": "2s",
	}}
}

func (r *rig) settings(t *testing.T, kv ...string) config.Settings {
	t.Helper()
	vars := map[string]string{}
	for k, v := range r.vars {
		vars[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	s, err := config.FromEnv(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// api trả kho ghi như fhir-server (phát sự kiện lên broker Redis ở địa chỉ hiện tại).
func (r *rig) api(t *testing.T) fhirrouter.Repository {
	t.Helper()
	br, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: r.redis.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Stop(context.Background()) })
	return fhirsub.Wrap(r.store, br)
}

func create(t *testing.T, repo fhirrouter.Repository, js string) {
	t.Helper()
	res, err := fhir.UnmarshalResource([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateResource(context.Background(), res); err != nil {
		t.Fatal(err)
	}
}

func subscribe(t *testing.T, repo fhirrouter.Repository, id, endpoint string) {
	t.Helper()
	create(t, repo, `{"resourceType":"Subscription","id":"`+id+`","meta":{"project":"projA"},"status":"active","reason":"test","criteria":"Patient?family=Hooked","channel":{"type":"rest-hook","endpoint":"`+endpoint+`"}}`)
}

func patient(t *testing.T, repo fhirrouter.Repository) {
	t.Helper()
	create(t, repo, `{"resourceType":"Patient","meta":{"project":"projA"},"name":[{"family":"Hooked"}]}`)
}

func receiver(t *testing.T, block chan struct{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if block != nil {
			<-block
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func eventually(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("quá hạn chờ: %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func start(t *testing.T, s config.Settings) *bootstrap.Container {
	t.Helper()
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	return c
}

// Chạy bằng role hẹp quyền trên schema riêng với DATABASE_REQUIRE_SCHEMA=true: đọc Subscription, khớp và giao thành công trên PostgreSQL và Redis thật.
func TestDeliversUnderNarrowRoleAndRequiredSchema(t *testing.T) {
	r := newRig(t)
	api := r.api(t)
	srv, hits := receiver(t, nil)
	start(t, r.settings(t))
	subscribe(t, api, "s1", srv.URL)
	time.Sleep(500 * time.Millisecond)
	patient(t, api)
	eventually(t, "một thông báo", 20*time.Second, func() bool { return hits.Load() == 1 })
	time.Sleep(time.Second)
	if hits.Load() != 1 {
		t.Errorf("máy nhận bị gọi %d lần, muốn 1", hits.Load())
	}
}

// Replica A dừng giữa lúc giao (máy nhận treo): message không được ACK; replica B (cùng group) nhận lại bằng XCLAIM trên Redis thật và giao.
func TestAnotherReplicaClaimsTheUnfinishedMessageOnRealRedis(t *testing.T) {
	r := newRig(t)
	api := r.api(t)
	block := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(block) }) }
	t.Cleanup(release)
	srv, hits := receiver(t, block)
	s := r.settings(t, "FHIR_WEBHOOK_MAX_ATTEMPTS", "1", "FHIR_WEBHOOK_TIMEOUT", "2s", "BROKER_CLAIM_IDLE", "3s")

	a, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	subscribe(t, api, "s1", srv.URL)
	time.Sleep(500 * time.Millisecond)
	patient(t, api)
	eventually(t, "A đã gọi webhook (đang treo)", 20*time.Second, func() bool { return hits.Load() == 1 })
	stopped := make(chan struct{})
	go func() { _ = a.Stop(context.Background()); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(15 * time.Second):
		t.Fatal("A không dừng được dù webhook còn treo")
	}
	release()
	b := start(t, s)
	eventually(t, "B nhận lại message chưa ACK và giao", 40*time.Second, func() bool { return b.Manager.Stats().Delivered >= 1 })
}

// Redis chết giữa chừng: readiness 503 (liveness vẫn 200); sau khi Redis sống lại service tự nối lại và giao cả sự kiện phát sau đó.
func TestSurvivesRedisOutage(t *testing.T) {
	r := newRig(t)
	srv, hits := receiver(t, nil)
	c := start(t, r.settings(t))
	h := httptest.NewServer(c.Handler())
	t.Cleanup(h.Close)
	get := func(p string) int {
		resp, err := http.Get(h.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	api := r.api(t)
	subscribe(t, api, "s1", srv.URL)
	time.Sleep(500 * time.Millisecond)
	patient(t, api)
	eventually(t, "thông báo đầu", 20*time.Second, func() bool { return hits.Load() == 1 })

	r.redis.Stop(t)
	eventually(t, "readiness 503 khi Redis chết", 15*time.Second, func() bool { return get("/health/ready") == 503 })
	if get("/healthz") != 200 || get("/health/live") != 200 {
		t.Error("Redis chết: liveness phải vẫn 200 (K8s không restart pod vì lỗi của Redis)")
	}

	r.redis.Start(t)
	eventually(t, "readiness trở lại 200", 40*time.Second, func() bool { return get("/health/ready") == 200 })
	api2 := r.api(t) // địa chỉ Redis có thể đã đổi: API mô phỏng nối lại
	patient(t, api2)
	eventually(t, "giao tiếp tục sau khi Redis sống lại", 40*time.Second, func() bool { return hits.Load() >= 2 })
}
