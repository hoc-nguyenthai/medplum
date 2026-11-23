package fhirauth

import (
	"testing"
	"time"
)

func mustPolicy(t *testing.T, v any) *Policy {
	t.Helper()
	p, err := ParsePolicy(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSnapshotRoundTrip(t *testing.T) {
	exp := time.Unix(time.Now().Add(time.Hour).Unix(), 0)
	in := &Principal{Subject: "u", Project: "P", Grants: Grants{"Observation": PermRead | PermSearch}, Expires: exp,
		Policy: mustPolicy(t, []any{map[string]any{"resourceType": "Observation", "criteria": "status=final"}})}
	raw, err := in.MarshalSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Subject != "u" || out.Project != "P" || !out.Expires.Equal(exp) || out.Grants["Observation"] != PermRead|PermSearch {
		t.Fatalf("%+v", out)
	}
	if !out.CanReadResource("Observation", map[string]any{"resourceType": "Observation", "status": "final"}) ||
		out.CanReadResource("Observation", map[string]any{"resourceType": "Observation", "status": "draft"}) ||
		out.CanReadResource("Patient", map[string]any{}) {
		t.Fatal("CanReadResource sai sau khi khôi phục")
	}
	// grants nil (không giới hạn theo loại) phải còn nil sau khứ hồi, không thành "từ chối hết"
	open, _ := (&Principal{Subject: "s"}).MarshalSnapshot()
	o, err := ParseSnapshot(open)
	if err != nil || o.Grants != nil || o.Policy != nil || !o.Expires.IsZero() || !o.CanReadResource("Patient", nil) {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestSnapshotRejectsBadInput(t *testing.T) {
	if _, err := (*Principal)(nil).MarshalSnapshot(); err == nil {
		t.Error("nil Principal phải lỗi")
	}
	for _, raw := range []string{`xx`, `{"v":1,"sub":"u"}`, `{"sub":"u"}`, `{"v":2,"policy":"x"}`} {
		if _, err := ParseSnapshot([]byte(raw)); err == nil {
			t.Errorf("%s phải bị từ chối", raw)
		}
	}
}

func TestCanReadResource(t *testing.T) {
	var nilP *Principal
	if !nilP.CanReadResource("Patient", nil) {
		t.Error("nil Principal: không phân quyền")
	}
	write := &Principal{Grants: Grants{"Observation": PermCreate | PermUpdate | PermDelete | PermSearch}}
	if write.CanReadResource("Observation", nil) {
		t.Error("thiếu chữ r phải bị từ chối dù có chữ s")
	}
	readonlyPolicy := &Principal{Policy: mustPolicy(t, []any{map[string]any{"resourceType": "Observation", "readonly": true}})}
	if !readonlyPolicy.CanReadResource("Observation", nil) {
		t.Error("readonly vẫn được đọc")
	}
	crit := &Principal{Policy: mustPolicy(t, []any{"Observation?status=final"})}
	if crit.CanReadResource("Observation", nil) {
		t.Error("có tiêu chí mà không có nội dung phải bị từ chối")
	}
}

func TestExpired(t *testing.T) {
	now := time.Now()
	if (&Principal{}).Expired(now) || (*Principal)(nil).Expired(now) {
		t.Error("không hạn: không hết hạn")
	}
	p := &Principal{Expires: now}
	if !p.Expired(now) || p.Expired(now.Add(-time.Second)) {
		t.Error("biên hết hạn sai")
	}
}

func TestPolicyWildcardAndDeny(t *testing.T) {
	p := mustPolicy(t, []any{
		map[string]any{"resourceType": "*", "readonly": true},
		map[string]any{"resourceType": "Observation"},                     // rule riêng thay thế "*": ghi được
		map[string]any{"resourceType": "ClientApplication", "deny": true}, // cấm dù có "*"
		map[string]any{"resourceType": "User"},                            // loại quản trị: chỉ có khi có rule riêng
	})
	cases := []struct {
		rt         string
		read, writ bool
	}{
		{"Patient", true, false},            // từ "*", chỉ đọc
		{"Observation", true, true},         // rule riêng
		{"ClientApplication", false, false}, // deny
		{"User", true, true},                // rule riêng cho loại bị loại khỏi "*"
		{"Project", false, false},           // loại quản trị không khớp "*"
		{"Login", false, false},
	}
	for _, c := range cases {
		if got := p.allows(c.rt, false); got != c.read {
			t.Errorf("%s đọc = %v", c.rt, got)
		}
		if got := p.allows(c.rt, true); got != c.writ {
			t.Errorf("%s ghi = %v", c.rt, got)
		}
	}
	types := p.Types()
	has := func(rt string) bool {
		for _, x := range types {
			if x == rt {
				return true
			}
		}
		return false
	}
	if !has("Patient") || !has("User") || has("ClientApplication") || has("Project") {
		t.Errorf("Types sai: %d loại", len(types))
	}
	// khớp tiêu chí vẫn theo loại: rule "*" không có criteria
	if !p.matchDoc("Patient", map[string]any{"resourceType": "Patient"}) || p.matchDoc("ClientApplication", nil) {
		t.Error("matchDoc với wildcard/deny sai")
	}
}

func TestPolicyWildcardRejectsBadForms(t *testing.T) {
	for name, v := range map[string]any{
		"hai wildcard":       []any{map[string]any{"resourceType": "*"}, map[string]any{"resourceType": "*", "readonly": true}},
		"wildcard criteria":  []any{map[string]any{"resourceType": "*", "criteria": "status=final"}},
		"wildcard deny":      []any{map[string]any{"resourceType": "*", "deny": true}},
		"deny không boolean": []any{map[string]any{"resourceType": "Patient", "deny": "yes"}},
		"loại lạ":            []any{map[string]any{"resourceType": "Nope"}},
	} {
		if _, err := ParsePolicy(v); err == nil {
			t.Errorf("%s phải bị từ chối", name)
		}
	}
}
