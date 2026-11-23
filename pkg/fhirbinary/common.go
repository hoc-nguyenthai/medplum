package fhirbinary

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

const fhirJSON = "application/fhir+json; charset=utf-8"

func writeOutcome(w http.ResponseWriter, e *fhirrouter.Error) {
	w.Header().Set("Content-Type", fhirJSON)
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e.Outcome)
}

func writeText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg)
}

// mediaType bỏ tham số và đưa về chữ thường ("Text/Plain; charset=x" thành "text/plain").
func mediaType(ct string) string {
	base, _, _ := strings.Cut(ct, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

// inlineTypes là các loại ảnh an toàn để trình duyệt hiển thị tại chỗ; mọi loại khác (kể cả SVG, HTML, PDF) bị buộc tải xuống
// (Q-S8: SVG chạy được script nên không inline; PDF chưa kiểm với CSP sandbox).
var inlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// serveContent mở nội dung của phiên bản ver và gửi dạng luồng với Content-Type của Binary. Lỗi trước byte đầu là 404 (không phân biệt
// "Binary không có" với "tệp không có" ra ngoài); lỗi sau byte đầu thì hủy kết nối.
func serveContent(w http.ResponseWriter, ctx context.Context, store binarystore.Store, b *fhir.Binary, ver string) {
	key, err := binarystore.Key(b.ID, ver)
	var rc io.ReadCloser
	if err == nil {
		rc, err = store.Open(ctx, key)
	}
	if err != nil {
		slog.WarnContext(ctx, "Error reading Binary content", "binary", "Binary/"+b.ID, "versionId", ver, "error", err.Error())
		writeOutcome(w, fhirrouter.NotFound())
		return
	}
	defer rc.Close()
	setDownloadHeaders(w, b.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, rc); err != nil {
		panic(http.ErrAbortHandler) // đã gửi header: chỉ còn cách đóng kết nối để client biết nội dung cụt
	}
}

func setDownloadHeaders(w http.ResponseWriter, contentType string) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if !inlineTypes[mediaType(contentType)] {
		h.Set("Content-Disposition", "attachment")
	}
}

// recorder ghi lại phản hồi của handler FHIR khi gọi nội bộ.
type recorder struct {
	h    http.Header
	code int
	body bytes.Buffer
}

func newRecorder() *recorder { return &recorder{h: http.Header{}, code: http.StatusOK} }

func (r *recorder) Header() http.Header         { return r.h }
func (r *recorder) WriteHeader(c int)           { r.code = c }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }

func (r *recorder) ok() bool { return r.code >= 200 && r.code < 300 }

// relay chép nguyên phản hồi đã ghi sang w.
func (r *recorder) relay(w http.ResponseWriter) {
	for k, v := range r.h {
		w.Header()[k] = v
	}
	w.WriteHeader(r.code)
	_, _ = w.Write(r.body.Bytes())
}
