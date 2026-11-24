package dicomweb

import "strconv"

// ParseQueryInt đọc tham số phân trang của QIDO-RS (`limit`, `offset`) thành số nguyên không âm. Thiếu, rỗng hoặc không hợp lệ trả (0, false) để
// người gọi dùng mặc định của server. Chặt hơn Medplum (parseInt cho "12abc" ra 12): chỉ nhận chuỗi toàn chữ số, dấu + hoặc - đều bị từ chối.
func ParseQueryInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
