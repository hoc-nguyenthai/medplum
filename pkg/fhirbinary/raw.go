package fhirbinary

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// RawConfig cấu hình RawBinary.
//   - FHIR: handler FHIR bình thường (đã có xác thực và tách project); mọi yêu cầu không phải Binary thô được chuyển cho nó, và việc tạo/sửa
//     resource Binary cũng đi qua nó để giữ quyền, kiểm tra và Wrap.
//   - Repo: repository của người gọi (đã tách project) để đọc Binary khi GET thô.
//   - Signer (có thể nil): có thì phản hồi POST/PUT thô có thêm `url` ký sẵn (như Medplum).
//   - MaxUploadBytes: trần nội dung SAU giải nén (0: không trần). JSONMaxBytes: cỡ tối đa thân JSON được thử nhận là resource Binary
//     (mặc định fhirrouter.MaxBodyBytes); lớn hơn thì coi là dữ liệu thô.
type RawConfig struct {
	FHIR           http.Handler
	Repo           fhirrouter.Repository
	Store          binarystore.Store
	Signer         *binarystore.Signer
	MaxUploadBytes int64
	JSONMaxBytes   int64
}

// RawBinary xử lý riêng: POST /Binary, PUT /Binary/{id} (tải lên nội dung thô, có thể nén gzip/deflate) và GET /Binary/{id} (trả nội dung
// thô trừ khi Accept là JSON). Đường dẫn là SAU khi bỏ tiền tố (ví dụ "/Binary/abc"). Mọi thứ khác chuyển cho cfg.FHIR.
func RawBinary(c RawConfig) http.Handler {
	if c.JSONMaxBytes <= 0 {
		c.JSONMaxBytes = fhirrouter.MaxBodyBytes
	}
	h := &raw{c}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Trim(r.URL.Path, "/")
		switch {
		case r.Method == http.MethodPost && p == "Binary":
			h.write(w, r, "")
		case r.Method == http.MethodPut && strings.HasPrefix(p, "Binary/") && !strings.Contains(p[len("Binary/"):], "/"):
			h.write(w, r, p[len("Binary/"):])
		case r.Method == http.MethodGet && strings.HasPrefix(p, "Binary/") && !strings.Contains(p[len("Binary/"):], "/") && !wantsResource(r):
			h.read(w, r, p[len("Binary/"):])
		default:
			c.FHIR.ServeHTTP(w, r)
		}
	})
}

type raw struct{ c RawConfig }

// wantsResource: client muốn resource Binary dạng JSON chứ không phải nội dung (Accept bắt đầu bằng JSON, hoặc có _format).
func wantsResource(r *http.Request) bool {
	if r.URL.Query().Get("_format") != "" {
		return true
	}
	a := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	return strings.HasPrefix(a, "application/fhir+json") || strings.HasPrefix(a, "application/json")
}

func (h *raw) read(w http.ResponseWriter, r *http.Request, id string) {
	res, err := h.c.Repo.ReadResource(r.Context(), "Binary", id)
	b, ok := res.(*fhir.Binary)
	if err != nil || !ok {
		h.c.FHIR.ServeHTTP(w, r) // cùng phản hồi lỗi (404, 403, 410...) như đường FHIR
		return
	}
	if !canReadSecurityContext(r.Context(), h.c.Repo, b) {
		writeOutcome(w, fhirrouter.Forbidden("Không có quyền đọc ngữ cảnh bảo mật của Binary"))
		return
	}
	if b.Data != "" { // dữ liệu kiểu cũ (base64 nằm trong CSDL)
		data, derr := base64.StdEncoding.DecodeString(b.Data)
		if derr != nil {
			slog.WarnContext(r.Context(), "Error reading Binary content", "binary", "Binary/"+id, "error", "base64 không hợp lệ")
			writeOutcome(w, fhirrouter.NotFound())
			return
		}
		setDownloadHeaders(w, b.ContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	serveContent(w, r.Context(), h.c.Store, b, versionOf(b))
}

var securityContextRef = regexp.MustCompile(`^[A-Za-z]{1,64}/[A-Za-z0-9\-.]{1,64}$`)

// trackReader ghi lại lỗi đầu tiên của nguồn (phân biệt lỗi do người gửi với lỗi của kho).
type trackReader struct {
	r   io.Reader
	err error
}

func (t *trackReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}

// sourceStatus đổi lỗi đọc thân yêu cầu thành mã HTTP và thông điệp.
func sourceStatus(err error) (int, string) {
	var mbe *http.MaxBytesError
	var corrupt flate.CorruptInputError
	switch {
	case errors.As(err, &mbe), errors.Is(err, binarystore.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "Payload too large"
	case errors.As(err, &corrupt), errors.Is(err, gzip.ErrChecksum), errors.Is(err, gzip.ErrHeader), errors.Is(err, zlib.ErrChecksum),
		errors.Is(err, zlib.ErrHeader), errors.Is(err, io.ErrUnexpectedEOF):
		return http.StatusBadRequest, "Invalid compressed body"
	}
	return http.StatusBadRequest, "Invalid request body"
}

func (h *raw) write(w http.ResponseWriter, r *http.Request, id string) {
	max := h.c.MaxUploadBytes
	if max > 0 && r.ContentLength > max {
		writeOutcome(w, &fhirrouter.Error{Status: http.StatusRequestEntityTooLarge, Outcome: fhirrouter.BadRequest("Payload too large").Outcome})
		return
	}
	var body io.Reader = r.Body
	if max > 0 {
		body = http.MaxBytesReader(w, r.Body, max)
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "":
	case "gzip":
		gr, err := gzip.NewReader(body)
		if err != nil {
			st, msg := sourceStatus(err)
			writeOutcome(w, statusError(st, msg))
			return
		}
		body = gr
	case "deflate":
		zr, err := zlib.NewReader(body)
		if err != nil {
			st, msg := sourceStatus(err)
			writeOutcome(w, statusError(st, msg))
			return
		}
		body = zr
	default:
		writeOutcome(w, fhirrouter.BadRequest("Unsupported content encoding"))
		return
	}
	// Giải nén vượt trần thì lỗi: giới hạn lại phía sau bộ giải nén (bom nén)
	if max > 0 {
		body = &capReader{r: body, left: max}
	}
	contentType := r.Header.Get("Content-Type")
	mt := mediaType(contentType)
	if mt == "application/fhir+json" || mt == "application/json" {
		buf, err := io.ReadAll(io.LimitReader(body, h.c.JSONMaxBytes+1))
		if err != nil {
			st, msg := sourceStatus(err)
			writeOutcome(w, statusError(st, msg))
			return
		}
		if int64(len(buf)) <= h.c.JSONMaxBytes && isBinaryResource(buf, id) {
			h.forward(w, r, buf)
			return
		}
		body = io.MultiReader(bytes.NewReader(buf), body)
	}
	h.upload(w, r, id, contentType, body)
}

func statusError(status int, msg string) *fhirrouter.Error {
	return &fhirrouter.Error{Status: status, Outcome: fhirrouter.BadRequest(msg).Outcome}
}

// isBinaryResource: thân JSON là resource Binary (và, với PUT, mang đúng id của đường dẫn); khác thì là dữ liệu thô.
func isBinaryResource(buf []byte, pathID string) bool {
	var probe struct {
		ResourceType string `json:"resourceType"`
		ID           string `json:"id"`
	}
	if json.Unmarshal(buf, &probe) != nil || probe.ResourceType != "Binary" {
		return false
	}
	return pathID == "" || probe.ID == pathID
}

// forward chuyển resource Binary dạng JSON cho handler FHIR (tạo hay cập nhật như mọi resource) và trả nguyên phản hồi.
func (h *raw) forward(w http.ResponseWriter, r *http.Request, body []byte) {
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Type", "application/fhir+json")
	req.Header.Del("Content-Encoding")
	h.c.FHIR.ServeHTTP(w, req)
}

// call gọi handler FHIR bằng thân JSON tự dựng; trả bản ghi phản hồi.
func (h *raw) call(r *http.Request, method, path string, body any) *recorder {
	rd := io.Reader(http.NoBody) // router đọc r.Body nên không được nil
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(r.Context(), method, path, rd)
	req.Header.Set("Content-Type", "application/fhir+json")
	req.Header.Set("Accept", "application/fhir+json")
	if v := r.Header.Get("If-Match"); v != "" && method == http.MethodPut {
		req.Header.Set("If-Match", v)
	}
	rec := newRecorder()
	h.c.FHIR.ServeHTTP(rec, req)
	return rec
}

func (h *raw) upload(w http.ResponseWriter, r *http.Request, id, contentType string, body io.Reader) {
	filename := r.URL.Query().Get("_filename")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Kiểm tên/loại TRƯỚC khi tạo resource để không để lại Binary mồ côi.
	if err := binarystore.CheckMetadata(filename, contentType); err != nil {
		writeOutcome(w, fhirrouter.BadRequest(err.Error()))
		return
	}
	res := map[string]any{"resourceType": "Binary", "contentType": contentType}
	if sc := r.Header.Get("X-Security-Context"); sc != "" {
		if !securityContextRef.MatchString(sc) {
			writeOutcome(w, fhirrouter.BadRequest("Invalid X-Security-Context"))
			return
		}
		res["securityContext"] = map[string]any{"reference": sc}
	}
	method, path := http.MethodPost, "/Binary"
	if id != "" {
		method, path = http.MethodPut, "/Binary/"+id
		res["id"] = id
	}
	rec := h.call(r, method, path, res)
	if !rec.ok() {
		rec.relay(w)
		return
	}
	var out map[string]any
	if json.Unmarshal(rec.body.Bytes(), &out) != nil {
		writeOutcome(w, fhirrouter.Internal("Internal Server Error"))
		return
	}
	newID, _ := out["id"].(string)
	meta, _ := out["meta"].(map[string]any)
	ver, _ := meta["versionId"].(string)
	project, _ := meta["project"].(string)
	key, err := binarystore.Key(newID, ver)
	if err != nil {
		h.abort(r, method, newID)
		writeOutcome(w, fhirrouter.Internal("Failed to store binary content"))
		return
	}
	src := &trackReader{r: body}
	if _, err := h.c.Store.Put(r.Context(), key, src, binarystore.PutOptions{ContentType: contentType, MaxBytes: h.c.MaxUploadBytes}); err != nil {
		h.abort(r, method, newID)
		if src.err != nil || errors.Is(err, binarystore.ErrTooLarge) {
			cause := src.err
			if cause == nil {
				cause = err
			}
			st, msg := sourceStatus(cause)
			writeOutcome(w, statusError(st, msg))
			return
		}
		slog.ErrorContext(r.Context(), "fhirbinary: ghi nội dung lỗi", "binary", "Binary/"+newID, "error", err)
		writeOutcome(w, fhirrouter.Internal("Failed to store binary content"))
		return
	}
	if h.c.Signer != nil {
		if u, err := h.c.Signer.Sign(http.MethodGet, newID, ver, project); err == nil {
			out["url"] = u
		}
	}
	for _, k := range []string{"Location", "ETag", "Last-Modified"} {
		if v := rec.h.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Content-Type", fhirJSON)
	w.WriteHeader(rec.code)
	_ = json.NewEncoder(w).Encode(out)
}

// abort bù trừ khi ghi nội dung lỗi: POST thì xóa resource vừa tạo; PUT thì giữ phiên bản mới (đã mang nội dung chép từ bản trước).
func (h *raw) abort(r *http.Request, method, id string) {
	if method != http.MethodPost || id == "" {
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodDelete, "/Binary/"+id, http.NoBody)
	rec := newRecorder()
	h.c.FHIR.ServeHTTP(rec, req)
	if !rec.ok() {
		slog.ErrorContext(r.Context(), "fhirbinary: không xóa được Binary sau lỗi ghi nội dung", "binary", "Binary/"+id, "status", rec.code)
	}
}

// capReader trả lỗi *http.MaxBytesError khi đã đọc quá trần (dùng sau bộ giải nén để chặn bom nén).
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, &http.MaxBytesError{}
	}
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, &http.MaxBytesError{}
	}
	return n, err
}
