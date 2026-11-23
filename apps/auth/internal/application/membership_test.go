package application_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestSetLoginMembershipHappyPath(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		m := e.membership(u, p, nil)
		l := e.login(u, func(l *fhir.Login) { l.RefreshSecret = "bí-mật" })
		out, err := e.svc.SetLoginMembership(ctx, l, m)
		if err != nil {
			t.Fatal(err)
		}
		if out.Membership == nil || out.Membership.Reference != "ProjectMembership/"+m.ID {
			t.Errorf("membership: %+v", out.Membership)
		}
		if out.Project == nil || out.Project.Reference != "Project/"+p.ID || out.Project.Display != "Dự án" {
			t.Errorf("project phải có display = tên project: %+v", out.Project)
		}
		if out.RefreshSecret != "bí-mật" {
			t.Error("project thường giữ refreshSecret")
		}
		stored, _ := e.r.Logins.Get(ctx, l.ID)
		if stored.Membership == nil {
			t.Error("phải được ghi vào kho")
		}
		// gọi lần hai trên login đã có membership
		_, err = e.svc.SetLoginMembership(ctx, out, m)
		wantErr(t, err, domain.KindBadRequest, "Login profile already set")
	})
}

func TestSetLoginMembershipValidation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		other := e.user("b@example.org", nil)
		m := e.membership(u, p, nil)

		revoked := e.login(u, func(l *fhir.Login) { l.Revoked = ptr(true) })
		_, err := e.svc.SetLoginMembership(ctx, revoked, m)
		wantErr(t, err, domain.KindBadRequest, "Login revoked")

		granted := e.login(u, func(l *fhir.Login) { l.Granted = ptr(true) })
		_, err = e.svc.SetLoginMembership(ctx, granted, m)
		wantErr(t, err, domain.KindBadRequest, "Login granted")

		_, err = e.svc.SetLoginMembership(ctx, e.login(other, nil), m)
		wantErr(t, err, domain.KindBadRequest, "Invalid profile")

		// login của một project cụ thể chỉ hoàn tất bằng membership của project đó
		p2 := e.project(nil)
		forP2 := e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + p2.ID} })
		_, err = e.svc.SetLoginMembership(ctx, forP2, m)
		wantErr(t, err, domain.KindBadRequest, "Invalid profile")
		forNew := e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/new"} })
		if _, err = e.svc.SetLoginMembership(ctx, forNew, m); err != nil {
			t.Errorf("Project/new được chọn membership bất kỳ: %v", err)
		}

		// membership thiếu project, hoặc project không tồn tại
		_, err = e.svc.SetLoginMembership(ctx, e.login(u, nil), &fhir.ProjectMembership{User: m.User})
		wantErr(t, err, domain.KindNotFound, "Not found")
		ghost := &fhir.ProjectMembership{User: m.User, Project: &fhir.Reference{Reference: "Project/khong-co"}}
		_, err = e.svc.SetLoginMembership(ctx, e.login(u, nil), ghost)
		wantErr(t, err, domain.KindNotFound, "Not found")

		// không hoạt động
		inactiveM := e.membership(u, p, func(m *fhir.ProjectMembership) { m.Active = ptr(false) })
		_, err = e.svc.SetLoginMembership(ctx, e.login(u, nil), inactiveM)
		wantErr(t, err, domain.KindBadRequest, "Profile not active")
		inactiveP := e.project(func(p *fhir.Project) { p.Active = ptr(false) })
		_, err = e.svc.SetLoginMembership(ctx, e.login(u, nil), e.membership(u, inactiveP, nil))
		wantErr(t, err, domain.KindBadRequest, "Project not active")

		// project bắt buộc đăng nhập Google
		g := e.project(func(p *fhir.Project) { p.Features = []string{"google-auth-required"} })
		gm := e.membership(u, g, nil)
		_, err = e.svc.SetLoginMembership(ctx, e.login(u, nil), gm)
		wantErr(t, err, domain.KindBadRequest, "Google authentication is required")
		if _, err = e.svc.SetLoginMembership(ctx, e.login(u, func(l *fhir.Login) { l.AuthMethod = "google" }), gm); err != nil {
			t.Errorf("đăng nhập Google được phép: %v", err)
		}
	})
}

func TestSetLoginMembershipIPRules(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		block := e.policy(p, fhir.AccessPolicyIpAccessRule{Value: "10.0.0.", Action: "block"})
		allowOnly := e.policy(p, fhir.AccessPolicyIpAccessRule{Value: "1.2.3.4", Action: "allow"}, fhir.AccessPolicyIpAccessRule{Value: "*", Action: "block"})
		viaAccess := e.policy(p, fhir.AccessPolicyIpAccessRule{Value: "9.9.9.9", Action: "block"})

		try := func(m *fhir.ProjectMembership, addr string) error {
			_, err := e.svc.SetLoginMembership(ctx, e.login(u, func(l *fhir.Login) { l.RemoteAddress = addr }), m)
			return err
		}
		mBlock := e.membership(u, p, func(m *fhir.ProjectMembership) {
			m.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + block.ID}
		})
		wantErr(t, try(mBlock, "10.0.0.7"), domain.KindBadRequest, domain.IPNotAllowed)
		if err := try(mBlock, "10.0.1.7"); err != nil {
			t.Errorf("địa chỉ không khớp rule block: %v", err)
		}
		if err := try(mBlock, ""); err != nil {
			t.Errorf("không có remoteAddress thì bỏ qua rule: %v", err)
		}
		mAllow := e.membership(u, p, func(m *fhir.ProjectMembership) {
			m.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + allowOnly.ID}
		})
		if err := try(mAllow, "1.2.3.4"); err != nil {
			t.Errorf("allow đứng trước: %v", err)
		}
		wantErr(t, try(mAllow, "5.6.7.8"), domain.KindBadRequest, domain.IPNotAllowed)
		// rule nằm ở access[].policy cũng được áp dụng
		mAccess := e.membership(u, p, func(m *fhir.ProjectMembership) {
			m.Access = []fhir.ProjectMembershipAccess{{Policy: &fhir.Reference{Reference: "AccessPolicy/" + viaAccess.ID}}}
		})
		wantErr(t, try(mAccess, "9.9.9.9"), domain.KindBadRequest, domain.IPNotAllowed)
		// membership không có policy: không có rule
		if err := try(e.membership(u, p, nil), "10.0.0.7"); err != nil {
			t.Error(err)
		}
	})
}

func TestSetLoginMembershipInvalidAccessPolicy(t *testing.T) {
	const msg = "Cannot authenticate: Invalid access policy configuration. Please contact your administrator to update your project membership."
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		otherProject := e.project(nil)
		foreign := e.policy(otherProject) // policy thuộc project khác
		noMeta := put(e, &fhir.AccessPolicy{ResourceType: "AccessPolicy", Name: "no meta"})

		for name, ref := range map[string]string{
			"policy không tồn tại":    "AccessPolicy/khong-co",
			"policy của project khác": "AccessPolicy/" + foreign.ID,
		} {
			m := e.membership(u, p, func(m *fhir.ProjectMembership) { m.AccessPolicy = &fhir.Reference{Reference: ref} })
			_, err := e.svc.SetLoginMembership(ctx, e.login(u, nil), m)
			if !domain.Is(err, domain.KindBadRequest, msg) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// policy không ghi project thì chấp nhận (dữ liệu cũ)
		m := e.membership(u, p, func(m *fhir.ProjectMembership) {
			m.AccessPolicy = &fhir.Reference{Reference: "AccessPolicy/" + noMeta.ID}
		})
		if _, err := e.svc.SetLoginMembership(ctx, e.login(u, nil), m); err != nil {
			t.Errorf("policy không có meta.project: %v", err)
		}
	})
}

func TestSetLoginMembershipSMART(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		superP := e.project(func(p *fhir.Project) { p.SuperAdmin = ptr(true) })
		u := e.user("a@example.org", nil)
		prac := e.membership(u, p, nil) // profile Practitioner: không có ngữ cảnh patient
		pat := e.membership(u, p, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Patient/pt-1"} })
		badPat := e.membership(u, p, func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Patient/a,Patient/b"} })
		launchPat := put(e, &fhir.SmartAppLaunch{ResourceType: "SmartAppLaunch", Patient: &fhir.Reference{Reference: "Patient/from-launch"}})

		withScope := func(scope string, launch *fhir.SmartAppLaunch) *fhir.Login {
			return e.login(u, func(l *fhir.Login) {
				l.Scope = scope
				if launch != nil {
					l.Launch = &fhir.Reference{Reference: "SmartAppLaunch/" + launch.ID}
				}
			})
		}
		// patient/ cần ngữ cảnh patient
		_, err := e.svc.SetLoginMembership(ctx, withScope("openid patient/Observation.rs", nil), prac)
		wantErr(t, err, domain.KindBadRequest, "Missing patient context for patient/ scope")
		if _, err = e.svc.SetLoginMembership(ctx, withScope("openid patient/Observation.rs", nil), pat); err != nil {
			t.Errorf("profile Patient là ngữ cảnh: %v", err)
		}
		if _, err = e.svc.SetLoginMembership(ctx, withScope("openid patient/Observation.rs", launchPat), prac); err != nil {
			t.Errorf("launch.patient là ngữ cảnh: %v", err)
		}
		// tham chiếu không phải dạng literal không được dùng làm ngữ cảnh (chống chèn cú pháp search)
		_, err = e.svc.SetLoginMembership(ctx, withScope("openid patient/Observation.rs", nil), badPat)
		wantErr(t, err, domain.KindBadRequest, "Missing patient context for patient/ scope")
		// scope user/ không cần ngữ cảnh
		if _, err = e.svc.SetLoginMembership(ctx, withScope("openid user/Observation.rs", nil), prac); err != nil {
			t.Errorf("user/ không cần ngữ cảnh: %v", err)
		}
		// launch trỏ tới resource không tồn tại: lỗi lan ra
		ghost := &fhir.SmartAppLaunch{ID: "khong-co"}
		_, err = e.svc.SetLoginMembership(ctx, withScope("openid", ghost), prac)
		wantErr(t, err, domain.KindNotFound, "")

		// super admin không dùng được SMART scope hay launch
		sm := e.membership(u, superP, nil)
		_, err = e.svc.SetLoginMembership(ctx, withScope("openid user/*.rs", nil), sm)
		wantErr(t, err, domain.KindBadRequest, "SMART scopes are not supported for super admin logins")
		_, err = e.svc.SetLoginMembership(ctx, withScope("openid", launchPat), sm)
		wantErr(t, err, domain.KindBadRequest, "SMART scopes are not supported for super admin logins")
		// ... và không có refresh token
		out, err := e.svc.SetLoginMembership(ctx, e.login(u, func(l *fhir.Login) { l.RefreshSecret = "bí-mật" }), sm)
		if err != nil || out.RefreshSecret != "" {
			t.Errorf("super admin phải bị xóa refreshSecret: %+v %v", out, err)
		}
	})
}

// Nhiều request đồng thời chọn profile cho cùng một login: đúng một cái thắng (so-sánh-và-đổi).
func TestSetLoginMembershipConcurrent(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		m := e.membership(u, p, nil)
		l := e.login(u, nil)
		const n = 8
		var ok, already atomic.Int32
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.svc.SetLoginMembership(ctx, l, m) // cùng một bản login đã đọc
				switch {
				case err == nil:
					ok.Add(1)
				case domain.Is(err, domain.KindBadRequest, "Login profile already set"):
					already.Add(1)
				default:
					t.Errorf("lỗi lạ: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 || already.Load() != n-1 {
			t.Errorf("thắng=%d, thua=%d", ok.Load(), already.Load())
		}
	})
}
