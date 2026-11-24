package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func inst(study, series, sop string, mut ...func(*dicomtest.Meta)) []byte {
	m := dicomtest.Meta{StudyUID: dicomtest.UID(study), SeriesUID: dicomtest.UID(study, series), SOPUID: dicomtest.UID(study, series, sop), StudyDate: "20240102"}
	for _, f := range mut {
		f(&m)
	}
	return dicomtest.Build(m)
}

// stowBody dựng thân multipart/related từ các tệp.
func stowBody(files ...io.Reader) (ct string, body io.Reader) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		for _, f := range files {
			w, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/dicom"}})
			if _, err := io.Copy(w, f); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.CloseWithError(mw.Close())
	}()
	return `multipart/related; type="application/dicom"; boundary="` + mw.Boundary() + `"`, pr
}

func send(t *testing.T, srv *httptest.Server, path, token, ct string, body io.Reader) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", ct)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func countIn(t *testing.T, repo interface {
	Search(ctx context.Context, req *fhirsearch.Request) (*fhirrouter.SearchResult, error)
}, project, rt string) int {
	t.Helper()
	req, _ := fhirsearch.Parse(rt, nil)
	req.Total, req.Count = "accurate", 1
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "t", Project: project, Role: fhirauth.RoleWrite})
	res, err := repo.Search(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return *res.Total
}

func TestStowOverHTTP(t *testing.T) {
	c, h := handler(t, settings(t, true, func(s *config.Settings) { s.BaseURL = "https://dicom.test" }))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")
	count := func(project, rt string) int { return countIn(t, c.Repo, project, rt) }

	ct, body := stowBody(bytes.NewReader(inst("1", "1", "1")), bytes.NewReader(inst("1", "1", "2")), bytes.NewReader(inst("1", "2", "1")))
	code, hdr, raw := send(t, srv, "/dicomweb/studies", wr, ct, body)
	if code != 200 || hdr.Get("Content-Type") != "application/dicom+json" {
		t.Fatalf("%d %q %s", code, hdr.Get("Content-Type"), raw)
	}
	var resp map[string]struct {
		VR    string
		Value []any
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if got := resp["00081190"].Value; len(got) != 1 || got[0] != "https://dicom.test/dicomweb/studies/"+dicomtest.UID("1") {
		t.Errorf("RetrieveURL cấp study: %v", got)
	}
	if len(resp["00081199"].Value) != 3 || resp["00081198"].Value != nil {
		t.Errorf("phản hồi: %s", raw)
	}
	if count("A", "DicomStudy") != 1 || count("A", "DicomSeries") != 2 || count("A", "DicomInstance") != 3 || count("A", "Binary") != 3 {
		t.Errorf("A: study %d, series %d, instance %d, binary %d", count("A", "DicomStudy"), count("A", "DicomSeries"), count("A", "DicomInstance"), count("A", "Binary"))
	}

	// tải lại và STOW vào đúng study: idempotent
	ct, body = stowBody(bytes.NewReader(inst("1", "1", "1")))
	if code, _, raw := send(t, srv, "/dicomweb/studies/"+dicomtest.UID("1"), wr, ct, body); code != 200 {
		t.Fatalf("vào study: %d %s", code, raw)
	}
	if count("A", "DicomInstance") != 3 || count("A", "Binary") != 3 {
		t.Error("tải lại tạo bản trùng")
	}
	// instance của study khác khi POST vào study cụ thể: 409 với FailureReason 0xA900 (43264)
	ct, body = stowBody(bytes.NewReader(inst("9", "1", "1")))
	if code, _, raw := send(t, srv, "/dicomweb/studies/"+dicomtest.UID("1"), wr, ct, body); code != 409 || !strings.Contains(string(raw), "43264") {
		t.Errorf("sai study: %d %s", code, raw)
	}

	// quyền, project, xác thực
	ct, body = stowBody(bytes.NewReader(inst("2", "1", "1")))
	if code, _, _ := send(t, srv, "/dicomweb/studies", rd, ct, body); code != 403 {
		t.Errorf("token đọc: %d", code)
	}
	ct, body = stowBody(bytes.NewReader(inst("2", "1", "1")))
	if code, _, _ := send(t, srv, "/dicomweb/studies", "", ct, body); code != 401 {
		t.Errorf("không token: %d", code)
	}
	ct, body = stowBody(bytes.NewReader(inst("2", "1", "1")))
	if code, _, _ := send(t, srv, "/projects/B/dicomweb/studies", wr, ct, body); code != 403 {
		t.Errorf("project khác trong URL: %d", code)
	}
	if count("A", "DicomStudy") != 1 {
		t.Error("yêu cầu bị từ chối vẫn tạo dữ liệu")
	}
	ct, body = stowBody(bytes.NewReader(inst("1", "1", "1")))
	if code, _, _ := send(t, srv, "/dicomweb/studies", tok(t, "B", "fhir.write"), ct, body); code != 200 {
		t.Errorf("project B cùng UID: %d", code)
	}
	if count("A", "DicomInstance") != 3 || count("B", "DicomInstance") != 1 {
		t.Errorf("cách ly project: A %d, B %d instance", count("A", "DicomInstance"), count("B", "DicomInstance"))
	}

	// lỗi cả yêu cầu
	if code, _, _ := send(t, srv, "/dicomweb/studies", wr, "application/dicom", bytes.NewReader(inst("3", "1", "1"))); code != 415 {
		t.Errorf("không phải multipart: %d", code)
	}
	// type không nháy kép (client sai chuẩn)
	ct, body = stowBody(bytes.NewReader(inst("3", "1", "1")))
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, strings.ReplaceAll(strings.Replace(ct, `type="application/dicom"`, "type=application/dicom", 1), `"`, ""), body); code != 200 {
		t.Errorf("type không nháy kép: %d %s", code, raw)
	}
}

// Tệp hỏng và tệp độc bị từ chối riêng, nhanh và không tốn bộ nhớ; không để lại gì.
func TestStowRejectsBadAndHostileFiles(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr := tok(t, "A", "fhir.write")
	bomb := inst("1", "1", "1", func(m *dicomtest.Meta) {
		m.Extra = []dicomtest.Elem{dicomtest.DeepUndefinedSQ(2000)}
		m.NoPixelData = true
	})
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	for name, data := range map[string][]byte{
		"bom SQ 60 KB": bomb, "cắt giữa PixelData": inst("1", "1", "2", func(m *dicomtest.Meta) { m.Rows, m.Cols = 64, 64; m.TruncatePixel = 500 }),
		"không phải DICOM": bytes.Repeat([]byte("x"), 3000), "độ dài giả 1 GiB": inst("1", "1", "3", func(m *dicomtest.Meta) { m.Extra = []dicomtest.Elem{dicomtest.HugeLengthPrivate(1 << 30)} }),
	} {
		ct, body := stowBody(bytes.NewReader(data))
		code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body)
		// độ dài khai báo 1 GiB làm phần đầu vượt giới hạn (A700 = 42752); các tệp còn lại là không hiểu được (C000 = 49152)
		reason := "49152"
		if strings.HasPrefix(name, "độ dài giả") {
			reason = "42752"
		}
		if code != 409 || !strings.Contains(string(raw), reason) {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("từ chối tệp độc mất %v", d)
	}
	if grew := int64(after.TotalAlloc - before.TotalAlloc); grew > 200<<20 {
		t.Errorf("tệp độc làm cấp phát %d MB (bom SQ 2000 lớp của thư viện DICOM tốn 2,2 GB)", grew>>20)
	}
	for _, rt := range []string{"DicomStudy", "DicomInstance", "Binary"} {
		if n := countIn(t, c.Repo, "A", rt); n != 0 {
			t.Errorf("%s còn %d sau khi từ chối", rt, n)
		}
	}
}

// Trần kích thước: Content-Length vượt DICOMWEB_MAX_REQUEST_BYTES thì 413 ngay; instance vượt DICOMWEB_MAX_INSTANCE_BYTES bị từ chối riêng (A700 = 42752).
func TestStowSizeLimits(t *testing.T) {
	_, h := handler(t, settings(t, true, func(s *config.Settings) { s.MaxRequestBytes, s.MaxInstanceBytes = 20000, 3000 }))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr := tok(t, "A", "fhir.write")

	big := inst("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols = 100, 100 }) // ~20 KB
	ct, body := stowBody(bytes.NewReader(big))
	buf, _ := io.ReadAll(body)
	if code, _, _ := send(t, srv, "/dicomweb/studies", wr, ct, bytes.NewReader(buf)); code != 413 { // có Content-Length
		t.Errorf("Content-Length vượt trần: %d", code)
	}
	ct, body = stowBody(bytes.NewReader(big)) // chunked, không biết trước
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body); code != 413 {
		t.Errorf("thân chunked vượt trần: %d %s", code, raw)
	}
	ct, body = stowBody(bytes.NewReader(inst("1", "1", "2", func(m *dicomtest.Meta) { m.Rows, m.Cols = 40, 40 })), bytes.NewReader(inst("1", "1", "3", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits = 4, 4, 8 })))
	code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body)
	if code != 202 || !strings.Contains(string(raw), "42752") || !strings.Contains(string(raw), "00081199") {
		t.Errorf("instance quá lớn: %d %s", code, raw)
	}
}

// Truyền luồng: tải một tệp 100 MB (chỉ vài MB bộ nhớ, kể cả phía client trong cùng tiến trình), nội dung ghi vào kho đúng từng byte và đủ độ dài.
func TestStowStreamsLargeFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("bỏ qua với -short")
	}
	c, h := handler(t, settings(t, true, func(s *config.Settings) { s.MaxRequestBytes, s.MaxInstanceBytes = 1<<30, 1<<30 }))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr := tok(t, "A", "fhir.write")

	stream, size := dicomtest.Stream(dicomtest.Meta{StudyUID: dicomtest.UID("1"), SeriesUID: dicomtest.UID("1", "1"), SOPUID: dicomtest.UID("1", "1", "1"), Rows: 2048, Cols: 2048, Bits: 16, Frames: 13})
	var peak atomic.Uint64
	stop := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peak.Load() {
					peak.Store(m.HeapInuse)
				}
			}
		}
	}()
	runtime.GC()
	ct, body := stowBody(stream)
	code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body)
	close(stop)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if size < 100<<20 {
		t.Fatalf("tệp thử chỉ %d byte", size)
	}
	t.Logf("tải %d MB, đỉnh heap %d MB", size>>20, peak.Load()>>20)
	if p := peak.Load(); p > 80<<20 {
		t.Errorf("đỉnh bộ nhớ %d MB khi tải %d MB: phải truyền luồng", p>>20, size>>20)
	}
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "t", Project: "A", Role: fhirauth.RoleWrite})
	req, _ := fhirsearch.Parse("Binary", nil)
	res, err := c.Repo.Search(ctx, req)
	if err != nil || len(res.Matches) != 1 {
		t.Fatalf("Binary: %v %v", res, err)
	}
	bin := res.Matches[0].(*fhir.Binary)
	key, _ := binarystore.Key(bin.ID, bin.Meta.VersionId)
	rc, err := c.Blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	// so từng byte với luồng sinh lại cùng quy luật (không nạp cả tệp vào bộ nhớ)
	want, _ := dicomtest.Stream(dicomtest.Meta{StudyUID: dicomtest.UID("1"), SeriesUID: dicomtest.UID("1", "1"), SOPUID: dicomtest.UID("1", "1", "1"), Rows: 2048, Cols: 2048, Bits: 16, Frames: 13})
	if n, err := sameBytes(rc, want); err != nil || n != size {
		t.Errorf("nội dung lưu khác nội dung gửi: %d byte so với %d (%v)", n, size, err)
	}
}

// sameBytes so hai luồng từng khối; trả số byte khớp liên tiếp từ đầu.
func sameBytes(a, b io.Reader) (int64, error) {
	ba, bb := make([]byte, 1<<20), make([]byte, 1<<20)
	var n int64
	for {
		na, ea := io.ReadFull(a, ba)
		nb, eb := io.ReadFull(b, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return n, io.ErrUnexpectedEOF
		}
		n += int64(na)
		if ea != nil || eb != nil {
			return n, nil
		}
	}
}

// Tải song song cùng một study trên cơ sở dữ liệu thật (SQLite, transaction và khóa thật, không phải bộ nhớ tuần tự): đúng một study, một series, một instance chung, và
// số liệu tổng hợp đúng.
func TestStowConcurrentOverRealDatabase(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr := tok(t, "A", "fhir.write")
	const n = 8
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			ct, body := stowBody(bytes.NewReader(inst("1", "1", "900")), bytes.NewReader(inst("1", "1", string(rune('1'+i)))))
			code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body)
			if code != 200 {
				errs <- string(raw)
				return
			}
			errs <- ""
		}(i)
	}
	for i := 0; i < n; i++ {
		if e := <-errs; e != "" {
			t.Errorf("yêu cầu song song lỗi: %s", e)
		}
	}
	count := func(rt string) int { return countIn(t, c.Repo, "A", rt) }
	if count("DicomStudy") != 1 || count("DicomSeries") != 1 || count("DicomInstance") != n+1 {
		t.Fatalf("study %d, series %d, instance %d (muốn 1, 1, %d)", count("DicomStudy"), count("DicomSeries"), count("DicomInstance"), n+1)
	}
	if count("Binary") != n+1 {
		t.Errorf("Binary %d (muốn %d): bên thua phải dọn", count("Binary"), n+1)
	}
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "t", Project: "A", Role: fhirauth.RoleWrite})
	req, _ := fhirsearch.Parse("DicomStudy", nil)
	res, err := c.Repo.Search(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	study := res.Matches[0].(*fhir.DicomStudy)
	if study.NumberOfStudyRelatedInstances == nil || *study.NumberOfStudyRelatedInstances != n+1 || *study.NumberOfStudyRelatedSeries != 1 {
		t.Errorf("số liệu tổng hợp: %v", study)
	}
}
