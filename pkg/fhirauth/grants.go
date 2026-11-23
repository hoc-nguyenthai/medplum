package fhirauth

import (
	"sort"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// Perm là quyền trên một loại resource, một bit cho mỗi chữ của SMART v2 (`cruds`). Phải tách riêng từng bit: gộp `c`, `u`, `d`
// vào một quyền "ghi" làm token chỉ có `system/Patient.c` (chỉ tạo) sửa và xóa được, còn `s` (chỉ tìm kiếm) đọc được theo id.
type Perm uint8

// Các quyền đơn (khớp chữ cái SMART v2).
const (
	PermCreate Perm = 1 << iota // c: create
	PermRead                    // r: read, vread, history của một resource
	PermUpdate                  // u: update, patch
	PermDelete                  // d: delete
	PermSearch                  // s: search
)

// Các tập quyền thường dùng.
const (
	PermWrite   = PermCreate | PermUpdate | PermDelete // c, u, d
	PermAllRead = PermRead | PermSearch                // r, s
	PermAll     = PermAllRead | PermWrite              // cruds
)

// permLetters là chữ SMART v2 của từng bit, theo thứ tự cruds.
const permLetters = "cruds"

// Grants là quyền theo loại resource, lấy từ scope SMART (system/Observation.rs, user/Patient.cud, system/*.cruds, và dạng v1
// `.read`, `.write`, `.*`). Khóa "*" áp cho mọi loại. Grants nil nghĩa là không giới hạn theo loại (Principal dựng tay, job nền).
type Grants map[string]Perm

func (g Grants) perm(rt string) Perm { return g["*"] | g[rt] }

// Can cho biết có đủ MỌI quyền trong need trên loại rt không. need là một hoặc nhiều bit (ví dụ PermUpdate).
func (g Grants) Can(rt string, need Perm) bool { return g == nil || g.perm(rt)&need == need }

// CanRead cho biết có được đọc (read, vread, history) một resource loại rt không. KHÔNG gồm search.
func (g Grants) CanRead(rt string) bool { return g.Can(rt, PermRead) }

// CanSearch cho biết có được tìm kiếm loại rt không.
func (g Grants) CanSearch(rt string) bool { return g.Can(rt, PermSearch) }

// CanSee cho biết kết quả của loại rt được phép xuất hiện trong một phản hồi tìm kiếm (đọc hoặc tìm kiếm). Dùng để lọc
// resource kèm theo (_include, _revinclude) và kết quả của chained search.
func (g Grants) CanSee(rt string) bool { return g == nil || g.perm(rt)&PermAllRead != 0 }

// FullSearch cho biết có được tìm kiếm mọi loại không (search hệ thống không có _type).
func (g Grants) FullSearch() bool { return g == nil || g["*"]&PermSearch != 0 }

// Searchable liệt kê các loại tìm kiếm được (dùng cho search hệ thống khi không có _type).
func (g Grants) Searchable() []string {
	var out []string
	for _, rt := range fhir.ResourceTypes {
		if g.perm(rt)&PermSearch != 0 {
			out = append(out, rt)
		}
	}
	sort.Strings(out)
	return out
}

// describe liệt kê các chữ cái SMART của p, ví dụ "cu" hoặc "rs"; dùng trong thông báo lỗi.
func describe(p Perm) string {
	var b strings.Builder
	for i, c := range permLetters {
		if p&(1<<i) != 0 {
			b.WriteRune(c)
		}
	}
	return b.String()
}

var knownTypes = func() map[string]bool {
	m := map[string]bool{"*": true}
	for _, rt := range fhir.ResourceTypes {
		m[rt] = true
	}
	return m
}()

// parseSMART đọc một scope SMART "system|user/<Type|*>.<quyền>". Quyền: read (rs), write (cud), * (cruds), hoặc các chữ cái
// c, r, u, d, s của SMART v2, mỗi chữ cấp ĐÚNG quyền của nó (không gộp).
// Scope không hợp lệ, loại không biết và scope patient/ (cần compartment, chưa hỗ trợ) bị bỏ qua, nghĩa là không cấp quyền.
func parseSMART(s string) (rt string, p Perm, ok bool) {
	ctxPart, rest, found := strings.Cut(s, "/")
	if !found || (ctxPart != "system" && ctxPart != "user") {
		return "", 0, false
	}
	rt, perm, found := strings.Cut(rest, ".")
	if !found || !knownTypes[rt] {
		return "", 0, false
	}
	switch perm {
	case "read":
		p = PermAllRead // v1: read = rs
	case "write":
		p = PermWrite // v1: write = cud
	case "*":
		p = PermAll
	default:
		for _, c := range perm {
			i := strings.IndexRune(permLetters, c)
			if i < 0 {
				return "", 0, false
			}
			p |= 1 << i
		}
	}
	return rt, p, p != 0
}

// grantsFromScopes gom quyền từ mọi scope. Scope vai trò (fhir.read/write/admin) cấp quyền trên mọi loại.
func grantsFromScopes(scopes []string) Grants {
	g := Grants{}
	for _, s := range scopes {
		switch s {
		case ScopeRead:
			g["*"] |= PermAllRead
		case ScopeWrite, ScopeAdmin:
			g["*"] |= PermAll
		default:
			if rt, p, ok := parseSMART(s); ok {
				g[rt] |= p
			}
		}
	}
	return g
}

// roleFromGrants suy ra vai trò HTTP tối thiểu: có quyền ghi ở bất kỳ loại nào thì RoleWrite, chỉ đọc thì RoleRead.
func roleFromGrants(g Grants) Role {
	r := RoleNone
	for _, p := range g {
		if p&PermWrite != 0 {
			return RoleWrite // cổng HTTP chỉ phân biệt đọc và ghi; từng thao tác do Repo kiểm tiếp
		}
		if p&PermAllRead != 0 {
			r = RoleRead
		}
	}
	return r
}
