package binarystore

import (
	"bytes"
	"context"
	"io"
	"sort"
	"sync"
	"time"
)

// Memory là kho trong bộ nhớ cho test của các gói dùng binarystore (DICOM, email, fhirbinary); không dùng ở production.
type Memory struct {
	mu sync.Mutex
	m  map[string][]byte
	ts map[string]time.Time
}

// NewMemory tạo kho rỗng.
func NewMemory() *Memory { return &Memory{m: map[string][]byte{}, ts: map[string]time.Time{}} }

// Put giữ cùng hợp đồng với FS (khóa, loại bị chặn, trần, ctx).
func (s *Memory) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (int64, error) {
	if err := checkPut(key, opts); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var src io.Reader = ctxReader{ctx, r}
	if opts.MaxBytes > 0 {
		src = io.LimitReader(src, opts.MaxBytes+1)
	}
	b, err := io.ReadAll(src)
	if err != nil {
		return 0, err
	}
	if opts.MaxBytes > 0 && int64(len(b)) > opts.MaxBytes {
		return 0, ErrTooLarge
	}
	s.mu.Lock()
	s.m[key] = b
	s.ts[key] = time.Now()
	s.mu.Unlock()
	return int64(len(b)), nil
}

// Open trả bản sao nội dung tại thời điểm mở.
func (s *Memory) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if _, _, err := SplitKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	b, ok := s.m[key]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *Memory) Copy(ctx context.Context, srcKey, dstKey string) error {
	if _, _, err := SplitKey(dstKey); err != nil {
		return err
	}
	rc, err := s.Open(ctx, srcKey)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = s.Put(ctx, dstKey, rc, PutOptions{})
	return err
}

func (s *Memory) Delete(ctx context.Context, key string) error {
	if _, _, err := SplitKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.m, key)
	delete(s.ts, key)
	s.mu.Unlock()
	return nil
}

// Len trả số khóa đang giữ (cho test kiểm không mồ côi).
func (s *Memory) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// SetModTime đặt thời điểm ghi của một khóa (cho test quét mồ côi); false nếu khóa không có.
func (s *Memory) SetModTime(key string, t time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok {
		return false
	}
	s.ts[key] = t
	return true
}

// List duyệt bản chụp các khóa theo thứ tự tăng dần (fn chạy ngoài khóa nên có thể gọi lại Memory).
func (s *Memory) List(ctx context.Context, fn func(Entry) error) error {
	s.mu.Lock()
	es := make([]Entry, 0, len(s.m))
	for k, b := range s.m {
		es = append(es, Entry{Key: k, Size: int64(len(b)), ModTime: s.ts[k]})
	}
	s.mu.Unlock()
	sort.Slice(es, func(i, j int) bool { return es[i].Key < es[j].Key })
	for _, e := range es {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
