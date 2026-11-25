package bootstrap_test

import (
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"

	"trivita-fhir/apps/fhir-server/internal/config"
)

func quotaSettings(user, project int, more ...func(*config.Settings)) config.Settings {
	return jwtSettings(append([]func(*config.Settings){func(s *config.Settings) { s.QuotaUser, s.QuotaProject = user, project }}, more...)...)
}

func sub(name string) func(jwt.MapClaims) { return func(c jwt.MapClaims) { c["sub"] = name } }

// params đọc Parameters của $rate-limits thành map tên -> số (hoặc bool).
func params(t *testing.T, r resp) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, p := range r.json(t)["parameter"].([]any) {
		m := p.(map[string]any)
		if v, ok := m["valueInteger"]; ok {
			out[m["name"].(string)] = int(v.(float64))
		} else {
			out[m["name"].(string)] = m["valueBoolean"]
		}
	}
	return out
}

func TestQuotaPerUserCostsPointsAndReturns429(t *testing.T) {
	_, srv := newServer(t, quotaSettings(10, 0))
	a := tok(t, "A", "fhir.write")
	r := want(t, call(t, srv, "GET", "/fhir/R4/Patient", a, nil), 200) // tìm kiếm: 5 điểm
	if r.header.Get("RateLimit-Limit") != "10" || r.header.Get("RateLimit-Remaining") != "5" {
		t.Fatalf("header sau 5 điểm: %v", r.header)
	}
	if reset, _ := strconv.Atoi(r.header.Get("RateLimit-Reset")); reset < 1 || reset > 60 {
		t.Fatalf("RateLimit-Reset: %q", r.header.Get("RateLimit-Reset"))
	}
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", a, patient("X")), 201) // ghi: 2 điểm, còn 3
	r = want(t, call(t, srv, "GET", "/fhir/R4/Patient/nope", a, nil), 404)  // đọc: 1 điểm kể cả 404, còn 2
	if r.header.Get("RateLimit-Remaining") != "2" {
		t.Fatalf("remaining: %v", r.header)
	}
	r = want(t, call(t, srv, "GET", "/fhir/R4/Patient", a, nil), 429) // 5 điểm > còn 2
	if r.header.Get("Retry-After") == "" || r.header.Get("RateLimit-Remaining") != "2" || r.json(t)["resourceType"] != "OperationOutcome" {
		t.Fatalf("429: %v %s", r.header, r.body)
	}
	// metadata không tính điểm và không có header
	if r := want(t, call(t, srv, "GET", "/fhir/R4/metadata", "", nil), 200); r.header.Get("RateLimit-Limit") != "" {
		t.Errorf("metadata có header: %v", r.header)
	}
	// người khác cùng project có điểm riêng; project khác độc lập
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "A", "fhir.write", sub("khac@A")), nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "B", "fhir.write", sub("user@A")), nil), 200)
}

func TestQuotaPerProjectSharedByUsers(t *testing.T) {
	_, srv := newServer(t, quotaSettings(0, 10))
	u1, u2, u3 := tok(t, "A", "fhir.read", sub("u1")), tok(t, "A", "fhir.read", sub("u2")), tok(t, "A", "fhir.read", sub("u3"))
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", u1, nil), 200)
	r := want(t, call(t, srv, "GET", "/fhir/R4/Patient", u2, nil), 200)
	if r.header.Get("RateLimit-Limit") != "10" || r.header.Get("RateLimit-Remaining") != "0" {
		t.Fatalf("project: %v", r.header)
	}
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", u3, nil), 429)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "B", "fhir.read", sub("u1")), nil), 200)
}

func TestQuotaHeaderShowsTheTighterLimit(t *testing.T) {
	_, srv := newServer(t, quotaSettings(100, 10))
	r := want(t, call(t, srv, "GET", "/fhir/R4/Patient", tok(t, "A", "fhir.read"), nil), 200)
	if r.header.Get("RateLimit-Limit") != "10" || r.header.Get("RateLimit-Remaining") != "5" {
		t.Fatalf("phải báo hạn mức chặt hơn (project): %v", r.header)
	}
}

func TestQuotaOffByDefault(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	a := tok(t, "A", "fhir.read")
	for i := 0; i < 30; i++ {
		if r := call(t, srv, "GET", "/fhir/R4/Patient", a, nil); r.status != 200 || r.header.Get("RateLimit-Limit") != "" {
			t.Fatalf("lần %d: %d %v", i, r.status, r.header)
		}
	}
}

func TestQuotaSharedAcrossReplicas(t *testing.T) {
	mr := miniredis.RunT(t)
	shared := func(s *config.Settings) { s.RateLimitShared, s.RedisURL = true, "redis://"+mr.Addr()+"/0" }
	_, a := newServer(t, quotaSettings(10, 0, shared))
	_, b := newServer(t, quotaSettings(10, 0, shared))
	u := tok(t, "A", "fhir.read")
	want(t, call(t, a, "GET", "/fhir/R4/Patient", u, nil), 200)
	want(t, call(t, b, "GET", "/fhir/R4/Patient", u, nil), 200)
	want(t, call(t, a, "GET", "/fhir/R4/Patient", u, nil), 429)
	want(t, call(t, b, "GET", "/fhir/R4/Patient", u, nil), 429)
}

func TestRateLimitsOperationReportsWithoutConsuming(t *testing.T) {
	_, srv := newServer(t, quotaSettings(20, 50, func(s *config.Settings) { s.QuotaWindow = 120e9 }))
	ad := tok(t, "A", "fhir.admin")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", ad, nil), 200) // 5 điểm
	for i := 0; i < 3; i++ {                                       // đọc tình trạng nhiều lần không trừ điểm
		r := want(t, call(t, srv, "GET", "/fhir/R4/Project/A/$rate-limits", ad, nil), 200)
		p := params(t, r)
		if p["enabled"] != true || p["windowSeconds"] != 120 || p["userLimit"] != 20 || p["userRemaining"] != 15 || p["projectLimit"] != 50 || p["projectRemaining"] != 45 {
			t.Fatalf("lần %d: %v", i, p)
		}
		if reset := p["userResetSeconds"].(int); reset < 1 || reset > 120 {
			t.Fatalf("reset: %v", p)
		}
		if r.header.Get("Cache-Control") != "no-store" || r.header.Get("RateLimit-Limit") != "" {
			t.Fatalf("header: %v", r.header)
		}
	}
	// chỉ admin của đúng project
	want(t, call(t, srv, "GET", "/fhir/R4/Project/A/$rate-limits", tok(t, "A", "fhir.write"), nil), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Project/A/$rate-limits", tok(t, "B", "fhir.admin"), nil), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Project/A/$rate-limits", "", nil), 401)
}

func TestRateLimitsOperationWhenQuotaDisabled(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	r := want(t, call(t, srv, "GET", "/fhir/R4/Project/A/$rate-limits", tok(t, "A", "fhir.admin"), nil), 200)
	if p := params(t, r); p["enabled"] != false || len(p) != 1 {
		t.Fatalf("tắt: %v", p)
	}
}
