package dicomretrieve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Download là phản hồi WADO-RS trả nguyên tệp DICOM Part 10 (multipart/related; type="application/dicom"), ghi theo luồng. Độ dài không biết trước (kho không lưu kích thước
// và study có thể gồm nhiều instance) nên phản hồi dùng chunked; lỗi sau khi đã ghi một phần thì người gọi ngắt kết nối.
type Download struct {
	ContentType string
	Write       func(io.Writer) error
}

const downloadPage = 100

// rawKey tìm khóa kho của nội dung một instance (Binary của nó); 404 nếu Binary không còn.
func (c Config) rawKey(ctx context.Context, inst *fhir.DicomInstance) (string, *Error) {
	if inst.Raw == nil {
		return "", notFound("Instance has no stored content")
	}
	br, err := c.Repo.ReadResource(ctx, "Binary", strings.TrimPrefix(inst.Raw.Reference, "Binary/"))
	if err != nil {
		var fe *fhirrouter.Error
		if errors.As(err, &fe) && (fe.Status == http.StatusNotFound || fe.Status == http.StatusGone) {
			return "", notFound("Instance has no stored content")
		}
		return "", internalErr(ctx, "đọc Binary", err)
	}
	bin, ok := br.(*fhir.Binary)
	if !ok || bin.Meta == nil {
		return "", internalErr(ctx, "Binary không hợp lệ", errors.New(inst.ID))
	}
	key, err := binarystore.Key(bin.ID, bin.Meta.VersionId)
	if err != nil {
		return "", internalErr(ctx, "khóa kho", err)
	}
	return key, nil
}

// OpenInstances trả tệp DICOM của một instance (instanceUID), mọi instance của một series (chỉ seriesUID) hoặc của một study (chỉ studyUID). Study, series hoặc instance không
// có thì 404, instance đầu tiên không có nội dung thì lỗi; mọi kiểm tra này xảy ra trước khi ghi byte nào.
func OpenInstances(ctx context.Context, c Config, studyUID, seriesUID, instanceUID string) (*Download, error) {
	var params url.Values
	switch {
	case instanceUID != "":
		series, inst, e := c.instance(ctx, studyUID, seriesUID, instanceUID)
		if e != nil {
			return nil, e
		}
		params = url.Values{"sop-instance-uid:exact": {inst.SopInstanceUid}, "series": {"DicomSeries/" + series.ID}}
	case seriesUID != "":
		_, series, e := c.series(ctx, studyUID, seriesUID)
		if e != nil {
			return nil, e
		}
		params = url.Values{"series": {"DicomSeries/" + series.ID}}
	default:
		study, e := c.study(ctx, studyUID)
		if e != nil {
			return nil, e
		}
		params = url.Values{"study": {"DicomStudy/" + study.ID}}
	}
	first, err := search(ctx, c.Repo, "DicomInstance", params, downloadPage, 0)
	if err != nil {
		return nil, internalErr(ctx, "tìm instance", err)
	}
	if len(first.Matches) == 0 {
		return nil, notFound("Instance not found")
	}
	// kiểm trước nội dung của instance đầu có đọc được, để lỗi kho trả sạch thay vì cắt ngang phản hồi
	if inst, ok := first.Matches[0].(*fhir.DicomInstance); ok {
		key, e := c.rawKey(ctx, inst)
		if e != nil {
			return nil, e
		}
		rc, err := c.Blobs.Open(ctx, key)
		if err != nil {
			return nil, internalErr(ctx, "mở nội dung", err)
		}
		_ = rc.Close()
	}
	boundary := newBoundary()
	return &Download{
		ContentType: fmt.Sprintf(`multipart/related; type="application/dicom"; boundary=%s`, boundary),
		Write: func(w io.Writer) error {
			res := first
			for offset := 0; ; offset += downloadPage {
				if offset > 0 {
					var err error
					if res, err = search(ctx, c.Repo, "DicomInstance", params, downloadPage, offset); err != nil {
						return err
					}
				}
				for _, m := range res.Matches {
					inst, ok := m.(*fhir.DicomInstance)
					if !ok {
						continue
					}
					key, e := c.rawKey(ctx, inst)
					if e != nil {
						return e
					}
					rc, err := c.Blobs.Open(ctx, key)
					if err != nil {
						return err
					}
					_, werr := w.Write(partHeader(boundary, "application/dicom"))
					if werr == nil {
						_, werr = io.Copy(w, rc)
					}
					_ = rc.Close()
					if werr == nil {
						_, werr = io.WriteString(w, "\r\n")
					}
					if werr != nil {
						return werr
					}
				}
				if !res.More {
					break
				}
			}
			_, err := w.Write(closing(boundary))
			return err
		},
	}, nil
}
