// Package dicomretrieve là QIDO-RS (PS3.18 10.6) và WADO-RS (10.4) trên dữ liệu do dicomstow ghi: tìm study và series, trả metadata của series và các frame
// ảnh. Không biết HTTP: trả cấu trúc hoặc ghi vào io.Writer, lỗi có mã trạng thái (*Error).
//
// Frame được lấy trực tiếp từ nội dung DICOM đã lưu, không nhân đôi dung lượng. Vị trí frame lấy từ chỉ mục lập lúc STOW (extension dicomweb.FrameIndexURL của DicomInstance):
// kho đọc được theo khoảng (binarystore.RangeOpener: FS, S3, Memory) thì mỗi frame là một lần đọc O(kích thước frame). Không có chỉ mục (instance cũ, quá nhiều frame, nhiều
// fragment không tách được) hoặc kho không đọc theo khoảng thì quay về cách của D4: quét tìm vị trí rồi chép, O(vị trí frame). Mọi đường đều theo luồng, bộ nhớ hằng số.
package dicomretrieve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

const (
	// DefaultLimit và MaxLimit là cỡ trang của QIDO-RS khi client không (hoặc đặt quá) `limit`.
	DefaultLimit = 100
	MaxLimit     = 1000
	// MaxFramesPerRequest là số frame tối đa trong một yêu cầu WADO-RS (`frames/1,2,3`).
	MaxFramesPerRequest = 50
	metadataPage        = 500
)

// Config là phụ thuộc của dịch vụ.
type Config struct {
	Repo  fhirrouter.Repository // kho resource của người gọi (đã tách project, fhirauth.Scope)
	Blobs binarystore.Store     // kho nội dung DICOM
}

// Error là lỗi kèm mã HTTP và thông điệp an toàn để trả cho client.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func notFound(msg string) *Error { return &Error{http.StatusNotFound, msg} }

// internalErr ghi chi tiết vào log và trả lỗi 500 không lộ chi tiết nội bộ.
func internalErr(ctx context.Context, what string, err error) *Error {
	var fe *fhirrouter.Error
	if errors.As(err, &fe) {
		switch fe.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return &Error{fe.Status, "Không đủ quyền"}
		}
	}
	slog.ErrorContext(ctx, "dicomretrieve: "+what, "error", err)
	return &Error{http.StatusInternalServerError, "Lỗi xử lý"}
}

// ---------------------------------------------------------------- tìm resource

func search(ctx context.Context, repo fhirrouter.Repository, rt string, q url.Values, count, offset int) (*fhirrouter.SearchResult, error) {
	req, err := fhirsearch.Parse(rt, q)
	if err != nil {
		return nil, err
	}
	req.Count, req.Offset, req.Total = count, offset, "none"
	return repo.Search(ctx, req)
}

func findOne(ctx context.Context, repo fhirrouter.Repository, rt string, q url.Values) (fhir.Resource, error) {
	res, err := search(ctx, repo, rt, q, 1, 0)
	if err != nil || len(res.Matches) == 0 {
		return nil, err
	}
	return res.Matches[0], nil
}

func (c Config) study(ctx context.Context, uid string) (*fhir.DicomStudy, *Error) {
	r, err := findOne(ctx, c.Repo, "DicomStudy", url.Values{"study-instance-uid:exact": {uid}})
	if err != nil {
		return nil, internalErr(ctx, "tìm study", err)
	}
	if s, ok := r.(*fhir.DicomStudy); ok {
		return s, nil
	}
	return nil, notFound("Study not found")
}

func (c Config) series(ctx context.Context, studyUID, seriesUID string) (*fhir.DicomStudy, *fhir.DicomSeries, *Error) {
	study, e := c.study(ctx, studyUID)
	if e != nil {
		return nil, nil, e
	}
	r, err := findOne(ctx, c.Repo, "DicomSeries", url.Values{"study": {"DicomStudy/" + study.ID}, "series-instance-uid:exact": {seriesUID}})
	if err != nil {
		return nil, nil, internalErr(ctx, "tìm series", err)
	}
	if s, ok := r.(*fhir.DicomSeries); ok {
		return study, s, nil
	}
	return nil, nil, notFound("Series not found")
}

// ---------------------------------------------------------------- QIDO-RS

// Page là một trang kết quả QIDO-RS.
type Page struct {
	Items []dicomweb.Dataset
	More  bool // còn kết quả sau trang này
}

func clamp(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	return min(limit, MaxLimit)
}

// SearchStudies trả các study (mới cập nhật trước). Hiện chỉ hỗ trợ phân trang (limit, offset), như Medplum; bộ lọc theo thuộc tính để D7.
func SearchStudies(ctx context.Context, c Config, limit, offset int) (*Page, error) {
	limit = clamp(limit)
	res, err := search(ctx, c.Repo, "DicomStudy", url.Values{"_sort": {"-_lastUpdated"}}, limit, max(offset, 0))
	if err != nil {
		return nil, internalErr(ctx, "tìm study", err)
	}
	p := &Page{More: res.More, Items: []dicomweb.Dataset{}}
	for _, m := range res.Matches {
		if s, ok := m.(*fhir.DicomStudy); ok {
			p.Items = append(p.Items, dicomweb.StudyToDataset(s))
		}
	}
	return p, nil
}

// SearchSeries trả các series của một study (kèm thuộc tính cấp study); 404 nếu study không có.
func SearchSeries(ctx context.Context, c Config, studyUID string, limit, offset int) (*Page, error) {
	study, e := c.study(ctx, studyUID)
	if e != nil {
		return nil, e
	}
	limit = clamp(limit)
	res, err := search(ctx, c.Repo, "DicomSeries", url.Values{"study": {"DicomStudy/" + study.ID}}, limit, max(offset, 0))
	if err != nil {
		return nil, internalErr(ctx, "tìm series", err)
	}
	p := &Page{More: res.More, Items: []dicomweb.Dataset{}}
	for _, m := range res.Matches {
		if s, ok := m.(*fhir.DicomSeries); ok {
			p.Items = append(p.Items, dicomweb.SeriesToDataset(study, s))
		}
	}
	return p, nil
}

// ---------------------------------------------------------------- WADO-RS: metadata

// SeriesMetadata kiểm study và series có thật (lỗi 404 trước khi ghi gì), rồi trả hàm ghi mảng JSON metadata của mọi instance trong series theo luồng, từng
// trang 500 instance (Medplum cắt ở 1000 instance mà không báo; ở đây trả đủ). Metadata là chuỗi JSON đã làm sạch do STOW lưu, ghi nguyên văn.
func SeriesMetadata(ctx context.Context, c Config, studyUID, seriesUID string) (func(io.Writer) error, error) {
	_, series, e := c.series(ctx, studyUID, seriesUID)
	if e != nil {
		return nil, e
	}
	ref := "DicomSeries/" + series.ID
	return func(w io.Writer) error {
		if _, err := io.WriteString(w, "["); err != nil {
			return err
		}
		first := true
		for offset := 0; ; offset += metadataPage {
			res, err := search(ctx, c.Repo, "DicomInstance", url.Values{"series": {ref}}, metadataPage, offset)
			if err != nil {
				return err
			}
			for _, m := range res.Matches {
				inst, ok := m.(*fhir.DicomInstance)
				if !ok || inst.Metadata == "" || !json.Valid([]byte(inst.Metadata)) {
					if ok {
						slog.WarnContext(ctx, "dicomretrieve: instance không có metadata hợp lệ", "instance", inst.ID)
					}
					continue
				}
				if !first {
					if _, err := io.WriteString(w, ","); err != nil {
						return err
					}
				}
				first = false
				if _, err := io.WriteString(w, inst.Metadata); err != nil {
					return err
				}
			}
			if !res.More {
				break
			}
		}
		_, err := io.WriteString(w, "]")
		return err
	}, nil
}

// ---------------------------------------------------------------- WADO-RS: frame

// Frames là phản hồi multipart/related của một yêu cầu frame, đã kiểm hợp lệ và sẵn sàng ghi.
type Frames struct {
	ContentType string // Content-Type của cả phản hồi (multipart/related, có boundary)
	Length      int64  // tổng số byte phản hồi, biết trước nên đặt được Content-Length
	// Write ghi toàn bộ phản hồi theo luồng. Lỗi sau khi đã ghi một phần không còn đổi được mã trạng thái.
	Write func(io.Writer) error
}

// OpenFrames kiểm study, series, instance, số frame và đọc cấu trúc tệp để biết vị trí frame; mọi lỗi 4xx và 5xx đều xảy ra trước khi trả Frames (chưa ghi byte
// nào). frames là các số thứ tự bắt đầu từ 1.
func OpenFrames(ctx context.Context, c Config, studyUID, seriesUID, instanceUID string, frames []int) (*Frames, error) {
	if len(frames) == 0 || len(frames) > MaxFramesPerRequest {
		return nil, &Error{http.StatusBadRequest, "Invalid frame number"}
	}
	for _, f := range frames {
		if f < 1 {
			return nil, &Error{http.StatusBadRequest, "Invalid frame number"}
		}
	}
	_, series, e := c.series(ctx, studyUID, seriesUID)
	if e != nil {
		return nil, e
	}
	r, err := findOne(ctx, c.Repo, "DicomInstance", url.Values{"series": {"DicomSeries/" + series.ID}, "sop-instance-uid:exact": {instanceUID}})
	if err != nil {
		return nil, internalErr(ctx, "tìm instance", err)
	}
	inst, ok := r.(*fhir.DicomInstance)
	if !ok {
		return nil, notFound("Instance not found")
	}
	if inst.Rows == nil || inst.Columns == nil || inst.Raw == nil { // không có ảnh (ví dụ báo cáo có cấu trúc)
		return nil, notFound("Pixel data not found for instance")
	}
	total := 1
	if inst.NumberOfFrames != nil && *inst.NumberOfFrames > 0 {
		total = *inst.NumberOfFrames
	}
	for _, f := range frames {
		if f > total {
			return nil, &Error{http.StatusRequestedRangeNotSatisfiable, "Requested frame number exceeds total frames available"}
		}
	}

	binID := strings.TrimPrefix(inst.Raw.Reference, "Binary/")
	br, err := c.Repo.ReadResource(ctx, "Binary", binID)
	if err != nil {
		var fe *fhirrouter.Error
		if errors.As(err, &fe) && (fe.Status == http.StatusNotFound || fe.Status == http.StatusGone) {
			return nil, notFound("Pixel data not found for instance")
		}
		return nil, internalErr(ctx, "đọc Binary", err)
	}
	bin, ok := br.(*fhir.Binary)
	if !ok || bin.Meta == nil {
		return nil, internalErr(ctx, "Binary không hợp lệ", errors.New(binID))
	}
	key, err := binarystore.Key(bin.ID, bin.Meta.VersionId)
	if err != nil {
		return nil, internalErr(ctx, "khóa kho", err)
	}
	open := func() (io.ReadCloser, error) { return c.Blobs.Open(ctx, key) }

	// vị trí frame: ưu tiên chỉ mục lập lúc STOW (extension của DicomInstance); không có hoặc không khớp thì quét nội dung đã lưu
	var all []dicomweb.FrameRange
	transferSyntax := ""
	if idx := frameIndexOf(ctx, inst); idx != nil && len(idx.Frames) == total {
		all, transferSyntax = idx.Frames, idx.TransferSyntax
	} else {
		rc, err := open()
		if err != nil {
			return nil, internalErr(ctx, "mở nội dung", err)
		}
		scan, err := dicomweb.Scan(rc, dicomweb.ScanOptions{})
		_ = rc.Close()
		if err != nil || !scan.HasPixelData {
			return nil, internalErr(ctx, "quét nội dung", fmt.Errorf("instance %s: %v", inst.ID, err))
		}
		all, transferSyntax = scan.Frames, scan.TransferSyntax
	}
	ranges := make([]dicomweb.FrameRange, len(frames))
	for i, f := range frames {
		if f > len(all) {
			return nil, &Error{http.StatusRequestedRangeNotSatisfiable, "Requested frame number exceeds total frames available"}
		}
		ranges[i] = all[f-1]
	}
	partType := dicomweb.FrameContentType(transferSyntax)
	boundary := newBoundary()
	var length int64
	for _, rg := range ranges {
		length += int64(len(partHeader(boundary, partType))) + rg.Size() + 2 // CRLF sau dữ liệu
	}
	length += int64(len(closing(boundary)))

	// Kho đọc được theo khoảng thì mỗi frame (hoặc fragment) là một lần đọc O(kích thước frame); không thì mở và bỏ qua các byte đứng trước. Với kho đọc theo khoảng, mở
	// sẵn phần đầu tiên ngay bây giờ để nội dung mất hoặc khoảng hỏng trả 500 trước khi ghi byte nào.
	ro, _ := c.Blobs.(binarystore.RangeOpener)
	var first io.ReadCloser
	if ro != nil {
		p := ranges[0]
		if len(p.Parts) > 0 {
			p = p.Parts[0]
		}
		if first, err = ro.OpenRange(ctx, key, p.Offset, p.Length); err != nil {
			return nil, internalErr(ctx, "đọc khoảng nội dung", err)
		}
	}
	return &Frames{
		ContentType: fmt.Sprintf(`multipart/related; type="%s"; boundary=%s`, dicomweb.FrameMediaType(transferSyntax), boundary),
		Length:      length,
		Write: func(w io.Writer) error {
			for _, rg := range ranges {
				if _, err := w.Write(partHeader(boundary, partType)); err != nil {
					return err
				}
				if ro != nil {
					parts := rg.Parts
					if len(parts) == 0 {
						parts = []dicomweb.FrameRange{rg}
					}
					for _, p := range parts {
						rc := first
						first = nil
						if rc == nil {
							var err error
							if rc, err = ro.OpenRange(ctx, key, p.Offset, p.Length); err != nil {
								return err
							}
						}
						n, err := io.Copy(w, rc)
						_ = rc.Close()
						if err == nil && n != p.Length {
							err = fmt.Errorf("%w: nhận %d byte, cần %d", dicomweb.ErrMalformed, n, p.Length)
						}
						if err != nil {
							return err
						}
					}
				} else {
					rc, err := open()
					if err != nil {
						return err
					}
					_, err = dicomweb.CopyFrame(rc, rg, w)
					_ = rc.Close()
					if err != nil {
						return err
					}
				}
				if _, err := io.WriteString(w, "\r\n"); err != nil {
					return err
				}
			}
			if first != nil { // không có phần nào dùng tới (không xảy ra), vẫn đóng
				_ = first.Close()
			}
			_, err := w.Write(closing(boundary))
			return err
		},
	}, nil
}

// frameIndexOf đọc chỉ mục frame lưu ở DicomInstance; hỏng thì bỏ qua (và ghi log) để quay về quét nội dung.
func frameIndexOf(ctx context.Context, inst *fhir.DicomInstance) *dicomweb.FrameIndex {
	for _, ex := range inst.Extension {
		if ex.URL != dicomweb.FrameIndexURL {
			continue
		}
		idx, err := dicomweb.ParseFrameIndex(ex.ValueString)
		if err != nil {
			slog.WarnContext(ctx, "dicomretrieve: bỏ qua chỉ mục frame hỏng", "instance", inst.ID, "error", err)
			return nil
		}
		return idx
	}
	return nil
}

func newBoundary() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "dicomweb-" + hex.EncodeToString(b[:])
}

func partHeader(boundary, contentType string) []byte {
	return []byte("--" + boundary + "\r\nContent-Type: " + contentType + "\r\n\r\n")
}

func closing(boundary string) []byte { return []byte("--" + boundary + "--\r\n") }

// ParseFrames đọc danh sách frame của URL (`1` hoặc `1,2,3`); lỗi 400 nếu không hợp lệ.
func ParseFrames(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		if part == "" || strings.TrimLeft(part, "0123456789") != "" || len(part) > 9 {
			return nil, &Error{http.StatusBadRequest, "Invalid frame number"}
		}
		n, _ := strconv.Atoi(part)
		if n < 1 {
			return nil, &Error{http.StatusBadRequest, "Invalid frame number"}
		}
		out = append(out, n)
	}
	if len(out) > MaxFramesPerRequest {
		return nil, &Error{http.StatusBadRequest, "Too many frames in one request"}
	}
	return out, nil
}
