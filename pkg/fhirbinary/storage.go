package fhirbinary

import (
	"errors"
	"log/slog"
	"net/http"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// StorageConfig cấu hình StorageHandler. Repo phải là repository KHÔNG tách theo project (đường này công khai, quyền do chữ ký);
// không bao giờ đưa repository đã bọc xác thực vào đây.
type StorageConfig struct {
	Signer *binarystore.Signer
	Store  binarystore.Store
	Repo   fhirrouter.Repository
	// MaxUploadBytes là trần thân PUT (0: không trần, chỉ nên dùng khi test).
	MaxUploadBytes int64
}

// StorageHandler phục vụ GET và PUT /storage/{id}/{versionId} bằng URL ký (không có Bearer). Đặt ngoài BodyLimit chung. Thứ tự kiểm tra
// theo Medplum (storage/routes.ts): Signature → Expires → đường dẫn thô → chữ ký → đọc Binary → Project.
//
// Khác Medplum (đã duyệt ở plan): phục vụ đúng phiên bản trong URL; PUT chỉ nhận khi URL trỏ tới phiên bản hiện tại (409 nếu không);
// Binary có project thì URL phải mang đúng Project đó; Content-Type so sau khi bỏ tham số; lỗi kho là 500 (không phải 400).
func StorageHandler(c StorageConfig) http.Handler {
	h := &storage{c}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.get(w, r)
		case http.MethodPut:
			h.put(w, r)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeText(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	})
}

type storage struct{ c StorageConfig }

// authorize xác minh chữ ký và nạp Binary (phiên bản trong URL, hoặc bản hiện tại nếu URL không có). ok=false nghĩa là đã trả lời.
func (s *storage) authorize(w http.ResponseWriter, r *http.Request, method string) (claims binarystore.Claims, b *fhir.Binary, current string, ok bool) {
	claims, err := s.c.Signer.Verify(method, r)
	switch {
	case errors.Is(err, binarystore.ErrMissingSignature):
		writeText(w, http.StatusUnauthorized, "Unauthorized")
	case errors.Is(err, binarystore.ErrExpired):
		writeText(w, http.StatusGone, "URL has expired")
	case err != nil:
		writeText(w, http.StatusUnauthorized, "Invalid signature")
	}
	if err != nil {
		return claims, nil, "", false
	}
	ctx := r.Context()
	cur, err := s.c.Repo.ReadResource(ctx, "Binary", claims.ID)
	if err != nil {
		s.readFailed(w, r, claims.ID, err)
		return claims, nil, "", false
	}
	curBin, isBin := cur.(*fhir.Binary)
	if !isBin {
		writeOutcome(w, fhirrouter.NotFound())
		return claims, nil, "", false
	}
	current = versionOf(curBin)
	b = curBin
	if claims.VersionID != "" && claims.VersionID != current {
		if method == http.MethodPut {
			writeText(w, http.StatusConflict, "Version mismatch")
			return claims, nil, current, false
		}
		old, err := s.c.Repo.ReadVersion(ctx, "Binary", claims.ID, claims.VersionID)
		if err != nil {
			s.readFailed(w, r, claims.ID, err)
			return claims, nil, current, false
		}
		ob, isBin := old.(*fhir.Binary)
		if !isBin {
			writeOutcome(w, fhirrouter.NotFound())
			return claims, nil, current, false
		}
		b = ob
	}
	if projectOf(b) != claims.ProjectID { // Binary thuộc project nào thì URL phải ký đúng project đó (kể cả khi Binary không có project)
		writeText(w, http.StatusUnauthorized, "Invalid signature")
		return claims, nil, current, false
	}
	if b.ID == "" {
		b.ID = claims.ID
	}
	return claims, b, current, true
}

func projectOf(b *fhir.Binary) string {
	if b.Meta != nil {
		return b.Meta.Project
	}
	return ""
}

func (s *storage) readFailed(w http.ResponseWriter, r *http.Request, id string, err error) {
	var fe *fhirrouter.Error
	if errors.As(err, &fe) && (fe.Status == http.StatusNotFound || fe.Status == http.StatusGone) {
		writeOutcome(w, fhirrouter.NotFound()) // đã xóa hay chưa từng có: như nhau ra ngoài
		return
	}
	slog.ErrorContext(r.Context(), "fhirbinary: đọc Binary lỗi", "binary", "Binary/"+id, "error", err)
	writeText(w, http.StatusInternalServerError, "Internal Server Error")
}

func (s *storage) get(w http.ResponseWriter, r *http.Request) {
	claims, b, current, ok := s.authorize(w, r, http.MethodGet)
	if !ok {
		return
	}
	ver := claims.VersionID
	if ver == "" {
		ver = current
	}
	serveContent(w, r.Context(), s.c.Store, b, ver)
}

func (s *storage) put(w http.ResponseWriter, r *http.Request) {
	_, b, current, ok := s.authorize(w, r, http.MethodPut)
	if !ok {
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && mediaType(ct) != mediaType(b.ContentType) {
		writeText(w, http.StatusBadRequest, "Content-Type mismatch")
		return
	}
	if err := binarystore.CheckMetadata("", b.ContentType); err != nil {
		writeText(w, http.StatusBadRequest, err.Error())
		return
	}
	max := s.c.MaxUploadBytes
	if max > 0 && r.ContentLength > max {
		writeText(w, http.StatusRequestEntityTooLarge, "Payload Too Large")
		return
	}
	body := r.Body
	if max > 0 {
		body = http.MaxBytesReader(w, r.Body, max)
	}
	key, err := binarystore.Key(b.ID, current)
	if err != nil {
		writeText(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	if _, err := s.c.Store.Put(r.Context(), key, body, binarystore.PutOptions{MaxBytes: max}); err != nil {
		var mbe *http.MaxBytesError
		if errors.Is(err, binarystore.ErrTooLarge) || errors.As(err, &mbe) {
			writeText(w, http.StatusRequestEntityTooLarge, "Payload Too Large")
			return
		}
		slog.ErrorContext(r.Context(), "fhirbinary: ghi nội dung lỗi", "binary", "Binary/"+b.ID, "error", err)
		writeText(w, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	w.WriteHeader(http.StatusOK)
}
