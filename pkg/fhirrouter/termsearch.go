package fhirrouter

import (
	"context"
	"strings"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Modifier terminology của tham số token, giải bằng CodeSystem/ValueSet lưu trong server (xem terminology.go):
//
//	Observation?code:in=http://example.org/ValueSet/vitals        mã thuộc ValueSet (URL chuẩn, ValueSet/<id>)
//	Observation?code:not-in=<ValueSet>                            mã không thuộc ValueSet
//	Condition?code:below=http://snomed.info/sct|73211009          mã là con cháu (is-a, gồm chính nó) của mã đã cho
//	Condition?code:above=http://snomed.info/sct|46635009          mã là tổ tiên (gồm chính nó) của mã đã cho
//
// Mỗi điều kiện được thay bằng một điều kiện token thường liệt kê các "system|code" tìm được (OR), nên Repository
// (Memory, SQL) không cần biết terminology. Giới hạn: tối đa maxTermCodes mã mỗi điều kiện (vượt thì 400, thu hẹp
// ValueSet hoặc mã gốc); :above/:below chỉ dùng CodeSystem đã lưu trong server (quan hệ cha con theo cây concept);
// kết quả là ảnh chụp tại thời điểm tìm, không nhất quán với thay đổi đồng thời của CodeSystem/ValueSet.
const maxTermCodes = 5000

// resolveTerminology thay các điều kiện token terminology bằng danh sách mã tường minh.
func resolveTerminology(ctx context.Context, repo Repository, sr *fhirsearch.Request) (*fhirsearch.Request, error) {
	has := false
	for _, f := range sr.Filters {
		has = has || f.IsTerminology()
	}
	if !has {
		return sr, nil
	}
	out := *sr
	out.Filters = make([]fhirsearch.Filter, 0, len(sr.Filters))
	var tr *Router // findByURL/expandMembers không dùng trạng thái của Router
	for _, f := range sr.Filters {
		if !f.IsTerminology() {
			out.Filters = append(out.Filters, f)
			continue
		}
		var keys []string
		var err error
		switch f.Modifier {
		case "in", "not-in":
			keys, err = tr.valueSetKeys(ctx, repo, f.Values)
		default:
			keys, err = tr.hierarchyKeys(ctx, repo, f.Modifier, f.Values)
		}
		if err != nil {
			return nil, err
		}
		nf := f
		nf.Modifier = ""
		switch {
		case f.Modifier == "not-in" && len(keys) == 0:
			continue // không mã nào bị loại: điều kiện luôn đúng
		case f.Modifier == "not-in":
			nf.Modifier = "not"
		case len(keys) == 0:
			keys = []string{noMatch}
		}
		nf.Values = keys
		out.Filters = append(out.Filters, nf)
	}
	return &out, nil
}

func tokenKey(system, code string) string {
	if system == "" {
		return code
	}
	return system + "|" + code
}

// valueSetKeys mở rộng các ValueSet (OR) thành danh sách "system|code" không trùng.
func (r *Router) valueSetKeys(ctx context.Context, repo Repository, refs []string) ([]string, error) {
	seen := map[string]bool{}
	var keys []string
	for _, ref := range refs {
		ref = fhirsearch.Unescape(ref)
		var res fhir.Resource
		var err error
		if id, ok := strings.CutPrefix(ref, "ValueSet/"); ok {
			res, err = repo.ReadResource(ctx, "ValueSet", id)
		} else if strings.Contains(ref, ":") {
			res, err = r.findByURL(ctx, repo, "ValueSet", ref)
		} else {
			return nil, BadRequest("giá trị :in phải là URL chuẩn hoặc ValueSet/<id>: " + ref)
		}
		if err != nil {
			return nil, err
		}
		vs, ok := res.(*fhir.ValueSet)
		if !ok {
			return nil, BadRequest("không phải ValueSet: " + ref)
		}
		ms, err := r.expandMembers(ctx, repo, vs, map[string]bool{})
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			k := tokenKey(m.system, m.code)
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		if len(keys) > maxTermCodes {
			return nil, BadRequest("ValueSet có quá nhiều mã cho :in/:not-in (> " + itoa(maxTermCodes) + "); thu hẹp ValueSet")
		}
	}
	return keys, nil
}

// hierarchyKeys tính các mã con cháu (below) hoặc tổ tiên (above), gồm chính mã gốc, của từng "system|code" (OR).
func (r *Router) hierarchyKeys(ctx context.Context, repo Repository, mod string, values []string) ([]string, error) {
	seen := map[string]bool{}
	var keys []string
	indexes := map[string]*csIndex{}
	for _, v := range values {
		system, code, ok := strings.Cut(fhirsearch.Unescape(v), "|")
		if !ok || system == "" || code == "" {
			return nil, BadRequest("giá trị :" + mod + " phải có dạng system|code: " + v)
		}
		ix := indexes[system]
		if ix == nil {
			res, err := r.findByURL(ctx, repo, "CodeSystem", system)
			if err != nil {
				return nil, err
			}
			cs, ok := res.(*fhir.CodeSystem)
			if !ok {
				return nil, BadRequest("không phải CodeSystem: " + system)
			}
			ix = indexCodeSystem(cs)
			indexes[system] = ix
		}
		if _, known := ix.byKey[code]; !known {
			continue // mã lạ: không khớp gì (kể cả chính nó, vì không có trong CodeSystem)
		}
		var codes []string
		if mod == "below" {
			codes = ix.descendants(code, true)
		} else {
			for c := code; c != ""; c = ix.byKey[c].parent {
				codes = append(codes, c)
			}
		}
		for _, c := range codes {
			k := tokenKey(system, c)
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		if len(keys) > maxTermCodes {
			return nil, BadRequest("kết quả :" + mod + " có quá nhiều mã (> " + itoa(maxTermCodes) + "); chọn mã gốc hẹp hơn")
		}
	}
	return keys, nil
}
