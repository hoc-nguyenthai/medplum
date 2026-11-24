package binarystore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// tmpDir là thư mục tạm nằm trong gốc (cùng hệ thống tệp để rename nguyên tử); '_' không nằm trong FHIR id nên không đụng khóa nào.
const tmpDir = "_tmp"

// FS là adapter hệ thống tệp: bố cục <dir>/<id>/<versionId> (như Medplum, đọc được dữ liệu cũ). Mọi truy cập qua os.Root nên
// không thoát khỏi <dir> kể cả qua symlink. Không dùng chung được giữa nhiều replica (cần S3 khi đó).
type FS struct{ root *os.Root }

// NewFS tạo thư mục gốc nếu chưa có (quyền 0750) và mở nó.
func NewFS(dir string) (*FS, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("binarystore: tạo thư mục gốc: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("binarystore: mở thư mục gốc: %w", err)
	}
	return &FS{root: root}, nil
}

// Close giải phóng thư mục gốc.
func (f *FS) Close() error { return f.root.Close() }

func relPath(key string) (string, error) {
	id, ver, err := SplitKey(key)
	if err != nil {
		return "", err
	}
	return id + "/" + ver, nil
}

// Put ghi vào tệp tạm trong gốc, Sync, rồi rename lên khóa đích.
func (f *FS) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (int64, error) {
	if err := checkPut(key, opts); err != nil {
		return 0, err
	}
	rel, _ := relPath(key)
	return f.write(ctx, rel, r, opts.MaxBytes)
}

func (f *FS) write(ctx context.Context, rel string, r io.Reader, maxBytes int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := f.root.MkdirAll(tmpDir, 0o750); err != nil {
		return 0, err
	}
	dir, _ := filepath.Split(rel)
	if err := f.root.MkdirAll(filepath.Clean(dir), 0o750); err != nil {
		return 0, err
	}
	var rnd [12]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return 0, err
	}
	tmp := tmpDir + "/" + hex.EncodeToString(rnd[:])
	file, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, err
	}
	cleanup := func() { _ = file.Close(); _ = f.root.Remove(tmp) }
	src := ctxReader{ctx, r}
	var n int64
	if maxBytes > 0 {
		n, err = io.Copy(file, io.LimitReader(src, maxBytes+1))
		if err == nil && n > maxBytes {
			cleanup()
			return 0, ErrTooLarge
		}
	} else {
		n, err = io.Copy(file, src)
	}
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		cleanup()
		return 0, err
	}
	if err := file.Close(); err != nil {
		_ = f.root.Remove(tmp)
		return 0, err
	}
	if err := f.root.Rename(tmp, rel); err != nil {
		_ = f.root.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// Open: lỗi không có tệp là ErrNotFound; lỗi khác (quyền, I/O) giữ nguyên, không gộp thành "not found".
func (f *FS) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	rel, err := relPath(key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := f.root.Open(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if st, err := file.Stat(); err != nil || !st.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	return file, nil
}

// limited đọc tối đa n byte rồi đóng tệp khi Close.
type limited struct {
	io.Reader
	f *os.File
}

func (l limited) Close() error { return l.f.Close() }

// OpenRange: xem RangeOpener. Seek thẳng tới offset, không đọc phần đứng trước.
func (f *FS) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length <= 0 {
		if _, err := relPath(key); err != nil {
			return nil, err
		}
		return nil, ErrInvalidRange
	}
	rc, err := f.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	file := rc.(*os.File)
	if st, err := file.Stat(); err != nil || offset >= st.Size() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidRange
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return limited{io.LimitReader(file, length), file}, nil
}

var _ RangeOpener = (*FS)(nil)

// Copy đọc src và ghi dst qua tệp tạm (không thấy dst dở).
func (f *FS) Copy(ctx context.Context, srcKey, dstKey string) error {
	if _, err := relPath(dstKey); err != nil {
		return err
	}
	src, err := f.Open(ctx, srcKey)
	if err != nil {
		return err
	}
	defer src.Close()
	rel, _ := relPath(dstKey)
	_, err = f.write(ctx, rel, src, 0)
	return err
}

// Delete idempotent. Không xóa thư mục cha (tránh đua với Put đồng thời).
func (f *FS) Delete(ctx context.Context, key string) error {
	rel, err := relPath(key)
	if err != nil {
		return err
	}
	if err := f.root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List duyệt <dir>/<id>/<versionId>; bỏ qua _tmp, thư mục/tệp không đúng dạng và mọi thứ không phải tệp thường (kể cả symlink).
func (f *FS) List(ctx context.Context, fn func(Entry) error) error {
	top, err := fs.ReadDir(f.root.FS(), ".")
	if err != nil {
		return err
	}
	for _, d := range top {
		if !d.IsDir() || !validID(d.Name()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		files, err := fs.ReadDir(f.root.FS(), d.Name())
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) { // bị xóa giữa chừng
				continue
			}
			return err
		}
		for _, e := range files {
			if !e.Type().IsRegular() || !validID(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return err
			}
			if err := fn(Entry{Key: keyPrefix + d.Name() + "/" + e.Name(), Size: info.Size(), ModTime: info.ModTime()}); err != nil {
				return err
			}
		}
	}
	return nil
}

// CleanTemp xóa tệp thường trong _tmp có thời điểm sửa cũ hơn olderThan (tệp bỏ lại khi tiến trình chết giữa lúc ghi).
func (f *FS) CleanTemp(ctx context.Context, olderThan time.Duration) (int, error) {
	files, err := fs.ReadDir(f.root.FS(), tmpDir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range files {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < olderThan {
			continue
		}
		if err := f.root.Remove(tmpDir + "/" + e.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

var (
	_ Lister      = (*FS)(nil)
	_ TempCleaner = (*FS)(nil)
	_ Lister      = (*Memory)(nil)
)
