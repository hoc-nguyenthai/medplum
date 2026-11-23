package application_test

import (
	"context"
	"testing"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestSetLoginScope(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		m := e.membership(u, p, nil)
		ref := &fhir.Reference{Reference: "ProjectMembership/" + m.ID}
		withScope := func(scope string) *fhir.Login {
			return e.login(u, func(l *fhir.Login) { l.Scope = scope; l.Membership = ref })
		}

		// trạng thái không hợp lệ
		_, err := e.svc.SetLoginScope(ctx, e.login(u, func(l *fhir.Login) { l.Revoked = ptr(true); l.Membership = ref }), "openid")
		wantErr(t, err, domain.KindBadRequest, "Login revoked")
		_, err = e.svc.SetLoginScope(ctx, e.login(u, func(l *fhir.Login) { l.Granted = ptr(true); l.Membership = ref }), "openid")
		wantErr(t, err, domain.KindBadRequest, "Login granted")
		_, err = e.svc.SetLoginScope(ctx, e.login(u, nil), "openid")
		wantErr(t, err, domain.KindBadRequest, "Login profile not set")

		type tc struct {
			name, existing, submitted string
			ok                        bool
		}
		for _, c := range []tc{
			{"giữ nguyên", "openid profile", "openid profile", true},
			{"thu hẹp term thường", "openid profile email", "openid email", true},
			{"thêm term thường mới", "openid", "openid profile", false},
			{"bỏ hết scope", "openid profile", "", true},
			{"không thêm SMART mới vào scope không có SMART (đúng như Medplum)", "openid", "openid user/Patient.rs", false},
			{"SMART hẹp hơn", "openid user/*.cruds", "openid user/Patient.rs", true},
			{"SMART rộng hơn", "openid user/Patient.rs", "openid user/Patient.cruds", false},
			{"SMART loại khác", "openid user/Patient.rs", "openid user/Observation.rs", false},
			{"bỏ hết SMART khi đang bị giới hạn", "openid user/Patient.rs", "openid", false},
			{"bỏ hết scope khi đang bị giới hạn", "openid user/Patient.rs", "", false},
			{"system hẹp thành user", "openid system/*.rs", "openid user/Patient.rs", true},
			{"không dùng patient/ ở bước này (không có ngữ cảnh)", "openid user/*.rs", "openid patient/Patient.rs", false},
			{"tham số phải khớp đúng", "openid user/Observation.rs?category=lab", "openid user/Observation.rs", false},
			{"tham số khớp đúng thì được", "openid user/Observation.rs?category=lab", "openid user/Observation.rs?category=lab", true},
		} {
			l := withScope(c.existing)
			out, err := e.svc.SetLoginScope(ctx, l, c.submitted)
			if c.ok {
				if err != nil || out.Scope != c.submitted {
					t.Errorf("%s: %+v %v", c.name, out, err)
				}
				if stored, _ := e.r.Logins.Get(ctx, l.ID); stored.Scope != c.submitted {
					t.Errorf("%s: kho giữ %q", c.name, stored.Scope)
				}
			} else if !domain.Is(err, domain.KindBadRequest, "Invalid scope") {
				t.Errorf("%s: mong Invalid scope, được %v", c.name, err)
			} else if stored, _ := e.r.Logins.Get(ctx, l.ID); stored.Scope != c.existing {
				t.Errorf("%s: lỗi thì không được ghi: %q", c.name, stored.Scope)
			}
		}
	})
}

// Thu hồi xảy ra sau khi đọc login (oauth/utils.ts:739, auth/setpassword.test.ts:404): SetLoginScope vẫn phải giữ revoked.
func TestSetLoginScopeKeepsConcurrentRevocation(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		m := e.membership(u, p, nil)
		stale := e.login(u, func(l *fhir.Login) {
			l.Membership = &fhir.Reference{Reference: "ProjectMembership/" + m.ID}
			l.Scope = "openid profile"
		})
		if err := e.svc.RevokeLogin(ctx, stale); err != nil { // thu hồi SAU khi `stale` được đọc
			t.Fatal(err)
		}
		out, err := e.svc.SetLoginScope(ctx, stale, "openid")
		if err != nil {
			t.Fatal(err)
		}
		if !domain.IsTrue(out.Revoked) {
			t.Error("bản trả về phải giữ revoked")
		}
		if stored, _ := e.r.Logins.Get(ctx, stale.ID); !domain.IsTrue(stored.Revoked) || stored.Scope != "openid" {
			t.Errorf("kho: revoked=%v scope=%q", stored.Revoked, stored.Scope)
		}
	})
}
