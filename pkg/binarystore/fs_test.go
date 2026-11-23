package binarystore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"trivita-fhir/pkg/binarystore"
)

func newFS(t *testing.T) (*binarystore.FS, string) {
	t.Helper()
	dir := t.TempDir()
	fs, err := binarystore.NewFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs, dir
}

func TestFSLayoutMatchesMedplum(t *testing.T) {
	fs, dir := newFS(t)
	k, _ := binarystore.Key("abc", "v1")
	if _, err := fs.Put(context.Background(), k, strings.NewReader("data"), binarystore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "abc", "v1")); err != nil || string(b) != "data" {
		t.Fatalf("bố cục <dir>/<id>/<ver>: %q, %v", b, err)
	}
	// dữ liệu có sẵn từ Medplum đọc được
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old", "7"), []byte("legacy"), 0o640); err != nil {
		t.Fatal(err)
	}
	rc, err := fs.Open(context.Background(), "binary/old/7")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "legacy" {
		t.Fatalf("got %q", b)
	}
}

func TestFSPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	fs, dir := newFS(t)
	k, _ := binarystore.Key("abc", "v1")
	_, _ = fs.Put(context.Background(), k, strings.NewReader("data"), binarystore.PutOptions{})
	if st, _ := os.Stat(filepath.Join(dir, "abc", "v1")); st.Mode().Perm() != 0o640 {
		t.Errorf("quyền tệp %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Join(dir, "abc")); st.Mode().Perm() != 0o750 {
		t.Errorf("quyền thư mục %v", st.Mode().Perm())
	}
}

func TestFSNoTempLeftovers(t *testing.T) {
	fs, dir := newFS(t)
	ctx := context.Background()
	k, _ := binarystore.Key("abc", "v1")
	boom := errors.New("boom")
	_, _ = fs.Put(ctx, k, io.MultiReader(strings.NewReader("x"), errReader{boom}), binarystore.PutOptions{})
	_, _ = fs.Put(ctx, k, bytes.NewReader(make([]byte, 100)), binarystore.PutOptions{MaxBytes: 5})
	_, _ = fs.Put(ctx, k, strings.NewReader("ok"), binarystore.PutOptions{})
	_ = fs.Copy(ctx, k, "binary/abc/v2")
	_ = fs.Copy(ctx, "binary/none/v1", "binary/abc/v3")
	// đích là thư mục: rename lỗi, tệp tạm vẫn phải được dọn
	if err := os.MkdirAll(filepath.Join(dir, "d1", "v1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Put(ctx, "binary/d1/v1", strings.NewReader("x"), binarystore.PutOptions{}); err == nil {
		t.Fatal("ghi lên thư mục mà không lỗi")
	}
	left, _ := os.ReadDir(filepath.Join(dir, "_tmp"))
	if len(left) != 0 {
		t.Fatalf("còn %d tệp tạm", len(left))
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestFSSymlinkCannotEscapeRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	fs, dir := newFS(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("TOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	// thư mục id là symlink ra ngoài; tệp version là symlink ra ngoài
	if err := os.Symlink(outside, filepath.Join(dir, "evil")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "linkfile"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "linkfile", "v1")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, k := range []string{"binary/evil/secret", "binary/linkfile/v1"} {
		if rc, err := fs.Open(ctx, k); err == nil {
			b, _ := io.ReadAll(rc)
			rc.Close()
			t.Errorf("Open(%s) đọc được %q ngoài gốc", k, b)
		}
	}
	if _, err := fs.Put(ctx, "binary/evil/pwned", strings.NewReader("x"), binarystore.PutOptions{}); err == nil {
		t.Error("Put qua symlink thoát gốc")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); err == nil {
		t.Error("tệp được tạo ngoài gốc")
	}
	_ = fs.Delete(ctx, "binary/evil/secret")
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Error("Delete xóa tệp ngoài gốc")
	}
}

func TestFSTempDirIsNotAKey(t *testing.T) {
	fs, _ := newFS(t)
	ctx := context.Background()
	if _, err := fs.Put(ctx, "binary/_tmp/x", strings.NewReader("x"), binarystore.PutOptions{}); !errors.Is(err, binarystore.ErrInvalidKey) {
		t.Fatalf("khóa vào thư mục tạm: %v", err)
	}
}

func TestFSDirectoryAtKeyIsNotFound(t *testing.T) {
	fs, dir := newFS(t)
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Open(context.Background(), "binary/a/b"); !errors.Is(err, binarystore.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestFSOpenPermissionErrorIsNotNotFound(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip()
	}
	fs, dir := newFS(t)
	k, _ := binarystore.Key("abc", "v1")
	_, _ = fs.Put(context.Background(), k, strings.NewReader("x"), binarystore.PutOptions{})
	if err := os.Chmod(filepath.Join(dir, "abc", "v1"), 0); err != nil {
		t.Fatal(err)
	}
	_, err := fs.Open(context.Background(), k)
	if err == nil || errors.Is(err, binarystore.ErrNotFound) {
		t.Fatalf("lỗi quyền bị gộp thành not found: %v", err)
	}
}

func TestNewFSCreatesDirAndRejectsFile(t *testing.T) {
	base := t.TempDir()
	if _, err := binarystore.NewFS(filepath.Join(base, "x", "y")); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(base, "file")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	if _, err := binarystore.NewFS(f); err == nil {
		t.Fatal("thư mục gốc là tệp mà không lỗi")
	}
}
