package bootstrap_test

import (
	"strings"
	"testing"
)

// Entry trong batch/transaction không được vượt vai trò của yêu cầu ngoài: token `write` gọi thẳng $reindex hay $expunge bị 403
// (cổng HTTP theo đường dẫn), nên cùng thao tác làm entry của Bundle cũng phải bị chặn (trước đây lọt qua vì cổng chỉ xét đường dẫn ngoài).

func bundle(typ string, entries ...map[string]any) map[string]any {
	es := make([]any, len(entries))
	for i, e := range entries {
		es[i] = e
	}
	return map[string]any{"resourceType": "Bundle", "type": typ, "entry": es}
}

func entry(method, url string, resource map[string]any) map[string]any {
	e := map[string]any{"request": map[string]any{"method": method, "url": url}}
	if resource != nil {
		e["resource"] = resource
	}
	return e
}

func TestBatchAndTransactionCannotEscalateRole(t *testing.T) {
	for _, mode := range []string{"static", "jwt"} {
		t.Run(mode, func(t *testing.T) {
			var srv = func() (string, string, string, func(method, path, token string, body any) resp) {
				if mode == "jwt" {
					_, s := newServer(t, jwtSettings())
					call2 := func(m, p, tk string, b any) resp { return call(t, s, m, p, tk, b) }
					return tok(t, "A", "fhir.read"), tok(t, "A", "fhir.write"), tok(t, "A", "fhir.admin"), call2
				}
				_, s := newServer(t, settings(withAdmin))
				call2 := func(m, p, tk string, b any) resp { return call(t, s, m, p, tk, b) }
				return readSecret, writeSecret, adminSecret, call2
			}
			rd, wr, ad, do := srv()
			p := want2(t, do("POST", "/fhir/R4/Patient", wr, patient("Victim")), 201)
			id := p.str(t, "id")

			adminOps := []map[string]any{
				entry("POST", "Patient/$reindex", nil),
				entry("POST", "/Patient/$reindex", nil),
				entry("POST", "Patient/"+id+"/$reindex", nil),
				entry("POST", "Patient/"+id+"/$expunge", nil),
			}
			for _, e := range adminOps {
				url := e["request"].(map[string]any)["url"].(string)
				// transaction: từ chối cả Bundle, không entry nào chạy
				tx := do("POST", "/fhir/R4", wr, bundle("transaction", e))
				if tx.status != 403 {
					t.Errorf("transaction %s bằng token write: %d, muốn 403: %s", url, tx.status, tx.body)
				}
				// batch: entry đó nhận 403, không có tác dụng phụ
				b := want2(t, do("POST", "/fhir/R4", wr, bundle("batch", e)), 200)
				entries := b.json(t)["entry"].([]any)
				got := entries[0].(map[string]any)["response"].(map[string]any)["status"].(string)
				if !strings.HasPrefix(got, "403") {
					t.Errorf("batch %s bằng token write: %q, muốn 403", url, got)
				}
				// token read không ghi được Bundle nói chung
				if r := do("POST", "/fhir/R4", rd, bundle("batch", e)); r.status != 403 {
					t.Errorf("batch bằng token read: %d", r.status)
				}
			}
			// resource vẫn còn (không bị $expunge)
			want2(t, do("GET", "/fhir/R4/Patient/"+id, wr, nil), 200)

			// admin vẫn được làm qua Bundle
			tx := do("POST", "/fhir/R4", ad, bundle("transaction", entry("POST", "Patient/"+id+"/$reindex", nil)))
			if tx.status != 200 {
				t.Errorf("admin transaction $reindex: %d %s", tx.status, tx.body)
			}
			// entry thường của token write vẫn chạy
			ok := want2(t, do("POST", "/fhir/R4", wr, bundle("batch", entry("POST", "Patient", patient("Fine")), entry("GET", "Patient/"+id, nil))), 200)
			for i, e := range ok.json(t)["entry"].([]any) {
				st := e.(map[string]any)["response"].(map[string]any)["status"].(string)
				if !strings.HasPrefix(st, "20") {
					t.Errorf("entry %d hợp lệ bị chặn: %s", i, st)
				}
			}
		})
	}
}

func want2(t *testing.T, r resp, status int) resp { t.Helper(); return want(t, r, status) }
