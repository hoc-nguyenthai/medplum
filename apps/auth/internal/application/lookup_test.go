package application_test

import (
	"context"
	"testing"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func TestClientApplicationLookup(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cli, err := e.svc.GetClientApplication(ctx, domain.CLIClientID)
		if err != nil || cli.ID != domain.CLIClientID || cli.PkceOptional == nil || !*cli.PkceOptional {
			t.Fatalf("client CLI dựng sẵn: %+v %v", cli, err)
		}
		cli.Name = "đã sửa"
		again, _ := e.svc.GetClientApplication(ctx, domain.CLIClientID)
		if again.Name == "đã sửa" {
			t.Error("client dựng sẵn trả bản sao, sửa không được ảnh hưởng bản gốc")
		}
		p := e.project(nil)
		c := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Name: "App", Meta: &fhir.Meta{Project: p.ID}})
		got, err := e.svc.GetClientApplication(ctx, c.ID)
		if err != nil || got.Name != "App" {
			t.Fatalf("client trong kho: %+v %v", got, err)
		}
		_, err = e.svc.GetClientApplication(ctx, "khong-co")
		wantErr(t, err, domain.KindNotFound, "Not found")
	})
}

func TestResolveProjectID(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		c := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication", Meta: &fhir.Meta{Project: p.ID}})
		noProject := put(e, &fhir.ClientApplication{ResourceType: "ClientApplication"})

		for _, tc := range []struct {
			name, client, project, want, errMsg string
		}{
			{"không client: giữ nguyên projectId", "", "abc", "abc", ""},
			{"không client, không project", "", "", "", ""},
			{"client không gửi projectId", c.ID, "", p.ID, ""},
			{"client và projectId khớp", c.ID, p.ID, p.ID, ""},
			{"projectId lệch với project của client", c.ID, "khac", "", "Invalid projectId"},
			{"client không thuộc project nào, không gửi projectId", noProject.ID, "", "", ""},
			{"client không thuộc project nào, gửi projectId", noProject.ID, "x", "", "Invalid projectId"},
			{"client dựng sẵn không thuộc project", domain.CLIClientID, "", "", ""},
		} {
			got, err := e.svc.ResolveProjectID(ctx, tc.client, tc.project)
			if tc.errMsg != "" {
				wantErr(t, err, domain.KindBadRequest, tc.errMsg)
				continue
			}
			if err != nil || got != tc.want {
				t.Errorf("%s: %q %v", tc.name, got, err)
			}
		}
		_, err := e.svc.ResolveProjectID(ctx, "khong-co", "")
		wantErr(t, err, domain.KindNotFound, "")
	})
}

func TestFindUserByEmail(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		global := e.user("an@example.org", nil)
		inProject := e.user("an@example.org", func(u *fhir.User) { u.Project = &fhir.Reference{Reference: "Project/" + p.ID} })

		cases := []struct {
			name, email, project string
			want                 *fhir.User
		}{
			{"không project: user toàn cục", "an@example.org", "", global},
			{"hạ chữ thường", "AN@Example.ORG", "", global},
			{"có project: user của project", "an@example.org", p.ID, inProject},
			{"project new: coi như không có project", "an@example.org", "new", global},
			{"project khác: rơi về user toàn cục (như Medplum)", "an@example.org", "khac", global},
			{"không tồn tại", "ai@example.org", "", nil},
		}
		for _, c := range cases {
			got, err := e.svc.FindUserByEmail(ctx, c.email, c.project)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if (c.want == nil) != (got == nil) || (got != nil && got.ID != c.want.ID) {
				t.Errorf("%s: %+v", c.name, got)
			}
		}
	})
}

func TestFindUserInScopeDoesNotFallBack(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		global := e.user("an@example.org", nil)
		inProject := e.user("chi-trong-project@example.org", func(u *fhir.User) { u.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		for _, c := range []struct {
			name, email, project string
			want                 *fhir.User
		}{
			{"toàn cục", "AN@example.org", "", global},
			{"project new coi như toàn cục", "an@example.org", "new", global},
			{"có project: không rơi về toàn cục", "an@example.org", p.ID, nil},
			{"user của project", "chi-trong-project@example.org", p.ID, inProject},
			{"user của project không thấy ở phạm vi toàn cục", "chi-trong-project@example.org", "", nil},
		} {
			got, err := e.svc.FindUserInScope(ctx, c.email, c.project)
			if err != nil || (c.want == nil) != (got == nil) || (got != nil && got.ID != c.want.ID) {
				t.Errorf("%s: %+v %v", c.name, got, err)
			}
		}
	})
}

func TestLoginProject(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.project(nil)
		u := e.user("a@example.org", nil)
		withProject := e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + p.ID} })
		if got := e.svc.LoginProject(ctx, withProject); got == nil || got.ID != p.ID {
			t.Errorf("%+v", got)
		}
		for name, l := range map[string]*fhir.Login{
			"không có project":       e.login(u, nil),
			"Project/new":            e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/new"} }),
			"project không đọc được": e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/khong-co"} }),
		} {
			if got := e.svc.LoginProject(ctx, l); got != nil {
				t.Errorf("%s: phải nil, được %+v", name, got)
			}
		}
	})
}

func TestMembershipsForLogin(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		active := e.project(nil)
		inactive := e.project(func(p *fhir.Project) { p.Active = ptr(false) })
		u := e.user("a@example.org", nil)
		mPrac := e.membership(u, active, nil)
		mPat := e.membership(u, e.project(nil), func(m *fhir.ProjectMembership) { m.Profile = &fhir.Reference{Reference: "Patient/pt1"} })
		e.membership(u, active, func(m *fhir.ProjectMembership) { m.Active = ptr(false) }) // membership không hoạt động: không liệt kê
		e.membership(e.user("khac@example.org", nil), active, nil)                         // user khác

		ids := func(ms []fhir.ProjectMembership) map[string]bool {
			out := map[string]bool{}
			for _, m := range ms {
				out[m.ID] = true
			}
			return out
		}
		got, err := e.svc.MembershipsForLogin(ctx, e.login(u, nil))
		if err != nil || len(got) != 2 || !ids(got)[mPrac.ID] || !ids(got)[mPat.ID] {
			t.Fatalf("mọi membership hoạt động: %v %v", ids(got), err)
		}
		got, _ = e.svc.MembershipsForLogin(ctx, e.login(u, func(l *fhir.Login) { l.ProfileType = "Patient" }))
		if len(got) != 1 || got[0].ID != mPat.ID {
			t.Errorf("lọc theo profileType: %v", ids(got))
		}
		got, _ = e.svc.MembershipsForLogin(ctx, e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/" + active.ID} }))
		if len(got) != 1 || got[0].ID != mPrac.ID {
			t.Errorf("lọc theo project của login: %v", ids(got))
		}
		got, err = e.svc.MembershipsForLogin(ctx, e.login(u, func(l *fhir.Login) { l.Project = &fhir.Reference{Reference: "Project/new"} }))
		if err != nil || len(got) != 0 {
			t.Errorf("Project/new thì rỗng: %v %v", got, err)
		}
		got, err = e.svc.MembershipsForLogin(ctx, e.login(e.user("none@example.org", nil), nil))
		if err != nil || len(got) != 0 {
			t.Errorf("user chưa có membership: %v %v", got, err)
		}
		_, err = e.svc.MembershipsForLogin(ctx, &fhir.Login{ResourceType: "Login"})
		wantErr(t, err, domain.KindBadRequest, "User reference is missing")

		// mọi project đều không hoạt động thì lỗi; còn project hoạt động thì loại bớt membership của project không hoạt động
		u2 := e.user("b@example.org", nil)
		e.membership(u2, inactive, nil)
		_, err = e.svc.MembershipsForLogin(ctx, e.login(u2, nil))
		wantErr(t, err, domain.KindBadRequest, "Project not active")
		mOK := e.membership(u2, active, nil)
		got, err = e.svc.MembershipsForLogin(ctx, e.login(u2, nil))
		if err != nil || len(got) != 1 || got[0].ID != mOK.ID {
			t.Errorf("loại membership của project không hoạt động: %v %v", ids(got), err)
		}
		// project không đọc được thì vẫn giữ membership (như Medplum)
		u3 := e.user("c@example.org", nil)
		mDangling := put(e, &fhir.ProjectMembership{ResourceType: "ProjectMembership", User: &fhir.Reference{Reference: "User/" + u3.ID},
			Project: &fhir.Reference{Reference: "Project/khong-co"}, Profile: &fhir.Reference{Reference: "Practitioner/p"}})
		got, err = e.svc.MembershipsForLogin(ctx, e.login(u3, nil))
		if err != nil || len(got) != 1 || got[0].ID != mDangling.ID {
			t.Errorf("project không đọc được: %v %v", ids(got), err)
		}
	})
}
