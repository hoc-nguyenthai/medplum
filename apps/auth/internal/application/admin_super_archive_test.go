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

func TestSuperArchiveAndRestoreProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		var buf bytes.Buffer
		e.svc.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		f := e.superFixture()
		ctx := context.Background()
		target := e.project(nil)
		active := func() *bool { p, _ := e.svc.Projects.Get(ctx, target.ID); return p.Active }
		if err := e.svc.SuperSetProjectActive(ctx, f.caller, target.ID, false); err != nil {
			t.Fatal(err)
		}
		if a := active(); a == nil || *a {
			t.Fatalf("archive: %v", a)
		}
		if reason := domain.InactiveReason(mustProject(t, e, target.ID), nil); reason != domain.ProjectNotActive {
			t.Fatalf("project phải không hoạt động: %q", reason)
		}
		if log := buf.String(); !strings.Contains(log, "audit=true") || !strings.Contains(log, target.ID) {
			t.Fatalf("thiếu log kiểm toán: %q", log)
		}
		// lặp lại: không đổi gì, không log thêm
		n := strings.Count(buf.String(), "audit=true")
		if err := e.svc.SuperSetProjectActive(ctx, f.caller, target.ID, false); err != nil || strings.Count(buf.String(), "audit=true") != n {
			t.Fatalf("lặp archive: %v", err)
		}
		if err := e.svc.SuperSetProjectActive(ctx, f.caller, target.ID, true); err != nil {
			t.Fatal(err)
		}
		if a := active(); a == nil || !*a {
			t.Fatalf("restore: %v", a)
		}
		n = strings.Count(buf.String(), "audit=true")
		if err := e.svc.SuperSetProjectActive(ctx, f.caller, target.ID, true); err != nil || strings.Count(buf.String(), "audit=true") != n {
			t.Fatalf("lặp restore: %v", err)
		}
		// project chưa từng đặt active: restore là không làm gì, không ghi active
		fresh := e.project(nil)
		if err := e.svc.SuperSetProjectActive(ctx, f.caller, fresh.ID, true); err != nil {
			t.Fatal(err)
		}
		if p := mustProject(t, e, fresh.ID); p.Active != nil {
			t.Fatalf("không được ghi active: %v", *p.Active)
		}
	})
}

func TestSuperArchiveProjectRejects(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		f := e.superFixture()
		ctx := context.Background()
		wantErr(t, e.svc.SuperSetProjectActive(ctx, f.caller, f.pro.ID, false), domain.KindBadRequest, "Cannot archive the project of the caller")
		if p := mustProject(t, e, f.pro.ID); p.Active != nil {
			t.Fatal("project của người gọi không được đổi")
		}
		// restore project của chính mình thì được (không đổi gì)
		if err := e.svc.SuperSetProjectActive(ctx, f.caller, f.pro.ID, true); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.svc.SuperSetProjectActive(ctx, f.caller, "khong-co", false), domain.KindNotFound, "")
		wantErr(t, e.svc.SuperSetProjectActive(ctx, f.caller, " ", false), domain.KindBadRequest, "projectId is required")
		adm := e.adminFixture()
		wantErr(t, e.svc.SuperSetProjectActive(ctx, adm.caller, f.pro.ID, false), domain.KindForbidden, "")
		wantErr(t, e.svc.SuperSetProjectActive(ctx, &application.AuthState{}, f.pro.ID, false), domain.KindUnauthorized, "")
	})
}

func mustProject(t *testing.T, e *env, id string) *fhir.Project {
	t.Helper()
	p, err := e.svc.Projects.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
