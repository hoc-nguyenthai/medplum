package bootstrap_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"trivita-fhir/apps/dicomweb/internal/config"
	"trivita-fhir/pkg/dicomtest"
)

func render(t *testing.T, srv *httptest.Server, path, token, accept string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	r, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, r.Header, b
}

func grayAtPNG(t *testing.T, b []byte, x, y int) uint8 {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("không phải PNG: %v", err)
	}
	return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
}

// D7 rendered: xám 16 bit và nhiều frame 8 bit thành PNG, JPEG baseline trả nguyên hoặc đổi sang PNG, và mọi trường hợp từ chối.
func TestD7Rendered(t *testing.T) {
	_, h := handler(t, settings(t, true))
	srv := httptest.NewServer(h)
	defer srv.Close()
	wr, rd := tok(t, "A", "fhir.write"), tok(t, "A", "fhir.read")

	// 16 bit: 4 điểm 0, 1000, 2000, 4000 (little endian)
	px16 := []byte{0, 0, 0xE8, 0x03, 0xD0, 0x07, 0xA0, 0x0F}
	// 8 bit hai frame 2x2: frame 1 toàn 0..., frame 2 tăng dần
	px8 := []byte{10, 10, 10, 10, 0, 85, 170, 255}
	var jbuf bytes.Buffer
	g := image.NewGray(image.Rect(0, 0, 8, 8))
	for i := range g.Pix {
		g.Pix[i] = byte(i * 3)
	}
	if err := jpeg.Encode(&jbuf, g, nil); err != nil {
		t.Fatal(err)
	}
	files := [][]byte{
		inst("1", "1", "1", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Pixel = 1, 4, 16, px16 }),
		inst("1", "1", "2", func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 2, 2, 8, 2, px8 }),
		inst("1", "1", "3", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Fragments = dicomtest.JPEGBaseline, 8, 8, 8, [][]byte{jbuf.Bytes()}
		}),
		inst("1", "1", "4", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Fragments = "1.2.840.10008.1.2.4.90", 8, 8, 8, [][]byte{{1, 2, 3, 4}}
		}),
		inst("1", "1", "6", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Fragments = "1.2.840.10008.1.2.4.51", 8, 8, 8, [][]byte{jbuf.Bytes()}
		}),
		inst("1", "1", "7", func(m *dicomtest.Meta) {
			m.TransferSyntax, m.Rows, m.Cols, m.Bits, m.Fragments = "1.2.840.10008.1.2.5", 8, 8, 8, [][]byte{bytes.Repeat([]byte{1}, 16)}
		}),
		inst("1", "1", "5", func(m *dicomtest.Meta) { m.NoPixelData = true; m.Modality = "SR" }),
	}
	var readers []io.Reader
	for _, f := range files {
		readers = append(readers, bytes.NewReader(f))
	}
	ct, body := stowBody(readers...)
	if code, _, raw := send(t, srv, "/dicomweb/studies", wr, ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	base := "/dicomweb/studies/" + dicomtest.UID("1") + "/series/" + dicomtest.UID("1", "1") + "/instances/"
	i := func(n string) string { return base + dicomtest.UID("1", "1", n) }

	// xám 16 bit -> PNG: min..max thành 0..255, điểm giữa tỉ lệ
	code, hdr, raw := render(t, srv, i("1")+"/rendered", rd, "image/png")
	if code != 200 || hdr.Get("Content-Type") != "image/png" || hdr.Get("Content-Length") == "" {
		t.Fatalf("PNG 16 bit: %d %q", code, hdr.Get("Content-Type"))
	}
	if a, b, c := grayAtPNG(t, raw, 0, 0), grayAtPNG(t, raw, 1, 0), grayAtPNG(t, raw, 3, 0); a != 0 || c != 255 || b < 62 || b > 66 {
		t.Errorf("window mặc định: %d %d %d (muốn 0, ~64, 255)", a, b, c)
	}
	// cửa sổ của client 0..1000 -> điểm 1000, 2000, 4000 đều 255
	_, _, raw = render(t, srv, i("1")+"/rendered?window=500,1000", rd, "image/png")
	if grayAtPNG(t, raw, 0, 0) != 0 || grayAtPNG(t, raw, 1, 0) != 255 || grayAtPNG(t, raw, 2, 0) != 255 {
		t.Errorf("window client: %d %d %d", grayAtPNG(t, raw, 0, 0), grayAtPNG(t, raw, 1, 0), grayAtPNG(t, raw, 2, 0))
	}
	// mặc định (không Accept) là JPEG
	code, hdr, raw = render(t, srv, i("1")+"/rendered?quality=70", rd, "")
	if _, err := jpeg.Decode(bytes.NewReader(raw)); code != 200 || hdr.Get("Content-Type") != "image/jpeg" || err != nil {
		t.Errorf("JPEG mặc định: %d %q %v", code, hdr.Get("Content-Type"), err)
	}
	// nhiều frame: frame 2 là 0,85,170,255; frame 1 phẳng; instance không kèm frame là frame 1
	_, _, raw = render(t, srv, i("2")+"/frames/2/rendered", rd, "image/png")
	if a, b, c, d := grayAtPNG(t, raw, 0, 0), grayAtPNG(t, raw, 1, 0), grayAtPNG(t, raw, 0, 1), grayAtPNG(t, raw, 1, 1); a != 0 || b != 85 || c != 170 || d != 255 {
		t.Errorf("frame 2: %d %d %d %d", a, b, c, d)
	}
	_, _, f1 := render(t, srv, i("2")+"/frames/1/rendered", rd, "image/png")
	_, _, inst1 := render(t, srv, i("2")+"/rendered", rd, "image/png")
	if !bytes.Equal(f1, inst1) {
		t.Error("rendered của instance phải là frame 1")
	}
	// JPEG baseline: trả nguyên byte; PNG: giải mã rồi mã hóa lại đúng kích thước
	code, hdr, raw = render(t, srv, i("3")+"/rendered", rd, "image/jpeg")
	if code != 200 || hdr.Get("Content-Type") != "image/jpeg" || !bytes.Equal(raw, jbuf.Bytes()) {
		t.Errorf("JPEG nguyên: %d %q equal=%v", code, hdr.Get("Content-Type"), bytes.Equal(raw, jbuf.Bytes()))
	}
	code, hdr, raw = render(t, srv, i("3")+"/frames/1/rendered", rd, "image/png")
	if img, err := png.Decode(bytes.NewReader(raw)); code != 200 || hdr.Get("Content-Type") != "image/png" || err != nil || img.Bounds().Dx() != 8 {
		t.Errorf("JPEG sang PNG: %d %q %v", code, hdr.Get("Content-Type"), err)
	}

	// từ chối
	for name, c := range map[string]struct {
		path, accept string
		want         int
	}{
		"Accept không hỗ trợ":                    {i("1") + "/rendered", "application/pdf", 406},
		"JPEG 2000 chưa hỗ trợ":                  {i("4") + "/rendered", "image/png", 406},
		"frame vượt số frame":                    {i("1") + "/frames/2/rendered", "image/png", 416},
		"frame 0":                                {i("1") + "/frames/0/rendered", "image/png", 400},
		"danh sách frame":                        {i("2") + "/frames/1,2/rendered", "image/png", 400},
		"quality 0":                              {i("1") + "/rendered?quality=0", "image/jpeg", 400},
		"window hỏng":                            {i("1") + "/rendered?window=a,b", "image/png", 400},
		"window rộng 0":                          {i("1") + "/rendered?window=5,0", "image/png", 400},
		"JPEG mở rộng 12 bit":                    {i("6") + "/rendered", "image/png", 406},
		"RLE (octet-stream kèm transfer-syntax)": {i("7") + "/rendered", "image/png", 406},
		"không có ảnh (SR)":                      {i("5") + "/rendered", "image/png", 404},
		"instance không có":                      {i("9") + "/rendered", "image/png", 404},
	} {
		if code, _, raw := render(t, srv, c.path, rd, c.accept); code != c.want {
			t.Errorf("%s: %d, muốn %d (%s)", name, code, c.want, raw)
		}
	}
	// xác thực và project
	if code, _, _ := render(t, srv, i("1")+"/rendered", tok(t, "B", "fhir.read"), "image/png"); code != 404 {
		t.Errorf("project khác: %d", code)
	}
	if code, _, _ := render(t, srv, i("1")+"/rendered", "", "image/png"); code != 401 {
		t.Errorf("không token: %d", code)
	}
}

// Frame lớn hơn trần bộ nhớ của rendered thì 413, không đọc hết vào bộ nhớ.
func TestD7RenderedRefusesHugeFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("STOW 67 MB")
	}
	_, h := handler(t, settings(t, true, func(s *config.Settings) { s.MaxRequestBytes, s.MaxInstanceBytes = 1<<30, 1<<30 }))
	srv := httptest.NewServer(h)
	defer srv.Close()
	m := dicomtest.Meta{StudyUID: dicomtest.UID("1"), SeriesUID: dicomtest.UID("1", "1"), SOPUID: dicomtest.UID("1", "1", "1"), Rows: 8200, Cols: 8200, Bits: 8}
	stream, _ := dicomtest.Stream(m)
	ct, body := stowBody(stream)
	if code, _, raw := send(t, srv, "/dicomweb/studies", tok(t, "A", "fhir.write"), ct, body); code != 200 {
		t.Fatalf("STOW: %d %s", code, raw)
	}
	path := "/dicomweb/studies/" + dicomtest.UID("1") + "/series/" + dicomtest.UID("1", "1") + "/instances/" + dicomtest.UID("1", "1", "1") + "/rendered"
	if code, _, raw := render(t, srv, path, tok(t, "A", "fhir.read"), "image/png"); code != 413 {
		t.Errorf("frame 67 MB: %d %.200s", code, raw)
	}
}
