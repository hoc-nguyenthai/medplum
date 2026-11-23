package binarystore_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"trivita-fhir/pkg/binarystore"
)

// S5: cách DICOM dùng kho. Luồng tải lên được tách đôi bằng io.TeeReader: một nhánh ghi thẳng vào kho (không nạp cả tệp vào bộ nhớ),
// nhánh kia giữ lại phần đầu để đọc tiêu đề; sau đó đọc lại 100 KB đầu bằng Open + io.LimitReader.
func TestDICOMUsagePattern(t *testing.T) {
	for name, store := range map[string]binarystore.Store{"memory": binarystore.NewMemory(), "fs": func() binarystore.Store {
		fs, err := binarystore.NewFS(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fs.Close() })
		return fs
	}()} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			instance := append(bytes.Repeat([]byte{0}, 128), []byte("DICM")...)
			instance = append(instance, bytes.Repeat([]byte("p"), 300_000)...) // giả lập PixelData lớn
			part := bytes.NewReader(instance)

			var head bytes.Buffer
			key, _ := binarystore.Key("instance-1", "1")
			n, err := store.Put(ctx, key, io.TeeReader(part, &limitedWriter{&head, 1024}), binarystore.PutOptions{ContentType: "application/dicom", MaxBytes: 1 << 30})
			if err != nil || n != int64(len(instance)) {
				t.Fatalf("Put = %d, %v", n, err)
			}
			if string(head.Bytes()[128:132]) != "DICM" {
				t.Fatal("không đọc được tiền tố DICM từ nhánh tách")
			}
			rc, err := store.Open(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			first, _ := io.ReadAll(io.LimitReader(rc, 100*1024))
			if len(first) != 100*1024 || !bytes.Equal(first[:132], instance[:132]) {
				t.Fatalf("đọc 100 KB đầu: %d byte", len(first))
			}
			if err := store.Delete(ctx, key); err != nil { // Binary bị xóa thì xóa nội dung
				t.Fatal(err)
			}
			if _, err := store.Open(ctx, key); err == nil {
				t.Fatal("còn sau khi xóa")
			}
		})
	}
}

type limitedWriter struct {
	b *bytes.Buffer
	n int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.n - w.b.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		w.b.Write(p[:room])
	}
	return len(p), nil
}
