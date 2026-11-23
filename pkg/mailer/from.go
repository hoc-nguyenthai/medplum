package mailer

import "strings"

// FromPolicy là chính sách chọn người gửi: Approved là danh sách địa chỉ trần (chữ thường) mà người gọi được đề nghị, Default là người gửi mặc định
// (địa chỉ trần hoặc "Tên <địa chỉ>").
type FromPolicy struct {
	Approved []string
	Default  string
}

// SplitApproved tách danh sách phân cách bằng dấu phẩy: cắt khoảng trắng, hạ chữ thường, bỏ mục rỗng. (Medplum không cắt khoảng trắng nên mục thứ hai của
// "a@x.com, b@x.com" không bao giờ khớp: sửa có chủ ý.)
func SplitApproved(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ResolveFrom chọn người gửi: địa chỉ đề nghị (kể cả tên hiển thị do người gọi đặt) chỉ được dùng khi phần địa chỉ trần nằm trong danh sách được duyệt
// (không phân biệt hoa thường); ngược lại là Default. Danh sách của server không áp cho SMTP của project và ngược lại: người gọi truyền đúng chính sách.
func ResolveFrom(requested *Address, p FromPolicy) Address {
	if requested != nil && requested.Email != "" {
		e := strings.ToLower(strings.TrimSpace(requested.Email))
		for _, a := range p.Approved {
			if e == a {
				return *requested
			}
		}
	}
	if a, err := ParseAddress(p.Default); err == nil {
		return a
	}
	return Address{Email: strings.TrimSpace(p.Default)}
}

// ApplyDisplayName gắn tên ứng dụng làm tên hiển thị khi người gửi chưa có tên. Chỉ nên gọi khi dùng SMTP riêng của project: tên hiển thị khác miền người gửi là
// dấu hiệu lừa đảo nên không gắn appName vào địa chỉ của server.
func ApplyDisplayName(from Address, appName string) Address {
	appName = strings.TrimSpace(appName)
	if appName == "" || from.Name != "" || CheckHeaderValue(appName) != nil {
		return from
	}
	return Address{Name: appName, Email: from.Email}
}
