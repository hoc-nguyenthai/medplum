package bootstrap_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
	"trivita-fhir/apps/fhir-server/internal/config"
)

// Hai replica (hai Container, hai máy chủ HTTP) dùng chung MỘT PostgreSQL và MỘT Redis, như khi chạy nhiều pod. Cần cả
// FHIRSTORE_TEST_POSTGRES_URL và FHIR_TEST_REDIS_URL (redis://host:port/db; test FLUSHDB database đó, dùng instance riêng).
const redisURLEnv = "FHIR_TEST_REDIS_URL"

func twoReplicas(t *testing.T, mutate ...func(*config.Settings)) (a, b *httptest.Server) {
	t.Helper()
	redisURL := os.Getenv(redisURLEnv)
	if os.Getenv(postgresEnv) == "" || redisURL == "" {
		t.Skipf("cần %s và %s", postgresEnv, redisURLEnv)
	}
	ro, err := goredis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rc := goredis.NewClient(ro)
	if err := rc.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.FlushDB(context.Background()).Err(); _ = rc.Close() })

	db := testDatabaseURL()
	mk := func() config.Settings {
		return wsSettings(append([]func(*config.Settings){func(s *config.Settings) {
			s.DatabaseURL = db
			s.BrokerDriver, s.RedisURL = "redis", redisURL
			s.WebhookAllowPrivate = true
			s.WSSigningKey = strings.Repeat("w", 40)
		}}, mutate...)...)
	}
	_, a = newServer(t, mk())
	_, b = newServer(t, mk()) // replica thứ hai thấy schema đã migrate
	return a, b
}

func TestTwoReplicasShareDataAndDeliverWebhookOnce(t *testing.T) {
	a, b := twoReplicas(t)
	hook, rec := newReceiver(t)
	want(t, call(t, a, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "replicas", "criteria": "Patient?family=Twice",
		"channel": map[string]any{"type": "rest-hook", "endpoint": hook.URL},
	}), 201)

	// ghi ở replica này, đọc ở replica kia
	id := want(t, call(t, b, "POST", "/fhir/R4/Patient", writeSecret, patient("Twice")), 201).str(t, "id")
	want(t, call(t, a, "GET", "/fhir/R4/Patient/"+id, readSecret, nil), 200)

	for i := 0; i < 300 && rec.count() < 1; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // chờ để bắt thông báo thừa nếu hai replica cùng giao
	if rec.count() != 1 {
		t.Fatalf("muốn đúng 1 thông báo webhook cho hai replica, được %d", rec.count())
	}
	// ghi ở replica đầu cũng tới đúng một lần
	want(t, call(t, a, "POST", "/fhir/R4/Patient", writeSecret, patient("Twice")), 201)
	for i := 0; i < 300 && rec.count() < 2; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if rec.count() != 2 {
		t.Fatalf("muốn 2 thông báo sau hai lần ghi, được %d", rec.count())
	}
}

func TestTwoReplicasWebSocketReceivesEventsWrittenElsewhere(t *testing.T) {
	a, b := twoReplicas(t)
	sub := want(t, call(t, a, "POST", "/fhir/R4/Subscription", writeSecret, wsSubscription("Patient")), 201).str(t, "id")
	ws := bindTo(t, b, readSecret, sub) // token cấp bởi replica B (chung khóa ký), nối vào B

	pid := want(t, call(t, a, "POST", "/fhir/R4/Patient", writeSecret, patient("Cross")), 201).str(t, "id") // ghi ở A
	m, ok := ws.next(t, 3*time.Second)
	if !ok {
		t.Fatal("websocket ở replica B không nhận được sự kiện ghi ở replica A")
	}
	res := m["entry"].([]any)[1].(map[string]any)["resource"].(map[string]any)
	if res["id"] != pid {
		t.Fatalf("thông báo: %v", m)
	}
	if extra, ok := ws.next(t, 300*time.Millisecond); ok {
		t.Fatalf("nhận thừa: %v", extra)
	}
}

func TestTwoReplicasShareJobs(t *testing.T) {
	a, b := twoReplicas(t, withAdmin)
	for i := 0; i < 5; i++ {
		want(t, call(t, a, "POST", "/fhir/R4/Patient", writeSecret, patient("Job")), 201)
	}
	// job tạo ở A: trạng thái lưu trong PostgreSQL nên xem được ở B, và chạy đến completed (replica nào nhận cũng được)
	acc := want(t, call(t, a, "POST", "/fhir/R4/Patient/$reindex", adminSecret, ""), 202)
	path := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	var st resp
	for i := 0; i < 500; i++ {
		if st = call(t, b, "GET", path, adminSecret, nil); st.status != 202 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.status != 200 || st.str(t, "status") != "completed" {
		t.Fatalf("job xem ở replica B: %d %s", st.status, st.body)
	}
}

// Nhiều pod khởi động cùng lúc với DATABASE_AUTO_MIGRATE=true trên database trống: migration không được làm hỏng nhau.
func TestReplicasMigrateConcurrentlyOnEmptyDatabase(t *testing.T) {
	if os.Getenv(postgresEnv) == "" {
		t.Skipf("cần %s", postgresEnv)
	}
	const n = 4
	db := testDatabaseURL()
	errs := make(chan error, n)
	cs := make(chan *bootstrap.Container, n)
	for i := 0; i < n; i++ {
		go func() {
			c, err := bootstrap.New(context.Background(), settings(func(s *config.Settings) { s.DatabaseURL = db }))
			errs <- err
			cs <- c
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("replica %d khởi động lỗi: %v", i, err)
		}
		if c := <-cs; c != nil {
			defer c.Stop(context.Background())
		}
	}
}

// Create có điều kiện đồng thời đi vào HAI replica khác nhau (cùng PostgreSQL) không được tạo trùng.
func TestTwoReplicasConditionalCreateDoesNotDuplicate(t *testing.T) {
	a, b := twoReplicas(t)
	const rounds, perRound = 5, 12
	for round := 0; round < rounds; round++ {
		family := "Dedup" + string(rune('A'+round))
		var wg, start sync.WaitGroup
		start.Add(1)
		for i := 0; i < perRound; i++ {
			wg.Add(1)
			srv := a
			if i%2 == 1 {
				srv = b
			}
			go func() {
				defer wg.Done()
				start.Wait()
				req, _ := http.NewRequest("POST", srv.URL+"/fhir/R4/Patient", strings.NewReader(`{"resourceType":"Patient","name":[{"family":"`+family+`"}]}`))
				req.Header.Set("Authorization", "Bearer "+writeSecret)
				req.Header.Set("Content-Type", "application/fhir+json")
				req.Header.Set("If-None-Exist", "family="+family)
				if res, err := http.DefaultClient.Do(req); err == nil {
					_ = res.Body.Close()
				}
			}()
		}
		start.Done()
		wg.Wait()
		r := want(t, call(t, a, "GET", "/fhir/R4/Patient?family="+family+"&_summary=count", readSecret, nil), 200)
		if got := r.json(t)["total"]; got != float64(1) {
			t.Fatalf("vòng %d: %d request đồng thời vào hai replica tạo ra %v Patient, muốn 1", round, perRound, got)
		}
	}
}
