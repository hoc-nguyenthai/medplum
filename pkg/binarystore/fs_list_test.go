package binarystore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/binarystore"
)

func TestFSListIgnoresForeignEntries(t *testing.T) {
	dir := t.TempDir()
	fs, err := binarystore.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	ctx := context.Background()
	k, _ := binarystore.Key("ok", "v1")
	if _, err := fs.Put(ctx, k, strings.NewReader("x"), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	// rác: tệp ở gốc, thư mục id sai, tệp tên sai, thư mục lồng, symlink, và tệp tạm
	_ = os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "bad id"), 0o750)
	_ = os.WriteFile(filepath.Join(dir, "bad id", "v1"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "ok", "bad ver"), []byte("x"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "ok", "nested"), 0o750)
	_ = os.Symlink("/etc/passwd", filepath.Join(dir, "ok", "link"))
	_ = os.WriteFile(filepath.Join(dir, "_tmp", "partial"), []byte("x"), 0o600)
	_ = os.Mkdir(filepath.Join(dir, ".."+"x"), 0o750) // "..x" hợp lệ về ký tự nhưng là thư mục rỗng
	var keys []string
	if err := fs.List(ctx, func(e binarystore.Entry) error { keys = append(keys, e.Key); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != k {
		t.Fatalf("keys=%v", keys)
	}
}

func TestFSCleanTemp(t *testing.T) {
	dir := t.TempDir()
	fs, err := binarystore.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	ctx := context.Background()
	if n, err := fs.CleanTemp(ctx, time.Hour); n != 0 || err != nil { // chưa có _tmp
		t.Fatalf("%d %v", n, err)
	}
	k, _ := binarystore.Key("a", "v1")
	if _, err := fs.Put(ctx, k, strings.NewReader("x"), binarystore.PutOptions{}); err != nil { // tạo _tmp
		t.Fatal(err)
	}
	old, fresh := filepath.Join(dir, "_tmp", "old"), filepath.Join(dir, "_tmp", "fresh")
	_ = os.WriteFile(old, []byte("x"), 0o600)
	_ = os.WriteFile(fresh, []byte("x"), 0o600)
	_ = os.Mkdir(filepath.Join(dir, "_tmp", "sub"), 0o750)
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(old, past, past)
	n, err := fs.CleanTemp(ctx, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("tệp cũ phải bị xóa")
	}
	for _, p := range []string{fresh, filepath.Join(dir, "_tmp", "sub"), filepath.Join(dir, "a", "v1")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("không được xóa %s: %v", p, err)
		}
	}
}

func TestMemorySetModTime(t *testing.T) {
	m := binarystore.NewMemory()
	k, _ := binarystore.Key("a", "v1")
	if m.SetModTime(k, time.Now()) {
		t.Error("khóa chưa có")
	}
	_, _ = m.Put(context.Background(), k, strings.NewReader("x"), binarystore.PutOptions{})
	past := time.Now().Add(-72 * time.Hour)
	if !m.SetModTime(k, past) {
		t.Fatal("khóa có")
	}
	_ = m.List(context.Background(), func(e binarystore.Entry) error {
		if !e.ModTime.Equal(past) {
			t.Errorf("ModTime %v", e.ModTime)
		}
		return nil
	})
}
