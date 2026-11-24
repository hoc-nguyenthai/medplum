package dicomweb

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Bộ quét luồng DICOM tối thiểu, không phụ thuộc thư viện DICOM (bản sản xuất của scan.go ở spike D0, docs/plans/cds-dicom/d0-spike.md). Việc của nó:
//
//  1. Chốt chặn trước khi đưa dữ liệu vào thư viện đọc metadata: giới hạn kích thước phần đầu, giới hạn độ sâu SQ (kể cả SQ nằm trong SQ có độ dài xác định,
//     vì thư viện tốn bộ nhớ bậc hai với SQ không rõ độ dài lồng sâu: tệp 60 KB, 2,2 GB), và từ chối transfer syntax không hỗ trợ.
//  2. Cho biết PixelData nằm ở đâu và (khi quét đầy đủ) khoảng byte của từng frame, từ một luồng chỉ đọc một lượt (kho đối tượng không có đọc theo
//     khoảng), để STOW kiểm tệp không bị cắt và worker D5 tách frame.
//
// Chỉ đọc các giá trị nhỏ cần thiết (transfer syntax, kích thước ảnh); mọi giá trị khác được bỏ qua, nên bộ nhớ là hằng số.

// Các lỗi của bộ quét; có thể dùng errors.Is.
var (
	ErrNotDICOM          = errors.New("dicomweb: không phải tệp DICOM (thiếu tiền tố DICM)")
	ErrHeaderTooLarge    = errors.New("dicomweb: phần đầu DICOM vượt giới hạn")
	ErrTooDeep           = errors.New("dicomweb: SQ lồng quá sâu")
	ErrUnsupportedSyntax = errors.New("dicomweb: transfer syntax không hỗ trợ")
	ErrMalformed         = errors.New("dicomweb: cấu trúc DICOM không hợp lệ")
)

// Transfer syntax không hỗ trợ: Explicit VR Big Endian (đã lỗi thời) và Deflated (cả dataset bị nén, không quét được cấu trúc).
const (
	tsExplicitBE = "1.2.840.10008.1.2.2"
	tsImplicitLE = "1.2.840.10008.1.2"
	tsDeflated   = "1.2.840.10008.1.2.1.99"
)

// MaxSQDepth là số lớp SQ lồng nhau tối đa được chấp nhận (tệp thật không quá 5 đến 6).
const MaxSQDepth = 16

const undefinedLength = 0xFFFFFFFF

// ScanOptions điều khiển Scan.
type ScanOptions struct {
	// HeaderOnly dừng ngay khi gặp PixelData (không đọc giá trị của nó và không tính frame): dùng cho STOW.
	HeaderOnly bool
	// MaxHeaderBytes giới hạn số byte từ đầu tệp tới PixelData (0: không giới hạn). Vượt thì ErrHeaderTooLarge.
	MaxHeaderBytes int64
}

// ScanResult là kết quả của Scan.
type ScanResult struct {
	TransferSyntax string
	Encapsulated   bool // PixelData đóng gói (nén): độ dài không xác định
	HasPixelData   bool
	// PixelHeaderOffset là vị trí byte đầu của phần tử PixelData (tag), PixelOffset là vị trí đầu giá trị (sau tiêu đề phần tử). Với phần tử đóng gói, giá trị
	// bắt đầu bằng Basic Offset Table. PixelLength là độ dài giá trị, -1 nếu không xác định.
	PixelHeaderOffset, PixelOffset, PixelLength int64
	// End là vị trí đã quét tới (tổng số byte đã dùng); ở chế độ HeaderOnly là PixelOffset hoặc điểm kết thúc của tệp không có PixelData.
	End int64
	// Frames chỉ có khi quét đầy đủ: khoảng byte của từng frame trong luồng. Frame đóng gói gồm nhiều fragment trả khoảng bao (cần nối nội dung khi ghi).
	Frames                                    []FrameRange
	Rows, Cols, Bits, Samples, NumberOfFrames int
}

// FrameRange là một khoảng byte trong luồng. Với frame đóng gói gồm nhiều fragment, Parts liệt kê từng fragment (không gồm tiêu đề item xen giữa) theo thứ tự;
// nội dung frame là phần nối của chúng, còn Offset và Length là khoảng bao ngoài (gồm cả tiêu đề item). Parts rỗng nghĩa là frame liền một khối.
type FrameRange struct {
	Offset, Length int64
	Parts          []FrameRange
}

// Size là số byte nội dung của frame (tổng các Parts, hoặc Length nếu liền một khối).
func (f FrameRange) Size() int64 {
	if len(f.Parts) == 0 {
		return f.Length
	}
	var n int64
	for _, p := range f.Parts {
		n += p.Length
	}
	return n
}

type scanner struct {
	r    *bufio.Reader
	pos  int64
	opt  ScanOptions
	res  *ScanResult
	want bool // đang ở cấp cao nhất và còn cần giá trị nhỏ
}

func (s *scanner) advance(n int64) error {
	if s.opt.MaxHeaderBytes > 0 && s.pos+n > s.opt.MaxHeaderBytes {
		return ErrHeaderTooLarge
	}
	return nil
}

func (s *scanner) read(n int) ([]byte, error) {
	if err := s.advance(int64(n)); err != nil {
		return nil, err
	}
	b := make([]byte, n)
	m, err := io.ReadFull(s.r, b)
	s.pos += int64(m)
	if err == io.EOF && m == 0 {
		return nil, io.EOF
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, io.ErrUnexpectedEOF)
	}
	return b, nil
}

func (s *scanner) skip(n int64) error {
	if err := s.advance(n); err != nil {
		return err
	}
	for n > 0 {
		step := n
		if step > 1<<30 {
			step = 1 << 30
		}
		d, err := s.r.Discard(int(step))
		s.pos += int64(d)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformed, io.ErrUnexpectedEOF)
		}
		n -= step
	}
	return nil
}

func isLongVR(vr string) bool {
	switch vr {
	case "OB", "OD", "OF", "OL", "OV", "OW", "SQ", "UC", "UR", "UT", "UN":
		return true
	}
	return false
}

// Scan đọc một luồng DICOM Part 10 (có preamble và tiền tố DICM) một lượt.
func Scan(rd io.Reader, opt ScanOptions) (*ScanResult, error) {
	s := &scanner{r: bufio.NewReaderSize(rd, 1<<16), opt: opt, res: &ScanResult{PixelLength: -1}}
	if err := s.skip(128); err != nil {
		return nil, ErrNotDICOM
	}
	if b, err := s.read(4); err != nil || string(b) != "DICM" {
		return nil, ErrNotDICOM
	}
	explicit, inMeta := true, true
	for {
		tagStart := s.pos
		hdr, err := s.read(4)
		if err == io.EOF { // hết tệp đúng ranh giới phần tử: không có PixelData
			s.res.End = s.pos
			return s.res, s.finish(inMeta)
		}
		if err != nil {
			return nil, err
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		if inMeta && g != 2 {
			inMeta = false
			switch s.res.TransferSyntax {
			case tsExplicitBE, tsDeflated:
				return nil, fmt.Errorf("%w: %s", ErrUnsupportedSyntax, s.res.TransferSyntax)
			case tsImplicitLE:
				explicit = false
			}
			s.res.Encapsulated = strings.HasPrefix(s.res.TransferSyntax, "1.2.840.10008.1.2.4.") || strings.HasPrefix(s.res.TransferSyntax, "1.2.840.10008.1.2.5")
		}
		vr, length, err := s.header(explicit || g == 2, g)
		if err != nil {
			return nil, err
		}
		if g == 0x7FE0 && e == 0x0010 {
			return s.pixelData(tagStart, length)
		}
		if err := s.value(g, e, vr, length, explicit, 0, true); err != nil {
			return nil, err
		}
	}
}

func (s *scanner) finish(inMeta bool) error {
	if inMeta && s.res.TransferSyntax == "" {
		return fmt.Errorf("%w: thiếu transfer syntax", ErrMalformed)
	}
	return nil
}

// header đọc VR (nếu tường minh) và độ dài của một phần tử, sau 4 byte tag.
func (s *scanner) header(explicit bool, g uint16) (vr string, length uint32, err error) {
	if g == 0xFFFE {
		explicit = false
	}
	if !explicit {
		l, err := s.read(4)
		if err != nil {
			return "", 0, eofToMalformed(err)
		}
		return "", binary.LittleEndian.Uint32(l), nil
	}
	b, err := s.read(4)
	if err != nil {
		return "", 0, eofToMalformed(err)
	}
	vr = string(b[0:2])
	if isLongVR(vr) {
		l, err := s.read(4)
		if err != nil {
			return "", 0, eofToMalformed(err)
		}
		return vr, binary.LittleEndian.Uint32(l), nil
	}
	return vr, uint32(binary.LittleEndian.Uint16(b[2:])), nil
}

func eofToMalformed(err error) error {
	if err == io.EOF {
		return fmt.Errorf("%w: %v", ErrMalformed, io.ErrUnexpectedEOF)
	}
	return err
}

// value xử lý giá trị của một phần tử: đọc nếu là giá trị nhỏ cần thiết ở cấp cao nhất, đi vào nếu là SQ (kiểm độ sâu), còn lại bỏ qua.
func (s *scanner) value(g, e uint16, vr string, length uint32, explicit bool, depth int, top bool) error {
	isSeq := vr == "SQ" || length == undefinedLength
	if !isSeq && !explicit && length >= 8 { // implicit VR không cho biết VR: nhìn 4 byte đầu xem có phải item không
		if p, err := s.r.Peek(4); err == nil && binary.LittleEndian.Uint16(p[0:]) == 0xFFFE && binary.LittleEndian.Uint16(p[2:]) == 0xE000 {
			isSeq = true
		}
	}
	if isSeq {
		if depth+1 > MaxSQDepth {
			return ErrTooDeep
		}
		if !top && g == 0x7FE0 && e == 0x0010 && length == undefinedLength { // PixelData đóng gói nằm trong item (icon): bỏ qua các fragment
			return s.skipFragments()
		}
		return s.sequence(length, explicit, depth+1)
	}
	if top && length <= 64 && wanted(g, e) {
		v, err := s.read(int(length))
		if err != nil {
			return eofToMalformed(err)
		}
		s.capture(g, e, v)
		return nil
	}
	return s.skip(int64(length))
}

func wanted(g, e uint16) bool {
	return (g == 2 && e == 0x10) || (g == 0x28 && (e == 2 || e == 8 || e == 0x10 || e == 0x11 || e == 0x100))
}

func (s *scanner) capture(g, e uint16, v []byte) {
	str := strings.TrimRight(string(v), "\x00 ")
	switch {
	case g == 2 && e == 0x10:
		s.res.TransferSyntax = str
	case g == 0x28 && e == 2 && len(v) >= 2:
		s.res.Samples = int(binary.LittleEndian.Uint16(v))
	case g == 0x28 && e == 8:
		s.res.NumberOfFrames, _ = strconv.Atoi(strings.TrimSpace(str))
	case g == 0x28 && e == 0x10 && len(v) >= 2:
		s.res.Rows = int(binary.LittleEndian.Uint16(v))
	case g == 0x28 && e == 0x11 && len(v) >= 2:
		s.res.Cols = int(binary.LittleEndian.Uint16(v))
	case g == 0x28 && e == 0x100 && len(v) >= 2:
		s.res.Bits = int(binary.LittleEndian.Uint16(v))
	}
}

// sequence đi qua các item của một SQ (độ dài xác định hoặc không), kiểm độ sâu ở mọi tầng.
func (s *scanner) sequence(length uint32, explicit bool, depth int) error {
	end := int64(-1)
	if length != undefinedLength {
		end = s.pos + int64(length)
	}
	for {
		if end >= 0 && s.pos >= end {
			return nil
		}
		hdr, err := s.read(8)
		if err != nil {
			return eofToMalformed(err)
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		l := binary.LittleEndian.Uint32(hdr[4:])
		switch {
		case g == 0xFFFE && e == 0xE0DD:
			return nil
		case g == 0xFFFE && e == 0xE000:
			itemEnd := int64(-1)
			if l != undefinedLength {
				itemEnd = s.pos + int64(l)
			}
			if err := s.item(itemEnd, explicit, depth); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: phần tử (%04X,%04X) trong SQ", ErrMalformed, g, e)
		}
	}
}

// item đi qua các phần tử của một item tới itemEnd (xác định) hoặc tới Item Delimitation (itemEnd < 0).
func (s *scanner) item(itemEnd int64, explicit bool, depth int) error {
	for {
		if itemEnd >= 0 && s.pos >= itemEnd {
			return nil
		}
		hdr, err := s.read(4)
		if err != nil {
			return eofToMalformed(err)
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		if g == 0xFFFE && e == 0xE00D {
			_, err := s.read(4)
			return eofToMalformed(err)
		}
		vr, length, err := s.header(explicit, g)
		if err != nil {
			return err
		}
		if err := s.value(g, e, vr, length, explicit, depth, false); err != nil {
			return err
		}
	}
}

// skipFragments bỏ qua các fragment của một PixelData đóng gói lồng trong item.
func (s *scanner) skipFragments() error {
	for {
		hdr, err := s.read(8)
		if err != nil {
			return eofToMalformed(err)
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		switch {
		case g == 0xFFFE && e == 0xE0DD:
			return nil
		case g == 0xFFFE && e == 0xE000:
			if err := s.skip(int64(binary.LittleEndian.Uint32(hdr[4:]))); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: fragment không hợp lệ", ErrMalformed)
		}
	}
}

// pixelData chốt kết quả khi gặp PixelData cấp cao nhất.
func (s *scanner) pixelData(tagStart int64, length uint32) (*ScanResult, error) {
	r := s.res
	r.HasPixelData, r.PixelHeaderOffset, r.PixelOffset = true, tagStart, s.pos
	if length != undefinedLength {
		r.PixelLength = int64(length)
	}
	r.Encapsulated = length == undefinedLength
	r.End = s.pos
	if s.opt.HeaderOnly {
		return r, nil
	}
	if r.Encapsulated {
		return r, s.encapsulated()
	}
	n := r.NumberOfFrames
	if n == 0 {
		n = 1
	}
	spp := r.Samples
	if spp == 0 {
		spp = 1
	}
	fs := int64(r.Rows) * int64(r.Cols) * int64(spp) * int64((r.Bits+7)/8)
	if fs <= 0 || fs*int64(n) > int64(length) {
		return r, fmt.Errorf("%w: kích thước frame %d x %d khác độ dài PixelData %d", ErrMalformed, fs, n, length)
	}
	for i := 0; i < n; i++ {
		r.Frames = append(r.Frames, FrameRange{Offset: r.PixelOffset + int64(i)*fs, Length: fs})
	}
	r.End = r.PixelOffset + int64(length)
	return r, nil
}

// encapsulated đọc Basic Offset Table và các fragment, tách thành frame: theo BOT nếu có, không thì mỗi fragment một frame (hoặc một frame khi
// NumberOfFrames là 1). Nhiều fragment cho nhiều frame mà không có BOT thì không tách được.
func (s *scanner) encapsulated() error {
	r := s.res
	hdr, err := s.read(8)
	if err != nil {
		return eofToMalformed(err)
	}
	if binary.LittleEndian.Uint16(hdr[0:]) != 0xFFFE || binary.LittleEndian.Uint16(hdr[2:]) != 0xE000 {
		return fmt.Errorf("%w: PixelData đóng gói thiếu Basic Offset Table", ErrMalformed)
	}
	var bot []uint32
	if n := int64(binary.LittleEndian.Uint32(hdr[4:])); n > 0 {
		if n > 1<<20 {
			return fmt.Errorf("%w: Basic Offset Table quá lớn", ErrMalformed)
		}
		b, err := s.read(int(n))
		if err != nil {
			return eofToMalformed(err)
		}
		for i := 0; i+4 <= len(b); i += 4 {
			bot = append(bot, binary.LittleEndian.Uint32(b[i:]))
		}
	}
	base := s.pos // offset trong BOT tính từ byte đầu của item đầu tiên sau BOT
	var frags []FrameRange
	var starts []int64
	for {
		h, err := s.read(8)
		if err != nil {
			return eofToMalformed(err)
		}
		g, e := binary.LittleEndian.Uint16(h[0:]), binary.LittleEndian.Uint16(h[2:])
		l := int64(binary.LittleEndian.Uint32(h[4:]))
		if g == 0xFFFE && e == 0xE0DD {
			break
		}
		if g != 0xFFFE || e != 0xE000 {
			return fmt.Errorf("%w: fragment không hợp lệ", ErrMalformed)
		}
		starts = append(starts, s.pos-8-base)
		frags = append(frags, FrameRange{Offset: s.pos, Length: l})
		if err := s.skip(l); err != nil {
			return err
		}
	}
	r.End = s.pos
	n := r.NumberOfFrames
	if n == 0 {
		n = 1
	}
	span := func(f []FrameRange) FrameRange {
		if len(f) == 1 {
			return f[0]
		}
		last := f[len(f)-1]
		return FrameRange{Offset: f[0].Offset, Length: last.Offset + last.Length - f[0].Offset, Parts: append([]FrameRange(nil), f...)}
	}
	switch {
	case len(frags) == 0:
		return fmt.Errorf("%w: PixelData đóng gói không có fragment", ErrMalformed)
	case len(bot) == n && n > 1:
		idx := func(off uint32) int {
			for j, st := range starts {
				if st == int64(off) {
					return j
				}
			}
			return -1
		}
		for i := 0; i < n; i++ {
			first, last := idx(bot[i]), len(frags)-1
			if first < 0 {
				return fmt.Errorf("%w: Basic Offset Table trỏ vào chỗ không phải đầu fragment", ErrMalformed)
			}
			if i+1 < n {
				next := idx(bot[i+1])
				if next <= first {
					return fmt.Errorf("%w: Basic Offset Table không tăng dần", ErrMalformed)
				}
				last = next - 1
			}
			r.Frames = append(r.Frames, span(frags[first:last+1]))
		}
	case len(frags) == n:
		r.Frames = append(r.Frames, frags...)
	case n == 1:
		r.Frames = append(r.Frames, span(frags))
	default:
		return fmt.Errorf("%w: %d fragment cho %d frame và không có Basic Offset Table", ErrMalformed, len(frags), n)
	}
	return nil
}
