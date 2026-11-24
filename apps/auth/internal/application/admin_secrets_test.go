package application_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func (f *admFix) secrets(t *testing.T) []fhir.ProjectSetting {
	t.Helper()
	p, err := f.svc.Projects.Get(context.Background(), f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p.Secret
}

func TestAdminReplaceSecretsValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		many := make([]fhir.ProjectSetting, 51)
		for i := range many {
			many[i] = fhir.ProjectSetting{Name: "k" + strings.Repeat("x", i), ValueString: "v"}
		}
		bad := map[string][]fhir.ProjectSetting{
			"tên rỗng":         {{ValueString: "v"}},
			"tên bắt đầu số":   {{Name: "1k", ValueString: "v"}},
			"tên có khoảng":    {{Name: "a b", ValueString: "v"}},
			"tên quá dài":      {{Name: "a" + strings.Repeat("b", 64), ValueString: "v"}},
			"trùng tên":        {{Name: "k", ValueString: "1"}, {Name: "k", ValueString: "2"}},
			"không có giá trị": {{Name: "k"}},
			"hai giá trị":      {{Name: "k", ValueString: "v", ValueBoolean: ptr(true)}},
			"giá trị quá dài":  {{Name: "k", ValueString: strings.Repeat("a", 4097)}},
			"quá nhiều":        many,
			"giữ khi chưa có":  {{Name: "moi", ValueString: "********"}},
			"smtp khi tắt":     {{Name: "smtpHost", ValueString: "smtp.example.org"}},
		}
		for name, in := range bad {
			_, err := f.svc.AdminReplaceSecrets(ctx, f.caller, f.project.ID, in)
			if !domain.Is(err, domain.KindBadRequest, "") {
				t.Errorf("%s: %v", name, err)
			}
		}
		if got := f.secrets(t); len(got) != 1 || got[0].ValueString != "bi-mat" {
			t.Fatalf("lỗi không được đổi gì: %+v", got)
		}
		_, err := f.svc.AdminReplaceSecrets(ctx, f.caller, "project-khac", nil)
		wantErr(t, err, domain.KindForbidden, "")
		_, err = f.svc.AdminReplaceSecrets(ctx, &application.AuthState{}, f.project.ID, nil)
		wantErr(t, err, domain.KindUnauthorized, "")
		_, mm := f.member("thuong@adm.example.org", false, nil)
		_, err = f.svc.AdminReplaceSecrets(ctx, &application.AuthState{Login: f.caller.Login, Membership: mm, Project: f.project}, f.project.ID, nil)
		wantErr(t, err, domain.KindForbidden, "")
	})
}

func TestAdminReplaceSecretsReplacesAndKeepsMasked(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		ctx := context.Background()
		v, err := f.svc.AdminReplaceSecrets(ctx, f.caller, f.project.ID, []fhir.ProjectSetting{
			{Name: "k", ValueString: "********"}, // giữ "bi-mat"
			{Name: "so", ValueInteger: ptr(7)},
			{Name: "co", ValueBoolean: ptr(false)},
			{Name: "moi", ValueString: "gia-tri"},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := f.secrets(t)
		if len(got) != 4 || got[0].ValueString != "bi-mat" || got[1].ValueInteger == nil || *got[1].ValueInteger != 7 || got[2].ValueBoolean == nil || *got[2].ValueBoolean || got[3].ValueString != "gia-tri" {
			t.Fatalf("lưu: %+v", got)
		}
		for _, st := range v.Secret { // phản hồi luôn che
			if st.ValueString != "" && st.ValueString != "********" {
				t.Fatalf("phản hồi lộ giá trị: %+v", v.Secret)
			}
		}
		// giữ secret kiểu không phải chuỗi bằng giá trị che
		if _, err = f.svc.AdminReplaceSecrets(ctx, f.caller, f.project.ID, []fhir.ProjectSetting{{Name: "so", ValueString: "********"}}); err != nil {
			t.Fatal(err)
		}
		if got = f.secrets(t); len(got) != 1 || got[0].ValueInteger == nil || *got[0].ValueInteger != 7 {
			t.Fatalf("giữ số: %+v", got)
		}
		// [] xóa hết
		if _, err = f.svc.AdminReplaceSecrets(ctx, f.caller, f.project.ID, []fhir.ProjectSetting{}); err != nil {
			t.Fatal(err)
		}
		if got = f.secrets(t); len(got) != 0 {
			t.Fatalf("xóa hết: %+v", got)
		}
	})
}

func TestAdminReplaceSecretsSMTPWhenEnabled(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		e.svc.Email.AllowProjectSMTP = true
		f := e.adminFixture()
		if _, err := f.svc.AdminReplaceSecrets(context.Background(), f.caller, f.project.ID, []fhir.ProjectSetting{{Name: "smtpHost", ValueString: "smtp.example.org"}}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAdminReplaceSecretsAuditLogHasNamesOnly(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		var buf bytes.Buffer
		e.svc.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		f := e.adminFixture()
		if _, err := f.svc.AdminReplaceSecrets(context.Background(), f.caller, f.project.ID, []fhir.ProjectSetting{{Name: "OPENAI_KEY", ValueString: "sk-rat-bi-mat"}}); err != nil {
			t.Fatal(err)
		}
		log := buf.String()
		if !strings.Contains(log, "audit=true") || !strings.Contains(log, "OPENAI_KEY") || strings.Contains(log, "sk-rat-bi-mat") {
			t.Fatalf("log: %q", log)
		}
	})
}
