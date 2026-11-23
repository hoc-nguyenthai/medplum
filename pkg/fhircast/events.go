// Package fhircast là phần dùng chung của Hub FHIRcast (đồng bộ ngữ cảnh giữa các ứng dụng lâm sàng): kiểu, tên sự kiện,
// lọc thông điệp theo người nhận và kho trạng thái. Không có HTTP, WebSocket hay phụ thuộc vào pkg/* khác: tầng giao tiếp
// nằm ở app. Hub chỉ có kênh websocket, không có callback đi ra (không webhook), nên không bao giờ gọi HTTP/mạng ra ngoài.
package fhircast

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Version là phiên bản giao thức Hub, quyết định hình dạng thông điệp xác nhận và GET ngữ cảnh.
type Version string

const (
	STU2 Version = "STU2"
	STU3 Version = "STU3"
)

// Valid cho biết v là phiên bản được hỗ trợ.
func (v Version) Valid() bool { return v == STU2 || v == STU3 }

// Hạn mặc định (đều cấu hình được ở nơi dùng).
const (
	DefaultLease      = time.Hour      // hạn một đăng ký và lease báo cho subscriber
	DefaultContextTTL = 24 * time.Hour // hạn ngữ cảnh hiện tại: Medplum không hạn (PHI tồn vô hạn), Go có hạn
	MaxTopicBytes     = 256            // topic là chuỗi tự do từ client
	HeartbeatPeriod   = 10 * time.Second
)

// Tên sự kiện đặc biệt luôn tới mọi subscriber bất kể đã đăng ký gì.
const (
	EventHeartbeat = "heartbeat"
	EventSyncError = "syncerror"
)

// SupportedEvents là 14 sự kiện Hub công bố (`eventsSupported`, giữ đúng thứ tự của Medplum).
var SupportedEvents = []string{
	"syncerror", "heartbeat", "userlogout", "userhibernate",
	"Patient-open", "Patient-close",
	"ImagingStudy-open", "ImagingStudy-close",
	"Encounter-open", "Encounter-close",
	"DiagnosticReport-open", "DiagnosticReport-close", "DiagnosticReport-select", "DiagnosticReport-update",
}

// IsSupportedEvent: tên có nằm trong danh sách công bố không (không phân biệt hoa thường).
func IsSupportedEvent(name string) bool {
	for _, e := range SupportedEvents {
		if strings.EqualFold(e, name) {
			return true
		}
	}
	return false
}

// ParseEvents tách `hub.events` (dấu phẩy). Khoảng trắng quanh tên được bỏ nhưng không bao giờ ngăn cách hai sự kiện
// ("Patient-open Patient-close" là MỘT tên); phần rỗng bị bỏ.
func ParseEvents(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ExtractEndpoint lấy endpoint từ URL `hub.channel.endpoint` mà subscriber gửi lại khi hủy: bỏ query, fragment và dấu "/"
// thừa. Trả "" nếu không có đoạn đường dẫn nào.
func ExtractEndpoint(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	parts := strings.Split(u, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}

// Category là loại tác động của một sự kiện lên ngữ cảnh.
type Category string

const (
	CategoryOpen   Category = "open"
	CategoryClose  Category = "close"
	CategoryUpdate Category = "update"
	CategorySelect Category = "select"
	CategoryOther  Category = "other"
)

// EventCategory phân loại tên sự kiện, không phân biệt hoa thường: Home-open là other; *-open là open; *-close là close;
// đúng diagnosticreport-update là update; *-select là select; còn lại là other.
func EventCategory(name string) Category {
	n := strings.ToLower(name)
	switch {
	case n == "home-open":
		return CategoryOther
	case strings.HasSuffix(n, "-open"):
		return CategoryOpen
	case strings.HasSuffix(n, "-close"):
		return CategoryClose
	case n == "diagnosticreport-update":
		return CategoryUpdate
	case strings.HasSuffix(n, "-select"):
		return CategorySelect
	}
	return CategoryOther
}

// ErrInvalidAnchor: tên sự kiện không neo vào loại tài nguyên được hỗ trợ.
var ErrInvalidAnchor = errors.New("Invalid anchor resource type")

var anchors = map[string]string{
	"patient": "Patient", "imagingstudy": "ImagingStudy", "encounter": "Encounter", "diagnosticreport": "DiagnosticReport",
}

// ExtractAnchorType lấy loại tài nguyên neo từ phần trước dấu "-" đầu tiên của tên sự kiện.
func ExtractAnchorType(event string) (string, error) {
	head, _, _ := strings.Cut(event, "-")
	if t, ok := anchors[strings.ToLower(head)]; ok {
		return t, nil
	}
	return "", ErrInvalidAnchor
}

// ValidTopic: không rỗng, không quá MaxTopicBytes, không ký tự điều khiển (topic đi vào log và khóa).
func ValidTopic(topic string) bool {
	if topic == "" || len(topic) > MaxTopicBytes {
		return false
	}
	for _, r := range topic {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// TopicHash là dạng của topic dùng trong khóa kho và tên kênh: topic có thể chứa ":" hay rất dài, nên không đưa thẳng vào khóa.
func TopicHash(topic string) string {
	h := sha256.Sum256([]byte(topic))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// NewID sinh UUID v4 từ crypto/rand: dùng cho endpoint (là bí mật, 122 bit ngẫu nhiên) và topic của người dùng.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("fhircast: crypto/rand lỗi: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
