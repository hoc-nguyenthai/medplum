// Package dicomrender chuyển một frame DICOM thành ảnh PNG hoặc JPEG cho WADO-RS `rendered` (PS3.18 8.3.5), chỉ dùng thư viện chuẩn.
//
// Phạm vi có chủ ý: JPEG baseline được trả nguyên (hoặc giải mã rồi mã hóa lại PNG), dữ liệu không nén xám 8 hoặc 16 bit và RGB 8 bit được đưa về ảnh 8 bit qua window/level.
// Mọi định dạng khác (JPEG 2000, JPEG-LS, RLE, YBR, bảng màu, 12 bit tách rời...) trả ErrUnsupported để tầng trên đáp 406, thay vì trả ảnh sai.
package dicomrender

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	JPEG = "image/jpeg"
	PNG  = "image/png"
	// MaxPixels là trần số điểm ảnh của một frame được dựng (chặn ảnh khổng lồ tốn bộ nhớ).
	MaxPixels = 64 << 20
)

// ErrUnsupported: định dạng điểm ảnh không dựng được.
var ErrUnsupported = errors.New("dicomrender: định dạng điểm ảnh chưa hỗ trợ")

// Params là thuộc tính ảnh lấy từ metadata của instance.
type Params struct {
	Rows, Cols          int
	Samples             int // SamplesPerPixel (mặc định 1)
	BitsAllocated       int
	BitsStored          int
	PixelRepresentation int // 1: có dấu
	PlanarConfiguration int
	Photometric         string
	Slope, Intercept    float64 // RescaleSlope và RescaleIntercept (0 slope coi là 1)
	// Center và Width là WindowCenter và WindowWidth đầu tiên; Width <= 0: không có cửa sổ, dùng min và max của ảnh.
	Center, Width float64
}

// Options là lựa chọn của client.
type Options struct {
	Format  string // JPEG hoặc PNG
	Quality int    // JPEG 1..100, 0: 90
	// Window ghi đè cửa sổ của instance khi Width > 0.
	Center, Width float64
}

// Negotiate chọn định dạng ảnh từ header Accept (có q và ký tự đại diện); rỗng hoặc */* là JPEG. ok=false khi không có định dạng nào hỗ trợ được chấp nhận.
func Negotiate(accept string) (format string, ok bool) {
	if strings.TrimSpace(accept) == "" {
		return JPEG, true
	}
	type cand struct {
		typ string
		q   float64
		pos int
	}
	var cs []cand
	for i, part := range strings.Split(accept, ",") {
		fields := strings.Split(part, ";")
		c := cand{typ: strings.ToLower(strings.TrimSpace(fields[0])), q: 1, pos: i}
		for _, f := range fields[1:] {
			if k, v, found := strings.Cut(strings.TrimSpace(f), "="); found && strings.EqualFold(k, "q") {
				if q, err := strconv.ParseFloat(v, 64); err == nil {
					c.q = q
				}
			}
		}
		if c.q > 0 {
			cs = append(cs, c)
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].q > cs[j].q })
	for _, c := range cs {
		switch c.typ {
		case JPEG, PNG:
			return c.typ, true
		case "*/*", "image/*":
			return JPEG, true
		}
	}
	return "", false
}

func encode(img image.Image, o Options) ([]byte, string, error) {
	var buf bytes.Buffer
	if o.Format == PNG {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), PNG, nil
	}
	q := o.Quality
	if q <= 0 || q > 100 {
		q = 90
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), JPEG, nil
}

// FromJPEG xử lý frame đã là JPEG baseline: trả nguyên khi cần JPEG, giải mã rồi mã hóa PNG khi cần PNG. Dữ liệu không giải mã được là lỗi (không phải ErrUnsupported).
func FromJPEG(data []byte, o Options) ([]byte, string, error) {
	if o.Format != PNG {
		if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
			return nil, "", fmt.Errorf("dicomrender: JPEG hỏng: %w", err)
		}
		return data, JPEG, nil
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("dicomrender: JPEG hỏng: %w", err)
	}
	return encode(img, o)
}

// FromNative dựng ảnh từ điểm ảnh không nén (little endian) của một frame.
func FromNative(p Params, pixel []byte, o Options) ([]byte, string, error) {
	if p.Rows <= 0 || p.Cols <= 0 || p.Rows*p.Cols > MaxPixels || p.Rows > MaxPixels/p.Cols {
		return nil, "", ErrUnsupported
	}
	n := p.Rows * p.Cols
	samples := p.Samples
	if samples == 0 {
		samples = 1
	}
	switch {
	case samples == 1 && (p.BitsAllocated == 8 || p.BitsAllocated == 16) && (p.Photometric == "MONOCHROME1" || p.Photometric == "MONOCHROME2"):
		bytesPer := p.BitsAllocated / 8
		if len(pixel) < n*bytesPer {
			return nil, "", fmt.Errorf("dicomrender: thiếu dữ liệu điểm ảnh (%d byte, cần %d)", len(pixel), n*bytesPer)
		}
		vals := make([]float64, n)
		slope := p.Slope
		if slope == 0 {
			slope = 1
		}
		for i := range vals {
			var v float64
			switch {
			case bytesPer == 1 && p.PixelRepresentation == 1:
				v = float64(int8(pixel[i]))
			case bytesPer == 1:
				v = float64(pixel[i])
			default:
				u := uint16(pixel[2*i]) | uint16(pixel[2*i+1])<<8
				if p.PixelRepresentation == 1 {
					v = float64(int16(u))
				} else {
					v = float64(u)
				}
			}
			vals[i] = v*slope + p.Intercept
		}
		lo, hi := window(vals, p, o)
		img := image.NewGray(image.Rect(0, 0, p.Cols, p.Rows))
		for i, v := range vals {
			g := scale(v, lo, hi)
			if p.Photometric == "MONOCHROME1" {
				g = 255 - g
			}
			img.Pix[i] = g
		}
		return encode(img, o)
	case samples == 3 && p.BitsAllocated == 8 && p.Photometric == "RGB":
		if len(pixel) < n*3 {
			return nil, "", fmt.Errorf("dicomrender: thiếu dữ liệu điểm ảnh (%d byte, cần %d)", len(pixel), n*3)
		}
		img := image.NewNRGBA(image.Rect(0, 0, p.Cols, p.Rows))
		for i := 0; i < n; i++ {
			var r, g, b byte
			if p.PlanarConfiguration == 1 {
				r, g, b = pixel[i], pixel[n+i], pixel[2*n+i]
			} else {
				r, g, b = pixel[3*i], pixel[3*i+1], pixel[3*i+2]
			}
			img.SetNRGBA(i%p.Cols, i/p.Cols, color.NRGBA{r, g, b, 255})
		}
		return encode(img, o)
	}
	return nil, "", ErrUnsupported
}

// window trả [lo, hi] của cửa sổ: tùy chọn của client, rồi cửa sổ của instance, rồi min và max của ảnh.
func window(vals []float64, p Params, o Options) (lo, hi float64) {
	switch {
	case o.Width > 0:
		return o.Center - o.Width/2, o.Center + o.Width/2
	case p.Width > 0:
		return p.Center - p.Width/2, p.Center + p.Width/2
	}
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	return lo, hi
}

func scale(v, lo, hi float64) byte {
	if hi <= lo {
		if v > lo {
			return 255
		}
		return 0
	}
	f := (v - lo) / (hi - lo)
	return byte(math.Round(math.Max(0, math.Min(1, f)) * 255))
}
