// Package scim chứa kiểu, bộ phân tích filter và bộ áp patch của SCIM 2.0 (RFC 7643, 7644) cho tài nguyên Users. Gói thuần: chỉ stdlib.
// Chỉ các thuộc tính đã biết được đọc hay ghi; mọi thứ khác bị bỏ qua (POST, PUT) hoặc bị từ chối (PATCH), nên một IdP không thể ghi
// admin, accessPolicy, profile, project... qua đường này.
package scim

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Các URN của SCIM.
const (
	SchemaUser  = "urn:ietf:params:scim:schemas:core:2.0:User"
	SchemaList  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	SchemaError = "urn:ietf:params:scim:api:messages:2.0:Error"
	SchemaPatch = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
)

// Name là name của SCIM User (chỉ hai trường được dùng).
type Name struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

// Email là một phần tử của emails (chỉ phần tử đầu được dùng).
type Email struct {
	Value string `json:"value"`
}

// Meta là meta của User.
type Meta struct {
	ResourceType string `json:"resourceType"`
	Version      string `json:"version,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Location     string `json:"location,omitempty"`
}

// User là biểu diễn SCIM trả ra và nhận vào. Struct khai báo tường minh: không bao giờ serialize resource FHIR, nên không thể lộ passwordHash, mfaSecret...
type User struct {
	Schemas    []string `json:"schemas"`
	ID         string   `json:"id,omitempty"`
	ExternalID *string  `json:"externalId,omitempty"`
	UserName   string   `json:"userName,omitempty"`
	UserType   string   `json:"userType,omitempty"`
	Name       *Name    `json:"name,omitempty"`
	Emails     []Email  `json:"emails,omitempty"`
	Active     *bool    `json:"active,omitempty"`
	Meta       *Meta    `json:"meta,omitempty"`
}

// ListResponse là kết quả GET /Users.
type ListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	ItemsPerPage int      `json:"itemsPerPage"`
	StartIndex   int      `json:"startIndex"`
	Resources    []User   `json:"Resources"`
}

// Error là lỗi SCIM (RFC 7644 mục 3.12); Status là chuỗi.
type Error struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	ScimType string   `json:"scimType,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// ProblemError là lỗi nghiệp vụ mang mã HTTP và scimType; application trả nó để presentation dựng Error.
type ProblemError struct {
	Status   int
	ScimType string
	Detail   string
}

func (e *ProblemError) Error() string { return e.Detail }

func problem(status int, scimType, format string, a ...any) *ProblemError {
	return &ProblemError{Status: status, ScimType: scimType, Detail: fmt.Sprintf(format, a...)}
}

// ResourceTypes là loại hồ sơ mà SCIM quản lý; userType khác hoặc thiếu là Practitioner.
var ResourceTypes = []string{"Patient", "Practitioner", "RelatedPerson"}

// NormalizeUserType trả loại hồ sơ hợp lệ (mặc định Practitioner).
func NormalizeUserType(t string) string {
	for _, r := range ResourceTypes {
		if t == r {
			return r
		}
	}
	return "Practitioner"
}

// ---- filter

// ParseFilter chỉ hiểu `userName eq "<giá trị>"` (không phân biệt hoa thường ở tên thuộc tính và toán tử; giá trị có thể chứa `\"` và `\\`).
// Trả giá trị đã hạ chữ thường để so sánh bằng. Filter rỗng trả ("", false, nil); dạng khác là lỗi invalidFilter.
func ParseFilter(f string) (value string, has bool, err error) {
	f = strings.TrimSpace(f)
	if f == "" {
		return "", false, nil
	}
	rest := f
	next := func() string {
		rest = strings.TrimLeft(rest, " ")
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			w := rest
			rest = ""
			return w
		}
		w := rest[:i]
		rest = rest[i+1:]
		return w
	}
	if !strings.EqualFold(next(), "userName") || !strings.EqualFold(next(), "eq") {
		return "", false, problem(400, "invalidFilter", "Unsupported filter: only `userName eq \"value\"` is supported")
	}
	rest = strings.TrimSpace(rest)
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return "", false, problem(400, "invalidFilter", "Unsupported filter: value must be a quoted string")
	}
	body := rest[1 : len(rest)-1]
	var sb strings.Builder
	for i := 0; i < len(body); i++ {
		switch c := body[i]; c {
		case '"':
			return "", false, problem(400, "invalidFilter", "Unsupported filter: unescaped quote in value")
		case '\\':
			if i+1 >= len(body) || (body[i+1] != '"' && body[i+1] != '\\') {
				return "", false, problem(400, "invalidFilter", "Unsupported filter: invalid escape in value")
			}
			i++
			sb.WriteByte(body[i])
		default:
			sb.WriteByte(c)
		}
	}
	if sb.Len() == 0 {
		return "", false, problem(400, "invalidFilter", "Unsupported filter: empty value")
	}
	return strings.ToLower(sb.String()), true, nil
}

// ---- patch

// PatchOp là body của PATCH.
type PatchOp struct {
	Schemas    []string         `json:"schemas"`
	Operations []PatchOperation `json:"Operations"`
}

// PatchOperation là một thao tác.
type PatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// ignorable là các thuộc tính chuẩn của SCIM mà ta không lưu: nhận và bỏ qua để IdP (Okta, Azure) đẩy hồ sơ đầy đủ không bị lỗi.
var ignorable = map[string]bool{
	"displayname": true, "nickname": true, "profileurl": true, "title": true, "preferredlanguage": true, "locale": true, "timezone": true,
	"phonenumbers": true, "addresses": true, "ims": true, "photos": true, "name.formatted": true, "name.middlename": true,
	"name.honorificprefix": true, "name.honorificsuffix": true, "username": true, "usertype": true,
}

// ApplyPatch áp các thao tác lên bản sao của u (đã dựng từ trạng thái hiện tại) và trả bản mới. Chỉ `active`, `externalId`, `name`, `name.givenName`,
// `name.familyName`, `emails` được ghi; `userName` và `userType` được nhận nhưng bị bỏ qua (không đổi được sau khi tạo); thuộc tính chuẩn không lưu (displayName...)
// bị bỏ qua; mọi đường dẫn khác (admin, accessPolicy, profile, project, user, meta, id, groups, roles...) là 400 invalidPath. `add` trên thuộc tính đa trị
// THAY nguyên (như Medplum), không nối thêm.
func ApplyPatch(u User, p PatchOp, maxOps int) (User, error) {
	if len(p.Schemas) == 0 || p.Schemas[0] != SchemaPatch {
		return u, problem(400, "invalidSyntax", "Invalid SCIM patch: missing required schema")
	}
	if maxOps > 0 && len(p.Operations) > maxOps {
		return u, problem(400, "tooMany", "Invalid SCIM patch: too many operations (max %d)", maxOps)
	}
	if u.Name != nil {
		c := *u.Name
		u.Name = &c
	}
	u.Emails = append([]Email(nil), u.Emails...)
	for _, op := range p.Operations {
		kind := strings.ToLower(op.Op)
		if kind != "add" && kind != "remove" && kind != "replace" {
			return u, problem(400, "invalidSyntax", "Invalid SCIM patch: unsupported operation")
		}
		if op.Path != "" {
			if strings.HasPrefix(op.Path, "/") {
				return u, problem(400, "invalidPath", `Invalid SCIM patch: path must not start with "/"`)
			}
			if err := applyOne(&u, kind, op.Path, op.Value); err != nil {
				return u, err
			}
			continue
		}
		if kind == "remove" {
			return u, problem(400, "noTarget", "Invalid SCIM patch: missing required path")
		}
		var obj map[string]json.RawMessage
		if len(op.Value) == 0 || json.Unmarshal(op.Value, &obj) != nil || obj == nil || len(obj) == 0 {
			return u, problem(400, "invalidValue", "Invalid SCIM patch: value must be an object if path is missing")
		}
		for k, v := range obj {
			if err := applyOne(&u, kind, k, v); err != nil {
				return u, err
			}
		}
	}
	return u, nil
}

func applyOne(u *User, op, path string, raw json.RawMessage) error {
	p := strings.ToLower(strings.TrimSpace(path))
	if ignorable[p] {
		return nil
	}
	remove := op == "remove"
	switch p {
	case "active":
		if remove {
			return problem(400, "mutability", "Invalid SCIM patch: active cannot be removed")
		}
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return problem(400, "invalidValue", "Invalid SCIM patch: active must be a boolean")
		}
		u.Active = &b
	case "externalid":
		if remove {
			empty := ""
			u.ExternalID = &empty
			return nil
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return problem(400, "invalidValue", "Invalid SCIM patch: externalId must be a string")
		}
		u.ExternalID = &s
	case "name":
		if remove {
			u.Name = &Name{}
			return nil
		}
		var n Name
		if json.Unmarshal(raw, &n) != nil {
			return problem(400, "invalidValue", "Invalid SCIM patch: name must be an object")
		}
		u.Name = &n
	case "name.givenname", "name.familyname":
		if u.Name == nil {
			u.Name = &Name{}
		}
		var s string
		if !remove && json.Unmarshal(raw, &s) != nil {
			return problem(400, "invalidValue", "Invalid SCIM patch: %s must be a string", path)
		}
		if p == "name.givenname" {
			u.Name.GivenName = s
		} else {
			u.Name.FamilyName = s
		}
	case "emails":
		if remove {
			return problem(400, "mutability", "Invalid SCIM patch: emails cannot be removed")
		}
		var e []Email
		if json.Unmarshal(raw, &e) != nil || len(e) == 0 || e[0].Value == "" {
			return problem(400, "invalidValue", "Invalid SCIM patch: emails must be a non-empty array with a value")
		}
		u.Emails = e
	default:
		return problem(400, "invalidPath", "Invalid SCIM patch: unsupported path %q", path)
	}
	return nil
}
