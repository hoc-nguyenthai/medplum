// Package load đo tải của dicomweb: STOW song song, rồi WADO-RS (frame, metadata) và QIDO-RS song song, báo thông lượng, độ trễ p50/p95/p99 và đỉnh bộ nhớ. Không phải kiểm đúng sai
// (đã có ở test đơn vị và e2e) mà là số đo để định cỡ và phát hiện hồi quy; chỉ chạy khi đặt DICOMWEB_LOAD=1 (`make test-load-dicomweb`).
//
// Mặc định chạy trong tiến trình test trên SQLite tạm và kho tệp tạm: số đo là của máy phát triển, dùng để so sánh với chính nó, không phải để suy ra production. Đặt
// DICOMWEB_TEST_POSTGRES_URL để đo trên PostgreSQL thật (một schema riêng), và DICOMWEB_TEST_S3_ENDPOINT, DICOMWEB_TEST_S3_BUCKET, DICOMWEB_TEST_S3_ACCESS_KEY_ID,
// DICOMWEB_TEST_S3_SECRET_ACCESS_KEY để đo trên S3 thật (tiền tố riêng). Với máy chủ chạy trong Docker trên cùng máy thì độ trễ mạng gần bằng 0: số đo cho thấy chi phí giao thức và
// truy vấn chứ không phải độ trễ của dịch vụ đám mây.
package load

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"trivita-fhir/apps/dicomweb/internal/bootstrap"
	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/fhirstore"
)

const (
	secret = "load-test-shared-secret-0123456789abcdef"
	issuer = "https://id.load"
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// postgresSchema tạo một schema riêng trên máy chủ PostgreSQL và trả URL có search_path trỏ vào nó (xóa khi test xong).
func postgresSchema(t *testing.T, base string) string {
	t.Helper()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	var rnd [6]byte
	_, _ = crand.Read(rnd[:])
	schema := "dicomweb_load_" + hex.EncodeToString(rnd[:])
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
	u, err := neturl.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func settings(t *testing.T) config.Settings {
	t.Helper()
	dir := t.TempDir()
	url := "sqlite://" + filepath.Join(dir, "fhir.db")
	if pg := os.Getenv("DICOMWEB_TEST_POSTGRES_URL"); pg != "" {
		url = postgresSchema(t, pg)
	}
	db, err := database.Open(context.Background(), database.Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	st, err := fhirstore.New(db)
	if err == nil {
		err = st.Migrate(context.Background())
	}
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s := config.Default()
	s.DatabaseURL = url
	s.DatabaseMaxOpenConns = 20
	s.JWTIssuer, s.JWTAudience, s.JWTHMACSecret = issuer, "fhir", secret
	s.BinaryStorage = "file:" + filepath.Join(dir, "blobs")
	if ep := os.Getenv("DICOMWEB_TEST_S3_ENDPOINT"); ep != "" {
		var rnd [6]byte
		_, _ = crand.Read(rnd[:])
		s.BinaryStorage = "s3:" + os.Getenv("DICOMWEB_TEST_S3_BUCKET") + "/dicomweb-load-" + hex.EncodeToString(rnd[:])
		s.S3Endpoint, s.S3Region = ep, "us-east-1"
		s.S3AccessKeyID, s.S3SecretAccessKey = os.Getenv("DICOMWEB_TEST_S3_ACCESS_KEY_ID"), os.Getenv("DICOMWEB_TEST_S3_SECRET_ACCESS_KEY")
	}
	s.MaxRequestBytes, s.MaxInstanceBytes = 1<<30, 1<<30
	return s
}

func token(t *testing.T, scope string) string {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": issuer, "aud": "fhir", "sub": "load", "scope": scope, "fhir_project": "load", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type stats struct {
	mu   sync.Mutex
	lat  []time.Duration
	errs atomic.Int64
}

func (s *stats) add(d time.Duration) { s.mu.Lock(); s.lat = append(s.lat, d); s.mu.Unlock() }

func (s *stats) report(t *testing.T, name string, wall time.Duration, bytes int64) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	sort.Slice(s.lat, func(i, j int) bool { return s.lat[i] < s.lat[j] })
	pct := func(p float64) time.Duration {
		if len(s.lat) == 0 {
			return 0
		}
		return s.lat[min(len(s.lat)-1, int(float64(len(s.lat))*p))]
	}
	t.Logf("%-22s %5d yêu cầu, %d lỗi, %6.1f yêu cầu/s, %7.1f MB/s, p50 %v p95 %v p99 %v", name, len(s.lat), s.errs.Load(), float64(len(s.lat))/wall.Seconds(),
		float64(bytes)/1e6/wall.Seconds(), pct(0.50).Round(time.Microsecond), pct(0.95).Round(time.Microsecond), pct(0.99).Round(time.Microsecond))
}

// peakHeap lấy mẫu HeapInuse tới khi gọi hàm trả về.
func peakHeap() func() uint64 {
	var peak atomic.Uint64
	stop := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}
	}()
	return func() uint64 { close(stop); return peak.Load() }
}

func parallel(n int, f func(worker int)) time.Duration {
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); f(i) }(i)
	}
	wg.Wait()
	return time.Since(start)
}

func TestDicomwebLoad(t *testing.T) {
	if os.Getenv("DICOMWEB_LOAD") != "1" {
		t.Skip("đặt DICOMWEB_LOAD=1 (make test-load-dicomweb) để đo tải")
	}
	clients := envInt("DICOMWEB_LOAD_CLIENTS", 16)
	studies := envInt("DICOMWEB_LOAD_STUDIES", 20)
	perStudy := envInt("DICOMWEB_LOAD_INSTANCES_PER_STUDY", 25)
	frameReads := envInt("DICOMWEB_LOAD_FRAME_READS", 2000)
	heapLimitMB := envInt("DICOMWEB_LOAD_HEAP_LIMIT_MB", 400)

	s := settings(t)
	c, err := bootstrap.New(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	e, err := c.Handler(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e)
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: clients * 2}, Timeout: 2 * time.Minute}
	wr, rd := token(t, "fhir.write"), token(t, "fhir.read")

	// mỗi instance: 4 frame 256x256 16 bit = 512 KiB pixel; 2 phần tư series có 1 frame (nhỏ), để có cả instance nhỏ lẫn lớn
	mk := func(st, se, sop int) []byte {
		frames := 4
		if sop%4 == 0 {
			frames = 1
		}
		return dicomtest.Build(dicomtest.Meta{StudyUID: dicomtest.UID(fmt.Sprint(st)), SeriesUID: dicomtest.UID(fmt.Sprint(st), fmt.Sprint(se)), SOPUID: dicomtest.UID(fmt.Sprint(st), fmt.Sprint(se), fmt.Sprint(sop)),
			Rows: 256, Cols: 256, Bits: 16, Frames: frames, PatientName: fmt.Sprintf("Load^%d", st)})
	}
	do := func(method, path, tok, ct string, body io.Reader) (int, []byte, http.Header, error) {
		req, _ := http.NewRequest(method, srv.URL+path, body)
		req.Header.Set("Authorization", "Bearer "+tok)
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		res, err := client.Do(req)
		if err != nil {
			return 0, nil, nil, err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		return res.StatusCode, b, res.Header, err
	}

	// ---- 1. STOW song song: mỗi client tải các study của mình, mỗi yêu cầu 5 instance ----
	var st1 stats
	var sent atomic.Int64
	stop := peakHeap()
	runtime.GC()
	wall := parallel(clients, func(w int) {
		for study := w; study < studies; study += clients {
			for from := 1; from <= perStudy; from += 5 {
				var buf bytes.Buffer
				mw := multipart.NewWriter(&buf)
				for sop := from; sop < from+5 && sop <= perStudy; sop++ {
					pw, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/dicom"}})
					f := mk(study+1, 1, sop)
					pw.Write(f)
					sent.Add(int64(len(f)))
				}
				mw.Close()
				t0 := time.Now()
				code, raw, _, err := do("POST", "/dicomweb/studies", wr, `multipart/related; type="application/dicom"; boundary=`+mw.Boundary(), &buf)
				st1.add(time.Since(t0))
				if err != nil || code != 200 {
					st1.errs.Add(1)
					t.Errorf("STOW: %d %v %.200s", code, err, raw)
				}
			}
		}
	})
	st1.report(t, "STOW (5 instance/lần)", wall, sent.Load())
	t.Logf("STOW: %d instance, %.1f MB, %.1f instance/s, đỉnh heap %d MB", studies*perStudy, float64(sent.Load())/1e6, float64(studies*perStudy)/wall.Seconds(), stop()>>20)

	// ---- 2. WADO-RS frame ngẫu nhiên ----
	var st2 stats
	var got atomic.Int64
	stop = peakHeap()
	wall = parallel(clients, func(w int) {
		rng := rand.New(rand.NewSource(int64(w)))
		for i := w; i < frameReads; i += clients {
			stud, sop := rng.Intn(studies)+1, rng.Intn(perStudy)+1
			fr := 1
			if sop%4 != 0 {
				fr = rng.Intn(4) + 1
			}
			path := fmt.Sprintf("/dicomweb/studies/%s/series/%s/instances/%s/frames/%d", dicomtest.UID(fmt.Sprint(stud)), dicomtest.UID(fmt.Sprint(stud), "1"), dicomtest.UID(fmt.Sprint(stud), "1", fmt.Sprint(sop)), fr)
			t0 := time.Now()
			code, raw, hdr, err := do("GET", path, rd, "", nil)
			st2.add(time.Since(t0))
			if err != nil || code != 200 {
				st2.errs.Add(1)
				t.Errorf("frame: %d %v", code, err)
				continue
			}
			if _, params, err := mime.ParseMediaType(hdr.Get("Content-Type")); err != nil || params["boundary"] == "" || len(raw) < 256*256*2 {
				st2.errs.Add(1)
				t.Errorf("frame trả về sai dạng: %q %d byte", hdr.Get("Content-Type"), len(raw))
			}
			got.Add(int64(len(raw)))
		}
	})
	st2.report(t, "WADO frame (512 KiB)", wall, got.Load())
	t.Logf("WADO frame: đỉnh heap %d MB", stop()>>20)

	// ---- 3. QIDO và metadata ----
	var st3, st4 stats
	stop = peakHeap()
	wall = parallel(clients, func(w int) {
		for i := 0; i < 20; i++ {
			stud := (w+i)%studies + 1
			t0 := time.Now()
			code, _, _, err := do("GET", "/dicomweb/studies?limit=50", rd, "", nil)
			st3.add(time.Since(t0))
			if err != nil || code != 200 {
				st3.errs.Add(1)
			}
			t0 = time.Now()
			code, raw, _, err := do("GET", fmt.Sprintf("/dicomweb/studies/%s/series/%s/metadata", dicomtest.UID(fmt.Sprint(stud)), dicomtest.UID(fmt.Sprint(stud), "1")), rd, "", nil)
			st4.add(time.Since(t0))
			if err != nil || code != 200 || bytes.Count(raw, []byte(`"00080018"`)) != perStudy {
				st4.errs.Add(1)
				t.Errorf("metadata: %d %v (%d instance)", code, err, bytes.Count(raw, []byte(`"00080018"`)))
			}
		}
	})
	st3.report(t, "QIDO studies", wall, 0)
	st4.report(t, fmt.Sprintf("metadata series (%d inst)", perStudy), wall, 0)
	peak := stop()
	t.Logf("QIDO/metadata: đỉnh heap %d MB", peak>>20)

	if n := st1.errs.Load() + st2.errs.Load() + st3.errs.Load() + st4.errs.Load(); n != 0 {
		t.Errorf("%d yêu cầu lỗi", n)
	}
	if int(peak>>20) > heapLimitMB {
		t.Errorf("đỉnh heap %d MB vượt giới hạn %d MB", peak>>20, heapLimitMB)
	}
}
