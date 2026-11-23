package fhircast

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Subscription là điều Hub nhớ về một subscriber, khóa theo endpoint đã cấp. Version quyết định hình dạng xác nhận.
type Subscription struct {
	ProjectID   string    `json:"projectId"`
	Topic       string    `json:"topic"`
	Events      []string  `json:"events"`
	Version     Version   `json:"version"`
	UserSubject string    `json:"userSubject,omitempty"` // người tạo (để log và để cho phép hủy bởi đúng người)
	ExpiresAt   time.Time `json:"expiresAt,omitzero"`    // hết lease: socket tự đóng
}

// ChannelMessage là thứ được phát lên kênh của topic. Mọi subscriber của topic đọc cùng một kênh, nên thông điệp dành cho
// MỘT subscriber ghi endpoint đó ở Target và những người khác bỏ nó. Chỉ Payload lên dây: địa chỉ không lộ endpoint của
// người này cho người kia.
type ChannelMessage struct {
	Payload map[string]any `json:"payload"`
	Target  string         `json:"target,omitempty"`
}

// MarshalChannelMessage mã hóa thông điệp cho kênh.
func MarshalChannelMessage(payload map[string]any, target string) ([]byte, error) {
	return json.Marshal(ChannelMessage{Payload: payload, Target: target})
}

// MessageForSubscriber quyết định thông điệp trên kênh có tới subscriber (endpoint, sub) không và trả payload để gửi
// nguyên văn. ok=false nghĩa là bỏ: hỏng hoặc thiếu payload, Target của người khác, hoặc sự kiện người này không đăng ký.
// Sự kiện heartbeat và syncerror luôn tới; payload không có hub.event (điều khiển Hub như denied) luôn tới.
func MessageForSubscriber(raw []byte, endpoint string, sub Subscription) (payload map[string]any, ok bool) {
	var m ChannelMessage
	if err := json.Unmarshal(raw, &m); err != nil || m.Payload == nil {
		return nil, false
	}
	if m.Target != "" && m.Target != endpoint {
		return nil, false
	}
	if ev, isMap := m.Payload["event"].(map[string]any); isMap {
		if name, isStr := ev["hub.event"].(string); isStr {
			l := strings.ToLower(name)
			if l != EventHeartbeat && l != EventSyncError && !hasEvent(sub.Events, l) {
				return nil, false
			}
		}
	}
	return m.Payload, true
}

func hasEvent(events []string, lower string) bool {
	for _, e := range events {
		if strings.ToLower(e) == lower {
			return true
		}
	}
	return false
}

// Confirmation là thông điệp xác nhận gửi khi socket kết nối. STU3: đúng bốn trường; STU2 giữ thêm bốn trường rỗng.
func Confirmation(sub Subscription) map[string]any {
	m := map[string]any{
		"hub.mode":          "subscribe",
		"hub.topic":         sub.Topic,
		"hub.events":        strings.Join(sub.Events, ","),
		"hub.lease_seconds": int(DefaultLease / time.Second),
	}
	if sub.Version == STU2 {
		for _, k := range []string{"hub.callback", "hub.channel", "hub.secret", "hub.subscriber"} {
			m[k] = ""
		}
	}
	return m
}

// Denied là thông điệp từ chối/hủy; sau khi gửi Hub đóng socket.
func Denied(topic string, events []string, reason string) map[string]any {
	return map[string]any{"hub.mode": "denied", "hub.topic": topic, "hub.events": strings.Join(events, ","), "hub.reason": reason}
}

// InvalidEndpoint là thông điệp gửi cho socket nối vào endpoint không tồn tại (rồi đóng).
func InvalidEndpoint() map[string]any { return Denied("", nil, "invalid endpoint") }

// Heartbeat tạo thông điệp heartbeat cục bộ cho socket của topic (không qua kênh chung, nên không trùng giữa các instance).
func Heartbeat(topic, id string, now time.Time, period time.Duration) map[string]any {
	return map[string]any{
		"timestamp": now.UTC().Format(time.RFC3339Nano),
		"id":        id,
		"event": map[string]any{
			"context":   []any{map[string]any{"key": "period", "decimal": formatSeconds(period)}},
			"hub.event": EventHeartbeat,
			"hub.topic": topic,
		},
	}
}

func formatSeconds(d time.Duration) string { return strconv.Itoa(int(d / time.Second)) }
