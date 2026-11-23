// Package fhirbinary nối binarystore với FHIR: decorator Repository chuyển nội dung Binary ra kho, handler `/storage` (URL ký) và
// handler Binary thô (POST/PUT/GET nội dung không bọc JSON) của fhir-server.
package fhirbinary

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// DefaultBase64MaxBytes là trần `data` base64 (đã giải mã) khi ghi Binary: 1 MiB như Medplum.
const DefaultBase64MaxBytes = 1 << 20

// WrapOptions cấu hình Wrap. Base64MaxBytes: 0 là DefaultBase64MaxBytes, âm là không trần.
type WrapOptions struct {
	Base64MaxBytes int64
	// Rewrite (tùy chọn): khi ghi resource khác Binary, đổi mọi URL Binary thành `Binary/<id>` (xem Rewriter.Write).
	Rewrite *Rewriter
}

// Repo là decorator của fhirrouter.Repository: Binary có `data` được giải mã, ghi vào kho khóa binary/<id>/<versionId> và `data` bị
// bỏ khỏi resource lưu trong CSDL; cập nhật không có `data` thì chép nội dung của phiên bản trước sang phiên bản mới; Expunge xóa
// nội dung mọi phiên bản (sau commit, lỗi chỉ ghi log). Resource khác đi thẳng xuống repository bên trong.
//
// Thứ tự ghi (Q-S9): CSDL trước, kho sau (vì versionId do CSDL cấp). Lỗi kho thì bù trừ: Create xóa resource vừa tạo; trong
// transaction thì hủy cả transaction và xóa các khóa đã ghi. Nội dung mồ côi (tiến trình chết giữa hai bước) cần công cụ quét sau.
type Repo struct {
	fhirrouter.Repository
	store binarystore.Store
	opt   WrapOptions
	tx    *txState
}

type txState struct {
	mu       sync.Mutex
	written  []string // xóa nếu transaction hủy
	expunged []string // xóa sau khi commit
}

// Wrap bọc repository; xem Repo.
func Wrap(inner fhirrouter.Repository, store binarystore.Store, opt WrapOptions) *Repo {
	if opt.Base64MaxBytes == 0 {
		opt.Base64MaxBytes = DefaultBase64MaxBytes
	}
	return &Repo{Repository: inner, store: store, opt: opt}
}

var (
	_ fhirrouter.Repository    = (*Repo)(nil)
	_ fhirrouter.Transactional = (*Repo)(nil)
	_ fhirrouter.KeyLocker     = (*Repo)(nil)
	_ fhirrouter.Reindexer     = (*Repo)(nil)
	_ fhirrouter.Expunger      = (*Repo)(nil)
)

// decode trả nội dung đã giải mã của Binary (has=false nếu không có `data`). Lỗi là *fhirrouter.Error 400.
func (w *Repo) decode(b *fhir.Binary) (content []byte, has bool, err error) {
	if b.Data == "" {
		return nil, false, nil
	}
	if max := w.opt.Base64MaxBytes; max > 0 && int64(base64.StdEncoding.DecodedLen(len(b.Data))) > max+2 { // +2: đệm '='
		return nil, false, fhirrouter.BadRequest(fmt.Sprintf("base64Binary exceeds %d bytes", max))
	}
	content, err = base64.StdEncoding.DecodeString(b.Data)
	if err != nil {
		return nil, false, fhirrouter.BadRequest("Invalid base64 data")
	}
	if max := w.opt.Base64MaxBytes; max > 0 && int64(len(content)) > max {
		return nil, false, fhirrouter.BadRequest(fmt.Sprintf("base64Binary exceeds %d bytes", max))
	}
	if err := binarystore.CheckMetadata("", b.ContentType); err != nil {
		return nil, false, fhirrouter.BadRequest(err.Error())
	}
	return content, true, nil
}

func versionOf(r fhir.Resource) string {
	if m := r.GetMeta(); m != nil {
		return m.VersionId
	}
	return ""
}

func (w *Repo) rewriteWrite(r fhir.Resource) fhir.Resource {
	if w.opt.Rewrite == nil {
		return r
	}
	return w.opt.Rewrite.Write(r)
}

func (w *Repo) put(ctx context.Context, id, ver string, content []byte) error {
	key, err := binarystore.Key(id, ver)
	if err != nil {
		return err
	}
	if _, err := w.store.Put(ctx, key, bytesReader(content), binarystore.PutOptions{ContentType: ""}); err != nil {
		return err
	}
	w.noteWritten(key)
	return nil
}

func (w *Repo) noteWritten(key string) {
	if w.tx != nil {
		w.tx.mu.Lock()
		w.tx.written = append(w.tx.written, key)
		w.tx.mu.Unlock()
	}
}

func storeFailed(ctx context.Context, op, id string, err error) *fhirrouter.Error {
	slog.ErrorContext(ctx, "fhirbinary: lỗi kho nội dung", "op", op, "binary", "Binary/"+id, "error", err)
	return fhirrouter.Internal("Failed to store binary content")
}

func (w *Repo) CreateResource(ctx context.Context, r fhir.Resource) (fhir.Resource, error) {
	b, ok := r.(*fhir.Binary)
	if !ok {
		return w.Repository.CreateResource(ctx, w.rewriteWrite(r))
	}
	content, has, err := w.decode(b)
	if err != nil {
		return nil, err
	}
	cp := *b
	cp.Data, cp.DataExt = "", nil
	out, err := w.Repository.CreateResource(ctx, &cp)
	if err != nil || !has {
		return out, err
	}
	if perr := w.put(ctx, out.GetID(), versionOf(out), content); perr != nil {
		if w.tx == nil { // ngoài transaction: bù trừ bằng cách xóa resource vừa tạo
			if derr := w.Repository.DeleteResource(context.WithoutCancel(ctx), "Binary", out.GetID()); derr != nil {
				slog.ErrorContext(ctx, "fhirbinary: không xóa được Binary sau lỗi kho", "binary", "Binary/"+out.GetID(), "error", derr)
			}
		}
		return nil, storeFailed(ctx, "create", out.GetID(), perr)
	}
	return out, nil
}

func (w *Repo) UpdateResource(ctx context.Context, r fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	b, ok := r.(*fhir.Binary)
	if !ok {
		return w.Repository.UpdateResource(ctx, w.rewriteWrite(r), o)
	}
	content, has, err := w.decode(b)
	if err != nil {
		return nil, err
	}
	prevVer := ""
	if !has {
		if prev, err := w.Repository.ReadResource(ctx, "Binary", b.ID); err == nil {
			if pb, ok := prev.(*fhir.Binary); ok && pb.Data == "" { // bản cũ kiểu cũ (data trong CSDL) thì không có gì để chép
				prevVer = versionOf(prev)
			}
		}
	}
	cp := *b
	cp.Data, cp.DataExt = "", nil
	out, err := w.Repository.UpdateResource(ctx, &cp, o)
	if err != nil {
		return nil, err
	}
	switch {
	case has:
		if perr := w.put(ctx, out.GetID(), versionOf(out), content); perr != nil {
			return nil, storeFailed(ctx, "update", out.GetID(), perr)
		}
	case prevVer != "":
		src, e1 := binarystore.Key(out.GetID(), prevVer)
		dst, e2 := binarystore.Key(out.GetID(), versionOf(out))
		if e1 != nil || e2 != nil {
			return nil, storeFailed(ctx, "copy", out.GetID(), errors.Join(e1, e2))
		}
		switch cerr := w.store.Copy(ctx, src, dst); {
		case cerr == nil:
			w.noteWritten(dst)
		case errors.Is(cerr, binarystore.ErrNotFound): // phiên bản trước chưa từng có nội dung: bỏ qua, không làm hỏng cập nhật metadata
		default:
			return nil, storeFailed(ctx, "copy", out.GetID(), cerr)
		}
	}
	return out, nil
}

// Expunge xóa vĩnh viễn rồi (sau commit) xóa nội dung của mọi phiên bản.
func (w *Repo) Expunge(ctx context.Context, rt, id string) error {
	ex, ok := w.Repository.(fhirrouter.Expunger)
	if !ok {
		return fhirrouter.NotImplemented("Repository không hỗ trợ expunge")
	}
	var keys []string
	if rt == "Binary" {
		if hist, err := w.Repository.ReadHistory(ctx, rt, id); err == nil {
			for _, h := range hist {
				if k, err := binarystore.Key(id, h.VersionID); err == nil {
					keys = append(keys, k)
				}
			}
		}
	}
	if err := ex.Expunge(ctx, rt, id); err != nil {
		return err
	}
	if w.tx != nil {
		w.tx.mu.Lock()
		w.tx.expunged = append(w.tx.expunged, keys...)
		w.tx.mu.Unlock()
		return nil
	}
	w.deleteKeys(ctx, keys)
	return nil
}

// deleteKeys xóa nội dung, lỗi chỉ ghi log (không làm hỏng thao tác đã commit).
func (w *Repo) deleteKeys(ctx context.Context, keys []string) {
	ctx = context.WithoutCancel(ctx)
	for _, k := range keys {
		if err := w.store.Delete(ctx, k); err != nil {
			slog.WarnContext(ctx, "fhirbinary: không xóa được nội dung", "key", k, "error", err)
		}
	}
}

// RunInTransaction chạy fn trong transaction của repository bên trong. Hủy thì xóa các khóa đã ghi; commit thì xóa nội dung của resource đã expunge.
func (w *Repo) RunInTransaction(ctx context.Context, fn func(fhirrouter.Repository) error) error {
	if w.tx != nil {
		return fn(w)
	}
	t, ok := w.Repository.(fhirrouter.Transactional)
	if !ok {
		return fhirrouter.NotImplemented("Repository không hỗ trợ transaction")
	}
	st := &txState{}
	err := t.RunInTransaction(ctx, func(rp fhirrouter.Repository) error {
		return fn(&Repo{Repository: rp, store: w.store, opt: w.opt, tx: st})
	})
	if err != nil {
		w.deleteKeys(ctx, st.written)
		return err
	}
	w.deleteKeys(ctx, st.expunged)
	return nil
}

// LockKey chuyển cho repository bên trong (xem fhirrouter.KeyLocker).
func (w *Repo) LockKey(ctx context.Context, key string) error {
	if kl, ok := w.Repository.(fhirrouter.KeyLocker); ok {
		return kl.LockKey(ctx, key)
	}
	return nil
}

func (w *Repo) Reindex(ctx context.Context, rt, id string, progress func(int)) (int, error) {
	ri, ok := w.Repository.(fhirrouter.Reindexer)
	if !ok {
		return 0, fhirrouter.NotImplemented("Repository không hỗ trợ reindex")
	}
	return ri.Reindex(ctx, rt, id, progress)
}

// ConceptStore: chuyển cho repository bên trong (xem fhirrouter.ConceptStore).
func (w *Repo) cs() (fhirrouter.ConceptStore, error) {
	cs, ok := w.Repository.(fhirrouter.ConceptStore)
	if !ok {
		return nil, fhirrouter.NotImplemented("Repository không hỗ trợ kho concept")
	}
	return cs, nil
}

func (w *Repo) PutConcepts(ctx context.Context, sid string, c []fhirrouter.Concept) error {
	cs, err := w.cs()
	if err != nil {
		return err
	}
	return cs.PutConcepts(ctx, sid, c)
}

func (w *Repo) PutConceptProperties(ctx context.Context, sid string, p []fhirrouter.ConceptProperty) error {
	cs, err := w.cs()
	if err != nil {
		return err
	}
	return cs.PutConceptProperties(ctx, sid, p)
}

func (w *Repo) PutConceptDesignations(ctx context.Context, sid string, d []fhirrouter.ConceptDesignation) error {
	cs, err := w.cs()
	if err != nil {
		return err
	}
	return cs.PutConceptDesignations(ctx, sid, d)
}

func (w *Repo) GetConcept(ctx context.Context, sid, code string) (*fhirrouter.StoredConcept, error) {
	cs, err := w.cs()
	if err != nil {
		return nil, err
	}
	return cs.GetConcept(ctx, sid, code)
}

func (w *Repo) DeleteConcepts(ctx context.Context, sid string) (int, error) {
	cs, err := w.cs()
	if err != nil {
		return 0, err
	}
	return cs.DeleteConcepts(ctx, sid)
}

var _ fhirrouter.ConceptStore = (*Repo)(nil)
