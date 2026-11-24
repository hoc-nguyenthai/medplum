// Package dicomtest dựng tệp DICOM tổng hợp (không dữ liệu bệnh nhân thật) cho test của dicomweb: không nén, nhiều frame, implicit VR, SQ không rõ độ dài,
// pixel data đóng gói, và các tệp độc (SQ lồng sâu, độ dài khai báo giả). Chỉ dùng từ test; không phụ thuộc thư viện DICOM nên cũng dùng được để kiểm tra
// thư viện. Bản Python tương đương dùng để đo ở spike D0 nằm ở tools/dicom-spike/gen.py.
package dicomtest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// Các transfer syntax thường dùng.
const (
	ImplicitLE   = "1.2.840.10008.1.2"
	ExplicitLE   = "1.2.840.10008.1.2.1"
	ExplicitBE   = "1.2.840.10008.1.2.2"
	Deflated     = "1.2.840.10008.1.2.1.99"
	JPEGBaseline = "1.2.840.10008.1.2.4.50"
	CTImage      = "1.2.840.10008.5.1.4.1.1.2"
)

var longVR = map[string]bool{"OB": true, "OD": true, "OF": true, "OL": true, "OV": true, "OW": true, "SQ": true, "UC": true, "UR": true, "UT": true, "UN": true}

// Undefined là độ dài "không xác định" (0xFFFFFFFF).
const Undefined = 0xFFFFFFFF

// Elem là một phần tử dữ liệu đã mã hóa.
type Elem []byte

func pad(b []byte, vr string) []byte {
	if len(b)%2 == 1 {
		if vr == "UI" || vr == "OB" || vr == "UN" {
			return append(b, 0)
		}
		return append(b, ' ')
	}
	return b
}

// E mã hóa một phần tử; explicit chọn VR tường minh hay ngầm. Giá trị tự đệm cho chẵn.
func E(explicit bool, group, element uint16, vr string, value []byte) Elem {
	if vr != "SQ" {
		value = pad(append([]byte(nil), value...), vr)
	}
	return Elem(encode(explicit, group, element, vr, uint32(len(value)), value))
}

func encode(explicit bool, g, e uint16, vr string, length uint32, value []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, g)
	_ = binary.Write(&b, binary.LittleEndian, e)
	switch {
	case !explicit:
		_ = binary.Write(&b, binary.LittleEndian, length)
	case longVR[vr]:
		b.WriteString(vr)
		b.Write([]byte{0, 0})
		_ = binary.Write(&b, binary.LittleEndian, length)
	default:
		b.WriteString(vr)
		_ = binary.Write(&b, binary.LittleEndian, uint16(length))
	}
	b.Write(value)
	return b.Bytes()
}

// US mã hóa số nguyên 16 bit.
func US(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }

// Item gói các phần tử thành một item của SQ; undefined=true dùng độ dài không xác định kèm Item Delimitation.
func Item(undefined bool, elems ...Elem) []byte {
	var body []byte
	for _, e := range elems {
		body = append(body, e...)
	}
	if undefined {
		out := binary.LittleEndian.AppendUint16(nil, 0xFFFE)
		out = binary.LittleEndian.AppendUint16(out, 0xE000)
		out = binary.LittleEndian.AppendUint32(out, Undefined)
		out = append(out, body...)
		out = binary.LittleEndian.AppendUint16(out, 0xFFFE)
		out = binary.LittleEndian.AppendUint16(out, 0xE00D)
		return binary.LittleEndian.AppendUint32(out, 0)
	}
	out := binary.LittleEndian.AppendUint16(nil, 0xFFFE)
	out = binary.LittleEndian.AppendUint16(out, 0xE000)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

func delim(e uint16) []byte {
	out := binary.LittleEndian.AppendUint16(nil, 0xFFFE)
	out = binary.LittleEndian.AppendUint16(out, e)
	return binary.LittleEndian.AppendUint32(out, 0)
}

// Sequence mã hóa một SQ từ các item đã dựng bằng Item. undefined=true dùng độ dài không xác định kèm Sequence Delimitation.
func Sequence(explicit bool, group, element uint16, undefined bool, items ...[]byte) Elem {
	var body []byte
	for _, it := range items {
		body = append(body, it...)
	}
	if undefined {
		return Elem(encode(explicit, group, element, "SQ", Undefined, append(body, delim(0xE0DD)...)))
	}
	return Elem(encode(explicit, group, element, "SQ", uint32(len(body)), body))
}

// Meta là tham số của Build.
type Meta struct {
	TransferSyntax, SOPClassUID string
	StudyUID, SeriesUID, SOPUID string
	Modality                    string // mặc định CT
	PatientName, PatientID      string // mặc định "Tran^Van A", "PID-0001"
	ReferringPhysician          string // PN, bỏ qua nếu rỗng
	StudyDate, StudyTime        string // DA, TM; bỏ qua nếu rỗng
	InstanceNumber              string // IS; bỏ qua nếu rỗng
	Rows, Cols, Bits, Frames    int    // mặc định 8, 8, 16, 1
	Extra                       []Elem // chèn trước nhóm 0028 (tag phải nhỏ hơn 0028 để giữ thứ tự)
	NoPixelData                 bool   // bỏ PixelData (ví dụ SR)
	Pixel                       []byte // PixelData thô (không nén); nil: sinh theo kích thước
	Fragments                   [][]byte
	TruncatePixel               int // cắt bớt từng đó byte cuối tệp (tệp bị cắt giữa PixelData)
}

func (m Meta) withDefaults() Meta {
	if m.TransferSyntax == "" {
		m.TransferSyntax = ExplicitLE
	}
	if m.SOPClassUID == "" {
		m.SOPClassUID = CTImage
	}
	if m.Modality == "" {
		m.Modality = "CT"
	}
	if m.PatientName == "" {
		m.PatientName = "Tran^Van A"
	}
	if m.PatientID == "" {
		m.PatientID = "PID-0001"
	}
	if m.Rows == 0 {
		m.Rows = 8
	}
	if m.Cols == 0 {
		m.Cols = 8
	}
	if m.Bits == 0 {
		m.Bits = 16
	}
	if m.Frames == 0 {
		m.Frames = 1
	}
	return m
}

// Header dựng phần trước PixelData: preamble, file meta (luôn explicit LE) và dataset. Trả (header, explicit).
func Header(m Meta) ([]byte, bool) {
	m = m.withDefaults()
	explicit := m.TransferSyntax != ImplicitLE
	meta := bytes.Join([][]byte{
		E(true, 2, 1, "OB", []byte{0, 1}), E(true, 2, 2, "UI", []byte(m.SOPClassUID)), E(true, 2, 3, "UI", []byte(m.SOPUID)),
		E(true, 2, 0x10, "UI", []byte(m.TransferSyntax)), E(true, 2, 0x12, "UI", []byte("1.2.826.0.1.3680043.8.498.1")),
	}, nil)
	var out bytes.Buffer
	out.Write(make([]byte, 128))
	out.WriteString("DICM")
	out.Write(E(true, 2, 0, "UL", binary.LittleEndian.AppendUint32(nil, uint32(len(meta)))))
	out.Write(meta)
	s := func(g, e uint16, vr, v string) { out.Write(E(explicit, g, e, vr, []byte(v))) }
	s(8, 0x16, "UI", m.SOPClassUID)
	s(8, 0x18, "UI", m.SOPUID)
	if m.StudyDate != "" {
		s(8, 0x20, "DA", m.StudyDate)
	}
	if m.StudyTime != "" {
		s(8, 0x30, "TM", m.StudyTime)
	}
	s(8, 0x50, "SH", "ACC123")
	s(8, 0x60, "CS", m.Modality)
	if m.ReferringPhysician != "" {
		s(8, 0x90, "PN", m.ReferringPhysician)
	}
	s(0x10, 0x10, "PN", m.PatientName)
	s(0x10, 0x20, "LO", m.PatientID)
	s(0x10, 0x30, "DA", "19800101")
	s(0x10, 0x40, "CS", "M")
	s(0x20, 0x0D, "UI", m.StudyUID)
	s(0x20, 0x0E, "UI", m.SeriesUID)
	s(0x20, 0x10, "SH", "STUDY1")
	s(0x20, 0x11, "IS", "1")
	if m.InstanceNumber != "" {
		s(0x20, 0x13, "IS", m.InstanceNumber)
	}
	for _, e := range m.Extra {
		out.Write(e)
	}
	if !m.NoPixelData {
		out.Write(E(explicit, 0x28, 2, "US", US(1)))
		out.Write(E(explicit, 0x28, 4, "CS", []byte("MONOCHROME2")))
		if m.Frames > 1 {
			s(0x28, 8, "IS", fmt.Sprint(m.Frames))
		}
		out.Write(E(explicit, 0x28, 0x10, "US", US(uint16(m.Rows))))
		out.Write(E(explicit, 0x28, 0x11, "US", US(uint16(m.Cols))))
		out.Write(E(explicit, 0x28, 0x100, "US", US(uint16(m.Bits))))
		out.Write(E(explicit, 0x28, 0x101, "US", US(uint16(m.Bits))))
		out.Write(E(explicit, 0x28, 0x102, "US", US(uint16(m.Bits-1))))
		out.Write(E(explicit, 0x28, 0x103, "US", US(0)))
	}
	return out.Bytes(), explicit
}

// Build dựng một tệp DICOM hoàn chỉnh.
func Build(m Meta) []byte {
	m = m.withDefaults()
	hdr, explicit := Header(m)
	if m.NoPixelData {
		return hdr
	}
	var out bytes.Buffer
	out.Write(hdr)
	switch {
	case len(m.Fragments) > 0:
		out.Write(encode(explicit, 0x7FE0, 0x10, "OB", Undefined, nil))
		out.Write(delimItem(0xE000, 0)) // Basic Offset Table rỗng
		for _, f := range m.Fragments {
			f = pad(append([]byte(nil), f...), "OB")
			out.Write(delimItem(0xE000, uint32(len(f))))
			out.Write(f)
		}
		out.Write(delim(0xE0DD))
	default:
		px := m.Pixel
		if px == nil {
			px = make([]byte, m.Rows*m.Cols*m.Frames*((m.Bits+7)/8))
			for i := range px {
				px[i] = byte(i*7 + i>>8)
			}
		}
		vr := "OW"
		if m.Bits <= 8 {
			vr = "OB"
		}
		out.Write(encode(explicit, 0x7FE0, 0x10, vr, uint32(len(px)), px))
	}
	b := out.Bytes()
	if m.TruncatePixel > 0 && m.TruncatePixel < len(b) {
		b = b[:len(b)-m.TruncatePixel]
	}
	return b
}

func delimItem(e uint16, length uint32) []byte {
	out := binary.LittleEndian.AppendUint16(nil, 0xFFFE)
	out = binary.LittleEndian.AppendUint16(out, e)
	return binary.LittleEndian.AppendUint32(out, length)
}

// DeepUndefinedSQ trả phần tử chèn vào Extra: n lớp SQ không rõ độ dài lồng nhau (bom bộ nhớ của thư viện DICOM: tệp 60 KB ở n=2000 làm
// suyashkumar/dicom dùng 2,2 GB, xem docs/plans/cds-dicom/d0-spike.md 3.1). Dữ liệu cố ý không đóng các SQ.
func DeepUndefinedSQ(n int) Elem {
	var b []byte
	for i := 0; i < n; i++ {
		b = append(b, encode(true, 8, 0x1110, "SQ", Undefined, nil)...)
		b = append(b, delimItem(0xE000, Undefined)...)
	}
	return Elem(b)
}

// DeepDefinedSQ là n lớp SQ có độ dài xác định lồng nhau, khép kín đúng chuẩn.
func DeepDefinedSQ(explicit bool, n int) Elem {
	cur := []byte(E(explicit, 0x10, 0x20, "LO", []byte("abcd")))
	for i := 0; i < n; i++ {
		item := Item(false, Elem(cur))
		cur = Sequence(explicit, 8, 0x1110, false, item)
	}
	return Elem(cur)
}

// HugeLengthPrivate là phần tử private OB khai báo độ dài lớn nhưng chỉ kèm vài byte (tệp bị cắt hoặc độc).
func HugeLengthPrivate(declared uint32) Elem {
	return Elem(encode(true, 9, 0x1001, "OB", declared, bytes.Repeat([]byte{'x'}, 100)))
}

// UID dựng UID thử nghiệm ổn định theo chuỗi.
func UID(parts ...string) string { return "1.2.826.0.1.3680043.8.498." + strings.Join(parts, ".") }

// patterned là luồng byte vô hạn có quy luật (không toàn số 0), để dựng tệp rất lớn mà không tốn bộ nhớ.
type patterned struct{ n byte }

func (p *patterned) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = p.n
		p.n += 7
	}
	return len(b), nil
}

// Stream dựng một tệp không nén có PixelData rất lớn dưới dạng luồng (phần đầu rồi pixel theo quy luật), không nạp cả tệp vào bộ nhớ. Kích thước pixel là
// Rows*Cols*Frames*byte mỗi mẫu của m; trả luồng và tổng số byte.
func Stream(m Meta) (io.Reader, int64) {
	m = m.withDefaults()
	hdr, explicit := Header(m)
	n := int64(m.Rows) * int64(m.Cols) * int64(m.Frames) * int64((m.Bits+7)/8)
	vr := "OW"
	if m.Bits <= 8 {
		vr = "OB"
	}
	ph := encode(explicit, 0x7FE0, 0x10, vr, uint32(n), nil)
	return io.MultiReader(bytes.NewReader(hdr), bytes.NewReader(ph), io.LimitReader(&patterned{}, n)), int64(len(hdr)+len(ph)) + n
}
