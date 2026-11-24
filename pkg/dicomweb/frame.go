package dicomweb

import (
	"fmt"
	"io"
	"strings"
)

// CopyFrame chép nội dung một frame từ luồng r (đang ở byte 0 của tệp DICOM) sang w: bỏ qua các byte đứng trước, rồi chép từng Part (hoặc cả khoảng nếu frame liền
// một khối), bỏ qua tiêu đề item xen giữa các fragment. Không cần Seek, bộ nhớ hằng số; trả số byte đã chép.
func CopyFrame(r io.Reader, fr FrameRange, w io.Writer) (int64, error) {
	parts := fr.Parts
	if len(parts) == 0 {
		parts = []FrameRange{fr}
	}
	var pos, written int64
	for _, p := range parts {
		if p.Offset < pos {
			return written, fmt.Errorf("%w: khoảng frame không tăng dần", ErrMalformed)
		}
		if _, err := io.CopyN(io.Discard, r, p.Offset-pos); err != nil {
			return written, fmt.Errorf("%w: tệp ngắn hơn vị trí frame", ErrMalformed)
		}
		n, err := io.CopyN(w, r, p.Length)
		written += n
		if err != nil {
			if err == io.EOF {
				return written, fmt.Errorf("%w: tệp ngắn hơn frame", ErrMalformed)
			}
			return written, err
		}
		pos = p.Offset + p.Length
	}
	return written, nil
}

// FrameMediaType trả loại nội dung (media type) của một frame theo transfer syntax của tệp, và có phải dạng nén không (PS3.18 Table 8.7.3-5). Dạng không
// nén (và RLE) là application/octet-stream; JPEG baseline và lossless là image/jpeg; JPEG-LS image/jls; JPEG 2000 image/jp2 (Part 2: image/jpx); HTJ2K image/jphc.
// Transfer syntax không biết thì application/octet-stream.
func FrameMediaType(transferSyntax string) string {
	switch {
	case transferSyntax == "1.2.840.10008.1.2.4.50", transferSyntax == "1.2.840.10008.1.2.4.51", transferSyntax == "1.2.840.10008.1.2.4.57", transferSyntax == "1.2.840.10008.1.2.4.70":
		return "image/jpeg"
	case transferSyntax == "1.2.840.10008.1.2.4.80", transferSyntax == "1.2.840.10008.1.2.4.81":
		return "image/jls"
	case transferSyntax == "1.2.840.10008.1.2.4.90", transferSyntax == "1.2.840.10008.1.2.4.91":
		return "image/jp2"
	case transferSyntax == "1.2.840.10008.1.2.4.92", transferSyntax == "1.2.840.10008.1.2.4.93":
		return "image/jpx"
	case strings.HasPrefix(transferSyntax, "1.2.840.10008.1.2.4.20"):
		return "image/jphc"
	}
	return "application/octet-stream"
}

// FrameContentType là Content-Type của một phần frame: media type kèm tham số transfer-syntax khi dữ liệu đã nén hoặc đóng gói (RLE), để client biết cách giải mã.
func FrameContentType(transferSyntax string) string {
	mt := FrameMediaType(transferSyntax)
	switch transferSyntax {
	case "", "1.2.840.10008.1.2", "1.2.840.10008.1.2.1", "1.2.840.10008.1.2.2":
		return mt
	}
	return mt + "; transfer-syntax=" + transferSyntax
}
