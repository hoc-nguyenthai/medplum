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

func (f *admFix) client(t *testing.T) *fhir.ClientApplication {
	t.Helper()
	c, err := f.svc.CreateClient(context.Background(), f.project.ID, application.NewClient{Name: "Máy đồng bộ", GrantTypes: []string{"client_credentials"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *admFix) stored(t *testing.T, id string) *fhir.ClientApplication {
	t.Helper()
	c, err := f.svc.Clients.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRotateSecretKeepsTheOldSecretAsRetiring(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		c := f.client(t)
		first := c.Secret
		ctx := context.Background()
		res, err := f.svc.AdminRotateClientSecret(ctx, f.caller, f.project.ID, c.ID, application.RotateSecretRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if res.ClientID != c.ID || len(res.Secret) != 64 || res.Secret == first || !res.Retiring {
			t.Fatalf("kết quả: %+v", res)
		}
		got := f.stored(t, c.ID)
		if got.Secret != res.Secret || got.RetiringSecret != first {
			t.Fatalf("lưu: secret=%q retiring=%q", got.Secret, got.RetiringSecret)
		}
		if !domain.MatchClientSecret(got, first) || !domain.MatchClientSecret(got, res.Secret) {
			t.Fatal("cả secret cũ và mới phải dùng được trong lúc xoay")
		}
		// xoay lần nữa: secret cũ nhất bị bỏ, secret ở giữa thành retiring
		res2, err := f.svc.AdminRotateClientSecret(ctx, f.caller, f.project.ID, c.ID, application.RotateSecretRequest{})
		if err != nil {
			t.Fatal(err)
		}
		got = f.stored(t, c.ID)
		if got.RetiringSecret != res.Secret || got.Secret != res2.Secret || domain.MatchClientSecret(got, first) {
			t.Fatalf("lần hai: secret cũ nhất phải hết hiệu lực: %+v", got)
		}
	})
}

func TestRotateSecretWithAGivenSecretAndValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		c := f.client(t)
		ctx := context.Background()
		mine := strings.Repeat("m", 40)
		res, err := f.svc.AdminRotateClientSecret(ctx, f.caller, f.project.ID, c.ID, application.RotateSecretRequest{Secret: mine})
		if err != nil || res.Secret != mine || f.stored(t, c.ID).Secret != mine {
			t.Fatalf("secret do người gọi đặt: %+v %v", res, err)
		}
		for name, tc := range map[string]struct {
			req  application.RotateSecretRequest
			want string
		}{
			"quá ngắn":       {application.RotateSecretRequest{Secret: "ngan"}, "between 32 and 256"},
			"quá dài":        {application.RotateSecretRequest{Secret: strings.Repeat("x", 257)}, "between 32 and 256"},
			"trùng hiện tại": {application.RotateSecretRequest{Secret: mine}, "must differ"},
			"loại trừ":       {application.RotateSecretRequest{Secret: mine + "x", RetireOnly: true}, "mutually exclusive"},
		} {
			if _, err := f.svc.AdminRotateClientSecret(ctx, f.caller, f.project.ID, c.ID, tc.req); domain.KindOf(err) != domain.KindBadRequest || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: muốn 400 %q, nhận %v", name, tc.want, err)
			}
		}
		if got := f.stored(t, c.ID); got.Secret != mine {
			t.Fatalf("lỗi không được đổi secret: %q", got.Secret)
		}
	})
}

func TestRetireOnlyDropsTheRetiringSecretAndIsIdempotent(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		c := f.client(t)
		ctx := context.Background()
		old := c.Secret
		res, _ := f.svc.AdminRotateClientSecret(ctx, f.caller, f.project.ID, c.ID, application.RotateSecretRequest{})
		for i := 0; i < 2; i++ { // lần hai không có gì để bỏ: vẫn thành công
			r, err := f.svc.AdminRotateClientSecret(ctx, f.caller, f.project.ID, c.ID, application.RotateSecretRequest{RetireOnly: true})
			if err != nil || r.Secret != "" || r.Retiring {
				t.Fatalf("lần %d: %+v %v", i+1, r, err)
			}
		}
		got := f.stored(t, c.ID)
		if got.Secret != res.Secret || got.RetiringSecret != "" || domain.MatchClientSecret(got, old) || !domain.MatchClientSecret(got, res.Secret) {
			t.Fatalf("sau retire: %+v", got)
		}
	})
}

func TestRotateSecretIsLimitedToAdminsOfTheClientsProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		c := f.client(t)
		ctx := context.Background()
		other := e.project(nil)
		foreign, err := f.svc.CreateClient(ctx, other.ID, application.NewClient{Name: "Của project khác"})
		if err != nil {
			t.Fatal(err)
		}
		_, nonAdminM := f.member("nv@adm.example.org", false, nil)
		nonAdmin := &application.AuthState{Login: f.caller.Login, Membership: nonAdminM, Project: f.project}
		for name, tc := range map[string]struct {
			a                   *application.AuthState
			projectID, clientID string
			kind                domain.Kind
		}{
			"không đăng nhập":         {nil, f.project.ID, c.ID, domain.KindUnauthorized},
			"không phải admin":        {nonAdmin, f.project.ID, c.ID, domain.KindForbidden},
			"project lệch":            {f.caller, other.ID, c.ID, domain.KindForbidden},
			"client của project khác": {f.caller, f.project.ID, foreign.ID, domain.KindForbidden},
			"client không có":         {f.caller, f.project.ID, "khong-co", domain.KindForbidden},
			"client dựng sẵn":         {f.caller, f.project.ID, domain.StandardClients(true, nil)[0].ID, domain.KindForbidden},
		} {
			if _, err := f.svc.AdminRotateClientSecret(ctx, tc.a, tc.projectID, tc.clientID, application.RotateSecretRequest{}); domain.KindOf(err) != tc.kind {
				t.Errorf("%s: muốn %v, nhận %v", name, tc.kind, err)
			}
		}
		if got := f.stored(t, foreign.ID); got.RetiringSecret != "" || got.Secret != foreign.Secret {
			t.Fatal("client của project khác không được bị đổi")
		}
	})
}

func TestRotateSecretRejectsAClientWithoutASecret(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.adminFixture()
		c := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "Công khai", Meta: &fhir.Meta{Project: f.project.ID}})
		_, err := f.svc.AdminRotateClientSecret(context.Background(), f.caller, f.project.ID, c.ID, application.RotateSecretRequest{})
		if domain.KindOf(err) != domain.KindBadRequest || !strings.Contains(err.Error(), "no secret") {
			t.Fatalf("muốn 400 no secret: %v", err)
		}
		if got := f.stored(t, c.ID); got.Secret != "" {
			t.Fatalf("không được tạo secret cho client công khai: %q", got.Secret)
		}
	})
}

// Log kiểm toán ghi việc xoay nhưng không bao giờ ghi giá trị secret (cũ hay mới).
func TestRotateSecretAuditLogNeverContainsSecrets(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		var buf bytes.Buffer
		e.svc.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		f := e.adminFixture()
		c := f.client(t)
		old := c.Secret
		res, err := f.svc.AdminRotateClientSecret(context.Background(), f.caller, f.project.ID, c.ID, application.RotateSecretRequest{})
		if err != nil {
			t.Fatal(err)
		}
		log := buf.String()
		if !strings.Contains(log, "audit=true") || !strings.Contains(log, c.ID) {
			t.Fatalf("thiếu log kiểm toán: %q", log)
		}
		if strings.Contains(log, old) || strings.Contains(log, res.Secret) {
			t.Fatalf("log lộ secret: %q", log)
		}
	})
}
