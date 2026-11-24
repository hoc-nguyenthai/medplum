// Package storetest là bộ test hợp đồng cho mọi adapter của binarystore.Store (chỉ dùng từ file _test.go).
package storetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"trivita-fhir/pkg/binarystore"
)

// New tạo một kho rỗng cho mỗi test.
type New func(t testing.TB) binarystore.Store

type failingReader struct {
	data []byte
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	return n, nil
}

// cancelAfter trả một phần dữ liệu rồi hủy ctx; lần Read sau phải thấy ctx đã hủy.
type cancelAfter struct {
	data   []byte
	cancel context.CancelFunc
	done   bool
}

func (c *cancelAfter) Read(p []byte) (int, error) {
	if !c.done {
		c.done = true
		n := copy(p, c.data[:10])
		c.cancel()
		return n, nil
	}
	return 0, io.EOF // nếu Put bỏ qua ctx đã hủy thì sẽ thành công và test thấy
}

func read(t testing.TB, s binarystore.Store, key string) []byte {
	t.Helper()
	rc, err := s.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open(%s): %v", key, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustKey(t testing.TB, id, ver string) string {
	t.Helper()
	k, err := binarystore.Key(id, ver)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Run chạy toàn bộ hợp đồng.
func Run(t *testing.T, mk New) {
	ctx := context.Background()
	sub := func(name string, f func(t *testing.T, s binarystore.Store)) {
		t.Run(name, func(t *testing.T) { f(t, mk(t)) })
	}

	sub("PutOpenRoundTrip", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		n, err := s.Put(ctx, k, bytes.NewReader([]byte("Hello World!")), binarystore.PutOptions{ContentType: "text/plain"})
		if err != nil || n != 12 {
			t.Fatalf("Put = %d, %v", n, err)
		}
		if got := read(t, s, k); string(got) != "Hello World!" {
			t.Fatalf("got %q", got)
		}
	})

	sub("EmptyContent", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		if n, err := s.Put(ctx, k, bytes.NewReader(nil), binarystore.PutOptions{}); err != nil || n != 0 {
			t.Fatalf("Put = %d, %v", n, err)
		}
		if got := read(t, s, k); len(got) != 0 {
			t.Fatalf("got %q", got)
		}
	})

	sub("OverwriteReplacesWholly", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("a long first content")), binarystore.PutOptions{})
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("short")), binarystore.PutOptions{})
		if got := read(t, s, k); string(got) != "short" {
			t.Fatalf("got %q", got)
		}
	})

	sub("VersionsAreSeparateObjects", func(t *testing.T, s binarystore.Store) {
		_, _ = s.Put(ctx, mustKey(t, "b1", "v1"), bytes.NewReader([]byte("one")), binarystore.PutOptions{})
		_, _ = s.Put(ctx, mustKey(t, "b1", "v2"), bytes.NewReader([]byte("two")), binarystore.PutOptions{})
		if string(read(t, s, mustKey(t, "b1", "v1"))) != "one" || string(read(t, s, mustKey(t, "b1", "v2"))) != "two" {
			t.Fatal("phiên bản lẫn nhau")
		}
	})

	sub("OpenMissingIsNotFound", func(t *testing.T, s binarystore.Store) {
		if _, err := s.Open(ctx, mustKey(t, "none", "v1")); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		// id có nhưng version khác
		_, _ = s.Put(ctx, mustKey(t, "b1", "v1"), bytes.NewReader([]byte("x")), binarystore.PutOptions{})
		if _, err := s.Open(ctx, mustKey(t, "b1", "v2")); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	sub("InvalidKeysAreRejectedEverywhere", func(t *testing.T, s binarystore.Store) {
		good := mustKey(t, "b1", "v1")
		_, _ = s.Put(ctx, good, bytes.NewReader([]byte("x")), binarystore.PutOptions{})
		for _, k := range []string{
			"", "binary", "binary/", "binary/b1", "binary/b1/", "binary//v1", "binary/b1/v1/extra", "other/b1/v1", "/binary/b1/v1",
			"binary/../v1", "binary/b1/..", "binary/./v1", "binary/b1/.", "../binary/b1/v1", "binary/b1\\x/v1", "binary/b1/v1\\x",
			"binary/b%2e/v1", "binary/b1/v\x001", "binary/b 1/v1", "binary/b1/v1 ", "binary/é/v1",
		} {
			if _, err := s.Put(ctx, k, bytes.NewReader([]byte("x")), binarystore.PutOptions{}); !errors.Is(err, binarystore.ErrInvalidKey) {
				t.Errorf("Put(%q) = %v", k, err)
			}
			if _, err := s.Open(ctx, k); !errors.Is(err, binarystore.ErrInvalidKey) {
				t.Errorf("Open(%q) = %v", k, err)
			}
			if err := s.Delete(ctx, k); !errors.Is(err, binarystore.ErrInvalidKey) {
				t.Errorf("Delete(%q) = %v", k, err)
			}
			if err := s.Copy(ctx, good, k); !errors.Is(err, binarystore.ErrInvalidKey) {
				t.Errorf("Copy(dst=%q) = %v", k, err)
			}
			if err := s.Copy(ctx, k, good); !errors.Is(err, binarystore.ErrInvalidKey) {
				t.Errorf("Copy(src=%q) = %v", k, err)
			}
		}
		if string(read(t, s, good)) != "x" {
			t.Fatal("khóa hợp lệ bị đổi")
		}
	})

	sub("MaxBytes", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		if n, err := s.Put(ctx, k, bytes.NewReader(make([]byte, 10)), binarystore.PutOptions{MaxBytes: 10}); err != nil || n != 10 {
			t.Fatalf("đúng trần: %d, %v", n, err)
		}
		_, err := s.Put(ctx, k, bytes.NewReader(make([]byte, 11)), binarystore.PutOptions{MaxBytes: 10})
		if !errors.Is(err, binarystore.ErrTooLarge) {
			t.Fatalf("vượt trần: %v", err)
		}
		if got := read(t, s, k); len(got) != 10 {
			t.Fatalf("nội dung cũ bị đổi: %d byte", len(got))
		}
		k2 := mustKey(t, "b2", "v1")
		if _, err := s.Put(ctx, k2, bytes.NewReader(make([]byte, 100)), binarystore.PutOptions{MaxBytes: 10}); !errors.Is(err, binarystore.ErrTooLarge) {
			t.Fatal(err)
		}
		if _, err := s.Open(ctx, k2); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatalf("khóa mới còn lại sau ErrTooLarge: %v", err)
		}
	})

	sub("BlockedContentType", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		for _, ct := range []string{"application/x-sh", "Application/X-SH", "application/x-sh; charset=utf-8", " application/java-archive "} {
			if _, err := s.Put(ctx, k, bytes.NewReader([]byte("x")), binarystore.PutOptions{ContentType: ct}); !errors.Is(err, binarystore.ErrBlockedContentType) {
				t.Errorf("%q: %v", ct, err)
			}
		}
		if _, err := s.Open(ctx, k); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatalf("loại bị chặn vẫn ghi: %v", err)
		}
		if _, err := s.Put(ctx, k, bytes.NewReader([]byte("x")), binarystore.PutOptions{ContentType: "application/dicom"}); err != nil {
			t.Fatal(err)
		}
	})

	sub("FailedPutKeepsOldContent", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("old")), binarystore.PutOptions{})
		boom := errors.New("boom")
		_, err := s.Put(ctx, k, &failingReader{data: []byte("partial new"), err: boom}, binarystore.PutOptions{})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if got := read(t, s, k); string(got) != "old" {
			t.Fatalf("thấy nội dung dở: %q", got)
		}
		k2 := mustKey(t, "b2", "v1")
		if _, err := s.Put(ctx, k2, &failingReader{data: []byte("partial"), err: boom}, binarystore.PutOptions{}); !errors.Is(err, boom) {
			t.Fatal(err)
		}
		if _, err := s.Open(ctx, k2); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatalf("khóa mới còn lại sau lỗi: %v", err)
		}
	})

	sub("CanceledContext", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("old")), binarystore.PutOptions{})
		c, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.Put(c, k, bytes.NewReader([]byte("new")), binarystore.PutOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Put: %v", err)
		}
		if _, err := s.Open(c, k); !errors.Is(err, context.Canceled) {
			t.Fatalf("Open: %v", err)
		}
		if got := read(t, s, k); string(got) != "old" {
			t.Fatalf("got %q", got)
		}
	})

	sub("ContextCanceledMidStream", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("old")), binarystore.PutOptions{})
		c, cancel := context.WithCancel(ctx)
		r := &cancelAfter{data: bytes.Repeat([]byte("n"), 1000), cancel: cancel}
		if _, err := s.Put(c, k, r, binarystore.PutOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Put: %v", err)
		}
		if got := read(t, s, k); string(got) != "old" {
			t.Fatalf("got %q", got)
		}
	})

	sub("Copy", func(t *testing.T, s binarystore.Store) {
		src, dst := mustKey(t, "b1", "v1"), mustKey(t, "b1", "v2")
		_, _ = s.Put(ctx, src, bytes.NewReader([]byte("content")), binarystore.PutOptions{})
		_, _ = s.Put(ctx, dst, bytes.NewReader([]byte("will be replaced by copy")), binarystore.PutOptions{})
		if err := s.Copy(ctx, src, dst); err != nil {
			t.Fatal(err)
		}
		if string(read(t, s, dst)) != "content" || string(read(t, s, src)) != "content" {
			t.Fatal("chép sai")
		}
		if err := s.Copy(ctx, mustKey(t, "none", "v1"), mustKey(t, "x", "v1")); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatalf("thiếu nguồn: %v", err)
		}
		if _, err := s.Open(ctx, mustKey(t, "x", "v1")); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatal("đích được tạo dù nguồn thiếu")
		}
		// chép lên chính nó
		if err := s.Copy(ctx, src, src); err != nil || string(read(t, s, src)) != "content" {
			t.Fatalf("tự chép: %v", err)
		}
	})

	sub("DeleteIsIdempotent", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		if err := s.Delete(ctx, k); err != nil {
			t.Fatalf("xóa khóa không có: %v", err)
		}
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("x")), binarystore.PutOptions{})
		_, _ = s.Put(ctx, mustKey(t, "b1", "v2"), bytes.NewReader([]byte("y")), binarystore.PutOptions{})
		if err := s.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, k); err != nil {
			t.Fatalf("xóa lần hai: %v", err)
		}
		if _, err := s.Open(ctx, k); !errors.Is(err, binarystore.ErrNotFound) {
			t.Fatal(err)
		}
		if string(read(t, s, mustKey(t, "b1", "v2"))) != "y" {
			t.Fatal("xóa nhầm phiên bản khác")
		}
		// ghi lại sau khi xóa
		if _, err := s.Put(ctx, k, bytes.NewReader([]byte("again")), binarystore.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	})

	sub("OpenedReaderSurvivesOverwriteAndDelete", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("first")), binarystore.PutOptions{})
		rc, err := s.Open(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		_, _ = s.Put(ctx, k, bytes.NewReader([]byte("second!")), binarystore.PutOptions{})
		_ = s.Delete(ctx, k)
		b, err := io.ReadAll(rc)
		if err != nil || string(b) != "first" {
			t.Fatalf("got %q, %v", b, err)
		}
	})

	sub("ReaderNeverSeesPartialContent", func(t *testing.T, s binarystore.Store) {
		k := mustKey(t, "b1", "v1")
		a, b := bytes.Repeat([]byte("A"), 300_000), bytes.Repeat([]byte("B"), 100_000)
		_, _ = s.Put(ctx, k, bytes.NewReader(a), binarystore.PutOptions{})
		var wg sync.WaitGroup
		stop := make(chan struct{})
		errs := make(chan string, 8)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 30; j++ {
					for _, c := range [][]byte{a, b} {
						if _, err := s.Put(ctx, k, bytes.NewReader(c), binarystore.PutOptions{}); err != nil {
							errs <- "Put: " + err.Error()
							return
						}
					}
				}
			}()
		}
		var rd sync.WaitGroup
		for i := 0; i < 3; i++ {
			rd.Add(1)
			go func() {
				defer rd.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					rc, err := s.Open(ctx, k)
					if err != nil {
						errs <- "Open: " + err.Error()
						return
					}
					got, err := io.ReadAll(rc)
					rc.Close()
					if err != nil || !(bytes.Equal(got, a) || bytes.Equal(got, b)) {
						errs <- "đọc thấy nội dung dở/lẫn"
						return
					}
				}
			}()
		}
		wg.Wait()
		close(stop)
		rd.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
	})
}

// RunLister chạy hợp đồng của binarystore.Lister cho adapter có cài đặt.
func RunLister(t *testing.T, mk New) {
	ctx := context.Background()
	list := func(t *testing.T, s binarystore.Store) map[string]binarystore.Entry {
		t.Helper()
		l, ok := s.(binarystore.Lister)
		if !ok {
			t.Fatal("adapter không cài binarystore.Lister")
		}
		out := map[string]binarystore.Entry{}
		if err := l.List(ctx, func(e binarystore.Entry) error {
			if _, dup := out[e.Key]; dup {
				t.Errorf("khóa trùng: %s", e.Key)
			}
			out[e.Key] = e
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	t.Run("ListEmpty", func(t *testing.T) {
		if got := list(t, mk(t)); len(got) != 0 {
			t.Fatalf("kho rỗng: %v", got)
		}
	})
	t.Run("ListReturnsKeysSizesAndRecentTimes", func(t *testing.T) {
		s := mk(t)
		before := time.Now().Add(-time.Minute)
		for id, body := range map[string]string{"a": "xx", "b.c-1": "", "d": "hello"} {
			if _, err := s.Put(ctx, mustKey(t, id, "v1"), strings.NewReader(body), binarystore.PutOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Copy(ctx, mustKey(t, "d", "v1"), mustKey(t, "d", "v2")); err != nil {
			t.Fatal(err)
		}
		got := list(t, s)
		want := map[string]int64{"binary/a/v1": 2, "binary/b.c-1/v1": 0, "binary/d/v1": 5, "binary/d/v2": 5}
		if len(got) != len(want) {
			t.Fatalf("got %v", got)
		}
		for k, n := range want {
			e, ok := got[k]
			if !ok || e.Size != n {
				t.Errorf("%s: %+v (cần size %d)", k, e, n)
			}
			if e.ModTime.Before(before) || e.ModTime.After(time.Now().Add(time.Minute)) {
				t.Errorf("%s: ModTime %v không gần bây giờ", k, e.ModTime)
			}
		}
	})
	t.Run("ListReflectsDelete", func(t *testing.T) {
		s := mk(t)
		k := mustKey(t, "a", "v1")
		_, _ = s.Put(ctx, k, strings.NewReader("x"), binarystore.PutOptions{})
		_ = s.Delete(ctx, k)
		if got := list(t, s); len(got) != 0 {
			t.Fatalf("sau Delete: %v", got)
		}
	})
	t.Run("ListStopsOnCallbackError", func(t *testing.T) {
		s := mk(t)
		for _, v := range []string{"v1", "v2", "v3"} {
			_, _ = s.Put(ctx, mustKey(t, "a", v), strings.NewReader("x"), binarystore.PutOptions{})
		}
		boom := errors.New("boom")
		n := 0
		err := s.(binarystore.Lister).List(ctx, func(binarystore.Entry) error { n++; return boom })
		if !errors.Is(err, boom) || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})
	t.Run("ListHonorsCanceledContext", func(t *testing.T) {
		s := mk(t)
		_, _ = s.Put(ctx, mustKey(t, "a", "v1"), strings.NewReader("x"), binarystore.PutOptions{})
		c, cancel := context.WithCancel(ctx)
		cancel()
		if err := s.(binarystore.Lister).List(c, func(binarystore.Entry) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	})
}

// RunRange chạy hợp đồng của binarystore.RangeOpener; mk phải trả kho cài RangeOpener.
func RunRange(t *testing.T, mk func(testing.TB) binarystore.Store) {
	ctx := context.Background()
	put := func(t *testing.T, s binarystore.Store, id string, size int) (string, []byte) {
		t.Helper()
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i*13 + 1)
		}
		k := mustKey(t, id, "v1")
		if _, err := s.Put(ctx, k, bytes.NewReader(data), binarystore.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		return k, data
	}
	open := func(t *testing.T) (binarystore.Store, binarystore.RangeOpener) {
		t.Helper()
		s := mk(t)
		ro, ok := s.(binarystore.RangeOpener)
		if !ok {
			t.Fatal("kho không cài RangeOpener")
		}
		return s, ro
	}
	readRange := func(t *testing.T, ro binarystore.RangeOpener, k string, off, n int64) ([]byte, error) {
		t.Helper()
		rc, err := ro.OpenRange(ctx, k, off, n)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	t.Run("RangeReturnsExactlyTheRequestedBytes", func(t *testing.T) {
		s, ro := open(t)
		k, data := put(t, s, "r1", 5000)
		for _, c := range [][2]int64{{0, 1}, {0, 5000}, {10, 100}, {4999, 1}, {1234, 2000}, {2048, 1024}} {
			got, err := readRange(t, ro, k, c[0], c[1])
			if err != nil || !bytes.Equal(got, data[c[0]:c[0]+c[1]]) {
				t.Errorf("[%d,+%d): %d byte, %v", c[0], c[1], len(got), err)
			}
		}
	})
	t.Run("RangePastTheEndIsTruncated", func(t *testing.T) {
		s, ro := open(t)
		k, data := put(t, s, "r2", 1000)
		got, err := readRange(t, ro, k, 900, 500)
		if err != nil || !bytes.Equal(got, data[900:]) {
			t.Errorf("%d byte, %v", len(got), err)
		}
	})
	t.Run("RangeRejectsBadRanges", func(t *testing.T) {
		s, ro := open(t)
		k, _ := put(t, s, "r3", 100)
		for _, c := range [][2]int64{{-1, 10}, {0, 0}, {0, -5}, {100, 10}, {5000, 1}} {
			if _, err := ro.OpenRange(ctx, k, c[0], c[1]); !errors.Is(err, binarystore.ErrInvalidRange) {
				t.Errorf("[%d,+%d): %v, muốn ErrInvalidRange", c[0], c[1], err)
			}
		}
	})
	t.Run("RangeMissingAndInvalidKeys", func(t *testing.T) {
		_, ro := open(t)
		if _, err := ro.OpenRange(ctx, mustKey(t, "nope", "v1"), 0, 10); !errors.Is(err, binarystore.ErrNotFound) {
			t.Errorf("khóa không có: %v", err)
		}
		if _, err := ro.OpenRange(ctx, "../etc/passwd", 0, 10); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("khóa sai: %v", err)
		}
		if _, err := ro.OpenRange(ctx, "../etc/passwd", -1, 10); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("khóa sai và khoảng sai: %v", err)
		}
	})
	t.Run("RangeHonorsCanceledContext", func(t *testing.T) {
		s, ro := open(t)
		k, _ := put(t, s, "r4", 100)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := ro.OpenRange(cctx, k, 0, 10); err == nil {
			t.Error("ctx đã hủy phải lỗi")
		}
	})
}
