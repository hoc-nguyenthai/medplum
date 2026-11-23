package fhirbinary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// RewriteConfig là hai gốc công khai (không có dấu / cuối) để nhận ra URL Binary: gốc API FHIR (ví dụ https://h/fhir/R4) và gốc /storage.
type RewriteConfig struct{ FHIRBaseURL, StorageBaseURL string }

// Rewriter viết lại tham chiếu tới Binary trong resource (Medplum fhir/rewrite.ts), hai chiều:
//   - Write (khi ghi, không kiểm quyền): mọi dạng URL Binary (kể cả URL ký đã cấp cho client) thành `Binary/<id>`, để CSDL không giữ
//     URL có hạn.
//   - Response (khi đọc): thuộc tính chuỗi tên `url` hoặc `path` trỏ tới Binary thành URL ký, chỉ khi repository của người gọi đọc được
//     Binary (và securityContext); không đọc được thì giữ nguyên; Binary không còn thì thành `Binary/<id>`.
//
// Không viết lại bên trong resource Binary, không đụng `Bundle.entry.request.url`, không sửa đối tượng đầu vào, và trả đúng đối tượng
// đầu vào khi không có gì để đổi. Chỉ nhận ra URL có tiền tố `Binary/`, gốc FHIR hoặc gốc storage (bỏ ?query trước khi tách).
type Rewriter struct {
	cfg    RewriteConfig
	repo   fhirrouter.Repository
	signer *binarystore.Signer
}

// NewRewriter dựng Rewriter. repo và signer chỉ cần cho Response (nil: Response không đổi gì); Write không cần.
func NewRewriter(cfg RewriteConfig, repo fhirrouter.Repository, signer *binarystore.Signer) *Rewriter {
	cfg.FHIRBaseURL = strings.TrimRight(cfg.FHIRBaseURL, "/")
	cfg.StorageBaseURL = strings.TrimRight(cfg.StorageBaseURL, "/")
	return &Rewriter{cfg: cfg, repo: repo, signer: signer}
}

type binRef struct{ id, ver string }

// parse nhận ra `Binary/<id>[/_history/<v>]`, `<fhirBase>/Binary/<id>[/_history/<v>]` và `<storageBase>/<id>[/<v>]`.
func (w *Rewriter) parse(s string) (binRef, bool) {
	if s == "" || len(s) > 2048 {
		return binRef{}, false
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	var rest string
	storage := false
	switch {
	case strings.HasPrefix(s, "Binary/"):
		rest = s[len("Binary/"):]
	case w.cfg.FHIRBaseURL != "" && strings.HasPrefix(s, w.cfg.FHIRBaseURL+"/Binary/"):
		rest = s[len(w.cfg.FHIRBaseURL+"/Binary/"):]
	case w.cfg.StorageBaseURL != "" && strings.HasPrefix(s, w.cfg.StorageBaseURL+"/"):
		rest, storage = s[len(w.cfg.StorageBaseURL+"/"):], true
	default:
		return binRef{}, false
	}
	parts := strings.Split(rest, "/")
	var ref binRef
	switch {
	case len(parts) == 1:
		ref.id = parts[0]
	case len(parts) == 2 && storage:
		ref.id, ref.ver = parts[0], parts[1]
	case len(parts) == 3 && !storage && parts[1] == "_history":
		ref.id, ref.ver = parts[0], parts[2]
	default:
		return binRef{}, false
	}
	if !binarystore.ValidID(ref.id) || (ref.ver != "" && !binarystore.ValidID(ref.ver)) {
		return binRef{}, false
	}
	return ref, true
}

// walk áp f lên mọi thuộc tính chuỗi `url`/`path`, sao chép-khi-ghi: trả (đối tượng mới, true) nếu có đổi, ngược lại (v, false).
func walk(v any, key string, f func(string) (string, bool)) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		if rt, _ := t["resourceType"].(string); rt == "Binary" {
			return v, false
		}
		if _, hasMethod := t["method"]; key == "request" && hasMethod { // Bundle.entry.request: không phải thuộc tính của dữ liệu
			return v, false
		}
		var out map[string]any
		for k, val := range t {
			var nv any
			var changed bool
			if s, ok := val.(string); ok && (k == "url" || k == "path") {
				if r, c := f(s); c {
					nv, changed = r, true
				}
			} else {
				nv, changed = walk(val, k, f)
			}
			if changed {
				if out == nil {
					out = make(map[string]any, len(t))
					for kk, vv := range t {
						out[kk] = vv
					}
				}
				out[k] = nv
			}
		}
		if out != nil {
			return out, true
		}
	case []any:
		var out []any
		for i, val := range t {
			if nv, changed := walk(val, key, f); changed {
				if out == nil {
					out = append([]any(nil), t...)
				}
				out[i] = nv
			}
		}
		if out != nil {
			return out, true
		}
	}
	return v, false
}

// ToReference viết lại mọi URL Binary trong doc (cây JSON tổng quát) thành `Binary/<id>`.
func (w *Rewriter) ToReference(doc any) (any, bool) {
	return walk(doc, "", func(s string) (string, bool) {
		ref, ok := w.parse(s)
		if !ok {
			return s, false
		}
		out := "Binary/" + ref.id
		return out, out != s
	})
}

// ToPresigned viết lại tham chiếu Binary thành URL ký theo quyền của người gọi (xem Rewriter).
func (w *Rewriter) ToPresigned(ctx context.Context, doc any) (any, bool) {
	if w.repo == nil || w.signer == nil {
		return doc, false
	}
	cache := map[string]*string{} // nil: giữ nguyên
	return walk(doc, "", func(s string) (string, bool) {
		ref, ok := w.parse(s)
		if !ok {
			return s, false
		}
		out, seen := cache[s]
		if !seen {
			out = w.resolve(ctx, ref)
			cache[s] = out
		}
		if out == nil || *out == s {
			return s, false
		}
		return *out, true
	})
}

// resolve trả chuỗi thay thế, hoặc nil nếu phải giữ nguyên.
func (w *Rewriter) resolve(ctx context.Context, ref binRef) *string {
	var res fhir.Resource
	var err error
	if ref.ver != "" {
		res, err = w.repo.ReadVersion(ctx, "Binary", ref.id, ref.ver)
	} else {
		res, err = w.repo.ReadResource(ctx, "Binary", ref.id)
	}
	if err != nil {
		var fe *fhirrouter.Error
		if errors.As(err, &fe) && (fe.Status == http.StatusNotFound || fe.Status == http.StatusGone) {
			s := "Binary/" + ref.id // không còn: về dạng tham chiếu
			return &s
		}
		return nil // không đọc được (403...) hoặc lỗi khác: giữ nguyên
	}
	b, ok := res.(*fhir.Binary)
	if !ok || !canReadSecurityContext(ctx, w.repo, b) {
		return nil
	}
	project := ""
	if b.Meta != nil {
		project = b.Meta.Project
	}
	u, err := w.signer.Sign(http.MethodGet, ref.id, versionOf(b), project)
	if err != nil {
		slog.WarnContext(ctx, "fhirbinary: không ký được URL", "binary", "Binary/"+ref.id, "error", err.Error())
		return nil
	}
	return &u
}

// mayContain loại nhanh các resource chắc chắn không có URL Binary (tránh giải mã lại JSON cho mọi phản hồi).
func (w *Rewriter) mayContain(raw []byte) bool {
	return bytes.Contains(raw, []byte("Binary/")) || (w.cfg.StorageBaseURL != "" && bytes.Contains(raw, []byte(w.cfg.StorageBaseURL)))
}

func (w *Rewriter) apply(res fhir.Resource, f func(any) (any, bool)) (fhir.Resource, bool, error) {
	switch res.GetResourceType() {
	case "Binary", "OperationOutcome":
		return res, false, nil
	}
	raw, err := json.Marshal(res)
	if err != nil || !w.mayContain(raw) {
		return res, false, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var doc any
	if err := d.Decode(&doc); err != nil {
		return res, false, err
	}
	out, changed := f(doc)
	if !changed {
		return res, false, nil
	}
	raw, err = json.Marshal(out)
	if err != nil {
		return res, false, err
	}
	nr, err := fhir.UnmarshalResource(raw)
	if err != nil {
		return res, false, err
	}
	return nr, true, nil
}

// Write viết lại resource sắp ghi (xem Rewriter). Lỗi nội bộ thì ghi nguyên resource gốc (không chặn việc ghi).
func (w *Rewriter) Write(res fhir.Resource) fhir.Resource {
	out, _, err := w.apply(res, w.ToReference)
	if err != nil {
		slog.Warn("fhirbinary: không viết lại được tham chiếu Binary khi ghi", "resource", res.GetResourceType()+"/"+res.GetID(), "error", err.Error())
		return res
	}
	return out
}

// Response viết lại resource sắp trả cho người gọi; dùng làm fhirrouter.Options.ResponseHook.
func (w *Rewriter) Response(ctx context.Context, res fhir.Resource) fhir.Resource {
	out, _, err := w.apply(res, func(d any) (any, bool) { return w.ToPresigned(ctx, d) })
	if err != nil {
		slog.WarnContext(ctx, "fhirbinary: không viết lại được URL Binary khi đọc", "resource", res.GetResourceType()+"/"+res.GetID(), "error", err.Error())
		return res
	}
	return out
}
