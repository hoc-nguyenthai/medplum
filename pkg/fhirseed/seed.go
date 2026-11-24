// Package fhirseed nạp các định nghĩa dựng sẵn (SearchParameter, StructureDefinition, OperationDefinition, ValueSet, CodeSystem)
// vào kho dưới dạng resource, để client truy vấn được GET /StructureDefinition, /SearchParameter... (D5, #18).
//
// Nguồn: thư mục định nghĩa của Medplum (packages/definitions/src/fhir/r4, các tệp Bundle JSON) nếu có Options.Dir; không có thì
// chỉ SearchParameter dựng từ dữ liệu đã biên dịch trong pkg/fhir. Máy chủ không đọc các resource này khi chạy (kiểm hợp lệ,
// search và terminology dùng mã sinh sẵn), nên seed là tùy chọn và chỉ phục vụ truy vấn của client.
//
// Mỗi project có bản riêng (không có project R4 dùng chung như Medplum): chạy lại cho từng project. Id là UUID v5 của
// (project, loại, url) nên chạy lại là idempotent: resource không đổi thì bỏ qua, đổi thì cập nhật. Như Medplum, bỏ `text` và
// `differential` để giảm dung lượng.
package fhirseed

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
)

// AllTypes là các loại resource seed hỗ trợ.
var AllTypes = []string{"SearchParameter", "StructureDefinition", "OperationDefinition", "CodeSystem", "ValueSet"}

// Các tệp Bundle trong thư mục định nghĩa của Medplum, theo thứ tự nạp (tệp không có thì bỏ qua và ghi vào Report.Missing).
var bundleFiles = []string{
	"search-parameters.json", "search-parameters-medplum.json", "search-parameters-uscore.json",
	"profiles-types.json", "profiles-resources.json", "profiles-medplum.json", "profiles-others.json",
	"valuesets.json", "valuesets-medplum.json", "valuesets-medplum-generated.json", "v3-codesystems.json", "v2-tables.json",
}

// Options của Seed.
type Options struct {
	// Project nhận bản sao định nghĩa. Rỗng: ghi thẳng vào repo đã cho, không gán project (chế độ một tenant, repo chưa tách project).
	Project string
	// Dir là thư mục chứa các Bundle JSON của Medplum. Rỗng: chỉ SearchParameter dựng sẵn.
	Dir string
	// Types giới hạn loại cần nạp (mặc định AllTypes).
	Types []string
	// DryRun chỉ đếm, không ghi.
	DryRun bool
}

// Report là kết quả.
type Report struct {
	Created, Updated, Unchanged map[string]int
	Failed                      map[string]int
	Missing                     []string // tệp trong Dir không có
	Errors                      []string // vài lỗi đầu tiên
}

func newReport() Report {
	return Report{Created: map[string]int{}, Updated: map[string]int{}, Unchanged: map[string]int{}, Failed: map[string]int{}}
}

// Seed nạp định nghĩa vào repo. repo nên là kho đã bọc fhirauth.Scope khi muốn tách theo project.
func Seed(ctx context.Context, repo fhirrouter.Repository, opt Options) (Report, error) {
	rep := newReport()
	types := opt.Types
	if len(types) == 0 {
		types = AllTypes
	}
	for _, t := range types {
		if !slices.Contains(AllTypes, t) {
			return rep, fmt.Errorf("fhirseed: loại không hỗ trợ: %q (hỗ trợ %v)", t, AllTypes)
		}
	}
	if opt.Project != "" {
		ctx = fhirauth.WithPrincipal(ctx, &fhirauth.Principal{Subject: "seed", Project: opt.Project, Role: fhirauth.RoleAdmin})
	}
	seen := map[string]bool{} // loại|url đã xử lý: tệp và phần tử sau thắng (như Medplum), nên duyệt ngược và bỏ bản trước
	apply := func(doc map[string]any) {
		rt, _ := doc["resourceType"].(string)
		if !slices.Contains(types, rt) {
			return
		}
		if u, _ := doc["url"].(string); u != "" {
			if seen[rt+"|"+u] {
				return
			}
			seen[rt+"|"+u] = true
		}
		if err := upsert(ctx, repo, opt, doc, &rep); err != nil {
			rep.Failed[rt]++
			if len(rep.Errors) < 10 {
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s %v: %v", rt, doc["url"], err))
			}
		}
	}

	if opt.Dir == "" {
		if slices.Contains(types, "SearchParameter") {
			for _, d := range builtinSearchParameters() { // đã loại trùng url
				apply(d)
			}
		}
		return rep, nil
	}
	if st, err := os.Stat(opt.Dir); err != nil || !st.IsDir() {
		return rep, fmt.Errorf("fhirseed: không đọc được thư mục định nghĩa %q", opt.Dir)
	}
	found := 0
	for i := len(bundleFiles) - 1; i >= 0; i-- {
		f := bundleFiles[i]
		path := filepath.Join(opt.Dir, f)
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			rep.Missing = append(rep.Missing, f)
			continue
		}
		if err != nil {
			return rep, err
		}
		found++
		var b struct {
			Entry []struct {
				Resource map[string]any `json:"resource"`
			} `json:"entry"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return rep, fmt.Errorf("fhirseed: %s không phải Bundle JSON: %w", f, err)
		}
		for i := len(b.Entry) - 1; i >= 0; i-- {
			if r := b.Entry[i].Resource; r != nil {
				apply(r)
			}
		}
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
	}
	if found == 0 {
		return rep, fmt.Errorf("fhirseed: không có tệp định nghĩa nào trong %q (cần ví dụ search-parameters.json)", opt.Dir)
	}
	return rep, nil
}

// upsert ghi một định nghĩa: tạo mới, cập nhật nếu khác, bỏ qua nếu giống.
func upsert(ctx context.Context, repo fhirrouter.Repository, opt Options, doc map[string]any, rep *Report) error {
	rt, _ := doc["resourceType"].(string)
	url, _ := doc["url"].(string)
	if url == "" {
		return fmt.Errorf("thiếu url")
	}
	doc = clean(doc)
	id := seedID(opt.Project, rt, url)
	doc["id"] = id
	res, err := toResource(doc)
	if err != nil {
		return err
	}
	cur, err := repo.ReadResource(ctx, rt, id)
	if err == nil {
		if same(cur, doc) {
			rep.Unchanged[rt]++
			return nil
		}
		if !opt.DryRun {
			if _, err := repo.UpdateResource(ctx, res, fhirrouter.UpdateOptions{}); err != nil {
				return err
			}
		}
		rep.Updated[rt]++
		return nil
	}
	var fe *fhirrouter.Error
	if !asNotFound(err, &fe) {
		return err
	}
	if !opt.DryRun {
		if _, err := repo.CreateResource(ctx, res); err != nil {
			return err
		}
	}
	rep.Created[rt]++
	return nil
}

func asNotFound(err error, out **fhirrouter.Error) bool {
	fe, ok := err.(*fhirrouter.Error)
	if ok && fe.Status == 404 {
		*out = fe
		return true
	}
	return false
}

// clean bỏ meta, text và differential (như Medplum).
func clean(doc map[string]any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		switch k {
		case "meta", "text", "differential", "id":
		default:
			out[k] = v
		}
	}
	return out
}

func toResource(doc map[string]any) (fhir.Resource, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return fhir.UnmarshalResource(raw)
}

// same so nội dung đang lưu với bản seed, bỏ meta.
func same(cur fhir.Resource, want map[string]any) bool {
	raw, err := json.Marshal(cur)
	if err != nil {
		return false
	}
	var have map[string]any
	if json.Unmarshal(raw, &have) != nil {
		return false
	}
	delete(have, "meta")
	// so qua JSON chuẩn hóa của bản seed để số và kiểu khớp với bản đã qua struct
	w, err := toResource(want)
	if err != nil {
		return false
	}
	wraw, _ := json.Marshal(w)
	var wantNorm map[string]any
	_ = json.Unmarshal(wraw, &wantNorm)
	delete(wantNorm, "meta")
	return reflect.DeepEqual(have, wantNorm)
}

// seedID là UUID v5 (không gian tên cố định) của project|loại|url.
func seedID(project, rt, url string) string {
	ns := [16]byte{0x7a, 0x1d, 0x3e, 0x52, 0x9c, 0x44, 0x4b, 0x0e, 0xa1, 0x6f, 0x2d, 0x88, 0x51, 0xb3, 0x0c, 0x97}
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(project + "|" + rt + "|" + url))
	s := h.Sum(nil)
	s[6] = s[6]&0x0f | 0x50
	s[8] = s[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", s[0:4], s[4:6], s[6:8], s[8:10], s[10:16])
}

// builtinSearchParameters dựng SearchParameter từ dữ liệu đã biên dịch trong pkg/fhir.
func builtinSearchParameters() []map[string]any {
	seen := map[string]bool{}
	var out []map[string]any
	add := func(sp fhir.SearchParamDef) {
		if seen[sp.URL] {
			return
		}
		seen[sp.URL] = true
		d := map[string]any{
			"resourceType": "SearchParameter", "url": sp.URL, "name": sp.Code, "code": sp.Code, "status": "active",
			"description": sp.Code + " (" + sp.Type + ")", "type": sp.Type, "base": strs(sp.Base),
		}
		if sp.Expression != "" {
			d["expression"] = sp.Expression
		}
		if len(sp.Target) > 0 {
			d["target"] = strs(sp.Target)
		}
		if len(sp.Comparator) > 0 {
			d["comparator"] = strs(sp.Comparator)
		}
		if len(sp.Modifier) > 0 {
			d["modifier"] = strs(sp.Modifier)
		}
		if len(sp.Component) > 0 {
			var cs []any
			for _, c := range sp.Component {
				cs = append(cs, map[string]any{"definition": c.Definition, "expression": c.Expression})
			}
			d["component"] = cs
		}
		out = append(out, d)
	}
	add(fhir.ProjectParam)
	for _, rt := range fhir.ResourceTypes {
		for _, sp := range fhir.SearchParams(rt) {
			add(sp)
		}
	}
	return out
}

func strs(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}
