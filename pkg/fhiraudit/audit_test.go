package fhiraudit_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhiraudit"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirvalidate"
)

var fixedNow = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

type env struct {
	mem  *fhirrouter.MemoryRepository
	sink *fhirrouter.MemoryRepository
	repo fhirrouter.Repository
}

func newEnv(t *testing.T, mode fhiraudit.Mode, inner func(*fhirrouter.MemoryRepository) fhirrouter.Repository) env {
	t.Helper()
	mem := fhirrouter.NewMemoryRepository()
	var base fhirrouter.Repository = mem
	if inner != nil {
		base = inner(mem)
	}
	return env{mem, mem, fhiraudit.Wrap(base, fhiraudit.Options{Mode: mode, Sink: mem, Observer: "fhir-test", Now: func() time.Time { return fixedNow }})}
}

// newSplitEnv dùng kho riêng cho AuditEvent: kho giao dịch của repo chính hoàn tác không động tới kho ghi AuditEvent, nên chỉ cách này mới phân biệt
// "ghi sau commit" với "ghi ngay".
func newSplitEnv(t *testing.T, mode fhiraudit.Mode, sinkWrap func(fhirrouter.Repository) fhirrouter.Repository) env {
	t.Helper()
	mem, sink := fhirrouter.NewMemoryRepository(), fhirrouter.NewMemoryRepository()
	var s fhirrouter.Repository = sink
	if sinkWrap != nil {
		s = sinkWrap(sink)
	}
	return env{mem, sink, fhiraudit.Wrap(mem, fhiraudit.Options{Mode: mode, Sink: s, Observer: "fhir-test", Now: func() time.Time { return fixedNow }})}
}

func asUser(project, sub string, super bool) context.Context {
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: sub, Project: project, Role: fhirauth.RoleAdmin, Super: super})
	return fhiraudit.WithClient(ctx, fhiraudit.Client{Address: "203.0.113.9"})
}

func (e env) events(t *testing.T) []*fhir.AuditEvent {
	t.Helper()
	sr, _ := fhirsearch.Parse("AuditEvent", map[string][]string{"_count": {"200"}, "_sort": {"_lastUpdated"}})
	res, err := e.sink.Search(context.Background(), sr)
	if err != nil {
		t.Fatal(err)
	}
	var out []*fhir.AuditEvent
	for _, m := range res.Matches {
		out = append(out, m.(*fhir.AuditEvent))
	}
	return out
}

func patient(id string) *fhir.Patient { return &fhir.Patient{ID: id, Gender: "female"} }

func TestWritesProduceAuditEvents(t *testing.T) {
	e := newEnv(t, fhiraudit.Writes, nil)
	ctx := asUser("proj1", "user-7", false)
	if _, err := e.repo.CreateResource(ctx, patient("p1")); err != nil {
		t.Fatal(err)
	}
	up := patient("p1")
	up.Gender = "male"
	if _, err := e.repo.UpdateResource(ctx, up, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.DeleteResource(ctx, "Patient", "p1"); err != nil {
		t.Fatal(err)
	}
	evs := e.events(t)
	if len(evs) != 3 {
		t.Fatalf("cần 3 AuditEvent, có %d", len(evs))
	}
	got := map[string]*fhir.AuditEvent{}
	for _, ev := range evs {
		got[ev.Subtype[0].Code] = ev
	}
	for code, action := range map[string]string{"create": "C", "update": "U", "delete": "D"} {
		ev := got[code]
		if ev == nil {
			t.Fatalf("thiếu AuditEvent %s", code)
		}
		if ev.Action != action || ev.Outcome != "0" || ev.Recorded != "2026-11-24T10:00:00.000Z" {
			t.Errorf("%s: action=%q outcome=%q recorded=%q", code, ev.Action, ev.Outcome, ev.Recorded)
		}
		if ev.Meta == nil || ev.Meta.Project != "proj1" {
			t.Errorf("%s: meta.project=%v", code, ev.Meta)
		}
		if ev.Type == nil || ev.Type.Code != "rest" || ev.Subtype[0].System != "http://hl7.org/fhir/restful-interaction" {
			t.Errorf("%s: type/subtype %v %v", code, ev.Type, ev.Subtype)
		}
		a := ev.Agent[0]
		if a.Who == nil || a.Who.Identifier == nil || a.Who.Identifier.Value != "user-7" || a.Requestor == nil || !*a.Requestor {
			t.Errorf("%s: agent %+v", code, a)
		}
		if a.Network == nil || a.Network.Address != "203.0.113.9" || a.Network.Type != "2" {
			t.Errorf("%s: network %+v", code, a.Network)
		}
		if len(a.Role) != 0 {
			t.Errorf("%s: không phải super-admin mà có role %v", code, a.Role)
		}
		if ev.Source == nil || ev.Source.Observer == nil || ev.Source.Observer.Display != "fhir-test" {
			t.Errorf("%s: source %+v", code, ev.Source)
		}
		if len(ev.Entity) != 1 || ev.Entity[0].What == nil || ev.Entity[0].What.Reference != "Patient/p1" {
			t.Errorf("%s: entity %+v", code, ev.Entity)
		}
		if res := fhirvalidate.ValidateResource(ev, nil); !res.OK() {
			t.Errorf("%s: AuditEvent không hợp lệ: %v", code, res.Errors())
		}
	}
	if !strings.HasPrefix(got["create"].Entity[0].Description, "versionId ") {
		t.Errorf("create phải ghi versionId: %q", got["create"].Entity[0].Description)
	}
}

func TestSuperAdminIsMarked(t *testing.T) {
	e := newEnv(t, fhiraudit.Writes, nil)
	if _, err := e.repo.CreateResource(asUser("p", "root", true), patient("p1")); err != nil {
		t.Fatal(err)
	}
	a := e.events(t)[0].Agent[0]
	if len(a.Role) != 1 || a.Role[0].Text != "super-admin" {
		t.Fatalf("super-admin phải được đánh dấu: %+v", a.Role)
	}
}

func TestAnonymousWithoutPrincipal(t *testing.T) {
	e := newEnv(t, fhiraudit.Writes, nil)
	if _, err := e.repo.CreateResource(context.Background(), patient("p1")); err != nil {
		t.Fatal(err)
	}
	ev := e.events(t)[0]
	if ev.Agent[0].Who.Identifier.Value != "anonymous" || ev.Agent[0].Network != nil {
		t.Fatalf("%+v", ev.Agent[0])
	}
}

func TestReadsOnlyInAllMode(t *testing.T) {
	run := func(mode fhiraudit.Mode) []*fhir.AuditEvent {
		e := newEnv(t, mode, nil)
		ctx := asUser("p", "u", false)
		out, _ := e.repo.CreateResource(ctx, patient("p1"))
		_, _ = e.repo.ReadResource(ctx, "Patient", "p1")
		_, _ = e.repo.ReadVersion(ctx, "Patient", "p1", out.GetMeta().VersionId)
		_, _ = e.repo.ReadHistory(ctx, "Patient", "p1")
		sr, _ := fhirsearch.Parse("Patient", map[string][]string{"gender": {"female"}})
		_, _ = e.repo.Search(ctx, sr)
		return e.events(t)
	}
	if n := len(run(fhiraudit.Writes)); n != 1 {
		t.Fatalf("writes: chỉ create, có %d", n)
	}
	evs := run(fhiraudit.All)
	codes := map[string]*fhir.AuditEvent{}
	for _, ev := range evs {
		codes[ev.Subtype[0].Code] = ev
	}
	for _, c := range []string{"create", "read", "vread", "history", "search-type"} {
		if codes[c] == nil {
			t.Fatalf("all: thiếu %s (có %d sự kiện)", c, len(evs))
		}
	}
	if codes["read"].Action != "R" || codes["vread"].Action != "R" || codes["history"].Action != "E" || codes["search-type"].Action != "E" {
		t.Errorf("action sai: %s %s %s %s", codes["read"].Action, codes["vread"].Action, codes["history"].Action, codes["search-type"].Action)
	}
	q := codes["search-type"].Entity[0]
	raw, err := base64.StdEncoding.DecodeString(q.Query)
	if err != nil || !strings.HasPrefix(string(raw), "Patient?") || !strings.Contains(string(raw), "gender=female") || q.Name != "" || q.What != nil {
		t.Fatalf("entity của search: %+v (%q, %v)", q, raw, err)
	}
	if res := fhirvalidate.ValidateResource(codes["search-type"], nil); !res.OK() {
		t.Fatalf("AuditEvent của search không hợp lệ: %v", res.Errors())
	}
}

func TestFailedOperationsAreNotAudited(t *testing.T) {
	e := newEnv(t, fhiraudit.All, nil)
	ctx := asUser("p", "u", false)
	if err := e.repo.DeleteResource(ctx, "Patient", "nope"); err == nil {
		t.Fatal("xóa resource không tồn tại phải lỗi")
	}
	if _, err := e.repo.ReadResource(ctx, "Patient", "nope"); err == nil {
		t.Fatal("đọc resource không tồn tại phải lỗi")
	}
	if n := len(e.events(t)); n != 0 {
		t.Fatalf("thao tác lỗi không được có AuditEvent, có %d", n)
	}
}

func TestAuditEventAndAsyncJobAreNotAudited(t *testing.T) {
	e := newEnv(t, fhiraudit.All, nil)
	ctx := asUser("p", "u", false)
	if _, err := e.repo.CreateResource(ctx, &fhir.AuditEvent{ID: "a1"}); err != nil {
		t.Fatal(err)
	}
	_, _ = e.repo.ReadResource(ctx, "AuditEvent", "a1")
	sr, _ := fhirsearch.Parse("AuditEvent", nil)
	_, _ = e.repo.Search(ctx, sr)
	if _, err := e.repo.CreateResource(ctx, &fhir.AsyncJob{ID: "j1"}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.events(t)); n != 1 { // chỉ chính a1
		t.Fatalf("AuditEvent/AsyncJob không được sinh AuditEvent, có %d AuditEvent", n)
	}
}

func TestAuditEventCannotBeModifiedOrDeleted(t *testing.T) {
	e := newEnv(t, fhiraudit.Writes, nil)
	ctx := asUser("p", "u", false)
	if _, err := e.repo.CreateResource(ctx, &fhir.AuditEvent{ID: "a1"}); err != nil {
		t.Fatalf("client được tạo AuditEvent: %v", err)
	}
	var fe *fhirrouter.Error
	if _, err := e.repo.UpdateResource(ctx, &fhir.AuditEvent{ID: "a1", Action: "U"}, fhirrouter.UpdateOptions{}); !errors.As(err, &fe) || fe.Status != 403 {
		t.Fatalf("sửa AuditEvent phải 403: %v", err)
	}
	if err := e.repo.DeleteResource(ctx, "AuditEvent", "a1"); !errors.As(err, &fe) || fe.Status != 403 {
		t.Fatalf("xóa AuditEvent phải 403: %v", err)
	}
	if got, _ := e.mem.ReadResource(ctx, "AuditEvent", "a1"); got == nil || got.(*fhir.AuditEvent).Action != "" {
		t.Fatalf("AuditEvent bị đổi: %+v", got)
	}
}

func TestOffModeReturnsRepoUnchanged(t *testing.T) {
	mem := fhirrouter.NewMemoryRepository()
	if got := fhiraudit.Wrap(mem, fhiraudit.Options{Mode: fhiraudit.Off, Sink: mem}); got != fhirrouter.Repository(mem) {
		t.Fatal("Off phải trả nguyên repo")
	}
	if got := fhiraudit.Wrap(mem, fhiraudit.Options{Mode: fhiraudit.Writes}); got != fhirrouter.Repository(mem) {
		t.Fatal("thiếu Sink phải trả nguyên repo")
	}
}

type failingSink struct{ fhirrouter.Repository }

func (failingSink) CreateResource(context.Context, fhir.Resource) (fhir.Resource, error) {
	return nil, errors.New("sink hỏng")
}

func TestSinkFailureDoesNotFailTheRequest(t *testing.T) {
	mem := fhirrouter.NewMemoryRepository()
	repo := fhiraudit.Wrap(mem, fhiraudit.Options{Mode: fhiraudit.Writes, Sink: failingSink{mem}})
	if _, err := repo.CreateResource(asUser("p", "u", false), patient("p1")); err != nil {
		t.Fatalf("lỗi ghi AuditEvent không được làm hỏng yêu cầu: %v", err)
	}
	if _, err := mem.ReadResource(context.Background(), "Patient", "p1"); err != nil {
		t.Fatal("dữ liệu phải đã được lưu")
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]fhiraudit.Mode{"": fhiraudit.Off, "off": fhiraudit.Off, "writes": fhiraudit.Writes, "all": fhiraudit.All} {
		if got, err := fhiraudit.ParseMode(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := fhiraudit.ParseMode("reads"); err == nil {
		t.Error("giá trị lạ phải lỗi")
	}
}

// ---- qua router HTTP, kể cả transaction

func routerEnv(t *testing.T, mode fhiraudit.Mode, split bool) (env, http.Handler) {
	t.Helper()
	e := newEnv(t, mode, nil)
	if split {
		e = newSplitEnv(t, mode, nil)
	}
	h := fhirrouter.New(e.repo, fhirrouter.Options{UpdateAsCreate: true}).HTTPHandler()
	return e, h
}

func do(h http.Handler, method, path, body string) (int, string) {
	req := httptest.NewRequest(method, "/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/fhir+json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func TestTransactionAuditedAfterCommitOnly(t *testing.T) {
	e, h := routerEnv(t, fhiraudit.Writes, true)
	ok := `{"resourceType":"Bundle","type":"transaction","entry":[
	 {"request":{"method":"PUT","url":"Patient/a"},"resource":{"resourceType":"Patient","id":"a"}},
	 {"request":{"method":"PUT","url":"Patient/b"},"resource":{"resourceType":"Patient","id":"b"}}]}`
	if code, out := do(h, "POST", "", ok); code != 200 {
		t.Fatalf("transaction: %d %s", code, out)
	}
	if n := len(e.events(t)); n != 2 {
		t.Fatalf("transaction thành công: cần 2 AuditEvent, có %d", n)
	}
	// entry thứ hai hỏng (resourceType lạ) làm cả transaction hủy: không AuditEvent nào cho entry đầu
	bad := `{"resourceType":"Bundle","type":"transaction","entry":[
	 {"request":{"method":"PUT","url":"Patient/c"},"resource":{"resourceType":"Patient","id":"c"}},
	 {"request":{"method":"PUT","url":"Patient/d"},"resource":{"resourceType":"Patient","id":"d","bogus":1}}]}`
	if code, _ := do(h, "POST", "", bad); code < 400 {
		t.Fatalf("transaction hỏng phải lỗi, nhận %d", code)
	}
	if n := len(e.events(t)); n != 2 {
		t.Fatalf("transaction bị hủy không được có AuditEvent mới, có %d tổng", n)
	}
}

func TestRESTWritesAndImmutabilityOverHTTP(t *testing.T) {
	e, h := routerEnv(t, fhiraudit.Writes, false)
	if code, out := do(h, "POST", "Patient", `{"resourceType":"Patient","gender":"male"}`); code != 201 {
		t.Fatalf("%d %s", code, out)
	}
	evs := e.events(t)
	if len(evs) != 1 || evs[0].Subtype[0].Code != "create" || !strings.HasPrefix(evs[0].Entity[0].What.Reference, "Patient/") {
		t.Fatalf("%+v", evs)
	}
	id := evs[0].ID
	if code, _ := do(h, "DELETE", "AuditEvent/"+id, ""); code != 403 {
		t.Fatalf("xóa AuditEvent qua REST phải 403, nhận %d", code)
	}
	if code, _ := do(h, "PUT", "AuditEvent/"+id, `{"resourceType":"AuditEvent","id":"`+id+`","recorded":"2026-01-01T00:00:00Z","type":{"code":"x"},"agent":[{"requestor":true}],"source":{"observer":{"display":"x"}}}`); code != 403 {
		t.Fatalf("sửa AuditEvent qua REST phải 403, nhận %d", code)
	}
	if code, _ := do(h, "GET", "AuditEvent/"+id, ""); code != 200 {
		t.Fatalf("đọc AuditEvent phải 200, nhận %d", code)
	}
}

func TestFailedCreateIsNotAudited(t *testing.T) {
	e := newEnv(t, fhiraudit.Writes, nil)
	ctx := asUser("p", "u", false)
	if _, err := e.repo.CreateResource(ctx, patient("p1")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.CreateResource(ctx, patient("p1")); err == nil {
		t.Skip("MemoryRepository không từ chối id trùng")
	}
	if n := len(e.events(t)); n != 1 {
		t.Fatalf("create lỗi không được có AuditEvent: %d", n)
	}
}

func TestSystemSearchIsAuditedInAllMode(t *testing.T) {
	e := newEnv(t, fhiraudit.All, nil)
	ctx := asUser("p", "u", false)
	sr, _ := fhirsearch.Parse("", map[string][]string{"_type": {"Patient,Observation"}})
	if _, err := e.repo.Search(ctx, sr); err != nil {
		t.Fatal(err)
	}
	evs := e.events(t)
	if len(evs) != 1 || evs[0].Subtype[0].Code != "search-system" || evs[0].Action != "E" {
		t.Fatalf("%+v", evs)
	}
	raw, _ := base64.StdEncoding.DecodeString(evs[0].Entity[0].Query)
	if !strings.HasPrefix(string(raw), "?") || !strings.Contains(string(raw), "_type=") || evs[0].Entity[0].What != nil {
		t.Fatalf("entity: %+v %q", evs[0].Entity[0], raw)
	}
	if res := fhirvalidate.ValidateResource(evs[0], nil); !res.OK() {
		t.Fatalf("AuditEvent không hợp lệ: %v", res.Errors())
	}
}

// Trong transaction chỉ ghi sau commit, và transaction lồng không ghi hai lần.
func TestNestedTransactionWritesOnceAfterCommit(t *testing.T) {
	e := newSplitEnv(t, fhiraudit.Writes, nil)
	ctx := asUser("p", "u", false)
	tx := e.repo.(fhirrouter.Transactional)
	err := tx.RunInTransaction(ctx, func(rp fhirrouter.Repository) error {
		if _, err := rp.CreateResource(ctx, patient("n1")); err != nil {
			return err
		}
		if n := len(e.events(t)); n != 0 {
			t.Errorf("chưa commit mà đã có %d AuditEvent", n)
		}
		return rp.(fhirrouter.Transactional).RunInTransaction(ctx, func(inner fhirrouter.Repository) error {
			_, err := inner.CreateResource(ctx, patient("n2"))
			return err
		})
	})
	if err != nil {
		t.Skipf("MemoryRepository không hỗ trợ transaction lồng: %v", err)
	}
	if n := len(e.events(t)); n != 2 {
		t.Fatalf("cần đúng 2 AuditEvent sau commit, có %d", n)
	}
}

type ctxProbeSink struct {
	fhirrouter.Repository
	canceled *bool
}

func (s ctxProbeSink) CreateResource(ctx context.Context, r fhir.Resource) (fhir.Resource, error) {
	*s.canceled = ctx.Err() != nil
	return s.Repository.CreateResource(ctx, r)
}

// Client ngắt kết nối (ctx hủy) sau khi thao tác đã thành công thì AuditEvent vẫn phải được ghi.
func TestAuditWriteSurvivesCanceledRequestContext(t *testing.T) {
	var canceled bool
	e := newSplitEnv(t, fhiraudit.Writes, func(r fhirrouter.Repository) fhirrouter.Repository { return ctxProbeSink{r, &canceled} })
	ctx, cancel := context.WithCancel(asUser("p", "u", false))
	cancel()
	if _, err := e.repo.CreateResource(ctx, patient("c1")); err != nil {
		t.Fatal(err)
	}
	if canceled {
		t.Fatal("ghi AuditEvent phải dùng context không hủy")
	}
	if n := len(e.events(t)); n != 1 {
		t.Fatalf("AuditEvent phải được ghi dù ctx hủy: %d", n)
	}
}

// Job nền: người yêu cầu là người tạo job (OnBehalfOf), job là tác nhân thứ hai không phải requestor.
func TestJobPrincipalRecordsRequesterAndJobAgents(t *testing.T) {
	e := newEnv(t, fhiraudit.Writes, nil)
	ctx := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "job:j1", Project: "p", Role: fhirauth.RoleRead, OnBehalfOf: "alice"})
	if _, err := e.repo.CreateResource(ctx, patient("p1")); err != nil {
		t.Fatal(err)
	}
	ev := e.events(t)[0]
	if len(ev.Agent) != 2 {
		t.Fatalf("cần 2 agent: %+v", ev.Agent)
	}
	u, j := ev.Agent[0], ev.Agent[1]
	if u.Who.Identifier.Value != "alice" || !*u.Requestor || j.Who.Identifier.Value != "job:j1" || *j.Requestor || len(j.Role) != 1 || j.Role[0].Text != "job" {
		t.Fatalf("agent: %+v / %+v", u, j)
	}
	if res := fhirvalidate.ValidateResource(ev, nil); !res.OK() {
		t.Fatalf("AuditEvent không hợp lệ: %v", res.Errors())
	}
}

// Search của job trả 0 kết quả (export quét mọi loại resource) không tạo AuditEvent; search thường vẫn được ghi dù 0 kết quả.
func TestEmptyJobSearchesAreNotAudited(t *testing.T) {
	e := newEnv(t, fhiraudit.All, nil)
	job := fhirauth.WithPrincipal(context.Background(), &fhirauth.Principal{Subject: "job:j1", Project: "p", OnBehalfOf: "alice"})
	sr, _ := fhirsearch.Parse("Observation", nil)
	if _, err := e.repo.Search(job, sr); err != nil {
		t.Fatal(err)
	}
	if n := len(e.events(t)); n != 0 {
		t.Fatalf("search rỗng của job không được ghi: %d", n)
	}
	if _, err := e.repo.Search(asUser("p", "bob", false), sr); err != nil {
		t.Fatal(err)
	}
	if n := len(e.events(t)); n != 1 {
		t.Fatalf("search rỗng của người dùng thường vẫn phải được ghi: %d", n)
	}
	if _, err := e.repo.CreateResource(job, patient("p1")); err != nil {
		t.Fatal(err)
	}
	srp, _ := fhirsearch.Parse("Patient", nil)
	if _, err := e.repo.Search(job, srp); err != nil {
		t.Fatal(err)
	}
	if n := len(e.events(t)); n != 3 { // search của bob, create của job, search có kết quả của job
		t.Fatalf("search có dữ liệu của job phải được ghi: %d", n)
	}
}
