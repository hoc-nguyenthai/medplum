package dicomweb_test

import (
	"bytes"
	"errors"
	"testing"

	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
)

func TestCopyFrameNativeMatchesPixelBytes(t *testing.T) {
	px := make([]byte, 4*6*2*3)
	for i := range px {
		px[i] = byte(i * 3)
	}
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 4, 6, 16, 3, px }))
	r, err := scan(t, b, dicomweb.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range r.Frames {
		var got bytes.Buffer
		n, err := dicomweb.CopyFrame(bytes.NewReader(b), f, &got)
		if err != nil || n != 48 || !bytes.Equal(got.Bytes(), px[i*48:(i+1)*48]) {
			t.Errorf("frame %d: %d byte %v", i, n, err)
		}
	}
}

func TestCopyFrameRejectsShortFiles(t *testing.T) {
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.Rows, m.Cols = 16, 16 }))
	r, _ := scan(t, b, dicomweb.ScanOptions{})
	var sink bytes.Buffer
	if _, err := dicomweb.CopyFrame(bytes.NewReader(b[:len(b)-10]), r.Frames[0], &sink); !errors.Is(err, dicomweb.ErrMalformed) {
		t.Errorf("tệp ngắn hơn frame: %v", err)
	}
	if _, err := dicomweb.CopyFrame(bytes.NewReader(b[:100]), r.Frames[0], &sink); !errors.Is(err, dicomweb.ErrMalformed) {
		t.Errorf("tệp ngắn hơn vị trí frame: %v", err)
	}
	if _, err := dicomweb.CopyFrame(bytes.NewReader(b), dicomweb.FrameRange{Parts: []dicomweb.FrameRange{{Offset: 500, Length: 4}, {Offset: 300, Length: 4}}}, &sink); !errors.Is(err, dicomweb.ErrMalformed) {
		t.Errorf("khoảng không tăng dần: %v", err)
	}
}

func TestFrameContentType(t *testing.T) {
	for ts, want := range map[string]string{
		dicomtest.ExplicitLE: "application/octet-stream", dicomtest.ImplicitLE: "application/octet-stream", "": "application/octet-stream",
		dicomtest.JPEGBaseline:    "image/jpeg; transfer-syntax=1.2.840.10008.1.2.4.50",
		"1.2.840.10008.1.2.4.70":  "image/jpeg; transfer-syntax=1.2.840.10008.1.2.4.70",
		"1.2.840.10008.1.2.4.90":  "image/jp2; transfer-syntax=1.2.840.10008.1.2.4.90",
		"1.2.840.10008.1.2.4.80":  "image/jls; transfer-syntax=1.2.840.10008.1.2.4.80",
		"1.2.840.10008.1.2.4.92":  "image/jpx; transfer-syntax=1.2.840.10008.1.2.4.92",
		"1.2.840.10008.1.2.4.201": "image/jphc; transfer-syntax=1.2.840.10008.1.2.4.201",
		"1.2.840.10008.1.2.5":     "application/octet-stream; transfer-syntax=1.2.840.10008.1.2.5",
	} {
		if got := dicomweb.FrameContentType(ts); got != want {
			t.Errorf("FrameContentType(%q) = %q, muốn %q", ts, got, want)
		}
	}
	if dicomweb.FrameMediaType(dicomtest.JPEGBaseline) != "image/jpeg" || dicomweb.FrameMediaType("1.2.840.10008.1.2.5") != "application/octet-stream" {
		t.Error("FrameMediaType")
	}
}
