package fhirauth

import (
	"encoding/json"
	"errors"
	"time"
)

// Ảnh chụp quyền cho kết nối sống lâu (websocket): người gọi được chụp lúc cấp token, nơi nhận kiểm lại từng sự kiện
// theo ảnh chụp này (CanReadResource). Ảnh chụp chỉ tin được khi nằm trong chuỗi đã ký của nơi cấp.

type principalSnapshot struct {
	Version int            `json:"v"`
	Subject string         `json:"sub"`
	Project string         `json:"proj"`
	Grants  map[string]int `json:"grants"` // null: không giới hạn theo loại
	Policy  any            `json:"policy"` // null: không có chính sách
	Exp     int64          `json:"exp,omitempty"`
}

// MarshalSnapshot chụp quyền của p (grants, policy, hạn token) thành JSON.
func (p *Principal) MarshalSnapshot() ([]byte, error) {
	if p == nil {
		return nil, errors.New("fhirauth: không có Principal")
	}
	s := principalSnapshot{Version: sealVersion, Subject: p.Subject, Project: p.Project}
	if p.Grants != nil {
		s.Grants = make(map[string]int, len(p.Grants))
		for k, v := range p.Grants {
			s.Grants[k] = int(v)
		}
	}
	if p.Policy != nil {
		s.Policy = p.Policy.raw
	}
	if !p.Expires.IsZero() {
		s.Exp = p.Expires.Unix()
	}
	return json.Marshal(s)
}

// ParseSnapshot dựng lại Principal từ MarshalSnapshot. Phiên bản khác bị từ chối (ý nghĩa bit Perm có thể đã đổi).
func ParseSnapshot(raw []byte) (*Principal, error) {
	var s principalSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.New("fhirauth: ảnh chụp quyền hỏng")
	}
	if s.Version != sealVersion {
		return nil, errors.New("fhirauth: ảnh chụp quyền phiên bản cũ")
	}
	p := &Principal{Subject: s.Subject, Project: s.Project, Role: RoleRead}
	if s.Grants != nil {
		p.Grants = make(Grants, len(s.Grants))
		for k, v := range s.Grants {
			p.Grants[k] = Perm(v)
		}
	}
	if s.Policy != nil {
		pol, err := ParsePolicy(s.Policy)
		if err != nil {
			return nil, errors.New("fhirauth: ảnh chụp có chính sách hỏng")
		}
		p.Policy = pol
	}
	if s.Exp != 0 {
		p.Expires = time.Unix(s.Exp, 0)
	}
	return p, nil
}

// Expired cho biết token của p đã hết hạn tại thời điểm now (Expires zero: không bao giờ).
func (p *Principal) Expired(now time.Time) bool {
	return p != nil && !p.Expires.IsZero() && !now.Before(p.Expires)
}

// CanReadResource cho biết p được đọc resource loại rt có nội dung doc: scope SMART phải có chữ r cho loại, AccessPolicy
// phải liệt kê loại và (nếu có tiêu chí) doc phải thỏa. doc nil chỉ qua được rule không có tiêu chí. p nil: cho phép
// (chế độ không phân quyền). Dùng khi giao thông báo, nơi không có Repository để Scope kiểm.
func (p *Principal) CanReadResource(rt string, doc map[string]any) bool {
	if p == nil {
		return true
	}
	if !p.Grants.CanRead(rt) || !p.Policy.allows(rt, false) {
		return false
	}
	if rt == jobType {
		return true
	}
	return p.Policy.matchDoc(rt, doc)
}
