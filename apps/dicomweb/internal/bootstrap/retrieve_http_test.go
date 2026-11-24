package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomtest"
)

func get(t *testing.T, srv *httptest.Server, path, token string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
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

func multipartParts(t *testing.T, h http.Header, raw []byte) (typ string, parts [][]byte, types []string) {
	t.Helper()
	mt, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || mt != "multipart/related" {
		t.Fatalf("Content-Type %q: %v", h.Get("Content-Type"), err)
	}
	if h.Get("Content-Length") != "" && h.Get("Content-Length") != itoa(len(raw)) {
		t.Errorf("Content-Length %s, thực tế %d", h.Get("Content-Length"), len(raw))
	}
	mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		parts, types = append(parts, b), append(types, p.Header.Get("Content-Type"))
	}
	return params["type"], parts, types
}

func itoa(n int) string { return strconv.Itoa(n) }

// STOW rồi truy vấn lại qua HTTP thật: QIDO-RS (study, series, phân trang), WADO-RS (metadata, frame không nén và JPEG, danh sách frame) và mọi mã lỗi.
func TestQidoWadoRoundTrip(t *testing.T) {
	_, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")

	px := make([]byte, 4*6*3)
	for i := range px {
		px[i] = byte(i*11 + 3)
	}
	jpegFrames := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 9, 8}, 25), bytes.Repeat([]byte{0xFF, 0xD8, 7, 6}, 13)}
	files := [][]byte{
		inst("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 4, 6, 8, 3, px }),
		inst("1", "1", "2", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 2, jpegFrames
		}),
		inst("1", "2", "1", func(m *dicomtest.Meta) { m.Modality = "MR" }),
		inst("2", "1", "1", func(m *dicomtest.Meta) { m.NoPixelData = true; m.Modality = "SR" }),
	}
	readers := make([]io.Reader, len(files))
	for i, f := range files {
		readers[i] = bytes.NewReader(f)
	}
	ct, body := stowBody(readers...)
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	s1, se1 := dicomtest.UID("1"), dicomtest.UID("1", "1")
	path := "/dicomweb/studies/" + s1 + "/series/" + se1

	// QIDO: study
	code, hdr, raw := get(t, srv, "/dicomweb/studies", rd)
	var studies []map[string]struct{ Value []any }
	if code != 200 || hdr.Get("Content-Type") != "application/dicom+json" || json.Unmarshal(raw, &studies) != nil || len(studies) != 2 {
		t.Fatalf("studies: %d %q %s", code, hdr.Get("Content-Type"), raw)
	}
	if code, hdr, raw := get(t, srv, "/dicomweb/studies?limit=1", rd); code != 200 || !strings.HasPrefix(hdr.Get("Warning"), "299 ") || bytes.Count(raw, []byte(`"0020000D"`)) != 1 {
		t.Errorf("limit=1: %d warning=%q %s", code, hdr.Get("Warning"), raw)
	}
	if _, hdr, _ := get(t, srv, "/dicomweb/studies?limit=2", rd); hdr.Get("Warning") != "" {
		t.Errorf("hết kết quả mà vẫn có Warning: %q", hdr.Get("Warning"))
	}
	if code, _, raw := get(t, srv, "/dicomweb/studies?limit=abc&offset=-3", rd); code != 200 || bytes.Count(raw, []byte(`"0020000D"`)) != 2 {
		t.Errorf("tham số hỏng dùng mặc định: %d %s", code, raw)
	}
	// QIDO: series
	code, _, raw = get(t, srv, "/dicomweb/studies/"+s1+"/series", rd)
	var series []map[string]struct{ Value []any }
	if code != 200 || json.Unmarshal(raw, &series) != nil || len(series) != 2 || series[0]["0020000D"].Value[0] != s1 {
		t.Fatalf("series: %d %s", code, raw)
	}
	if code, _, raw := get(t, srv, "/dicomweb/studies/"+dicomtest.UID("9")+"/series", rd); code != 404 || !strings.Contains(string(raw), "Study not found") {
		t.Errorf("study không có: %d %s", code, raw)
	}
	// WADO: metadata
	code, hdr, raw = get(t, srv, path+"/metadata", rd)
	var meta []map[string]struct{ Value []any }
	if code != 200 || hdr.Get("Content-Type") != "application/dicom+json" || json.Unmarshal(raw, &meta) != nil || len(meta) != 2 {
		t.Fatalf("metadata: %d %q %s", code, hdr.Get("Content-Type"), raw)
	}
	if bytes.Contains(raw, []byte("7FE00010")) || bytes.Contains(raw, []byte(`"00020010"`)) {
		t.Error("metadata có PixelData hoặc file meta")
	}
	// WADO: frame không nén, từng frame và danh sách
	i1 := path + "/instances/" + dicomtest.UID("1", "1", "1")
	for fn := 1; fn <= 3; fn++ {
		code, hdr, raw := get(t, srv, i1+"/frames/"+string(rune('0'+fn)), rd)
		typ, parts, types := multipartParts(t, hdr, raw)
		if code != 200 || typ != "application/octet-stream" || len(parts) != 1 || !bytes.Equal(parts[0], px[(fn-1)*24:fn*24]) || types[0] != "application/octet-stream" {
			t.Errorf("frame %d: %d type=%q %d phần", fn, code, typ, len(parts))
		}
	}
	code, hdr, raw = get(t, srv, i1+"/frames/3,1", rd)
	if _, parts, _ := multipartParts(t, hdr, raw); code != 200 || len(parts) != 2 || !bytes.Equal(parts[0], px[48:72]) || !bytes.Equal(parts[1], px[0:24]) {
		t.Errorf("frames/3,1: %d %d phần", code, len(parts))
	}
	// WADO: frame JPEG
	i2 := path + "/instances/" + dicomtest.UID("1", "1", "2")
	code, hdr, raw = get(t, srv, i2+"/frames/2", rd)
	typ, parts, types := multipartParts(t, hdr, raw)
	if code != 200 || typ != "image/jpeg" || !bytes.Equal(parts[0], jpegFrames[1]) || types[0] != "image/jpeg; transfer-syntax="+dicomtest.JPEGBaseline {
		t.Errorf("JPEG: %d type=%q %q", code, typ, types)
	}
	// lỗi
	for name, c := range map[string]struct {
		path string
		want int
	}{
		"frame 0": {i1 + "/frames/0", 400}, "frame chữ": {i1 + "/frames/abc", 400}, "danh sách hỏng": {i1 + "/frames/1,,2", 400}, "vượt số frame": {i1 + "/frames/4", 416},
		"instance không có":        {path + "/instances/" + dicomtest.UID("1", "1", "9") + "/frames/1", 404},
		"series không có":          {"/dicomweb/studies/" + s1 + "/series/" + dicomtest.UID("1", "9") + "/instances/x/frames/1", 404},
		"không có ảnh (SR)":        {"/dicomweb/studies/" + dicomtest.UID("2") + "/series/" + dicomtest.UID("2", "1") + "/instances/" + dicomtest.UID("2", "1", "1") + "/frames/1", 404},
		"metadata series không có": {"/dicomweb/studies/" + s1 + "/series/" + dicomtest.UID("1", "9") + "/metadata", 404},
	} {
		if code, _, raw := get(t, srv, c.path, rd); code != c.want {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	// route chưa cài vẫn 501, tiền tố khác cùng phục vụ, xác thực và project
	if code, _, _ := get(t, srv, path+"/instances/x/bulkdata/y", rd); code != 501 {
		t.Errorf("bulkdata: %d", code)
	}
	if code, _, raw := get(t, srv, "/api/projects/A/dicomweb/studies", rd); code != 200 || bytes.Count(raw, []byte(`"0020000D"`)) != 2 {
		t.Errorf("tiền tố /api/projects/A: %d", code)
	}
	if code, _, _ := get(t, srv, "/projects/B/dicomweb/studies", rd); code != 403 {
		t.Errorf("project khác trong URL: %d", code)
	}
	if code, _, _ := get(t, srv, "/dicomweb/studies", ""); code != 401 {
		t.Errorf("không token: %d", code)
	}
	if code, _, raw := get(t, srv, "/dicomweb/studies", tok(t, "B", "fhir.read")); code != 200 || strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("project B không thấy gì: %d %s", code, raw)
	}
	if code, _, _ := get(t, srv, i1+"/frames/1", tok(t, "B", "fhir.read")); code != 404 {
		t.Errorf("frame của A qua B: %d", code)
	}
}

// Lấy một frame của tệp 104 MB (frame cuối): đúng từng byte và bộ nhớ nhỏ vì chép theo luồng, không nạp cả tệp.
func TestWadoFrameOfLargeFileStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("bỏ qua với -short")
	}
	c, h := handler(t, settings(t, true, func(s *config.Settings) { s.MaxRequestBytes, s.MaxInstanceBytes = 1<<30, 1<<30 }))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")
	m := dicomtest.Meta{StudyUID: dicomtest.UID("1"), SeriesUID: dicomtest.UID("1", "1"), SOPUID: dicomtest.UID("1", "1", "1"), Rows: 2048, Cols: 2048, Bits: 16, Frames: 13}
	stream, _ := dicomtest.Stream(m)
	ct, body := stowBody(stream)
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}

	// đếm byte đọc ra từ kho khi lấy frame (chỉ mục + đọc theo khoảng: chỉ đúng byte của frame, không phải cả tệp 104 MB)
	cb := &countingBlobs{Store: c.Blobs}
	c.Blobs = cb
	var peak atomic.Uint64
	stop := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak.Load() {
					peak.Store(ms.HeapInuse)
				}
			}
		}
	}()
	runtime.GC()
	req, _ := http.NewRequest("GET", srv.URL+"/dicomweb/studies/"+dicomtest.UID("1")+"/series/"+dicomtest.UID("1", "1")+"/instances/"+dicomtest.UID("1", "1", "1")+"/frames/13", nil)
	req.Header.Set("Authorization", "Bearer "+rd)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, params, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	mr := multipart.NewReader(res.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	// so với quy luật của dicomtest.Stream: byte thứ i của PixelData là byte(7*i); frame 13 bắt đầu sau 12 frame
	const frameSize = 2048 * 2048 * 2
	start := int64(12) * frameSize
	buf := make([]byte, 1<<20)
	var got int64
	for {
		n, err := part.Read(buf)
		for j := 0; j < n; j++ {
			if buf[j] != byte(7*(start+got+int64(j))) {
				t.Fatalf("sai byte ở vị trí %d của frame", got+int64(j))
			}
		}
		got += int64(n)
		if err != nil {
			break
		}
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	close(stop)
	if got != frameSize || res.StatusCode != 200 {
		t.Fatalf("%d, frame %d byte (muốn %d)", res.StatusCode, got, frameSize)
	}
	t.Logf("lấy frame 8 MB từ tệp 104 MB: đọc %d MB từ kho, đỉnh heap %d MB", cb.n.Load()>>20, peak.Load()>>20)
	if n := cb.n.Load(); n != frameSize {
		t.Errorf("đọc %d byte từ kho để lấy frame %d byte: phải đọc đúng khoảng của frame (chỉ mục frame lập lúc STOW)", n, frameSize)
	}
	if p := peak.Load(); p > 60<<20 {
		t.Errorf("đỉnh bộ nhớ %d MB: phải chép theo luồng", p>>20)
	}
}

// countingBlobs đếm byte đọc ra từ kho (cả Open lẫn OpenRange).
type countingBlobs struct {
	binarystore.Store
	n atomic.Int64
}

type countRC struct {
	io.ReadCloser
	n *atomic.Int64
}

func (c countRC) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func (c *countingBlobs) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := c.Store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return countRC{rc, &c.n}, nil
}

func (c *countingBlobs) OpenRange(ctx context.Context, key string, off, n int64) (io.ReadCloser, error) {
	rc, err := c.Store.(binarystore.RangeOpener).OpenRange(ctx, key, off, n)
	if err != nil {
		return nil, err
	}
	return countRC{rc, &c.n}, nil
}

// Ảnh nén lớn (30 frame JPEG 1 MB): chỉ mục lập song song với việc ghi (không đọc lại tệp), rồi một frame cuối chỉ đọc đúng 1 MB từ kho thay vì cả tệp 30 MB.
func TestCompressedFrameUsesIndexAndReadsOnlyItsBytes(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")
	frames := make([][]byte, 30)
	for i := range frames {
		frames[i] = bytes.Repeat([]byte{0xFF, 0xD8, byte(i), 0xAB}, 256<<10) // 1 MiB
	}
	data := inst("1", "1", "1", func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, len(frames), frames
	})
	ct, body := stowBody(bytes.NewReader(data))
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	cb := &countingBlobs{Store: c.Blobs}
	c.Blobs = cb
	path := "/dicomweb/studies/" + dicomtest.UID("1") + "/series/" + dicomtest.UID("1", "1") + "/instances/" + dicomtest.UID("1", "1", "1") + "/frames/"
	code, hdr, raw := get(t, srv, path+"30", rd)
	typ, parts, types := multipartParts(t, hdr, raw)
	if code != 200 || typ != "image/jpeg" || len(parts) != 1 || !bytes.Equal(parts[0], frames[29]) || types[0] != "image/jpeg; transfer-syntax="+dicomtest.JPEGBaseline {
		t.Fatalf("frame 30: %d type=%q", code, typ)
	}
	if n := cb.n.Load(); n != int64(len(frames[29])) {
		t.Errorf("đọc %d byte từ kho để lấy frame %d byte (tệp %d byte)", n, len(frames[29]), len(data))
	}
	if code, _, raw := get(t, srv, path+"31", rd); code != 416 {
		t.Errorf("frame 31: %d %s", code, raw)
	}
}
