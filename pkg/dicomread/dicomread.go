// Package dicomread đọc metadata của tệp DICOM bằng thư viện github.com/suyashkumar/dicom (MIT) và đổi sang dicomweb.Dataset (DICOM JSON model). Thư viện được
// cô lập trong gói này; phần còn lại của dicomweb chỉ biết Dataset.
//
// Luôn đọc với SkipPixelData (không giữ pixel trong bộ nhớ: tệp 120 MB tốn 1,8 MB thay vì 127 MB) và CHỈ gọi trên dữ liệu đã qua dicomweb.Scan (giới hạn kích
// thước phần đầu và độ sâu SQ), vì thư viện không tự chặn SQ lồng sâu (docs/plans/cds-dicom/d0-spike.md 3.1).
package dicomread

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"trivita-fhir/pkg/dicomweb"
)

// ErrUnreadable là lỗi khi thư viện không đọc được cấu trúc DICOM.
var ErrUnreadable = errors.New("dicomread: không đọc được metadata DICOM")

// ReadHeader đọc mọi phần tử (cả file meta nhóm 0002) của dữ liệu header: các byte từ đầu tệp tới ngay trước phần tử PixelData (hoặc cả tệp nếu không có
// PixelData). Dataset chưa làm sạch: gọi dicomweb.CleanDataset trước khi lưu.
func ReadHeader(header []byte) (dicomweb.Dataset, error) {
	if len(header) < 132 || string(header[128:132]) != "DICM" {
		return nil, fmt.Errorf("%w: thiếu tiền tố DICM", ErrUnreadable)
	}
	ds, err := dicom.Parse(bytes.NewReader(header), int64(len(header)), nil, dicom.SkipPixelData())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	return fromElements(ds.Elements), nil
}

func fromElements(elems []*dicom.Element) dicomweb.Dataset {
	out := make(dicomweb.Dataset, len(elems))
	for _, e := range elems {
		if e.Tag == tag.PixelData {
			continue
		}
		key := fmt.Sprintf("%04X%04X", e.Tag.Group, e.Tag.Element)
		out[key] = element(e)
	}
	return out
}

func element(e *dicom.Element) dicomweb.Element {
	vr := e.RawValueRepresentation
	el := dicomweb.Element{VR: vr}
	switch v := e.Value.GetValue().(type) {
	case []string:
		for _, s := range v {
			switch vr {
			case "PN":
				el.Value = append(el.Value, personName(s))
			case "IS":
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
					el.Value = append(el.Value, n)
				}
			case "DS":
				if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
					el.Value = append(el.Value, f)
				}
			default:
				el.Value = append(el.Value, s)
			}
		}
	case []int:
		for _, n := range v {
			el.Value = append(el.Value, n)
		}
	case []float64:
		for _, f := range v {
			el.Value = append(el.Value, f)
		}
	case []byte:
		el.Value = []any{v}
	case []*dicom.SequenceItemValue:
		for _, it := range v {
			if inner, ok := it.GetValue().([]*dicom.Element); ok {
				el.Value = append(el.Value, fromElements(inner))
			}
		}
		el.VR = "SQ"
	}
	return el
}

// personName tách giá trị PN theo "=" thành các nhóm của DICOM JSON (Alphabetic, Ideographic, Phonetic); chỉ Alphabetic được dùng ở nơi khác.
func personName(s string) dicomweb.PersonName {
	pn := dicomweb.PersonName{}
	parts := strings.SplitN(s, "=", 3)
	for i, k := range []string{"Alphabetic", "Ideographic", "Phonetic"} {
		if i < len(parts) && parts[i] != "" {
			pn[k] = parts[i]
		}
	}
	return pn
}
