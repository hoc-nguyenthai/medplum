package bootstrap_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
)

func metaStow(meta string, bulks map[string][]byte, types map[string]string) (string, *bytes.Buffer) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	pw, _ := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/dicom+json"}})
	pw.Write([]byte(meta))
	for loc, data := range bulks {
		h := textproto.MIMEHeader{"Content-Location": {loc}, "Content-Type": {types[loc]}}
		bw, _ := w.CreatePart(h)
		bw.Write(data)
	}
	w.Close()
	return `multipart/related; type="application/dicom+json"; boundary="` + w.Boundary() + `"`, &buf
}

func metaInstance(study, series, sop, uri string, frames, rows int) string {
	return fmt.Sprintf(`{"00080016":{"vr":"UI","Value":["%s"]},"00080018":{"vr":"UI","Value":["%s"]},"00080060":{"vr":"CS","Value":["CT"]},"00080020":{"vr":"DA","Value":["20240102"]},
	"00100010":{"vr":"PN","Value":[{"Alphabetic":"Le^Van C"}]},"00100020":{"vr":"LO","Value":["PID-88"]},"0020000D":{"vr":"UI","Value":["%s"]},"0020000E":{"vr":"UI","Value":["%s"]},
	"00280002":{"vr":"US","Value":[1]},"00280004":{"vr":"CS","Value":["MONOCHROME2"]},"00280008":{"vr":"IS","Value":["%d"]},"00280010":{"vr":"US","Value":[%d]},"00280011":{"vr":"US","Value":[%d]},
	"00280100":{"vr":"US","Value":[8]},"00280101":{"vr":"US","Value":[8]},"00280102":{"vr":"US","Value":[7]},"00280103":{"vr":"US","Value":[0]},"7FE00010":{"vr":"OB","BulkDataURI":"%s"}}`,
		dicomtest.CTImage, dicomtest.UID(study, series, sop), dicomtest.UID(study), dicomtest.UID(study, series), frames, rows, rows, uri)
}

// STOW dạng metadata qua HTTP thật rồi đọc lại bằng mọi đường: frame, bulkdata, tải tệp (đọc lại được bằng bộ quét), QIDO, rendered; sai thì mã lỗi chuẩn; không để lại tệp tạm.
func TestD10MetadataStowRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	_, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")

	px := make([]byte, 8*8*3)
	for i := range px {
		px[i] = byte(i*9 + 2)
	}
	ct, body := metaStow("["+metaInstance("1", "1", "1", "http://bulk/1", 3, 8)+"]", map[string][]byte{"http://bulk/1": px}, map[string]string{"http://bulk/1": "application/octet-stream"})
	code, hdr, raw := send(t, srv, "/dicomweb/studies", wr, ct, body)
	if code != 200 || hdr.Get("Content-Type") != "application/dicom+json" || !strings.Contains(string(raw), "00081199") || strings.Contains(string(raw), "00081198") {
		t.Fatalf("STOW metadata: %d %s", code, raw)
	}
	if !strings.Contains(string(raw), "/studies/"+dicomtest.UID("1")+"/series/"+dicomtest.UID("1", "1")+"/instances/"+dicomtest.UID("1", "1", "1")) {
		t.Errorf("thiếu RetrieveURL: %s", raw)
	}
	i1 := "/dicomweb/studies/" + dicomtest.UID("1") + "/series/" + dicomtest.UID("1", "1") + "/instances/" + dicomtest.UID("1", "1", "1")

	// frame 2 khớp byte đã gửi
	code, h2, raw := get(t, srv, i1+"/frames/2", rd)
	_, parts, _ := multipartParts(t, h2, raw)
	if code != 200 || len(parts) != 1 || !bytes.Equal(parts[0], px[64:128]) {
		t.Fatalf("frame 2: %d", code)
	}
	// bulkdata và tệp tải về: Part 10 hợp lệ, quét ra đúng 3 frame
	code, h3, raw := get(t, srv, i1+"/bulkdata/7FE00010", rd)
	_, parts, _ = multipartParts(t, h3, raw)
	if code != 200 || len(parts) != 1 || !bytes.Equal(parts[0], px) {
		t.Fatalf("bulkdata: %d", code)
	}
	code, h4, raw := get(t, srv, i1, rd)
	_, parts, _ = multipartParts(t, h4, raw)
	scan, err := dicomweb.Scan(bytes.NewReader(parts[0]), dicomweb.ScanOptions{})
	if code != 200 || err != nil || len(scan.Frames) != 3 || scan.TransferSyntax != "1.2.840.10008.1.2.1" {
		t.Fatalf("tải tệp: %d %v %+v", code, err, scan)
	}
	// QIDO thấy bệnh nhân; rendered dựng được
	if code, _, raw := get(t, srv, "/dicomweb/studies?PatientID=PID-88", rd); code != 200 || bytes.Count(raw, []byte(`"0020000D"`)) != 1 {
		t.Errorf("QIDO: %d %s", code, raw)
	}
	if code, _, _ := render(t, srv, i1+"/frames/3/rendered", rd, "image/png"); code != 200 {
		t.Errorf("rendered: %d", code)
	}
	// project khác không thấy
	if code, _, _ := get(t, srv, i1, tok(t, "B", "fhir.read")); code != 404 {
		t.Errorf("project khác: %d", code)
	}
	// token đọc không được STOW dạng metadata
	ct2, body2 := metaStow("["+metaInstance("2", "1", "1", "u", 1, 8)+"]", map[string][]byte{"u": px[:64]}, map[string]string{"u": "application/octet-stream"})
	if code, _, _ := send(t, srv, "/dicomweb/studies", rd, ct2, body2); code != 403 {
		t.Errorf("token đọc: %d", code)
	}
	// thiếu bulkdata: 409 với FailedSOPSequence; JSON hỏng: 400
	ct3, body3 := metaStow("["+metaInstance("3", "1", "1", "khong-co", 1, 8)+"]", nil, nil)
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct3, body3); code != 409 || !strings.Contains(string(raw), "00081198") {
		t.Errorf("thiếu bulkdata: %d %s", code, raw)
	}
	ct4, body4 := metaStow(`[{"oops"`, nil, nil)
	if code, _, _ := send(t, srv, "/dicomweb/studies", wr, ct4, body4); code != 400 {
		t.Errorf("JSON hỏng: %d", code)
	}
	// POST /studies/{uid}: instance của study khác bị từ chối riêng
	ct5, body5 := metaStow("["+metaInstance("4", "1", "1", "u", 1, 8)+"]", map[string][]byte{"u": px[:64]}, map[string]string{"u": "application/octet-stream"})
	if code, _, _ := send(t, srv, "/dicomweb/studies/"+dicomtest.UID("1"), wr, ct5, body5); code != 409 {
		t.Errorf("sai study: %d", code)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("còn %d tệp tạm trong TMPDIR", len(entries))
	}
}

// Bulkdata lớn đi qua tệp tạm chứ không qua bộ nhớ: 96 MB gửi dạng metadata, đỉnh heap vẫn nhỏ, frame cuối đọc đúng byte (đọc theo khoảng), tệp tạm được xóa.
func TestD10MetadataStowLargeBulkdataStaysOnDisk(t *testing.T) {
	if testing.Short() {
		t.Skip("96 MB")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	_, h := handler(t, settings(t, true, func(s *config.Settings) { s.MaxRequestBytes, s.MaxInstanceBytes = 1<<30, 1<<30 }))
	srv := httptest.NewServer(h)
	defer srv.Close()
	const frames, rows = 24, 2048 // 24 frame 2048x2048 8 bit = 96 MiB
	frameSize := rows * rows
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		w, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/dicom+json"}})
		w.Write([]byte("[" + metaInstance("1", "1", "1", "big", frames, rows) + "]"))
		bw, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Location": {"big"}, "Content-Type": {"application/octet-stream"}})
		buf := make([]byte, 1<<20)
		for off := 0; off < frames*frameSize; off += len(buf) {
			for i := range buf {
				buf[i] = byte(7 * (off + i))
			}
			if _, err := bw.Write(buf); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.CloseWithError(mw.Close())
	}()
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
	code, _, raw := send(t, srv, "/dicomweb/studies", tok(t, "A", "fhir.write"), `multipart/related; type="application/dicom+json"; boundary="`+mw.Boundary()+`"`, pr)
	close(stop)
	if code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	if p := peak.Load(); p > 64<<20 {
		t.Errorf("đỉnh heap %d MB cho bulkdata 96 MB", p>>20)
	}
	path := "/dicomweb/studies/" + dicomtest.UID("1") + "/series/" + dicomtest.UID("1", "1") + "/instances/" + dicomtest.UID("1", "1", "1") + "/frames/24"
	code, hdr, body := get(t, srv, path, tok(t, "A", "fhir.read"))
	_, parts, _ := multipartParts(t, hdr, body)
	if code != 200 || len(parts) != 1 || len(parts[0]) != frameSize {
		t.Fatalf("frame 24: %d", code)
	}
	start := 23 * frameSize
	for i := 0; i < frameSize; i += 4093 {
		if parts[0][i] != byte(7*(start+i)) {
			t.Fatalf("byte %d của frame 24 sai", i)
		}
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("còn %d tệp tạm", len(entries))
	}
}
