package fhirauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Sealer ký "ảnh chụp" quyền của người gọi (project, grants, chính sách) để gắn vào AsyncJob. Job nền chạy sau khi
// request kết thúc nên cần dựng lại đúng quyền đó; chữ ký (HMAC-SHA256, ràng buộc id job, project và request) chặn việc
// ai đó sửa hoặc tự tạo AsyncJob để nâng quyền. Mọi replica phải dùng chung khóa.
type Sealer struct{ key []byte }

// NewSealer dựng Sealer; khóa phải dài ít nhất 32 byte.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) < 32 {
		return nil, errors.New("fhirauth: khóa ký job phải dài ít nhất 32 byte")
	}
	return &Sealer{key: append([]byte(nil), key...)}, nil
}

// sealVersion tăng khi ý nghĩa của các bit Perm đổi. Bản 2: Perm tách theo chữ SMART v2 (create, read, update, delete, search);
// ảnh chụp quyền cũ (không có trường v) dùng hai bit gộp đọc/ghi nên KHÔNG được hiểu bằng bit mới (job cũ bị từ chối, an toàn).
const sealVersion = 2

type sealPayload struct {
	Version int            `json:"v"`
	Subject string         `json:"sub"`
	Grants  map[string]int `json:"grants"` // null: không giới hạn theo loại
	Policy  any            `json:"policy"` // null: không có chính sách
}

func (s *Sealer) mac(jobID, project, request string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	for _, part := range [][]byte{[]byte(jobID), []byte(project), []byte(request), payload} {
		m.Write(part)
		m.Write([]byte{0})
	}
	return m.Sum(nil)
}

// Seal tạo chuỗi đã ký cho job. p nil (không có người gọi) cho ra ảnh chụp không có quyền gì (grants rỗng).
func (s *Sealer) Seal(jobID, request string, p *Principal) string {
	pl := sealPayload{Version: sealVersion, Grants: map[string]int{}}
	project := ""
	if p != nil {
		project, pl.Subject = p.Project, p.Subject
		if p.Grants != nil {
			for k, v := range p.Grants {
				pl.Grants[k] = int(v)
			}
		} else {
			pl.Grants = nil
		}
		if p.Policy != nil {
			pl.Policy = p.Policy.raw
		}
	}
	raw, _ := json.Marshal(pl)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(s.mac(jobID, project, request, raw))
}

// Open kiểm chữ ký và dựng lại Principal. project là project thật của AsyncJob.
func (s *Sealer) Open(jobID, project, request, sealed string) (*Principal, error) {
	a, b, ok := strings.Cut(sealed, ".")
	if !ok {
		return nil, errors.New("job thiếu hoặc sai chữ ký quyền")
	}
	raw, err1 := base64.RawURLEncoding.DecodeString(a)
	sig, err2 := base64.RawURLEncoding.DecodeString(b)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, s.mac(jobID, project, request, raw)) {
		return nil, errors.New("job thiếu hoặc sai chữ ký quyền")
	}
	var pl sealPayload
	if err := json.Unmarshal(raw, &pl); err != nil {
		return nil, errors.New("job có ảnh chụp quyền hỏng")
	}
	if pl.Version != sealVersion {
		return nil, errors.New("job có ảnh chụp quyền phiên bản cũ, hãy chạy lại")
	}
	p := &Principal{Subject: "job:" + jobID, Project: project, Role: RoleRead, Job: true, OnBehalfOf: pl.Subject}
	if pl.Grants != nil {
		p.Grants = Grants{}
		for k, v := range pl.Grants {
			p.Grants[k] = Perm(v)
		}
		p.Grants["Binary"] |= PermWrite // chỉ ghi/xóa, không đọc: để lưu và dọn file kết quả
	}
	if pl.Policy != nil {
		pol, err := ParsePolicy(pl.Policy)
		if err != nil {
			return nil, errors.New("job có chính sách hỏng")
		}
		pol.jobBinary = true
		p.Policy = pol
	}
	return p, nil
}
