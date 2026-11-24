package dicomrender_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"trivita-fhir/pkg/dicomrender"
)

func gray16(vals ...uint16) []byte {
	var b []byte
	for _, v := range vals {
		b = append(b, byte(v), byte(v>>8))
	}
	return b
}

func decodePNG(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func grayAt(img image.Image, x, y int) uint8 {
	return color.GrayModel.Convert(img.At(x, y)).(color.Gray).Y
}

func TestNegotiate(t *testing.T) {
	for accept, want := range map[string]string{
		"": dicomrender.JPEG, "*/*": dicomrender.JPEG, "image/png": dicomrender.PNG, "image/jpeg": dicomrender.JPEG, "image/*": dicomrender.JPEG,
		"image/png;q=0.5, image/jpeg;q=0.9": dicomrender.JPEG, "image/jpeg;q=0.1, image/png": dicomrender.PNG, "text/html, image/png": dicomrender.PNG,
	} {
		if got, ok := dicomrender.Negotiate(accept); !ok || got != want {
			t.Errorf("%q: %q %v, muốn %q", accept, got, ok, want)
		}
	}
	for _, accept := range []string{"application/pdf", "image/png;q=0", "image/gif"} {
		if _, ok := dicomrender.Negotiate(accept); ok {
			t.Errorf("%q phải bị từ chối", accept)
		}
	}
}

func TestGrayWindowing(t *testing.T) {
	p := dicomrender.Params{Rows: 1, Cols: 4, BitsAllocated: 16, BitsStored: 16, Photometric: "MONOCHROME2"}
	pix := gray16(0, 100, 200, 400)
	// không cửa sổ: min..max -> 0..255
	b, ct, err := dicomrender.FromNative(p, pix, dicomrender.Options{Format: dicomrender.PNG})
	if err != nil || ct != dicomrender.PNG {
		t.Fatal(err, ct)
	}
	img := decodePNG(t, b)
	if got := []uint8{grayAt(img, 0, 0), grayAt(img, 3, 0)}; got[0] != 0 || got[1] != 255 {
		t.Errorf("min/max: %v", got)
	}
	// cửa sổ của instance: 100..300 -> 0, 0, 128, 255 (kẹp)
	p.Center, p.Width = 200, 200
	b, _, _ = dicomrender.FromNative(p, pix, dicomrender.Options{Format: dicomrender.PNG})
	img = decodePNG(t, b)
	if g := []uint8{grayAt(img, 0, 0), grayAt(img, 1, 0), grayAt(img, 2, 0), grayAt(img, 3, 0)}; g[0] != 0 || g[1] != 0 || g[2] < 126 || g[2] > 129 || g[3] != 255 {
		t.Errorf("cửa sổ instance: %v", g)
	}
	// cửa sổ của client thắng
	b, _, _ = dicomrender.FromNative(p, pix, dicomrender.Options{Format: dicomrender.PNG, Center: 50, Width: 100})
	img = decodePNG(t, b)
	if g := []uint8{grayAt(img, 0, 0), grayAt(img, 1, 0), grayAt(img, 2, 0)}; g[0] != 0 || g[1] != 255 || g[2] != 255 {
		t.Errorf("cửa sổ client: %v", g)
	}
	// MONOCHROME1 đảo
	p.Center, p.Width, p.Photometric = 0, 0, "MONOCHROME1"
	b, _, _ = dicomrender.FromNative(p, pix, dicomrender.Options{Format: dicomrender.PNG})
	if img = decodePNG(t, b); grayAt(img, 0, 0) != 255 || grayAt(img, 3, 0) != 0 {
		t.Errorf("MONOCHROME1: %d %d", grayAt(img, 0, 0), grayAt(img, 3, 0))
	}
	// có dấu và rescale: -1000 + 1*v, v là int16
	p = dicomrender.Params{Rows: 1, Cols: 2, BitsAllocated: 16, PixelRepresentation: 1, Photometric: "MONOCHROME2", Slope: 2, Intercept: -10}
	b, _, _ = dicomrender.FromNative(p, gray16(0xFFFF, 10), dicomrender.Options{Format: dicomrender.PNG, Center: 0, Width: 40})
	// -1*2-10 = -12 -> (-12+20)/40*255 = 51; 10*2-10 = 10 -> (10+20)/40 = 191
	if img = decodePNG(t, b); grayAt(img, 0, 0) < 49 || grayAt(img, 0, 0) > 53 || grayAt(img, 1, 0) < 189 || grayAt(img, 1, 0) > 193 {
		t.Errorf("có dấu và rescale: %d %d", grayAt(img, 0, 0), grayAt(img, 1, 0))
	}
}

func TestRGBInterleavedAndPlanar(t *testing.T) {
	p := dicomrender.Params{Rows: 1, Cols: 2, Samples: 3, BitsAllocated: 8, Photometric: "RGB"}
	b, _, err := dicomrender.FromNative(p, []byte{255, 0, 0, 0, 0, 255}, dicomrender.Options{Format: dicomrender.PNG})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, b)
	if r, _, _, _ := img.At(0, 0).RGBA(); r>>8 != 255 {
		t.Error("điểm 0 phải đỏ")
	}
	if _, _, bl, _ := img.At(1, 0).RGBA(); bl>>8 != 255 {
		t.Error("điểm 1 phải xanh dương")
	}
	p.PlanarConfiguration = 1
	b, _, _ = dicomrender.FromNative(p, []byte{10, 20, 30, 40, 50, 60}, dicomrender.Options{Format: dicomrender.PNG})
	img = decodePNG(t, b)
	for x, want := range [][3]uint32{{10, 30, 50}, {20, 40, 60}} {
		if r, g, bl, _ := img.At(x, 0).RGBA(); r>>8 != want[0] || g>>8 != want[1] || bl>>8 != want[2] {
			t.Errorf("planar điểm %d: %d %d %d, muốn %v", x, r>>8, g>>8, bl>>8, want)
		}
	}
}

func TestJPEGEncodingAndErrors(t *testing.T) {
	p := dicomrender.Params{Rows: 2, Cols: 2, BitsAllocated: 8, Photometric: "MONOCHROME2"}
	b, ct, err := dicomrender.FromNative(p, []byte{0, 85, 170, 255}, dicomrender.Options{Format: dicomrender.JPEG})
	if err != nil || ct != dicomrender.JPEG {
		t.Fatal(err, ct)
	}
	if _, err := jpeg.Decode(bytes.NewReader(b)); err != nil {
		t.Errorf("JPEG đầu ra không giải mã được: %v", err)
	}
	// không hỗ trợ
	for name, bad := range map[string]dicomrender.Params{
		"YBR":              {Rows: 1, Cols: 1, Samples: 3, BitsAllocated: 8, Photometric: "YBR_FULL"},
		"bảng màu":         {Rows: 1, Cols: 1, BitsAllocated: 8, Photometric: "PALETTE COLOR"},
		"32 bit":           {Rows: 1, Cols: 1, BitsAllocated: 32, Photometric: "MONOCHROME2"},
		"không kích thước": {BitsAllocated: 8, Photometric: "MONOCHROME2"},
		"quá lớn":          {Rows: 1 << 20, Cols: 1 << 20, BitsAllocated: 8, Photometric: "MONOCHROME2"},
	} {
		if _, _, err := dicomrender.FromNative(bad, make([]byte, 16), dicomrender.Options{}); !errors.Is(err, dicomrender.ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// thiếu dữ liệu không phải ErrUnsupported
	if _, _, err := dicomrender.FromNative(p, []byte{1}, dicomrender.Options{}); err == nil || errors.Is(err, dicomrender.ErrUnsupported) {
		t.Errorf("thiếu dữ liệu: %v", err)
	}
}

func TestFromJPEG(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	var src bytes.Buffer
	_ = jpeg.Encode(&src, img, nil)
	b, ct, err := dicomrender.FromJPEG(src.Bytes(), dicomrender.Options{Format: dicomrender.JPEG})
	if err != nil || ct != dicomrender.JPEG || !bytes.Equal(b, src.Bytes()) {
		t.Errorf("trả nguyên: %v %q", err, ct)
	}
	b, ct, err = dicomrender.FromJPEG(src.Bytes(), dicomrender.Options{Format: dicomrender.PNG})
	if err != nil || ct != dicomrender.PNG {
		t.Fatal(err, ct)
	}
	if got := decodePNG(t, b).Bounds(); got.Dx() != 4 || got.Dy() != 4 {
		t.Errorf("kích thước PNG: %v", got)
	}
	for _, f := range []string{dicomrender.JPEG, dicomrender.PNG} {
		if _, _, err := dicomrender.FromJPEG([]byte{0xFF, 0xD8, 1, 2, 3}, dicomrender.Options{Format: f}); err == nil {
			t.Errorf("JPEG hỏng (%s) phải lỗi", f)
		}
	}
}
