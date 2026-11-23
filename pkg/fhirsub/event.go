// Package fhirsub triển khai Subscription kênh rest-hook: bọc Repository để phát sự kiện thay đổi (sau khi commit),
// khớp sự kiện với tiêu chí của các Subscription đang hoạt động, và giao thông báo qua HTTP POST có retry.
//
// Tương thích hành vi rest-hook của Medplum: body là resource (hoặc {} khi xóa), các header X-Medplum-Subscription,
// X-Medplum-Interaction, X-Medplum-Deleted-Resource, header tùy biến của channel.header, chữ ký HMAC-SHA256 trong
// X-Signature nếu Subscription có extension subscription-secret, và lọc theo extension subscription-supported-interaction.
//
// Kênh websocket: xem ws.go. Chưa hỗ trợ: email, sms; tiêu chí FHIRPath mở rộng; giao hàng bền vững qua restart
// (hàng đợi giao nằm trong bộ nhớ; sự kiện đi qua broker nên với Redis Streams không mất sự kiện chưa xử lý).
package fhirsub

import (
	"encoding/json"

	"trivita-fhir/pkg/core"
)

// EventType là topic của sự kiện thay đổi resource trên broker.
const EventType = "fhir.resource.changed"

// Các giá trị Interaction.
const (
	Create = "create"
	Update = "update"
	Delete = "delete"
)

// ChangeEvent là sự kiện một resource vừa được ghi. Resource là JSON của phiên bản vừa ghi;
// với delete là phiên bản cuối cùng trước khi xóa (để khớp tiêu chí và để người nhận biết đã xóa gì).
type ChangeEvent struct {
	core.BaseEvent
	Interaction  string          `json:"interaction"`
	ResourceType string          `json:"resource_type"`
	ID           string          `json:"id"`
	VersionID    string          `json:"version_id"`
	Resource     json.RawMessage `json:"resource,omitempty"`
}

// EventType triển khai core.Event.
func (ChangeEvent) EventType() string { return EventType }
