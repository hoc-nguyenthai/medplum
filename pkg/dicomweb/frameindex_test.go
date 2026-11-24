package dicomweb_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"trivita-fhir/pkg/dicomtest"
	"trivita-fhir/pkg/dicomweb"
)

func TestFrameIndexRoundTrip(t *testing.T) {
	in := dicomweb.FrameIndex{TransferSyntax: dicomtest.JPEGBaseline, Frames: []dicomweb.FrameRange{
		{Offset: 100, Length: 50},
		{Offset: 160, Length: 60, Parts: []dicomweb.FrameRange{{Offset: 168, Length: 20}, {Offset: 196, Length: 24}}},
	}}
	s, ok := in.Encode()
	if !ok {
		t.Fatal("không mã hóa được")
	}
	out, err := dicomweb.ParseFrameIndex(s)
	if err != nil || !reflect.DeepEqual(*out, in) {
		t.Fatalf("%+v %v\n%s", out, err, s)
	}
	if out.Frames[1].Size() != 44 {
		t.Errorf("Size: %d", out.Frames[1].Size())
	}
	if !strings.HasPrefix(s, `{"v":1,"ts":"1.2.840.10008.1.2.4.50","f":[[100,50],[160,60,[[168,20],[196,24]]]]}`) {
		t.Errorf("dạng gọn: %s", s)
	}
}

func TestFrameIndexRejectsBadAndHugeInput(t *testing.T) {
	for name, s := range map[string]string{
		"rỗng": "", "không phải JSON": "{", "sai phiên bản": `{"v":2,"ts":"x","f":[[1,2]]}`, "không có frame": `{"v":1,"ts":"x","f":[]}`,
		"thiếu độ dài": `{"v":1,"ts":"x","f":[[1]]}`, "offset âm": `{"v":1,"ts":"x","f":[[-1,5]]}`, "độ dài 0": `{"v":1,"ts":"x","f":[[1,0]]}`,
		"không phải số": `{"v":1,"ts":"x","f":[["a",5]]}`, "fragment ngoài khoảng": `{"v":1,"ts":"x","f":[[10,20,[[5,5],[12,5]]]]}`,
		"fragment chồng": `{"v":1,"ts":"x","f":[[10,20,[[10,10],[15,5]]]]}`, "một fragment": `{"v":1,"ts":"x","f":[[10,20,[[10,10]]]]}`,
		"fragment vượt": `{"v":1,"ts":"x","f":[[10,20,[[10,10],[20,15]]]]}`,
	} {
		if _, err := dicomweb.ParseFrameIndex(s); !errors.Is(err, dicomweb.ErrBadFrameIndex) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, ok := (dicomweb.FrameIndex{}).Encode(); ok {
		t.Error("không có frame thì không mã hóa")
	}
	many := dicomweb.FrameIndex{TransferSyntax: "x"}
	for i := 0; i < 30000; i++ { // vượt MaxFrameIndexBytes
		many.Frames = append(many.Frames, dicomweb.FrameRange{Offset: int64(i) * 1000000, Length: 999999})
	}
	if _, ok := many.Encode(); ok {
		t.Error("chỉ mục quá lớn không được lưu")
	}
}

func TestIndexEncapsulatedMatchesFullScan(t *testing.T) {
	frames := [][]byte{bytes.Repeat([]byte{1}, 33), bytes.Repeat([]byte{2}, 8), bytes.Repeat([]byte{3}, 120)}
	b := dicomtest.Build(with(uids, func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 3, frames
	}))
	full, err := scan(t, b, dicomweb.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	head, err := scan(t, b, dicomweb.ScanOptions{HeaderOnly: true})
	if err != nil || head.Frames != nil {
		t.Fatalf("header: %+v %v", head, err)
	}
	// chỉ mục lập từ luồng bắt đầu ngay đầu giá trị PixelData, cho cùng kết quả với quét đầy đủ
	got, end, err := dicomweb.IndexEncapsulated(bytes.NewReader(b[head.PixelOffset:]), head.PixelOffset, 3)
	if err != nil || !reflect.DeepEqual(got, full.Frames) || end != full.End || end != int64(len(b)) {
		t.Errorf("%+v end=%d (muốn %+v end=%d) %v", got, end, full.Frames, full.End, err)
	}
	// tệp bị cắt: lỗi, không trả chỉ mục dở dang
	if _, _, err := dicomweb.IndexEncapsulated(bytes.NewReader(b[head.PixelOffset:len(b)-20]), head.PixelOffset, 3); err == nil {
		t.Error("tệp đóng gói bị cắt phải lỗi")
	}
	// không có Basic Offset Table hợp lệ
	if _, _, err := dicomweb.IndexEncapsulated(bytes.NewReader(make([]byte, 64)), 0, 1); !errors.Is(err, dicomweb.ErrMalformed) {
		t.Errorf("rác: %v", err)
	}
}
