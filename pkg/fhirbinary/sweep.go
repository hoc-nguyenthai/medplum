package fhirbinary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhirrouter"
)

// DefaultSweepGrace là thời gian ân hạn mặc định: nội dung mới hơn mức này không bao giờ bị coi là mồ côi.
const DefaultSweepGrace = 24 * time.Hour

// MinSweepGrace là ân hạn tối thiểu. Wrap ghi nội dung TRONG transaction trước khi commit nên một khóa hợp lệ có thể tồn tại một lúc
// mà CSDL chưa thấy phiên bản; ân hạn phải dài hơn mọi transaction.
const MinSweepGrace = time.Hour

// ErrSweepAborted: số mồ côi quá lớn so với tổng nên có khả năng repo sai (trỏ nhầm CSDL, CSDL rỗng); không xóa gì.
var ErrSweepAborted = errors.New("fhirbinary: số nội dung mồ côi bất thường, hủy quét (dùng Force nếu chắc chắn)")

// SweepOptions cấu hình Sweep.
type SweepOptions struct {
	// Grace: chỉ khóa cũ hơn mức này mới xét; 0 là DefaultSweepGrace; nhỏ hơn MinSweepGrace là lỗi.
	Grace time.Duration
	// DryRun chỉ đếm và liệt kê, không xóa.
	DryRun bool
	// MaxDelete là trần số khóa xóa mỗi lượt (0: 1000). Phần còn lại để lượt sau.
	MaxDelete int
	// Force bỏ qua chốt chặn tỷ lệ mồ côi (xem ErrSweepAborted).
	Force bool
	// Now (tùy chọn) thay đồng hồ (test).
	Now func() time.Time
}

// SweepResult là kết quả một lượt quét.
type SweepResult struct {
	Scanned     int // số khóa đã duyệt
	Young       int // bỏ qua vì còn trong ân hạn
	Live        int // có phiên bản trong CSDL
	Orphans     int // không có phiên bản trong CSDL và quá ân hạn
	Deleted     int
	Errors      int // lỗi tra cứu hoặc xóa (khóa đó được giữ lại)
	TempRemoved int // tệp tạm bỏ lại đã dọn (kho FS)
	Keys        []string
}

// Sweep xóa nội dung mồ côi: khóa binary/<id>/<versionId> mà repo không có phiên bản Binary tương ứng (kể cả phiên bản xóa) và đã
// quá ân hạn. Nguyên nhân thường gặp: tiến trình chết giữa ghi CSDL và ghi/xóa kho, lỗi bù trừ không xóa được.
//
// repo PHẢI là repository KHÔNG tách theo project và KHÔNG bọc Binary (đọc thẳng CSDL), nếu không phiên bản của project khác trông như
// không tồn tại và bị xóa. An toàn: chỉ xóa khi repo trả 404 dứt khoát (lỗi khác thì giữ khóa và đếm Errors); có trần MaxDelete; có
// chốt chặn tỷ lệ (ErrSweepAborted) khi quá nửa số khóa (từ 20 khóa) là mồ côi.
func Sweep(ctx context.Context, store binarystore.Store, repo fhirrouter.Repository, opt SweepOptions) (SweepResult, error) {
	var res SweepResult
	l, ok := store.(binarystore.Lister)
	if !ok {
		return res, errors.New("fhirbinary: kho không hỗ trợ duyệt khóa (binarystore.Lister)")
	}
	if opt.Grace == 0 {
		opt.Grace = DefaultSweepGrace
	}
	if opt.Grace < MinSweepGrace {
		return res, fmt.Errorf("fhirbinary: ân hạn tối thiểu %s", MinSweepGrace)
	}
	if opt.MaxDelete <= 0 {
		opt.MaxDelete = 1000
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	cutoff := now().Add(-opt.Grace)

	var orphans []string
	err := l.List(ctx, func(e binarystore.Entry) error {
		res.Scanned++
		if !e.ModTime.Before(cutoff) { // mới (hoặc thời gian trong tương lai): không đụng
			res.Young++
			return nil
		}
		id, ver, err := binarystore.SplitKey(e.Key)
		if err != nil {
			return nil
		}
		_, err = repo.ReadVersion(ctx, "Binary", id, ver)
		switch {
		case err == nil, isStatus(err, http.StatusGone):
			res.Live++
		case isStatus(err, http.StatusNotFound):
			res.Orphans++
			if len(orphans) < opt.MaxDelete {
				orphans = append(orphans, e.Key)
			}
		default:
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			res.Errors++
			slog.WarnContext(ctx, "fhirbinary: sweep không tra được phiên bản, giữ khóa", "key", e.Key, "error", err)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	res.Keys = orphans
	if !opt.Force && res.Scanned >= 20 && res.Orphans*2 > res.Scanned {
		return res, ErrSweepAborted
	}
	if !opt.DryRun {
		for _, k := range orphans {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if err := store.Delete(ctx, k); err != nil {
				res.Errors++
				slog.WarnContext(ctx, "fhirbinary: sweep không xóa được khóa", "key", k, "error", err)
				continue
			}
			res.Deleted++
		}
		if tc, ok := store.(binarystore.TempCleaner); ok {
			n, err := tc.CleanTemp(ctx, opt.Grace)
			res.TempRemoved = n
			if err != nil {
				res.Errors++
				slog.WarnContext(ctx, "fhirbinary: sweep không dọn được tệp tạm", "error", err)
			}
		}
	}
	return res, nil
}

func isStatus(err error, status int) bool {
	var fe *fhirrouter.Error
	return errors.As(err, &fe) && fe.Status == status
}
