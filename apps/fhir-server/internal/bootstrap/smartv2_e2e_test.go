package bootstrap_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callCT như call nhưng đặt Content-Type tùy ý (JSON Patch).
func callCT(t *testing.T, srv *httptest.Server, method, path, token, ctype, body string) resp {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ctype)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, raw}
}

// Mỗi chữ của SMART v2 chỉ cấp đúng thao tác của nó, kiểm bằng JWT thật qua HTTP. Trước khi sửa, một token chỉ có
// `system/Patient.c` (chỉ tạo) sửa và xóa được vì c, u, d bị gộp thành một quyền "ghi".
func TestSMARTv2EachLetterGrantsOnlyItsInteraction(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	admin := tok(t, "A", "fhir.write")
	seed := func() string {
		return want(t, call(t, srv, "POST", "/fhir/R4/Patient", admin, patient("Seed")), 201).str(t, "id")
	}
	with := func(scope string) string { return tok(t, "A", scope) }
	put := func(id string) map[string]any {
		return map[string]any{"resourceType": "Patient", "id": id, "gender": "female", "name": []any{map[string]any{"family": "Moi"}}}
	}
	const jsonPatch = `[{"op":"replace","path":"/gender","value":"female"}]`

	id := seed()
	// tạo (c)
	c := with("system/Patient.c")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", c, patient("Moi")), 201)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, c, nil), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", c, nil), 403)
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, c, put(id)), 403) // lỗ hổng cũ: được phép
	want(t, callCT(t, srv, "PATCH", "/fhir/R4/Patient/"+id, c, "application/json-patch+json", jsonPatch), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, c, nil), 403) // lỗ hổng cũ: được phép
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id+"/_history", c, nil), 403)

	// sửa (u): PUT được; không đọc, không tạo, không xóa, và PATCH cần đọc trước nên bị chặn
	u := with("system/Patient.u")
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, u, put(id)), 200)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", u, patient("Moi")), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, u, nil), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, u, nil), 403)
	want(t, callCT(t, srv, "PATCH", "/fhir/R4/Patient/"+id, u, "application/json-patch+json", jsonPatch), 403)
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient?identifier=x", u, patient("Cond")), 403) // PUT có điều kiện cần tìm kiếm (s)

	// xóa (d)
	d := with("system/Patient.d")
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, d, put(id)), 403)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", d, patient("Moi")), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, d, nil), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, d, nil), 204)

	// tìm kiếm (s): tìm được, nhưng không đọc theo id, không ghi
	id = seed()
	s := with("system/Patient.s")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", s, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, s, nil), 403) // lỗ hổng cũ: được phép
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", s, patient("Moi")), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, s, nil), 403)

	// đọc (r): đọc, vread, lịch sử; không tìm kiếm, không ghi
	r := with("system/Patient.r")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, r, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id+"/_history", r, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", r, nil), 403)
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, r, put(id)), 403)

	// PATCH cần đọc và sửa
	ru := with("system/Patient.ru")
	want(t, callCT(t, srv, "PATCH", "/fhir/R4/Patient/"+id, ru, "application/json-patch+json", jsonPatch), 200)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, ru, nil), 403)

	// dạng v1 giữ nghĩa: read = rs, write = cud
	rd := with("system/Patient.read")
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, rd, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", rd, nil), 200)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", rd, patient("Moi")), 403)
	wr := with("system/Patient.write")
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", wr, patient("Moi")), 201)
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, wr, put(id)), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, wr, nil), 403) // write = cud, không có r

	// scope ở loại khác không cấp quyền ở Patient
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", with("system/Observation.cruds"), patient("Moi")), 403)
}

// Trong batch, từng entry được kiểm theo thao tác của chính nó: token chỉ có `c` tạo được nhưng entry PUT/DELETE bị 403.
func TestSMARTv2PerEntryEnforcementInBatch(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	id := want(t, call(t, srv, "POST", "/fhir/R4/Patient", tok(t, "A", "fhir.write"), patient("Seed")), 201).str(t, "id")
	bundle := map[string]any{"resourceType": "Bundle", "type": "batch", "entry": []any{
		map[string]any{"request": map[string]any{"method": "POST", "url": "Patient"}, "resource": patient("Batch")},
		map[string]any{"request": map[string]any{"method": "PUT", "url": "Patient/" + id}, "resource": map[string]any{"resourceType": "Patient", "id": id, "gender": "female"}},
		map[string]any{"request": map[string]any{"method": "DELETE", "url": "Patient/" + id}},
	}}
	r := want(t, call(t, srv, "POST", "/fhir/R4", tok(t, "A", "system/Patient.c"), bundle), 200)
	for i, wantPrefix := range []string{"201", "403", "403"} {
		if got := r.str(t, "entry", itoa(i), "response", "status"); !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("entry %d: %q, mong %s...: %s", i, got, wantPrefix, r.body)
		}
	}
	// và Patient không bị sửa hay xóa
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, tok(t, "A", "fhir.read"), nil), 200)
}

func itoa(i int) string { return string(rune('0' + i)) }
