package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/pkg/dicomtest"
)

// DICOMweb qua các tiến trình thật: auth cấp token, dicomweb nhận STOW và phục vụ QIDO/WADO, fhir-server đọc cùng dữ liệu qua API FHIR.
//
//	auth ──JWT/JWKS──> dicomweb  ──ghi──> schema fhir + kho Binary (thư mục dùng chung)
//	     └────────────> fhir-server ──đọc──┘
//
// Dùng chung một tệp SQLite và một thư mục kho Binary giữa dicomweb và fhir-server (như dùng chung schema fhir và bucket ở production).

type dicomStack struct {
	*stack
	dicom     string
	dicomProc *proc
	dicomBin  string
	dicomEnv  map[string]string
}

func newDicomStack(t *testing.T) *dicomStack {
	t.Helper()
	blobs := "file:{DIR}/blobs"
	s := newStackFull(t, false, nil, map[string]string{"FHIR_BINARY_STORAGE": blobs}, nil)
	d := &dicomStack{stack: s}
	port := freePort(t)
	d.dicom = fmt.Sprintf("http://127.0.0.1:%d", port)
	d.dicomBin = build(t, repoRoot(t), s.dir, "dicomweb")
	d.dicomEnv = map[string]string{
		"HTTP_ADDR": fmt.Sprintf("127.0.0.1:%d", port), "ENVIRONMENT": "local", "LOG_JSON": "false", "SHUTDOWN_DRAIN_DELAY": "0s",
		"DATABASE_URL":    "sqlite://" + filepath.Join(s.dir, "fhir.db"), // CÙNG cơ sở dữ liệu với fhir-server (đã được fhir-server migrate)
		"FHIR_JWT_ISSUER": s.auth, "FHIR_JWT_AUDIENCE": s.auth, "FHIR_JWKS_URL": s.auth + "/.well-known/jwks.json",
		"FHIR_BINARY_STORAGE": "file:" + filepath.Join(s.dir, "blobs"), "DICOMWEB_BASE_URL": d.dicom,
	}
	d.dicomProc = start(t, d.dicomBin, d.dicomEnv)
	waitReady(t, d.dicom+"/health/ready")
	return d
}

func (d *dicomStack) restartDicomweb() {
	d.t.Helper()
	if !d.dicomProc.stop() {
		d.t.Fatal("dicomweb không tắt êm")
	}
	d.dicomProc = start(d.t, d.dicomBin, d.dicomEnv)
	waitReady(d.t, d.dicom+"/health/ready")
}

func stowMultipart(files ...[]byte) (string, []byte) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range files {
		pw, _ := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/dicom"}})
		pw.Write(f)
	}
	w.Close()
	return `multipart/related; type="application/dicom"; boundary="` + w.Boundary() + `"`, buf.Bytes()
}

func (d *dicomStack) stow(token, path string, files ...[]byte) resp {
	ct, body := stowMultipart(files...)
	return do(d.t, "POST", d.dicom+path, token, ct, bytes.NewReader(body))
}

func (d *dicomStack) get(token, path string) resp {
	return do(d.t, "GET", d.dicom+path, token, "", nil)
}

func dcm(study, series, sop string, mut ...func(*dicomtest.Meta)) []byte {
	m := dicomtest.Meta{StudyUID: dicomtest.UID(study), SeriesUID: dicomtest.UID(study, series), SOPUID: dicomtest.UID(study, series, sop), StudyDate: "20240102"}
	for _, f := range mut {
		f(&m)
	}
	return dicomtest.Build(m)
}

func parseFrames(t *testing.T, r resp) [][]byte {
	t.Helper()
	mt, params, err := mime.ParseMediaType(r.hdr.Get("Content-Type"))
	if err != nil || mt != "multipart/related" {
		t.Fatalf("Content-Type %q: %v", r.hdr.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(bytes.NewReader(r.body), params["boundary"])
	var out [][]byte
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		out = append(out, b)
	}
}

func total(t *testing.T, r resp) int {
	t.Helper()
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if n, ok := r.json(t)["total"].(float64); ok {
		return int(n)
	}
	return len(r.json(t)["entry"].([]any))
}

func TestDicomwebThroughRealProcesses(t *testing.T) {
	d := newDicomStack(t)
	adminA := d.signIn("a@example.org", d.clientA)["access_token"].(string)
	clientB := d.clientCredentials(d.clientB, d.secretB, "")

	px := make([]byte, 6*4*2*3)
	for i := range px {
		px[i] = byte(i*5 + 2)
	}
	jpeg := [][]byte{bytes.Repeat([]byte{0xFF, 0xD8, 1, 2}, 30), bytes.Repeat([]byte{0xFF, 0xD8, 3, 4}, 20)}
	f1 := dcm("1", "1", "1", func(m *dicomtest.Meta) {
		m.Rows, m.Cols, m.Bits, m.Frames, m.Pixel = 4, 6, 16, 3, px
		m.PatientName = "Tran^Van A"
	})
	f2 := dcm("1", "1", "2", func(m *dicomtest.Meta) {
		m.TransferSyntax, m.Bits, m.Frames, m.Fragments = dicomtest.JPEGBaseline, 8, 2, jpeg
	})
	f3 := dcm("1", "2", "1", func(m *dicomtest.Meta) { m.Modality = "MR" })

	// STOW bằng token của auth
	r := d.stow(adminA, "/dicomweb/studies", f1, f2, f3)
	if r.status != 200 || r.hdr.Get("Content-Type") != "application/dicom+json" {
		t.Fatalf("STOW: %d %q %s", r.status, r.hdr.Get("Content-Type"), r.body)
	}
	var stow map[string]struct{ Value []any }
	if err := json.Unmarshal(r.body, &stow); err != nil || len(stow["00081199"].Value) != 3 || stow["00081198"].Value != nil {
		t.Fatalf("phản hồi STOW: %s", r.body)
	}
	if got := stow["00081190"].Value[0]; got != d.dicom+"/dicomweb/studies/"+dicomtest.UID("1") {
		t.Errorf("RetrieveURL: %v", got)
	}
	// tải lại: idempotent
	if r := d.stow(adminA, "/dicomweb/studies", f1); r.status != 200 {
		t.Fatalf("tải lại: %d %s", r.status, r.body)
	}

	// fhir-server (tiến trình khác) thấy cùng dữ liệu qua API FHIR, trong đúng project
	if n := total(t, d.fhirGet(adminA, "/DicomStudy")); n != 1 {
		t.Errorf("fhir-server thấy %d DicomStudy, muốn 1", n)
	}
	if n := total(t, d.fhirGet(adminA, "/DicomInstance")); n != 3 {
		t.Errorf("fhir-server thấy %d DicomInstance, muốn 3 (tải lại không tạo bản trùng)", n)
	}
	study := d.fhirGet(adminA, "/DicomStudy").json(t)["entry"].([]any)[0].(map[string]any)["resource"].(map[string]any)
	if study["patientName"] != "Tran^Van A" || study["studyDate"] != "2024-01-02" || study["numberOfStudyRelatedInstances"] != float64(3) || study["numberOfStudyRelatedSeries"] != float64(2) {
		t.Errorf("DicomStudy: %v", study)
	}
	// nội dung do dicomweb ghi vào kho được fhir-server đọc lại đúng từng byte qua Binary
	inst := d.fhirGet(adminA, "/DicomInstance?sop-instance-uid:exact="+dicomtest.UID("1", "1", "1")).json(t)["entry"].([]any)[0].(map[string]any)["resource"].(map[string]any)
	binID := strings.TrimPrefix(inst["raw"].(map[string]any)["reference"].(string), "Binary/")
	if raw := do(t, "GET", d.fhir+"/fhir/R4/Binary/"+binID, adminA, "", nil); raw.status != 200 || !bytes.Equal(raw.body, f1) {
		t.Errorf("Binary %s qua fhir-server: %d, %d byte (gửi %d byte)", binID, raw.status, len(raw.body), len(f1))
	}

	// QIDO, WADO qua dicomweb
	s1, se1 := dicomtest.UID("1"), dicomtest.UID("1", "1")
	if r := d.get(adminA, "/dicomweb/studies"); r.status != 200 || bytes.Count(r.body, []byte(`"0020000D"`)) != 1 {
		t.Errorf("QIDO studies: %d %s", r.status, r.body)
	}
	if r := d.get(adminA, "/dicomweb/studies/"+s1+"/series"); r.status != 200 || bytes.Count(r.body, []byte(`"0020000E"`)) != 2 {
		t.Errorf("QIDO series: %d %s", r.status, r.body)
	}
	if r := d.get(adminA, "/dicomweb/studies/"+s1+"/series/"+se1+"/metadata"); r.status != 200 || bytes.Count(r.body, []byte(`"00080018"`)) != 2 || bytes.Contains(r.body, []byte("7FE00010")) {
		t.Errorf("metadata: %d %s", r.status, r.body)
	}
	i1 := "/dicomweb/studies/" + s1 + "/series/" + se1 + "/instances/"
	if fr := parseFrames(t, d.get(adminA, i1+dicomtest.UID("1", "1", "1")+"/frames/2,3")); len(fr) != 2 || !bytes.Equal(fr[0], px[48:96]) || !bytes.Equal(fr[1], px[96:144]) {
		t.Errorf("frame không nén sai")
	}
	if fr := parseFrames(t, d.get(adminA, i1+dicomtest.UID("1", "1", "2")+"/frames/2")); len(fr) != 1 || !bytes.Equal(fr[0], jpeg[1]) {
		t.Errorf("frame JPEG sai")
	}
	if r := d.get(adminA, i1+dicomtest.UID("1", "1", "1")+"/frames/4"); r.status != 416 {
		t.Errorf("frame vượt số: %d", r.status)
	}

	// cách ly project: B không thấy gì của A, không lấy được frame, và có bản riêng khi tải cùng tệp
	if r := d.get(clientB, "/dicomweb/studies"); r.status != 200 || strings.TrimSpace(string(r.body)) != "[]" {
		t.Errorf("project B thấy: %d %s", r.status, r.body)
	}
	if r := d.get(clientB, i1+dicomtest.UID("1", "1", "1")+"/frames/1"); r.status != 404 {
		t.Errorf("project B lấy frame của A: %d", r.status)
	}
	if r := d.stow(clientB, "/dicomweb/studies", f1); r.status != 200 {
		t.Errorf("project B STOW cùng tệp: %d %s", r.status, r.body)
	}
	if total(t, d.fhirGet(adminA, "/DicomInstance")) != 3 || total(t, d.fhirGet(clientB, "/DicomInstance")) != 1 {
		t.Error("cách ly project giữa hai tiến trình bị vỡ")
	}
	// tiền tố có project phải khớp project của token
	if r := d.get(adminA, "/projects/"+d.projectA+"/dicomweb/studies"); r.status != 200 {
		t.Errorf("tiền tố project của mình: %d", r.status)
	}
	if r := d.get(adminA, "/projects/"+d.projectB+"/dicomweb/studies"); r.status != 403 {
		t.Errorf("tiền tố project khác: %d", r.status)
	}

	// quyền theo scope SMART: token chỉ đọc đọc được nhưng không STOW được
	readOnly := d.clientCredentials(d.clientA, d.secretA, "system/*.rs")
	if r := d.get(readOnly, "/dicomweb/studies"); r.status != 200 || bytes.Count(r.body, []byte(`"0020000D"`)) != 1 {
		t.Errorf("token chỉ đọc QIDO: %d %s", r.status, r.body)
	}
	if r := d.stow(readOnly, "/dicomweb/studies", dcm("5", "1", "1")); r.status != 403 {
		t.Errorf("token chỉ đọc STOW: %d %s", r.status, r.body)
	}
	if total(t, d.fhirGet(adminA, "/DicomStudy")) != 1 {
		t.Error("STOW bị từ chối vẫn tạo dữ liệu")
	}

	// dữ liệu và chỉ mục frame còn nguyên sau khi dicomweb khởi động lại (và dicomweb tắt êm)
	d.restartDicomweb()
	if fr := parseFrames(t, d.get(adminA, i1+dicomtest.UID("1", "1", "1")+"/frames/1")); len(fr) != 1 || !bytes.Equal(fr[0], px[:48]) {
		t.Errorf("frame sau restart sai")
	}
}

// Token giả mạo hoặc của bên khác bị dicomweb từ chối như fhir-server (kiểm cục bộ bằng JWKS của auth).
func TestDicomwebRejectsForgedAndForeignTokens(t *testing.T) {
	d := newDicomStack(t)
	good := d.signIn("a@example.org", d.clientA)["access_token"].(string)
	if r := d.get(good, "/dicomweb/studies"); r.status != 200 {
		t.Fatalf("token thật: %d", r.status)
	}
	parsed, _, _ := jwt.NewParser().ParseUnverified(good, jwt.MapClaims{})
	claims := func() jwt.MapClaims { return parsed.Claims.(jwt.MapClaims) }
	kid := parsed.Header["kid"].(string)
	rogue, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sign := func(m jwt.SigningMethod, key any, mut func(jwt.MapClaims)) string {
		c := jwt.MapClaims{}
		for k, v := range claims() {
			c[k] = v
		}
		if mut != nil {
			mut(c)
		}
		tok := jwt.NewWithClaims(m, c)
		tok.Header["kid"] = kid
		raw, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"`+d.auth+`","aud":"`+d.auth+`","sub":"x","fhir_project":"khac","scope":"fhir.admin","exp":4102444800}`)) + "." + parts[2]
	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims())
	none.Header["kid"] = kid
	noneRaw, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)

	for name, tok := range map[string]string{
		"không token": "", "rác": "abc.def.ghi", "ký bằng khóa lạ, mượn kid thật": sign(jwt.SigningMethodES256, rogue, nil),
		"payload bị sửa, giữ chữ ký": tampered, "alg none": noneRaw, "HS256 ký bằng chuỗi bất kỳ": sign(jwt.SigningMethodHS256, []byte("bat-ky-khoa-nao-0123456789abcdef"), nil),
		"sai audience": sign(jwt.SigningMethodES256, rogue, func(c jwt.MapClaims) { c["aud"] = "khac" }),
	} {
		if r := d.get(tok, "/dicomweb/studies"); r.status != 401 {
			t.Errorf("QIDO %s: %d %s", name, r.status, r.body)
		}
		if r := d.stow(tok, "/dicomweb/studies", dcm("1", "1", "1")); r.status != 401 {
			t.Errorf("STOW %s: %d %s", name, r.status, r.body)
		}
	}
	tk := d.signIn("a@example.org", d.clientA)
	for name, other := range map[string]string{"id token": tk["id_token"].(string), "refresh token": tk["refresh_token"].(string)} {
		if r := d.get(other, "/dicomweb/studies"); r.status == 200 {
			t.Errorf("%s dùng được ở dicomweb: %d", name, r.status)
		}
	}
}

// dicomweb không tự migrate: chạy trên cơ sở dữ liệu chưa được fhir-server migrate thì thoát ngay với thông báo rõ ràng.
func TestDicomwebRefusesUnmigratedDatabase(t *testing.T) {
	d := newDicomStack(t)
	env := map[string]string{}
	for k, v := range d.dicomEnv {
		env[k] = v
	}
	env["DATABASE_URL"] = "sqlite://" + filepath.Join(d.dir, "trong.db")
	env["HTTP_ADDR"] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	out, err := runOnce(d.dicomBin, env)
	if err == nil || !strings.Contains(out, "migrate") {
		t.Errorf("phải thoát với lỗi nhắc migrate: err=%v out=%s", err, out)
	}
}

// runOnce chạy binary tới khi thoát (tối đa 20 giây) và trả đầu ra gộp.
func runOnce(bin string, env map[string]string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = envList(env)
	var buf lockedBuf
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return buf.String(), fmt.Errorf("không thoát sau 20 giây")
	}
}

// `dicomweb reindex` chạy như lệnh riêng trên cùng cơ sở dữ liệu và kho với server đang chạy: dữ liệu STOW đã có chỉ mục nên không còn gì để lập; sai cờ thì lỗi; chưa migrate thì từ chối.
func TestDicomwebReindexCommand(t *testing.T) {
	d := newDicomStack(t)
	adminA := d.signIn("a@example.org", d.clientA)["access_token"].(string)
	if r := d.stow(adminA, "/dicomweb/studies", dcm("1", "1", "1"), dcm("1", "1", "2")); r.status != 200 {
		t.Fatalf("STOW: %d %s", r.status, r.body)
	}
	env := map[string]string{}
	for k, v := range d.dicomEnv {
		env[k] = v
	}
	env["HTTP_ADDR"] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	for _, args := range [][]string{{"reindex"}, {"reindex", "--dry-run"}, {"reindex", "--force", "--limit", "1"}} {
		out, err := runOnce(d.dicomBin, env, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		want := "đã duyệt 2, lập chỉ mục 0, đã đủ 2"
		if args[len(args)-1] == "1" {
			want = "đã duyệt 2, lập chỉ mục 1, đã đủ 0"
		}
		if !strings.Contains(out, want) || !strings.Contains(out, "lỗi 0") {
			t.Errorf("%v: %s (muốn chứa %q)", args, out, want)
		}
	}
	if out, err := runOnce(d.dicomBin, env, "reindex", "--bogus"); err == nil {
		t.Errorf("cờ sai phải lỗi: %s", out)
	}
	env["DATABASE_URL"] = "sqlite://" + filepath.Join(d.dir, "trong.db")
	if out, err := runOnce(d.dicomBin, env, "reindex"); err == nil || !strings.Contains(out, "migrate") {
		t.Errorf("chưa migrate phải từ chối: err=%v %s", err, out)
	}
	// server vẫn phục vụ đúng sau khi chạy lệnh
	if r := d.get(adminA, "/dicomweb/studies/"+dicomtest.UID("1")+"/series/"+dicomtest.UID("1", "1")+"/instances/"+dicomtest.UID("1", "1", "1")+"/frames/1"); r.status != 200 {
		t.Errorf("frame sau reindex: %d", r.status)
	}
}
