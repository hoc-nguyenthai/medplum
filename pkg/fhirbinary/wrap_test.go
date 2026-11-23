package fhirbinary_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirbinary"
	"trivita-fhir/pkg/fhirrouter"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func verOf(r fhir.Resource) string { return r.GetMeta().VersionId }

func TestWrapCreateMovesDataToStore(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	out, err := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("Hello World!")})
	if err != nil {
		t.Fatal(err)
	}
	if out.(*fhir.Binary).Data != "" {
		t.Fatal("kết quả trả về còn data")
	}
	if got, ok := e.stored("b1", verOf(out)); !ok || got != "Hello World!" {
		t.Fatalf("kho: %q %v", got, ok)
	}
	rd, _ := e.repo.ReadResource(ctx, "Binary", "b1") // CSDL (repo gốc) không giữ data
	if rd.(*fhir.Binary).Data != "" {
		t.Fatal("CSDL còn giữ data")
	}
}

func TestWrapRejectsBadData(t *testing.T) {
	e := newEnv(t, func(o *envOpts) { o.base64Max = 10 })
	ctx := context.Background()
	for name, b := range map[string]*fhir.Binary{
		"quá trần":        {ID: "a", ContentType: "text/plain", Data: b64("12345678901")},
		"base64 sai":      {ID: "b", ContentType: "text/plain", Data: "!!!not base64"},
		"loại bị chặn":    {ID: "c", ContentType: "application/x-msdownload", Data: b64("x")},
		"loại chặn+param": {ID: "d", ContentType: "application/x-sh; charset=utf-8", Data: b64("x")},
	} {
		_, err := e.wrapped.CreateResource(ctx, b)
		var fe *fhirrouter.Error
		if !errors.As(err, &fe) || fe.Status != 400 {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := e.repo.ReadResource(ctx, "Binary", b.ID); err == nil {
			t.Errorf("%s: resource vẫn được tạo", name)
		}
	}
	if e.store.Len() != 0 {
		t.Fatalf("kho còn %d đối tượng", e.store.Len())
	}
	// đúng trần thì được
	if _, err := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "ok", ContentType: "text/plain", Data: b64("1234567890")}); err != nil {
		t.Fatal(err)
	}
}

func TestWrapBase64DefaultLimitIsOneMiB(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	if _, err := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "ok", ContentType: "text/plain", Data: b64(strings.Repeat("a", 1<<20))}); err != nil {
		t.Fatalf("đúng 1 MiB: %v", err)
	}
	if _, err := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "big", ContentType: "text/plain", Data: b64(strings.Repeat("a", 1<<20+1))}); err == nil {
		t.Fatal("quá 1 MiB mà không lỗi")
	}
	e2 := newEnv(t, func(o *envOpts) { o.base64Max = -1 })
	if _, err := e2.wrapped.CreateResource(ctx, &fhir.Binary{ID: "big", ContentType: "text/plain", Data: b64(strings.Repeat("a", 1<<20+1))}); err != nil {
		t.Fatalf("không trần: %v", err)
	}
}

func TestWrapUpdate(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	v1, _ := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("first")})
	// cập nhật metadata, không data: nội dung được chép sang phiên bản mới
	v2, err := e.wrapped.UpdateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", SecurityContext: &fhir.Reference{Reference: "Patient/1"}}, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e.stored("b1", verOf(v2)); got != "first" {
		t.Fatalf("phiên bản 2 không có nội dung chép: %q", got)
	}
	// cập nhật có data: thay nội dung phiên bản mới; phiên bản cũ giữ nguyên
	v3, err := e.wrapped.UpdateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("third")}, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e.stored("b1", verOf(v3)); got != "third" {
		t.Fatalf("v3: %q", got)
	}
	if got, _ := e.stored("b1", verOf(v1)); got != "first" {
		t.Fatalf("v1 bị đổi: %q", got)
	}
	if got, _ := e.stored("b1", verOf(v2)); got != "first" {
		t.Fatalf("v2 bị đổi: %q", got)
	}
}

func TestWrapUpdateWithoutPreviousContentDoesNotFail(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	if _, err := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain"}); err != nil { // Binary không nội dung
		t.Fatal(err)
	}
	out, err := e.wrapped.UpdateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain"}, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatalf("cập nhật Binary không nội dung phải thành công: %v", err)
	}
	if _, ok := e.stored("b1", verOf(out)); ok {
		t.Fatal("không được bịa nội dung")
	}
}

func TestWrapLegacyDataInDBIsNotCopied(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	// dữ liệu kiểu cũ: ghi thẳng vào repo gốc, data nằm trong CSDL
	if _, err := e.repo.CreateResource(ctx, &fhir.Binary{ID: "old", ContentType: "text/plain", Data: b64("legacy")}); err != nil {
		t.Fatal(err)
	}
	out, err := e.wrapped.UpdateResource(ctx, &fhir.Binary{ID: "old", ContentType: "text/plain"}, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.stored("old", verOf(out)); ok {
		t.Fatal("không có gì để chép")
	}
	if e.store.Len() != 0 {
		t.Fatal("kho phải rỗng")
	}
}

func TestWrapStoreFailureOnCreateRollsBack(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	e.store.failPut.Store(true)
	_, err := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("x")})
	var fe *fhirrouter.Error
	if !errors.As(err, &fe) || fe.Status != 500 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(fe.Error(), "secret") || strings.Contains(fe.Error(), "disk") {
		t.Fatalf("lộ lỗi nội bộ: %v", fe)
	}
	if _, err := e.repo.ReadResource(ctx, "Binary", "b1"); err == nil {
		t.Fatal("resource phải bị xóa sau lỗi kho")
	}
}

func TestWrapStoreFailureOnUpdateAndCopy(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	_, _ = e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("x")})
	e.store.failPut.Store(true)
	if _, err := e.wrapped.UpdateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("y")}, fhirrouter.UpdateOptions{}); err == nil {
		t.Fatal("Put lỗi mà không báo")
	}
	e.store.failPut.Store(false)
	e.store.failCopy.Store(true)
	_, err := e.wrapped.UpdateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain"}, fhirrouter.UpdateOptions{})
	var fe *fhirrouter.Error
	if !errors.As(err, &fe) || fe.Status != 500 || strings.Contains(fe.Error(), "secret") {
		t.Fatalf("Copy lỗi: %v", err)
	}
}

func TestWrapPassesThroughOtherResources(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	if _, err := e.wrapped.CreateResource(ctx, &fhir.Patient{ID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.wrapped.UpdateResource(ctx, &fhir.Patient{ID: "p1"}, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.wrapped.Expunge(ctx, "Patient", "p1"); err != nil {
		t.Fatal(err)
	}
	if e.store.Len() != 0 {
		t.Fatal("kho phải rỗng")
	}
}

func TestWrapExpungeRemovesAllVersionsContent(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	v1, _ := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("one")})
	v2, _ := e.wrapped.UpdateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("two")}, fhirrouter.UpdateOptions{})
	_, _ = e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "other", ContentType: "text/plain", Data: b64("keep")})
	if err := e.wrapped.Expunge(ctx, "Binary", "b1"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{verOf(v1), verOf(v2)} {
		if _, ok := e.stored("b1", v); ok {
			t.Fatalf("nội dung phiên bản %s còn lại", v)
		}
	}
	if e.store.Len() != 1 {
		t.Fatalf("phải còn đúng 1 đối tượng của Binary khác, có %d", e.store.Len())
	}
	// expunge resource không có: lỗi của repo gốc, không động tới kho
	if err := e.wrapped.Expunge(ctx, "Binary", "missing"); err == nil {
		t.Fatal("expunge id lạ phải lỗi")
	}
	if e.store.Len() != 1 {
		t.Fatal("kho bị đổi")
	}
}

func TestWrapTransactionRollbackRemovesWrittenContent(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	boom := errors.New("boom")
	err := e.wrapped.RunInTransaction(ctx, func(tx fhirrouter.Repository) error {
		if _, err := tx.CreateResource(ctx, &fhir.Binary{ID: "t1", ContentType: "text/plain", Data: b64("in tx")}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if e.store.Len() != 0 {
		t.Fatalf("hủy transaction mà kho còn %d đối tượng", e.store.Len())
	}
	if _, err := e.repo.ReadResource(ctx, "Binary", "t1"); err == nil {
		t.Fatal("resource vẫn còn sau khi hủy")
	}
}

func TestWrapTransactionCommitKeepsContent(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	var ver string
	err := e.wrapped.RunInTransaction(ctx, func(tx fhirrouter.Repository) error {
		out, err := tx.CreateResource(ctx, &fhir.Binary{ID: "t1", ContentType: "text/plain", Data: b64("Hello World!")})
		if err == nil {
			ver = verOf(out)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e.stored("t1", ver); got != "Hello World!" {
		t.Fatalf("got %q", got)
	}
}

func TestWrapTransactionExpungeDeletesAfterCommitOnly(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	v, _ := e.wrapped.CreateResource(ctx, &fhir.Binary{ID: "b1", ContentType: "text/plain", Data: b64("x")})
	boom := errors.New("boom")
	_ = e.wrapped.RunInTransaction(ctx, func(tx fhirrouter.Repository) error {
		if err := tx.(fhirrouter.Expunger).Expunge(ctx, "Binary", "b1"); err != nil {
			return err
		}
		return boom
	})
	if _, ok := e.stored("b1", verOf(v)); !ok {
		t.Fatal("transaction hủy mà nội dung đã bị xóa")
	}
	_ = e.wrapped.RunInTransaction(ctx, func(tx fhirrouter.Repository) error {
		return tx.(fhirrouter.Expunger).Expunge(ctx, "Binary", "b1")
	})
	if _, ok := e.stored("b1", verOf(v)); ok {
		t.Fatal("commit xong mà nội dung còn")
	}
}

func TestWrapKeyIsBinaryIDAndVersion(t *testing.T) {
	e := newEnv(t, nil)
	out, _ := e.wrapped.CreateResource(context.Background(), &fhir.Binary{ID: "abc", ContentType: "text/plain", Data: b64("x")})
	k, _ := binarystore.Key("abc", verOf(out))
	if _, err := e.store.Memory.Open(context.Background(), k); err != nil {
		t.Fatalf("khóa %s: %v", k, err)
	}
	_ = fhirbinary.DefaultBase64MaxBytes
}
