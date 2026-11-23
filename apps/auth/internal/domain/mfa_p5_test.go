package domain

import (
	"errors"
	"testing"
	"time"

	"trivita-fhir/pkg/fhir"
)

func TestParseMFAMethod(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{{"email", MFAEmail}, {"totp", MFATotp}, {"EMAIL", MFATotp}, {"", MFATotp}, {"sms", MFATotp}, {nil, MFATotp}, {5, MFATotp}, {true, MFATotp}, {[]string{"email"}, MFATotp}} {
		if got := ParseMFAMethod(tc.in); got != tc.want {
			t.Errorf("ParseMFAMethod(%v) = %q, mong %q", tc.in, got, tc.want)
		}
	}
}

func TestAddMFAMethodKeepsOrderAndDoesNotAliasInput(t *testing.T) {
	u := &fhir.User{MfaMethod: []string{"totp"}}
	got := AddMFAMethod(u, MFAEmail)
	if len(got) != 2 || got[0] != "totp" || got[1] != "email" {
		t.Fatalf("%v", got)
	}
	if len(u.MfaMethod) != 1 {
		t.Error("không được sửa slice đầu vào")
	}
	if again := AddMFAMethod(&fhir.User{MfaMethod: got}, MFAEmail); len(again) != 2 {
		t.Errorf("thêm trùng không nhân đôi: %v", again)
	}
	// tương thích ngược: mfaEnrolled mà không có danh sách là totp
	if legacy := AddMFAMethod(&fhir.User{MfaEnrolled: ptr(true)}, MFAEmail); len(legacy) != 2 || legacy[0] != "totp" {
		t.Errorf("legacy: %v", legacy)
	}
	if fresh := AddMFAMethod(&fhir.User{}, MFATotp); len(fresh) != 1 || fresh[0] != "totp" {
		t.Errorf("fresh: %v", fresh)
	}
}

func TestHasMFAMethodAndAllowed(t *testing.T) {
	u := &fhir.User{MfaMethod: []string{"email"}}
	if !HasMFAMethod(u, MFAEmail) || HasMFAMethod(u, MFATotp) || HasMFAMethod(nil, MFATotp) {
		t.Error("HasMFAMethod")
	}
	if !HasMFAMethod(&fhir.User{MfaEnrolled: ptr(true)}, MFATotp) {
		t.Error("legacy coi là totp")
	}
	both := &fhir.Project{Setting: []fhir.ProjectSetting{{Name: "allowedMfaMethods", ValueString: " totp , email,sms "}}}
	if !MFAMethodAllowed(both, MFAEmail) || !MFAMethodAllowed(both, MFATotp) {
		t.Error("project cho cả hai")
	}
	if MFAMethodAllowed(nil, MFAEmail) || !MFAMethodAllowed(nil, MFATotp) {
		t.Error("không có project: chỉ totp")
	}
	if MFAMethodAllowed(&fhir.Project{Setting: []fhir.ProjectSetting{{Name: "allowedMfaMethods", ValueString: "sms"}}}, MFAEmail) {
		t.Error("giá trị không hợp lệ rơi về totp")
	}
}

func TestAssertMFALoginActiveTable(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	fresh := func(mut func(*fhir.Login)) *fhir.Login {
		l := &fhir.Login{AuthMethod: AuthPassword, AuthTime: now.Add(-time.Minute).Format(time.RFC3339Nano)}
		if mut != nil {
			mut(l)
		}
		return l
	}
	for name, tc := range map[string]struct {
		l    *fhir.Login
		want string
	}{
		"ổn":                         {fresh(nil), ""},
		"revoked":                    {fresh(func(l *fhir.Login) { l.Revoked = ptr(true) }), "Login revoked"},
		"granted":                    {fresh(func(l *fhir.Login) { l.Granted = ptr(true) }), "Login granted"},
		"đã xác minh":                {fresh(func(l *fhir.Login) { l.MfaVerified = ptr(true) }), "Login already verified"},
		"google":                     {fresh(func(l *fhir.Login) { l.AuthMethod = AuthGoogle }), "Invalid login authentication method"},
		"client":                     {fresh(func(l *fhir.Login) { l.AuthMethod = AuthClient }), "Invalid login authentication method"},
		"không có phương thức":       {fresh(func(l *fhir.Login) { l.AuthMethod = "" }), "Invalid login authentication method"},
		"đúng 10 phút là hết hạn":    {fresh(func(l *fhir.Login) { l.AuthTime = now.Add(-10 * time.Minute).Format(time.RFC3339Nano) }), "Login expired"},
		"9 phút 59 giây còn hạn":     {fresh(func(l *fhir.Login) { l.AuthTime = now.Add(-(10*time.Minute - time.Second)).Format(time.RFC3339Nano) }), ""},
		"authTime hỏng":              {fresh(func(l *fhir.Login) { l.AuthTime = "không phải ngày" }), "Login expired"},
		"authTime rỗng":              {fresh(func(l *fhir.Login) { l.AuthTime = "" }), "Login expired"},
		"authTime ở tương lai là ổn": {fresh(func(l *fhir.Login) { l.AuthTime = now.Add(time.Hour).Format(time.RFC3339Nano) }), ""},
		"thứ tự: revoked trước mọi lỗi": {fresh(func(l *fhir.Login) {
			l.Revoked, l.Granted, l.MfaVerified, l.AuthMethod, l.AuthTime = ptr(true), ptr(true), ptr(true), "google", ""
		}), "Login revoked"},
		"thứ tự: granted trước verified":     {fresh(func(l *fhir.Login) { l.Granted, l.MfaVerified = ptr(true), ptr(true) }), "Login granted"},
		"thứ tự: verified trước phương thức": {fresh(func(l *fhir.Login) { l.MfaVerified, l.AuthMethod = ptr(true), "google" }), "Login already verified"},
		"thứ tự: phương thức trước hạn":      {fresh(func(l *fhir.Login) { l.AuthMethod, l.AuthTime = "google", "" }), "Invalid login authentication method"},
	} {
		err := AssertMFALoginActive(tc.l, now, 10*time.Minute)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if !Is(err, KindBadRequest, tc.want) {
			t.Errorf("%s: %v, mong %q", name, err, tc.want)
		}
	}
	if err := AssertMFALoginActive(fresh(func(l *fhir.Login) { l.AuthTime = now.Add(-3 * time.Minute).Format(time.RFC3339Nano) }), now, 2*time.Minute); !Is(err, KindBadRequest, "Login expired") {
		t.Errorf("hạn cấu hình được: %v", err)
	}
}

func TestMFAAppNameAndIssuer(t *testing.T) {
	p := &fhir.Project{Setting: []fhir.ProjectSetting{{Name: "appName", ValueString: "  Acme: Health  "}}}
	if got := MFAAppName(p, "Trivita"); got != "Acme: Health" {
		t.Errorf("appName: %q", got)
	}
	if got := MFAIssuer(p, "Trivita"); got != "Acme Health" {
		t.Errorf("issuer bỏ dấu ':': %q", got)
	}
	for _, proj := range []*fhir.Project{nil, {}, {Setting: []fhir.ProjectSetting{{Name: "appName", ValueString: "   "}}}} {
		if got := MFAAppName(proj, "Trivita"); got != "Trivita" {
			t.Errorf("mặc định: %q", got)
		}
	}
	if got := MFAIssuer(nil, "Tri:vita"); got != "Trivita" {
		t.Errorf("issuer mặc định cũng bỏ ':': %q", got)
	}
}

func TestTooManyRequestsError(t *testing.T) {
	err := TooManyRequests(1500 * time.Millisecond)
	var de *Error
	if !errors.As(err, &de) || de.Kind != KindTooManyRequests || de.RetryAfter != 1500*time.Millisecond || KindOf(err) != KindTooManyRequests {
		t.Fatalf("%+v", de)
	}
}
