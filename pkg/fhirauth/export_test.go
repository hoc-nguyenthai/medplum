package fhirauth

import "strings"

// ExprPathStrings mở exprPaths cho test ngoài gói: mỗi đường dẫn thành chuỗi "a.b~Kiểu".
func ExprPathStrings(expr string) []string {
	var out []string
	for _, p := range exprPaths(expr) {
		var segs []string
		for _, s := range p {
			if s.typ != "" {
				segs = append(segs, s.name+"~"+s.typ)
			} else {
				segs = append(segs, s.name)
			}
		}
		out = append(out, strings.Join(segs, "."))
	}
	return out
}

// ExprTouches mở touchesAny cho test: biểu thức của một tham số có đụng tới trường ẩn hidden không.
func ExprTouches(hidden []string, expr string) bool {
	for _, p := range exprPaths(expr) {
		if touchesAny(hidden, p) {
			return true
		}
	}
	return false
}
