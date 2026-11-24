package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Bộ quét luồng tối thiểu (không phụ thuộc thư viện DICOM): tìm PixelData, tách frame bằng khoảng byte. Dùng cho worker D5:
// đọc một lượt, hằng số bộ nhớ, không cần Seek (kho đối tượng chỉ có Open).

type frameRange struct{ Off, Len int64 }

type scanResult struct {
	TransferSyntax string
	Encapsulated   bool
	PixelOffset    int64 // vị trí byte đầu của giá trị PixelData (sau tiêu đề phần tử)
	PixelLen       int64 // -1 nếu độ dài không xác định (đóng gói)
	Frames         []frameRange
	Rows, Cols     int
	Bits, Samples  int
	NumFrames      int
}

type cr struct {
	r   *bufio.Reader
	pos int64
}

func (c *cr) read(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(c.r, b)
	c.pos += int64(n)
	return b, err
}
func (c *cr) skip(n int64) error {
	d, err := c.r.Discard(int(n))
	c.pos += int64(d)
	for err == nil && int64(d) < n { // n > MaxInt chưa gặp, nhưng giữ đúng
		break
	}
	return err
}

var longVR = map[string]bool{"OB": true, "OD": true, "OF": true, "OL": true, "OV": true, "OW": true, "SQ": true, "UC": true, "UR": true, "UT": true, "UN": true}

func scan(rd io.Reader) (*scanResult, error) {
	c := &cr{r: bufio.NewReaderSize(rd, 1<<16)}
	if err := c.skip(128); err != nil {
		return nil, err
	}
	if b, err := c.read(4); err != nil || string(b) != "DICM" {
		return nil, errors.New("không có tiền tố DICM")
	}
	res := &scanResult{PixelLen: -1}
	explicit, implicitDefault := true, false
	_ = implicitDefault
	inMeta := true
	// đọc từng phần tử cấp cao nhất
	for {
		hdr, err := c.read(4)
		if err != nil {
			return nil, fmt.Errorf("đọc tag: %w", err)
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		if inMeta && g != 2 { // hết nhóm meta: chốt transfer syntax
			inMeta = false
			switch res.TransferSyntax {
			case "1.2.840.10008.1.2":
				explicit = false
			case "1.2.840.10008.1.2.2":
				return nil, errors.New("Explicit VR Big Endian chưa hỗ trợ")
			}
			res.Encapsulated = strings.HasPrefix(res.TransferSyntax, "1.2.840.10008.1.2.4") || strings.HasPrefix(res.TransferSyntax, "1.2.840.10008.1.2.5")
		}
		var vr string
		var length uint32
		explicitHere := explicit || g == 2
		if g == 0xFFFE {
			explicitHere = false
		}
		if explicitHere {
			b, err := c.read(4)
			if err != nil {
				return nil, err
			}
			vr = string(b[0:2])
			if longVR[vr] {
				l, err := c.read(4)
				if err != nil {
					return nil, err
				}
				length = binary.LittleEndian.Uint32(l)
			} else {
				length = uint32(binary.LittleEndian.Uint16(b[2:]))
			}
		} else {
			l, err := c.read(4)
			if err != nil {
				return nil, err
			}
			length = binary.LittleEndian.Uint32(l)
		}
		if g == 0x7FE0 && e == 0x0010 {
			res.PixelOffset = c.pos
			if length == 0xFFFFFFFF {
				res.PixelLen = -1
				return res, scanEncapsulated(c, res)
			}
			res.PixelLen = int64(length)
			n := res.NumFrames
			if n == 0 {
				n = 1
			}
			fs := int64(res.Rows) * int64(res.Cols) * int64(res.Samples) * int64((res.Bits+7)/8)
			if fs <= 0 || fs*int64(n) > int64(length) {
				return res, fmt.Errorf("kích thước frame %d x %d khác độ dài PixelData %d", fs, n, length)
			}
			for i := 0; i < n; i++ {
				res.Frames = append(res.Frames, frameRange{res.PixelOffset + int64(i)*fs, fs})
			}
			return res, nil
		}
		if length == 0xFFFFFFFF { // SQ hoặc phần tử không rõ độ dài khác: bỏ qua theo mức lồng
			if err := skipUndefined(c, explicit, 1); err != nil {
				return nil, err
			}
			continue
		}
		if length > 1<<20 && !(g == 0x7FE0) { // phần tử lớn bất thường trước PixelData: bỏ qua không đọc
			if err := c.skip(int64(length)); err != nil {
				return nil, err
			}
			continue
		}
		val, err := c.read(int(length))
		if err != nil {
			return nil, err
		}
		s := strings.TrimRight(string(val), "\x00 ")
		switch {
		case g == 2 && e == 0x10:
			res.TransferSyntax = s
		case g == 0x28 && e == 0x2:
			res.Samples = int(binary.LittleEndian.Uint16(val))
		case g == 0x28 && e == 0x8:
			res.NumFrames, _ = strconv.Atoi(strings.TrimSpace(s))
		case g == 0x28 && e == 0x10:
			res.Rows = int(binary.LittleEndian.Uint16(val))
		case g == 0x28 && e == 0x11:
			res.Cols = int(binary.LittleEndian.Uint16(val))
		case g == 0x28 && e == 0x100:
			res.Bits = int(binary.LittleEndian.Uint16(val))
		}
	}
}

// skipUndefined bỏ qua giá trị độ dài không xác định tới Sequence Delimitation; item có thể rõ hoặc không rõ độ dài và lồng nhau.
const maxDepth = 32

func skipUndefined(c *cr, explicit bool, depth int) error {
	if depth > maxDepth {
		return errors.New("SQ lồng quá sâu")
	}
	for {
		hdr, err := c.read(8)
		if err != nil {
			return err
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		l := binary.LittleEndian.Uint32(hdr[4:])
		switch {
		case g == 0xFFFE && e == 0xE0DD:
			return nil
		case g == 0xFFFE && e == 0xE000:
			if l != 0xFFFFFFFF {
				if err := c.skip(int64(l)); err != nil {
					return err
				}
				continue
			}
			if err := skipItem(c, explicit, depth+1); err != nil {
				return err
			}
		default:
			return errors.New("SQ không hợp lệ")
		}
	}
}

// skipItem bỏ qua các phần tử của một item không rõ độ dài tới Item Delimitation.
func skipItem(c *cr, explicit bool, depth int) error {
	for {
		hdr, err := c.read(4)
		if err != nil {
			return err
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		if g == 0xFFFE && e == 0xE00D {
			_, err := c.read(4)
			return err
		}
		var vr string
		var length uint32
		if explicit {
			b, err := c.read(4)
			if err != nil {
				return err
			}
			vr = string(b[0:2])
			if longVR[vr] {
				l, err := c.read(4)
				if err != nil {
					return err
				}
				length = binary.LittleEndian.Uint32(l)
			} else {
				length = uint32(binary.LittleEndian.Uint16(b[2:]))
			}
		} else {
			l, err := c.read(4)
			if err != nil {
				return err
			}
			length = binary.LittleEndian.Uint32(l)
		}
		if length == 0xFFFFFFFF {
			if err := skipUndefined(c, explicit, depth+1); err != nil {
				return err
			}
			continue
		}
		if err := c.skip(int64(length)); err != nil {
			return err
		}
	}
}

func scanEncapsulated(c *cr, res *scanResult) error {
	// Basic Offset Table
	hdr, err := c.read(8)
	if err != nil {
		return err
	}
	if binary.LittleEndian.Uint16(hdr[0:]) != 0xFFFE || binary.LittleEndian.Uint16(hdr[2:]) != 0xE000 {
		return errors.New("PixelData đóng gói thiếu Basic Offset Table")
	}
	botLen := int64(binary.LittleEndian.Uint32(hdr[4:]))
	var bot []uint32
	if botLen > 0 {
		b, err := c.read(int(botLen))
		if err != nil {
			return err
		}
		for i := 0; i+4 <= len(b); i += 4 {
			bot = append(bot, binary.LittleEndian.Uint32(b[i:]))
		}
	}
	base := c.pos // offset trong BOT tính từ byte đầu của item đầu tiên sau BOT
	var frags []frameRange
	var fragStarts []int64
	for {
		hdr, err := c.read(8)
		if err != nil {
			return err
		}
		g, e := binary.LittleEndian.Uint16(hdr[0:]), binary.LittleEndian.Uint16(hdr[2:])
		l := int64(binary.LittleEndian.Uint32(hdr[4:]))
		if g == 0xFFFE && e == 0xE0DD {
			break
		}
		if g != 0xFFFE || e != 0xE000 {
			return errors.New("fragment không hợp lệ")
		}
		fragStarts = append(fragStarts, c.pos-8-base)
		frags = append(frags, frameRange{c.pos, l})
		if err := c.skip(l); err != nil {
			return err
		}
	}
	// Ghép fragment thành frame: có BOT thì theo BOT; không có thì mỗi fragment một frame, hoặc chia đều theo NumberOfFrames.
	n := res.NumFrames
	if n == 0 {
		n = 1
	}
	switch {
	case len(bot) == n && n > 0:
		for i := 0; i < n; i++ {
			var first, last = -1, -1
			for j, s := range fragStarts {
				if s == int64(bot[i]) {
					first = j
				}
			}
			if first < 0 {
				return errors.New("BOT trỏ vào chỗ không phải đầu fragment")
			}
			last = len(frags) - 1
			if i+1 < n {
				for j, s := range fragStarts {
					if s == int64(bot[i+1]) {
						last = j - 1
					}
				}
			}
			res.Frames = append(res.Frames, group(frags[first:last+1]))
		}
	case len(frags) == n:
		for _, f := range frags {
			res.Frames = append(res.Frames, f)
		}
	case n == 1:
		res.Frames = append(res.Frames, group(frags))
	default:
		return fmt.Errorf("%d fragment cho %d frame và không có BOT: không tách được", len(frags), n)
	}
	return nil
}

// group chỉ đúng khi các fragment liền nhau trong luồng (cộng phần đệm); với nhiều fragment thật cần nối nội dung. Ở đây trả khoảng bao.
func group(f []frameRange) frameRange {
	last := f[len(f)-1]
	return frameRange{f[0].Off, last.Off + last.Len - f[0].Off}
}

func runScan(path string) {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	t0 := time.Now()
	res, err := scan(f)
	el := time.Since(t0)
	name := path[strings.LastIndex(path, "/")+1:]
	if res == nil {
		fmt.Printf("scan %-24s LỖI %v (%v)\n", name, err, el.Round(time.Microsecond))
		return
	}
	fmt.Printf("scan %-24s ts=%s encap=%v off=%d len=%d frames=%d %dx%d bits=%d spp=%d first=%v err=%v (%v)\n", name, res.TransferSyntax, res.Encapsulated,
		res.PixelOffset, res.PixelLen, len(res.Frames), res.Rows, res.Cols, res.Bits, res.Samples, firstFrame(res), err, el.Round(time.Microsecond))
}

func firstFrame(r *scanResult) any {
	if len(r.Frames) == 0 {
		return nil
	}
	return r.Frames[0]
}
