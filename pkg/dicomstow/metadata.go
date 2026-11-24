package dicomstow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"trivita-fhir/pkg/dicomweb"
)

// maxMetadataPart là trần phần metadata DICOM JSON của STOW dạng metadata (8 MiB: đủ cho hàng nghìn instance không kèm nhị phân lớn).
const maxMetadataPart = 8 << 20

// isMetadataPart: phần đầu của yêu cầu là DICOM JSON, tức yêu cầu STOW dạng metadata.
func isMetadataPart(p *multipart.Part) bool {
	mt, _, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
	return err == nil && mt == "application/dicom+json"
}

// spooled là một bulkdata đã nhận về tệp tạm (cần biết độ dài trước khi dựng phần tử DICOM).
type spooled struct {
	path        string
	size        int64
	contentType string
	oversize    bool // vượt MaxInstanceBytes: không giữ nội dung
}

// bulkKeys là các khóa mà BulkDataURI có thể dùng để tham chiếu một phần: Content-Location nguyên văn và Content-ID dạng cid:<id>.
func bulkKeys(get func(string) string) []string {
	var keys []string
	if v := strings.TrimSpace(get("Content-Location")); v != "" {
		keys = append(keys, v)
	}
	if v := strings.TrimSpace(get("Content-ID")); v != "" {
		keys = append(keys, "cid:"+strings.TrimSuffix(strings.TrimPrefix(v, "<"), ">"))
	}
	return keys
}

// storeMetadata xử lý STOW dạng metadata (PS3.18 10.5.1.2): phần đầu là mảng DICOM JSON (mỗi phần tử một instance), các phần sau là bulkdata tham chiếu bằng BulkDataURI
// (Content-Location hoặc cid:Content-ID). Mỗi instance được dựng thành tệp DICOM Part 10 (dicomweb.BuildPart10) rồi đi qua đúng đường lưu của STOW tệp thường, nên kiểm tra,
// idempotent, chỉ mục frame và phản hồi giống hệt. Bulkdata được ghi ra tệp tạm (cần độ dài trước khi dựng phần tử) và xóa khi xong.
func (s *service) storeMetadata(ctx context.Context, out *Outcome, mr *multipart.Reader, first *multipart.Part) (*Outcome, error) {
	raw, err := io.ReadAll(io.LimitReader(first, maxMetadataPart+1))
	switch {
	case isTooLarge(err):
		return nil, &RequestError{http.StatusRequestEntityTooLarge, "Body quá lớn"}
	case err != nil:
		return nil, &RequestError{http.StatusBadRequest, "Thân multipart không hợp lệ"}
	case len(raw) > maxMetadataPart:
		return nil, &RequestError{http.StatusRequestEntityTooLarge, "Metadata DICOM JSON quá lớn"}
	}
	datasets, err := dicomweb.ParseMetadataJSON(raw)
	if err != nil {
		return nil, &RequestError{http.StatusBadRequest, strings.TrimPrefix(err.Error(), dicomweb.ErrEncode.Error()+": ")}
	}

	dir, err := os.MkdirTemp(s.cfg.TempDir, "dicomstow-")
	if err != nil {
		slog.ErrorContext(ctx, "dicomstow: không tạo được thư mục tạm", "error", err)
		return nil, &RequestError{http.StatusInternalServerError, "Không tạo được thư mục tạm"}
	}
	defer os.RemoveAll(dir)

	bulks := map[string]*spooled{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if isTooLarge(err) {
				return nil, &RequestError{http.StatusRequestEntityTooLarge, "Body quá lớn"}
			}
			return nil, &RequestError{http.StatusBadRequest, "Thân multipart không hợp lệ"}
		}
		keys := bulkKeys(part.Header.Get)
		if len(keys) == 0 {
			return nil, &RequestError{http.StatusBadRequest, "Phần bulkdata thiếu Content-Location hoặc Content-ID"}
		}
		f, err := os.CreateTemp(dir, "bulk-*")
		if err != nil {
			return nil, &RequestError{http.StatusInternalServerError, "Không tạo được tệp tạm"}
		}
		var src io.Reader = part
		if s.cfg.MaxInstanceBytes > 0 {
			src = io.LimitReader(part, s.cfg.MaxInstanceBytes+1)
		}
		n, cerr := io.Copy(f, src)
		_ = f.Close()
		if cerr != nil {
			if isTooLarge(cerr) {
				return nil, &RequestError{http.StatusRequestEntityTooLarge, "Body quá lớn"}
			}
			return nil, &RequestError{http.StatusBadRequest, "Thân multipart không hợp lệ"}
		}
		sp := &spooled{path: f.Name(), size: n, contentType: part.Header.Get("Content-Type")}
		if s.cfg.MaxInstanceBytes > 0 && n > s.cfg.MaxInstanceBytes {
			sp.oversize = true
			_ = os.Remove(f.Name())
			// phần còn lại của yêu cầu vẫn phải đọc để các phần sau được nhận; bỏ phần thừa của phần này
			_, _ = io.Copy(io.Discard, part)
		}
		for _, k := range keys {
			bulks[k] = sp
		}
		_ = part.Close()
	}

	for _, ds := range datasets {
		var opened []*os.File
		oversize := false
		resolve := func(uri string) (*dicomweb.Bulk, bool) {
			sp, ok := bulks[strings.TrimSpace(uri)]
			if !ok {
				return nil, false
			}
			if sp.oversize {
				oversize = true
				return nil, false
			}
			f, err := os.Open(sp.path)
			if err != nil {
				return nil, false
			}
			opened = append(opened, f)
			return &dicomweb.Bulk{R: f, Size: sp.size, ContentType: sp.contentType}, true
		}
		segs, _, berr := dicomweb.BuildPart10(ds, resolve)
		var st *Stored
		var fl *Failed
		var fatal *RequestError
		switch {
		case oversize:
			fl = fail(ReasonOutOfResources, "Bulkdata vượt trần kích thước instance", ds)
		case berr != nil:
			if !errors.Is(berr, dicomweb.ErrEncode) {
				slog.ErrorContext(ctx, "dicomstow: dựng DICOM từ metadata lỗi", "error", berr)
			}
			fl = fail(ReasonCannotUnderstand, strings.TrimPrefix(berr.Error(), dicomweb.ErrEncode.Error()+": "), ds)
		default:
			st, fl, fatal = s.storeOne(ctx, "application/dicom", io.MultiReader(segs...))
		}
		for _, f := range opened {
			_ = f.Close()
		}
		if fatal != nil {
			s.cleanupAggregates(ctx)
			return nil, fatal
		}
		if fl != nil {
			out.Failed = append(out.Failed, *fl)
			if fl.abort {
				break
			}
			continue
		}
		out.Stored = append(out.Stored, *st)
	}
	return s.conclude(ctx, out)
}
