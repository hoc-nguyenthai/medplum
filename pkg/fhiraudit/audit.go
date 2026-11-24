// Package fhiraudit ghi AuditEvent (kiểm toán) vào kho cho các thao tác REST trên repository: create, update, delete (mặc định) và
// read, vread, history, search (tùy chọn). H-01 của docs/plans/modules/fhir-parity-audit.md.
//
// Repo bọc repository phía người gọi (ngoài Scope và Guard), nên chỉ thao tác ĐÃ được cho phép và thành công mới có AuditEvent: yêu cầu bị từ chối
// (401, 403, 404) không có, chúng chỉ là dòng log. AuditEvent được ghi bằng repository `Sink` (không tách theo người gọi) với meta.project là project
// của người gọi, người gọi (Principal.Subject, super-admin được đánh dấu) và địa chỉ IP lấy từ WithClient.
//
// Trong transaction Bundle các AuditEvent được gom lại và chỉ ghi SAU KHI commit thành công (transaction hủy thì không có gì để kiểm toán).
// Ghi AuditEvent lỗi không làm hỏng yêu cầu (đã thành công rồi) mà chỉ ghi log mức error: kiểm toán "mở khi lỗi", xem tài liệu runbook.
//
// Không kiểm toán chính AuditEvent (tránh đệ quy và nổ khối lượng) và AsyncJob (cập nhật trạng thái job rất dày). AuditEvent không sửa, không xóa được
// qua repository này (403): muốn dọn theo chính sách lưu giữ phải dùng $expunge của quản trị.
package fhiraudit

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// Mode là mức kiểm toán.
type Mode string

const (
	// Off không ghi gì (không nên bọc repository).
	Off Mode = "off"
	// Writes ghi create, update, delete.
	Writes Mode = "writes"
	// All ghi thêm read, vread, history, search.
	All Mode = "all"
)

// ParseMode phân tích giá trị cấu hình; rỗng là Off.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "", Off:
		return Off, nil
	case Writes, All:
		return Mode(s), nil
	}
	return Off, fmt.Errorf("fhiraudit: chế độ %q không hợp lệ (off, writes hoặc all)", s)
}

// Client là thông tin về nơi gọi, do lớp HTTP gắn vào context.
type Client struct {
	Address string // địa chỉ IP của người gọi
}

type clientKey struct{}

// WithClient gắn thông tin nơi gọi vào context để Repo ghi vào agent.network.
func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

func clientFrom(ctx context.Context) Client {
	c, _ := ctx.Value(clientKey{}).(Client)
	return c
}

// Options cấu hình Repo.
type Options struct {
	// Mode là mức kiểm toán (Writes hoặc All).
	Mode Mode
	// Sink là repository ghi AuditEvent: không tách theo người gọi (không qua Scope); meta.project được đặt rõ ràng.
	Sink fhirrouter.Repository
	// Observer tên hệ thống ghi sự kiện (AuditEvent.source.observer.display), ví dụ tên service.
	Observer string
	// Now (tùy chọn) thay đồng hồ, dùng trong test.
	Now func() time.Time
}

// anonymous là chủ thể khi không có Principal (xác thực tắt).
const anonymous = "anonymous"

// SubjectSystem là identifier.system của agent.who.
const SubjectSystem = "urn:trivita:subject"

const (
	typeSystem        = "http://terminology.hl7.org/CodeSystem/audit-event-type"
	interactionSystem = "http://hl7.org/fhir/restful-interaction"
)

var skipTypes = map[string]bool{"AuditEvent": true, "AsyncJob": true}

// Repo là decorator Repository ghi AuditEvent. Nhúng *Guard để chuyển tiếp mọi giao diện tùy chọn của repository bên trong (Transactional, KeyLocker,
// Reindexer, Expunger, ConceptStore...) giống Guard; Guard ở đây không lọc gì (allowed luôn đúng) hoặc là Guard thật của container.
type Repo struct {
	*fhirrouter.Guard
	o   Options
	buf *buffer // khác nil trong transaction
}

type buffer struct {
	mu     sync.Mutex
	events []pending
}

type pending struct {
	ctx         context.Context
	interaction string
	rt, id      string
	query       string
	vid         string
}

// Wrap bọc repo. repo nên là repository phía người gọi (đã qua Scope). Mode Off hoặc Sink nil: trả repo nguyên trạng.
func Wrap(repo fhirrouter.Repository, o Options) fhirrouter.Repository {
	if o.Mode == Off || o.Mode == "" || o.Sink == nil {
		return repo
	}
	g, ok := repo.(*fhirrouter.Guard)
	if !ok {
		g = fhirrouter.NewGuard(repo, func(context.Context, string) bool { return true }).(*fhirrouter.Guard)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Repo{Guard: g, o: o}
}

var (
	_ fhirrouter.Repository    = (*Repo)(nil)
	_ fhirrouter.Transactional = (*Repo)(nil)
)

func (r *Repo) reads() bool { return r.o.Mode == All }

func audited(rt string) bool { return !skipTypes[rt] }

func (r *Repo) CreateResource(ctx context.Context, res fhir.Resource) (fhir.Resource, error) {
	out, err := r.Guard.CreateResource(ctx, res)
	if err == nil && audited(out.GetResourceType()) {
		r.record(ctx, pending{interaction: "create", rt: out.GetResourceType(), id: out.GetID(), vid: versionOf(out)})
	}
	return out, err
}

func (r *Repo) UpdateResource(ctx context.Context, res fhir.Resource, opts fhirrouter.UpdateOptions) (fhir.Resource, error) {
	if res.GetResourceType() == "AuditEvent" {
		return nil, fhirrouter.Forbidden("AuditEvent không được sửa")
	}
	out, err := r.Guard.UpdateResource(ctx, res, opts)
	if err == nil && audited(out.GetResourceType()) {
		r.record(ctx, pending{interaction: "update", rt: out.GetResourceType(), id: out.GetID(), vid: versionOf(out)})
	}
	return out, err
}

func (r *Repo) DeleteResource(ctx context.Context, rt, id string) error {
	if rt == "AuditEvent" {
		return fhirrouter.Forbidden("AuditEvent không được xóa")
	}
	err := r.Guard.DeleteResource(ctx, rt, id)
	if err == nil && audited(rt) {
		r.record(ctx, pending{interaction: "delete", rt: rt, id: id})
	}
	return err
}

func (r *Repo) ReadResource(ctx context.Context, rt, id string) (fhir.Resource, error) {
	out, err := r.Guard.ReadResource(ctx, rt, id)
	if err == nil && r.reads() && audited(rt) {
		r.record(ctx, pending{interaction: "read", rt: rt, id: id, vid: versionOf(out)})
	}
	return out, err
}

func (r *Repo) ReadVersion(ctx context.Context, rt, id, vid string) (fhir.Resource, error) {
	out, err := r.Guard.ReadVersion(ctx, rt, id, vid)
	if err == nil && r.reads() && audited(rt) {
		r.record(ctx, pending{interaction: "vread", rt: rt, id: id, vid: vid})
	}
	return out, err
}

func (r *Repo) ReadHistory(ctx context.Context, rt, id string) ([]fhirrouter.HistoryEntry, error) {
	out, err := r.Guard.ReadHistory(ctx, rt, id)
	if err == nil && r.reads() && audited(rt) {
		r.record(ctx, pending{interaction: "history", rt: rt, id: id})
	}
	return out, err
}

func (r *Repo) Search(ctx context.Context, req *fhirsearch.Request) (*fhirrouter.SearchResult, error) {
	out, err := r.Guard.Search(ctx, req)
	if err == nil && r.reads() && audited(req.ResourceType) && !emptyJobSearch(ctx, out) {
		if req.ResourceType == "" { // search hệ thống (_type): đọc xuyên loại nên cũng được kiểm toán
			r.record(ctx, pending{interaction: "search-system", query: "?" + req.Query(0).Encode()})
		} else {
			r.record(ctx, pending{interaction: "search-type", rt: req.ResourceType, query: req.ResourceType + "?" + req.Query(0).Encode()})
		}
	}
	return out, err
}

// JobSubjectPrefix là tiền tố Principal.Subject của job nền chạy bằng quyền người gọi (bulk export): bulk export tìm trên MỌI loại resource nên mỗi
// loại không có dữ liệu sẽ thành một AuditEvent vô nghĩa (không có dữ liệu nào được đọc); search của job trả 0 kết quả không được ghi.
const JobSubjectPrefix = "job:"

func emptyJobSearch(ctx context.Context, out *fhirrouter.SearchResult) bool {
	pr, ok := fhirauth.PrincipalFrom(ctx)
	return ok && strings.HasPrefix(pr.Subject, JobSubjectPrefix) && out != nil && len(out.Matches) == 0
}

// RunInTransaction: AuditEvent của các thao tác trong transaction chỉ được ghi sau khi commit.
func (r *Repo) RunInTransaction(ctx context.Context, fn func(fhirrouter.Repository) error) error {
	if r.buf != nil { // đã trong transaction
		return fn(r)
	}
	b := &buffer{}
	err := r.Guard.RunInTransaction(ctx, func(rp fhirrouter.Repository) error {
		g, ok := rp.(*fhirrouter.Guard)
		if !ok {
			return fhirrouter.Internal("fhiraudit: repository giao dịch không phải Guard")
		}
		return fn(&Repo{Guard: g, o: r.o, buf: b})
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	evs := b.events
	b.events = nil
	b.mu.Unlock()
	for _, p := range evs {
		r.write(p)
	}
	return nil
}

func (r *Repo) record(ctx context.Context, p pending) {
	p.ctx = ctx
	if r.buf != nil {
		r.buf.mu.Lock()
		r.buf.events = append(r.buf.events, p)
		r.buf.mu.Unlock()
		return
	}
	r.write(p)
}

func (r *Repo) write(p pending) {
	ae := r.build(p)
	// ghi sau khi yêu cầu đã thành công: không hủy việc ghi vì client ngắt kết nối
	ctx := context.WithoutCancel(p.ctx)
	if _, err := r.o.Sink.CreateResource(ctx, ae); err != nil {
		slog.ErrorContext(ctx, "fhiraudit: không ghi được AuditEvent", "interaction", p.interaction, "resource", p.rt+"/"+p.id, "error", err)
	}
}

func (r *Repo) build(p pending) *fhir.AuditEvent {
	pr, _ := fhirauth.PrincipalFrom(p.ctx)
	subject, project := anonymous, ""
	var role []fhir.CodeableConcept
	if pr != nil {
		subject, project = pr.Subject, pr.Project
		if pr.Super {
			role = []fhir.CodeableConcept{{Text: "super-admin"}}
		}
	}
	yes, no := true, false
	agent := fhir.AuditEventAgent{
		Who:       &fhir.Reference{Identifier: &fhir.Identifier{System: SubjectSystem, Value: subject}, Display: subject},
		Role:      role,
		Requestor: &yes,
	}
	if c := clientFrom(p.ctx); c.Address != "" {
		agent.Network = &fhir.AuditEventAgentNetwork{Address: c.Address, Type: "2"} // 2 = địa chỉ IP
	}
	agents := []fhir.AuditEventAgent{agent}
	if pr != nil && pr.OnBehalfOf != "" {
		// job nền: người yêu cầu là người đã tạo job; job (Subject "job:<id>") là tác nhân thực hiện
		agents = []fhir.AuditEventAgent{
			{Who: &fhir.Reference{Identifier: &fhir.Identifier{System: SubjectSystem, Value: pr.OnBehalfOf}, Display: pr.OnBehalfOf}, Requestor: &yes},
			{Who: agent.Who, Role: []fhir.CodeableConcept{{Text: "job"}}, Requestor: &no},
		}
	}
	ae := &fhir.AuditEvent{
		ID:       newID(),
		Meta:     &fhir.Meta{Project: project},
		Type:     &fhir.Coding{System: typeSystem, Code: "rest", Display: "Restful Operation"},
		Subtype:  []fhir.Coding{{System: interactionSystem, Code: p.interaction, Display: p.interaction}},
		Action:   actionOf(p.interaction),
		Recorded: r.o.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Outcome:  "0",
		Agent:    agents,
		Source:   &fhir.AuditEventSource{Observer: &fhir.Reference{Display: r.o.Observer}},
	}
	ent := fhir.AuditEventEntity{}
	if p.id != "" {
		ent.What = &fhir.Reference{Reference: p.rt + "/" + p.id}
		if p.vid != "" {
			ent.Description = "versionId " + p.vid
		}
	}
	if p.query != "" {
		ent.Description = strings.TrimSpace("search " + p.rt) // sev-1: chỉ một trong name hoặc query
		ent.Query = base64.StdEncoding.EncodeToString([]byte(p.query))
	}
	ae.Entity = []fhir.AuditEventEntity{ent}
	return ae
}

// actionOf ánh xạ interaction sang AuditEvent.action: C create, R read, U update, D delete, E execute (search, history).
func actionOf(interaction string) string {
	switch interaction {
	case "create":
		return "C"
	case "read", "vread":
		return "R"
	case "update":
		return "U"
	case "delete":
		return "D"
	}
	return "E"
}

func versionOf(res fhir.Resource) string {
	if res == nil {
		return ""
	}
	if m := res.GetMeta(); m != nil {
		return m.VersionId
	}
	return ""
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
