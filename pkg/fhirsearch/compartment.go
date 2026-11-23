package fhirsearch

import (
	"regexp"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// compartmentRef là giá trị hợp lệ của _compartment: đúng một tham chiếu Patient/<id>. Không nhận dấu phẩy (nhiều compartment), URL tuyệt đối hay loại khác:
// giá trị lạ phải bị từ chối chứ không được hiểu thành "không lọc", vì _compartment dùng để giới hạn quyền (AccessPolicy `%patient`).
var compartmentRef = regexp.MustCompile(`^Patient/[A-Za-z0-9.\-]{1,64}$`)

// compartmentExpr dựng biểu thức "resource rt thuộc compartment của Patient ref": OR của các tham số compartment (ví dụ Observation: subject, performer) trỏ tới
// ref, và với Patient thì thêm chính nó (_id). Trả lỗi nếu ref sai dạng hoặc loại rt không thuộc compartment Patient (fail đóng: không bao giờ trả biểu thức rỗng,
// vì OR rỗng hoặc bị bỏ qua đều có thể thành "đọc cả loại").
func compartmentExpr(rt, ref string) (*Expr, error) {
	if !compartmentRef.MatchString(ref) {
		return nil, errf("_compartment phải có dạng Patient/<id>: %q", ref)
	}
	params, ok := fhir.PatientCompartment()[rt]
	if !ok {
		return nil, errf("loại %s không thuộc compartment Patient", rt)
	}
	var kids []*Expr
	if rt == "Patient" {
		def, found := Lookup(rt, "_id")
		if !found {
			return nil, errf("không tìm thấy tham số _id của Patient")
		}
		kids = append(kids, &Expr{Op: "leaf", Leaf: &Filter{Code: "_id", Def: def, Values: []string{strings.TrimPrefix(ref, "Patient/")}}})
	}
	for _, p := range params {
		def, found := Lookup(rt, p)
		if !found {
			continue
		}
		kids = append(kids, &Expr{Op: "leaf", Leaf: &Filter{Code: p, Def: def, Values: []string{ref}}})
	}
	if len(kids) == 0 {
		return nil, errf("loại %s không có tham số compartment dùng được", rt)
	}
	if len(kids) == 1 {
		return kids[0], nil
	}
	return &Expr{Op: "or", Kids: kids}, nil
}
