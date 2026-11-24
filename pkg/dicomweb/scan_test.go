package dicomweb_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
)

var uids = dicomtest.Meta{StudyUID: dicomtest.UID("1"), SeriesUID: dicomtest.UID("1", "1"), SOPUID: dicomtest.UID("1", "1", "1")}

func with(m dicomtest.Meta, f func(*dicomtest.Meta)) dicomtest.Meta { f(&m); return m }

func scan(t *testing.T, b []byte, opt dicomweb.ScanOptions) (*dicomweb.ScanResult, error) {
	t.Helper()
	return dicomweb.Scan(bytes.NewReader(b), opt)
}

func TestScanNativeFramesMatchTheFileBytes(t *testing.T) {
	for _, ts := range []string{dicomtest.ExplicitLE, dicomtest.ImplicitLE} {
		b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.TransferSyntax = ts; m.Rows, m.Cols, m.Bits, m.Frames = 4, 6, 16, 3 }))
		r, err := scan(t, b, dicomweb.ScanOptions{})
		if err != nil {
			t.Fatalf("%s: %v", ts, err)
		}
		if r.TransferSyntax != ts || r.Encapsulated || !r.HasPixelData || r.Rows != 4 || r.Cols != 6 || r.Bits != 16 || r.NumberOfFrames != 3 || len(r.Frames) != 3 {
			t.Fatalf("%s: %+v", ts, r)
		}
		const fs = 4 * 6 * 2
		if r.PixelLength != 3*fs || r.PixelOffset+r.PixelLength != int64(len(b)) || r.End != int64(len(b)) {
			t.Errorf("%s: vị trí PixelData %+v, tệp %d byte", ts, r, len(b))
		}
		for i, f := range r.Frames {
			if f.Length != fs || f.Offset != r.PixelOffset+int64(i)*fs {
				t.Errorf("%s: frame %d: %+v", ts, i, f)
			}
		}
		// tag PixelData đúng ở PixelHeaderOffset
		if g, e := binary.LittleEndian.Uint16(b[r.PixelHeaderOffset:]), binary.LittleEndian.Uint16(b[r.PixelHeaderOffset+2:]); g != 0x7FE0 || e != 0x10 {
			t.Errorf("%s: PixelHeaderOffset trỏ vào (%04X,%04X)", ts, g, e)
		}
	}
}

func TestScanHeaderOnlyStopsAtPixelData(t *testing.T) {
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.Rows, m.Cols, m.Frames = 64, 64, 2 }))
	r, err := scan(t, b, dicomweb.ScanOptions{HeaderOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasPixelData || r.Frames != nil || r.PixelHeaderOffset <= 0 || r.PixelOffset <= r.PixelHeaderOffset || r.PixelLength != 64*64*2*2 {
		t.Errorf("%+v", r)
	}
	// phần cần giữ để đọc metadata là b[:PixelHeaderOffset]; scanner không được đọc hết tệp (đọc trước tối đa một bộ đệm)
	cr := &countReader{r: bytes.NewReader(b)}
	if _, err := dicomweb.Scan(cr, dicomweb.ScanOptions{HeaderOnly: true}); err != nil {
		t.Fatal(err)
	}
	if cr.n > r.PixelOffset+(1<<16) {
		t.Errorf("đọc %d byte, header chỉ %d", cr.n, r.PixelOffset)
	}
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestScanEncapsulatedFrames(t *testing.T) {
	frames := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1}, 100), bytes.Repeat([]byte{0xFF, 0xD8, 2}, 51), bytes.Repeat([]byte{0xFF, 0xD8, 3}, 7)}
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 3, frames
	}))
	r, err := scan(t, b, dicomweb.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Encapsulated || r.PixelLength != -1 || len(r.Frames) != 3 {
		t.Fatalf("%+v", r)
	}
	for i, f := range r.Frames {
		want := frames[i]
		if want, got := len(want)+len(want)%2, int(f.Length); got != want {
			t.Errorf("frame %d dài %d, muốn %d", i, got, want)
		}
		if !bytes.Equal(b[f.Offset:f.Offset+int64(len(frames[i]))], frames[i]) {
			t.Errorf("frame %d: nội dung ở offset %d không khớp", i, f.Offset)
		}
	}
	if r.End != int64(len(b)) {
		t.Errorf("End %d, tệp %d", r.End, len(b))
	}
	// HeaderOnly: biết là đóng gói nhưng không đọc fragment
	h, err := scan(t, b, dicomweb.ScanOptions{HeaderOnly: true})
	if err != nil || !h.Encapsulated || h.Frames != nil || h.End != h.PixelOffset {
		t.Errorf("HeaderOnly: %+v %v", h, err)
	}
	// nhiều fragment, nhiều frame, không có BOT: không tách được
	bad := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 2, frames
	}))
	if _, err := scan(t, bad, dicomweb.ScanOptions{}); !errors.Is(err, dicomweb.ErrMalformed) {
		t.Errorf("3 fragment cho 2 frame: %v", err)
	}
	// một frame gồm nhiều fragment: trả khoảng bao
	one := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 1, frames
	}))
	if r, err := scan(t, one, dicomweb.ScanOptions{}); err != nil || len(r.Frames) != 1 || r.Frames[0].Length < int64(len(frames[0])+len(frames[1])+len(frames[2])) {
		t.Errorf("một frame nhiều fragment: %+v %v", r, err)
	}
}

func TestScanFileWithoutPixelData(t *testing.T) {
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.NoPixelData = true }))
	r, err := scan(t, b, dicomweb.ScanOptions{HeaderOnly: true})
	if err != nil || r.HasPixelData || r.End != int64(len(b)) || r.TransferSyntax != dicomtest.ExplicitLE {
		t.Errorf("%+v %v", r, err)
	}
}

func TestScanSequences(t *testing.T) {
	item := func(undefined bool, explicit bool) dicomtest.Elem {
		return dicomtest.Sequence(explicit, 8, 0x1110, undefined, dicomtest.Item(undefined, dicomtest.E(explicit, 8, 0x1150, "UI", []byte("1.2.3"))))
	}
	for _, c := range []struct {
		name     string
		ts       string
		explicit bool
		undef    bool
	}{{"explicit xác định", dicomtest.ExplicitLE, true, false}, {"explicit không rõ", dicomtest.ExplicitLE, true, true},
		{"implicit xác định", dicomtest.ImplicitLE, false, false}, {"implicit không rõ", dicomtest.ImplicitLE, false, true}} {
		b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
			m.TransferSyntax = c.ts
			m.Extra = []dicomtest.Elem{item(c.undef, c.explicit)}
		}))
		r, err := scan(t, b, dicomweb.ScanOptions{})
		if err != nil || !r.HasPixelData || r.PixelOffset+r.PixelLength != int64(len(b)) {
			t.Errorf("%s: %+v %v", c.name, r, err)
		}
	}
}

// Chốt chặn độ sâu SQ (d0-spike.md 3.1): SQ không rõ độ dài lồng sâu làm thư viện DICOM dùng bộ nhớ bậc hai; bộ quét từ chối trước khi tới thư viện.
func TestScanRejectsDeepSequences(t *testing.T) {
	deep := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
		m.Extra = []dicomtest.Elem{dicomtest.DeepUndefinedSQ(2000)}
		m.NoPixelData = true
	}))
	if _, err := scan(t, deep, dicomweb.ScanOptions{HeaderOnly: true}); !errors.Is(err, dicomweb.ErrTooDeep) {
		t.Errorf("2000 lớp không rõ độ dài: %v", err)
	}
	// độ sâu hợp lý vẫn qua (6 lớp), ở cả hai kiểu độ dài
	for _, ex := range []dicomtest.Elem{dicomtest.DeepDefinedSQ(true, 6), dicomtest.DeepDefinedSQ(false, 6)} {
		b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
			m.Extra = []dicomtest.Elem{ex}
			if !bytes.Contains(ex, []byte("SQ")) {
				m.TransferSyntax = dicomtest.ImplicitLE
			}
		}))
		if _, err := scan(t, b, dicomweb.ScanOptions{}); err != nil {
			t.Errorf("6 lớp hợp lệ: %v", err)
		}
	}
	// vượt giới hạn bằng SQ độ dài xác định
	if _, err := scan(t, dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
		m.Extra = []dicomtest.Elem{dicomtest.DeepDefinedSQ(true, dicomweb.MaxSQDepth+2)}
	})), dicomweb.ScanOptions{}); !errors.Is(err, dicomweb.ErrTooDeep) {
		t.Errorf("vượt giới hạn: %v", err)
	}
	// SQ không rõ độ dài nấp trong một SQ có độ dài xác định không được lọt qua
	evil := dicomtest.DeepUndefinedSQ(40)
	hidden := dicomtest.Sequence(true, 8, 0x1111, false, dicomtest.Item(false, evil))
	if _, err := scan(t, dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.Extra = []dicomtest.Elem{hidden}; m.NoPixelData = true })), dicomweb.ScanOptions{}); !errors.Is(err, dicomweb.ErrTooDeep) {
		t.Errorf("SQ độc nấp trong SQ xác định: %v", err)
	}
}

func TestScanHeaderLimit(t *testing.T) {
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
		m.Extra = []dicomtest.Elem{dicomtest.E(true, 9, 0x1001, "OB", make([]byte, 5000))}
	}))
	if _, err := scan(t, b, dicomweb.ScanOptions{HeaderOnly: true, MaxHeaderBytes: 2000}); !errors.Is(err, dicomweb.ErrHeaderTooLarge) {
		t.Errorf("header 5 KB, trần 2 KB: %v", err)
	}
	if _, err := scan(t, b, dicomweb.ScanOptions{HeaderOnly: true, MaxHeaderBytes: 1 << 20}); err != nil {
		t.Errorf("trần đủ lớn: %v", err)
	}
	// độ dài khai báo giả 1 GiB: từ chối ngay, không cấp phát và không đọc
	evil := dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.Extra = []dicomtest.Elem{dicomtest.HugeLengthPrivate(1 << 30)} }))
	cr := &countReader{r: bytes.NewReader(evil)}
	if _, err := dicomweb.Scan(cr, dicomweb.ScanOptions{HeaderOnly: true, MaxHeaderBytes: 1 << 20}); !errors.Is(err, dicomweb.ErrHeaderTooLarge) {
		t.Errorf("độ dài giả: %v", err)
	}
	if _, err := scan(t, evil, dicomweb.ScanOptions{}); !errors.Is(err, dicomweb.ErrMalformed) {
		t.Errorf("không giới hạn mà độ dài giả vượt tệp phải là cấu trúc hỏng: %v", err)
	}
}

func TestScanRejectsBadInput(t *testing.T) {
	good := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {}))
	for name, b := range map[string][]byte{
		"rỗng": nil, "không phải DICOM": bytes.Repeat([]byte("hello"), 100), "cắt giữa tiền tố": good[:100],
		"BE":       dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.TransferSyntax = dicomtest.ExplicitBE })),
		"deflated": dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.TransferSyntax = dicomtest.Deflated })),
	} {
		_, err := scan(t, b, dicomweb.ScanOptions{})
		if err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
	if _, err := scan(t, bytes.Repeat([]byte("hello"), 100), dicomweb.ScanOptions{}); !errors.Is(err, dicomweb.ErrNotDICOM) {
		t.Errorf("không phải DICOM: %v", err)
	}
	if _, err := scan(t, dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.TransferSyntax = dicomtest.ExplicitBE })), dicomweb.ScanOptions{}); !errors.Is(err, dicomweb.ErrUnsupportedSyntax) {
		t.Errorf("Big Endian: %v", err)
	}
	// cắt giữa header (sau tiền tố): cấu trúc hỏng, không treo
	hdr, _ := dicomtest.Header(with(uids, func(m *dicomtest.Meta) {}))
	if _, err := scan(t, hdr[:len(hdr)-3], dicomweb.ScanOptions{HeaderOnly: true}); !errors.Is(err, dicomweb.ErrMalformed) {
		t.Errorf("header bị cắt: %v", err)
	}
}

// Tệp cắt giữa PixelData: bộ quét báo đúng độ dài khai báo để STOW so với số byte thực nhận (d0-spike.md 3.3).
func TestScanReportsDeclaredPixelLengthOfTruncatedFile(t *testing.T) {
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) { m.Rows, m.Cols = 16, 16; m.TruncatePixel = 100 }))
	r, err := scan(t, b, dicomweb.ScanOptions{HeaderOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.PixelOffset+r.PixelLength <= int64(len(b)) {
		t.Errorf("độ dài khai báo %d phải vượt tệp %d", r.PixelOffset+r.PixelLength, len(b))
	}
}
