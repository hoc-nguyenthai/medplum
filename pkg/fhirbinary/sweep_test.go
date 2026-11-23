package fhirbinary_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirbinary"
	"trivita-fhir/pkg/fhirrouter"
)

type sweepEnv struct {
	t     *testing.T
	repo  *fhirrouter.MemoryRepository
	mem   *binarystore.Memory
	wrap  *fhirbinary.Repo
	ctx   context.Context
	clock time.Time
}

func newSweepEnv(t *testing.T) *sweepEnv {
	repo, mem := fhirrouter.NewMemoryRepository(), binarystore.NewMemory()
	return &sweepEnv{t: t, repo: repo, mem: mem, wrap: fhirbinary.Wrap(repo, mem, fhirbinary.WrapOptions{}), ctx: context.Background(), clock: time.Now()}
}

func (e *sweepEnv) create(id, body string) string {
	e.t.Helper()
	out, err := e.wrap.CreateResource(e.ctx, &fhir.Binary{ID: id, ContentType: "text/plain", Data: b64(body)})
	if err != nil {
		e.t.Fatal(err)
	}
	return verOf(out)
}

func (e *sweepEnv) plant(id, ver string) string {
	e.t.Helper()
	k, err := binarystore.Key(id, ver)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.mem.Put(e.ctx, k, strings.NewReader("orphan"), binarystore.PutOptions{}); err != nil {
		e.t.Fatal(err)
	}
	return k
}

// ageAll làm mọi khóa cũ đi d.
func (e *sweepEnv) ageAll(d time.Duration) {
	e.t.Helper()
	_ = e.mem.List(e.ctx, func(en binarystore.Entry) error { e.mem.SetModTime(en.Key, time.Now().Add(-d)); return nil })
}

func (e *sweepEnv) keys() []string {
	var ks []string
	_ = e.mem.List(e.ctx, func(en binarystore.Entry) error { ks = append(ks, en.Key); return nil })
	sort.Strings(ks)
	return ks
}

func (e *sweepEnv) sweep(opt fhirbinary.SweepOptions) (fhirbinary.SweepResult, error) {
	return fhirbinary.Sweep(e.ctx, e.mem, e.repo, opt)
}

func TestSweepRemovesOnlyOrphans(t *testing.T) {
	e := newSweepEnv(t)
	v1 := e.create("live", "one")
	out, err := e.wrap.UpdateResource(e.ctx, &fhir.Binary{ID: "live", ContentType: "text/plain", Data: b64("two")}, fhirrouter.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v2 := verOf(out)
	e.create("gone", "deleted but kept in history")
	if err := e.wrap.DeleteResource(e.ctx, "Binary", "gone"); err != nil {
		t.Fatal(err)
	}
	e.create("wiped", "expunged behind our back")
	if err := e.repo.Expunge(e.ctx, "Binary", "wiped"); err != nil { // bỏ qua Wrap: nội dung còn lại trong kho
		t.Fatal(err)
	}
	k1 := e.plant("never-existed", "v1")
	k2 := e.plant("live", "stale-version") // id có thật, phiên bản không có
	e.ageAll(48 * time.Hour)
	young := e.plant("young-orphan", "v1") // mới: trong ân hạn

	res, err := e.sweep(fhirbinary.SweepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Orphans != 3 || res.Deleted != 3 || res.Young != 1 || res.Errors != 0 || res.Scanned != 7 {
		t.Fatalf("%+v", res)
	}
	got := strings.Join(e.keys(), ",")
	for _, must := range []string{"binary/live/" + v1, "binary/live/" + v2, "binary/gone/", young} {
		if !strings.Contains(got, must) {
			t.Errorf("phải còn %s trong %s", must, got)
		}
	}
	for _, mustnt := range []string{k1, k2, "binary/wiped/"} {
		if strings.Contains(got, mustnt) {
			t.Errorf("phải mất %s trong %s", mustnt, got)
		}
	}
	// lượt thứ hai không còn gì để xóa
	if res, _ := e.sweep(fhirbinary.SweepOptions{}); res.Orphans != 0 || res.Deleted != 0 {
		t.Errorf("lượt hai: %+v", res)
	}
}

func TestSweepGraceBoundary(t *testing.T) {
	e := newSweepEnv(t)
	k := e.plant("o", "v1")
	now := time.Now()
	for _, age := range []time.Duration{2 * time.Hour, time.Hour + time.Minute} {
		e.mem.SetModTime(k, now.Add(-age))
		res, err := e.sweep(fhirbinary.SweepOptions{Grace: 2 * time.Hour, DryRun: true, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		// 2h đúng ân hạn 2h: không phải "cũ hơn" nên chưa xét; 1h01 < 2h cũng chưa xét
		if res.Orphans != 0 {
			t.Errorf("age %v: %+v", age, res)
		}
	}
	e.mem.SetModTime(k, now.Add(-2*time.Hour-time.Second))
	if res, _ := e.sweep(fhirbinary.SweepOptions{Grace: 2 * time.Hour, DryRun: true, Now: func() time.Time { return now }}); res.Orphans != 1 {
		t.Errorf("quá ân hạn 1s: %+v", res)
	}
	e.mem.SetModTime(k, now.Add(24*time.Hour)) // thời gian tương lai: coi là mới
	if res, _ := e.sweep(fhirbinary.SweepOptions{Grace: 2 * time.Hour, Now: func() time.Time { return now }}); res.Orphans != 0 || res.Young != 1 {
		t.Errorf("tương lai: %+v", res)
	}
}

func TestSweepDryRunAndMaxDelete(t *testing.T) {
	e := newSweepEnv(t)
	for i := 0; i < 30; i++ {
		e.create(fmt.Sprintf("live%d", i), "x")
	}
	for i := 0; i < 5; i++ {
		e.plant(fmt.Sprintf("orph%d", i), "v1")
	}
	e.ageAll(48 * time.Hour)
	res, err := e.sweep(fhirbinary.SweepOptions{DryRun: true})
	if err != nil || res.Orphans != 5 || res.Deleted != 0 || len(res.Keys) != 5 || len(e.keys()) != 35 {
		t.Fatalf("dry-run: %+v %v", res, err)
	}
	res, err = e.sweep(fhirbinary.SweepOptions{MaxDelete: 2})
	if err != nil || res.Orphans != 5 || res.Deleted != 2 || len(e.keys()) != 33 {
		t.Fatalf("MaxDelete: %+v %v", res, err)
	}
	res, _ = e.sweep(fhirbinary.SweepOptions{})
	if res.Deleted != 3 || len(e.keys()) != 30 {
		t.Fatalf("phần còn lại: %+v", res)
	}
}

func TestSweepAbortsWhenMostKeysLookOrphaned(t *testing.T) {
	e := newSweepEnv(t)
	for i := 0; i < 25; i++ {
		e.plant(fmt.Sprintf("o%d", i), "v1") // repo rỗng: mọi thứ trông như mồ côi (trỏ nhầm CSDL)
	}
	e.ageAll(48 * time.Hour)
	res, err := e.sweep(fhirbinary.SweepOptions{})
	if !errors.Is(err, fhirbinary.ErrSweepAborted) || res.Deleted != 0 || len(e.keys()) != 25 {
		t.Fatalf("%+v %v", res, err)
	}
	if res, err = e.sweep(fhirbinary.SweepOptions{Force: true}); err != nil || res.Deleted != 25 {
		t.Fatalf("Force: %+v %v", res, err)
	}
	// ít khóa (<20) thì không áp chốt chặn; đúng một nửa cũng không
	e2 := newSweepEnv(t)
	e2.plant("a", "v1")
	e2.ageAll(48 * time.Hour)
	if res, err := e2.sweep(fhirbinary.SweepOptions{}); err != nil || res.Deleted != 1 {
		t.Errorf("ít khóa: %+v %v", res, err)
	}
	e3 := newSweepEnv(t)
	for i := 0; i < 10; i++ {
		e3.create(fmt.Sprintf("l%d", i), "x")
		e3.plant(fmt.Sprintf("o%d", i), "v1")
	}
	e3.ageAll(48 * time.Hour)
	if res, err := e3.sweep(fhirbinary.SweepOptions{}); err != nil || res.Deleted != 10 {
		t.Errorf("đúng một nửa: %+v %v", res, err)
	}
}

type failingRepo struct {
	fhirrouter.Repository
	err error
}

func (f failingRepo) ReadVersion(context.Context, string, string, string) (fhir.Resource, error) {
	return nil, f.err
}

func TestSweepKeepsKeysWhenLookupFails(t *testing.T) {
	e := newSweepEnv(t)
	e.plant("a", "v1")
	e.ageAll(48 * time.Hour)
	for name, err := range map[string]error{"db": errors.New("connection refused"), "500": fhirrouter.Internal("boom"), "403": fhirrouter.Forbidden("no")} {
		res, serr := fhirbinary.Sweep(e.ctx, e.mem, failingRepo{e.repo, err}, fhirbinary.SweepOptions{})
		if serr != nil || res.Errors != 1 || res.Deleted != 0 || len(e.keys()) != 1 {
			t.Errorf("%s: %+v %v", name, res, serr)
		}
	}
	// 410 (phiên bản xóa) là có tồn tại
	res, _ := fhirbinary.Sweep(e.ctx, e.mem, failingRepo{e.repo, fhirrouter.Gone()}, fhirbinary.SweepOptions{})
	if res.Live != 1 || res.Deleted != 0 {
		t.Errorf("410: %+v", res)
	}
}

func TestSweepContextAndValidation(t *testing.T) {
	e := newSweepEnv(t)
	e.plant("a", "v1")
	e.ageAll(48 * time.Hour)
	c, cancel := context.WithCancel(e.ctx)
	cancel()
	if _, err := fhirbinary.Sweep(c, e.mem, e.repo, fhirbinary.SweepOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("ctx: %v", err)
	}
	// lỗi tra cứu do ctx hủy giữa chừng không bị tính là lỗi thường
	cc, cancel2 := context.WithCancel(e.ctx)
	r := ctxCancelRepo{Repository: e.repo, cancel: cancel2}
	if _, err := fhirbinary.Sweep(cc, e.mem, r, fhirbinary.SweepOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("ctx giữa chừng: %v", err)
	}
	if _, err := e.sweep(fhirbinary.SweepOptions{Grace: time.Minute}); err == nil {
		t.Error("ân hạn dưới mức tối thiểu phải lỗi")
	}
	if _, err := e.sweep(fhirbinary.SweepOptions{Grace: -time.Hour}); err == nil {
		t.Error("ân hạn âm phải lỗi")
	}
	if _, err := fhirbinary.Sweep(e.ctx, noListStore{e.mem}, e.repo, fhirbinary.SweepOptions{}); err == nil {
		t.Error("kho không duyệt được phải lỗi")
	}
	if len(e.keys()) != 1 {
		t.Error("không được xóa gì")
	}
}

type ctxCancelRepo struct {
	fhirrouter.Repository
	cancel context.CancelFunc
}

func (c ctxCancelRepo) ReadVersion(context.Context, string, string, string) (fhir.Resource, error) {
	c.cancel()
	return nil, errors.New("canceled while reading")
}

type noListStore struct{ binarystore.Store }

type tempStore struct {
	*binarystore.Memory
	cleaned int
	grace   time.Duration
}

func (s *tempStore) CleanTemp(_ context.Context, d time.Duration) (int, error) {
	s.cleaned++
	s.grace = d
	return 7, nil
}

func TestSweepCleansTempFilesOnlyWhenNotDryRun(t *testing.T) {
	ts := &tempStore{Memory: binarystore.NewMemory()}
	repo := fhirrouter.NewMemoryRepository()
	res, err := fhirbinary.Sweep(context.Background(), ts, repo, fhirbinary.SweepOptions{DryRun: true})
	if err != nil || ts.cleaned != 0 || res.TempRemoved != 0 {
		t.Fatalf("dry-run: %+v %v", res, err)
	}
	res, err = fhirbinary.Sweep(context.Background(), ts, repo, fhirbinary.SweepOptions{Grace: 3 * time.Hour})
	if err != nil || ts.cleaned != 1 || ts.grace != 3*time.Hour || res.TempRemoved != 7 {
		t.Fatalf("%+v %v %d %v", res, err, ts.cleaned, ts.grace)
	}
}

type failDeleteStore struct{ *binarystore.Memory }

func (failDeleteStore) Delete(context.Context, string) error { return errors.New("denied") }

func TestSweepCountsDeleteErrors(t *testing.T) {
	m := binarystore.NewMemory()
	k, _ := binarystore.Key("o", "v1")
	_, _ = m.Put(context.Background(), k, strings.NewReader("x"), binarystore.PutOptions{})
	m.SetModTime(k, time.Now().Add(-48*time.Hour))
	res, err := fhirbinary.Sweep(context.Background(), failDeleteStore{m}, fhirrouter.NewMemoryRepository(), fhirbinary.SweepOptions{})
	if err != nil || res.Errors != 1 || res.Deleted != 0 || res.Orphans != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}
