package dicomretrieve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// ReindexReport là kết quả của Reindex.
type ReindexReport struct {
	Scanned   int // số DicomInstance đã duyệt
	Indexed   int // đã lập (hoặc, với DryRun, sẽ lập) chỉ mục
	UpToDate  int // đã có chỉ mục khớp số frame
	NoPixels  int // không có ảnh (báo cáo có cấu trúc...): không cần chỉ mục
	Unindexed int // đã quét nhưng không lập được chỉ mục (ảnh đóng gói không tách được frame, hoặc chỉ mục quá lớn): WADO-RS vẫn đúng nhờ quét
	Failed    int // lỗi (mất nội dung, tệp hỏng, xung đột phiên bản...): chạy lại để thử tiếp
}

// ReindexOptions điều chỉnh Reindex.
type ReindexOptions struct {
	DryRun bool // chỉ đếm, không ghi
	// Force lập lại cả chỉ mục đã khớp số frame (ví dụ sau khi sửa lỗi bộ lập chỉ mục).
	Force bool
	// Limit dừng sau chừng ấy instance cần lập (0: không giới hạn).
	Limit int
}

const reindexPage = 500

// Reindex lập (hoặc lập lại) chỉ mục frame cho các DicomInstance chưa có chỉ mục dùng được: instance lưu trước D5, chỉ mục hỏng hoặc sai số frame. Dùng Config.Repo đúng như được
// đưa vào: truyền kho chưa tách project (fhirstore) để duyệt mọi project, hoặc kho đã tách để chỉ duyệt project của Principal.
//
// Hai pha để việc ghi không làm lệch phân trang: pha một duyệt hết và ghi nhớ id cần xử lý (chỉ đọc), pha hai lập chỉ mục từng instance (quét nội dung đã lưu một lượt, không nạp
// vào bộ nhớ). Ghi dùng If-Match theo phiên bản đã đọc nên không ghi đè thay đổi chạy song song (instance đó tính là Failed, chạy lại là xong). Chạy lại bao nhiêu lần cũng an toàn.
func Reindex(ctx context.Context, c Config, o ReindexOptions) (ReindexReport, error) {
	var rep ReindexReport
	var todo []*fhir.DicomInstance
	for offset := 0; ; offset += reindexPage {
		res, err := search(ctx, c.Repo, "DicomInstance", url.Values{}, reindexPage, offset)
		if err != nil {
			return rep, fmt.Errorf("duyệt DicomInstance: %w", err)
		}
		for _, m := range res.Matches {
			inst, ok := m.(*fhir.DicomInstance)
			if !ok {
				continue
			}
			rep.Scanned++
			switch {
			case inst.Rows == nil || inst.Columns == nil || inst.Raw == nil:
				rep.NoPixels++
			case !o.Force && indexMatches(inst):
				rep.UpToDate++
			default:
				todo = append(todo, inst)
			}
		}
		if !res.More {
			break
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
	}
	if o.Limit > 0 && len(todo) > o.Limit {
		todo = todo[:o.Limit]
	}
	for _, inst := range todo {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if o.DryRun {
			rep.Indexed++
			continue
		}
		switch err := reindexOne(ctx, c, inst); {
		case err == nil:
			rep.Indexed++
		case errors.Is(err, errNoIndex):
			rep.Unindexed++
		default:
			rep.Failed++
			slog.WarnContext(ctx, "dicomretrieve: không lập được chỉ mục frame", "instance", inst.ID, "error", err.Error())
		}
	}
	return rep, nil
}

var errNoIndex = errors.New("không lập được chỉ mục")

// indexMatches: instance đã có chỉ mục parse được và đúng số frame.
func indexMatches(inst *fhir.DicomInstance) bool {
	total := 1
	if inst.NumberOfFrames != nil && *inst.NumberOfFrames > 0 {
		total = *inst.NumberOfFrames
	}
	for _, ex := range inst.Extension {
		if ex.URL == dicomweb.FrameIndexURL {
			idx, err := dicomweb.ParseFrameIndex(ex.ValueString)
			return err == nil && len(idx.Frames) == total
		}
	}
	return false
}

func reindexOne(ctx context.Context, c Config, inst *fhir.DicomInstance) error {
	key, e := c.rawKey(ctx, inst)
	if e != nil {
		return e
	}
	rc, err := c.Blobs.Open(ctx, key)
	if err != nil {
		return err
	}
	scan, err := dicomweb.Scan(rc, dicomweb.ScanOptions{})
	_ = rc.Close()
	if err != nil {
		if errors.Is(err, dicomweb.ErrUnsplittable) {
			return errNoIndex
		}
		return err
	}
	if !scan.HasPixelData {
		return errNoIndex
	}
	total := 1
	if inst.NumberOfFrames != nil && *inst.NumberOfFrames > 0 {
		total = *inst.NumberOfFrames
	}
	if len(scan.Frames) != total {
		return fmt.Errorf("tệp có %d frame, metadata khai báo %d", len(scan.Frames), total)
	}
	enc, ok := dicomweb.FrameIndex{TransferSyntax: scan.TransferSyntax, Frames: scan.Frames}.Encode()
	if !ok {
		return errNoIndex
	}
	updated := *inst
	updated.Extension = nil
	for _, ex := range inst.Extension {
		if ex.URL != dicomweb.FrameIndexURL {
			updated.Extension = append(updated.Extension, ex)
		}
	}
	updated.Extension = append(updated.Extension, fhir.Extension{URL: dicomweb.FrameIndexURL, ValueString: enc})
	opts := fhirrouter.UpdateOptions{}
	if inst.Meta != nil {
		opts.IfMatch = inst.Meta.VersionId
	}
	_, err = c.Repo.UpdateResource(ctx, &updated, opts)
	return err
}
