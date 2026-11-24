package bootstrap_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"net/url"
	"testing"

	"trivita-fhir/pkg/dicomtest"
)

// D9: bulkdata PixelData (không nén một phần, nén mỗi frame một phần, đọc theo khoảng) và WADO-URI (tệp nguyên văn, ảnh, lỗi).
func TestD9BulkdataAndWadoURI(t *testing.T) {
	c, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")

	px := make([]byte, 16*16*3) // 3 frame 16x16 8 bit
	for i := range px {
		px[i] = byte(i*5 + 1)
	}
	jf := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1, 2}, 100), bytes.Repeat([]byte{0xFF, 0xD8, 3, 4}, 50)}
	var jb bytes.Buffer
	_ = jpeg.Encode(&jb, image.NewGray(image.Rect(0, 0, 8, 8)), nil)
	files := map[string][]byte{
		"1": inst("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 16, 16, 8, 3, px }),
		"2": inst("1", "1", "2", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 8, 8, 2, jf
		}),
		"3": inst("1", "1", "3", func(m *dicomtest.Meta) { m.NoPixelData = true; m.Modality = "SR" }),
		"4": inst("1", "1", "4", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Fragments = dicomtest.JPEGBaseline, 8, 8, 8, [][]byte{jb.Bytes()}
		}),
	}
	ct, body := stowBody(bytes.NewReader(files["1"]), bytes.NewReader(files["2"]), bytes.NewReader(files["3"]), bytes.NewReader(files["4"]))
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	s1, se1 := dicomtest.UID("1"), dicomtest.UID("1", "1")
	ipath := func(n string) string {
		return "/dicomweb/studies/" + s1 + "/series/" + se1 + "/instances/" + dicomtest.UID("1", "1", n)
	}

	// bulkdata không nén: MỘT phần chứa mọi frame; chỉ đọc đúng số byte điểm ảnh từ kho (đọc theo khoảng)
	cb := &countingBlobs{Store: c.Blobs}
	c.Blobs = cb
	code, hdr, raw := get(t, srv, ipath("1")+"/bulkdata/7FE00010", rd)
	typ, parts, types := multipartParts(t, hdr, raw)
	if code != 200 || typ != "application/octet-stream" || len(parts) != 1 || !bytes.Equal(parts[0], px) || types[0] != "application/octet-stream" {
		t.Fatalf("bulkdata không nén: %d type=%q %d phần", code, typ, len(parts))
	}
	if hdr.Get("Content-Length") == "" {
		t.Error("thiếu Content-Length")
	}
	if n := cb.n.Load(); n != int64(len(px)) {
		t.Errorf("đọc %d byte từ kho, muốn %d", n, len(px))
	}
	if _, _, raw2 := get(t, srv, ipath("1")+"/bulkdata/7fe00010", rd); !bytes.Equal(raw, raw2) && len(raw2) == 0 {
		t.Error("tag viết thường phải được chấp nhận")
	}
	// bulkdata nén: mỗi frame một phần
	code, hdr, raw = get(t, srv, ipath("2")+"/bulkdata/7FE00010", rd)
	typ, parts, types = multipartParts(t, hdr, raw)
	if code != 200 || typ != "image/jpeg" || len(parts) != 2 || !bytes.Equal(parts[0], jf[0]) || !bytes.Equal(parts[1], jf[1]) || types[0] != "image/jpeg; transfer-syntax="+dicomtest.JPEGBaseline {
		t.Errorf("bulkdata nén: %d type=%q %d phần %q", code, typ, len(parts), types)
	}
	for name, p := range map[string]string{
		"tag khác":          ipath("1") + "/bulkdata/00091001",
		"tag overlay":       ipath("1") + "/bulkdata/60003000",
		"không có ảnh (SR)": ipath("3") + "/bulkdata/7FE00010",
		"instance không có": ipath("9") + "/bulkdata/7FE00010",
	} {
		if code, _, _ := get(t, srv, p, rd); code != 404 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _, _ := get(t, srv, ipath("1")+"/bulkdata/7FE00010", tok(t, "B", "fhir.read")); code != 404 {
		t.Errorf("project khác: %d", code)
	}

	// WADO-URI
	wado := func(kv ...string) string {
		v := url.Values{"requestType": {"WADO"}, "studyUID": {s1}, "seriesUID": {se1}, "objectUID": {dicomtest.UID("1", "1", "1")}}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return "/dicomweb/wado?" + v.Encode()
	}
	code, hdr, raw = get(t, srv, wado("contentType", "application/dicom"), rd)
	if code != 200 || hdr.Get("Content-Type") != "application/dicom" || !bytes.Equal(raw, files["1"]) {
		t.Fatalf("WADO-URI tệp: %d %q equal=%v", code, hdr.Get("Content-Type"), bytes.Equal(raw, files["1"]))
	}
	// ảnh: mặc định JPEG frame 1; PNG frame 2 với cửa sổ; khớp với rendered của WADO-RS
	code, hdr, raw = get(t, srv, wado(), rd)
	if _, err := jpeg.Decode(bytes.NewReader(raw)); code != 200 || hdr.Get("Content-Type") != "image/jpeg" || err != nil {
		t.Errorf("WADO-URI mặc định: %d %q %v", code, hdr.Get("Content-Type"), err)
	}
	code, _, raw = get(t, srv, wado("contentType", "image/png", "frameNumber", "2", "windowCenter", "128", "windowWidth", "256"), rd)
	_, _, viaRS := render(t, srv, ipath("1")+"/frames/2/rendered?window=128,256", rd, "image/png")
	if _, err := png.Decode(bytes.NewReader(raw)); code != 200 || err != nil || !bytes.Equal(raw, viaRS) {
		t.Errorf("WADO-URI PNG khác rendered của WADO-RS: %d %v", code, err)
	}
	// JPEG baseline nguyên byte
	uri4 := "/dicomweb/wado?requestType=WADO&studyUID=" + s1 + "&seriesUID=" + se1 + "&objectUID=" + dicomtest.UID("1", "1", "4") + "&contentType=image/jpeg"
	if code, _, raw := get(t, srv, uri4, rd); code != 200 || !bytes.Equal(raw, jb.Bytes()) {
		t.Errorf("WADO-URI JPEG nguyên: %d", code)
	}
	for name, c := range map[string]struct {
		path string
		want int
	}{
		"objectUID không có":      {wado("objectUID", dicomtest.UID("1", "1", "9")), 404},
		"seriesUID sai":           {wado("seriesUID", dicomtest.UID("1", "9")), 404},
		"studyUID không có":       {wado("studyUID", dicomtest.UID("9")), 404},
		"SR không có ảnh":         {wado("objectUID", dicomtest.UID("1", "1", "3")), 404},
		"SR tải tệp được":         {wado("objectUID", dicomtest.UID("1", "1", "3"), "contentType", "application/dicom"), 200},
		"contentType lạ":          {wado("contentType", "text/html"), 406},
		"frame vượt":              {wado("frameNumber", "9"), 416},
		"frame hỏng":              {wado("frameNumber", "x"), 400},
		"anonymize":               {wado("anonymize", "yes"), 400},
		"requestType sai":         {wado("requestType", "FOO"), 400},
		"imageQuality sai":        {wado("imageQuality", "999"), 400},
		"windowWidth thiếu":       {wado("windowCenter", "5"), 400},
		"JPEG lưu trong kho hỏng": {wado("objectUID", dicomtest.UID("1", "1", "2"), "contentType", "image/png", "frameNumber", "1"), 500}, // dữ liệu giả không giải mã được: lỗi phía server, không lộ chi tiết
	} {
		if code, _, raw := get(t, srv, c.path, rd); code != c.want {
			t.Errorf("%s: %d, muốn %d (%.100s)", name, code, c.want, raw)
		}
	}
	if code, _, _ := get(t, srv, wado(), ""); code != 401 {
		t.Errorf("không token: %d", code)
	}
	if code, _, _ := get(t, srv, wado(), tok(t, "B", "fhir.read")); code != 404 {
		t.Errorf("project khác: %d", code)
	}
}
