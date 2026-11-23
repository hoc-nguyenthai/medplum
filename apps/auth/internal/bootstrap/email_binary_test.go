package bootstrap

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"trivita-fhir/apps/auth/internal/config"
)

// fakeFHIR giả dịch vụ FHIR: ghi lại Authorization và đường dẫn của mọi yêu cầu, trả Binary theo bảng.
type fakeFHIR struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
	paths []string
}

func newFakeFHIR(t *testing.T, binaries map[string]struct {
	status int
	ct     string
	body   string
}) *fakeFHIR {
	t.Helper()
	f := &fakeFHIR{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		b, ok := binaries[strings.TrimPrefix(r.URL.Path, "/fhir/R4/Binary/")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if b.status != 0 && b.status != 200 {
			w.WriteHeader(b.status)
			return
		}
		w.Header().Set("Content-Type", b.ct)
		_, _ = io.WriteString(w, b.body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeFHIR) calls() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auths...), append([]string(nil), f.paths...)
}

// attachmentBodies giải mã mọi phần base64 của thư thành chuỗi.
func attachmentBodies(t *testing.T, data string) []string {
	t.Helper()
	msg := parseMail(t, data)
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(ct string, p map[string]string, r io.Reader)
	walk = func(ct string, p map[string]string, r io.Reader) {
		if strings.HasPrefix(ct, "multipart/") {
			mr := multipart.NewReader(r, p["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					return
				}
				pct, pp, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
				walk(pct, pp, part)
			}
		}
		raw, _ := io.ReadAll(r)
		if d, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", "")); err == nil {
			out = append(out, string(d))
		}
	}
	walk(mt, params, msg.Body)
	return out
}

func TestHTTPEmailBinaryAttachmentReadWithCallerToken(t *testing.T) {
	fhirSrv := newFakeFHIR(t, map[string]struct {
		status int
		ct     string
		body   string
	}{
		"doc-1":  {200, "application/pdf", "%PDF contents"},
		"locked": {403, "", ""},
		"gone":   {410, "", ""},
		"broken": {500, "", ""},
	})
	a, srv, hdr := emailAPI(t, func(s *config.Settings) { s.EmailFHIRURL = fhirSrv.URL + "/fhir/R4" })
	send := func(att string) resp {
		return a.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S","text":"T","attachments":`+att+`}`, jsonHdr(hdr))
	}
	// thành công: nội dung vào thư, FHIR nhận đúng Bearer của người gọi
	if r := send(`[{"filename":"report.pdf","path":"Binary/doc-1"}]`); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	mails := srv.Snapshot().Mails
	if len(mails) != 1 {
		t.Fatalf("%d thư", len(mails))
	}
	if got := attachmentBodies(t, mails[0].Data); len(got) == 0 || !strings.Contains(strings.Join(got, "|"), "%PDF contents") {
		t.Fatalf("tệp đính kèm: %v\n%s", got, mails[0].Data)
	}
	if !strings.Contains(mails[0].Data, "application/pdf") || !strings.Contains(mails[0].Data, "report.pdf") {
		t.Errorf("thiếu loại hoặc tên tệp: %s", mails[0].Data)
	}
	auths, paths := fhirSrv.calls()
	if len(auths) != 1 || auths[0] != hdr["Authorization"] || paths[0] != "/fhir/R4/Binary/doc-1" {
		t.Fatalf("FHIR nhận %v %v; muốn Bearer của người gọi", auths, paths)
	}
	// lỗi: 400 và KHÔNG gửi thêm thư
	for name, tc := range map[string]struct {
		att  string
		code int
		want string
	}{
		"không tồn tại":  {`[{"path":"Binary/missing"}]`, 400, "Attachment not found: Binary/missing"},
		"410":            {`[{"path":"Binary/gone"}]`, 400, "Attachment not found: Binary/gone"},
		"không quyền":    {`[{"path":"Binary/locked"}]`, 400, "Not authorized to read attachment: Binary/locked"},
		"FHIR lỗi 500":   {`[{"path":"Binary/broken"}]`, 502, "Failed to read attachment"},
		"id xấu":         {`[{"path":"Binary/../../admin"}]`, 400, "File access rejected"},
		"hai tệp 1 hỏng": {`[{"path":"Binary/doc-1"},{"path":"Binary/missing"}]`, 400, "Attachment not found"},
	} {
		r := send(tc.att)
		if r.status != tc.code || !strings.Contains(strings.Join(issueTexts(t, r), "|"), tc.want) {
			t.Errorf("%s: %d %s (mong %d %q)", name, r.status, r.body, tc.code, tc.want)
		}
		if strings.Contains(string(r.body), fhirSrv.URL) || strings.Contains(string(r.body), strings.TrimPrefix(hdr["Authorization"], "Bearer ")) {
			t.Errorf("%s: phản hồi lộ địa chỉ FHIR hoặc token", name)
		}
	}
	if n := len(srv.Snapshot().Mails); n != 1 {
		t.Fatalf("chỉ thư thành công được gửi, có %d", n)
	}
	// id xấu không bao giờ tới FHIR
	_, paths = fhirSrv.calls()
	for _, p := range paths {
		if strings.Contains(p, "..") || strings.Contains(p, "admin") {
			t.Errorf("đường dẫn xấu tới FHIR: %s", p)
		}
	}
}

func TestHTTPEmailBinaryAttachmentNotConfigured(t *testing.T) {
	a, srv, hdr := emailAPI(t, nil)
	r := a.do("POST", "/email/v1/send", `{"to":"to@x.org","subject":"S","attachments":[{"path":"Binary/123"}]}`, jsonHdr(hdr))
	if r.status != 400 || !strings.Contains(strings.Join(issueTexts(t, r), "|"), "Binary attachments are not supported") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if len(srv.Snapshot().Mails) != 0 {
		t.Fatal("không được gửi thư")
	}
}
