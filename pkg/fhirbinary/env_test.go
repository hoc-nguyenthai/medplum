package fhirbinary_test

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirbinary"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

var (
	keyOnce sync.Once
	keyVal  *rsa.PrivateKey
)

func testKey(t testing.TB) *rsa.PrivateKey {
	keyOnce.Do(func() {
		k, err := binarystore.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		keyVal = k
	})
	return keyVal
}

type fixedClock struct{ t atomic.Int64 }

func (c *fixedClock) Now() time.Time      { return time.Unix(c.t.Load(), 0) }
func (c *fixedClock) Add(d time.Duration) { c.t.Add(int64(d / time.Second)) }

// flakyStore bọc Memory và có thể làm Put/Copy/Open lỗi.
type flakyStore struct {
	*binarystore.Memory
	failPut, failCopy, failOpen atomic.Bool
}

var errDisk = errors.New("disk exploded: /secret/path/leak")

func (s *flakyStore) Put(ctx context.Context, k string, r io.Reader, o binarystore.PutOptions) (int64, error) {
	if s.failPut.Load() {
		return 0, errDisk
	}
	return s.Memory.Put(ctx, k, r, o)
}
func (s *flakyStore) Copy(ctx context.Context, a, b string) error {
	if s.failCopy.Load() {
		return errDisk
	}
	return s.Memory.Copy(ctx, a, b)
}
func (s *flakyStore) Open(ctx context.Context, k string) (io.ReadCloser, error) {
	if s.failOpen.Load() {
		return nil, errDisk
	}
	return s.Memory.Open(ctx, k)
}

type envOpts struct {
	maxUpload, base64Max int64
	jsonMax              int64
	noSigner             bool
	noRewrite            bool
	cannotUpdate         bool
}

// denyRepo là repo của "người gọi" bị chặn đọc một số loại (giả lập thiếu quyền).
type denyRepo struct {
	fhirrouter.Repository
	deny map[string]bool
}

func (d denyRepo) ReadResource(ctx context.Context, rt, id string) (fhir.Resource, error) {
	if d.deny[rt] {
		return nil, fhirrouter.Forbidden("no access to " + rt)
	}
	return d.Repository.ReadResource(ctx, rt, id)
}

func (d denyRepo) ReadVersion(ctx context.Context, rt, id, v string) (fhir.Resource, error) {
	if d.deny[rt] {
		return nil, fhirrouter.Forbidden("no access to " + rt)
	}
	return d.Repository.ReadVersion(ctx, rt, id, v)
}

type env struct {
	t       *testing.T
	repo    *fhirrouter.MemoryRepository
	store   *flakyStore
	wrapped *fhirbinary.Repo
	caller  denyRepo
	srv     *httptest.Server
	signer  *binarystore.Signer
	clk     *fixedClock
}

func newEnv(t *testing.T, mut func(*envOpts)) *env {
	t.Helper()
	o := envOpts{maxUpload: 1 << 20}
	if mut != nil {
		mut(&o)
	}
	e := &env{t: t, repo: fhirrouter.NewMemoryRepository(), store: &flakyStore{Memory: binarystore.NewMemory()}, clk: &fixedClock{}}
	e.clk.t.Store(time.Now().Unix())
	mux := http.NewServeMux()
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	var err error
	e.signer, err = binarystore.NewSigner(binarystore.SignerConfig{BaseURL: e.srv.URL + "/storage", Key: testKey(t), Clock: e.clk})
	if err != nil {
		t.Fatal(err)
	}
	rcfg := fhirbinary.RewriteConfig{FHIRBaseURL: e.srv.URL + "/fhir/R4", StorageBaseURL: e.srv.URL + "/storage"}
	wopt := fhirbinary.WrapOptions{Base64MaxBytes: o.base64Max}
	if !o.noRewrite {
		wopt.Rewrite = fhirbinary.NewRewriter(rcfg, nil, nil)
	}
	e.wrapped = fhirbinary.Wrap(e.repo, e.store, wopt)
	e.caller = denyRepo{Repository: e.wrapped, deny: map[string]bool{}}
	ropts := fhirrouter.Options{BaseURL: e.srv.URL + "/fhir/R4", SkipValidation: true}
	if !o.noRewrite {
		ropts.ResponseHook = fhirbinary.NewRewriter(rcfg, e.caller, e.signer).Response
	}
	router := fhirrouter.New(e.wrapped, ropts)
	var canUpdate func(context.Context, string) bool
	if !o.cannotUpdate {
		canUpdate = func(context.Context, string) bool { return true }
	}
	router.Add("GET", "Binary/:id/$presigned-url", fhirbinary.PresignedURL(fhirbinary.PresignConfig{Repo: e.caller, Signer: e.signer, CanUpdate: canUpdate}))
	t.Cleanup(func() { _ = router.Shutdown(context.Background()) })
	var sg *binarystore.Signer
	if !o.noSigner {
		sg = e.signer
	}
	raw := fhirbinary.RawBinary(fhirbinary.RawConfig{FHIR: router.HTTPHandler(), Repo: e.caller, Store: e.store, Signer: sg, MaxUploadBytes: o.maxUpload, JSONMaxBytes: o.jsonMax})
	mux.Handle("/fhir/R4/", http.StripPrefix("/fhir/R4", raw))
	mux.Handle("/storage/", fhirbinary.StorageHandler(fhirbinary.StorageConfig{Signer: e.signer, Store: e.store, Repo: e.repo, MaxUploadBytes: o.maxUpload}))
	return e
}

type resp struct {
	status int
	h      http.Header
	body   []byte
}

func (r resp) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (e *env) do(method, path string, hdr map[string]string, body []byte) resp {
	e.t.Helper()
	url := path
	if strings.HasPrefix(path, "/") {
		url = e.srv.URL + path
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, b}
}

func (e *env) post(ct string, body []byte, hdr ...string) resp {
	h := map[string]string{"Content-Type": ct}
	for i := 0; i+1 < len(hdr); i += 2 {
		h[hdr[i]] = hdr[i+1]
	}
	return e.do("POST", "/fhir/R4/Binary", h, body)
}

// created tạo Binary thô và trả id, versionId.
func (e *env) created(ct, content string) (id, ver string) {
	e.t.Helper()
	r := e.post(ct, []byte(content))
	if r.status != 201 {
		e.t.Fatalf("POST = %d %s", r.status, r.body)
	}
	m := r.json()
	id, _ = m["id"].(string)
	meta, _ := m["meta"].(map[string]any)
	ver, _ = meta["versionId"].(string)
	return
}

func (e *env) stored(id, ver string) (string, bool) {
	e.t.Helper()
	k, err := binarystore.Key(id, ver)
	if err != nil {
		e.t.Fatal(err)
	}
	rc, err := e.store.Memory.Open(context.Background(), k)
	if err != nil {
		return "", false
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b), true
}

func (e *env) binaryCount() int {
	e.t.Helper()
	r := e.do("GET", "/fhir/R4/Binary", map[string]string{"Accept": "application/fhir+json"}, nil)
	m := r.json()
	ent, _ := m["entry"].([]any)
	return len(ent)
}

func ptr[T any](v T) *T { return &v }

var _ = fhir.Binary{}

// errRepo là repo hệ thống luôn lỗi (không phải not found).
type errRepo struct{ err error }

func (r errRepo) CreateResource(context.Context, fhir.Resource) (fhir.Resource, error) {
	return nil, r.err
}
func (r errRepo) ReadResource(context.Context, string, string) (fhir.Resource, error) {
	return nil, r.err
}
func (r errRepo) ReadVersion(context.Context, string, string, string) (fhir.Resource, error) {
	return nil, r.err
}
func (r errRepo) ReadHistory(context.Context, string, string) ([]fhirrouter.HistoryEntry, error) {
	return nil, r.err
}
func (r errRepo) UpdateResource(context.Context, fhir.Resource, fhirrouter.UpdateOptions) (fhir.Resource, error) {
	return nil, r.err
}
func (r errRepo) DeleteResource(context.Context, string, string) error { return r.err }
func (r errRepo) Search(context.Context, *fhirsearch.Request) (*fhirrouter.SearchResult, error) {
	return nil, r.err
}

func newEnvWithSystemRepo(t *testing.T, sys fhirrouter.Repository) *env {
	t.Helper()
	e := &env{t: t, store: &flakyStore{Memory: binarystore.NewMemory()}, clk: &fixedClock{}}
	e.clk.t.Store(time.Now().Unix())
	mux := http.NewServeMux()
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	var err error
	e.signer, err = binarystore.NewSigner(binarystore.SignerConfig{BaseURL: e.srv.URL + "/storage", Key: testKey(t), Clock: e.clk})
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/storage/", fhirbinary.StorageHandler(fhirbinary.StorageConfig{Signer: e.signer, Store: e.store, Repo: sys}))
	return e
}
