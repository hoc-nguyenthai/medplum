package fhircast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Error là lỗi của Hub kèm mã HTTP; tầng HTTP đổi thành OperationOutcome với Message ở issue[0].details.text.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func badRequest(msg string) *Error { return &Error{Status: 400, Message: msg} }
func badRequestf(f string, a ...any) *Error {
	return &Error{Status: 400, Message: fmt.Sprintf(f, a...)}
}

var errInternal = &Error{Status: 500, Message: "Internal server error"}

// Hub là máy trạng thái của FHIRcast trên Store và Bus: đăng ký/hủy websocket và đổi ngữ cảnh. Không biết HTTP hay socket.
type Hub struct {
	Store Store
	Bus   Bus
	// Lease là hạn một đăng ký (mặc định DefaultLease).
	Lease time.Duration
	// MaxUpdateEntries là số entry tối đa của update bundle (mặc định 200).
	MaxUpdateEntries int
	// Now cho test; mặc định time.Now.
	Now func() time.Time
}

func (h *Hub) lease() time.Duration {
	if h.Lease <= 0 {
		return DefaultLease
	}
	return h.Lease
}

func (h *Hub) maxEntries() int {
	if h.MaxUpdateEntries <= 0 {
		return 200
	}
	return h.MaxUpdateEntries
}

func (h *Hub) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// ---------------------------------------------------------------- đăng ký / hủy

// SubscribeRequest là yêu cầu đăng ký hoặc hủy (các khóa phẳng hub.*).
type SubscribeRequest struct {
	ChannelType     string // hub.channel.type
	Mode            string // hub.mode
	Topic           string // hub.topic
	Events          string // hub.events
	ChannelEndpoint string // hub.channel.endpoint (URL đã cấp)
	LegacyEndpoint  string // endpoint (client trước @medplum/core 5.1.29)
}

// Subscribe xử lý yêu cầu đăng ký hoặc hủy của người dùng user trong project. Đăng ký: trả URL websocket (wsBase là
// ws(s)://host, không có dấu "/" cuối); mỗi lần đăng ký sinh một endpoint mới, không chứa topic. Hủy: trả endpoint đã hủy
// và báo "denied" chỉ cho đúng socket đó.
func (h *Hub) Subscribe(ctx context.Context, project, user string, version Version, wsBase string, r SubscribeRequest) (string, error) {
	switch {
	case r.ChannelType == "":
		return "", badRequest("Missing hub.channel.type")
	case r.Mode == "":
		return "", badRequest("Missing hub.mode")
	case r.Topic == "":
		return "", badRequest("Missing hub.topic")
	}
	if r.ChannelType != "websocket" { // chỉ websocket: webhook/rest-hook/email bị từ chối, Hub không bao giờ gọi ra ngoài
		return "", badRequest("Invalid hub.channel.type")
	}
	if r.Mode != "subscribe" && r.Mode != "unsubscribe" {
		return "", badRequest("Invalid hub.mode")
	}
	if !ValidTopic(r.Topic) {
		return "", badRequest("Invalid hub.topic")
	}
	if r.Mode == "unsubscribe" {
		return h.unsubscribe(ctx, project, user, r)
	}
	if r.Events == "" {
		return "", badRequest("Missing hub.events")
	}
	events := ParseEvents(r.Events)
	if len(events) == 0 {
		return "", badRequest("Invalid hub.events")
	}
	for _, e := range events {
		if !IsSupportedEvent(e) {
			return "", badRequest("Invalid hub.events")
		}
	}
	endpoint := NewID()
	sub := Subscription{ProjectID: project, Topic: r.Topic, Events: events, Version: version, UserSubject: user, ExpiresAt: h.now().Add(h.lease())}
	if err := h.Store.PutSubscription(ctx, endpoint, sub, h.lease()); err != nil {
		slog.ErrorContext(ctx, "fhircast: không lưu được đăng ký", "error", err)
		return "", &Error{Status: 500, Message: "Failed to create subscription for topic"}
	}
	return strings.TrimRight(wsBase, "/") + "/ws/fhircast/" + endpoint, nil
}

func (h *Hub) unsubscribe(ctx context.Context, project, user string, r SubscribeRequest) (string, error) {
	endpoint := ExtractEndpoint(r.ChannelEndpoint)
	if endpoint == "" {
		endpoint = ExtractEndpoint(r.LegacyEndpoint)
		if endpoint != "" {
			slog.InfoContext(ctx, "fhircast: hủy bằng trường endpoint cũ")
		}
	}
	if endpoint == "" {
		return "", badRequest("Missing endpoint")
	}
	sub, ok, err := h.Store.GetSubscription(ctx, endpoint)
	if err != nil {
		slog.ErrorContext(ctx, "fhircast: không đọc được đăng ký", "error", err)
		return "", errInternal
	}
	// Không phân biệt "không có", "khác project", "khác topic", "của người khác": đều là endpoint không hợp lệ.
	if !ok || sub.ProjectID != project || sub.Topic != r.Topic || (sub.UserSubject != "" && user != "" && sub.UserSubject != user) {
		return "", badRequest("Invalid endpoint")
	}
	if err := h.Store.DeleteSubscription(ctx, endpoint); err != nil {
		slog.ErrorContext(ctx, "fhircast: không xóa được đăng ký", "error", err)
		return "", errInternal
	}
	msg, err := MarshalChannelMessage(Denied(sub.Topic, sub.Events, "Subscriber unsubscribed from topic"), endpoint)
	if err == nil {
		err = h.Bus.Publish(ctx, Channel(project, sub.Topic), msg)
	}
	if err != nil { // đăng ký đã xóa; socket sẽ bị đóng khi hết lease
		slog.WarnContext(ctx, "fhircast: không báo được denied cho socket đã hủy", "error", err)
	}
	return endpoint, nil
}

// ---------------------------------------------------------------- đổi ngữ cảnh

// ValidateEvent kiểm hình dạng thông điệp đổi ngữ cảnh {id, timestamp, event{hub.topic, hub.event, context}} và trả
// (topic, tên sự kiện). Thông điệp lỗi giữ đúng chuỗi của Medplum.
func ValidateEvent(p map[string]any) (topic, name string, err error) {
	if s, _ := p["id"].(string); s == "" {
		return "", "", badRequest("Missing event ID")
	}
	if s, _ := p["timestamp"].(string); s == "" {
		return "", "", badRequest("Missing event timestamp")
	}
	ev, ok := p["event"].(map[string]any)
	if !ok {
		return "", "", badRequest("Missing event payload")
	}
	topic, _ = ev["hub.topic"].(string)
	if topic == "" {
		return "", "", badRequest(`Missing event["hub.topic"]`)
	}
	name, _ = ev["hub.event"].(string)
	if name == "" {
		return "", "", badRequest(`Missing event["hub.event"]`)
	}
	if ev["context"] == nil {
		return "", "", badRequest("Missing event.context")
	}
	if _, ok := toItems(ev["context"]); !ok {
		return "", "", badRequest("Invalid event.context")
	}
	if !ValidTopic(topic) {
		return "", "", badRequest(`Invalid event["hub.topic"]`)
	}
	if !IsSupportedEvent(name) {
		return "", "", badRequest(`Invalid event["hub.event"]`)
	}
	return topic, name, nil
}

// ChangeContext xử lý một yêu cầu đổi ngữ cảnh của project rồi phát thông điệp (đã bổ sung context.versionId) cho mọi
// subscriber của topic. Trả thông điệp đã phát (cho phản hồi 202). payload không bị sửa.
func (h *Hub) ChangeContext(ctx context.Context, project string, payload map[string]any) (map[string]any, error) {
	p, err := clone(payload)
	if err != nil {
		return nil, badRequest("Invalid event payload")
	}
	topic, name, err := ValidateEvent(p)
	if err != nil {
		return nil, err
	}
	ev := p["event"].(map[string]any)
	switch EventCategory(name) {
	case CategoryOpen:
		err = h.open(ctx, project, topic, name, ev)
	case CategoryClose:
		err = h.close(ctx, project, topic, ev)
	case CategoryUpdate:
		err = h.update(ctx, project, topic, ev)
	}
	if err != nil {
		return nil, wrap(ctx, err)
	}
	raw, err := MarshalChannelMessage(p, "")
	if err == nil {
		err = h.Bus.Publish(ctx, Channel(project, topic), raw)
	}
	if err != nil {
		return nil, wrap(ctx, err)
	}
	return p, nil
}

// wrap giữ lỗi của Hub; mọi lỗi khác (hạ tầng) chỉ được log, người gọi nhận 500 chung, riêng ngữ cảnh quá lớn là 400.
func wrap(ctx context.Context, err error) error {
	var he *Error
	if errors.As(err, &he) {
		return he
	}
	if errors.Is(err, ErrContextTooLarge) {
		return badRequest("Context exceeds the maximum size")
	}
	if errors.Is(err, ErrTooManyContexts) {
		return badRequest("Too many stored contexts for this topic")
	}
	slog.ErrorContext(ctx, "fhircast: lỗi hạ tầng khi đổi ngữ cảnh", "error", err)
	return errInternal
}

func (h *Hub) open(ctx context.Context, project, topic, name string, ev map[string]any) error {
	anchor, err := ExtractAnchorType(name)
	if err != nil {
		return badRequest(err.Error())
	}
	items, _ := toItems(ev["context"])

	cur, ok, err := h.Store.GetContext(ctx, project, topic)
	if err != nil {
		return err
	}
	// Ngữ cảnh hiện tại là DiagnosticReport thì cất lại để khôi phục khi mở lại báo cáo đó.
	if ok && cur.Type == "DiagnosticReport" {
		id := resourceID(findResource(cur.Context, "report"), "DiagnosticReport")
		if id == "" {
			return badRequest("No DiagnosticReport currently open for this topic")
		}
		if err := h.Store.SaveReportContext(ctx, project, topic, id, cur); err != nil {
			return err
		}
	}
	// Đã từng mở báo cáo này thì khôi phục ngữ cảnh đã lưu, giữ context.versionId.
	if id, _ := findResource(items, "report")["id"].(string); id != "" {
		stored, found, err := h.Store.GetReportContext(ctx, project, topic, id)
		if err != nil {
			return err
		}
		if found {
			if err := h.Store.SetContext(ctx, project, topic, stored); err != nil {
				return err
			}
			ev["context.versionId"] = stored.VersionID
			return nil
		}
	}
	version := NewID()
	ev["context.versionId"] = version
	next := CurrentContext{Type: anchor, VersionID: version, Context: items}
	if anchor == "DiagnosticReport" {
		// Hub hỗ trợ chia sẻ nội dung thì luôn có đúng một Bundle `collection` ở khóa content (rỗng nếu chưa có gì).
		next.Context = append(append([]map[string]any{}, items...), map[string]any{
			"key":      "content",
			"resource": map[string]any{"id": NewID(), "resourceType": "Bundle", "type": "collection"},
		})
	}
	return h.Store.SetContext(ctx, project, topic, next)
}

func (h *Hub) close(ctx context.Context, project, topic string, ev map[string]any) error {
	items, _ := toItems(ev["context"])
	// Luôn đóng ngữ cảnh hiện tại, không kiểm đúng tài nguyên (như Medplum).
	if err := h.Store.ClearContext(ctx, project, topic); err != nil {
		return err
	}
	if id, _ := findResource(items, "report")["id"].(string); id != "" {
		return h.Store.DeleteReportContext(ctx, project, topic, id)
	}
	return nil
}

func (h *Hub) update(ctx context.Context, project, topic string, ev map[string]any) error {
	cur, ok, err := h.Store.GetContext(ctx, project, topic)
	if err != nil {
		return err
	}
	if !ok || cur.Type != "DiagnosticReport" {
		return badRequest("No DiagnosticReport currently open for this topic")
	}
	prior := cur.VersionID
	got, _ := ev["context.versionId"].(string)
	if got != prior {
		if got == "" {
			return badRequestf("Expected event.context.versionId to be '%s', received empty event.context.versionId", prior)
		}
		return badRequestf("Expected event.context.versionId to be '%s', received '%s'", prior, got)
	}
	items, _ := toItems(ev["context"])
	updates := findResource(items, "updates")
	if updates == nil {
		return badRequest("Update event requires an update bundle in the context")
	}
	if err := h.applyUpdates(updates, &cur); err != nil {
		return err
	}
	next := NewID()
	ev["context.priorVersionId"] = prior
	ev["context.versionId"] = next
	cur.VersionID = next
	// Chỉ ghi có điều kiện mới giữ đúng chuỗi phiên bản: update khác có thể đã chen vào giữa lúc đọc và ghi.
	applied, err := h.Store.CompareAndSetContext(ctx, project, topic, prior, cur)
	if err != nil {
		return err
	}
	if !applied {
		return &Error{Status: 409, Message: fmt.Sprintf("Context for this topic changed while the update was being applied, it is no longer at version '%s'", prior)}
	}
	return nil
}

// applyUpdates áp từng entry của update bundle lên bundle `content` của cur (cur là bản sao riêng, lỗi giữa chừng không
// lưu gì).
func (h *Hub) applyUpdates(updates map[string]any, cur *CurrentContext) error {
	content := findResource(cur.Context, "content")
	if content == nil { // dữ liệu cũ thiếu content: dựng lại Bundle rỗng
		content = map[string]any{"id": NewID(), "resourceType": "Bundle", "type": "collection"}
		cur.Context = append(cur.Context, map[string]any{"key": "content", "resource": content})
	}
	entries, _ := updates["entry"].([]any)
	if len(entries) > h.maxEntries() {
		return badRequestf("Update bundle has too many entries (maximum %d)", h.maxEntries())
	}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		req, _ := entry["request"].(map[string]any)
		method, hasMethod := req["method"].(string)
		switch {
		case !hasMethod || method == "":
			return badRequest("Update bundle contains entry with a missing request.method")
		case method == "PUT":
			res, ok := entry["resource"].(map[string]any)
			if !ok {
				return badRequest("Missing resource in a update bundle PUT entry")
			}
			putEntry(content, res)
		case method == "DELETE":
			if err := deleteEntry(entry, content, cur); err != nil {
				return err
			}
		default:
			return badRequestf("Update bundle contains bad entry with request.method = '%s', only 'PUT' and 'DELETE' are supported by an update event", method)
		}
	}
	return nil
}

func putEntry(content, res map[string]any) {
	entries, _ := content["entry"].([]any)
	if id, _ := res["id"].(string); id != "" {
		for _, e := range entries {
			if m, _ := e.(map[string]any); m != nil {
				if r, _ := m["resource"].(map[string]any); r != nil && r["id"] == id {
					m["resource"] = res
					return
				}
			}
		}
	}
	content["entry"] = append(entries, map[string]any{"resource": res})
}

func deleteEntry(entry, content map[string]any, cur *CurrentContext) error {
	full, _ := entry["fullUrl"].(string)
	if full == "" {
		return badRequest("DELETE entry in update bundle missing fullUrl")
	}
	parts := strings.Split(full, "/") // như resolveId của Medplum: phần tử thứ hai của "Loại/id"
	if len(parts) < 2 || parts[1] == "" {
		return badRequest("fullUrl in DELETE entry is not a resolvable reference string")
	}
	id := parts[1]
	entries, _ := content["entry"].([]any)
	kept := make([]any, 0, len(entries))
	for _, e := range entries {
		if m, _ := e.(map[string]any); m != nil {
			if r, _ := m["resource"].(map[string]any); r != nil && r["id"] == id {
				continue
			}
		}
		kept = append(kept, e)
	}
	if len(kept) != len(entries) { // luôn xóa được thứ nằm trong content
		content["entry"] = kept
		return nil
	}
	for _, it := range cur.Context {
		if r, _ := it["resource"].(map[string]any); r != nil && r["id"] == id {
			return badRequest("Cannot delete a resource that is part of the original open context")
		}
	}
	return badRequest("Cannot delete resource not currently in the content bundle")
}

// ---------------------------------------------------------------- GET ngữ cảnh

// CurrentContext đọc ngữ cảnh hiện tại của topic trong project.
func (h *Hub) CurrentContext(ctx context.Context, project, topic string) (CurrentContext, bool, error) {
	if !ValidTopic(topic) {
		return CurrentContext{}, false, badRequest("Invalid hub.topic")
	}
	cc, ok, err := h.Store.GetContext(ctx, project, topic)
	if err != nil {
		slog.ErrorContext(ctx, "fhircast: không đọc được ngữ cảnh", "error", err)
		return CurrentContext{}, false, errInternal
	}
	return cc, ok, nil
}

// ContextResponse là thân phản hồi GET /{topic}: STU2 trả mảng context (hoặc [] khi không có: mở rộng không chuẩn cho
// Nuance PowerCast Hub); STU3 trả đối tượng đầy đủ (hoặc {"context.type":"","context":[]}).
func ContextResponse(v Version, cc CurrentContext, ok bool) any {
	if !ok {
		if v == STU2 {
			return []any{}
		}
		return map[string]any{"context.type": "", "context": []any{}}
	}
	if cc.Context == nil {
		cc.Context = []map[string]any{}
	}
	if v == STU2 {
		return cc.Context
	}
	return map[string]any{"context.type": cc.Type, "context": cc.Context, "context.versionId": cc.VersionID}
}

// ---------------------------------------------------------------- tiện ích

func clone(m map[string]any) (map[string]any, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return nil, errors.New("payload không phải đối tượng")
	}
	return out, nil
}

// toItems đổi event.context (mảng) thành danh sách {key, resource...}; phần tử không phải đối tượng là không hợp lệ.
func toItems(v any) ([]map[string]any, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, false
		}
		out = append(out, m)
	}
	return out, true
}

// findResource trả resource của phần tử đầu tiên có khóa key, nil nếu không có.
func findResource(items []map[string]any, key string) map[string]any {
	for _, it := range items {
		if it["key"] == key {
			r, _ := it["resource"].(map[string]any)
			return r
		}
	}
	return nil
}

// resourceID trả id của r nếu r là tài nguyên loại typ có id chuỗi, ngược lại "".
func resourceID(r map[string]any, typ string) string {
	if r == nil || r["resourceType"] != typ {
		return ""
	}
	id, _ := r["id"].(string)
	return id
}
