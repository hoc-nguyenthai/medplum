package bootstrap_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Công tắc chuẩn bị tách service (S1 của docs/plans/fhir-server-split-candidates.md): tắt phía giao, phía tải và thực thi job ở API.
// Mặc định bật cả ba (hành vi cũ, các test khác đã phủ); ở đây kiểm khi tắt thì API chỉ phát sự kiện và nhận job chứ không làm thay.

// streamLen đếm sự kiện ghi resource đã được phát lên Redis (đọc thẳng stream, không qua consumer nào).
func streamLen(t *testing.T, addr string) int64 {
	t.Helper()
	c := goredis.NewClient(&goredis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*fhir.resource.changed").Result()
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, k := range keys {
		l, err := c.XLen(ctx, k).Result()
		if err != nil {
			t.Fatal(err)
		}
		n += l
	}
	return n
}

func TestSubscriptionDeliveryOffPublishesButDoesNotDeliver(t *testing.T) {
	addr := issuerRedis(t)
	hook, rec := newReceiver(t)
	c, srv := newServer(t, settings(func(s *config.Settings) {
		s.Subscriptions, s.WebhookAllowPrivate, s.SubscriptionDelivery = true, true, false
		s.BrokerDriver, s.RedisURL = "redis", "redis://"+addr
	}))
	if c.Manager != nil || c.Broker == nil {
		t.Fatalf("giao tắt: Manager phải nil, Broker phải có (manager=%v broker=%v)", c.Manager != nil, c.Broker != nil)
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "e2e", "criteria": "Patient?family=Hooked",
		"channel": map[string]any{"type": "rest-hook", "endpoint": hook.URL},
	}), 201)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Hooked")), 201)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && streamLen(t, addr) < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if n := streamLen(t, addr); n < 2 {
		t.Fatalf("API phải vẫn phát sự kiện (Subscription và Patient) lên Redis, có %d", n)
	}
	time.Sleep(500 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("API không được giao rest-hook khi FHIR_SUBSCRIPTION_DELIVERY=false, đã gọi %d lần", rec.count())
	}
}

func TestAutoDownloadWorkerOffPublishesButDoesNotDownload(t *testing.T) {
	addr := issuerRedis(t)
	var hits atomic.Int32
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("%PDF"))
	}))
	defer ext.Close()
	mut, _ := storageSettings(t, func(s *config.Settings) {
		s.AutoDownload, s.AutoDownloadAllowPrivate, s.AutoDownloadWorker = true, true, false
		s.BrokerDriver, s.RedisURL = "redis", "redis://"+addr
	})
	_, srv := newServer(t, settings(mut))
	body := `{"resourceType":"DocumentReference","status":"current","content":[{"attachment":{"url":"` + ext.URL + `/a.pdf"}}]}`
	id := want(t, call(t, srv, "POST", "/fhir/R4/DocumentReference", writeSecret, body), 201).str(t, "id")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && streamLen(t, addr) < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	if streamLen(t, addr) < 1 {
		t.Fatal("API phải vẫn phát sự kiện ghi lên Redis")
	}
	time.Sleep(500 * time.Millisecond)
	got := want(t, call(t, srv, "GET", "/fhir/R4/DocumentReference/"+id, writeSecret, nil), 200).str(t, "content", "0", "attachment", "url")
	if hits.Load() != 0 || !strings.HasPrefix(got, ext.URL) {
		t.Fatalf("API không được tải khi FHIR_AUTO_DOWNLOAD_WORKER=false: gọi ngoài %d lần, url=%q", hits.Load(), got)
	}
}

func TestJobExecutionOffLeavesJobAccepted(t *testing.T) {
	for _, exec := range []bool{true, false} {
		name := "chạy tại chỗ"
		if !exec {
			name = "tắt thực thi"
		}
		t.Run(name, func(t *testing.T) {
			_, srv := newServer(t, settings(withAdmin, func(s *config.Settings) { s.JobExecution = exec }))
			want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Alpha")), 201)
			acc := want(t, call(t, srv, "POST", "/fhir/R4/Patient/$reindex", adminSecret, ""), 202)
			path := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
			var st resp
			for i := 0; i < 50; i++ {
				if st = call(t, srv, "GET", path, readSecret, nil); st.status != 202 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if exec && (st.status != 200 || st.str(t, "status") != "completed") {
				t.Fatalf("mặc định job phải chạy xong: %d %s", st.status, st.body)
			}
			if !exec && st.status != 202 {
				t.Fatalf("API không được chạy job khi FHIR_JOB_EXECUTION=false: %d %s", st.status, st.body)
			}
		})
	}
}

// FHIR_WEBHOOK_DURABLE qua server thật trên Redis: giao đồng bộ trong handler, thử lại tại chỗ khi máy nhận trả 503, rồi ACK (không giao thêm).
func TestWebhookDurableDeliversAndRetriesInPlace(t *testing.T) {
	addr := issuerRedis(t)
	var calls atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // lần đầu lỗi tạm
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()
	_, srv := newServer(t, settings(func(s *config.Settings) {
		s.Subscriptions, s.WebhookAllowPrivate, s.WebhookDurable = true, true, true
		s.BrokerDriver, s.RedisURL = "redis", "redis://"+addr
	}))
	want(t, call(t, srv, "POST", "/fhir/R4/Subscription", writeSecret, map[string]any{
		"resourceType": "Subscription", "status": "active", "reason": "e2e", "criteria": "Patient?family=Hooked",
		"channel": map[string]any{"type": "rest-hook", "endpoint": hook.URL},
	}), 201)
	time.Sleep(300 * time.Millisecond) // chờ Subscription được nạp (sự kiện Subscription làm mới danh sách)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Hooked")), 201)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("muốn 2 lần gọi (503 rồi 200), được %d", calls.Load())
	}
	time.Sleep(600 * time.Millisecond)
	if calls.Load() != 2 {
		t.Errorf("sau khi giao được không được giao thêm: %d", calls.Load())
	}
}

// P0-2 của kế hoạch tách: kết quả job (file export) đi ra kho đối tượng chứ không nằm base64 trong CSDL, tải về đúng nội dung, và dọn kết quả hết hạn bằng Expunge nên cả dòng
// trong CSDL lẫn tệp ở kho đều mất (xóa mềm sẽ để lại nội dung mãi).
func TestExportResultsGoToObjectStorageAndAreExpungedOnExpiry(t *testing.T) {
	mut, _ := storageSettings(t, func(s *config.Settings) {
		s.JobMaintenanceInterval, s.ExportRetention = 200*time.Millisecond, 2*time.Second
		s.Base64BinaryMaxBytes = 1 << 10 // trần base64 qua API nhỏ: file export lớn hơn trần này vẫn phải ghi được (job không bị trần)
	})
	s := settings(withAdmin, mut)
	c, srv := newServer(t, s)
	blobDir := strings.TrimPrefix(s.BinaryStorage, "file:")
	for i := 0; i < 30; i++ {
		want(t, call(t, srv, "POST", "/fhir/R4/Patient", writeSecret, patient("Export"+strings.Repeat("x", 80)+string(rune('a'+i%26)))), 201)
	}

	acc := rawCall(t, srv, "GET", "/fhir/R4/Patient/$export", adminSecret, map[string]string{"Prefer": "respond-async"}, nil)
	want(t, acc, 202)
	path := strings.TrimPrefix(acc.header.Get("Content-Location"), "http://fhir.test")
	var st resp
	for i := 0; i < 300; i++ {
		if st = call(t, srv, "GET", path, adminSecret, nil); st.status != 202 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want(t, st, 200)
	url := st.str(t, "output", "0", "url")
	binID := url[strings.LastIndex(url, "/")+1:]

	// nội dung nằm ở kho; dòng Binary trong CSDL không giữ `data`
	if n := countFiles(t, blobDir); n == 0 {
		t.Fatal("file export không được ghi ra kho đối tượng")
	}
	res, err := c.Store.ReadResource(context.Background(), "Binary", binID)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := res.(*fhir.Binary); !ok || b.Data != "" {
		t.Fatalf("Binary trong CSDL không được giữ nội dung base64: %+v", res)
	}
	// tải qua API: đúng nội dung ndjson
	got := rawCall(t, srv, "GET", "/fhir/R4/Binary/"+binID, adminSecret, map[string]string{"Accept": "application/fhir+ndjson"}, nil)
	want(t, got, 200)
	if lines := strings.Count(strings.TrimSpace(string(got.body)), "\n") + 1; lines != 30 {
		t.Errorf("file export có %d dòng, muốn 30 Patient", lines)
	}

	// hết hạn: dòng AsyncJob, dòng Binary (Expunge, không phải xóa mềm) và tệp ở kho đều mất
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && countFiles(t, blobDir) != 0 {
		time.Sleep(100 * time.Millisecond)
	}
	if n := countFiles(t, blobDir); n != 0 {
		t.Fatalf("kết quả hết hạn phải bị xóa khỏi kho, còn %d tệp", n)
	}
	if _, err := c.Store.ReadResource(context.Background(), "Binary", binID); err == nil {
		t.Error("dòng Binary phải bị Expunge (đọc phải lỗi), không chỉ xóa mềm")
	} else if e, ok := err.(*fhirrouter.Error); !ok || e.Status != 404 {
		t.Errorf("sau Expunge muốn 404 (không còn dấu vết), được %v", err)
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}
