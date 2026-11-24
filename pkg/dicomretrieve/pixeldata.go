package dicomretrieve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// Phần này tạo `DicomInstance.pixelData` như worker của Medplum: mỗi frame thành một Binary riêng (nội dung chép từ tệp DICOM đã lưu, Content-Type theo transfer syntax,
// securityContext trỏ về DicomInstance) và `pixelData` là danh sách tham chiếu theo thứ tự frame. WADO-RS không dùng chúng (vẫn đọc theo khoảng từ tệp gốc); chúng chỉ để client đọc
// thẳng qua API FHIR (`Binary/{id}`) như với Medplum. Đổi lại dung lượng nhân đôi (mỗi frame được lưu hai lần), nên tính năng này tùy chọn.

var (
	// ErrNoPixels: instance không có ảnh (báo cáo có cấu trúc...), không có gì để tách.
	ErrNoPixels = errors.New("dicomretrieve: instance không có ảnh")
)

// PixelDataOptions điều chỉnh BuildPixelData và SweepPixelData.
type PixelDataOptions struct {
	// Force dựng lại cả khi pixelData đã đủ số frame: tạo Binary mới, đổi tham chiếu rồi xóa Binary cũ.
	Force bool
}

func framesOf(inst *fhir.DicomInstance) int {
	if inst.NumberOfFrames != nil && *inst.NumberOfFrames > 0 {
		return *inst.NumberOfFrames
	}
	return 1
}

// BuildPixelData tạo pixelData cho một instance và trả số Binary đã tạo (0 nếu đã đủ). Lỗi giữa chừng thì xóa các Binary đã tạo (không để mồ côi). Ghi instance bằng If-Match theo phiên
// bản đã đọc nên thua một ghi song song thì cũng dọn rồi báo lỗi (chạy lại là xong). Chi phí: đọc một lần mỗi frame từ kho (theo khoảng khi kho hỗ trợ) và ghi một lần.
func BuildPixelData(ctx context.Context, c Config, inst *fhir.DicomInstance, o PixelDataOptions) (int, error) {
	if inst.Rows == nil || inst.Columns == nil || inst.Raw == nil {
		return 0, ErrNoPixels
	}
	total := framesOf(inst)
	if !o.Force && len(inst.PixelData) == total {
		return 0, nil
	}
	key, ts, ranges, e := c.locateFrames(ctx, inst, total)
	if e != nil {
		return 0, e
	}
	if len(ranges) != total {
		return 0, fmt.Errorf("tệp có %d frame, metadata khai báo %d", len(ranges), total)
	}
	contentType := dicomweb.FrameMediaType(ts)

	var created []fhir.Reference
	cleanup := func() {
		for _, ref := range created {
			c.dropFrameBinary(ctx, ref.Reference)
		}
	}
	for i, rg := range ranges {
		ref, err := c.putFrame(ctx, inst, key, contentType, rg)
		if err != nil {
			cleanup()
			return 0, fmt.Errorf("frame %d: %w", i+1, err)
		}
		created = append(created, ref)
	}
	old := inst.PixelData
	updated := *inst
	updated.PixelData = created
	opts := fhirrouter.UpdateOptions{}
	if inst.Meta != nil {
		opts.IfMatch = inst.Meta.VersionId
	}
	if _, err := c.Repo.UpdateResource(ctx, &updated, opts); err != nil {
		cleanup()
		return 0, err
	}
	for _, ref := range old { // dựng lại: Binary cũ không còn được tham chiếu
		c.dropFrameBinary(ctx, ref.Reference)
	}
	return len(created), nil
}

// putFrame tạo Binary cho một frame và ghi nội dung frame vào kho theo luồng.
func (c Config) putFrame(ctx context.Context, inst *fhir.DicomInstance, rawKey, contentType string, rg dicomweb.FrameRange) (fhir.Reference, error) {
	frame := &fhir.Binary{ID: newUUID(), ContentType: contentType, SecurityContext: &fhir.Reference{Reference: "DicomInstance/" + inst.ID}}
	if inst.Meta != nil && inst.Meta.Project != "" {
		// kho đã tách project tự gán project của người gọi; kho chưa tách (lệnh quét bù) phải giữ project của instance, nếu không Binary sẽ không ai thấy
		frame.Meta = &fhir.Meta{Project: inst.Meta.Project}
	}
	bin, err := c.Repo.CreateResource(ctx, frame)
	if err != nil {
		return fhir.Reference{}, err
	}
	ver := ""
	if m := bin.GetMeta(); m != nil {
		ver = m.VersionId
	}
	key, err := binarystore.Key(bin.GetID(), ver)
	if err != nil {
		c.dropFrameBinary(ctx, "Binary/"+bin.GetID())
		return fhir.Reference{}, err
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(c.copyFrame(ctx, rawKey, rg, pw)) }()
	_, err = c.Blobs.Put(ctx, key, pr, binarystore.PutOptions{ContentType: contentType})
	_ = pr.CloseWithError(err)
	if err != nil {
		_ = c.Blobs.Delete(context.WithoutCancel(ctx), key)
		c.dropFrameBinary(ctx, "Binary/"+bin.GetID())
		return fhir.Reference{}, err
	}
	return fhir.Reference{Reference: "Binary/" + bin.GetID()}, nil
}

// copyFrame ghi các byte của một frame (nối các fragment nếu có) vào w, theo khoảng nếu kho hỗ trợ, không thì mở rồi bỏ qua phần đứng trước.
func (c Config) copyFrame(ctx context.Context, key string, rg dicomweb.FrameRange, w io.Writer) error {
	if ro, ok := c.Blobs.(binarystore.RangeOpener); ok {
		parts := rg.Parts
		if len(parts) == 0 {
			parts = []dicomweb.FrameRange{rg}
		}
		for _, p := range parts {
			rc, err := ro.OpenRange(ctx, key, p.Offset, p.Length)
			if err != nil {
				return err
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
		return nil
	}
	rc, err := c.Blobs.Open(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = dicomweb.CopyFrame(rc, rg, w)
	return err
}

// dropFrameBinary xóa nội dung và Binary của một frame (cố gắng hết sức, thử lại vì SQLite dev có thể báo bận); lỗi chỉ ghi log.
func (c Config) dropFrameBinary(ctx context.Context, ref string) {
	ctx = context.WithoutCancel(ctx)
	id := ref
	if len(ref) > 7 && ref[:7] == "Binary/" {
		id = ref[7:]
	}
	if r, err := c.Repo.ReadResource(ctx, "Binary", id); err == nil {
		if b, ok := r.(*fhir.Binary); ok && b.Meta != nil {
			if key, kerr := binarystore.Key(b.ID, b.Meta.VersionId); kerr == nil {
				if derr := c.Blobs.Delete(ctx, key); derr != nil {
					slog.WarnContext(ctx, "dicomretrieve: không xóa được nội dung frame", "binary", id, "error", derr)
				}
			}
		}
	}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = c.Repo.DeleteResource(ctx, "Binary", id); err == nil {
			return
		}
		var fe *fhirrouter.Error
		if errors.As(err, &fe) && (fe.Status == http.StatusNotFound || fe.Status == http.StatusGone || fe.Status == http.StatusForbidden) {
			break
		}
		time.Sleep(time.Duration(10<<attempt) * time.Millisecond)
	}
	slog.WarnContext(ctx, "dicomretrieve: không xóa được Binary của frame", "binary", id, "error", err)
}

// PixelDataReport là kết quả của SweepPixelData.
type PixelDataReport struct {
	Scanned  int // số DicomInstance đã duyệt
	Built    int // instance đã dựng (hoặc, với DryRun, sẽ dựng) pixelData
	Frames   int // tổng số Binary frame đã tạo
	UpToDate int // đã đủ pixelData
	NoPixels int // không có ảnh
	Failed   int // lỗi: chạy lại để thử tiếp
}

// PixelDataSweepOptions điều chỉnh SweepPixelData.
type PixelDataSweepOptions struct {
	PixelDataOptions
	DryRun bool // chỉ đếm
	Limit  int  // dừng sau chừng ấy instance cần dựng (0: không giới hạn)
}

// SweepPixelData dựng pixelData cho mọi instance còn thiếu (đã có từ trước khi bật tính năng, hoặc lần dựng tự động bị lỗi). Hai pha như Reindex (duyệt chỉ đọc rồi dựng từng
// instance) nên ghi không làm lệch phân trang; chạy lại bao nhiêu lần cũng an toàn.
func SweepPixelData(ctx context.Context, c Config, o PixelDataSweepOptions) (PixelDataReport, error) {
	var rep PixelDataReport
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
			case !o.Force && len(inst.PixelData) == framesOf(inst):
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
			rep.Built++
			continue
		}
		n, err := BuildPixelData(ctx, c, inst, o.PixelDataOptions)
		if err != nil {
			rep.Failed++
			slog.WarnContext(ctx, "dicomretrieve: không dựng được pixelData", "instance", inst.ID, "error", err.Error())
			continue
		}
		rep.Built++
		rep.Frames += n
	}
	return rep, nil
}
