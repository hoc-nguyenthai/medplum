// Package integration_test chạy fhir-downloader TRONG tiến trình test nhưng trên PostgreSQL và Redis THẬT (testcontainers): kiểm điều mà miniredis và SQLite không
// kiểm được, như role DB hẹp quyền với DATABASE_REQUIRE_SCHEMA (mô hình D-1 của docs/service-principles.md) và hành vi khi Redis chết rồi sống lại.
// Phía fhir-server được mô phỏng bằng cách ghi resource qua pkg/fhirstore và pkg/fhirsub lên Redis (hợp đồng dây C-6).
package integration_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-downloader/internal/bootstrap"
	"trivita-fhir/apps/fhir-downloader/internal/config"
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
	repo     fhirrouter.Repository
	redis    *testkit.RedisInstance
	settings config.Settings
	blobDir  string
}

// newRig dựng schema riêng với role CHỈ có quyền trên schema đó (như fhir_app), migrate bằng chính role đó (như `fhir-server migrate up`), rồi cấu hình downloader với
// DATABASE_REQUIRE_SCHEMA=true.
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
	br, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: rds.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Stop(context.Background()) })
	blobDir := filepath.Join(t.TempDir(), "blobs")
	vars := map[string]string{
		"DATABASE_URL": dsn, "DATABASE_REQUIRE_SCHEMA": "true", "REDIS_URL": redisProxy(t, rds), "FHIR_BINARY_STORAGE": "file:" + blobDir,
		"FHIR_AUTO_DOWNLOAD_ALLOW_PRIVATE": "true", "SHUTDOWN_DRAIN_DELAY": "0s",
	}
	s, err := config.FromEnv(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	return &rig{store: st, repo: fhirsub.Wrap(st, br), redis: rds, settings: s, blobDir: blobDir}
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

func (r *rig) createDoc(t *testing.T, id, project, url string) {
	t.Helper()
	d := &fhir.DocumentReference{ID: id, Status: "current", Meta: &fhir.Meta{Project: project},
		Content: []fhir.DocumentReferenceContent{{Attachment: &fhir.Attachment{URL: url}}}}
	if _, err := r.repo.CreateResource(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) url(t *testing.T, id string) string {
	t.Helper()
	res, err := r.store.ReadResource(context.Background(), "DocumentReference", id)
	if err != nil {
		t.Fatal(err)
	}
	return res.(*fhir.DocumentReference).Content[0].Attachment.URL
}

func (r *rig) waitBinary(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if strings.HasPrefix(r.url(t, id), "Binary/") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: attachment chưa thành Binary/<id>: %q", id, r.url(t, id))
}

func site(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
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

// Chạy bằng role hẹp quyền trên schema riêng với DATABASE_REQUIRE_SCHEMA=true: đọc resource nguồn, ghi Binary, cập nhật attachment đều thành công và
// nội dung tệp đúng từng byte trong kho.
func TestDownloadsUnderNarrowRoleAndRequiredSchema(t *testing.T) {
	r := newRig(t)
	pdf := bytes.Repeat([]byte("%PDF nội dung tài liệu "), 2000)
	srv, hits := site(t, pdf)
	start(t, r.settings)

	r.createDoc(t, "doc-1", "projA", srv.URL+"/a.pdf")
	r.waitBinary(t, "doc-1")

	id := strings.TrimPrefix(r.url(t, "doc-1"), "Binary/")
	res, err := r.store.ReadResource(context.Background(), "Binary", id)
	if err != nil {
		t.Fatal(err)
	}
	b := res.(*fhir.Binary)
	if b.Meta == nil || b.Meta.Project != "projA" || b.SecurityContext == nil || b.SecurityContext.Reference != "DocumentReference/doc-1" {
		t.Errorf("Binary: %+v", b)
	}
	var found []byte
	_ = filepath.WalkDir(r.blobDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found, _ = os.ReadFile(p)
		}
		return nil
	})
	if !bytes.Equal(found, pdf) {
		t.Errorf("nội dung kho: %d byte, muốn %d byte đúng từng byte", len(found), len(pdf))
	}
	if hits.Load() != 1 {
		t.Errorf("máy chủ ngoài bị gọi %d lần, muốn 1", hits.Load())
	}
}

// Redis chết giữa chừng: readiness 503 (liveness vẫn 200), sau khi Redis sống lại service tự nối lại và xử lý cả sự kiện ghi lúc Redis chết (API ghi vào stream sau khi Redis lại lên).
func TestSurvivesRedisOutage(t *testing.T) {
	r := newRig(t)
	srv, _ := site(t, []byte("%PDF một"))
	c := start(t, r.settings)
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
	r.createDoc(t, "doc-1", "projA", srv.URL+"/1.pdf")
	r.waitBinary(t, "doc-1")

	r.redis.Stop(t)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && get("/health/ready") != 503 {
		time.Sleep(100 * time.Millisecond)
	}
	if get("/health/ready") != 503 {
		t.Error("Redis chết: readiness phải 503")
	}
	if get("/healthz") != 200 || get("/health/live") != 200 {
		t.Error("Redis chết: liveness phải vẫn 200 (K8s không restart pod vì lỗi của Redis)")
	}

	r.redis.Start(t)
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && get("/health/ready") != 200 {
		time.Sleep(200 * time.Millisecond)
	}
	if get("/health/ready") != 200 {
		t.Fatal("Redis sống lại: readiness phải trở lại 200")
	}
	br, err := messaging.New(messaging.Config{Driver: "redis", RedisURL: r.redis.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Stop(context.Background()) })
	r.repo = fhirsub.Wrap(r.store, br)
	r.createDoc(t, "doc-2", "projA", srv.URL+"/2.pdf")
	r.waitBinary(t, "doc-2")
}
