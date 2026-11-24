// Package load_test đo ảnh hưởng của `$export` lớn lên API (#12c): chạy fhir-server THẬT (một binary, PostgreSQL thật qua testcontainers), đo độ trễ p50/p95/p99,
// thông lượng, CPU và RSS của server trong ba pha:
//
//	A. baseline: chỉ tải API (đọc và tìm kiếm trộn);
//	B. export một mình: thời gian export và chi phí của nó;
//	C. API + export cùng lúc: cùng tải API như A trong lúc export chạy.
//
// Kết luận "API bị ảnh hưởng" khi p95 (hoặc p99) ở pha C vượt EXPORT_LOAD_MAX_RATIO lần baseline (mặc định 1.5). Đây là điều kiện để mở lại P3b `fhir-worker`
// (D-DB-3). Test KHÔNG fail theo ngưỡng (đây là số đo, không phải kiểm đúng sai); nó ghi kết luận bằng t.Log. Chạy:
//
//	make bench-export                                   # 50.000 resource, 16 client, FHIR_JOB_CONCURRENCY=2
//	EXPORT_LOAD_RESOURCES=300000 EXPORT_LOAD_CLIENTS=32 make bench-export
//	EXPORT_LOAD_EXPORTS=4 FHIR_JOB_CONCURRENCY=4 make bench-export   # bốn export đồng thời ở pha C
//
// Số đo phụ thuộc máy: server, PostgreSQL (trong Docker) và bộ tạo tải chia sẻ cùng CPU, nên đo lại trên cấu hình triển khai thật trước khi quyết định.
package load_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"trivita-fhir/pkg/testkit"
)

const token = "load-write-token-0123456789abcdef"

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func envFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && v > 0 {
		return v
	}
	return def
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func build(t *testing.T) string {
	t.Helper()
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "fhir-server")
	cmd := exec.Command("go", "build", "-o", bin, "./apps/fhir-server/cmd/fhir-server")
	cmd.Dir = strings.TrimSpace(string(root))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fhir-server: %v\n%s", err, out)
	}
	return bin
}

func start(t *testing.T, bin string, env map[string]string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("---- log fhir-server ----\n%s", out.String())
		}
	})
	return cmd
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(url); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s không sẵn sàng sau 60 giây", url)
}

// procStat đọc RSS (KB) và CPU tích lũy (giây) của tiến trình bằng ps (macOS và Linux).
func procStat(pid int) (rssKB int, cpu time.Duration) {
	out, err := exec.Command("ps", "-o", "rss=,time=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return 0, 0
	}
	rssKB, _ = strconv.Atoi(f[0])
	return rssKB, parseCPU(f[1])
}

// parseCPU đọc "M:SS.cc", "H:MM:SS" hoặc "D-HH:MM:SS".
func parseCPU(s string) time.Duration {
	days := 0
	if i := strings.Index(s, "-"); i >= 0 {
		days, _ = strconv.Atoi(s[:i])
		s = s[i+1:]
	}
	var secs float64
	for _, p := range strings.Split(s, ":") {
		v, _ := strconv.ParseFloat(p, 64)
		secs = secs*60 + v
	}
	return time.Duration((secs + float64(days)*86400) * float64(time.Second))
}

type result struct {
	name       string
	lat        []time.Duration
	errs       int
	dur        time.Duration
	cpu        time.Duration
	pgCPU      time.Duration
	peakRSS    int
	extra      string
	throughput float64
}

func pct(s []time.Duration, p float64) time.Duration {
	if len(s) == 0 {
		return 0
	}
	return s[int(float64(len(s)-1)*p)]
}

func (r *result) log(t *testing.T) {
	sort.Slice(r.lat, func(i, j int) bool { return r.lat[i] < r.lat[j] })
	r.throughput = float64(len(r.lat)) / r.dur.Seconds()
	t.Logf("%-22s %6.1fs  req=%-6d err=%-3d %6.0f req/s  p50=%-8v p95=%-8v p99=%-8v  CPU server=%.0f%% PG=%.0f%% (1 lõi=100%%)  RSS server đỉnh=%d MB %s",
		r.name, r.dur.Seconds(), len(r.lat), r.errs, r.throughput, pct(r.lat, .5).Round(10*time.Microsecond), pct(r.lat, .95).Round(10*time.Microsecond),
		pct(r.lat, .99).Round(10*time.Microsecond), 100*r.cpu.Seconds()/r.dur.Seconds(), 100*r.pgCPU.Seconds()/r.dur.Seconds(), r.peakRSS/1024, r.extra)
}

var client = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 256}}

func call(method, url, body string, hdr ...string) (int, []byte, http.Header, error) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/fhir+json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header, nil
}

// postBatch gửi một batch POST và trả id (phần sau "Type/") của từng entry tạo thành công theo thứ tự.
func postBatch(t *testing.T, api string, entries []any) []string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"resourceType": "Bundle", "type": "batch", "entry": entries})
	st, b, _, err := call("POST", api, string(body))
	if err != nil || st != 200 {
		t.Fatalf("batch seed: %d %v %.300s", st, err, b)
	}
	var out struct {
		Entry []struct {
			Response struct {
				Status   string `json:"status"`
				Location string `json:"location"`
			} `json:"response"`
			Resource struct {
				ID string `json:"id"`
			} `json:"resource"`
		} `json:"entry"`
	}
	_ = json.Unmarshal(b, &out)
	ids := make([]string, 0, len(out.Entry))
	for _, e := range out.Entry {
		if !strings.HasPrefix(e.Response.Status, "2") {
			t.Fatalf("entry seed lỗi: %s trong %.400s", e.Response.Status, b)
		}
		id := e.Resource.ID
		if id == "" {
			parts := strings.Split(e.Response.Location, "/")
			for i, p := range parts {
				if i > 0 && (p == "_history") {
					id = parts[i-1]
				}
			}
		}
		ids = append(ids, id)
	}
	return ids
}

// seed nạp patients Patient rồi 4*patients Observation (batch POST, 8 luồng); trả danh sách id Patient.
func seed(t *testing.T, api string, patients int) []string {
	t.Helper()
	parallel := func(entries []any, fn func(ids []string)) {
		ch := make(chan []any)
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for c := range ch {
					fn(postBatch(t, api, c))
				}
			}()
		}
		for i := 0; i < len(entries); i += 200 {
			j := i + 200
			if j > len(entries) {
				j = len(entries)
			}
			ch <- entries[i:j]
		}
		close(ch)
		wg.Wait()
	}
	post := func(rt string, res map[string]any) any {
		res["resourceType"] = rt
		return map[string]any{"request": map[string]any{"method": "POST", "url": rt}, "resource": res}
	}
	var pats []any
	for p := 0; p < patients; p++ {
		pats = append(pats, post("Patient", map[string]any{"name": []any{map[string]any{"family": fmt.Sprintf("Ho%d", p%500), "given": []any{"An"}}}, "gender": []string{"male", "female"}[p%2], "birthDate": fmt.Sprintf("19%02d-0%d-1%d", 50+p%50, 1+p%9, p%9)}))
	}
	var mu sync.Mutex
	var ids []string
	parallel(pats, func(x []string) { mu.Lock(); ids = append(ids, x...); mu.Unlock() })
	var obs []any
	for p, pid := range ids {
		for k := 0; k < 4; k++ {
			obs = append(obs, post("Observation", map[string]any{
				"status": "final", "code": map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": fmt.Sprintf("%d-%d", 8000+k, p%7)}}},
				"subject": map[string]any{"reference": "Patient/" + pid}, "valueQuantity": map[string]any{"value": float64(p%200) + float64(k)/10, "unit": "mmHg"},
				"effectiveDateTime": "2025-01-01T10:00:00Z",
			}))
		}
	}
	parallel(obs, func([]string) {})
	return ids
}

// apiLoad chạy tải API trộn (60% đọc Patient, 25% tìm Observation theo subject, 15% tìm Patient theo family) bằng clients client cho tới khi stop đóng.
func apiLoad(api string, ids []string, clients int, stop <-chan struct{}) (lat []time.Duration, errs int) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			var mine []time.Duration
			bad := 0
			for {
				select {
				case <-stop:
					mu.Lock()
					lat = append(lat, mine...)
					errs += bad
					mu.Unlock()
					return
				default:
				}
				pid := ids[rnd.Intn(len(ids))]
				var url string
				switch x := rnd.Intn(100); {
				case x < 60:
					url = fmt.Sprintf("%s/Patient/%s", api, pid)
				case x < 85:
					url = fmt.Sprintf("%s/Observation?subject=Patient/%s&_count=20", api, pid)
				default:
					url = fmt.Sprintf("%s/Patient?family=Ho%d&_count=20", api, rnd.Intn(500))
				}
				t0 := time.Now()
				st, _, _, err := call("GET", url, "")
				d := time.Since(t0)
				if err != nil || st != 200 {
					bad++
					continue
				}
				mine = append(mine, d)
			}
		}(int64(c) + 1)
	}
	wg.Wait()
	return
}

// measure chạy fn (chặn tới khi xong) trong lúc lấy mẫu RSS/CPU của server.
func measure(pg *testkit.PG, pid int, fn func()) (dur, cpu, pgCPU time.Duration, peak int) {
	_, cpu0 := procStat(pid)
	pg0 := containerCPU(pg)
	stopS := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if r, _ := procStat(pid); r > peak {
				peak = r
			}
			select {
			case <-stopS:
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
	t0 := time.Now()
	fn()
	dur = time.Since(t0)
	close(stopS)
	wg.Wait()
	_, cpu1 := procStat(pid)
	return dur, cpu1 - cpu0, containerCPU(pg) - pg0, peak
}

// containerCPU đọc CPU tích lũy của container PostgreSQL từ cgroup (v2: cpu.stat usage_usec; v1: cpuacct.usage); 0 nếu không đọc được.
func containerCPU(pg *testkit.PG) time.Duration {
	out := pg.QuietExec("sh", "-c", "cat /sys/fs/cgroup/cpu.stat 2>/dev/null || cat /sys/fs/cgroup/cpuacct/cpuacct.usage")
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && f[0] == "usage_usec" {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			return time.Duration(n) * time.Microsecond
		}
		if len(f) == 1 {
			if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
				return time.Duration(n)
			}
		}
	}
	return 0
}

// runExport gọi system $export và chờ xong; trả số file kết quả.
func runExport(t *testing.T, api string) (files int, err error) {
	st, b, h, e := call("GET", api+"/$export?_type=Patient,Observation", "", "Prefer", "respond-async")
	if e != nil || st != 202 {
		return 0, fmt.Errorf("start export: %d %v %.200s", st, e, b)
	}
	loc := h.Get("Content-Location")
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		st, b, _, e := call("GET", loc, "")
		if e != nil {
			return 0, e
		}
		if st == 200 {
			var out struct {
				Output []any `json:"output"`
			}
			_ = json.Unmarshal(b, &out)
			return len(out.Output), nil
		}
		if st != 202 {
			return 0, fmt.Errorf("status export: %d %.200s", st, b)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return 0, fmt.Errorf("export quá 30 phút")
}

func TestExportLoadVersusAPI(t *testing.T) {
	if os.Getenv("TRIVITA_E2E_LOAD") != "1" {
		t.Skip("đặt TRIVITA_E2E_LOAD=1 (make bench-export) để đo tải")
	}
	testkit.RequireDocker(t)
	resources := envInt("EXPORT_LOAD_RESOURCES", 50000)
	clients := envInt("EXPORT_LOAD_CLIENTS", 16)
	baseSecs := envInt("EXPORT_LOAD_BASELINE_SECS", 20)
	conc := envInt("FHIR_JOB_CONCURRENCY", 2)
	exports := envInt("EXPORT_LOAD_EXPORTS", 1) // số export chạy đồng thời ở pha C
	ratio := envFloat("EXPORT_LOAD_MAX_RATIO", 1.5)
	patients := resources / 5

	pg := testkit.Postgres(t)
	dsn, _ := pg.NewSchema(t)
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	api := fmt.Sprintf("http://%s/fhir/R4", addr)
	p := start(t, build(t), map[string]string{
		"HTTP_ADDR": addr, "ENVIRONMENT": "local", "DATABASE_URL": dsn, "FHIR_BASE_URL": api, "FHIR_WRITE_TOKENS": "ops:" + token,
		"FHIR_RATE_LIMIT": "0", "FHIR_JOB_CONCURRENCY": strconv.Itoa(conc), "SHUTDOWN_DRAIN_DELAY": "0s", "LOG_JSON": "true",
		"DATABASE_MAX_OPEN_CONNS": strconv.Itoa(envInt("DATABASE_MAX_OPEN_CONNS", 20)),
	})
	waitReady(t, fmt.Sprintf("http://%s/healthz", addr))
	pid := p.Process.Pid

	t0 := time.Now()
	ids := seed(t, api, patients)
	// autovacuum chưa kịp phân tích bảng vừa nạp; không có thống kê thì planner chọn kế hoạch tệ (đo ra số không đại diện cho server đã chạy một thời gian)
	if err := pg.Exec(context.Background(), "ANALYZE"); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}
	t.Logf("nạp %d resource (%d Patient + %d Observation) mất %v; RSS sau nạp %d MB", 5*len(ids), len(ids), 4*len(ids), time.Since(t0).Round(time.Second), func() int { r, _ := procStat(pid); return r / 1024 }())
	t.Logf("cấu hình: %d client, FHIR_JOB_CONCURRENCY=%d, %d export đồng thời ở pha C, ngưỡng ảnh hưởng p95/p99 > %.2fx baseline", clients, conc, exports, ratio)

	// A. baseline (có một lượt khởi động ngắn để làm nóng cache/plan)
	warm := make(chan struct{})
	go func() { time.Sleep(3 * time.Second); close(warm) }()
	apiLoad(api, ids, clients, warm)
	var A result
	A.name = "A baseline (API)"
	stop := make(chan struct{})
	go func() { time.Sleep(time.Duration(baseSecs) * time.Second); close(stop) }()
	A.dur, A.cpu, A.pgCPU, A.peakRSS = measure(pg, pid, func() { A.lat, A.errs = apiLoad(api, ids, clients, stop) })
	A.log(t)

	// B. export một mình
	var B result
	B.name = "B export một mình"
	B.dur, B.cpu, B.pgCPU, B.peakRSS = measure(pg, pid, func() {
		files, err := runExport(t, api)
		if err != nil {
			t.Fatalf("export: %v", err)
		}
		B.extra = fmt.Sprintf("(%d file)", files)
	})
	B.lat = []time.Duration{0}
	B.log(t)

	// C. API + export cùng lúc: tải API chạy suốt thời gian export (kéo dài thêm 2 giây để thấy pha hạ nhiệt)
	var C result
	C.name = "C API + export"
	stopC := make(chan struct{})
	exportDur := time.Duration(0)
	C.dur, C.cpu, C.pgCPU, C.peakRSS = measure(pg, pid, func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			t1 := time.Now()
			var ew sync.WaitGroup
			var mu sync.Mutex
			files := 0
			for i := 0; i < exports; i++ {
				ew.Add(1)
				go func() {
					defer ew.Done()
					n, err := runExport(t, api)
					if err != nil {
						t.Errorf("export (song song): %v", err)
					}
					mu.Lock()
					files += n
					mu.Unlock()
				}()
			}
			ew.Wait()
			exportDur = time.Since(t1)
			C.extra = fmt.Sprintf("(%d export, xong hết sau %v, %d file)", exports, exportDur.Round(100*time.Millisecond), files)
			time.Sleep(2 * time.Second)
			close(stopC)
		}()
		C.lat, C.errs = apiLoad(api, ids, clients, stopC)
		<-done
	})
	C.log(t)

	sort.Slice(A.lat, func(i, j int) bool { return A.lat[i] < A.lat[j] })
	sort.Slice(C.lat, func(i, j int) bool { return C.lat[i] < C.lat[j] })
	r95 := float64(pct(C.lat, .95)) / float64(pct(A.lat, .95))
	r99 := float64(pct(C.lat, .99)) / float64(pct(A.lat, .99))
	rt := C.throughput / A.throughput
	verdict := "KHÔNG bị ảnh hưởng quá ngưỡng: chưa cần mở P3b (fhir-worker)"
	if r95 > ratio || r99 > ratio {
		verdict = "BỊ ẢNH HƯỞNG quá ngưỡng: cân nhắc mở P3b (fhir-worker) hoặc hạ FHIR_JOB_CONCURRENCY"
	}
	t.Logf("KẾT LUẬN: p95 x%.2f, p99 x%.2f, thông lượng API x%.2f so với baseline; export mất %v khi chạy song song so với %v một mình. %s",
		r95, r99, rt, exportDur.Round(100*time.Millisecond), B.dur.Round(100*time.Millisecond), verdict)
	if A.errs+C.errs > 0 {
		t.Errorf("có %d yêu cầu API lỗi (baseline %d, song song %d)", A.errs+C.errs, A.errs, C.errs)
	}
}
