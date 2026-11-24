// Package binarystore là kho nội dung nhị phân theo khóa (Binary của FHIR, tệp DICOM, tệp đính kèm) và bộ ký URL tải lên/xuống.
//
// Gói chỉ dùng stdlib. Kho (Store) chỉ biết khóa và luồng byte: metadata (loại nội dung, tên tệp, project) nằm ở CSDL của app.
// Adapter: FS (hệ thống tệp, ghi nguyên tử, không thoát khỏi thư mục gốc) và Memory (cho test). Bộ test hợp đồng ở storetest.
package binarystore

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrNotFound: khóa không có trong kho.
	ErrNotFound = errors.New("binarystore: not found")
	// ErrTooLarge: nội dung vượt PutOptions.MaxBytes; kho không giữ lại gì.
	ErrTooLarge = errors.New("binarystore: content too large")
	// ErrInvalidRange: khoảng đọc của OpenRange không hợp lệ (offset âm, độ dài không dương, hoặc offset từ cuối nội dung trở đi).
	ErrInvalidRange = errors.New("binarystore: invalid range")
	// ErrInvalidKey: khóa không đúng dạng binary/<id>/<versionId> (hoặc id có ký tự ngoài ^[A-Za-z0-9\-.]{1,64}$, hoặc là "." / "..").
	ErrInvalidKey = errors.New("binarystore: invalid key")
	// ErrBlockedFileExtension và ErrBlockedContentType: tên tệp hay loại nội dung nằm trong danh sách chặn (CheckMetadata, Put).
	ErrBlockedFileExtension = errors.New("Invalid file extension") //nolint:staticcheck // thông điệp giữ nguyên như Medplum
	ErrBlockedContentType   = errors.New("Invalid content type")   //nolint:staticcheck // thông điệp giữ nguyên như Medplum
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9\-.]{1,64}$`)

const keyPrefix = "binary/"

func validID(s string) bool { return idPattern.MatchString(s) && s != "." && s != ".." }

// ValidID cho biết s là id hoặc versionId hợp lệ để dùng trong khóa (FHIR id, không phải "." hay "..").
func ValidID(s string) bool { return validID(s) }

// Key trả "binary/<id>/<versionID>"; ErrInvalidKey nếu id hay versionID không phải FHIR id hợp lệ (chặn "..", "/", "\", "%", NUL).
func Key(id, versionID string) (string, error) {
	if !validID(id) || !validID(versionID) {
		return "", ErrInvalidKey
	}
	return keyPrefix + id + "/" + versionID, nil
}

// SplitKey tách khóa thành id và versionID; ErrInvalidKey nếu không phải kết quả hợp lệ của Key.
func SplitKey(key string) (id, versionID string, err error) {
	rest, ok := strings.CutPrefix(key, keyPrefix)
	if !ok {
		return "", "", ErrInvalidKey
	}
	id, versionID, ok = strings.Cut(rest, "/")
	if !ok || !validID(id) || !validID(versionID) {
		return "", "", ErrInvalidKey
	}
	return id, versionID, nil
}

// PutOptions: ContentType (nếu có) bị kiểm với danh sách chặn; MaxBytes <= 0 là không trần.
type PutOptions struct {
	ContentType string
	MaxBytes    int64
}

// Store là kho đối tượng. Mọi phương thức nhận khóa dạng Key(...) và trả ErrInvalidKey cho khóa khác.
type Store interface {
	// Put ghi nguyên tử: người đọc không bao giờ thấy nội dung dở; lỗi (kể cả ErrTooLarge, ctx hủy, đọc r lỗi) thì khóa giữ nguyên như trước.
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (written int64, err error)
	// Open mở nội dung để đọc (người gọi phải Close); ErrNotFound nếu không có.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Copy chép nguyên tử src sang dst (ghi đè dst); ErrNotFound nếu không có src.
	Copy(ctx context.Context, srcKey, dstKey string) error
	// Delete xóa; idempotent (khóa không có thì không lỗi).
	Delete(ctx context.Context, key string) error
}

// RangeOpener là tùy chọn của adapter: đọc một khoảng byte mà không đọc phần đứng trước (FS: Seek; S3: header Range; Memory: cắt lát). Nhờ đó lấy một frame của tệp DICOM
// lớn tốn O(kích thước frame) thay vì O(vị trí frame). OpenRange trả đúng các byte [offset, offset+length); nội dung ngắn hơn offset+length thì trả tới hết nội dung (người đọc
// tự kiểm số byte nhận được); offset không nhỏ hơn kích thước nội dung thì ErrInvalidRange; ErrNotFound nếu khóa không có. Người gọi phải Close.
type RangeOpener interface {
	OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
}

// URLPresigner là tùy chọn của adapter ngoài (S3...) cấp URL ký phía kho; FS và Memory không cài.
type URLPresigner interface {
	PresignURL(ctx context.Context, key, method string, ttl time.Duration) (string, error)
}

// Entry là một khóa trong kho kèm kích thước và thời điểm ghi cuối (dùng để quét nội dung mồ côi).
type Entry struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// Lister là tùy chọn của adapter: duyệt mọi khóa hợp lệ (dạng Key) hiện có, thứ tự không xác định. Khóa/tệp không đúng dạng bị bỏ qua.
// fn trả lỗi thì dừng và trả lỗi đó. Kho đang được ghi đồng thời: kết quả không phải ảnh chụp nhất quán (đủ cho việc quét có thời gian ân hạn).
type Lister interface {
	List(ctx context.Context, fn func(Entry) error) error
}

// TempCleaner là tùy chọn của adapter có tệp tạm cục bộ (FS): xóa tệp tạm bỏ lại do tiến trình chết giữa lúc ghi, cũ hơn olderThan.
type TempCleaner interface {
	CleanTemp(ctx context.Context, olderThan time.Duration) (removed int, err error)
}

// checkPut gom kiểm tra dùng chung trước khi ghi: khóa và loại nội dung.
func checkPut(key string, opts PutOptions) error {
	if _, _, err := SplitKey(key); err != nil {
		return err
	}
	if opts.ContentType != "" {
		return CheckMetadata("", opts.ContentType)
	}
	return nil
}

// ctxReader dừng việc đọc khi ctx bị hủy, để Put dừng giữa chừng.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
