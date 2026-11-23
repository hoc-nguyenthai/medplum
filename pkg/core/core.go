// Package core chứa lớp nền DDD dùng chung: ID, domain event, aggregate và lỗi nghiệp vụ.
package core

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// NewID trả về ID ngẫu nhiên 32 ký tự hex (UUID v4 không có dấu gạch).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand không bao giờ lỗi trên nền tảng được hỗ trợ
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return hex.EncodeToString(b[:])
}

// Now trả về thời điểm hiện tại theo UTC.
func Now() time.Time { return time.Now().UTC() }

// Event là domain event. Event bất biến; topic mặc định trên broker = EventType().
type Event interface {
	EventID() string
	EventType() string
	OccurredAtTime() time.Time
}

// BaseEvent nhúng vào mọi event cụ thể (event còn lại phải tự cài EventType).
type BaseEvent struct {
	ID         string    `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewBaseEvent tạo BaseEvent với ID mới và thời điểm hiện tại.
func NewBaseEvent() BaseEvent { return BaseEvent{ID: NewID(), OccurredAt: Now()} }

func (e BaseEvent) EventID() string           { return e.ID }
func (e BaseEvent) OccurredAtTime() time.Time { return e.OccurredAt }

// EventSource là thứ UnitOfWork phát event sau khi commit.
type EventSource interface {
	PullEvents() []Event
}

// Aggregate nhúng vào aggregate root để ghi lại domain event.
type Aggregate struct {
	events []Event
}

// Record ghi một event; UnitOfWork sẽ phát nó sau khi commit thành công.
func (a *Aggregate) Record(e Event) { a.events = append(a.events, e) }

// PullEvents trả về và xóa các event đã ghi.
func (a *Aggregate) PullEvents() []Event {
	events := a.events
	a.events = nil
	return events
}

// Kind phân loại lỗi nghiệp vụ; tầng presentation map sang HTTP status.
type Kind int

const (
	KindBusiness   Kind = iota // quy tắc nghiệp vụ bị vi phạm (400)
	KindNotFound               // 404
	KindConflict               // 409
	KindValidation             // dữ liệu đầu vào sai (422)
)

func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindValidation:
		return "validation"
	default:
		return "business"
	}
}

// Error là lỗi nghiệp vụ gốc.
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(k Kind, format string, args ...any) *Error {
	return &Error{Kind: k, Message: fmt.Sprintf(format, args...)}
}

// Business, NotFound, Conflict, Invalid tạo lỗi theo từng loại.
func Business(format string, args ...any) error { return newError(KindBusiness, format, args...) }
func NotFound(format string, args ...any) error { return newError(KindNotFound, format, args...) }
func Conflict(format string, args ...any) error { return newError(KindConflict, format, args...) }
func Invalid(format string, args ...any) error  { return newError(KindValidation, format, args...) }

// AsError trả về *Error nếu err (hoặc lỗi bọc bên trong) là lỗi nghiệp vụ.
func AsError(err error) (*Error, bool) {
	var de *Error
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}

// IsKind báo err có phải lỗi nghiệp vụ loại k không.
func IsKind(err error, k Kind) bool {
	de, ok := AsError(err)
	return ok && de.Kind == k
}

// MarshalJSON mã hóa v thành JSON UTF-8 không escape HTML (&, <, > giữ nguyên, tiếng Việt giữ nguyên);
// indent=true thì thụt 2 khoảng trắng. Không có dòng xuống cuối.
func MarshalJSON(v any, indent bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
