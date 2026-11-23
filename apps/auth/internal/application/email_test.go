package application_test

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/mailer"
)

// fakeSender ghi lại thư; Configure quyết định "đã cấu hình" (có thể phụ thuộc SMTP của project).
type fakeSender struct {
	mailer.Fake
	configured func(*mailer.ProjectContext) bool
	pcs        []*mailer.ProjectContext
	bodies     []string
}

func (f *fakeSender) Send(ctx context.Context, m mailer.Message) error {
	f.pcs = append(f.pcs, m.Project)
	for _, a := range m.Attachments {
		b, _ := io.ReadAll(a.Body)
		f.bodies = append(f.bodies, string(b))
	}
	return f.Fake.Send(ctx, m)
}

func (f *fakeSender) Configured(p *mailer.ProjectContext) bool {
	if f.configured == nil {
		return true
	}
	return f.configured(p)
}

type fakeFetcher struct {
	data  map[string]string
	err   error
	calls []string
	max   int64
}

func (f *fakeFetcher) Fetch(_ context.Context, u string, max int64) ([]byte, error) {
	f.calls = append(f.calls, u)
	f.max = max
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.data[u]), nil
}

type emailFix struct {
	*admFix
	sender  *fakeSender
	fetcher *fakeFetcher
}

func (e *env) emailFixture(mutProject func(*fhir.Project), cfg application.EmailConfig) *emailFix {
	f := &emailFix{admFix: e.adminFixture(), sender: &fakeSender{}, fetcher: &fakeFetcher{data: map[string]string{"https://files.example.org/a.txt": "nội dung tải về"}}}
	cp := *f.project
	cp.Features = []string{"email"}
	cp.Setting = []fhir.ProjectSetting{{Name: "appName", ValueString: "  Phòng khám  "}}
	if mutProject != nil {
		mutProject(&cp)
	}
	up, err := e.repo.UpdateResource(context.Background(), &cp, fhirUpdate())
	if err != nil {
		e.t.Fatal(err)
	}
	f.project = up.(*fhir.Project)
	f.caller.Project = f.project
	d := f.svc.Deps
	d.EmailSender, d.EmailAttachments, d.Email = f.sender, f.fetcher, cfg
	f.svc = application.NewService(d)
	return f
}

func emailReq() application.EmailRequest {
	return application.EmailRequest{Message: mailer.Message{To: []mailer.Address{{Email: "to@x.org"}}, Subject: "Chào", Text: "nội dung"}}
}

func projectSecrets(extra ...fhir.ProjectSetting) func(*fhir.Project) {
	return func(p *fhir.Project) { p.Secret = append(p.Secret, extra...) }
}

func smtpSecrets(host string, port int) []fhir.ProjectSetting {
	return []fhir.ProjectSetting{{Name: "smtpHost", ValueString: host}, {Name: "smtpPort", ValueInteger: &port}, {Name: "smtpUsername", ValueString: "u"},
		{Name: "smtpPassword", ValueString: "p"}, {Name: "smtpFromAddress", ValueString: "no-reply@proj.example.org"}}
}

func TestSendEmailCheckOrder(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.emailFixture(nil, application.EmailConfig{})
		ctx := context.Background()
		wantErr(t, f.svc.SendEmail(ctx, nil, emailReq()), domain.KindUnauthorized, "")
		// tính năng email chưa bật: 400 TRƯỚC kiểm admin
		noFeature := e.emailFixture(func(p *fhir.Project) { p.Features = nil }, application.EmailConfig{})
		plainNF := *noFeature.caller
		pm := *noFeature.am
		pm.Admin = nil
		plainNF.Membership = &pm
		wantErr(t, noFeature.svc.SendEmail(ctx, &plainNF, emailReq()), domain.KindBadRequest, "Email feature is not enabled for this project")
		// không phải admin: 403
		plain := *f.caller
		pm2 := *f.am
		pm2.Admin = nil
		plain.Membership = &pm2
		wantErr(t, f.svc.SendEmail(ctx, &plain, emailReq()), domain.KindForbidden, "Only project administrators can send emails")
		// chưa có đường gửi
		d := f.svc.Deps
		d.EmailSender = nil
		wantErr(t, application.NewService(d).SendEmail(ctx, f.caller, emailReq()), domain.KindBadRequest, "Email is not configured on this server. Set up SMTP.")
		f.sender.configured = func(*mailer.ProjectContext) bool { return false }
		wantErr(t, f.svc.SendEmail(ctx, f.caller, emailReq()), domain.KindBadRequest, "Email is not configured on this server. Set up SMTP.")
		if len(f.sender.Sent()) != 0 {
			t.Error("không được gửi khi chưa cấu hình")
		}
		// cấu hình đủ: gửi, Project context có id và appName đã cắt khoảng trắng
		f.sender.configured = nil
		if err := f.svc.SendEmail(ctx, f.caller, emailReq()); err != nil {
			t.Fatal(err)
		}
		if len(f.sender.pcs) != 1 || f.sender.pcs[0].ID != f.project.ID || f.sender.pcs[0].AppName != "Phòng khám" || f.sender.pcs[0].SMTP != nil {
			t.Errorf("project context: %+v", f.sender.pcs)
		}
	})
}

func TestSendEmailProjectSMTP(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		// AllowProjectSMTP tắt: bỏ qua SMTP project, kể cả khi cấu hình đủ
		off := e.emailFixture(projectSecrets(smtpSecrets("smtp.proj.example.org", 587)...), application.EmailConfig{})
		if err := off.svc.SendEmail(ctx, off.caller, emailReq()); err != nil || off.sender.pcs[0].SMTP != nil {
			t.Fatalf("tắt: %v %+v", err, off.sender.pcs)
		}
		// bật: ProjectContext mang SMTP đã đọc từ Project.secret
		on := e.emailFixture(projectSecrets(smtpSecrets("smtp.proj.example.org", 465)...), application.EmailConfig{AllowProjectSMTP: true})
		if err := on.svc.SendEmail(ctx, on.caller, emailReq()); err != nil {
			t.Fatal(err)
		}
		if p := on.sender.pcs[0].SMTP; p == nil || p.Host != "smtp.proj.example.org" || p.Port != 465 || p.FromAddress != "no-reply@proj.example.org" || !p.ImplicitTLS() {
			t.Errorf("smtp project: %+v", p)
		}
		// cấu hình dở: 400 với thông điệp của Medplum, trước kiểm "đã cấu hình", không gửi gì
		bad := e.emailFixture(projectSecrets(fhir.ProjectSetting{Name: "smtpHost", ValueString: "smtp.proj.example.org"}), application.EmailConfig{AllowProjectSMTP: true})
		bad.sender.configured = func(p *mailer.ProjectContext) bool { return true }
		err := bad.svc.SendEmail(ctx, bad.caller, emailReq())
		wantErr(t, err, domain.KindBadRequest, "Project SMTP configuration is incomplete or invalid")
		if len(bad.sender.Sent()) != 0 {
			t.Error("không được gửi")
		}
		// secret không liên quan: dùng đường server
		other := e.emailFixture(projectSecrets(fhir.ProjectSetting{Name: "OPENAI_API_KEY", ValueString: "k"}), application.EmailConfig{AllowProjectSMTP: true})
		if err := other.svc.SendEmail(ctx, other.caller, emailReq()); err != nil || other.sender.pcs[0].SMTP != nil {
			t.Errorf("secret lạ: %v", err)
		}
		// chỉ SMTP project (server không có): Configured quyết định theo project
		only := e.emailFixture(projectSecrets(smtpSecrets("smtp.proj.example.org", 587)...), application.EmailConfig{AllowProjectSMTP: true})
		only.sender.configured = func(p *mailer.ProjectContext) bool { return p != nil && p.SMTP != nil }
		if err := only.svc.SendEmail(ctx, only.caller, emailReq()); err != nil {
			t.Errorf("chỉ SMTP project: %v", err)
		}
		none := e.emailFixture(nil, application.EmailConfig{AllowProjectSMTP: true})
		none.sender.configured = func(p *mailer.ProjectContext) bool { return p != nil && p.SMTP != nil }
		wantErr(t, none.svc.SendEmail(ctx, none.caller, emailReq()), domain.KindBadRequest, "Email is not configured on this server. Set up SMTP.")
	})
}

func TestSendEmailAttachments(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		f := e.emailFixture(nil, application.EmailConfig{MaxAttachmentBytes: 64, MaxAttachments: 3})
		req := emailReq()
		req.Attachments = []application.EmailAttachment{
			{Filename: "hello.txt", Content: "hello"},
			{Filename: "b64.bin", Content: "aGVsbG8gYjY0", Encoding: "base64", ContentType: "application/octet-stream"},
			{Path: "https://files.example.org/a.txt", Filename: "a.txt"},
		}
		if err := f.svc.SendEmail(ctx, f.caller, req); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(f.sender.bodies, "|"); got != "hello|hello b64|nội dung tải về" {
			t.Errorf("nội dung đính kèm: %q", got)
		}
		if len(f.fetcher.calls) != 1 || f.fetcher.max != 64 {
			t.Errorf("fetch: %v max=%d", f.fetcher.calls, f.fetcher.max)
		}
		sent := f.sender.Sent()[0]
		if sent.Attachments[0].Filename != "hello.txt" || sent.Attachments[1].ContentType != "application/octet-stream" {
			t.Errorf("thuộc tính: %+v", sent.Attachments)
		}
		bad := map[string]struct {
			at  application.EmailAttachment
			sub string
		}{
			"Binary":             {application.EmailAttachment{Path: "Binary/123"}, "Binary attachments are not supported"},
			"tệp cục bộ":         {application.EmailAttachment{Path: "./package.json"}, "File access rejected for ./package.json"},
			"đường tuyệt đối":    {application.EmailAttachment{Path: "/etc/passwd"}, "File access rejected for /etc/passwd"},
			"file://":            {application.EmailAttachment{Path: "file:///etc/passwd"}, "File access rejected"},
			"base64 sai":         {application.EmailAttachment{Content: "!!!", Encoding: "base64"}, "not valid base64"},
			"encoding lạ":        {application.EmailAttachment{Content: "x", Encoding: "hex"}, "Unsupported attachment encoding"},
			"nội dung quá lớn":   {application.EmailAttachment{Content: strings.Repeat("x", 65)}, "Attachment is too large"},
			"path có điều khiển": {application.EmailAttachment{Path: "a\r\nb"}, "File access rejected for a??b"},
		}
		for name, tc := range bad {
			f.sender.Err = nil
			before := len(f.sender.Sent())
			r := emailReq()
			r.Attachments = []application.EmailAttachment{tc.at}
			err := f.svc.SendEmail(ctx, f.caller, r)
			wantErrContains(t, err, domain.KindBadRequest, tc.sub)
			if len(f.sender.Sent()) != before {
				t.Errorf("%s: không được gửi", name)
			}
		}
		// quá nhiều tệp
		r := emailReq()
		for i := 0; i < 4; i++ {
			r.Attachments = append(r.Attachments, application.EmailAttachment{Content: "x"})
		}
		wantErr(t, f.svc.SendEmail(ctx, f.caller, r), domain.KindBadRequest, "Too many attachments")
		// lỗi tải URL: tiền tố cố định, thông điệp của adapter
		f.fetcher.err = errors.New("Outbound request to unsafe hostname 169.254.170.2 is blocked")
		r = emailReq()
		r.Attachments = []application.EmailAttachment{{Path: "https://169.254.170.2/v2/metadata"}}
		wantErr(t, f.svc.SendEmail(ctx, f.caller, r), domain.KindBadRequest, "Failed to fetch attachment: Outbound request to unsafe hostname 169.254.170.2 is blocked")
		// không có fetcher: URL bị từ chối, không tải
		d := f.svc.Deps
		d.EmailAttachments = nil
		wantErrContains(t, application.NewService(d).SendEmail(ctx, f.caller, r), domain.KindBadRequest, "Failed to fetch attachment")
	})
}

func TestSendEmailErrorMapping(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.emailFixture(nil, application.EmailConfig{})
		ctx := context.Background()
		for name, tc := range map[string]struct {
			err  error
			kind domain.Kind
			msg  string
		}{
			"invalid":   {&mailer.Error{Kind: mailer.KindInvalid, Msg: "Too many recipients (max 2)"}, domain.KindBadRequest, "Too many recipients (max 2)"},
			"config":    {&mailer.Error{Kind: mailer.KindConfig, Msg: "Project SMTP configuration is incomplete or invalid"}, domain.KindBadRequest, "Project SMTP configuration is incomplete or invalid"},
			"transport": {&mailer.Error{Kind: mailer.KindTransport, Msg: "Failed to send email", Err: errors.New("550 <victim@x.org>")}, domain.KindBadGateway, "Failed to send email"},
			"lạ":        {errors.New("lỗi lạ chứa victim@x.org"), domain.KindBadGateway, "Failed to send email"},
		} {
			f.sender.Err = tc.err
			err := f.svc.SendEmail(ctx, f.caller, emailReq())
			wantErr(t, err, tc.kind, tc.msg)
			if strings.Contains(err.Error(), "victim") {
				t.Errorf("%s: lộ chi tiết: %v", name, err)
			}
		}
	})
}

type fakeBinaries struct {
	files map[string]string // id → nội dung
	types map[string]string
	err   map[string]error
	calls []string // "token|id|max"
}

func (f *fakeBinaries) Read(_ context.Context, token, id string, max int64) ([]byte, string, error) {
	f.calls = append(f.calls, token+"|"+id+"|"+strconv.FormatInt(max, 10))
	if err := f.err[id]; err != nil {
		return nil, "", err
	}
	c, ok := f.files[id]
	if !ok {
		return nil, "", application.ErrBinaryNotFound
	}
	return []byte(c), f.types[id], nil
}

func TestSendEmailBinaryAttachments(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.emailFixture(nil, application.EmailConfig{MaxAttachmentBytes: 64})
		src := &fakeBinaries{files: map[string]string{"bin-1": "nội dung binary", "bin-2": "x"}, types: map[string]string{"bin-1": "application/pdf; charset=binary", "bin-2": "bad type"},
			err: map[string]error{"denied": application.ErrBinaryForbidden, "big": application.ErrBinaryTooLarge, "boom": errors.New("dial tcp 10.1.2.3:8005: secret-token-xyz refused")}}
		d := f.svc.Deps
		d.EmailBinaries = src
		svc := application.NewService(d)
		ctx := context.Background()
		req := func(paths ...string) application.EmailRequest {
			r := emailReq()
			r.BearerToken = "caller-token"
			for _, p := range paths {
				r.Attachments = append(r.Attachments, application.EmailAttachment{Path: p, Filename: "f.bin"})
			}
			return r
		}

		// thành công: đọc bằng token của người gọi, trần theo cấu hình, ContentType lấy từ Binary (bỏ tham số), thư được gửi
		if err := svc.SendEmail(ctx, f.caller, req("Binary/bin-1")); err != nil {
			t.Fatal(err)
		}
		if len(src.calls) != 1 || src.calls[0] != "caller-token|bin-1|64" {
			t.Fatalf("calls: %v", src.calls)
		}
		sent := f.sender.Sent()
		if got := sent[len(sent)-1].Attachments[0]; got.ContentType != "application/pdf" || got.Filename != "f.bin" {
			t.Fatalf("thuộc tính: %+v", got)
		}
		if f.sender.bodies[len(f.sender.bodies)-1] != "nội dung binary" {
			t.Fatalf("nội dung: %v", f.sender.bodies)
		}
		// contentType của yêu cầu thắng; Content-Type không hợp lệ của Binary bị bỏ
		r := req("Binary/bin-1") // Binary là application/pdf nhưng yêu cầu nêu text/csv
		r.Attachments[0].ContentType = "text/csv"
		if err := svc.SendEmail(ctx, f.caller, r); err != nil {
			t.Fatal(err)
		}
		if got := f.sender.Sent()[len(f.sender.Sent())-1].Attachments[0].ContentType; got != "text/csv" {
			t.Fatalf("contentType yêu cầu: %q", got)
		}
		if err := svc.SendEmail(ctx, f.caller, req("Binary/bin-2")); err != nil {
			t.Fatal(err)
		}
		if got := f.sender.Sent()[len(f.sender.Sent())-1].Attachments[0].ContentType; got != "" {
			t.Fatalf("Content-Type không hợp lệ phải bị bỏ: %q", got)
		}

		// lỗi: không gửi gì
		for name, tc := range map[string]struct {
			path string
			kind domain.Kind
			sub  string
		}{
			"không tồn tại": {"Binary/missing", domain.KindBadRequest, "Attachment not found: Binary/missing"},
			"không quyền":   {"Binary/denied", domain.KindBadRequest, "Not authorized to read attachment: Binary/denied"},
			"quá lớn":       {"Binary/big", domain.KindBadRequest, "Attachment is too large"},
			"lỗi hạ tầng":   {"Binary/boom", domain.KindBadGateway, "Failed to read attachment"},
			"id xấu":        {"Binary/../etc", domain.KindBadRequest, "File access rejected"},
			"có version":    {"Binary/bin-1/_history/2", domain.KindBadRequest, "File access rejected"},
			"có query":      {"Binary/bin-1?x=1", domain.KindBadRequest, "File access rejected"},
			"rỗng":          {"Binary/", domain.KindBadRequest, "File access rejected"},
			"id dài":        {"Binary/" + strings.Repeat("a", 65), domain.KindBadRequest, "File access rejected"},
			"chữ thường":    {"binary/bin-1", domain.KindBadRequest, "File access rejected"},
			"id là .":       {"Binary/.", domain.KindBadRequest, "File access rejected"},
			"id là ..":      {"Binary/..", domain.KindBadRequest, "File access rejected"},
		} {
			before := len(f.sender.Sent())
			err := svc.SendEmail(ctx, f.caller, req(tc.path))
			wantErrContains(t, err, tc.kind, tc.sub)
			if len(f.sender.Sent()) != before {
				t.Errorf("%s: thư vẫn được gửi", name)
			}
			if err != nil && (strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "10.1.2.3")) {
				t.Errorf("%s: lộ chi tiết hạ tầng: %v", name, err)
			}
		}
		// một Binary hỏng trong nhiều tệp thì không gửi thư (kể cả tệp tốt đứng trước)
		before := len(f.sender.Sent())
		wantErrContains(t, svc.SendEmail(ctx, f.caller, req("Binary/bin-1", "Binary/missing")), domain.KindBadRequest, "Attachment not found")
		if len(f.sender.Sent()) != before {
			t.Error("không được gửi thư khi một tệp đính kèm lỗi")
		}
		// chưa cấu hình nguồn Binary: chưa hỗ trợ, và không có lời gọi nào
		src.calls = nil
		wantErrContains(t, f.svc.SendEmail(ctx, f.caller, req("Binary/bin-1")), domain.KindBadRequest, "Binary attachments are not supported")
		if len(src.calls) != 0 {
			t.Fatal("không cấu hình thì không được gọi")
		}
	})
}
