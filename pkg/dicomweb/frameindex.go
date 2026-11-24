package dicomweb

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Chỉ mục frame (D5): vị trí từng frame trong nội dung DICOM đã lưu, lập một lần lúc STOW (khi tệp đang được ghi, không đọc lại) và lưu ở extension của DicomInstance. Nhờ đó
// WADO-RS đọc một frame bằng một lần đọc theo khoảng (binarystore.RangeOpener) mà không quét lại tệp và không nhân đôi dung lượng như tách frame ra Binary riêng.

// FrameIndexURL là url của extension chứa chỉ mục frame trên DicomInstance (valueString là JSON do FrameIndex.Encode sinh).
const FrameIndexURL = "https://trivita.ai/fhir/StructureDefinition/dicom-frame-index"

// MaxFrameIndexBytes là cỡ tối đa của chỉ mục đã mã hóa; ảnh có quá nhiều frame thì không lưu chỉ mục (WADO-RS quay về quét tệp).
const MaxFrameIndexBytes = 256 << 10

const maxIndexedFrames = 100000

// FrameIndex là chỉ mục frame của một instance.
type FrameIndex struct {
	TransferSyntax string
	Frames         []FrameRange
}

type indexJSON struct {
	V  int     `json:"v"`
	TS string  `json:"ts"`
	F  [][]any `json:"f"`
}

// Encode mã hóa chỉ mục thành JSON gọn: {"v":1,"ts":"...","f":[[offset,length],[offset,length,[[o,l],...]]]}; phần tử thứ ba chỉ có với frame gồm nhiều fragment. ok=false nếu
// không có frame hoặc kết quả vượt MaxFrameIndexBytes.
func (x FrameIndex) Encode() (s string, ok bool) {
	if len(x.Frames) == 0 || len(x.Frames) > maxIndexedFrames {
		return "", false
	}
	j := indexJSON{V: 1, TS: x.TransferSyntax, F: make([][]any, len(x.Frames))}
	for i, f := range x.Frames {
		e := []any{f.Offset, f.Length}
		if len(f.Parts) > 0 {
			parts := make([][2]int64, len(f.Parts))
			for k, p := range f.Parts {
				parts[k] = [2]int64{p.Offset, p.Length}
			}
			e = append(e, parts)
		}
		j.F[i] = e
	}
	raw, err := json.Marshal(j)
	if err != nil || len(raw) > MaxFrameIndexBytes {
		return "", false
	}
	return string(raw), true
}

// ErrBadFrameIndex là lỗi của chỉ mục hỏng; người gọi nên bỏ chỉ mục và quét tệp.
var ErrBadFrameIndex = errors.New("dicomweb: chỉ mục frame không hợp lệ")

// ParseFrameIndex đọc chuỗi do Encode sinh và kiểm hợp lệ: đúng phiên bản, khoảng không âm và có độ dài dương, fragment tăng dần không chồng nhau.
func ParseFrameIndex(s string) (*FrameIndex, error) {
	var j struct {
		V  int                 `json:"v"`
		TS string              `json:"ts"`
		F  [][]json.RawMessage `json:"f"`
	}
	if err := json.Unmarshal([]byte(s), &j); err != nil || j.V != 1 || len(j.F) == 0 || len(j.F) > maxIndexedFrames {
		return nil, fmt.Errorf("%w", ErrBadFrameIndex)
	}
	x := &FrameIndex{TransferSyntax: j.TS, Frames: make([]FrameRange, len(j.F))}
	for i, e := range j.F {
		if len(e) < 2 || len(e) > 3 {
			return nil, ErrBadFrameIndex
		}
		var off, length int64
		if json.Unmarshal(e[0], &off) != nil || json.Unmarshal(e[1], &length) != nil || off < 0 || length <= 0 {
			return nil, ErrBadFrameIndex
		}
		fr := FrameRange{Offset: off, Length: length}
		if len(e) == 3 {
			var parts [][2]int64
			if json.Unmarshal(e[2], &parts) != nil || len(parts) < 2 {
				return nil, ErrBadFrameIndex
			}
			var prevEnd int64
			for _, p := range parts {
				if p[0] < off || p[1] <= 0 || p[0] < prevEnd || p[0]+p[1] > off+length {
					return nil, ErrBadFrameIndex
				}
				prevEnd = p[0] + p[1]
				fr.Parts = append(fr.Parts, FrameRange{Offset: p[0], Length: p[1]})
			}
		}
		x.Frames[i] = fr
	}
	return x, nil
}
