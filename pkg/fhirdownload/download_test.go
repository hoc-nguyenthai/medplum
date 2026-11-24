package fhirdownload_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirdownload"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirsub"
	"trivita-fhir/pkg/messaging"
)

type env struct {
	t      *testing.T
	base   *fhirrouter.MemoryRepository
	repo   fhirrouter.Repository // phát sự kiện
	blobs  binarystore.Store
	broker *messaging.InMemory
	w      *fhirdownload.Worker
}

func newEnv(t *testing.T, mut func(*fhirdownload.Config)) *env {
	t.Helper()
	base := fhirrouter.NewMemoryRepository()
	broker := messaging.NewInMemory()
	repo := fhirsub.Wrap(base, broker)
	blobs := binarystore.NewMemory()
	cfg := fhirdownload.Config{Repo: repo, BinaryRepo: base, Blobs: blobs, MaxBytes: 1 << 20, AllowPrivate: true, OwnPrefixes: []string{"https://fhir.test/fhir/R4/"}}
	if mut != nil {
		mut(&cfg)
	}
	w, err := fhirdownload.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Shutdown(context.Background()) })
	w.Register(broker)
	return &env{t: t, base: base, repo: repo, blobs: blobs, broker: broker, w: w}
}

func docRef(project string, urls ...string) *fhir.DocumentReference {
	d := &fhir.DocumentReference{Status: "current"}
	for _, u := range urls {
		d.Content = append(d.Content, fhir.DocumentReferenceContent{Attachment: &fhir.Attachment{URL: u}})
	}
	if project != "" {
		d.Meta = &fhir.Meta{Project: project}
	}
	return d
}

func (e *env) create(d *fhir.DocumentReference) *fhir.DocumentReference {
	e.t.Helper()
	d.ID = fmt.Sprintf("d%d", time.Now().UnixNano())
	r, err := e.repo.CreateResource(context.Background(), d)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.(*fhir.DocumentReference)
}

func (e *env) read(id string) *fhir.DocumentReference {
	e.t.Helper()
	r, err := e.base.ReadResource(context.Background(), "DocumentReference", id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.(*fhir.DocumentReference)
}

func (e *env) binaries() []*fhir.Binary {
	e.t.Helper()
	req, _ := fhirsearch.Parse("Binary", nil)
	req.Count, req.Total = 100, "none"
	res, err := e.base.Search(context.Background(), req)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []*fhir.Binary
	for _, m := range res.Matches {
		out = append(out, m.(*fhir.Binary))
	}
	return out
}

func (e *env) content(b *fhir.Binary) []byte {
	e.t.Helper()
	key, _ := binarystore.Key(b.ID, b.Meta.VersionId)
	rc, err := e.blobs.Open(context.Background(), key)
	if err != nil {
		e.t.Fatalf("nội dung của Binary %s: %v", b.ID, err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	return data
}

func (e *env) blobCount() int {
	n := 0
	_ = e.blobs.(binarystore.Lister).List(context.Background(), func(binarystore.Entry) error { n++; return nil })
	return n
}

type site struct {
	*httptest.Server
	hits map[string]*atomic.Int32
}

func newSite(t *testing.T, h map[string]http.HandlerFunc) *site {
	s := &site{hits: map[string]*atomic.Int32{}}
	mux := http.NewServeMux()
	for p, f := range h {
		c := &atomic.Int32{}
		s.hits[p] = c
		f := f
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) { c.Add(1); f(w, r) })
	}
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func serve(ct string, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		_, _ = w.Write(body)
	}
}

func TestDownloadsExternalAttachmentIntoProjectBinary(t *testing.T) {
	pdf := bytes.Repeat([]byte("%PDF-1.7 nội dung"), 100)
	site := newSite(t, map[string]http.HandlerFunc{"/doc.pdf": serve("application/pdf; charset=binary", pdf), "/other.txt": serve("text/plain", []byte("hello"))})
	e := newEnv(t, nil)
	d := e.create(docRef("projA", site.URL+"/doc.pdf?token=ky", site.URL+"/doc.pdf?token=ky", site.URL+"/other.txt"))
	got := e.read(d.ID)
	a0, a1, a2 := got.Content[0].Attachment, got.Content[1].Attachment, got.Content[2].Attachment
	if !strings.HasPrefix(a0.URL, "Binary/") || a0.URL != a1.URL || !strings.HasPrefix(a2.URL, "Binary/") || a0.URL == a2.URL {
		t.Fatalf("URL sau khi tải: %q %q %q", a0.URL, a1.URL, a2.URL)
	}
	if a0.ContentType != "application/pdf" || a2.ContentType != "text/plain" {
		t.Errorf("contentType của attachment: %q %q", a0.ContentType, a2.ContentType)
	}
	if site.hits["/doc.pdf"].Load() != 1 {
		t.Errorf("cùng URL hai attachment phải tải một lần: %d", site.hits["/doc.pdf"].Load())
	}
	bins := e.binaries()
	if len(bins) != 2 || e.blobCount() != 2 {
		t.Fatalf("%d Binary, %d nội dung (muốn 2, 2)", len(bins), e.blobCount())
	}
	for _, b := range bins {
		if b.Meta.Project != "projA" || b.SecurityContext == nil || b.SecurityContext.Reference != "DocumentReference/"+d.ID {
			t.Errorf("Binary %s: project %q, securityContext %+v", b.ID, b.Meta.Project, b.SecurityContext)
		}
		if "Binary/"+b.ID == a0.URL {
			if !bytes.Equal(e.content(b), pdf) || b.ContentType != "application/pdf" {
				t.Errorf("nội dung hoặc contentType sai: %q %d byte", b.ContentType, len(e.content(b)))
			}
		}
	}
	// lần ghi do worker gây ra kích hoạt lại sự kiện nhưng không còn URL ngoài: không tải thêm
	time.Sleep(50 * time.Millisecond)
	if len(e.binaries()) != 2 || site.hits["/other.txt"].Load() != 1 {
		t.Errorf("tải lặp: %d Binary, %d lần", len(e.binaries()), site.hits["/other.txt"].Load())
	}
}

func TestSkipsUrlsThatAreNotExternalOrNotAllowed(t *testing.T) {
	site := newSite(t, map[string]http.HandlerFunc{"/a": serve("text/plain", []byte("a")), "/ok/b": serve("text/plain", []byte("b")), "/ign/c": serve("text/plain", []byte("c")), "/own/f": serve("text/plain", []byte("own"))})
	e := newEnv(t, func(c *fhirdownload.Config) {
		c.OwnPrefixes = append(c.OwnPrefixes, site.URL+"/own/")
		c.Allowed = []string{site.URL + "/ok/", site.URL + "/ign/", site.URL + "/own/"}
		c.Ignored = []string{site.URL + "/ign/"}
	})
	d := docRef("p", "Binary/abc", "https://fhir.test/fhir/R4/Binary/x", "relative/path.pdf", "ftp://example.org/x", "mailto:a@b.c", site.URL+"/own/f", site.URL+"/a", site.URL+"/ign/c", site.URL+"/ok/b")
	// Extension.url là http tuyệt đối nhưng KHÔNG phải Attachment: không được tải
	d.Extension = []fhir.Extension{{URL: site.URL + "/a"}}
	got := e.read(e.create(d).ID)
	want := []string{"Binary/abc", "https://fhir.test/fhir/R4/Binary/x", "relative/path.pdf", "ftp://example.org/x", "mailto:a@b.c", site.URL + "/own/f", site.URL + "/a", site.URL + "/ign/c"}
	for i, w := range want {
		if got.Content[i].Attachment.URL != w {
			t.Errorf("attachment %d bị đổi: %q (muốn giữ %q)", i, got.Content[i].Attachment.URL, w)
		}
	}
	if !strings.HasPrefix(got.Content[8].Attachment.URL, "Binary/") {
		t.Errorf("URL trong danh sách cho phép phải được tải: %q", got.Content[8].Attachment.URL)
	}
	if site.hits["/a"].Load() != 0 || site.hits["/ign/c"].Load() != 0 || site.hits["/own/f"].Load() != 0 || got.Extension[0].URL != site.URL+"/a" {
		t.Errorf("gọi nhầm: /a=%d /ign/c=%d", site.hits["/a"].Load(), site.hits["/ign/c"].Load())
	}
	if len(e.binaries()) != 1 {
		t.Errorf("%d Binary, muốn 1", len(e.binaries()))
	}
}

func TestBlocksPrivateAddressesByDefault(t *testing.T) {
	site := newSite(t, map[string]http.HandlerFunc{"/secret": serve("text/plain", []byte("nội bộ"))})
	e := newEnv(t, func(c *fhirdownload.Config) { c.AllowPrivate = false })
	got := e.read(e.create(docRef("p", site.URL+"/secret", "http://169.254.169.254/latest/meta-data/")).ID)
	if got.Content[0].Attachment.URL != site.URL+"/secret" || got.Content[1].Attachment.URL != "http://169.254.169.254/latest/meta-data/" {
		t.Errorf("URL nội bộ bị đổi: %+v", got.Content)
	}
	if site.hits["/secret"].Load() != 0 || len(e.binaries()) != 0 || e.blobCount() != 0 {
		t.Errorf("không được gọi địa chỉ nội bộ: %d lần, %d Binary", site.hits["/secret"].Load(), len(e.binaries()))
	}
}

func handleOf(t *testing.T, e *env, d *fhir.DocumentReference) error {
	t.Helper()
	// kích hoạt trực tiếp bằng sự kiện để lấy lỗi trả về (broker trong bộ nhớ chỉ ghi log)
	d.ID = "direct"
	r, err := e.base.CreateResource(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := fhirJSON(r)
	ev := fhirsub.ChangeEvent{Interaction: "create", ResourceType: "DocumentReference", ID: "direct", VersionID: "1", Resource: raw}
	env, err := messaging.NewEnvelope(ev)
	if err != nil {
		t.Fatal(err)
	}
	return e.w.Handle(context.Background(), env)
}

func TestTemporaryFailuresAreReturnedAndLeaveNothing(t *testing.T) {
	site := newSite(t, map[string]http.HandlerFunc{
		"/500": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) },
		"/404": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
	})
	for _, p := range []string{"/500", "/404"} {
		e := newEnv(t, nil)
		err := handleOf(t, e, docRef("p", site.URL+p+"?token=BIMAT"))
		if err == nil || !strings.Contains(err.Error(), "HTTP") {
			t.Errorf("%s: phải trả lỗi tạm để broker thử lại: %v", p, err)
		}
		if err != nil && strings.Contains(err.Error(), "BIMAT") {
			t.Errorf("lỗi lộ query string của URL: %v", err)
		}
		if len(e.binaries()) != 0 || e.blobCount() != 0 {
			t.Errorf("%s: để lại %d Binary, %d nội dung", p, len(e.binaries()), e.blobCount())
		}
	}
	// máy chủ không tồn tại: lỗi mạng cũng không lộ query
	e := newEnv(t, nil)
	err := handleOf(t, e, docRef("p", "http://127.0.0.1:1/x?token=BIMAT"))
	if err == nil || strings.Contains(err.Error(), "BIMAT") {
		t.Errorf("lỗi mạng: %v", err)
	}
}

func TestPermanentRefusalsLeaveNothing(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 5000)
	site := newSite(t, map[string]http.HandlerFunc{
		"/big": serve("application/octet-stream", big),
		"/chunked": func(w http.ResponseWriter, _ *http.Request) {
			w.(http.Flusher).Flush()
			_, _ = w.Write(big)
			w.(http.Flusher).Flush()
		},
		"/exe": serve("application/x-sh", []byte("#!/bin/sh")),
	})
	for _, p := range []string{"/big", "/chunked", "/exe"} {
		e := newEnv(t, func(c *fhirdownload.Config) { c.MaxBytes = 1000 })
		if err := handleOf(t, e, docRef("p", site.URL+p)); err != nil {
			t.Errorf("%s: từ chối vĩnh viễn không được trả lỗi (sẽ thử lại vô ích): %v", p, err)
		}
		if len(e.binaries()) != 0 || e.blobCount() != 0 {
			t.Errorf("%s: để lại %d Binary, %d nội dung", p, len(e.binaries()), e.blobCount())
		}
	}
}

// Ghi song song làm đổi phiên bản giữa lúc tải: không ghi đè, dọn Binary vừa tạo, trả lỗi để thử lại; lần sau thành công.
type bumpRepo struct {
	fhirrouter.Repository
	bumps atomic.Int32
}

func (b *bumpRepo) UpdateResource(ctx context.Context, r fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	if b.bumps.Add(1) == 1 {
		cur, _ := b.Repository.ReadResource(ctx, r.GetResourceType(), r.GetID())
		d := cur.(*fhir.DocumentReference)
		d.Description = "đổi song song"
		if _, err := b.Repository.UpdateResource(ctx, d, fhirrouter.UpdateOptions{}); err != nil {
			panic(err)
		}
	}
	return b.Repository.UpdateResource(ctx, r, o)
}

func TestVersionConflictCleansUpAndRetrySucceeds(t *testing.T) {
	site := newSite(t, map[string]http.HandlerFunc{"/f": serve("text/plain", []byte("data"))})
	e := newEnv(t, nil)
	br := &bumpRepo{Repository: e.base}
	w, _ := fhirdownload.New(fhirdownload.Config{Repo: br, BinaryRepo: e.base, Blobs: e.blobs, MaxBytes: 1 << 20, AllowPrivate: true})
	e.w = w
	d := docRef("p", site.URL+"/f")
	d.ID = "direct"
	r, _ := e.base.CreateResource(context.Background(), d)
	raw, _ := fhirJSON(r)
	env, _ := messaging.NewEnvelope(fhirsub.ChangeEvent{Interaction: "create", ResourceType: "DocumentReference", ID: "direct", Resource: raw})
	if err := w.Handle(context.Background(), env); err == nil {
		t.Fatal("xung đột phiên bản phải trả lỗi để thử lại")
	}
	if len(e.binaries()) != 0 || e.blobCount() != 0 {
		t.Errorf("để lại %d Binary, %d nội dung sau xung đột", len(e.binaries()), e.blobCount())
	}
	if got := e.read("direct"); got.Description != "đổi song song" || strings.HasPrefix(got.Content[0].Attachment.URL, "Binary/") {
		t.Errorf("thay đổi song song bị ghi đè hoặc URL bị đổi: %+v", got)
	}
	if err := w.Handle(context.Background(), env); err != nil {
		t.Fatalf("lần thử lại: %v", err)
	}
	got := e.read("direct")
	if !strings.HasPrefix(got.Content[0].Attachment.URL, "Binary/") || got.Description != "đổi song song" || len(e.binaries()) != 1 {
		t.Errorf("sau khi thử lại: %+v, %d Binary", got.Content[0].Attachment, len(e.binaries()))
	}
}

// Resource đổi URL (hoặc bị xóa) trong lúc đang tải: không tạo Binary mồ côi và không đổi gì.
func TestResourceChangedOrDeletedWhileDownloading(t *testing.T) {
	var e *env
	site := newSite(t, map[string]http.HandlerFunc{
		"/slow": func(w http.ResponseWriter, _ *http.Request) {
			cur, _ := e.base.ReadResource(context.Background(), "DocumentReference", "direct")
			d := cur.(*fhir.DocumentReference)
			d.Content[0].Attachment.URL = "https://example.org/da-doi"
			_, _ = e.base.UpdateResource(context.Background(), d, fhirrouter.UpdateOptions{})
			_, _ = w.Write([]byte("x"))
		},
		"/gone": func(w http.ResponseWriter, _ *http.Request) {
			_ = e.base.DeleteResource(context.Background(), "DocumentReference", "direct")
			_, _ = w.Write([]byte("x"))
		},
	})
	for _, p := range []string{"/slow", "/gone"} {
		e = newEnv(t, nil)
		if err := handleOf(t, e, docRef("p", site.URL+p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		if len(e.binaries()) != 0 || e.blobCount() != 0 {
			t.Errorf("%s: để lại %d Binary, %d nội dung", p, len(e.binaries()), e.blobCount())
		}
	}
}

// Chế độ Async (broker trong bộ nhớ giao đồng bộ): ghi resource trả ngay, việc tải chạy nền và thử lại theo hạn lùi.
func TestAsyncModeDoesNotBlockTheWriteAndRetries(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	site := newSite(t, map[string]http.HandlerFunc{
		"/blocked": func(w http.ResponseWriter, _ *http.Request) { <-release; _, _ = w.Write([]byte("chậm")) },
		"/flaky": func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) < 3 {
				http.Error(w, "tạm lỗi", 503)
				return
			}
			_, _ = w.Write([]byte("ổn"))
		},
	})
	e := newEnv(t, func(c *fhirdownload.Config) {
		c.Async = true
		c.Backoff = func(int) time.Duration { return 5 * time.Millisecond }
	})
	t0 := time.Now()
	slow := e.create(docRef("p", site.URL+"/blocked"))
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("ghi resource bị chặn %v bởi việc tải", d)
	}
	flaky := e.create(docRef("p", site.URL+"/flaky"))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !strings.HasPrefix(e.read(flaky.ID).Content[0].Attachment.URL, "Binary/") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(e.read(flaky.ID).Content[0].Attachment.URL, "Binary/") || calls.Load() != 3 {
		t.Fatalf("thử lại: %d lần gọi, %q", calls.Load(), e.read(flaky.ID).Content[0].Attachment.URL)
	}
	close(release)
	for time.Now().Before(deadline) && !strings.HasPrefix(e.read(slow.ID).Content[0].Attachment.URL, "Binary/") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(e.read(slow.ID).Content[0].Attachment.URL, "Binary/") {
		t.Error("việc tải chậm không hoàn tất")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.w.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestIgnoresDeletesAndUnrelatedTypes(t *testing.T) {
	site := newSite(t, map[string]http.HandlerFunc{"/x": serve("text/plain", []byte("x"))})
	e := newEnv(t, nil)
	raw := []byte(`{"resourceType":"DocumentReference","id":"z","status":"current","content":[{"attachment":{"url":"` + site.URL + `/x"}}]}`)
	for _, ev := range []fhirsub.ChangeEvent{
		{Interaction: "delete", ResourceType: "DocumentReference", ID: "z", Resource: raw},
		{Interaction: "create", ResourceType: "Binary", ID: "z", Resource: raw},
		{Interaction: "create", ResourceType: "AuditEvent", ID: "z", Resource: raw},
		{Interaction: "create", ResourceType: "DocumentReference", ID: "z", Resource: []byte(`{"không phải":"resource"}`)},
		{Interaction: "create", ResourceType: "DocumentReference", ID: "z"},
	} {
		env, _ := messaging.NewEnvelope(ev)
		if err := e.w.Handle(context.Background(), env); err != nil {
			t.Errorf("%s/%s: %v", ev.Interaction, ev.ResourceType, err)
		}
	}
	if site.hits["/x"].Load() != 0 {
		t.Errorf("gọi ra ngoài %d lần cho sự kiện không liên quan", site.hits["/x"].Load())
	}
}

func TestNewValidatesConfig(t *testing.T) {
	base := fhirrouter.NewMemoryRepository()
	for name, c := range map[string]fhirdownload.Config{
		"thiếu repo": {BinaryRepo: base, Blobs: binarystore.NewMemory(), MaxBytes: 1},
		"thiếu kho":  {Repo: base, BinaryRepo: base, MaxBytes: 1},
		"MaxBytes 0": {Repo: base, BinaryRepo: base, Blobs: binarystore.NewMemory()},
	} {
		if _, err := fhirdownload.New(c); err == nil {
			t.Errorf("%s phải lỗi", name)
		}
	}
}
